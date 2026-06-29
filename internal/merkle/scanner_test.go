package merkle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/haider-toha/merkle-sync/internal/protocol"
)

const selfID protocol.ShortID = 7

// TestSetDeleted_BumpsAndZeroes — WS-1 criterion 5: SetDeleted zeroes content, sets
// the tombstone flag, and bumps self's VV; the receiver is not mutated.
func TestSetDeleted_BumpsAndZeroes(t *testing.T) {
	orig := leaf("doc.txt", "live content")
	origVV := orig.Version.Copy()

	tomb := orig.SetDeleted(selfID)

	if !tomb.Deleted {
		t.Errorf("tombstone Deleted = false")
	}
	if tomb.ContentHash != ([32]byte{}) {
		t.Errorf("tombstone content hash not zeroed: %x", tomb.ContentHash)
	}
	if tomb.Size != 0 {
		t.Errorf("tombstone size = %d, want 0", tomb.Size)
	}
	if got := tomb.Version.Get(selfID); got != origVV.Get(selfID)+1 {
		t.Errorf("tombstone VV[self] = %d, want %d", got, origVV.Get(selfID)+1)
	}
	// receiver unchanged (copy-on-write)
	if !orig.Version.IsEqual(origVV) || orig.Deleted {
		t.Errorf("SetDeleted mutated the receiver")
	}
}

// TestTombstone_DistinctHash — WS-1 criterion 5 (SR-9): a tombstone's structural
// hash differs from its pre-delete leaf, so the deletion shows in the diff and
// changes the root.
func TestTombstone_DistinctHash(t *testing.T) {
	orig := leaf("doc.txt", "live content")
	tomb := orig.SetDeleted(selfID)
	if leafHash(orig) == leafHash(tomb) {
		t.Errorf("tombstone hash equals pre-delete leaf hash — deletion would be invisible")
	}

	// And the tree root flips when a leaf becomes a tombstone.
	before := mustTree(t, []FileInfo{orig, leaf("other.txt", "x")}).RootHash()
	after := mustTree(t, []FileInfo{tomb, leaf("other.txt", "x")}).RootHash()
	if before == after {
		t.Errorf("root did not change when a leaf became a tombstone")
	}
}

// TestSnapshotDiff_SynthesizesDeletion — WS-1 criterion 6 (MK-6, R-5): a file
// present in the snapshot but absent on disk becomes a synthesized tombstone with a
// bumped VV; the snapshot input is not mutated.
func TestSnapshotDiff_SynthesizesDeletion(t *testing.T) {
	fileA := leaf("a.txt", "A")
	fileB := leaf("b.txt", "B")
	prev := []FileInfo{fileA, fileB}
	cur := []FileInfo{fileA} // b.txt was deleted while the daemon was down

	out := SynthesizeDeletions(prev, cur, selfID)

	var tomb *FileInfo
	for i := range out {
		if out[i].Path == "b.txt" {
			tomb = &out[i]
		}
	}
	if tomb == nil {
		t.Fatalf("no entry synthesized for deleted b.txt; out=%v", out)
	}
	if !tomb.Deleted {
		t.Errorf("b.txt entry is not a tombstone")
	}
	if got, want := tomb.Version.Get(selfID), fileB.Version.Get(selfID)+1; got != want {
		t.Errorf("synthesized tombstone VV[self] = %d, want %d (bumped)", got, want)
	}
	// a.txt still live, exactly one of each path
	if len(out) != 2 {
		t.Errorf("expected 2 entries (a live, b tombstone), got %d", len(out))
	}
	// prev not mutated
	if prev[1].Deleted {
		t.Errorf("SynthesizeDeletions mutated the snapshot input")
	}
}

// TestSnapshotMissing_CreateOnly — WS-1 criterion 6 (MK-6 step 3): with no snapshot,
// no deletions are synthesized (create-only), so first run / snapshot loss never
// manufactures mass tombstones.
func TestSnapshotMissing_CreateOnly(t *testing.T) {
	cur := []FileInfo{leaf("a.txt", "A"), leaf("b.txt", "B")}
	out := SynthesizeDeletions(nil, cur, selfID)
	if len(out) != len(cur) {
		t.Fatalf("create-only should return cur unchanged, got %d entries", len(out))
	}
	for _, fi := range out {
		if fi.Deleted {
			t.Errorf("create-only synthesized a tombstone for %q", fi.Path)
		}
	}
}

// TestSnapshotDiff_CarriesTombstoneUnchanged — CDD-7.1: an existing tombstone still
// absent on disk is carried forward UNCHANGED (no re-bump), so a restart does not
// re-stamp a peer-authored tombstone as a fresh local delete.
func TestSnapshotDiff_CarriesTombstoneUnchanged(t *testing.T) {
	tomb := leaf("gone.txt", "x").SetDeleted(99) // authored by some other device 99
	prev := []FileInfo{tomb}
	cur := []FileInfo{} // still absent

	out := SynthesizeDeletions(prev, cur, selfID)
	if len(out) != 1 {
		t.Fatalf("expected the tombstone carried forward, got %d", len(out))
	}
	if !out[0].Version.IsEqual(tomb.Version) {
		t.Errorf("tombstone VV was changed on carry-forward: %v -> %v", tomb.Version, out[0].Version)
	}
	if out[0].Version.Get(selfID) != 0 {
		t.Errorf("carry-forward re-stamped self's counter: %d", out[0].Version.Get(selfID))
	}
}

// TestSnapshotDiff_RecreatedPathDropsTombstone: a path that reappears on disk over a
// prev tombstone is a legitimate new local create; the tombstone is dropped.
func TestSnapshotDiff_RecreatedPathDropsTombstone(t *testing.T) {
	tomb := leaf("d.txt", "old").SetDeleted(selfID)
	prev := []FileInfo{tomb}
	recreated := leaf("d.txt", "new-content")
	cur := []FileInfo{recreated}

	out := SynthesizeDeletions(prev, cur, selfID)
	if len(out) != 1 {
		t.Fatalf("expected just the recreated file, got %d entries", len(out))
	}
	if out[0].Deleted {
		t.Errorf("recreated path should be a live create, not a tombstone")
	}
}

// TestScanner_SizeMtimePrefilter — WS-1 criterion 7 (SR-4, AL-11): identity is
// content_hash, never size/mtime. The cheap RescanCandidate pre-filter is only a
// hint.
func TestScanner_SizeMtimePrefilter(t *testing.T) {
	// Two files with the SAME size but different content hash to different values:
	// identity is by content, not size.
	a := leaf("f", "AAAA")
	b := leaf("f", "BBBB")
	if a.Size != b.Size {
		t.Fatalf("test setup: sizes differ")
	}
	if a.ContentHash == b.ContentHash {
		t.Errorf("same-size different-content files must hash differently (identity is content)")
	}

	// Same content, different mtime -> same content hash (mtime never affects identity).
	c := leaf("f", "AAAA")
	c.ModTimeNS = a.ModTimeNS + 1_000_000
	if c.ContentHash != a.ContentHash {
		t.Errorf("mtime change altered content hash")
	}

	// The pre-filter: unchanged size+mtime -> skip; either changed -> candidate.
	if RescanCandidate(a, a.Size, a.ModTimeNS) {
		t.Errorf("unchanged size+mtime should NOT be a rescan candidate")
	}
	if !RescanCandidate(a, a.Size+1, a.ModTimeNS) {
		t.Errorf("changed size should be a rescan candidate")
	}
	if !RescanCandidate(a, a.Size, a.ModTimeNS+1) {
		t.Errorf("changed mtime should be a rescan candidate")
	}
}

// TestScan_SymlinkTypedLeafNotFollowed: a symlink is a typed leaf whose content is
// the hash of its target; it is not followed (mode-symlink-mapping, antipattern
// symlink-following). Skipped on platforms where symlink creation is unprivileged.
func TestScan_SymlinkTypedLeaf(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "target.txt", "real content")
	link := filepath.Join(root, "link")
	if err := os.Symlink("target.txt", link); err != nil {
		t.Skipf("cannot create symlink on this platform: %v", err)
	}

	set, err := Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var sym *FileInfo
	for i := range set {
		if set[i].Path == "link" {
			sym = &set[i]
		}
	}
	if sym == nil {
		t.Fatalf("symlink leaf not scanned; set=%v", set)
	}
	if sym.Type != TypeSymlink {
		t.Errorf("symlink Type = %v, want TypeSymlink", sym.Type)
	}
	// content is the hash of the (normalised) target path, not the target's bytes
	if sym.ContentHash != HashBytes([]byte("target.txt")) {
		t.Errorf("symlink content hash is not hash(target path) — link may have been followed")
	}
}
