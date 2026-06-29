# Review verdict — PR-2 (Version-vector comparison: concurrent vs causal)

- Reviewed: 2026-06-29 (Phase 6 reviewer)
- Finding: `docs/audit/findings/protocol/PR-2-version-vector-comparison.md` (claimed `status: fixed`, WS-0, commit `801d094`)
- **Verdict: FIXED**

## What was claimed
`Compare` → `{Equal, Dominates, DominatedBy, Concurrent}` with a missing entry read as 0;
`Bump` = pure `prev+1`; `Merge` = pointwise max; canonical sorted/no-zero `Encode`/`Decode`;
every op copy-on-write. Antisymmetry proof obligation; tombstone-dominates-absent.

## Evidence verified against code
- Four-way `Compare`, missing-entry-as-0, lock-step walk:
  `internal/protocol/versionvector.go:158-200` (`aGreater && bGreater ⇒ Concurrent`,
  trailing ids ⇒ greater, l.184-189).
- `Bump` = `prev+1`, copy-on-write insert: `versionvector.go:108-121`.
- `Merge` = pointwise max, fresh array: `versionvector.go:127-154`.
- Canonical normalisation + `Get` via binary search: `versionvector.go:68-93`.
- `Encode` (u16 count + sorted (id,val) big-endian) `:222-230`; `DecodeVersionVector`
  rejects truncation / zero-value counter / non-ascending ids `:237-267`.

## Evidence verified against tests (all PASS under `-race`)
- `internal/protocol/versionvector_test.go:56` `TestCompare_Antisymmetry` — named pairs +
  a 5000-case randomized property asserting `Compare(b,a)==reverse(Compare(a,b))` and
  `Equal ⟺ IsEqual` (the PR-2 §7 obligation).
- `versionvector_test.go:14` `TestCompare_Cases` — 11-case table incl.
  "tombstone dominates stale absent" (the SR-10 substrate).
- `:95` `TestMerge_PointwiseMax` (+ commutativity); `:123` `TestBump_PrevPlusOne`
  (insert front/middle/end); `:148` `TestOps_CopyOnWrite` + `:187` `_Race` (16 goroutines
  read while others derive — `-race` clean); `:223` `TestEncode_GoldenVector` (pinned hex
  `0002 0000…0001 0000…0002 …`); `:292` `TestDecodeVersionVector_RejectsMalformed`
  (6 malformed cases); `:264` `TestNewVersionVector_Normalize`.

## Run-log corroboration
- `docs/audit/runs/race-all.log:7` `ok internal/protocol`; fresh 2026-06-29 run all `--- PASS`.

## Skeptical check
The 5000-case antisymmetry property is the prerequisite for both peers independently
classifying every leaf the same way; it is not a hollow assertion. The COW race test under
`-race` rules out the Syncthing value-receiver aliasing footgun the finding cites. Golden
hex pins the wire grammar. No gap found.
