# Finding MK-5 — Rename handling in a path-keyed Merkle tree

- Slug: `MK-5-rename-detection`
- Phase / role: Phase 2 — merkle-researcher
- Status: complete; backs `decisions/merkle/rename-detection.md`
- Severity: **low** (v1 delete+create is correct and lossless; the cost is index
  churn + a local copy, both bounded and well understood; pure optimization deferred)
- Date / access date for all URLs: 2026-06-28
- Reads-first honoured: `docs/audit/findings/literature/{merkle-tree,rsync-algorithm}.md`,
  `docs/audit/rules/sync-rules.md` (SR-9/SR-10/SR-13), `crossplatform-rules.md` (XP-1)

## Claim

In a path-keyed Merkle tree a **rename surfaces as a delete + a create** (and a
**directory** rename as a subtree-sized delete+create that re-parents the subtree and
re-hashes every ancestor). v1 should **treat it exactly as that** — no rename
detection — because the deletion is already a lossless tombstone and the new path's
bytes are sourced by **content-addressed local reuse** (≈ zero wire bytes). A
hash-match rename heuristic is the natural *future* optimization (the leaf already
carries `content_hash`) but its only real win over v1 is skipping a local copy, not
worth its pairing ambiguity now.

## Evidence

- **A rename is two single-side leaves.** The differ (`MK-2`) sees `old.txt` absent
  (delete candidate) and `new.txt` present (create candidate); neither the hash nor
  the structure alone says "this is a move." A **directory** rename re-parents the
  whole subtree and changes every ancestor hash — the Dynamo "many key ranges change
  … requiring the tree(s) to be recalculated" analogue (Dynamo §4.7,
  https://www.cs.cornell.edu/courses/cs5414/2017fa/papers/dynamo.pdf, accessed
  2026-06-28; `literature/merkle-tree.md` §4.6).
- **rsync can't detect renames either** — it diffs a named pair, so a renamed file is
  a brand-new path with no basis → full re-send (`rsync-algorithm` §9.5). Shipping the
  same limitation is defensible; we *soften* the byte cost with content-addressed reuse
  that rsync's named-pair model lacks.
- **The deletion half is already lossless and safe.** The old path becomes a
  tombstone (`deleted=true` + bumped VV) that propagates and resists resurrection by a
  stale peer (`syncthing-bep` §4.5 `SetDeleted`; SR-9/SR-10). No special rename path is
  needed to avoid data loss.
- **The create half costs ≈ zero wire bytes** because the new path's blocks are found
  locally by `content_hash` before any network request (the content-addressed local
  reuse in `decisions/merkle/chunking-fixed-32kib-vs-cdc.md`; `syncthing-bep` §7.2).
  The residual cost is **index churn** (one tombstone + one new `FileInfo`, or a
  subtree's worth for a directory rename) and one local file copy — bounded by files
  moved, not bytes.
- **Why not OS file-identity (inode/Windows fileID):** non-portable across
  macOS↔Windows and has no place in the canonical forward-slash identity (XP-1,
  SR-13); it would inject OS-specific state into a deliberately OS-agnostic model and
  still not simplify cross-peer propagation (`decisions/merkle/rename-detection.md`
  Option C).

## Recommendation / impact

- **DECISION:** v1 = delete(tombstone)+create with content-addressed local reuse; no
  `RENAME` message type. Hash-match heuristic is the documented forward path behind
  `algo_version` if rename frequency on big trees becomes a measured pain
  (`merkle-tree` §4.6 A8; `rsync-algorithm` §13(4)). See
  `decisions/merkle/rename-detection.md`.
- **Implementers:** no new code beyond `internal/reconcile/{tombstone.go, transfer.go,
  apply.go}`. **Test:** directory rename ⇒ subtree reappears under the new path with
  bytes sourced locally, old paths left as non-resurrecting tombstones (SR-10).
- **Cross-refs:** SR-9/SR-10/SR-13, XP-1; AL-7; literature `merkle-tree` §4.6,
  `rsync-algorithm` §9.5.
