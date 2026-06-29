// Package merkle is the Merkle-tree state model of Merkle Sync: the source of truth
// for WHAT differs between two peers. It owns the per-path FileInfo leaf, the
// byte-exact domain-separated structural hash, the tree build + prune-equal differ,
// the filesystem scanner, and the local last-synced snapshot. It imports
// internal/protocol (the FileInfo carries a VersionVector) and internal/pathnorm
// (canonical keys); it is never imported by protocol, keeping the graph acyclic.
//
// # The leaf and the structural hash
//
// A FileInfo carries content_hash + size + mode + mtime + version_vector + deleted
// (MK-3). The STRUCTURAL hash — the thing the convergence oracle compares (SR-5) —
// commits to only content_hash, a portable 2-state mode {executable, isSymlink},
// the deleted flag, and the version vector, and EXCLUDES raw mode, mtime, and size
// (structural-hash-grammar-finalization decision, folding in XP-6). Excluding mtime
// (per-machine volatile) and raw mode (non-portable on NTFS) is what makes
// "converged <=> identical root hash" hold across Mac and Windows; including the
// version vector is what makes it hold even for same-bytes-different-history files
// and for tombstones.
//
// Hashing uses RFC 9162 domain separation — SHA-256(0x00 || leafEncoding) for a
// leaf, SHA-256(0x01 || nodeEncoding) for a directory node — which is required for
// second-preimage resistance (MK-1). A directory node hashes its children's sorted
// (name, childHash) pairs, so a single leaf change re-hashes exactly its root->leaf
// path and the root, nothing else.
//
// # Diff
//
// Diff walks two trees, prunes any subtree whose two node hashes are equal (never
// recursing it), and recurses only where children differ (MK-2). A child present on
// only one side is emitted as a CANDIDATE — absence is ambiguous, so the
// version-vector + tombstone resolver (WS-4) decides create-vs-delete, never the
// differ.
//
// # Scanner and snapshot
//
// Scan produces the FileInfo set for the on-disk tree (files + symlinks; empty dirs
// are not synced — CDD-8). SynthesizeDeletions diffs a fresh scan against a loaded
// snapshot to recover deletions that happened while the daemon was down (MK-6, R-5):
// in-snapshot/absent-on-disk becomes a tombstone; a missing/corrupt snapshot falls
// back to create-only. The snapshot is local-only gob (GR-7-permitted), written
// atomically.
package merkle
