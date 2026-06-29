# Review verdict — MK-1 (Merkle tree construction: hashing, domain separation, incremental rebuild)

- Reviewed: 2026-06-29 (Phase 6 reviewer)
- Finding: `docs/audit/findings/merkle/MK-1-tree-construction.md` (claimed `status: fixed`, WS-1, commit `182ff00`)
- **Verdict: FIXED** (pillars 1-2). **Pillar 3 (incremental rebuild): originally
  REFUTED by skeptic-1, resolved in Phase-7** — see the corrected evidence below and
  commit `920290f3354e28ada401579d54d17bbfd305829f`.
- **Phase-7 disposition (2026-06-29):** skeptic-1's REFUTED vote
  (`votes/MK-1-skeptic1.md`) was correct — the incremental-rebuild pillar was a false
  code citation over a non-existent path. Phase-7 round 1 implemented the real
  copy-on-write `merkle.Tree.Update` and the branch-touch/equivalence tests skeptic-1
  asked for (its option b), and folded in skeptic-3's #2/#3/#4 hardening. The verdict
  now stands on implemented + tested code for all three pillars. Decision:
  `docs/audit/decisions/phase7/MK-1-incremental-rebuild-resolution.md`.

## What was claimed
RFC-6962 `0x00`/`0x01` leaf/node domain separation; a byte-exact, length-prefixed,
sorted, big-endian grammar identical on Mac/Windows; n-ary, never duplicate-last;
incremental rebuild touching only the changed leaf's root→leaf path. Golden-vector +
cross-platform-root tests green.

## Evidence verified against code
- Domain separation is real and required, not field-layout-only:
  `internal/merkle/node.go:13-14` (`leafPrefix byte = 0x00`, `nodePrefix byte = 0x01`),
  `node.go:31-38` `leafHash = SHA-256(0x00 || leafEncoding)`,
  `node.go:41-48` `dirHash = SHA-256(0x01 || nodeEncoding)`.
- Byte-exact grammar: `internal/merkle/codec.go:30-42` `leafEncoding = content_hash[32] ||
  modeByte:u8 || deleted:u8 || vvEncoding` (name deliberately excluded — git model, comment
  l.23-29); `codec.go:56-65` `nodeEncoding = childCount:u32 || (nameLen:u16 || name ||
  childHash[32])`, big-endian via `binary.BigEndian.Append*`.
- Sorted children, n-ary, no duplicate-last: `node.go:61-73` sorts ascending by bytewise
  name compare before hashing; `nodeEncoding` writes exactly `len(children)` with no
  power-of-two padding.
- Incremental rebuild — **corrected (Phase-7).** The original verdict claimed
  "`node.go:56-74` `rehash()` recurses and re-hashes only along the changed branch."
  That was FALSE: `rehash()`/`BuildTree` do a full O(n) recompute (skeptic-1 REFUTED,
  `votes/MK-1-skeptic1.md`). The O(depth) incremental rebuild is now real:
  `merkle.Tree.Update` (`internal/merkle/tree.go`) re-hashes only the changed leaf's
  root→leaf chain and reuses every off-path subtree verbatim (shared by pointer),
  sharing `hashFromChildren` with `rehash` so it is byte-identical to a full build.
  Commit `920290f3354e28ada401579d54d17bbfd305829f`; decision
  `docs/audit/decisions/phase7/MK-1-incremental-rebuild-resolution.md`.

## Evidence verified against tests (all PASS under `-race`)
- `internal/merkle/merkle_test.go:127` `TestStructuralHash_GoldenVector` — independently
  reconstructs leaf/node encodings, asserts `SHA-256(0x00||leaf)` / `SHA-256(0x01||node)`,
  and pins stable hex (`goldenLeafHex`/`goldenNodeHex`, l.19-21) so any recipe drift fails.
- `merkle_test.go:89` `TestOneByteChange_MinimalBranch` — asserts EXACTLY the
  `{"", "a", "a/b", "a/b/c.txt"}` branch nodes change and every off-path node is
  byte-identical (the SR-5 minimal-branch property).
- `merkle_test.go:188` `TestCrossPlatformRoot_RoundTrip` — Windows-hostile keys
  (`a:b.txt`, `back\slash`, `COM1.txt`, …) produce a bit-identical root after a
  Mac→wire→Windows→wire→Mac trip.
- `merkle_test.go:60` `TestScanTwice_IdenticalRoot` — same folder twice ⇒ identical root.

### Pillar-3 (incremental rebuild) — Phase-7 tests proving the once-refuted claim
- `TestUpdate_OffPathNodesReusedVerbatim` — pointer-identity proof that `Tree.Update`
  re-allocates EXACTLY the changed leaf's root→leaf chain and reuses every off-path
  node verbatim (same `*Node`); this is the "off-path reused, only O(depth) recomputed"
  property the original verdict asserted but did not have code for.
- `TestUpdate_IncrementalEqualsFullBuild` + `TestUpdate_EquivalenceFuzz`
  (5 seeds × 200 random upserts incl. tombstones + new deep paths) — `Update` is
  byte-identical (root + every node hash) to `BuildTree` of the same set, at every step.
- `TestUpdate_NewPathCreatesIntermediateDirs`, `TestUpdate_FileDirConflict`,
  `TestUpdate_CrossPlatformKeys` (Windows-hostile canonical keys).
- `TestBuildTree_OrderIndependent` (skeptic-3 #2) and
  `TestBuildTree_RejectsMalformedPathComponents` (skeptic-3 #3/#4: empty path, empty
  intermediate component, oversized component → `ErrTreeConflict`).

## Run-log corroboration
- `docs/audit/runs/race-all.log:6` `ok internal/merkle`; reproduced fresh 2026-06-29
  (`go test ./internal/merkle -race` → all named tests `--- PASS`).

## Skeptical check
Tried to refute: the golden vector is not hollow — it rebuilds the grammar byte-for-byte
AND checks a pinned hex, so a silent encoding change cannot pass. The minimal-branch test
asserts off-path bytes are unchanged (not merely that the root changed). No gap found.
