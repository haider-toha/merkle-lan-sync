# Antipattern finding — a reassembled file is renamed into place without a whole-file hash check

- **Catalogue ID:** AP-05 · **finding slug:** `no-verify-after-reconstruct`
- **Source slug:** antipatterns
- **Phase / role:** Phase 2 — antipatterns-researcher (anti-slop pass)
- **Status:** open
- **Severity:** high
- **Proposes rule:** **SR-16 (PROPOSED)** — verify-after-reconstruct before rename
- **Reads-first honoured:** `docs/audit/rules/{sync,go,crossplatform}-rules.md`,
  `docs/audit/findings/synthesis/problem-space-map.md` (AL-12, R-4)
- **Access date for all URLs:** 2026-06-28

## Claim

A chunked/block transfer can reassemble into a **full-size file of the wrong
content** that still looks "complete": a misordered write, an off-by-one offset, a
dropped/duplicated block, a truncated last block, or a block that matched the
wrong expectation. If the engine renames the temp into place without recomputing
the **whole-file** hash, that corrupt file is accepted as the new truth — and its
leaf hash may even be recomputed from the corrupt bytes and broadcast. Synthesis
lists this as **AL-12 (verify-after-reconstruct)** but it is **not** an SR rule —
a gap.

## Wrong shape

```go
for _, blk := range plan {
    data := recv(blk)
    if sha256.Sum256(data) == blk.Hash {     // per-block check only
        writeAt(tmp, blk.offset, data)       // wrong offset / wrong order / missed block still possible
    }
}
os.Rename(tmp, dst)                          // trusted blindly — no whole-file verification
```

## Why it CORRUPTS data (not merely slow)

Per-block hashes do not prove the *assembly* is correct: blocks can be written at
the wrong offsets, a block can be silently skipped, the final short block can be
mis-sized, or (with fixed offsets) a planning bug can map block *i*'s bytes to
slot *j*. The result passes every per-block check yet is globally wrong, and the
atomic rename then makes the corruption durable and authoritative. rsync guards
exactly this with a post-transfer whole-file checksum:

> "Note that rsync always verifies that each transferred file was correctly
> reconstructed on the receiving side by checking a whole-file checksum that is
> generated as the file is transferred."
> ([rsync(1)](https://man7.org/linux/man-pages/man1/rsync.1.html))

This is also Merkle Sync's own AL-12 ("recompute whole-file SHA-256 == expected
`content_hash` before the atomic rename ... catches reassembly/ordering/
truncation/collision corruption", synthesis §1) and hardens risk **R-4**
(non-atomic/interrupted-transfer corruption). Because `content_hash` already *is*
the leaf identity, the check is free of new machinery.

## How to test (the failing assertion)

```go
cases := []corruption{dropBlock, dupBlock, swapTwoBlocks, truncLastBlock, flipByte}
for _, c := range cases {
    tmp := reconstructWith(c)                       // produces a full-size but wrong temp
    err := finalize(tmp, dst, expectedContentHash)
    assert.ErrorIs(t, err, ErrHashMismatch)
    assert.NoRename(t, dst)                          // dst keeps previous content
    assert.TempDiscarded(t, tmp)
}
```
Happy path: a correct reconstruction passes and renames exactly once.

## Correct approach (PROPOSED SR-16)

Before the atomic rename, recompute the whole-file SHA-256 over the finished temp
and assert it equals the expected `content_hash`. On mismatch: discard the temp
and refetch; **never** rename. Sequence becomes
`reassemble → whole-file verify → fsync → rename → dir fsync` (SR-1/SR-2 unchanged,
verify inserted before rename). Syncthing's receiver validates blocks before
writing and only updates state after the rename succeeds
(`lib/scanner/blocks.go:124-131` `Validate`; `folder_sendrecv.go` finish path
@v2.1.1, `docs/audit/findings/codebases/syncthing-source.md` §1d/§1e). Lands in
`internal/reconcile/transfer.go`.

This also fixes AP-06 (resuming a kept partial temp): the same whole-file verify
is the gate that makes a retained temp safe to reuse.

## Cross-references

- Catalogue: `docs/audit/rules/sync-antipatterns.md` AP-05 (+ AP-06).
- Synthesis: promotes **AL-12** to a rule; hardens **R-4**.
- Rules: composes with SR-1/SR-2 (atomic write) and SR-3 (idempotent,
  content-addressed apply) — SR-3 says "already have this content ⇒ no-op"; SR-16
  says "don't *commit* content you can't prove you reconstructed".
- Decision: `docs/audit/decisions/phase2/antipatterns-rule-gap-handling.md`.

## Sources (accessed 2026-06-28)

- rsync(1) (whole-file transfer verification) — https://man7.org/linux/man-pages/man1/rsync.1.html
- Syncthing source `blocks.go` `Validate` / finish path (@v2.1.1) — via `docs/audit/findings/codebases/syncthing-source.md`
