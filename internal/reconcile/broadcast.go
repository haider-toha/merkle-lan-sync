package reconcile

import (
	"sort"

	"github.com/haider-toha/merkle-sync/internal/merkle"
	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// snapshotFilesLocked returns a deterministically-ordered copy of the current
// FileInfo set (live leaves + tombstones), sorted by path. Caller holds e.mu (read
// or write). The copy lets the caller encode/iterate without holding the lock (GR-5).
func (e *Engine) snapshotFilesLocked() []merkle.FileInfo {
	out := make([]merkle.FileInfo, 0, len(e.files))
	for _, fi := range e.files {
		out = append(out, fi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// buildIndex encodes the full current FileInfo set as an INDEX message (the snapshot
// a peer diffs against on connect). Tombstones are included — a deletion must be in
// the index so the peer can apply/ack it (SR-9).
func (e *Engine) buildIndex() (protocol.Index, error) {
	e.mu.RLock()
	set := e.snapshotFilesLocked()
	e.mu.RUnlock()
	body, err := merkle.EncodeFileInfos(set)
	if err != nil {
		return protocol.Index{}, err
	}
	return protocol.Index{FolderID: e.folderID, Count: uint32(len(set)), Body: body}, nil
}

// orderCreatesBeforeDeletes sorts a delta so live leaves (creates/edits) precede
// tombstones (deletes). For a rename emitted as create-new + delete-old in one batch,
// this means a peer sees the new path BEFORE the old path's tombstone, so it never
// transiently deletes the only copy (PR-5; combined with content-addressed local
// reuse the new path also costs zero network). Stable secondary sort by path keeps
// the batch deterministic.
func orderCreatesBeforeDeletes(delta []merkle.FileInfo) {
	sort.SliceStable(delta, func(i, j int) bool {
		if delta[i].Deleted != delta[j].Deleted {
			return !delta[i].Deleted // live (false) sorts before deleted (true)
		}
		return delta[i].Path < delta[j].Path
	})
}

// broadcastUpdate sends a set of changed leaves to every connected peer as an
// INDEX_UPDATE. It is called ONLY after confirmed local authorship (SR-6) — applying
// a received file never calls it (SR-8), which is the load-bearing half of the
// no-sync-loop invariant (PR-6). Send is non-blocking (buffered-with-shed), so a
// slow peer never wedges the caller (CDD-1).
func (e *Engine) broadcastUpdate(changed []merkle.FileInfo) {
	if len(changed) == 0 {
		return
	}
	orderCreatesBeforeDeletes(changed)
	body, err := merkle.EncodeFileInfos(changed)
	if err != nil {
		e.logf("broadcast encode failed: %v", err)
		return
	}
	msg := protocol.IndexUpdate{FolderID: e.folderID, Count: uint32(len(changed)), Body: body}

	e.mu.RLock()
	conns := make([]peerConn, 0, len(e.peers))
	for _, ps := range e.peers {
		conns = append(conns, ps.conn)
	}
	e.mu.RUnlock()

	for _, c := range conns {
		c.Send(msg) // non-blocking; a full outbound buffer sheds that peer, never blocks us
	}
}
