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
// ever touching a live device's history (vv-pruning-counter-cleanup, CDD-7.2). This
// exported method is the single-device entry point for an EXPLICIT device removal; the
// startup de-pair sweep (sweepDepairedCountersLocked) shares its copy-on-write core.
// For v1 (2-device, N6) the wired trigger is the between-runs -peer change picked up by
// the startup sweep; a runtime hot un-pair is the documented deferred path
// (decisions/phase7/PR-4-ghost-counter-wiring-and-test-obligations.md). It is the
// caller's responsibility to only invoke this for an explicitly removed device whose
// drop the remaining peer has agreed to (symmetric pruning).
func (e *Engine) DropCounter(id protocol.ShortID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dropCounterLocked(id) {
		e.rebuildLocked()
	}
}

// dropCounterLocked strips id's counter from every stored leaf's VV (copy-on-write)
// and reports whether anything changed. It does NOT rebuild — the caller rebuilds once
// so a multi-device sweep pays a single tree rebuild. Caller holds e.mu (write) OR is
// the single-goroutine New path. Shared by DropCounter and sweepDepairedCountersLocked.
func (e *Engine) dropCounterLocked(id protocol.ShortID) bool {
	changed := false
	for path, fi := range e.files {
		nv := dropFromVV(fi.Version, id)
		if !nv.IsEqual(fi.Version) {
			fi.Version = nv
			e.files[path] = fi
			changed = true
		}
	}
	return changed
}

// sweepDepairedCountersLocked removes, from every stored leaf's VV, the counter of any
// device that is neither self nor in the explicitly-declared paired set — i.e. a device
// the operator has DE-PAIRED (removed from the allow-list). This is the ack-gated,
// device-removal-ONLY ghost-counter prune (#10590 / FM-1): a de-paired device's counter
// is otherwise permanent, and once two live devices' histories differ on it NEITHER
// vector can dominate ⇒ a tombstone fails to win ⇒ the deleted file resurrects (as a
// clean copy or a conflict storm — the #10590 symptom). It runs ONLY when the paired set
// is KNOWN (Config.Peers != nil ⇒ pairedKnown); otherwise every counter is retained (the
// safe fallback, mirroring tombstone GC's retain-when-no-peer — never prune a counter we
// cannot prove is dead). By construction it never drops a live/paired device's counter,
// so SR-10 dominance among live devices is preserved (vv-pruning-counter-cleanup,
// Option A). Caller holds e.mu (write) OR is the single-goroutine New path; the caller
// rebuilds. Returns true if anything changed.
func (e *Engine) sweepDepairedCountersLocked() bool {
	if !e.pairedKnown {
		return false
	}
	// Collect the distinct de-paired device shorts present anywhere, then drop each over
	// all leaves (dead devices are rare and few; one pass per dead device).
	var dead []protocol.ShortID
	seen := make(map[protocol.ShortID]bool)
	for _, fi := range e.files {
		for _, c := range fi.Version {
			if !e.pairedShorts[c.ID] && !seen[c.ID] {
				seen[c.ID] = true
				dead = append(dead, c.ID)
			}
		}
	}
	changed := false
	for _, id := range dead {
		if e.dropCounterLocked(id) {
			changed = true
		}
	}
	return changed
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
