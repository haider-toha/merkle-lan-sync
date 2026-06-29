# Decision: In-memory tree representation, build, and the prune-equal differ

- Area: WS-1 / merkle (tree.go, node.go, differ.go)
- Status: decided
- Date: 2026-06-29
- Decider: WS-1 implementer
- Consumes: MK-1 (incremental rebuild / one-leaf-change cost), MK-2 (prune-equal
  diff, "absence is ambiguous"), the structural-hash grammar decision, CDD-8
  (empty dirs not synced; oracle holds at quiescence).
- Implements plan WS-1 criteria 1, 3, 8.

## Context

The tree must (a) build deterministically from a `[]FileInfo` set so the same
folder scanned twice yields the identical root (SR-5, criterion 1); (b) let a
single leaf change re-hash exactly that leaf's branch and the root, nothing else
(criterion 3); (c) support a diff that prunes equal subtrees at the top-of-call
hash compare and never recurses into them (MK-2, criterion 3). Directories are
internal nodes; files/symlinks (incl. tombstones) are leaves. Empty directories are
not represented (CDD-8): a node exists only on a path to a leaf.

## Options — tree representation (scored 1-5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — keep only the flat `map[path]FileInfo`; compute the root by sorting all paths
- Correctness **3**: a global sort + re-hash on every change is O(n log n) per edit
  and does not naturally express "one leaf change touches only its branch"; the diff
  cannot prune subtrees without reconstructing structure anyway.
- Testability **2** for the minimal-branch property. Rejected.

### Option B — build a pointer node tree (dirs = internal nodes, files = leaves), hashes computed bottom-up at build (CHOSEN)
- Each `*Node` caches its structural hash; a dir's children are a
  `map[component]*Node`; the build groups FileInfos by path components.
- Correctness **5**: directly the git/Merkle model — a node hash depends only on its
  direct children, so a leaf change re-hashes exactly its root→leaf path and the
  differ prunes by the top-of-call hash compare (MK-1 §D.4, MK-2).
- Concurrency **5**: a built tree is an **immutable snapshot** (built under the
  caller's lock, then read-only); the differ takes two snapshots and does zero I/O
  (GR-5). Testability **5** (white-box node-hash walk for the minimal-branch +
  prune assertions). Cross-platform **5** (hashes are the byte-exact recipe).
- Chosen.

### Option C — a persistent/immutable balanced structure (e.g. a HAMT) for structural sharing
- Correctness **4** but Testability/complexity **2**: balanced-structure machinery
  is unrelated to a directory hierarchy (which is inherently unbalanced — MK-2's
  honest-complexity point) and adds surface with no v1 benefit at LAN scale.
  Rejected as premature (the forward path if rebuild cost is ever measured).

## Decision

Adopt **Option B**. Concretely:

- `type Tree struct { root *Node }`; `BuildTree(set []FileInfo) (*Tree, error)`:
  for each non-deleted-or-deleted leaf, split `Path` on `/`, descend/create
  intermediate **dir** nodes, attach the leaf at the final component. A duplicate
  path or a path that would make a file also a directory parent is an error
  (`ErrTreeConflict`). Tombstones (`Deleted == true`) **are** placed as leaves (they
  must be in the hash so a deletion changes the root and converged peers with the
  same tombstones get equal roots).
- `(*Node).rehash()` computes `SHA-256(0x00||leafEncoding)` for a leaf and
  `SHA-256(0x01||nodeEncoding)` for a dir (children sorted ascending by bytewise
  name), bottom-up, once at build. `(*Tree).RootHash() [32]byte`.
- Empty input ⇒ a root dir node with `childCount 0` ⇒ a well-defined empty-root
  hash. Empty *sub*directories never arise: a dir node is created only en route to a
  leaf (CDD-8).
- `Diff(local, remote *Tree) []DiffEntry` where
  `DiffEntry { Path string; Local, Remote *FileInfo }`: walk from roots; if the two
  node hashes are equal **return immediately** (prune — never recurse the subtree);
  if both are leaves emit `(path, L, R)`; if both are dirs recurse the **union** of
  child names; a child present on only one side is emitted as a *candidate*
  (`Local` or `Remote` nil) — never pre-classified as create-vs-delete (MK-2:
  absence is ambiguous; the VV/tombstone resolver in WS-4 decides direction). The
  differ is read-only and does zero I/O.

A white-box `diffCounted` returns the number of node-pairs compared, so tests can
assert that equal sibling subtrees are pruned (not walked).

## Rationale

- The pointer node tree is the literal git/Merkle model the findings specify; it is
  the representation in which the SR-5 acceptance properties (identical root on
  re-scan, minimal branch on one-byte change, prune-equal diff) are *naturally*
  expressible and testable, not emergent.
- An immutable built snapshot keeps the differ lock-free and I/O-free (GR-5), and
  keeps "absence is ambiguous" honest by emitting single-sided nodes as candidates
  for the WS-4 resolver rather than guessing.

## Consequences

- Drives `internal/merkle/{node.go, tree.go, differ.go}`.
- Tests: `TestScanTwice_IdenticalRoot`, `TestOneByteChange_MinimalBranch`,
  `TestDiff_PrunesEqualSubtrees`, `TestDiff_SingleSidedCandidate`,
  `TestEmptyDir_NotEmitted`, `TestBuildTree_ConflictRejected`.
- WS-4 consumes `Diff` and feeds each `DiffEntry` to the VV/tombstone resolver.
- Cross-refs: SR-5, GR-5; MK-1, MK-2, CDD-8.
