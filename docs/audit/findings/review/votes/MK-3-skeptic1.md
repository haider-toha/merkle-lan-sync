# Skeptic #1 vote — MK-3 (Leaf metadata for two-way sync)

- Vote date: 2026-06-29
- Verdict under challenge: FIXED
- My vote: **refuted = false** (the FIXED verdict stands)
- Confidence: high

## Attempted refutations and why each failed

1. **Is `leafEncoding` actually wired into the tree hash, or is the golden vector
   testing dead code?** Verified `node.go:34` `h.Write(leafEncoding(fi))` under the
   RFC-9162 leaf domain byte `0x00` (`node.go:30`, `doc.go:20-21`). The encoding the
   golden vector pins is the real input to `leafHash`. Not dead code.

2. **Is raw mode / mtime / size exclusion only asserted, not enforced?**
   `leafEncoding` (`codec.go:30-42`) never references `ModTimeNS` or `Size`, and emits
   only `canonicalModeByte()` for mode. Exclusion is structural (impossible to leak),
   not a runtime guard. `TestModeByte_TwoState` (`merkle_test.go:236`) positively
   proves a 0644→0640 perm-only change does NOT move the hash while a 0755 exec change
   does — directly closing the "raw mode leaked" regression.

3. **Size-exclusion has no isolated test.** Not a real hole: `Size` is fully
   determined by `ContentHash` (SHA-256 of bytes); there is no realistic leaf with
   identical content hash but different size. Redundant field, no convergence risk.

4. **No behavioural test isolating a VV-only difference in the structural hash**
   (same content_hash/mode/deleted, differing VV → differing leafHash). The tombstone
   test bumps VV *and* flips `deleted`, so it does not isolate VV. HOWEVER, the
   make-or-break property (VV is part of the hashed identity) is locked by
   `TestStructuralHash_GoldenVector` (`merkle_test.go:127`): the independently
   reconstructed `wantLeaf` includes the 18 VV bytes and asserts byte-equality, so a
   regression dropping VV from `leafEncoding` fails loudly. Property is protected; the
   missing isolated case is a cosmetic test-completeness nit, not a coverage gap.

5. **Out-of-scope by the finding's own text:** VV seeding / bumping-on-local-edit and
   pruning are explicitly deferred to the protocol-researcher (MK-3 §Recommendation,
   OQ-2/OQ-3). They are not part of what "FIXED" claims here.

## Evidence
- `internal/merkle/fileinfo.go:26-58` (struct + canonicalModeByte), `codec.go:30-42`
  (leafEncoding), `node.go:32-34` (wiring), `protocol/versionvector.go:222-230` (Encode).
- Tests re-run locally under `-race`: `TestStructuralHash_GoldenVector`,
  `TestModeByte_TwoState`, `TestTombstone_DistinctHash`, `TestScanner_SizeMtimePrefilter`
  all `--- PASS`. Run log: `docs/audit/runs/race-all.log:6` `ok internal/merkle`.

## Conclusion
Within the finding's scope (leaf field set + structural-hash include/exclude split),
the implementation matches field-for-field, is wired into the real hash path, and the
exclusion/inclusion properties are pinned by an independent golden vector and a
targeted 2-state-mode test, all green under `-race`. No missing-test, edge-case,
uncovered-path, or regression refutes FIXED.
