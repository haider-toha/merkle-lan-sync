// Package integration runs two in-process msync engines over loopback TLS to verify
// the WS-4 system invariants end-to-end: convergence, conflict no-loss, deletion +
// anti-resurrection, and bidirectional back-pressure (plan WS-4 #1/#2/#6/#11). Per
// CDD-8 every assertion is quiesce-then-compare (the equal-root oracle holds AT
// quiescence, SR-5).
package integration

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haider-toha/merkle-sync/internal/protocol"
	"github.com/haider-toha/merkle-sync/internal/reconcile"
	"github.com/haider-toha/merkle-sync/internal/transport"
)

// Convergence budgets, sized as UPPER BOUNDS for loaded/shared CI runners (the
// windows-latest runner is slow + heavily shared). waitConverged/waitRootChanged poll
// and return as soon as the condition holds, so the common case stays sub-second; the
// headroom only absorbs scheduler starvation under load, and a genuine hang still fails
// (just later). See docs/audit/findings/review/phase6-convergence-timeout-flake.md and
// docs/audit/decisions/phase6/convergence-timeout-deflake.md.
const (
	budgetAuthor   = 15 * time.Second // local authorship detected via the rescan
	budgetConverge = 30 * time.Second // small-file two-node convergence
	budgetLarge    = 60 * time.Second // multi-MiB transfers under load
)

// node is one in-process engine + its transport, listening on a loopback port. id and
// snapPath are retained so a node can be STOPPED and RESTARTED over the same folder,
// device identity, and snapshot file (the MK-6 deletion-across-restart scenarios).
type node struct {
	dir      string
	id       *transport.Identity
	tp       *transport.Transport
	eng      *reconcile.Engine
	addr     string
	done     chan struct{}
	cancel   context.CancelFunc
	snapPath string
}

// nodeOpt tweaks a node's reconcile.Config before construction (used by the large-file
// scenarios to relax the deliberately-aggressive defaults — see the de-flake decision).
type nodeOpt func(*reconcile.Config)

// withRescan overrides the rescan interval. The large-file tests relax the 40ms default
// to ~1s: their files are static after startup, so re-hashing MiB files on the single
// engine loop 25×/s is pure churn that competes with chunk-response routing.
func withRescan(d time.Duration) nodeOpt { return func(c *reconcile.Config) { c.RescanInterval = d } }

// withRequestTimeout overrides the per-chunk request timeout (larger ⇒ a starved loop
// does not spuriously time out and restart a whole file).
func withRequestTimeout(d time.Duration) nodeOpt {
	return func(c *reconcile.Config) { c.RequestTimeout = d }
}

// startNode builds an engine over syncDir (snapshot kept OUTSIDE syncDir so it is
// never scanned), starts it, and begins listening on a loopback port. The watcher is
// disabled and the rescan interval is short, so change detection is fully
// deterministic (rescan-as-truth, SR-11) without depending on a live OS watcher.
func startNode(t *testing.T, ctx context.Context, syncDir string, opts ...nodeOpt) *node {
	t.Helper()
	id, err := transport.GenerateIdentity()
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	snapPath := filepath.Join(t.TempDir(), "snapshot.gob")
	return buildNode(t, ctx, syncDir, id, snapPath, opts...)
}

// restartNode simulates a daemon restart: it builds a FRESH engine + transport over the
// SAME folder, device identity, and snapshot path as old. The caller MUST stop(old)
// first (so old's shutdown has persisted the snapshot and freed its port). The restarted
// node loads that snapshot and runs startupReconcile — the only path that can recover a
// deletion/recreate that happened while the daemon was down (MK-6).
func restartNode(t *testing.T, ctx context.Context, old *node, opts ...nodeOpt) *node {
	t.Helper()
	return buildNode(t, ctx, old.dir, old.id, old.snapPath, opts...)
}

// buildNode is the shared constructor for startNode/restartNode. The transport is tied
// to the node's OWN cancellable child context, so stop(n) tears down the transport (and
// frees its loopback port) as well as the engine — a clean, restartable teardown.
func buildNode(t *testing.T, ctx context.Context, syncDir string, id *transport.Identity, snapPath string, opts ...nodeOpt) *node {
	t.Helper()
	// Each node owns its own cancellable child context so stop / cleanup reaps its Run
	// + transport goroutines BEFORE the test object is torn down (otherwise the engine's
	// t.Logf would race testing's teardown).
	nctx, ncancel := context.WithCancel(ctx)

	var eng *reconcile.Engine
	tp := transport.New(nctx, id, transport.NewAllowlist(),
		transport.WithHello(func() protocol.Hello { return eng.Hello() }))

	cfg := reconcile.Config{
		FolderID:         "t",
		AbsRoot:          syncDir,
		Self:             id.DeviceID,
		SnapshotPath:     snapPath,
		Transport:        tp,
		RescanInterval:   40 * time.Millisecond, // fast change-detection (watcher is disabled)
		SnapshotInterval: time.Hour,
		RequestTimeout:   15 * time.Second, // headroom for loaded runners (no spurious full-file restart)
		Logf:             t.Logf,
	}
	for _, o := range opts {
		o(&cfg)
	}
	eng, err := reconcile.New(cfg)
	if err != nil {
		ncancel()
		t.Fatalf("engine: %v", err)
	}
	addr, err := tp.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ncancel()
		t.Fatalf("listen: %v", err)
	}
	n := &node{dir: syncDir, id: id, tp: tp, eng: eng, addr: addr.String(), done: make(chan struct{}), cancel: ncancel, snapPath: snapPath}
	go func() { _ = eng.Run(nctx); close(n.done) }()
	t.Cleanup(func() { ncancel(); <-n.done })
	return n
}

// stop cancels n's engine+transport context and waits for Run to return. Run's teardown
// persists the snapshot (engine.go shutdown saveSnapshot), so after stop the snapshot on
// disk reflects n's last state — what a restartNode then loads. Idempotent: the t.Cleanup
// hook may cancel + wait again (a second cancel is a no-op; the closed done returns at once).
func stop(t *testing.T, n *node) {
	t.Helper()
	n.cancel()
	<-n.done
}

// connect pairs a and b (mutual allow-list) and dials a -> b. Dial blocks through the
// handshake; both engines receive PeerConnected and exchange indices.
func connect(t *testing.T, a, b *node) {
	t.Helper()
	a.tp.Allowlist().Add(b.id.DeviceID)
	b.tp.Allowlist().Add(a.id.DeviceID)
	if err := a.tp.Dial("tcp", b.addr); err != nil {
		t.Fatalf("dial: %v", err)
	}
}

// waitConverged polls until both roots are bit-identical and STAY identical for a
// short settle window (quiesce-then-compare, CDD-8). A stable equal root also proves
// no sync loop (a received file produced no sustained outbound churn — SR-6).
func waitConverged(t *testing.T, a, b *node, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if a.eng.RootHash() == b.eng.RootHash() {
			// Confirm it is genuine quiescence, not a transient crossing.
			stable := true
			for i := 0; i < 5; i++ {
				time.Sleep(20 * time.Millisecond)
				if a.eng.RootHash() != b.eng.RootHash() {
					stable = false
					break
				}
			}
			if stable {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("did not converge within %v: rootA=%x rootB=%x", timeout, a.eng.RootHash(), b.eng.RootHash())
}

// waitRootChanged polls until n's root differs from baseline (a local authorship
// event has been picked up by the rescan), or fails on timeout.
func waitRootChanged(t *testing.T, n *node, baseline [32]byte, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n.eng.RootHash() != baseline {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("root did not change within %v (local authorship not detected)", timeout)
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// conflictCopies lists the .sync-conflict-* basenames in dir (non-recursive over the
// directory of rel's parent), used to assert both peers minted the identical copy.
func conflictCopies(t *testing.T, dir, parent string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(parent)))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if name := e.Name(); strings.Contains(name, ".sync-conflict-") {
			out = append(out, name)
		}
	}
	return out
}

// hasContentSomewhere reports whether ANY regular file under dir (recursively) holds
// exactly want — used to assert a conflict left the loser's bytes RECOVERABLE without
// pinning which path won. The engine's own temp/probe artefacts (.msync-*) are skipped.
// This makes the "no data loss" assertion robust to the conflict winner being either the
// live path OR a .sync-conflict copy (the outcome depends on mtime/ShortID ordering).
func hasContentSomewhere(t *testing.T, dir, want string) bool {
	t.Helper()
	found := false
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".msync-") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr == nil && string(b) == want {
			found = true
		}
		return nil
	})
	return found
}

// tempFiles lists the engine's atomic-write temp artefacts (.msync-*.tmp) left in dir.
// After a killed transfer SR-1 requires this to be empty: the temp is discarded on any
// pre-rename error, so a partial reconstruction never lingers (transfer.go atomicWriteVerify).
func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if name := e.Name(); strings.HasPrefix(name, ".msync-") && strings.HasSuffix(name, ".tmp") {
			out = append(out, name)
		}
	}
	return out
}

// cutProxy is a loopback TCP middlebox: it forwards a single dialled connection to dst
// but severs BOTH legs once a cumulative byte threshold is crossed, to interrupt a
// real transfer mid-stream for the killed-transfer scenario (SR-1/SR-2;
// decisions/phase6/killed-transfer-fault-injection.md). The threshold is chosen above
// the TLS handshake + HELLO + INDEX bytes and below the file size, so the cut lands
// inside the chunk stream. cut is closed once a connection has been severed.
type cutProxy struct {
	ln       net.Listener
	dst      string
	cutAfter int64
	cut      chan struct{}
	once     sync.Once
}

// startCutProxy starts a cutProxy forwarding to dst, severing after cutAfter cumulative
// bytes (both directions). It returns immediately; the listener is closed at test cleanup.
func startCutProxy(t *testing.T, dst string, cutAfter int64) *cutProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cutProxy listen: %v", err)
	}
	p := &cutProxy{ln: ln, dst: dst, cutAfter: cutAfter, cut: make(chan struct{})}
	go p.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return p
}

func (p *cutProxy) addr() string { return p.ln.Addr().String() }

func (p *cutProxy) serve() {
	for {
		in, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(in)
	}
}

// handle pipes in<->dst, counting bytes in BOTH directions toward one shared threshold;
// once crossed it closes both legs (severing the transfer) and signals cut.
func (p *cutProxy) handle(in net.Conn) {
	out, err := net.Dial("tcp", p.dst)
	if err != nil {
		_ = in.Close()
		return
	}
	var total atomic.Int64
	done := make(chan struct{}, 2)
	sever := func() {
		p.once.Do(func() { close(p.cut) })
		_ = in.Close()
		_ = out.Close()
	}
	pipe := func(dst, src net.Conn) {
		buf := make([]byte, 4096)
		for {
			n, rerr := src.Read(buf)
			if n > 0 {
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
				if total.Add(int64(n)) >= p.cutAfter {
					sever()
					break
				}
			}
			if rerr != nil {
				if rerr != io.EOF {
					_ = dst.Close()
				}
				break
			}
		}
		done <- struct{}{}
	}
	go pipe(out, in)
	go pipe(in, out)
	<-done
	_ = in.Close()
	_ = out.Close()
}
