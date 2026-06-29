package merkle

import (
	"crypto/sha256"
	"sort"
)

// RFC 9162 §2.1.1 domain-separation prefixes. Hashing a leaf and a node differently
// is REQUIRED for second-preimage resistance (MK-1): without it a crafted leaf
// encoding could collide with a directory node's child-list bytes. One byte, not
// optional.
const (
	leafPrefix byte = 0x00
	nodePrefix byte = 0x01
)

// Node is one position in the in-memory Merkle tree. A directory is an internal
// node (isDir, children); a file/symlink/tombstone is a leaf (leaf != nil). The
// cached hash is the domain-separated structural hash, computed bottom-up once at
// build (tree-representation-and-differ decision). A built node is treated as an
// immutable snapshot shared read-only under the reconcile RWMutex (GR-5).
type Node struct {
	name     string           // path component (NFC); "" for the root
	isDir    bool             // directory (internal node) vs leaf
	leaf     *FileInfo        // the leaf's FileInfo (nil for a directory)
	children map[string]*Node // directory children, keyed by component name
	hash     [32]byte         // cached structural hash
}

// leafHash = SHA-256(0x00 || leafEncoding(fi)).
func leafHash(fi FileInfo) [32]byte {
	h := sha256.New()
	h.Write([]byte{leafPrefix})
	h.Write(leafEncoding(fi))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// dirHash = SHA-256(0x01 || nodeEncoding(sorted children)).
func dirHash(children []childEntry) [32]byte {
	h := sha256.New()
	h.Write([]byte{nodePrefix})
	h.Write(nodeEncoding(children))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// rehash computes this node's structural hash bottom-up: a leaf hashes its
// leafEncoding under the leaf domain byte; a directory sorts its children ascending
// by bytewise name compare, recurses, and hashes the (name, childHash) list under
// the node domain byte. Because a node's hash depends only on its DIRECT children,
// a single leaf change re-hashes exactly its root->leaf path; every off-path
// sibling hash is reused verbatim (MK-1 §D.4, WS-1 criterion 3).
func (n *Node) rehash() {
	if !n.isDir {
		n.hash = leafHash(*n.leaf)
		return
	}
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })

	entries := make([]childEntry, len(names))
	for i, name := range names {
		c := n.children[name]
		c.rehash()
		entries[i] = childEntry{name: name, hash: c.hash}
	}
	n.hash = dirHash(entries)
}
