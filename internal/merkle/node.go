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

// rehash recomputes this node's structural hash bottom-up over the WHOLE subtree it
// is called on: a leaf hashes its leafEncoding under the leaf domain byte; a
// directory recurses into EVERY child and then hashes the name-sorted
// (name, childHash) list (hashFromChildren) under the node domain byte. BuildTree
// calls it once on the root, so a full build is O(n) — every node is (re)hashed.
//
// This is NOT the incremental rebuild: the O(depth) "re-hash only a changed leaf's
// root->leaf path, reuse every off-path sibling hash verbatim" recompute MK-1
// describes is Tree.Update (tree.go), which shares hashFromChildren so both paths
// produce byte-identical hashes. What rehash gives is the DETERMINISM behind WS-1
// criterion 3's OUTPUT property: a node's hash depends only on its direct children,
// so a one-byte change yields a hash difference confined to that leaf's branch —
// asserted by TestOneByteChange_MinimalBranch (two independent full builds compared).
func (n *Node) rehash() {
	if !n.isDir {
		n.hash = leafHash(*n.leaf)
		return
	}
	for _, c := range n.children {
		c.rehash()
	}
	n.hash = hashFromChildren(n.children)
}

// hashFromChildren computes a directory node's structural hash from its children's
// ALREADY-CACHED hashes (it does NOT recurse). It sorts children ascending by
// bytewise name compare, then hashes the (name, childHash) list under the node domain
// byte. It is the single shared directory-hash recipe used by BOTH the full rebuild
// (rehash, after it has recursed) and the incremental copy-on-write rebuild
// (Tree.Update / cowUpsert, which reuses every off-path child's cached hash verbatim)
// — so the two paths are byte-for-byte equivalent by construction. Every child's hash
// field must already be populated.
func hashFromChildren(children map[string]*Node) [32]byte {
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })

	entries := make([]childEntry, len(names))
	for i, name := range names {
		entries[i] = childEntry{name: name, hash: children[name].hash}
	}
	return dirHash(entries)
}
