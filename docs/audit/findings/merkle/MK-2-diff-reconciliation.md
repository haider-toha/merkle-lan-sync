# Finding MK-2 — The diff / reconciliation algorithm: prune equal, recurse mismatching

- Slug: `MK-2-diff-reconciliation`
- Phase / role: Phase 2 — merkle-researcher
- Status: **fixed** (WS-1) — the prune-equal/recurse-mismatching differ is
  implemented in `internal/merkle/differ.go` with the "absence is ambiguous →
  single-sided candidate" rule and a white-box prune assertion test; the VV/tombstone
  resolver that consumes it stays in `internal/reconcile` (WS-4). Decision
  `docs/audit/decisions/ws1/tree-representation-and-differ.md`. Commit `182ff00a16868df05377cb3585b914aa1d59784e`.
- Severity: **medium** (foundational to SR-5; two correctness subtleties — "absence
  is ambiguous" and the honest complexity bound — are easy to get wrong)
- Date / access date for all URLs: 2026-06-28
- Reads-first honoured: `docs/audit/rules/{sync,go,crossplatform}-rules.md`,
  `docs/audit/findings/synthesis/problem-space-map.md`,
  `docs/audit/findings/literature/merkle-tree.md`,
  `.claude/skills/merkle-sync/SKILL.md` §2

## Claim

To find what differs between local tree `L` and a peer's tree `R`, walk both from
the root and **prune any subtree whose two hashes are equal** (skip it entirely);
recurse **only** into children whose hashes differ. A child present on **only one
side is NOT automatically a create or delete** — absence is ambiguous and must be
crossed against version vectors and tombstones before acting. The cost is
proportional to the **differences**, not the tree size (and not a strict
`O(log n)`); the walk is **read-only under `RLock` with zero I/O held**.

## The algorithm (specification)

```
diff(Lnode, Rnode) -> emits (path, Lleaf|nil, Rleaf|nil):
    if Lnode.hash == Rnode.hash:
        return                      # subtree identical on both sides — PRUNE, do not recurse
    if both are file leaves:
        emit(path, Lleaf, Rleaf)    # bytes and/or metadata differ — resolve via VV (§ below)
        return
    # both are directories whose hashes differ → recurse only where children differ
    for name in sorted(union(Lnode.children, Rnode.children)):
        lc, rc = Lnode.children[name], Rnode.children[name]   # either may be nil
        if lc != nil and rc != nil: diff(lc, rc)              # top-of-call hash check prunes equal subtrees
        elif lc != nil:             emit(name, lc, nil)       # only local has it  → SEND or remote-deleted?
        else:                       emit(name, nil, rc)       # only remote has it → FETCH or local-deleted?
```

After the structural diff yields a differing path, **direction is decided by the
version-vector comparison of the two `FileInfo`s** (not by the hash, not by mtime —
SKILL §3, SR-4):

- `Dominates` / `DominatedBy` (causal): the dominating side wins outright; the
  other applies the file (or tombstone). **No conflict copy.**
- `Equal` + equal `content_hash`: idempotent no-op (SR-3).
- `Concurrent` + differing content: **conflict** — keep both, loser renamed to a
  `.sync-conflict-*` copy, never deleted (SR-7). (Conflict policy itself is the
  protocol-researcher's lane; the differ only *flags* the path.)

## Evidence

- **Prune-equal-subtrees is the load-bearing property** shared by every Merkle
  diff: *"compare children hashes recursively until you reach mismatched leaves …
  sync only the data for mismatched leaves, not the entire dataset"* (deepengineering,
  *Merkle Trees and Anti-Entropy*,
  https://deepengineering.net/p/merkle-trees-and-anti-entropy-concepts; Apache
  Cassandra *AntiEntropy*,
  https://cwiki.apache.org/confluence/display/CASSANDRA2/AntiEntropy; both accessed
  2026-06-28; `literature/merkle-tree.md` §2.1, §5, AL-2). Equal subtree hash ⇒ the
  whole subtree is skipped by the **top-of-call** hash compare — this is the entire
  efficiency win and the SR-5 "one byte changed flips exactly that leaf's branch and
  the root, nothing else" acceptance.

- **Absence is ambiguous (the subtle correctness point).** A child on only one side
  may be a genuine create, a not-yet-propagated file, or the *other* side's
  completed deletion. BEP makes deletion a versioned event precisely so absence is
  never trusted (`syncthing-bep` §4.5, `SetDeleted`); a stale peer reconnecting with
  a pre-delete file must see the tombstone **dominate** it and delete locally, not
  resurrect it on everyone else (SR-9/SR-10; `version-vectors` §4.3 absent-counter-
  as-0 rule). So the differ **emits the single-sided node as a candidate** and hands
  it to the VV+tombstone resolver — it must not itself decide create-vs-delete.

- **Honest complexity (resolve the §6.2 over-claim).** "`O(log n)`" holds only for a
  *balanced binary* tree; a directory hierarchy is unbalanced (depth = FS nesting
  `D`, unrelated to `log N`) (`literature/merkle-tree.md` §4.5, §5; synthesis §6.2).
  The defensible bound: diff visits the union of root→changed-leaf branches —
  `O(d · D)` node-hash compares plus `O(b)` child enumeration per visited directory,
  and `O(1)` when the roots already match. **Test the property (SR-5), not a big-O
  assertion.**

- **Concurrency discipline (GR-5).** The diff is read-only over the tree: take
  `RLock`, snapshot the subtree/`FileInfo`s needed, release, *then* act. **Zero
  network or disk I/O while the lock is held** — doing I/O under the lock is the
  watcher↔sync-write deadlock the concurrency-critic hunts (GR-5; `rsync-or-librsync`
  ADOPT-2 "codec steps operate on copied-out buffers, the socket lives elsewhere").

## Recommendation / impact

- **ADOPT** the prune-equal/recurse-mismatching walk in `internal/merkle/differ.go`;
  the resolver (VV → apply/conflict/tombstone) lives in `internal/reconcile`.
- **Test obligations** (`differ_test.go`, `merkle_test.go`): equal roots ⇒ empty
  diff with **no** child recursion (assert prune happened); one byte changed ⇒
  exactly that leaf emitted + only its ancestor nodes visited (minimal-recursion);
  single-sided child ⇒ emitted as a candidate, not pre-classified; `-race` on a diff
  running concurrently with a watcher write.
- **Forward note (OQ-8):** Syncthing's `previous_blocks_hash` content-causality
  fast-forward (`syncthing-bep` §7.4) could refine conflict precision beyond pure
  VV, but is **safe to skip in v1** (treat any `Concurrent` VV as a conflict — eager
  but never lossy). Revisit only if spurious conflict copies are measured.
- **Cross-refs:** SR-3/4/5/7/9/10, GR-5; AL-2; literature `merkle-tree`,
  `version-vectors`, `syncthing-bep`.
