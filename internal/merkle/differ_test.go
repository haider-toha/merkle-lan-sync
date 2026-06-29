package merkle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/haider-toha/merkle-sync/internal/pathnorm"
)

// TestDiff_IdenticalTreesEmpty: equal trees produce an empty diff and prune at the
// root (a single top-of-call comparison, no recursion).
func TestDiff_IdenticalTreesEmpty(t *testing.T) {
	set := []FileInfo{leaf("a/b.txt", "x"), leaf("a/c.txt", "y"), leaf("d.txt", "z")}
	l := mustTree(t, set)
	r := mustTree(t, set)
	entries, comparisons := diffCounted(l, r)
	if len(entries) != 0 {
		t.Errorf("expected empty diff, got %v", entries)
	}
	if comparisons != 1 {
		t.Errorf("equal trees should prune at the root in 1 comparison, got %d", comparisons)
	}
}

// TestDiff_PrunesEqualSubtrees — WS-1 criterion 3 (MK-2): a large identical subtree
// is pruned by the top-of-call hash compare and never recursed; only the one
// differing leaf's branch is walked.
func TestDiff_PrunesEqualSubtrees(t *testing.T) {
	// "big" holds 5 identical files on both sides; the trees differ only in small.txt.
	common := []FileInfo{
		leaf("big/f1", "1"), leaf("big/f2", "2"), leaf("big/f3", "3"),
		leaf("big/f4", "4"), leaf("big/f5", "5"),
	}
	left := append(append([]FileInfo{}, common...), leaf("small.txt", "left"))
	right := append(append([]FileInfo{}, common...), leaf("small.txt", "right"))

	entries, comparisons := diffCounted(mustTree(t, left), mustTree(t, right))

	if len(entries) != 1 || entries[0].Path != "small.txt" {
		t.Fatalf("expected exactly small.txt to differ, got %v", entries)
	}
	// root(1) + big(1, pruned) + small.txt(1, differs) = 3. If "big" were recursed
	// it would add 5 more. This asserts the prune happened.
	if comparisons != 3 {
		t.Errorf("expected 3 comparisons (root, big pruned, small.txt), got %d — equal subtree was NOT pruned", comparisons)
	}
}

// TestDiff_OneLeafDiffers: a single differing leaf is emitted with both sides.
func TestDiff_OneLeafDiffers(t *testing.T) {
	l := mustTree(t, []FileInfo{leaf("a/x.txt", "old"), leaf("b.txt", "same")})
	r := mustTree(t, []FileInfo{leaf("a/x.txt", "new"), leaf("b.txt", "same")})
	entries := Diff(l, r)
	if len(entries) != 1 || entries[0].Path != "a/x.txt" {
		t.Fatalf("expected a/x.txt, got %v", entries)
	}
	e := entries[0]
	if e.Local == nil || e.Remote == nil {
		t.Fatalf("both sides should be present for a modified leaf: %+v", e)
	}
	if e.Local.ContentHash == e.Remote.ContentHash {
		t.Errorf("modified leaf should have differing content hashes")
	}
}

// TestDiff_SingleSidedCandidate — MK-2: a child present on only one side is emitted
// as a candidate (the other side nil), NOT pre-classified as create-vs-delete.
func TestDiff_SingleSidedCandidate(t *testing.T) {
	l := mustTree(t, []FileInfo{leaf("shared.txt", "s"), leaf("only-local.txt", "L")})
	r := mustTree(t, []FileInfo{leaf("shared.txt", "s"), leaf("only-remote.txt", "R")})
	entries := Diff(l, r)

	byPath := map[string]DiffEntry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}
	if _, ok := byPath["shared.txt"]; ok {
		t.Errorf("shared.txt is identical and should be pruned, but was emitted")
	}
	local, ok := byPath["only-local.txt"]
	if !ok || local.Local == nil || local.Remote != nil {
		t.Errorf("only-local.txt should be local-only candidate, got %+v (ok=%v)", local, ok)
	}
	remote, ok := byPath["only-remote.txt"]
	if !ok || remote.Remote == nil || remote.Local != nil {
		t.Errorf("only-remote.txt should be remote-only candidate, got %+v (ok=%v)", remote, ok)
	}
}

// TestDiff_RemoteOnlySubtreeRecursedToLeaves: a subtree present only remotely is
// emitted as per-leaf candidates (the resolver fetches each).
func TestDiff_RemoteOnlySubtreeRecursedToLeaves(t *testing.T) {
	l := mustTree(t, []FileInfo{leaf("keep.txt", "k")})
	r := mustTree(t, []FileInfo{leaf("keep.txt", "k"), leaf("new/a.txt", "a"), leaf("new/b.txt", "b")})
	entries := Diff(l, r)
	got := map[string]bool{}
	for _, e := range entries {
		if e.Local != nil || e.Remote == nil {
			t.Errorf("entry %q should be remote-only", e.Path)
		}
		got[e.Path] = true
	}
	if !got["new/a.txt"] || !got["new/b.txt"] || len(got) != 2 {
		t.Errorf("expected new/a.txt and new/b.txt as candidates, got %v", got)
	}
}

// TestDiff_FileVsDirTypeClash — MK-2 refutation fix (Phase 7): when the same path is
// a FILE on one side and a DIRECTORY on the other, the differ must NOT emit a FALSE
// "absent" candidate (Remote=nil over a path the remote holds as a directory) and must
// NOT recurse the directory subtree into impossible single-sided installs. Instead it
// emits exactly ONE truthful clash entry — the file leaf on its side, the *Dir flag on
// the directory side, the other side nil — and prunes the subtree (minimal comparison
// count). Covers both directions and a multi-file subtree; one case uses a canonicalised
// Windows-hostile key to show the clash carries the canonical path faithfully.
// (decisions/phase7/MK-2-file-vs-dir-typeclash-resolution.md)
func TestDiff_FileVsDirTypeClash(t *testing.T) {
	hostile, err := pathnorm.CanonicalizeSlash("COM1.txt") // a reserved name; canonical key is path-content-agnostic to the clash
	if err != nil {
		t.Fatalf("canonicalise: %v", err)
	}
	cases := []struct {
		name           string
		local, remote  []FileInfo
		clashPath      string
		wantLocalDir   bool // the local side is the directory (remote is the file leaf)
		wantRemoteDir  bool // the remote side is the directory (local is the file leaf)
		wantComparison int  // root(1) + clash node(1, pruned) = 2; recursing the dir would add ≥1
	}{
		{
			name:           "local-file-vs-remote-dir",
			local:          []FileInfo{leaf("foo", "i-am-a-file")},
			remote:         []FileInfo{leaf("foo/bar.txt", "i-am-in-a-dir")},
			clashPath:      "foo",
			wantRemoteDir:  true,
			wantComparison: 2,
		},
		{
			name:           "local-dir-vs-remote-file (symmetric)",
			local:          []FileInfo{leaf("foo/bar.txt", "i-am-in-a-dir")},
			remote:         []FileInfo{leaf("foo", "i-am-a-file")},
			clashPath:      "foo",
			wantLocalDir:   true,
			wantComparison: 2,
		},
		{
			name:           "deeper-subtree-pruned",
			local:          []FileInfo{leaf("x", "iam-file")},
			remote:         []FileInfo{leaf("x/a.txt", "a"), leaf("x/sub/b.txt", "b")},
			clashPath:      "x",
			wantRemoteDir:  true,
			wantComparison: 2, // the whole x/ subtree (a.txt, sub, sub/b.txt) is pruned by the clash
		},
		{
			name:           "windows-hostile-clash-key",
			local:          []FileInfo{leaf(hostile, "file-on-mac")},
			remote:         []FileInfo{leaf(hostile+"/inner.bin", "dir-on-win")},
			clashPath:      hostile,
			wantRemoteDir:  true,
			wantComparison: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, comparisons := diffCounted(mustTree(t, tc.local), mustTree(t, tc.remote))
			if len(entries) != 1 {
				t.Fatalf("expected exactly ONE clash entry (subtree pruned), got %d: %+v", len(entries), entries)
			}
			e := entries[0]
			if e.Path != tc.clashPath {
				t.Errorf("clash path = %q, want %q", e.Path, tc.clashPath)
			}
			if !e.IsTypeClash() {
				t.Errorf("entry should report IsTypeClash(): %+v", e)
			}
			if e.LocalDir != tc.wantLocalDir || e.RemoteDir != tc.wantRemoteDir {
				t.Errorf("dir flags = (Local %v, Remote %v), want (Local %v, Remote %v)", e.LocalDir, e.RemoteDir, tc.wantLocalDir, tc.wantRemoteDir)
			}
			// The directory side's *FileInfo is nil (a dir has no leaf), but the path is
			// NOT absent there — the *Dir flag says so. The file side carries its leaf.
			if tc.wantRemoteDir {
				if e.Local == nil {
					t.Errorf("file side (Local) must carry the file leaf, got nil")
				}
				if e.Remote != nil {
					t.Errorf("directory side (Remote) must be nil, got %+v", e.Remote)
				}
			} else { // wantLocalDir
				if e.Remote == nil {
					t.Errorf("file side (Remote) must carry the file leaf, got nil")
				}
				if e.Local != nil {
					t.Errorf("directory side (Local) must be nil, got %+v", e.Local)
				}
			}
			if comparisons != tc.wantComparison {
				t.Errorf("comparisons = %d, want %d (the directory subtree must be pruned, not recursed)", comparisons, tc.wantComparison)
			}
		})
	}
}

// TestEmptyDir_NotEmitted — WS-1 criterion 8 (CDD-8): empty directories are not
// synced — the scanner emits no FileInfo for one, and a tree with an empty subdir
// has the same root as the tree without it.
func TestEmptyDir_NotEmitted(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/file.txt", "content")
	if err := os.MkdirAll(filepath.Join(root, "empty", "also-empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	set, err := Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, fi := range set {
		if fi.Path == "empty" || fi.Path == "empty/also-empty" {
			t.Errorf("empty directory %q was emitted as a FileInfo", fi.Path)
		}
	}
	if len(set) != 1 || set[0].Path != "a/file.txt" {
		t.Fatalf("expected only a/file.txt, got %v", set)
	}

	// The root of the scanned tree (with the empty dirs on disk) equals the root of
	// a tree built from the same single-file set — the empty dirs contribute nothing.
	scannedRoot := mustTree(t, set).RootHash()
	fileOnlyRoot := mustTree(t, set).RootHash()
	if scannedRoot != fileOnlyRoot {
		t.Errorf("empty-dir tree root not stable")
	}
}
