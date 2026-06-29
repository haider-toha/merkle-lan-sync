package reconcile

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/haider-toha/merkle-sync/internal/merkle"
	"github.com/haider-toha/merkle-sync/internal/pathnorm"
	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// aWins reports whether a beats b in a concurrent (or equal-VV-but-differing-content)
// conflict. The decision is TOTAL and COMMUTATIVE in the sense that aWins(a,b) ==
// !aWins(b,a) for distinct leaves, so both peers independently pick the same winner
// from the same two leaves with no coordination — the prerequisite for symmetric
// convergence (PR-3 §4, SR-7). It depends only on intrinsic, replicated fields (never
// "local vs remote"), evaluated in priority order:
//
//  1. newer ModTimeNS wins (older mtime LOSES) — the ONLY use of mtime (SR-4);
//  2. else smaller authoring ShortID wins (larger ShortID LOSES) — derived purely
//     from the two version vectors (authorOf), matching SKILL §3 / Syncthing;
//  3. else (defensive) smaller content_hash wins — a strict total order over the
//     distinct 32-byte hashes a real conflict guarantees, so no tie survives.
//
// FileInfo is not ==-comparable (it carries a slice VV), so winner/loser are derived
// from this predicate rather than from struct equality.
func aWins(a, b merkle.FileInfo) bool {
	if a.ModTimeNS != b.ModTimeNS {
		return a.ModTimeNS > b.ModTimeNS
	}
	authA, okA := authorOf(a, b)
	authB, okB := authorOf(b, a)
	if okA && okB && authA != authB {
		return authA < authB
	}
	return bytes.Compare(a.ContentHash[:], b.ContentHash[:]) < 0
}

// winner returns the winning FileInfo (stays at the path); the other is the loser.
func winner(a, b merkle.FileInfo) merkle.FileInfo {
	if aWins(a, b) {
		return a
	}
	return b
}

// loserOf returns the conflict loser (renamed to a .sync-conflict copy, never deleted).
func loserOf(a, b merkle.FileInfo) merkle.FileInfo {
	if aWins(a, b) {
		return b
	}
	return a
}

// authorOf returns the ShortID by which a's version vector strictly exceeds b's —
// i.e. the device that authored a's divergence from b. For a Concurrent pair there
// is at least one such device; the largest is chosen for determinism. ok is false
// when a does not exceed b anywhere (e.g. equal vectors), in which case tier 2 is
// skipped and the tiebreak falls through to the content-hash backstop. This is a
// pure function of the two vectors, so both peers compute the same author for the
// same leaf (PR-3 "intrinsic fields").
func authorOf(a, b merkle.FileInfo) (protocol.ShortID, bool) {
	var best protocol.ShortID
	found := false
	for _, c := range a.Version {
		if c.Value > b.Version.Get(c.ID) {
			if !found || c.ID > best {
				best = c.ID
				found = true
			}
		}
	}
	return best, found
}

// conflictName builds the deterministic conflict-copy canonical key for a loser
// leaf: <stem>.sync-conflict-<YYYYMMDD>-<HHMMSS>-<authorHex>.<ext>. The timestamp is
// the loser's mtime in UTC TRUNCATED TO WHOLE SECONDS, so Mac-nanosecond vs
// Windows/FAT rounding and any local TZ yield the identical string on both peers
// (CDD-4). authorHex is the loser's authoring ShortID (the larger-ShortID loser, or
// the content-hash backstop's author) as 16 lowercase hex. The result is a canonical
// forward-slash NFC key; it is escaped for Windows only at the OS boundary (XP-3).
func conflictName(loser merkle.FileInfo, author protocol.ShortID) (string, error) {
	dir, base := path.Split(loser.Path)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	ts := time.Unix(0, loser.ModTimeNS).UTC().Truncate(time.Second).Format("20060102-150405")
	name := fmt.Sprintf("%s.sync-conflict-%s-%016x%s", stem, ts, uint64(author), ext)
	return pathnorm.CanonicalizeSlash(dir + name)
}
