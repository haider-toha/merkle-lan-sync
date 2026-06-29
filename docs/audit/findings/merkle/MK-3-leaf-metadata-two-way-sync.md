# Finding MK-3 — What metadata each LEAF must carry for TWO-WAY sync (the critical one)

- Slug: `MK-3-leaf-metadata-two-way-sync`
- Phase / role: Phase 2 — merkle-researcher (the task's CRITICAL deliverable)
- Status: **fixed** (WS-1) — the two-way `FileInfo` leaf (content_hash + size + mode
  + mtime + version_vector + deleted) is implemented in `internal/merkle/fileinfo.go`
  with the structural hash committing to {content_hash, 2-state mode, deleted, VV} and
  excluding raw mode/mtime/size; tombstone-distinct-hash test green. Commit
  `__WS1_SHA__`. (Originally: complete; backs
  `decisions/merkle/leaf-shape-and-structural-hash.md`.)
- Severity: **high** (getting the leaf metadata wrong = silent data loss or
  permanent non-convergence; this is the load-bearing definition of two-way sync)
- Date / access date for all URLs: 2026-06-28
- Reads-first honoured: `docs/audit/rules/sync-rules.md` (SR-4/6/7/9/10),
  `docs/audit/findings/literature/{version-vectors,syncthing-bep}.md`,
  `docs/audit/decisions/phase0/merkle-leaf-shape.md`

## Claim

A **bare content hash** leaf supports a one-way **mirror** but **cannot** support
two-way **sync**. It tells you two files *differ*; it cannot tell you **who is
causally newer**, **whether the edits conflict (concurrent) or one side is merely
behind (causal)**, or **whether a file was deleted**. Two-way sync therefore
requires each leaf to be a `FileInfo` carrying — at minimum — a **version vector**
(causal order) and a **tombstone** (versioned deletion), plus `mode` (metadata-only
edits), with `mtime` as a tiebreaker-only field and `size` as a pre-filter. The
**structural hash must commit to the version vector and the deleted flag** (and
`content_hash`, `mode`) but **must exclude raw `mtime`/`size`**, or convergence
("equal root ⇔ converged") breaks.

## Why a bare hash is insufficient — the three questions it cannot answer

1. **Who is causally newer?** Two leaves differ; which edit happened-before which?
   A hash has no order. Using wall-clock `mtime` to order is the classic data-loss
   trap — laptops skew, NTP steps backward, "last-write-wins by timestamp silently
   drops data" (aphyr, *The trouble with timestamps*,
   https://aphyr.com/posts/299-the-trouble-with-timestamps, accessed 2026-06-28;
   `version-vectors` FM-5; SR-4).
2. **Conflict or just behind?** If A edited and B edited the same file, did they
   diverge (keep both) or did B simply not yet receive A's update (apply silently)?
   A hash cannot distinguish concurrent from causal — the exact distinction between
   "make a `.sync-conflict` copy" and "apply, no copy" (SR-7).
3. **Deleted, or never created, or must-be-removed?** A path missing on one side is
   ambiguous. Absence cannot propagate a deletion or resist its resurrection by a
   stale peer (SR-9/SR-10).

## The required leaf — field by field (the answer)

`FileInfo{path, content_hash, size, mode, mtime, version_vector, deleted}`
(`decisions/phase0/merkle-leaf-shape.md` Option C, hardened by
`decisions/merkle/leaf-shape-and-structural-hash.md`):

| field | answers which two-way question | evidence |
|---|---|---|
| `content_hash` (SHA-256 of bytes) | "do the bytes differ?" + the transfer/dedup key | `merkle-leaf-shape.md`; `syncthing-bep` §7 |
| `version_vector` (`map`/sorted-slice `device→counter`) | **who is newer + concurrent-vs-causal** — bump **only** your own counter, **only** on a confirmed *local* edit; merge = pointwise max; `Compare → {Equal,Dominates,DominatedBy,Concurrent}` | `version-vectors` §2; SR-4/SR-6 |
| `deleted` (tombstone) + bumped VV | **deletion as a versioned event**, so it propagates and *dominates* a stale peer's pre-delete VV (no resurrection) | `syncthing-bep` §4.5 `SetDeleted`; SR-9/SR-10 |
| `mode` (uint32) | a permission/exec-bit change is a real edit with **no** content change | `merkle-leaf-shape.md`; XP-6 |
| `mtime` (int64 ns) | **conflict tiebreaker ONLY** (older loses), **never** orders edits | SR-4/SR-7; `version-vectors` FM-5 |
| `size` (uint64) | cheap scanner pre-filter + transfer planning; redundant with `content_hash` for difference | `merkle-leaf-shape.md`; `rsync-or-librsync` ADOPT-1 |

### Version vectors specifically (why this exact mechanism)

A version vector is `device → counter`; a device increments **only its own** counter
and **only on a local write**; sync merges by **pointwise max** (Version vector,
Wikipedia, https://en.wikipedia.org/wiki/Version_vector, accessed 2026-06-28;
`version-vectors` §2). Comparison yields exactly: **A dominates B** (apply A, no
conflict), **B dominates A** (symmetric), or **neither** (concurrent ⇒ conflict).
This is chosen over:

- **Lamport timestamps** — a single scalar gives a total order but **cannot detect
  concurrency**, so it would force a winner on genuinely concurrent edits = silent
  loss (`version-vectors` §3).
- **Vector clocks** — same shape, but they increment on **every** event including
  message send/receive, which would manufacture spurious causality and fight the
  no-sync-loop invariant; version vectors increment **only on a data write** —
  exactly SR-6 (`version-vectors` §3, exhypothesi,
  https://www.exhypothesi.com/clocks-and-causality/, accessed 2026-06-28).
- **Per-file CRDT op-log** — correct but overkill for LAN file sync; balloons state
  (`merkle-leaf-shape.md` Option D).

The deletion case is what makes the absent-counter-as-0 rule load-bearing: a
tombstone whose VV adds the deleter's bumped counter **dominates** a stale peer's
pre-delete VV (`version-vectors` §4.3), so the file is removed on the stale peer and
**not** resurrected on the deleter — SR-10, the marquee long-lived sync bug
(Syncthing #10590 reported **8,591 conflicts** from ghost VV counters;
https://github.com/syncthing/syncthing/issues/10590, accessed 2026-06-28).

## What the structural hash includes / excludes — and why it matters here

**Include** `content_hash`, `mode`, `deleted`, `version_vector`; **exclude** raw
`mtime` and `size` (`decisions/merkle/leaf-shape-and-structural-hash.md` §D.1):

- Including the **version vector** in the hashed identity is what makes "converged ⇔
  identical root hash" (SR-5) true even for files whose **bytes match but whose
  history differed**, and for **tombstones** whose bytes are absent
  (`version-vectors` §7). The VV is therefore **part of the hashed leaf identity, not
  a side table** — which is why its serialization must be byte-deterministic and
  identical cross-platform (sorted by `id`, fixed-width, big-endian — SR-13).
- Excluding **`mtime`** is mandatory: it differs across machines for byte-identical
  files; hashing it manufactures spurious whole-tree diffs and the tree never
  converges (`merkle-tree` §4.4). Excluding **`size`** is free: it is fully
  determined by `content_hash`.

## Recommendation / impact

- **ADOPT** the `FileInfo` leaf with VV + tombstone as the minimum two-way identity;
  the structural-hash include/exclude split is non-negotiable for SR-5.
- **Implementers:** `internal/merkle/fileinfo.go` (the struct),
  `internal/protocol/versionvector.go` (VV with copy-on-write `Bump`/`Merge`/
  `Compare`, sorted-slice repr — `version-vectors` §8). VV counter **seeding**
  (pure-logical `prev+1` vs Syncthing's `max(prev+1, now)` floor) and **pruning /
  device-counter cleanup** are the **protocol-researcher's** decisions (OQ-2, OQ-3),
  not this finding's — flagged so they are not lost.
- **Cross-refs:** SR-4/5/6/7/9/10/13; AL-4/7; literature `version-vectors`,
  `syncthing-bep`; decisions `phase0/merkle-leaf-shape.md`,
  `merkle/leaf-shape-and-structural-hash.md`.
