# Decision: rename handling — treat as delete(tombstone)+create for v1

- Area: merkle / reconcile (Phase 2 — merkle-researcher)
- Status: decided
- Date: 2026-06-28
- Decider: merkle-researcher (Phase 2)
- Closes open question: **OQ-7** (synthesis problem-space map §4; roster "Rename
  detection (treat as delete+create, or hash-match heuristic)").

## Context

The diff/reconciliation algorithm (`findings/merkle/MK-2-diff-reconciliation.md`)
classifies a path present on **only one side** as a candidate create *or* delete,
then crosses it against version vectors and tombstones (SKILL §2). A **rename**
(`a/old.txt` → `a/new.txt`) surfaces in a path-keyed Merkle tree as **two** such
single-side leaves: `old.txt` now absent (looks like a delete) and `new.txt` now
present (looks like a create). A **directory** rename re-parents an entire subtree
and changes every ancestor hash — to a path-keyed tree it is a mass delete+create
(`merkle-tree` §4.6, the Dynamo "key ranges change" analogue). rsync has the same
limitation: it diffs a *named* pair, so a rename is a brand-new path with no basis
→ full re-send (`rsync-algorithm` §9.5). Whether to *detect* the rename (and emit a
cheap rename op) or just let it ride as delete+create is a consequential choice: it
affects the message catalogue, the tombstone interaction, and the byte cost.

## Options (scored 1–5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — Treat a rename as delete(tombstone)+create (CHOSEN, v1)

No rename detection. The deleted old path becomes a tombstone (SR-9); the new path
is a normal create. Content reaches the new path via the ordinary transfer path,
which **finds the bytes locally by `content_hash`** (the content-addressed local
reuse in `decisions/merkle/chunking-fixed-32kib-vs-cdc.md` step 3), so a pure
same-machine rename moves ~0 bytes over the wire even though the index churns.

- Correctness **5**: no heuristic, no false matches; uses only mechanisms already
  required for correctness (tombstones + content-addressed transfer). Converges by
  the same argument as any create/delete (SR-5/SR-9/SR-10).
- Concurrency-safety **5**: nothing new under the lock; reuses the existing apply
  path.
- Testability **5**: a rename test reduces to "delete propagates as a tombstone +
  create propagates + the new file's bytes were sourced locally, not re-fetched."
- Cross-platform **5**: pure path/content operations; no OS-specific identity.
- **Cost (accepted):** the **index** entries churn (one tombstone + one new
  `FileInfo`), and a directory rename churns the whole subtree's worth of index
  entries; the **byte** cost stays near zero thanks to content-addressed reuse. No
  user-visible data loss; the tombstone preserves history.

### Option B — Hash-match rename heuristic (detect a delete+create pair with equal `content_hash`, emit a rename)

When the diff yields a single-side delete of hash `H` and a single-side create of
the same `H` in the same reconciliation pass, treat it as a rename: move the file
locally and skip the transfer; optionally a `RENAME` message records intent.

- Correctness **4**: usually right, but the heuristic is **ambiguous** when two
  distinct files share `H` (e.g. duplicate files, or empty files all hashing equal)
  — a delete of one and an unrelated create of another could be mis-paired into a
  spurious "rename," and a copy-then-delete-original is indistinguishable from a
  move. It also needs a deterministic pairing rule so **both** peers infer the same
  rename, or they diverge.
- Concurrency-safety **4** · Testability **3** (combinatorial pairing cases,
  duplicate-hash edge cases) · Cross-platform **5**.
- **Deferred:** the leaf already carries `content_hash`, so this is the natural
  *future* optimisation — but its only real win over Option A is avoiding a **local**
  copy (the wire bytes are already ~0 under Option A's content reuse), which is cheap.
  Not worth the ambiguity surface in v1.

### Option C — OS file-identity tracking (inode / Windows fileID) to follow a rename

Track each file's OS-level identity (macOS inode, Windows `FileIndex`/`FileId`) and
recognise a rename as "same identity, new path."

- Correctness **3**: identity is reliable *within* one machine but says nothing
  across the two peers (the rename must still propagate as a path change on the
  wire); inode reuse after delete and fileID semantics differ.
- Cross-platform **2**: macOS inode vs Windows 64/128-bit fileID are different
  models, not portable, and have **no place in the canonical forward-slash identity**
  (XP-1, SR-13) — pulling OS identity into the model violates the project's
  cross-platform invariant.
- **Rejected:** non-portable, adds OS-specific state to a model that is deliberately
  OS-agnostic, and still doesn't simplify the cross-peer propagation.

## Decision

Adopt **Option A**: v1 treats a rename as **delete(tombstone) + create**, with the
new path's content sourced by **content-addressed local reuse** so the wire cost is
near zero for a same-machine rename. No `RENAME` message type in v1. Option B
(hash-match heuristic) is the documented forward path, enabled by the `content_hash`
already in every leaf and reachable behind the `algo_version` negotiation if it ever
warrants a dedicated op.

## Rationale

- **It is correct with zero new mechanism** — tombstones (SR-9/SR-10) and
  content-addressed transfer are already mandatory; a rename is just their
  composition. Adding nothing keeps the no-data-loss contract trivially intact.
- **The byte cost is already solved** by content-addressed local reuse, so the only
  thing Option B buys is skipping a local file copy — not worth its pairing
  ambiguity and the cross-peer "both must infer the same rename" burden in v1.
- **It refuses to import OS-specific identity** into the canonical model, protecting
  SR-13 / XP-1 convergence.
- rsync itself does not detect renames (`rsync-algorithm` §9.5); shipping the same
  limitation, but with content-addressed reuse softening the cost, is a defensible,
  well-understood v1 posture.

## Consequences

- No code beyond the existing tombstone + transfer paths
  (`internal/reconcile/{tombstone.go, transfer.go, apply.go}`); a **directory rename
  test** asserts the subtree re-appears under the new path with bytes sourced
  locally and the old path left as tombstones (no resurrection, SR-10).
- **Index-churn caveat for the planner:** a large-directory rename produces
  subtree-sized index churn (tombstone + create per file). This is the same
  incremental-rebuild cost noted in `decisions/merkle/leaf-shape-and-structural-
  hash.md` §D.4 and is bounded by the number of files moved, not the bytes.
- **Forward path (Option B):** if rename frequency on big trees becomes a measured
  pain, add a deterministic hash-match pairing + a `RENAME` op behind `algo_version`;
  the `content_hash` leaf field already supports it (`merkle-tree` §4.6 A8;
  `rsync-algorithm` §13(4)).
- Cross-references: SR-9/SR-10/SR-13, XP-1; AL-7; literature `merkle-tree` §4.6,
  `rsync-algorithm` §9.5; decisions `decisions/merkle/{leaf-shape-and-structural-
  hash,chunking-fixed-32kib-vs-cdc}.md`.
