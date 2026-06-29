package merkle

import (
	"errors"
	"fmt"
	"strings"
)

// ErrTreeConflict is returned by BuildTree when a path is used as both a file and a
// directory parent, when two FileInfos share a path, or when a path is empty.
var ErrTreeConflict = errors.New("merkle: tree path conflict")

// Tree is an immutable Merkle tree built from a FileInfo set. The root is a
// directory node; RootHash is the convergence oracle (SR-5).
type Tree struct {
	root *Node
}

// BuildTree builds the tree from a FileInfo set. Each FileInfo (including
// tombstones — they must be in the hash so a deletion changes the root) is placed
// as a leaf at its canonical path; intermediate directory nodes are created on the
// way. Empty directories never arise: a directory node exists only en route to a
// leaf (CDD-8). Node hashes are computed bottom-up once at build, so the same set
// always yields the identical root regardless of input order (WS-1 criterion 1).
func BuildTree(set []FileInfo) (*Tree, error) {
	root := &Node{isDir: true, children: make(map[string]*Node)}
	for _, fi := range set {
		comps, err := splitPathComponents(fi.Path)
		if err != nil {
			return nil, err
		}
		cur := root
		for i, comp := range comps {
			last := i == len(comps)-1
			if last {
				if _, ok := cur.children[comp]; ok {
					return nil, fmt.Errorf("%w: %q already present (dir-vs-file or duplicate)", ErrTreeConflict, fi.Path)
				}
				leaf := fi
				cur.children[comp] = &Node{name: comp, isDir: false, leaf: &leaf}
				break
			}
			child, ok := cur.children[comp]
			if !ok {
				child = &Node{name: comp, isDir: true, children: make(map[string]*Node)}
				cur.children[comp] = child
			}
			if !child.isDir {
				return nil, fmt.Errorf("%w: %q traverses a file component %q", ErrTreeConflict, fi.Path, comp)
			}
			cur = child
		}
	}
	root.rehash()
	return &Tree{root: root}, nil
}

// splitPathComponents splits a canonical forward-slash key into its components,
// rejecting a path the structural grammar cannot encode unambiguously: an empty path,
// an empty component (e.g. "a//b" or a leading/trailing "/"), or a component whose
// UTF-8 byte length exceeds the uint16 nameLen prefix nodeEncoding uses (codec.go) —
// such a component would silently truncate and could collide with a different name.
// The upstream canonicaliser is the primary guard; this is defense in depth at the
// tree layer (MK-1 skeptic-3 #3/#4). Shared by BuildTree and Tree.Update so both
// enforce identical path invariants.
func splitPathComponents(path string) ([]string, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty path", ErrTreeConflict)
	}
	comps := strings.Split(path, "/")
	for _, c := range comps {
		if c == "" {
			return nil, fmt.Errorf("%w: empty path component in %q", ErrTreeConflict, path)
		}
		if len(c) > 0xFFFF {
			return nil, fmt.Errorf("%w: path component (%d bytes) exceeds uint16 nameLen in %q", ErrTreeConflict, len(c), path)
		}
	}
	return comps, nil
}

// Update returns a NEW tree equal to BuildTree(the current set with fi upserted at
// fi.Path), but computed INCREMENTALLY: it re-hashes ONLY the directory nodes on
// fi.Path's root->leaf chain (depth+1 SHA-256 computations) and shares every off-path
// subtree with the receiver by pointer (copy-on-write). The receiver is NEVER mutated,
// so the old snapshot stays valid and safe to read concurrently under the reconcile
// RWMutex (GR-5, node.go:17-21). This is the O(depth) incremental rebuild MK-1
// describes — a single leaf change reuses every off-path sibling hash verbatim instead
// of the O(n) full BuildTree — and it shares hashFromChildren with rehash, so its
// output is byte-for-byte identical to a full rebuild (proven by
// TestUpdate_IncrementalEqualsFullBuild / TestUpdate_EquivalenceFuzz).
//
// fi is upserted as a leaf at fi.Path: it replaces an existing leaf there, or creates
// it plus any missing intermediate directory nodes. It returns ErrTreeConflict on the
// same path invariants BuildTree enforces — an empty path, an empty/oversized
// component (splitPathComponents), a component that traverses an existing FILE, or a
// final component already occupied by a DIRECTORY (file-vs-directory collision).
//
// Update does not remove nodes; a deletion is modelled as upserting a tombstone leaf
// (FileInfo.SetDeleted), which Update handles as an ordinary same-path leaf replace.
func (t *Tree) Update(fi FileInfo) (*Tree, error) {
	comps, err := splitPathComponents(fi.Path)
	if err != nil {
		return nil, err
	}
	var oldRoot *Node
	if t != nil {
		oldRoot = t.root
	}
	if oldRoot == nil {
		oldRoot = &Node{isDir: true, children: make(map[string]*Node)}
	}
	leaf := fi // own copy; the tree must not alias the caller's value
	newRoot, err := cowUpsert(oldRoot, comps, &leaf)
	if err != nil {
		return nil, err
	}
	return &Tree{root: newRoot}, nil
}

// cowUpsert returns a copy-on-write clone of the directory node dir with leaf upserted
// at comps (comps[0] is the next component under dir; comps is non-empty). Off-path
// children are shared with dir by pointer — their cached hashes reused verbatim — and
// only the clone plus the chain down to the leaf are freshly allocated and re-hashed
// (via hashFromChildren, the same recipe rehash uses). dir is never mutated.
func cowUpsert(dir *Node, comps []string, leaf *FileInfo) (*Node, error) {
	if !dir.isDir {
		return nil, fmt.Errorf("%w: %q traverses a file component", ErrTreeConflict, comps[0])
	}
	kids := make(map[string]*Node, len(dir.children)+1)
	for name, c := range dir.children {
		kids[name] = c // share off-path subtrees by pointer (copy-on-write)
	}
	clone := &Node{name: dir.name, isDir: true, children: kids}

	head := comps[0]
	if len(comps) == 1 {
		if existing, ok := kids[head]; ok && existing.isDir {
			return nil, fmt.Errorf("%w: %q is a directory, cannot place a leaf", ErrTreeConflict, head)
		}
		ln := &Node{name: head, isDir: false, leaf: leaf}
		ln.hash = leafHash(*leaf)
		kids[head] = ln
	} else {
		child, ok := kids[head]
		if !ok {
			child = &Node{name: head, isDir: true, children: make(map[string]*Node)}
		}
		newChild, err := cowUpsert(child, comps[1:], leaf)
		if err != nil {
			return nil, err
		}
		kids[head] = newChild
	}
	clone.hash = hashFromChildren(kids)
	return clone, nil
}

// RootHash returns the tree's root structural hash. Two fully-converged peers hold
// identical FileInfo sets and therefore bit-identical root hashes (SR-5).
func (t *Tree) RootHash() [32]byte {
	if t == nil || t.root == nil {
		var zero [32]byte
		return zero
	}
	return t.root.hash
}

// Lookup returns the FileInfo at a canonical path, if a leaf exists there.
func (t *Tree) Lookup(path string) (FileInfo, bool) {
	if t == nil || t.root == nil || path == "" {
		return FileInfo{}, false
	}
	cur := t.root
	for _, comp := range strings.Split(path, "/") {
		next, ok := cur.children[comp]
		if !ok {
			return FileInfo{}, false
		}
		cur = next
	}
	if cur.isDir {
		return FileInfo{}, false
	}
	return *cur.leaf, true
}

// nodeHashes walks the tree and returns a map of canonical path -> structural hash
// for every node (directories use their path; the root uses ""). It is white-box
// test support for the minimal-branch property (WS-1 criterion 3).
func (t *Tree) nodeHashes() map[string][32]byte {
	out := make(map[string][32]byte)
	var walk func(prefix string, n *Node)
	walk = func(prefix string, n *Node) {
		out[prefix] = n.hash
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
