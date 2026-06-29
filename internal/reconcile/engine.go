package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/haider-toha/merkle-sync/internal/discovery"
	"github.com/haider-toha/merkle-sync/internal/merkle"
	"github.com/haider-toha/merkle-sync/internal/pathnorm"
	"github.com/haider-toha/merkle-sync/internal/protocol"
	"github.com/haider-toha/merkle-sync/internal/transport"
)

const (
	fetchQueueDepth = 256
	serveQueueDepth = 64
	chanDepth       = 256
)

// peerConn is the slice of transport.Conn the engine uses, so tests can substitute a
// recording fake (the real *transport.Conn satisfies it).
type peerConn interface {
	DeviceID() protocol.DeviceID
	Hello() protocol.Hello
	Send(protocol.Message) bool
	Done() <-chan struct{}
}

// peerTransport / peerDiscovery are the narrow slices of the two network layers the
// engine consumes, so it can be driven in-process by tests.
type peerTransport interface {
	Events() <-chan transport.Event
	Dial(network, addr string) error
}
type peerDiscovery interface {
	Events() <-chan discovery.Event
}

// peerState is per-connection engine state. The index map and inflight set are
// touched ONLY by the engine loop (no mutex); resp is shared with the puller (respMu).
// The puller + server goroutines are owned by ps.wg and reaped after cancel.
type peerState struct {
	conn     peerConn
	short    protocol.ShortID
	index    map[string]merkle.FileInfo // peer's last advertised set (loop-only)
	inflight map[string]bool            // paths currently queued/fetching (loop-only)

	fetchQ chan fetchTask
	serveQ chan protocol.Request

	respMu  sync.Mutex
	resp    map[uint32]chan protocol.Response
	nextReq atomic.Uint32

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// fetchTask asks the per-peer puller to materialise leaf's content at leaf.Path. A
// conflict-copy task sets advertise so the loser broadcasts the copy on success (so
// the peer that lacks the loser's bytes can fetch it — SR-7 "the copy syncs as a
// normal file").
type fetchTask struct {
	leaf      merkle.FileInfo
	advertise bool
}

// completion is the puller's report back to the loop after a materialisation attempt.
type completion struct {
	leaf      merkle.FileInfo
	ok        bool
	clobber   bool
	advertise bool
	peer      protocol.ShortID
}

// Config configures an Engine. Transport/Discovery/Watcher are optional (nil ⇒ that
// input is simply absent), so the engine can be unit-driven without the network.
type Config struct {
	FolderID         string
	AbsRoot          string
	Self             protocol.DeviceID
	SnapshotPath     string
	RescanInterval   time.Duration
	SnapshotInterval time.Duration
	DebounceWindow   time.Duration
	RequestTimeout   time.Duration

	Transport peerTransport
	Discovery peerDiscovery
	// Watcher injects a watcher (tests use a fake). If nil and EnableWatcher is set,
	// Run creates the production fsnotify-backed watcher over AbsRoot.
	Watcher       fsWatcher
	EnableWatcher bool
	Logf          func(string, ...any)
}

// Engine is the reconcile core: the single writer of tree state behind one RWMutex
// (GR-5). Construct with New, then Run(ctx) to drive it (Run blocks until ctx is
// cancelled). See package doc + docs/audit/decisions/ws4/*.md.
type Engine struct {
	folderID  string
	absRoot   string
	self      protocol.DeviceID
	selfShort protocol.ShortID

	snapshotPath     string
	rescanInterval   time.Duration
	snapshotInterval time.Duration
	debounceWindow   time.Duration
	requestTimeout   time.Duration
	caseSensitive    bool

	tport         peerTransport
	disco         peerDiscovery
	watch         fsWatcher
	enableWatcher bool
	logf          func(string, ...any)

	mu       sync.RWMutex // guards files, tree, expected, peers (the reconcile core)
	files    map[string]merkle.FileInfo
	tree     *merkle.Tree
	expected map[string][32]byte
	peers    map[protocol.DeviceID]*peerState

	reseed  bool                       // loop-only: cold-start reseed pending
	dialing map[protocol.DeviceID]bool // loop-only

	localChanges chan string
	completions  chan completion
	dialDone     chan protocol.DeviceID
	rescanNow    chan struct{}
	debounceIn   chan string

	runCtx context.Context
	wg     sync.WaitGroup
}

func orDur(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

// New builds an Engine over cfg.AbsRoot: it probes case-sensitivity, loads the local
// snapshot, runs the initial scan, and synthesises any deletion-across-restart (MK-6)
// — but starts no goroutines (call Run). A missing/corrupt snapshot enters cold-start
// reseed mode (vv-counter-seeding).
func New(cfg Config) (*Engine, error) {
	if cfg.AbsRoot == "" {
		return nil, errors.New("reconcile: AbsRoot required")
	}
	absRoot, err := filepath.Abs(cfg.AbsRoot)
	if err != nil {
		return nil, fmt.Errorf("reconcile: abs root: %w", err)
	}
	e := &Engine{
		folderID:         cfg.FolderID,
		absRoot:          absRoot,
		self:             cfg.Self,
		selfShort:        cfg.Self.Short(),
		snapshotPath:     cfg.SnapshotPath,
		rescanInterval:   orDur(cfg.RescanInterval, 30*time.Second),
		snapshotInterval: orDur(cfg.SnapshotInterval, 60*time.Second),
		debounceWindow:   orDur(cfg.DebounceWindow, 150*time.Millisecond),
		requestTimeout:   orDur(cfg.RequestTimeout, 30*time.Second),
		tport:            cfg.Transport,
		disco:            cfg.Discovery,
		watch:            cfg.Watcher,
		enableWatcher:    cfg.EnableWatcher,
		logf:             cfg.Logf,
		files:            make(map[string]merkle.FileInfo),
		expected:         make(map[string][32]byte),
		peers:            make(map[protocol.DeviceID]*peerState),
		dialing:          make(map[protocol.DeviceID]bool),
		localChanges:     make(chan string, chanDepth),
		completions:      make(chan completion, chanDepth),
		dialDone:         make(chan protocol.DeviceID, 64),
		rescanNow:        make(chan struct{}, 1),
		debounceIn:       make(chan string, chanDepth),
	}
	if e.logf == nil {
		e.logf = func(string, ...any) {}
	}
	e.caseSensitive = probeCaseSensitive(absRoot)
	if err := e.startupReconcile(); err != nil {
		return nil, err
	}
	return e, nil
}

// startupReconcile loads the snapshot, scans, restores version vectors for unchanged
// files (+ bumps for files changed while down), and synthesises tombstones for
// deletions that happened while the daemon was off (MK-6, CDD-7.1).
func (e *Engine) startupReconcile() error {
	var prev []merkle.FileInfo
	if e.snapshotPath != "" {
		p, err := merkle.LoadSnapshot(e.snapshotPath)
		if err != nil && !errors.Is(err, merkle.ErrSnapshotFormat) {
			return fmt.Errorf("reconcile: load snapshot: %w", err)
		}
		prev = p // nil for a missing or corrupt snapshot ⇒ create-only + reseed
	}
	cur, err := merkle.Scan(e.absRoot)
	if err != nil {
		return fmt.Errorf("reconcile: initial scan: %w", err)
	}
	cur = dropInternal(cur)
	cur = restoreVVs(prev, cur, e.selfShort)
	for _, fi := range merkle.SynthesizeDeletions(prev, cur, e.selfShort) {
		e.files[fi.Path] = fi
	}
	e.reseed = len(prev) == 0
	e.rebuildLocked()
	return nil
}

// restoreVVs re-attaches persisted version vectors to a fresh scan (whose VVs are
// empty): an unchanged file keeps its history; a file whose content changed while the
// daemon was down is a local authorship event (bump); a brand-new file keeps the empty
// VV the initial scan seeds (CDD-3 — initial scan is not authorship). A reappeared path
// over a prior tombstone is a new create (empty VV). Pure; testable.
func restoreVVs(prev, cur []merkle.FileInfo, self protocol.ShortID) []merkle.FileInfo {
	if len(prev) == 0 {
		return cur
	}
	byPath := make(map[string]merkle.FileInfo, len(prev))
	for _, p := range prev {
		byPath[p.Path] = p
	}
	out := make([]merkle.FileInfo, len(cur))
	copy(out, cur)
	for i := range out {
		p, ok := byPath[out[i].Path]
		if !ok || p.Deleted {
			continue
		}
		if p.ContentHash == out[i].ContentHash && p.Type == out[i].Type {
			out[i].Version = p.Version
		} else {
			out[i].Version = p.Version.Bump(self)
		}
	}
	return out
}

// RootHash returns the current local Merkle root (the convergence oracle, SR-5).
func (e *Engine) RootHash() [32]byte {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.tree.RootHash()
}

// Snapshot returns a copy of the current FileInfo set (live leaves + tombstones),
// sorted by path — for tests and diagnostics.
func (e *Engine) Snapshot() []merkle.FileInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshotFilesLocked()
}

// Hello provides the engine's current HELLO for the transport's WithHello (the
// transport overrides DeviceID with its own identity). RootHash drives the SR-5
// "skip INDEX when already converged" short-circuit.
func (e *Engine) Hello() protocol.Hello {
	return protocol.Hello{
		ProtoVersion: transport.ProtoVersion,
		FolderID:     e.folderID,
		RootHash:     e.RootHash(),
	}
}

// rebuildLocked recomputes e.tree from e.files. Caller holds e.mu (write), except the
// single-goroutine New path. CPU only (no I/O) — safe under the lock (GR-5).
func (e *Engine) rebuildLocked() {
	set := make([]merkle.FileInfo, 0, len(e.files))
	for _, fi := range e.files {
		set = append(set, fi)
	}
	t, err := merkle.BuildTree(set)
	if err != nil {
		e.logf("reconcile: rebuild tree: %v", err)
		return
	}
	e.tree = t
}

// Run drives the engine: it starts the watcher debounce + drains, the rescan and
// snapshot tickers, and the main select loop that is the SINGLE WRITER of tree state.
// It blocks until ctx is cancelled, then tears down peers + watcher, persists the
// snapshot, and reaps every owned goroutine (GR-3).
func (e *Engine) Run(ctx context.Context) error {
	e.runCtx = ctx
	e.startWatcher(ctx)

	rescanT := time.NewTicker(e.rescanInterval)
	defer rescanT.Stop()
	snapT := time.NewTicker(e.snapshotInterval)
	defer snapT.Stop()

	var tEvents <-chan transport.Event
	if e.tport != nil {
		tEvents = e.tport.Events()
	}
	var dEvents <-chan discovery.Event
	if e.disco != nil {
		dEvents = e.disco.Events()
	}

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case ev := <-tEvents:
			e.handleTransportEvent(ev)
		case ev := <-dEvents:
			e.onPeerDiscovered(ev)
		case key := <-e.localChanges:
			e.onLocalChange(key)
		case c := <-e.completions:
			e.handleCompletion(c)
		case id := <-e.dialDone:
			delete(e.dialing, id)
		case <-e.rescanNow:
			e.rescan()
		case <-rescanT.C:
			e.rescan()
		case <-snapT.C:
			e.saveSnapshot()
		}
	}

	e.shutdownPeers()
	if e.watch != nil {
		_ = e.watch.Close()
	}
	e.wg.Wait()
	e.saveSnapshot()
	return ctx.Err()
}

// startWatcher wires the optional fs watcher: raw events -> debounce -> localChanges;
// an overflow -> a full rescan (SR-11). All goroutines are owned by e.wg.
func (e *Engine) startWatcher(ctx context.Context) {
	if e.watch == nil && e.enableWatcher {
		w, err := newFSNotifyWatcher(ctx, e.absRoot)
		if err != nil {
			e.logf("reconcile: watcher disabled (%v); relying on periodic rescan (SR-11)", err)
		} else {
			e.watch = w
		}
	}
	if e.watch == nil {
		return
	}
	deb := &debouncer{
		window: e.debounceWindow,
		in:     e.debounceIn,
		now:    time.Now,
		emit: func(k string) {
			select {
			case e.localChanges <- k:
			case <-ctx.Done():
			}
		},
	}
	e.wg.Add(1)
	go func() { defer e.wg.Done(); deb.run(ctx) }()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case k, ok := <-e.watch.Changes():
				if !ok {
					return
				}
				select {
				case e.debounceIn <- k:
				case <-ctx.Done():
					return
				}
			case _, ok := <-e.watch.Overflow():
				if !ok {
					return
				}
				select {
				case e.rescanNow <- struct{}{}:
				default:
				}
			}
		}
	}()
}

// ---- transport / discovery event handling (loop) ----

func (e *Engine) handleTransportEvent(ev transport.Event) {
	switch ev.Kind {
	case transport.PeerConnected:
		e.onPeerConnected(ev.Conn)
	case transport.PeerDisconnected:
		e.removePeer(ev.DeviceID)
	case transport.PeerMessage:
		e.handleMessage(ev)
	}
}

func (e *Engine) onPeerConnected(conn peerConn) {
	if conn == nil {
		return
	}
	e.addPeer(conn)
	if conn.Hello().RootHash == e.RootHash() {
		return // already converged ⇒ skip the INDEX exchange (SR-5 short-circuit)
	}
	idx, err := e.buildIndex()
	if err != nil {
		e.logf("reconcile: build index for %s: %v", conn.DeviceID(), err)
		return
	}
	conn.Send(idx)
}

func (e *Engine) addPeer(conn peerConn) *peerState {
	pctx, cancel := context.WithCancel(e.runCtx)
	ps := &peerState{
		conn:     conn,
		short:    conn.DeviceID().Short(),
		index:    make(map[string]merkle.FileInfo),
		inflight: make(map[string]bool),
		fetchQ:   make(chan fetchTask, fetchQueueDepth),
		serveQ:   make(chan protocol.Request, serveQueueDepth),
		resp:     make(map[uint32]chan protocol.Response),
		cancel:   cancel,
	}
	e.mu.Lock()
	e.peers[conn.DeviceID()] = ps
	e.mu.Unlock()

	ps.wg.Add(2)
	go e.pullLoop(pctx, ps)
	go e.serveLoop(pctx, ps)
	return ps
}

func (e *Engine) removePeer(id protocol.DeviceID) {
	e.mu.Lock()
	ps, ok := e.peers[id]
	if ok {
		delete(e.peers, id)
	}
	e.mu.Unlock()
	if !ok {
		return
	}
	ps.cancel()
	e.wg.Add(1)
	go func() { defer e.wg.Done(); ps.wg.Wait() }() // reap off the loop (no loop block)
}

func (e *Engine) shutdownPeers() {
	e.mu.Lock()
	peers := make([]*peerState, 0, len(e.peers))
	for id, ps := range e.peers {
		peers = append(peers, ps)
		delete(e.peers, id)
	}
	e.mu.Unlock()
	for _, ps := range peers {
		ps.cancel()
		e.wg.Add(1)
		go func(ps *peerState) { defer e.wg.Done(); ps.wg.Wait() }(ps)
	}
}

func (e *Engine) handleMessage(ev transport.Event) {
	ps := e.peerByDevice(ev.DeviceID)
	if ps == nil {
		return
	}
	switch m := ev.Message.(type) {
	case protocol.Index:
		e.onIndex(ps, m.Body)
	case protocol.IndexUpdate:
		e.onIndexUpdate(ps, m.Body)
	case protocol.Request:
		e.routeRequest(ps, m)
	case protocol.Response:
		e.routeResponse(ps, m)
	case protocol.Ping, protocol.Close:
		// keepalive / graceful-close notice — nothing to mutate
	}
}

func (e *Engine) routeRequest(ps *peerState, req protocol.Request) {
	select {
	case ps.serveQ <- req:
	default:
		ps.conn.Send(protocol.Response{ReqID: req.ReqID, Code: protocol.CodeGeneric}) // overloaded ⇒ decline cleanly
	}
}

func (e *Engine) routeResponse(ps *peerState, resp protocol.Response) {
	ps.respMu.Lock()
	ch, ok := ps.resp[resp.ReqID]
	ps.respMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- resp: // buffered depth 1; the puller is waiting
	default:
	}
}

func (e *Engine) onPeerDiscovered(ev discovery.Event) {
	if e.tport == nil || ev.Kind != discovery.PeerDiscovered {
		return
	}
	if ev.DeviceID == e.self || !ev.Addr.IsValid() {
		return
	}
	e.mu.RLock()
	_, connected := e.peers[ev.DeviceID]
	e.mu.RUnlock()
	if connected || e.dialing[ev.DeviceID] {
		return
	}
	e.dialing[ev.DeviceID] = true
	id, addr := ev.DeviceID, ev.Addr.String()
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		if err := e.tport.Dial("tcp", addr); err != nil {
			e.logf("reconcile: dial %s: %v", addr, err)
		}
		select {
		case e.dialDone <- id:
		case <-e.runCtx.Done():
		}
	}()
}

// ---- index exchange + reconciliation (loop) ----

func (e *Engine) onIndex(ps *peerState, body []byte) {
	set, err := merkle.DecodeFileInfos(body)
	if err != nil {
		e.logf("reconcile: decode INDEX from %d: %v", ps.short, err)
		return
	}
	idx := make(map[string]merkle.FileInfo, len(set))
	for _, fi := range set {
		idx[fi.Path] = fi
	}
	ps.index = idx
	if e.reseed {
		e.doReseed(ps)
	}
	e.reconcileWithPeer(ps)
}

func (e *Engine) onIndexUpdate(ps *peerState, body []byte) {
	set, err := merkle.DecodeFileInfos(body)
	if err != nil {
		e.logf("reconcile: decode INDEX_UPDATE from %d: %v", ps.short, err)
		return
	}
	for _, fi := range set {
		ps.index[fi.Path] = fi
	}
	e.reconcileWithPeer(ps)
}

// doReseed merges the peer's version vectors into our leaves before we assert any
// local authorship (vv-counter-seeding guard 2): a path whose content differs from
// the peer's is bumped ON TOP of the merged vector so our genuine post-wipe edit
// Dominates (and is accepted, not silently lost — FM-4). The bumped leaves are
// broadcast so the peer adopts them. Pull-based apply means the peer never overwrites
// us before we reseed, so this is data-loss-free.
func (e *Engine) doReseed(ps *peerState) {
	var bumped []merkle.FileInfo
	e.mu.Lock()
	for path, lf := range e.files {
		// Only reseed UN-AUTHORED local content (empty VV — the wipe signature: a file
		// present on disk but with no history) against a peer's LIVE entry. A file we
		// already authored needs no reseed; and a peer TOMBSTONE must NOT be reseeded
		// into — doing so would bump our stale copy into a delete-vs-modify conflict
		// and resurrect the deleted file (the SR-10 bug this guard avoids).
		if lf.Deleted || len(lf.Version) != 0 {
			continue
		}
		pf, ok := ps.index[path]
		if !ok || pf.Deleted {
			continue
		}
		merged := pf.Version.Copy() // adopt the peer's history (lf has none)
		if lf.ContentHash != pf.ContentHash {
			merged = merged.Bump(e.selfShort) // a genuine post-wipe edit ⇒ assert authorship (FM-4)
		}
		lf.Version = merged
		e.files[path] = lf
		bumped = append(bumped, lf)
	}
	if len(bumped) > 0 {
		e.rebuildLocked()
	}
	e.mu.Unlock()
	e.reseed = false
	e.broadcastUpdate(bumped)
}

// reconcileWithPeer diffs the local tree against the peer's advertised tree (prune-
// equal, MK-2) and executes the total resolver verdict for each differing path, then
// GCs any tombstone the peer has acknowledged. Read-only over the tree under RLock
// (zero I/O held, GR-5); mutations go through execute / the puller.
func (e *Engine) reconcileWithPeer(ps *peerState) {
	e.mu.RLock()
	localSet := e.snapshotFilesLocked()
	e.mu.RUnlock()

	peerSet := make([]merkle.FileInfo, 0, len(ps.index))
	for _, fi := range ps.index {
		peerSet = append(peerSet, fi)
	}
	localTree, err1 := merkle.BuildTree(localSet)
	peerTree, err2 := merkle.BuildTree(peerSet)
	if err1 != nil || err2 != nil {
		e.logf("reconcile: build trees: local=%v peer=%v", err1, err2)
		return
	}
	for _, d := range merkle.Diff(localTree, peerTree) {
		// A file-vs-directory type clash is structurally irreconcilable at one path
		// without choosing a loser: refuse + flag (no data lost, no impossible install,
		// no livelock), NEVER feed its nil side to resolve as a false absence (MK-2).
		if d.IsTypeClash() {
			e.flagTypeClash(d)
			continue
		}
		e.execute(ps, resolve(d.Local, d.Remote, e.selfShort))
	}
	e.mu.Lock()
	if e.gcTombstonesLocked() {
		e.rebuildLocked()
	}
	e.mu.Unlock()
}

// execute carries out a resolver plan. NoOp does nothing; a live install / conflict
// is materialised by the per-peer puller (off the loop); a tombstone install / delete
// is applied directly. For a conflict the loser copy is enqueued BEFORE the winner so
// the loser's still-on-disk bytes are copied locally before the winner overwrites the
// path (FIFO per-peer puller preserves the order).
func (e *Engine) execute(ps *peerState, p plan) {
	switch p.kind {
	case planNoOp:
		return
	case planInstall:
		if p.install.Deleted {
			e.applyTombstone(p.install)
		} else {
			e.enqueueFetch(ps, p.install, false)
		}
	case planConflict:
		// Only the side that ALREADY holds the loser's bytes materialises the copy
		// (local-reuse, zero network) and advertises it; the other side simply fetches
		// it as a normal remote file once that advertisement lands (a later reconcile),
		// so there is no racy cross-fetch of a copy the source has not created yet.
		if p.loser != nil && e.hasLocalContent(p.loser.ContentHash) {
			e.enqueueFetch(ps, *p.loser, true)
		}
		if p.winner.Deleted {
			e.applyTombstone(p.winner)
		} else {
			e.enqueueFetch(ps, p.winner, false)
		}
	}
}

// flagTypeClash records a file-vs-directory divergence at one path (one peer has a
// FILE there, the other a DIRECTORY) and does nothing destructive: it enqueues no
// fetch and reports no completion, so neither the impossible install nor a retry
// livelock can occur. Both peers keep their own data (no loss); the path is left
// divergent and FLAGGED, the same accepted carve-out as the CDD-5 case-clobber refuse.
// Auto keep-both (directory wins, file -> .sync-conflict copy, both converge) is the
// logged forward path (decisions/phase7/MK-2-file-vs-dir-typeclash-resolution.md).
func (e *Engine) flagTypeClash(d merkle.DiffEntry) {
	if d.RemoteDir {
		e.logf("%v: %q is a FILE locally but a DIRECTORY on the peer — refused (no data lost; resolve by hand)", ErrTypeClash, d.Path)
	} else {
		e.logf("%v: %q is a DIRECTORY locally but a FILE on the peer — refused (no data lost; resolve by hand)", ErrTypeClash, d.Path)
	}
}

// inflightLocked reports whether any peer is currently materialising key (an apply in
// progress). Caller holds e.mu. The change-detection paths consult this so the brief
// window between a fetched file's atomic rename and its handleCompletion (when files +
// expected are updated) is NOT mistaken for local authorship — which would bump the VV
// for content we are applying, not authoring, and diverge the peers (SR-8 guard c).
func (e *Engine) inflightLocked(key string) bool {
	for _, ps := range e.peers {
		if ps.inflight[key] {
			return true
		}
	}
	return false
}

// hasLocalContent reports whether some live local file already holds content hashing
// to hash (a cheap recorded-state check, no disk I/O — the puller re-verifies on disk
// before reusing). Used to decide which side of a conflict materialises the copy.
func (e *Engine) hasLocalContent(hash [32]byte) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, fi := range e.files {
		if !fi.Deleted && fi.Type == merkle.TypeFile && fi.ContentHash == hash {
			return true
		}
	}
	return false
}

func (e *Engine) enqueueFetch(ps *peerState, leaf merkle.FileInfo, advertise bool) {
	if ps.inflight[leaf.Path] {
		return
	}
	select {
	case ps.fetchQ <- fetchTask{leaf: leaf, advertise: advertise}:
		ps.inflight[leaf.Path] = true
	default:
		// queue full; the diff persists, so a later reconcile / rescan retries it
	}
}

// applyTombstone removes the on-disk file (off the lock) then records the tombstone
// and its expected (zero) hash under the lock. On a real transition (we previously
// held the file, or a different version) it RE-ADVERTISES the tombstone once: this
// is not new authorship (it carries the origin's VV, so the peer sees Equal and the
// apply is idempotent — no sync loop), it is the symmetric-GC handshake — it tells
// the peer "I have applied this delete," so BOTH peers' ack-gate (canGC) eventually
// fires and they GC the tombstone together rather than one-with-one-without
// (tombstone-retention-gc, CDD-7.2). A redundant re-apply of the identical tombstone
// is silent.
func (e *Engine) applyTombstone(t merkle.FileInfo) {
	osPath := pathnorm.ToOSPath(e.absRoot, t.Path, pathnorm.HostTarget())
	_ = os.Remove(osPath) // best-effort; already-gone is fine
	e.mu.Lock()
	prev, had := e.files[t.Path]
	redundant := had && prev.Deleted && prev.Version.IsEqual(t.Version)
	e.files[t.Path] = t
	e.expected[t.Path] = t.ContentHash
	e.rebuildLocked()
	e.mu.Unlock()
	if !redundant {
		e.broadcastUpdate([]merkle.FileInfo{t})
	}
}

func (e *Engine) pullLoop(ctx context.Context, ps *peerState) {
	defer ps.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ps.conn.Done():
			return
		case task := <-ps.fetchQ:
			e.materialise(ctx, ps, task.leaf, task.advertise)
		}
	}
}

func (e *Engine) serveLoop(ctx context.Context, ps *peerState) {
	defer ps.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ps.conn.Done():
			return
		case req := <-ps.serveQ:
			e.serveRequest(ps, req)
		}
	}
}

// report delivers a puller completion to the loop, without blocking past shutdown
// (the loop stops draining once runCtx is done).
func (e *Engine) report(c completion) {
	select {
	case e.completions <- c:
	case <-e.runCtx.Done():
	}
}

// handleCompletion installs a successfully-materialised leaf (updating the FileInfo
// map + the expected-hash echo record under the lock — NEVER broadcasting, since an
// apply is not local authorship, SR-6/SR-8), then re-reconciles with the source peer
// to drain remaining diffs promptly.
func (e *Engine) handleCompletion(c completion) {
	if c.ok {
		e.mu.Lock()
		e.files[c.leaf.Path] = c.leaf
		e.expected[c.leaf.Path] = c.leaf.ContentHash
		e.rebuildLocked()
		e.mu.Unlock()
		// A conflict copy is a NEW path the peer may lack; advertise it so the peer
		// can fetch it (SR-7 — the copy syncs as a normal file). It carries a fixed
		// VV, so the peer's apply is Equal/idempotent — no sync loop. Normal fetches
		// are never advertised (SR-6 "received file ⇒ zero outbound").
		if c.advertise {
			e.broadcastUpdate([]merkle.FileInfo{c.leaf})
		}
	}
	e.clearInflight(c.peer, c.leaf.Path)
	// Re-reconcile on BOTH success and failure: success drains the next batch; a
	// failure (e.g. a conflict-copy fetch that raced the source's create) re-enqueues
	// the still-needed leaf now that inflight is clear and the source has since
	// advertised it. The re-enqueue only fires when the leaf is in the peer's index
	// (the source has it on disk by then), so this cannot tight-spin.
	if ps := e.peerByShort(c.peer); ps != nil {
		e.reconcileWithPeer(ps)
	}
}

// ---- local change detection (loop) ----

// onLocalChange handles a settled watcher hint for one path: a content change vs the
// recorded leaf is local authorship (bump VV + broadcast, SR-6); a re-hash equal to
// the recorded content is our own apply echo (no authorship, SR-8 — filtered by
// content, so a genuine concurrent edit is still caught); a vanished file is a local
// delete (tombstone + broadcast, SR-9).
func (e *Engine) onLocalChange(key string) {
	if internalFile(key) {
		return // our own temp/probe artefact — never authorship
	}
	osPath := pathnorm.ToOSPath(e.absRoot, key, pathnorm.HostTarget())
	info, statErr := os.Lstat(osPath)

	e.mu.RLock()
	prev, had := e.files[key]
	inflight := e.inflightLocked(key)
	e.mu.RUnlock()
	if inflight {
		return // an apply is materialising this path — not local authorship (SR-8)
	}

	if statErr != nil { // file gone
		if had && !prev.Deleted {
			tomb := prev.SetDeleted(e.selfShort)
			e.mu.Lock()
			e.files[key] = tomb
			delete(e.expected, key)
			e.rebuildLocked()
			e.mu.Unlock()
			e.broadcastUpdate([]merkle.FileInfo{tomb})
		}
		return
	}
	if info.IsDir() {
		return // directories are tree nodes, not leaves
	}
	nfi, err := e.scanOne(key, osPath, info)
	if err != nil {
		e.logf("reconcile: rehash %q: %v", key, err)
		return
	}
	if had && !prev.Deleted && prev.ContentHash == nfi.ContentHash && prev.Type == nfi.Type {
		if prev.ModTimeNS != nfi.ModTimeNS { // content identical, only mtime moved ⇒ quiet update
			prev.ModTimeNS = nfi.ModTimeNS
			e.mu.Lock()
			e.files[key] = prev
			e.mu.Unlock()
		}
		return // echo / no new authorship (SR-8)
	}
	nfi.Version = prev.Version.Bump(e.selfShort) // prev.Version is empty for a new file
	e.mu.Lock()
	e.files[key] = nfi
	delete(e.expected, key)
	e.rebuildLocked()
	e.mu.Unlock()
	e.broadcastUpdate([]merkle.FileInfo{nfi})
}

// rescan is the SOURCE OF TRUTH (SR-11): a full scan whose delta vs the recorded set
// catches every create/edit/delete the watcher may have dropped, applies local
// authorship (bump + broadcast), GCs acknowledged tombstones, and persists the
// snapshot. The scan I/O happens BEFORE the lock (GR-5).
func (e *Engine) rescan() {
	scanned, err := merkle.Scan(e.absRoot)
	if err != nil {
		e.logf("reconcile: rescan: %v", err)
		return
	}
	scanned = dropInternal(scanned)
	present := make(map[string]struct{}, len(scanned))
	for _, s := range scanned {
		present[s.Path] = struct{}{}
	}

	var delta []merkle.FileInfo
	e.mu.Lock()
	for _, s := range scanned {
		prev, had := e.files[s.Path]
		if had && !prev.Deleted && prev.ContentHash == s.ContentHash && prev.Type == s.Type {
			if prev.ModTimeNS != s.ModTimeNS {
				prev.ModTimeNS = s.ModTimeNS
				e.files[s.Path] = prev
			}
			continue // unchanged / echo
		}
		if e.inflightLocked(s.Path) {
			continue // an apply is materialising this path — not local authorship (SR-8)
		}
		s.Version = prev.Version.Bump(e.selfShort)
		e.files[s.Path] = s
		delete(e.expected, s.Path)
		delta = append(delta, s)
	}
	for path, fi := range e.files {
		if fi.Deleted {
			continue
		}
		if _, ok := present[path]; !ok {
			tomb := fi.SetDeleted(e.selfShort)
			e.files[path] = tomb
			delete(e.expected, path)
			delta = append(delta, tomb)
		}
	}
	changed := len(delta) > 0
	if e.gcTombstonesLocked() {
		changed = true
	}
	if changed {
		e.rebuildLocked()
	}
	e.mu.Unlock()

	e.broadcastUpdate(delta)
	// Re-reconcile with every peer: the rescan is the source of truth (SR-11), so it
	// also re-drives any fetch left pending by a transient failure (the bounded-cadence
	// retry safety net the diff-persists contract relies on).
	e.mu.RLock()
	peers := make([]*peerState, 0, len(e.peers))
	for _, ps := range e.peers {
		peers = append(peers, ps)
	}
	e.mu.RUnlock()
	for _, ps := range peers {
		e.reconcileWithPeer(ps)
	}
	e.saveSnapshot()
}

// scanOne computes the FileInfo for a single path (the per-path analogue of
// merkle.Scan), leaving Version empty for the caller to seed/bump.
func (e *Engine) scanOne(key, osPath string, info os.FileInfo) (merkle.FileInfo, error) {
	fi := merkle.FileInfo{Path: key, Mode: uint32(info.Mode()), ModTimeNS: info.ModTime().UnixNano()}
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(osPath)
		if err != nil {
			return fi, err
		}
		norm := pathnorm.NormalizeComponent(filepath.ToSlash(target))
		fi.Type = merkle.TypeSymlink
		fi.ContentHash = merkle.HashBytes([]byte(norm))
		fi.Size = uint64(len(norm))
		return fi, nil
	}
	h, err := merkle.HashFile(osPath)
	if err != nil {
		return fi, err
	}
	fi.Type = merkle.TypeFile
	fi.ContentHash = h
	fi.Size = uint64(info.Size())
	return fi, nil
}

func (e *Engine) saveSnapshot() {
	if e.snapshotPath == "" {
		return
	}
	e.mu.RLock()
	set := e.snapshotFilesLocked()
	e.mu.RUnlock()
	if err := merkle.SaveSnapshot(e.snapshotPath, set); err != nil {
		e.logf("reconcile: snapshot save: %v", err)
	}
}

// ---- small lookups ----

func (e *Engine) peerByDevice(id protocol.DeviceID) *peerState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.peers[id]
}

func (e *Engine) peerByShort(s protocol.ShortID) *peerState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, ps := range e.peers {
		if ps.short == s {
			return ps
		}
	}
	return nil
}

func (e *Engine) clearInflight(short protocol.ShortID, key string) {
	if ps := e.peerByShort(short); ps != nil {
		delete(ps.inflight, key)
	}
}

// internalPrefix marks the engine's OWN on-disk artefacts inside the sync root — the
// atomic-write temp files (.msync-*.tmp) and the case-sensitivity probe files. They
// must never enter the synced tree (they are transient and node-local), or a scan
// racing a transfer would advertise a temp file and break convergence.
const internalPrefix = ".msync-"

func internalFile(key string) bool {
	return strings.HasPrefix(path.Base(key), internalPrefix)
}

func dropInternal(set []merkle.FileInfo) []merkle.FileInfo {
	out := set[:0:0]
	for _, fi := range set {
		if !internalFile(fi.Path) {
			out = append(out, fi)
		}
	}
	return out
}
