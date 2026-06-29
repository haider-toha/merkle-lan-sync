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
//
// preserve, if non-nil, is a leaf whose on-disk bytes MUST be materialised BEFORE leaf
// (the destructive step) is applied. The puller (runFetch) runs preserve-then-leaf as
// ONE unit: the destructive leaf step (overwrite a live winner, or remove the original
// for a tombstone leaf) only proceeds if the preserve copy actually lands; otherwise the
// leaf is SKIPPED so the bytes that were about to be destroyed survive for the next
// reconcile. Coupling the two into ONE queue slot also makes a queue-full drop atomic —
// both or neither — closing the split-drop data-loss hole. Two callers use it:
//   - enqueueConflict (PR-3): preserve = the conflict LOSER's .sync-conflict copy,
//     leaf = the winner install; preserveAdvertise=true (the minted copy is a new path
//     the peer may lack — SR-7).
//   - enqueueRename (PR-5): preserve = the rename's NEW create (which reuses the still-
//     present OLD bytes via localSource — zero network), leaf = the OLD path's tombstone;
//     preserveAdvertise=false (the create is a RECEIVED file, never re-broadcast —
//     SR-6/SR-8/PR-6). Deferring the tombstone's os.Remove until after the create's copy
//     lands makes the rename zero-network optimisation ORDER-INDEPENDENT
//     (decisions/phase7/PR-5-rename-zero-network-order-independence).
//
// preserveAdvertise is whether the preserve step broadcasts its leaf on success.
type fetchTask struct {
	leaf              merkle.FileInfo
	advertise         bool
	preserve          *merkle.FileInfo
	preserveAdvertise bool
}

// completion is the puller's report back to the loop after a materialisation attempt.
// applyTomb marks a coupled-conflict winner that is a TOMBSTONE: the loser copy has
// already landed, so the loop now applies the winning deletion (removing the original
// AFTER the copy — never before, the (B) delete-vs-modify fix). conflictAbort marks a
// coupled task whose loser copy did NOT land: the winner was skipped to protect the
// loser's bytes, so the loop just clears inflight WITHOUT an immediate re-reconcile (a
// rescan-paced retry; an immediate one could tight-spin while e.files still shows the
// loser live).
type completion struct {
	leaf          merkle.FileInfo
	ok            bool
	clobber       bool
	advertise     bool
	applyTomb     bool
	conflictAbort bool
	peer          protocol.ShortID
}

// Config configures an Engine. Transport/Discovery/Watcher are optional (nil ⇒ that
// input is simply absent), so the engine can be unit-driven without the network.
type Config struct {
	FolderID string
	AbsRoot  string
	Self     protocol.DeviceID
	// Peers is the explicitly-declared set of currently-paired peer DeviceIDs (the
	// out-of-band TOFU allow-list, PR-7). When non-nil it enables the startup ghost-
	// counter de-pair sweep (#10590, PR-4): any stored VV counter for a device not in
	// {Self} ∪ Peers is a de-paired device's ghost and is pruned at load. Nil ⇒ the
	// paired set is unknown ⇒ retain every counter (the safe fallback). cmd/msync passes
	// the -peer set; in-process tests that pair dynamically leave it nil.
	Peers            []protocol.DeviceID
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

	pairedShorts map[protocol.ShortID]bool // {self} ∪ declared peers; gates the de-pair ghost-counter sweep
	pairedKnown  bool                      // Config.Peers was provided (non-nil) ⇒ the sweep is enabled

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
	// Record the declared paired set (if any) so startupReconcile can prune the ghost
	// counters of de-paired devices (#10590, PR-4). Nil Config.Peers ⇒ pairedKnown stays
	// false ⇒ the sweep is a no-op and every counter is retained (the safe fallback).
	if cfg.Peers != nil {
		e.pairedKnown = true
		e.pairedShorts = make(map[protocol.ShortID]bool, len(cfg.Peers)+1)
		e.pairedShorts[e.selfShort] = true
		for _, p := range cfg.Peers {
			e.pairedShorts[p.Short()] = true
		}
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
	// Prune the ghost counters of any de-paired device before building the tree, so a
	// removed device's permanent counter can't block tombstone dominance and resurrect a
	// deletion (#10590 / FM-1, PR-4). No-op unless Config.Peers declared the paired set.
	e.sweepDepairedCountersLocked()
	e.reseed = len(prev) == 0
	e.rebuildLocked()
	return nil
}

// restoreVVs re-attaches persisted version vectors to a fresh scan (whose VVs are
// empty): an unchanged file keeps its history; a file whose content changed while the
// daemon was down is a local authorship event (bump); a brand-new file keeps the empty
// VV the initial scan seeds (CDD-3 — initial scan is not authorship). A path whose
// snapshot entry is a TOMBSTONE but which is present on disk again was RECREATED while
// the daemon was down: its VV is bumped ON TOP of the tombstone's so the recreate
// DOMINATES the prior delete — identical to the two live recreate paths (onLocalChange,
// rescan: prev.Version.Bump(self)). Without this, the recreate would keep an empty VV
// and a peer still holding the tombstone (non-empty VV) would dominate it and re-delete
// the local re-creation — the MK-6 recreate-over-tombstone data-loss case. Pure; testable.
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
		if !ok {
			continue // brand-new file: keep the empty VV the scan seeds (CDD-3)
		}
		if p.Deleted {
			// Recreated over a snapshot tombstone while down: bump so it dominates the
			// delete it supersedes (SynthesizeDeletions keeps this present cur entry).
			out[i].Version = p.Version.Bump(self)
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

	// Resolve every differing path, bucketing the two halves of a possible rename so they
	// can be COUPLED (PR-5). merkle.Diff emits in path-sorted order, NOT creates-before-
	// deletes, and a tombstone install's os.Remove is synchronous while a create install is
	// an async puller fetch — so without pairing a rename's OLD file is removed from disk
	// before the create can reuse it, forcing a needless network fetch (the refuted "zero
	// network" claim). Pairing a create with a same-content tombstone whose old bytes are
	// still local lets the puller reuse them BEFORE the deferred removal — order-independent
	// (decisions/phase7/PR-5-rename-zero-network-order-independence).
	type tombInstall struct {
		plan    plan
		oldHash [32]byte // the receiver's CURRENT on-disk content at the old path (d.Local)
		hasOld  bool     // the receiver actually holds those bytes locally ⇒ reusable
	}
	var creates []plan
	var tombs []tombInstall
	for _, d := range merkle.Diff(localTree, peerTree) {
		// A file-vs-directory type clash is structurally irreconcilable at one path
		// without choosing a loser: refuse + flag (no data lost, no impossible install,
		// no livelock), NEVER feed its nil side to resolve as a false absence (MK-2).
		if d.IsTypeClash() {
			e.flagTypeClash(d)
			continue
		}
		p := resolve(d.Local, d.Remote, e.selfShort)
		switch {
		case p.kind == planInstall && !p.install.Deleted:
			creates = append(creates, p)
		case p.kind == planInstall && p.install.Deleted:
			ti := tombInstall{plan: p}
			// d.Local is the receiver's live leaf at the old path being deleted — its bytes
			// are the rename source the create can reuse (the tombstone itself carries no
			// content_hash; SetDeleted zeroes it).
			if d.Local != nil && !d.Local.Deleted && d.Local.Type == merkle.TypeFile {
				ti.oldHash = d.Local.ContentHash
				ti.hasOld = true
			}
			tombs = append(tombs, ti)
		default:
			e.execute(ps, p) // conflict / no-op — unchanged
		}
	}

	// Pair each create with a same-content tombstone whose old bytes are still local and
	// enqueue the coupled rename task; skip any path already inflight (a concurrent task or
	// the deferral window). Each tombstone matches at most one create. Coupling never drops
	// or suppresses either half — it only reorders execution and sources the create's bytes
	// locally — so even a content-hash false match (two unrelated identical files) reaches
	// the identical converged end-state with no data loss.
	tombPaired := make([]bool, len(tombs))
	createDone := make([]bool, len(creates))
	for ci := range creates {
		if ps.inflight[creates[ci].install.Path] {
			createDone[ci] = true // already in flight ⇒ do not re-enqueue standalone below
			continue
		}
		for ti := range tombs {
			if tombPaired[ti] || !tombs[ti].hasOld || ps.inflight[tombs[ti].plan.install.Path] {
				continue
			}
			if tombs[ti].oldHash == creates[ci].install.ContentHash {
				e.enqueueRename(ps, creates[ci].install, tombs[ti].plan.install)
				tombPaired[ti] = true
				createDone[ci] = true
				break
			}
		}
	}

	// Execute the unpaired halves normally. A tombstone whose path is inflight (its coupled
	// rename is mid-apply, deferring the os.Remove) is skipped — the coupled task owns its
	// application; clearing inflight on completion lets a later reconcile finish any retry.
	for ci := range creates {
		if !createDone[ci] {
			e.execute(ps, creates[ci])
		}
	}
	for ti := range tombs {
		if !tombPaired[ti] && !ps.inflight[tombs[ti].plan.install.Path] {
			e.execute(ps, tombs[ti].plan)
		}
	}

	e.mu.Lock()
	if e.gcTombstonesLocked() {
		e.rebuildLocked()
	}
	e.mu.Unlock()
}

// execute carries out a resolver plan. NoOp does nothing; a live install is
// materialised by the per-peer puller (off the loop); a tombstone install is applied
// directly. For a conflict where THIS side holds the loser's bytes, the loser copy and
// the winner's (destructive) install are enqueued as ONE coupled puller task so the
// winner can never overwrite/remove the loser's only on-disk copy before that copy
// lands — the SR-7 no-data-loss guarantee under queue saturation AND for a winning
// tombstone (PR-3 (A)+(B); decisions/phase7/PR-3-conflict-no-data-loss-ordering.md).
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
		switch {
		case p.loser != nil && e.hasLocalContent(p.loser.ContentHash):
			// This side holds the loser's bytes: it is the custodian that mints the copy.
			// Refuse+flag (never destructive) if the conflict-copy key would exceed Windows
			// MAX_PATH on an unconfirmed-long-path target — leaving BOTH versions at their
			// paths (no data lost), the same carve-out as ErrCaseClobber / ErrTypeClash
			// (PR-3 §6, maxpath-longpath). Otherwise enqueue the COUPLED copy-then-winner
			// task: the puller preserves the loser before the winner touches the path.
			if pathnorm.WouldExceedMaxPath(e.absRoot, p.loser.Path) {
				e.logf("%v: conflict copy %q would exceed Windows MAX_PATH (%d) — refused (no data lost; resolve by hand)", ErrMaxPathExceeded, p.loser.Path, pathnorm.MaxPath)
				return
			}
			e.enqueueConflict(ps, *p.loser, p.winner)
		case p.winner.Deleted:
			// Winner-custodian for a winning deletion (we lack the loser's bytes — they
			// arrive as the advertised copy from the side that holds them): apply the delete.
			e.applyTombstone(p.winner)
		default:
			// Winner-custodian for a live winner (or a losing tombstone, p.loser==nil):
			// install the winner; any loser copy is fetched from its holder as a normal file.
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

// enqueueConflict enqueues a COUPLED conflict task: preserve the loser's bytes as its
// .sync-conflict copy (advertised so the peer that lacks them can fetch it — SR-7), then
// install the winner at the shared path. Both ride ONE queue slot keyed on the winner
// path, so (a) a queue-full drop is atomic — both or neither, the loser's bytes stay at
// the original path for the next reconcile — and (b) the puller runs them in order,
// gating the winner's destructive install on the copy actually landing (runFetch). This
// is the enforced copy-before-destroy that the SR-7 no-data-loss contract requires under
// saturation and for a winning tombstone (PR-3 (A)+(B)). A duplicate reconcile is
// deduped while the winner path is in flight.
func (e *Engine) enqueueConflict(ps *peerState, loser, winner merkle.FileInfo) {
	if ps.inflight[winner.Path] {
		return
	}
	l := loser
	select {
	case ps.fetchQ <- fetchTask{leaf: winner, advertise: false, preserve: &l, preserveAdvertise: true}:
		ps.inflight[winner.Path] = true
	default:
		// queue full; the WHOLE coupled task is dropped atomically — the loser's bytes
		// stay at the original path, the diff persists, a later reconcile retries (SR-7).
	}
}

// enqueueRename couples a rename's two halves so the new path's content-addressed local
// reuse is ORDER-INDEPENDENT (PR-5). The peer renamed old->new (same content_hash); the
// receiver must create `newCreate` and tombstone `oldTomb`, and the new path's bytes are
// still on disk ONLY at the old path. Enqueued as ONE coupled task (preserve=newCreate,
// leaf=oldTomb), the puller materialises newCreate FIRST — localSource finds the OLD file
// (still live in e.files + on disk, because the tombstone's destructive os.Remove is
// deferred to the applyTomb completion that runs AFTER the copy) — so the create costs
// ZERO network regardless of whether new sorts before or after old in the path-sorted
// diff. preserveAdvertise=false: the create is a RECEIVED file, never re-broadcast
// (SR-6/SR-8/PR-6). BOTH paths are marked inflight: newCreate so a duplicate reconcile
// does not double-fetch it, oldTomb so a reconcile during the deferral window does not
// separately apply the still-live old tombstone (reconcileWithPeer skips inflight tombs).
// A queue-full drop is atomic (both or neither): the old bytes stay at the old path and a
// later reconcile/rescan retries — no data loss.
func (e *Engine) enqueueRename(ps *peerState, newCreate, oldTomb merkle.FileInfo) {
	if ps.inflight[newCreate.Path] || ps.inflight[oldTomb.Path] {
		return
	}
	c := newCreate
	select {
	case ps.fetchQ <- fetchTask{leaf: oldTomb, preserve: &c, preserveAdvertise: false}:
		ps.inflight[newCreate.Path] = true
		ps.inflight[oldTomb.Path] = true
	default:
		// queue full; the coupled rename is dropped atomically — the old file stays live
		// on disk at the old path, the diff persists, a later reconcile retries.
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
			e.runFetch(ctx, ps, task)
		}
	}
}

// runFetch executes one (possibly coupled) puller task. A plain task just materialises
// leaf. A COUPLED task (preserve != nil) materialises the preserve leaf FIRST and only if
// that lands does it perform leaf's destructive step: a live winner overwrites the path
// (materialise); a tombstone leaf is removed on the loop AFTER the copy (a deferred
// applyTomb completion). If the preserve copy does NOT land, leaf is SKIPPED
// (conflictAbort) so the bytes about to be destroyed survive untouched for a rescan-paced
// retry — the enforced copy-before-destroy. This serves BOTH the conflict loser-copy
// (PR-3 (A)+(B)) and the rename create-before-old-removal (PR-5): for a rename, preserve
// is the NEW create (reusing the OLD bytes still on disk — zero network) and leaf is the
// OLD tombstone, so the create's local reuse ALWAYS precedes the old's removal regardless
// of lexicographic order (order-independent zero-network rename).
func (e *Engine) runFetch(ctx context.Context, ps *peerState, task fetchTask) {
	if task.preserve != nil {
		if !e.materialise(ctx, ps, *task.preserve, task.preserveAdvertise) {
			// The loser copy did not land (queue/disk/declined). Do NOT touch the shared
			// path. Clear the winner's inflight without re-driving an immediate reconcile;
			// the loser's bytes remain intact at the path for the next rescan to retry.
			e.report(completion{leaf: task.leaf, peer: ps.short, conflictAbort: true})
			return
		}
		if task.leaf.Deleted {
			// Winner is a tombstone (the delete won): the loser copy is safe ⇒ apply the
			// winning deletion now, on the loop, AFTER the copy (never before — fix (B)).
			e.report(completion{leaf: task.leaf, peer: ps.short, ok: true, applyTomb: true})
			return
		}
	}
	e.materialise(ctx, ps, task.leaf, task.advertise)
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
	if c.applyTomb {
		// A coupled conflict whose winner is a TOMBSTONE: the loser copy already landed,
		// so it is now safe to apply the winning deletion (remove the original, record the
		// tombstone, GC-handshake broadcast). Deferred to here so the destructive remove
		// NEVER precedes the copy (PR-3 (B)). applyTombstone is the same loop-side delete
		// run for any tombstone; inflight was keyed on this path by the coupled enqueue.
		e.applyTombstone(c.leaf)
		e.clearInflight(c.peer, c.leaf.Path)
		if ps := e.peerByShort(c.peer); ps != nil {
			e.reconcileWithPeer(ps)
		}
		return
	}
	if c.conflictAbort {
		// The loser copy did NOT land, so the winner step was skipped to protect the
		// loser's bytes (still at the original path). Clear inflight so a LATER reconcile
		// retries the coupled task; do not re-reconcile now — an immediate retry could
		// tight-spin while e.files still records the loser as live (PR-3). The periodic
		// rescan (source of truth, SR-11) re-drives it.
		e.clearInflight(c.peer, c.leaf.Path)
		return
	}
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
