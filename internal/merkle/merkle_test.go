package merkle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haider-toha/merkle-sync/internal/pathnorm"
	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// Golden structural hashes for the TestStructuralHash_GoldenVector fixture. These
// pin the byte-exact recipe (fields, widths, the 0x00/0x01 domain bytes); a change
// to any of them is a deliberate, fail-closed protocol change, never silent.
const (
	goldenLeafHex = "cfe2fd197396ead5a15db239cb78e8cee95e3b4a4dbf8d020cc85124e194f78b"
	goldenNodeHex = "44057f147934c37e232516387ca6de6d409e9816beacdb69f4adcd657cff88f4"
)

// leaf builds a regular-file FileInfo whose content hash is SHA-256 of seed, with a
// 1-counter version vector, for tree-shape tests.
func leaf(path, seed string) FileInfo {
	return FileInfo{
		Path:        path,
		ContentHash: HashBytes([]byte(seed)),
		Size:        uint64(len(seed)),
		Mode:        0o644,
		ModTimeNS:   1_000,
		Type:        TypeFile,
		Version:     protocol.NewVersionVector(map[protocol.ShortID]uint64{1: 1}),
	}
}

func mustTree(t *testing.T, set []FileInfo) *Tree {
	t.Helper()
	tr, err := BuildTree(set)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	return tr
}

// writeFile writes content to root/rel, creating parents.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestScanTwice_IdenticalRoot — WS-1 criterion 1 (SR-5): scanning the same folder
// twice yields the identical root hash.
func TestScanTwice_IdenticalRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/b/c.txt", "hello")
	writeFile(t, root, "a/b/d.txt", "world")
	writeFile(t, root, "a/e.txt", "third")
	writeFile(t, root, "top.txt", "root-level")
	writeFile(t, root, "z/deep/nested/file.bin", "deep content")

	set1, err := Scan(root)
	if err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	set2, err := Scan(root)
	if err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	r1 := mustTree(t, set1).RootHash()
	r2 := mustTree(t, set2).RootHash()
	if r1 != r2 {
		t.Errorf("two scans gave different roots:\n %x\n %x", r1, r2)
	}
	if len(set1) != 5 {
		t.Errorf("expected 5 files, got %d", len(set1))
	}
}

// TestOneByteChange_MinimalBranch — WS-1 criterion 3 (SR-5): a single leaf change
// re-hashes exactly that leaf's root->leaf branch; every off-path sibling hash is
// byte-identical.
func TestOneByteChange_MinimalBranch(t *testing.T) {
	base := []FileInfo{
		leaf("a/b/c.txt", "c-content"),
		leaf("a/b/d.txt", "d-content"),
		leaf("a/e.txt", "e-content"),
		leaf("f.txt", "f-content"),
	}
	before := mustTree(t, base).nodeHashes()

	// Change exactly one byte of one leaf (new content hash for a/b/c.txt).
	changed := make([]FileInfo, len(base))
	copy(changed, base)
	changed[0] = leaf("a/b/c.txt", "c-contenX")
	after := mustTree(t, changed).nodeHashes()

	mustChange := map[string]bool{"": true, "a": true, "a/b": true, "a/b/c.txt": true}
	for path, h := range before {
		switch {
		case mustChange[path]:
			if after[path] == h {
				t.Errorf("node %q on the changed branch did NOT change", path)
			}
		default:
			if after[path] != h {
				t.Errorf("off-path node %q changed (should be byte-identical)", path)
			}
		}
	}
	// The root must change.
	if before[""] == after[""] {
		t.Errorf("root hash did not change after a one-byte edit")
	}
}

// TestStructuralHash_GoldenVector — WS-1 criterion 4 (R-1 gate): the byte-exact
// leaf/node encodings and the domain-separated hashes are pinned. Reconstructs the
// grammar independently AND asserts a stable hash hex so any recipe change (a field,
// a width, the 0x00/0x01 domain byte) fails loudly.
func TestStructuralHash_GoldenVector(t *testing.T) {
	var ch [32]byte
	for i := range ch {
		ch[i] = byte(i)
	}
	fi := FileInfo{
		Path:        "a.txt",
		ContentHash: ch,
		Mode:        0o644, // not executable -> modeByte bit0 = 0
		Type:        TypeFile,
		Version:     protocol.NewVersionVector(map[protocol.ShortID]uint64{1: 1}),
	}

	// Independent reconstruction of leafEncoding.
	var wantLeaf []byte
	wantLeaf = append(wantLeaf, ch[:]...)
	wantLeaf = append(wantLeaf, 0x00)                   // modeByte: file, non-exec
	wantLeaf = append(wantLeaf, 0x00)                   // deleted = false
	wantLeaf = append(wantLeaf, 0x00, 0x01)             // vv count = 1
	wantLeaf = append(wantLeaf, 0, 0, 0, 0, 0, 0, 0, 1) // id = 1
	wantLeaf = append(wantLeaf, 0, 0, 0, 0, 0, 0, 0, 1) // value = 1
	if !bytes.Equal(leafEncoding(fi), wantLeaf) {
		t.Fatalf("leafEncoding mismatch:\n got %x\nwant %x", leafEncoding(fi), wantLeaf)
	}
	wantLeafHash := sha256.Sum256(append([]byte{0x00}, wantLeaf...))
	if leafHash(fi) != wantLeafHash {
		t.Fatalf("leafHash != SHA-256(0x00 || leafEncoding)")
	}

	// Node: a directory with the single child "a.txt".
	entries := []childEntry{{name: "a.txt", hash: leafHash(fi)}}
	var wantNode []byte
	wantNode = append(wantNode, 0, 0, 0, 1)         // childCount = 1
	wantNode = append(wantNode, 0x00, 0x05)         // nameLen = 5
	wantNode = append(wantNode, []byte("a.txt")...) // name
	lh := leafHash(fi)
	wantNode = append(wantNode, lh[:]...) // childHash
	if !bytes.Equal(nodeEncoding(entries), wantNode) {
		t.Fatalf("nodeEncoding mismatch:\n got %x\nwant %x", nodeEncoding(entries), wantNode)
	}
	wantNodeHash := sha256.Sum256(append([]byte{0x01}, wantNode...))
	if dirHash(entries) != wantNodeHash {
		t.Fatalf("dirHash != SHA-256(0x01 || nodeEncoding)")
	}

	// Stable-hex guard (pinned recipe).
	lhash := leafHash(fi)
	nhash := dirHash(entries)
	if got := hex.EncodeToString(lhash[:]); got != goldenLeafHex {
		t.Errorf("golden leaf hash drift:\n got %s\nwant %s", got, goldenLeafHex)
	}
	if got := hex.EncodeToString(nhash[:]); got != goldenNodeHex {
		t.Errorf("golden node hash drift:\n got %s\nwant %s", got, goldenNodeHex)
	}
}

// TestCrossPlatformRoot_RoundTrip — WS-1 criterion 4 (R-1 gate): a tree of
// Windows-hostile-named files survives a Mac->wire->Windows->wire->Mac round-trip
// with a bit-identical root. The canonical keys are unchanged by the OS escaping
// (only the on-disk form is escaped), and the structural hash uses only canonical
// fields, so both ends compute the same root.
func TestCrossPlatformRoot_RoundTrip(t *testing.T) {
	const winRoot = `C:\sync`
	keys := []string{
		"dir/a:b.txt", "dir/what?.dat", `dir/back\slash`, "café/résumé.txt",
		"COM1.txt", "trailingdot./inner.bin", "normal/file.go",
	}
	var set []FileInfo
	for i, k := range keys {
		key, err := pathnorm.CanonicalizeSlash(k)
		if err != nil {
			t.Fatalf("canonicalise %q: %v", k, err)
		}
		set = append(set, leaf(key, "content-"+string(rune('A'+i))))
	}
	macRoot := mustTree(t, set).RootHash()

	// Simulate the Windows side: each FileInfo crosses the wire, the path is
	// materialised+rescanned through the Windows escape boundary, and the set is
	// re-encoded for the trip back. The canonical key must be invariant.
	var winSet []FileInfo
	for _, fi := range set {
		enc, err := EncodeFileInfo(fi)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		got, _, err := DecodeFileInfo(enc)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		osPath := pathnorm.ToOSPath(winRoot, got.Path, pathnorm.Windows)
		backKey, err := pathnorm.FromOSPath(winRoot, osPath, pathnorm.Windows)
		if err != nil {
			t.Fatalf("FromOSPath(%q): %v", osPath, err)
		}
		if backKey != fi.Path {
			t.Errorf("canonical key changed across OS boundary: %q -> %q (osPath %q)", fi.Path, backKey, osPath)
		}
		got.Path = backKey
		winSet = append(winSet, got)
	}
	winRootHash := mustTree(t, winSet).RootHash()
	if macRootHash := macRoot; macRootHash != winRootHash {
		t.Errorf("cross-platform root differs:\n mac %x\n win %x", macRootHash, winRootHash)
	}
}

// TestModeByte_TwoState — the canonical 2-state mode changes the structural hash for
// exec and symlink leaves, but raw non-exec permission bits do not (XP-6).
func TestModeByte_TwoState(t *testing.T) {
	base := leaf("x", "same-bytes")

	exec := base
	exec.Mode = 0o755 // executable
	if leafHash(exec) == leafHash(base) {
		t.Errorf("exec bit did not change the structural hash")
	}

	// A non-exec permission-only difference (0644 vs 0640) must NOT change the hash
	// (raw mode is excluded; only the 2-state is hashed).
	permOnly := base
	permOnly.Mode = 0o640
	if leafHash(permOnly) != leafHash(base) {
		t.Errorf("non-exec permission change changed the structural hash (raw mode leaked)")
	}

	sym := base
	sym.Type = TypeSymlink
	if leafHash(sym) == leafHash(base) {
		t.Errorf("symlink type did not change the structural hash")
	}
}

// ----------------------------------------------------------------------------
// Incremental rebuild (Tree.Update) — MK-1 pillar 3, the previously-refuted claim.
// The earlier "fixed" verdict cited rehash() as an O(depth) incremental rebuild, but
// rehash()+BuildTree do a full O(n) recompute (MK-1 skeptic-1, REFUTED). Tree.Update
// is the real incremental path; these tests prove it (a) equals a full BuildTree
// byte-for-byte and (b) actually reuses off-path subtrees verbatim (pointer identity).
// ----------------------------------------------------------------------------

// upsertSet is the full-rebuild oracle for Tree.Update: a copy of set with fi
// replacing any existing entry at fi.Path, or appended if absent.
func upsertSet(set []FileInfo, fi FileInfo) []FileInfo {
	out := make([]FileInfo, 0, len(set)+1)
	replaced := false
	for _, e := range set {
		if e.Path == fi.Path {
			out = append(out, fi)
			replaced = true
		} else {
			out = append(out, e)
		}
	}
	if !replaced {
		out = append(out, fi)
	}
	return out
}

func sameHashMaps(a, b map[string][32]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// nodePtrs walks the tree returning canonical path -> *Node for every node (root is
// ""). White-box support for the branch-touch property: comparing two trees' node
// POINTERS proves which subtrees Update reused verbatim (same pointer) vs recomputed
// (fresh pointer).
func (t *Tree) nodePtrs() map[string]*Node {
	out := make(map[string]*Node)
	var walk func(prefix string, n *Node)
	walk = func(prefix string, n *Node) {
		out[prefix] = n
		for name, c := range n.children {
			child := name
			if prefix != "" {
				child = prefix + "/" + name
			}
			walk(child, c)
		}
	}
	if t != nil && t.root != nil {
		walk("", t.root)
	}
	return out
}

// TestUpdate_IncrementalEqualsFullBuild — Tree.Update(fi) yields a tree byte-for-byte
// identical (root + every node hash) to BuildTree(set with fi upserted), for an
// in-place leaf edit, a tombstone replace, and a brand-new path.
func TestUpdate_IncrementalEqualsFullBuild(t *testing.T) {
	base := []FileInfo{
		leaf("a/b/c.txt", "c-content"),
		leaf("a/b/d.txt", "d-content"),
		leaf("a/e.txt", "e-content"),
		leaf("f.txt", "f-content"),
	}
	cases := []struct {
		name string
		fi   FileInfo
	}{
		{"edit-existing-leaf", leaf("a/b/c.txt", "c-CHANGED")},
		{"tombstone-existing-leaf", func() FileInfo {
			fi := leaf("a/e.txt", "e-content")
			fi.Deleted = true
			fi.ContentHash = [32]byte{}
			return fi
		}()},
		{"new-leaf-existing-dir", leaf("a/b/new.txt", "brand-new")},
		{"new-leaf-new-deep-dir", leaf("x/y/z/deep.txt", "deep")},
		{"new-root-level-leaf", leaf("g.txt", "g-content")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			incr, err := mustTree(t, base).Update(tc.fi)
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			full := mustTree(t, upsertSet(base, tc.fi))
			if incr.RootHash() != full.RootHash() {
				t.Errorf("root mismatch:\n incr %x\n full %x", incr.RootHash(), full.RootHash())
			}
			if !sameHashMaps(incr.nodeHashes(), full.nodeHashes()) {
				t.Errorf("node-hash maps differ between incremental Update and full BuildTree")
			}
		})
	}
}

// TestUpdate_OffPathNodesReusedVerbatim — the previously-refuted O(depth) property:
// Update re-hashes ONLY the changed leaf's root->leaf chain and reuses every off-path
// subtree verbatim. Proven by POINTER IDENTITY: off-path nodes are the same *Node in
// the old and new tree; exactly the root->leaf chain is freshly allocated.
func TestUpdate_OffPathNodesReusedVerbatim(t *testing.T) {
	base := []FileInfo{
		leaf("a/b/c.txt", "c-content"),
		leaf("a/b/d.txt", "d-content"),
		leaf("a/e.txt", "e-content"),
		leaf("f.txt", "f-content"),
		leaf("g/h/i.txt", "i-content"),
	}
	t1 := mustTree(t, base)
	t2, err := t1.Update(leaf("a/b/c.txt", "c-CHANGED"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	onPath := map[string]bool{"": true, "a": true, "a/b": true, "a/b/c.txt": true}
	before := t1.nodePtrs()
	after := t2.nodePtrs()

	// t1 must be untouched: its own node set is unchanged and its root hash is stable
	// (Update is copy-on-write, never mutates the receiver — GR-5).
	if t1.RootHash() == t2.RootHash() {
		t.Fatalf("root did not change after an incremental edit")
	}

	for path, oldPtr := range before {
		newPtr, ok := after[path]
		if !ok {
			t.Errorf("node %q vanished from the updated tree", path)
			continue
		}
		if onPath[path] {
			if newPtr == oldPtr {
				t.Errorf("on-path node %q was NOT re-allocated (incremental rebuild must recompute it)", path)
			}
		} else {
			if newPtr != oldPtr {
				t.Errorf("off-path node %q was re-allocated (must be reused verbatim by pointer)", path)
			}
			if after[path].hash != before[path].hash {
				t.Errorf("off-path node %q hash changed (must be reused verbatim)", path)
			}
		}
	}

	// Count freshly-allocated nodes: exactly the 4 on-path nodes, nothing else.
	fresh := 0
	for path, newPtr := range after {
		if before[path] != newPtr {
			fresh++
		}
	}
	if fresh != len(onPath) {
		t.Errorf("incremental Update re-allocated %d nodes; expected exactly %d (the root->leaf chain)", fresh, len(onPath))
	}
}

// TestUpdate_NewPathCreatesIntermediateDirs — upserting into a non-existent deep path
// creates the intermediate directory nodes and still equals a full build, while reusing
// the unrelated siblings verbatim.
func TestUpdate_NewPathCreatesIntermediateDirs(t *testing.T) {
	base := []FileInfo{leaf("a/b/c.txt", "c"), leaf("f.txt", "f")}
	t1 := mustTree(t, base)
	t2, err := t1.Update(leaf("p/q/r.txt", "r"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full := mustTree(t, upsertSet(base, leaf("p/q/r.txt", "r")))
	if !sameHashMaps(t2.nodeHashes(), full.nodeHashes()) {
		t.Errorf("new-deep-path Update diverged from full build")
	}
	// The unrelated "a" subtree must be reused verbatim.
	if t1.nodePtrs()["a"] != t2.nodePtrs()["a"] {
		t.Errorf("unrelated subtree \"a\" was re-allocated by a new-path Update")
	}
}

// TestUpdate_FileDirConflict — Update enforces the same file-vs-directory invariants as
// BuildTree: a leaf cannot replace a directory node, and a path cannot traverse a file.
func TestUpdate_FileDirConflict(t *testing.T) {
	tr := mustTree(t, []FileInfo{leaf("a/b/c.txt", "c"), leaf("f.txt", "f")})

	// "a/b" is a directory — placing a leaf there is a conflict.
	if _, err := tr.Update(leaf("a/b", "x")); !errors.Is(err, ErrTreeConflict) {
		t.Errorf("leaf-over-directory: got %v, want ErrTreeConflict", err)
	}
	// "f.txt" is a file — traversing it as a directory is a conflict.
	if _, err := tr.Update(leaf("f.txt/inner.bin", "x")); !errors.Is(err, ErrTreeConflict) {
		t.Errorf("traverse-file-as-dir: got %v, want ErrTreeConflict", err)
	}
}

// TestUpdate_EquivalenceFuzz — randomized sequences of upserts (live + tombstone, leaf
// replaces + new intermediate dirs) applied incrementally via Tree.Update must, at
// every step, produce the byte-identical tree a full BuildTree of the same set
// produces. This is the strong guard against a silent incremental/full divergence.
func TestUpdate_EquivalenceFuzz(t *testing.T) {
	dirs := []string{"d0", "d1", "d2"}
	files := []string{"f0.txt", "f1.txt", "f2.txt", "f3.txt"}
	for _, seed := range []int64{1, 7, 42, 1234, 99999} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			randPath := func() string {
				depth := rng.Intn(len(dirs) + 1) // 0..len(dirs) dir components
				comps := make([]string, 0, depth+1)
				for i := 0; i < depth; i++ {
					comps = append(comps, dirs[rng.Intn(len(dirs))])
				}
				comps = append(comps, files[rng.Intn(len(files))]) // final is always a file name
				return strings.Join(comps, "/")
			}

			oracle := upsertSet(nil, leaf(randPath(), "seed"))
			tr := mustTree(t, oracle)
			for step := 0; step < 200; step++ {
				fi := leaf(randPath(), fmt.Sprintf("c-%d-%d", seed, step))
				if rng.Intn(4) == 0 { // ~25% tombstones
					fi.Deleted = true
					fi.ContentHash = [32]byte{}
				}
				oracle = upsertSet(oracle, fi)
				var err error
				tr, err = tr.Update(fi)
				if err != nil {
					t.Fatalf("step %d Update(%q): %v", step, fi.Path, err)
				}
				full := mustTree(t, oracle)
				if tr.RootHash() != full.RootHash() {
					t.Fatalf("step %d (%q): incremental root %x != full %x", step, fi.Path, tr.RootHash(), full.RootHash())
				}
				if !sameHashMaps(tr.nodeHashes(), full.nodeHashes()) {
					t.Fatalf("step %d (%q): incremental node hashes diverged from full build", step, fi.Path)
				}
			}
		})
	}
}

// TestUpdate_CrossPlatformKeys — Update works on canonical keys derived from
// Windows-hostile names (the XP-1/XP-2 set) and still matches a full build, so the
// incremental path shares the cross-platform determinism the full build has.
func TestUpdate_CrossPlatformKeys(t *testing.T) {
	hostile := []string{
		"dir/a:b.txt", "dir/what?.dat", `dir/back\slash`, "café/résumé.txt",
		"COM1.txt", "trailingdot./inner.bin", "normal/file.go",
	}
	keys := make([]string, len(hostile))
	for i, h := range hostile {
		k, err := pathnorm.CanonicalizeSlash(h)
		if err != nil {
			t.Fatalf("canonicalise %q: %v", h, err)
		}
		keys[i] = k
	}

	// Build a base from the first two, then incrementally upsert the rest, checking
	// equivalence to a full build at every step.
	oracle := []FileInfo{leaf(keys[0], "k0"), leaf(keys[1], "k1")}
	tr := mustTree(t, oracle)
	for i := 2; i < len(keys); i++ {
		fi := leaf(keys[i], "k"+string(rune('0'+i)))
		oracle = upsertSet(oracle, fi)
		var err error
		tr, err = tr.Update(fi)
		if err != nil {
			t.Fatalf("Update(%q): %v", keys[i], err)
		}
		if tr.RootHash() != mustTree(t, oracle).RootHash() {
			t.Errorf("hostile-key Update(%q) diverged from full build", keys[i])
		}
	}
}

// TestBuildTree_OrderIndependent — BuildTree yields the identical root + node hashes
// regardless of input order (WS-1 criterion 1; MK-1 skeptic-3 #2, previously untested
// with a permuted slice — TestScanTwice only exercised the deterministic scanner order).
func TestBuildTree_OrderIndependent(t *testing.T) {
	base := []FileInfo{
		leaf("a/b/c.txt", "c"), leaf("a/b/d.txt", "d"), leaf("a/e.txt", "e"),
		leaf("f.txt", "f"), leaf("g/h/i.txt", "i"), leaf("g/h/j.txt", "j"),
	}
	want := mustTree(t, base)
	for _, seed := range []int64{1, 2, 3, 100, 2026} {
		shuffled := make([]FileInfo, len(base))
		copy(shuffled, base)
		rng := rand.New(rand.NewSource(seed))
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got := mustTree(t, shuffled)
		if got.RootHash() != want.RootHash() {
			t.Errorf("seed %d: shuffled input changed the root hash", seed)
		}
		if !sameHashMaps(got.nodeHashes(), want.nodeHashes()) {
			t.Errorf("seed %d: shuffled input changed a node hash", seed)
		}
	}
}

// TestBuildTree_RejectsMalformedPathComponents — defense-in-depth path validation at
// the tree layer (MK-1 skeptic-3 #3/#4): an empty path, an empty intermediate
// component, and a component longer than the uint16 nameLen prefix are rejected by
// BOTH BuildTree and Tree.Update with ErrTreeConflict (never a silent "" child or a
// truncated, colliding name).
func TestBuildTree_RejectsMalformedPathComponents(t *testing.T) {
	oversized := strings.Repeat("a", 0x10000) // 65536 bytes > uint16 max
	bad := []struct {
		name string
		path string
	}{
		{"empty-path", ""},
		{"empty-intermediate-component", "a//b"},
		{"leading-slash", "/a"},
		{"trailing-slash", "a/"},
		{"oversized-component", oversized},
		{"oversized-intermediate", oversized + "/x.txt"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildTree([]FileInfo{leaf(tc.path, "x")}); !errors.Is(err, ErrTreeConflict) {
				t.Errorf("BuildTree(%q): got %v, want ErrTreeConflict", tc.path, err)
			}
			// Update on an empty tree must reject identically.
			var empty *Tree
			if _, err := empty.Update(leaf(tc.path, "x")); !errors.Is(err, ErrTreeConflict) {
				t.Errorf("Update(%q): got %v, want ErrTreeConflict", tc.path, err)
			}
		})
	}
}
