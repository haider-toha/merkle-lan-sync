package reconcile

import (
	"github.com/haider-toha/merkle-sync/internal/merkle"
	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// planKind is the resolver's verdict for one differing path.
type planKind int

const (
	// planNoOp: nothing to do locally (we dominate, we are local-only, the contents
	// already match, or an advertised tombstone is for a path we never had — CDD-3).
	planNoOp planKind = iota
	// planInstall: install a single leaf at its path. A live leaf has its content
	// materialised (local-reuse-or-fetch); a tombstone leaf removes the file.
	planInstall
	// planConflict: keep BOTH versions — install the winner at the path (VV already
	// merged) and, unless the loser is a tombstone, the loser as a deterministic
	// .sync-conflict copy. Neither version's bytes are ever dropped (SR-7).
	planConflict
)

// plan is the total verdict resolve returns for a (local, remote) leaf pair. It is
// data only — the engine executes it under the lock / via the transfer layer.
type plan struct {
	kind    planKind
	install merkle.FileInfo  // planInstall: the leaf to install
	winner  merkle.FileInfo  // planConflict: the winner leaf, at the path, VV merged
	loser   *merkle.FileInfo // planConflict: the loser copy leaf (nil if loser is a tombstone)
	flag    string           // optional human flag, surfaced in logs
}

// resolve is the TOTAL decision procedure over the Compare x content matrix (PR-2 §4,
// CDD-3). It is a pure function of the two leaves (either may be nil for a single-
// sided path) and self's ShortID, so both peers reach the same verdict for the same
// inputs — the prerequisite for symmetric convergence (SR-5). It performs no I/O and
// takes no lock.
//
//	Compare(localV,remoteV) | content | verdict
//	------------------------|---------|------------------------------------------
//	(remote nil)            | -       | NoOp  (local-only; peer pulls from us)
//	(local nil)             | live    | Install remote (fetch a new file)
//	(local nil)             | delete  | NoOp  (unknown tombstone — never re-mint)
//	Equal                   | equal   | NoOp  (idempotent, SR-3)
//	Equal                   | differ  | Conflict (equal-VV backstop — never clobber)
//	Dominates               | any     | NoOp  (we are causally newer)
//	DominatedBy             | any     | Install remote (fetch live / apply tombstone)
//	Concurrent              | equal   | Install local, VV merged (MergeVV, no copy)
//	Concurrent              | differ  | Conflict (keep both, loser -> copy, SR-7)
func resolve(local, remote *merkle.FileInfo, self protocol.ShortID) plan {
	switch {
	case remote == nil:
		return plan{kind: planNoOp}
	case local == nil:
		if remote.Deleted {
			return plan{kind: planNoOp, flag: "unknown-tombstone"}
		}
		return plan{kind: planInstall, install: *remote}
	}

	sameContent := local.ContentHash == remote.ContentHash && local.Deleted == remote.Deleted

	switch local.Version.Compare(remote.Version) {
	case protocol.Equal:
		if sameContent {
			return plan{kind: planNoOp}
		}
		return conflictPlan(*local, *remote)
	case protocol.Dominates:
		return plan{kind: planNoOp}
	case protocol.DominatedBy:
		adopt := *remote
		adopt.Version = local.Version.Merge(remote.Version) // == remote (remote dominates); defensive
		return plan{kind: planInstall, install: adopt}
	default: // Concurrent
		if sameContent {
			keep := *local
			keep.Version = local.Version.Merge(remote.Version)
			return plan{kind: planInstall, install: keep}
		}
		return conflictPlan(*local, *remote)
	}
}

// conflictPlan builds the symmetric keep-both verdict. The winner stays at the path
// with the merged VV (both peers compute the same winner + same merged VV ⇒ the path
// converges); the loser, if it has content, becomes a .sync-conflict copy at a
// deterministic new path (both peers mint/receive the identical leaf ⇒ the copy
// converges). A losing tombstone yields no copy (no bytes to preserve). No content is
// ever dropped (SR-7). On the theoretical conflict-name error, it degrades to NoOp so
// nothing is lost (the conflict is left unresolved + flagged, never destructive).
func conflictPlan(local, remote merkle.FileInfo) plan {
	w := winner(local, remote)
	l := loserOf(local, remote)

	win := w
	win.Path = local.Path
	win.Version = local.Version.Merge(remote.Version)

	p := plan{kind: planConflict, winner: win, flag: "conflict"}
	if l.Deleted {
		return p // losing tombstone: keep the winner, nothing to preserve
	}
	name, err := conflictName(l, loserAuthor(l, w))
	if err != nil {
		return plan{kind: planNoOp, flag: "conflict-name-error: " + err.Error()}
	}
	cp := l
	cp.Path = name // a new path; keep the loser's own VV (content-addressed; converges)
	p.loser = &cp
	return p
}

// loserAuthor picks the deterministic authoring ShortID used in the loser's conflict-
// copy suffix: the device by which the loser diverged from the winner, or — when the
// vectors are equal (the equal-VV backstop, where there is no distinguishing author)
// — the largest ShortID present in the loser's vector (0 if empty). Both peers compute
// the same value because the loser leaf is identical on both.
func loserAuthor(l, w merkle.FileInfo) protocol.ShortID {
	if a, ok := authorOf(l, w); ok {
		return a
	}
	return maxShortID(l.Version)
}

func maxShortID(vv protocol.VersionVector) protocol.ShortID {
	var m protocol.ShortID
	for _, c := range vv {
		if c.ID > m {
			m = c.ID
		}
	}
	return m
}
