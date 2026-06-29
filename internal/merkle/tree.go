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
		if fi.Path == "" {
			return nil, fmt.Errorf("%w: empty path", ErrTreeConflict)
		}
		comps := strings.Split(fi.Path, "/")
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
