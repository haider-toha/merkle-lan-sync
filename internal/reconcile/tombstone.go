package reconcile

import (
	"github.com/haider-toha/merkle-sync/internal/merkle"
	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// canGC reports whether a tombstone may be garbage-collected given a peer's last
// advertised index: ONLY once that peer holds the deletion — it advertises a
// tombstone for the same path whose version vector dominates-or-equals the
// tombstone's (the peer has applied the delete). Until then the peer might still be
// carrying a pre-delete version, so GC'ing would let it resurrect the file (SR-10).
// This is the exact ack-gate from tombstone-retention-gc (Option A); pure + testable.
func canGC(tomb merkle.FileInfo, peerIndex map[string]merkle.FileInfo) bool {
	pf, ok := peerIndex[tomb.Path]
	if !ok || !pf.Deleted {
		return false // peer hasn't advertised the tombstone — it may still hold the file
	}
	switch pf.Version.Compare(tomb.Version) {
	case protocol.Dominates, protocol.Equal:
		return true
	default:
		return false // Concurrent / DominatedBy: not yet a mutual, equal acknowledgement
	}
}

// gcTombstonesLocked removes every tombstone that ALL currently-known peers have
// acknowledged (canGC). If no peer is known it retains everything — never a timer
// (tombstone-retention-gc: fall back to retain, never to a TTL). Symmetric by
// construction: each peer GCs only after it observes the other's ack, so the two
// tombstone sets converge rather than diverging into an FM-3 false conflict. Caller
// holds e.mu (write). Returns true if anything changed (so the caller rebuilds).
func (e *Engine) gcTombstonesLocked() bool {
	if len(e.peers) == 0 {
		return false
	}
	changed := false
	for path, fi := range e.files {
		if !fi.Deleted {
			continue
		}
		ackedByAll := true
		for _, ps := range e.peers {
			if !canGC(fi, ps.index) {
				ackedByAll = false
				break
			}
		}
		if ackedByAll {
			delete(e.files, path)
			changed = true
		}
	}
	return changed
}

// DropCounter strips a de-paired device's counter from every stored leaf's version
// vector (copy-on-write) and rebuilds — the ack-gated, device-removal-ONLY pruning
// that kills the ghost-counter resurrection/conflict-storm class (#10590) without
// ever touching a live device's history (vv-pruning-counter-cleanup, CDD-7.2). For
// v1 (2-device, N6) the only trigger is un-pairing the last peer; never time/size.
// It is the caller's responsibility to only invoke this for an explicitly removed
// device whose drop the remaining peer has agreed to (symmetric pruning).
func (e *Engine) DropCounter(id protocol.ShortID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	changed := false
	for path, fi := range e.files {
		nv := dropFromVV(fi.Version, id)
		if !nv.IsEqual(fi.Version) {
			fi.Version = nv
			e.files[path] = fi
			changed = true
		}
	}
	if changed {
		e.rebuildLocked()
	}
}

// dropFromVV returns a copy of vv with id's counter removed (copy-on-write; the
// receiver is never mutated). The result stays canonical (sorted, no zero values).
func dropFromVV(vv protocol.VersionVector, id protocol.ShortID) protocol.VersionVector {
	var out protocol.VersionVector
	for _, c := range vv {
		if c.ID != id {
			out = append(out, c)
		}
	}
	return out
}
