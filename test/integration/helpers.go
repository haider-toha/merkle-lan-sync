// Package integration runs two in-process msync engines over loopback TLS to verify
// the WS-4 system invariants end-to-end: convergence, conflict no-loss, deletion +
// anti-resurrection, and bidirectional back-pressure (plan WS-4 #1/#2/#6/#11). Per
// CDD-8 every assertion is quiesce-then-compare (the equal-root oracle holds AT
// quiescence, SR-5).
package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haider-toha/merkle-sync/internal/protocol"
	"github.com/haider-toha/merkle-sync/internal/reconcile"
	"github.com/haider-toha/merkle-sync/internal/transport"
)

// node is one in-process engine + its transport, listening on a loopback port.
type node struct {
	dir  string
	id   *transport.Identity
	tp   *transport.Transport
	eng  *reconcile.Engine
	addr string
	done chan struct{}
}

// startNode builds an engine over syncDir (snapshot kept OUTSIDE syncDir so it is
// never scanned), starts it, and begins listening on a loopback port. The watcher is
// disabled and the rescan interval is short, so change detection is fully
// deterministic (rescan-as-truth, SR-11) without depending on a live OS watcher.
func startNode(t *testing.T, ctx context.Context, syncDir string) *node {
	t.Helper()
	id, err := transport.GenerateIdentity()
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	snapDir := t.TempDir()

	var eng *reconcile.Engine
	tp := transport.New(ctx, id, transport.NewAllowlist(),
		transport.WithHello(func() protocol.Hello { return eng.Hello() }))

	eng, err = reconcile.New(reconcile.Config{
		FolderID:         "t",
		AbsRoot:          syncDir,
		Self:             id.DeviceID,
		SnapshotPath:     filepath.Join(snapDir, "snapshot.gob"),
		Transport:        tp,
		RescanInterval:   40 * time.Millisecond,
		SnapshotInterval: time.Hour,
		RequestTimeout:   5 * time.Second,
		Logf:             t.Logf,
	})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	addr, err := tp.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Each node owns its own cancellable child context so its Run goroutine is reaped
	// at cleanup BEFORE the test object is torn down (otherwise the engine's t.Logf
	// would race testing's teardown). Cleanup runs after the test body returns.
	nctx, ncancel := context.WithCancel(ctx)
	n := &node{dir: syncDir, id: id, tp: tp, eng: eng, addr: addr.String(), done: make(chan struct{})}
	go func() { _ = eng.Run(nctx); close(n.done) }()
	t.Cleanup(func() { ncancel(); <-n.done })
	return n
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
