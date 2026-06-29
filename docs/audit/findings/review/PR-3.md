# Review verdict — PR-3 (Conflict-copy policy + deterministic, symmetric tiebreaker)

- Reviewed: 2026-06-29 (Phase 6 reviewer)
- Finding: `docs/audit/findings/protocol/PR-3-conflict-copy-policy-and-tiebreaker.md` (claimed `status: fixed`, WS-4, commit `af12de0`)
- **Verdict: FIXED** (with an implementation-detail note on the tier-2 author rule)

## What was claimed
On a `Concurrent` differing-content pair, keep both: winner stays at the path, loser
renamed to a deterministic `.sync-conflict-<UTC-date>-<UTC-time>-<deviceID>.<ext>`, never
deleted. Winner `W` is total + commutative (depends only on intrinsic fields), so both
peers pick the same loser and converge with no data loss.

## Evidence verified against code
- Total + commutative winner: `internal/reconcile/conflict.go:30-40` `aWins`
  (tier1 newer mtime wins; tier2 smaller author ShortID wins; tier3 smaller content_hash
  wins — strict total order); `:43-56` `winner`/`loserOf`.
- Deterministic conflict name, UTC truncated to whole seconds:
  `conflict.go:86-93` `conflictName` (`time.Unix(0,mtime).UTC().Truncate(time.Second)`,
  `%016x` author, canonicalised key).
- Symmetric keep-both, loser never dropped, degrade-to-NoOp on name error (never
  destructive): `internal/reconcile/apply.go:93-113` `conflictPlan`; the engine enqueues
  the loser copy before the winner (`engine.go:662-686` `execute`) and advertises the copy
  so it syncs as a normal file (`engine.go:800-806`).

## Evidence verified against tests (all PASS under `-race`)
- `internal/reconcile/reconcile_test.go:216` `TestW_Commutative` — 5000 random pairs:
  `aWins` antisymmetric and `winner(a,b).hash == winner(b,a).hash`.
- `:237/266/279` `TestConflict_CopyNameDeterministic` (symmetric name across swapped
  arguments + UTC-truncation) / `_TZIndependent` (TZ=LA vs Kolkata identical) /
  `_WindowsRoundTrips` (hostile names round-trip Mac→Win→Mac, no reserved char on disk).
- `:142` `TestResolver_Matrix` (11-case Compare×content totality);
  `:185` `TestResolver_EqualVVDiffContentConflicts` (equal-VV differing-content ⇒ conflict,
  never silent overwrite).
- Integration `test/integration/sync_test.go:46`
  `TestConflict_NeitherVersionLostSymmetricName` — two live engines: exactly one conflict
  copy per node, byte-identical name on both, both versions present (no loss).

## Run-log corroboration
- `docs/audit/runs/scenario-conflict.log:8` `--- PASS: TestConflict_NeitherVersionLost...`;
  `docs/audit/runs/race-all.log:9,11`; fresh 2026-06-29 run all `--- PASS`.

## Implementation-detail note (not a regression)
The finding §3.1 stated the intent to use an explicit `modified_by` (DeviceID) field for
tier 2. The shipped `FileInfo` has no `ModifiedBy` field; tier 2 derives the author from
the version vector via `authorOf` (`conflict.go:65-77`, "largest ShortID where a's VV
exceeds b's"). This is the VV-direction realisation the finding called equivalent for a
2-device tool. The load-bearing property the finding actually claims — a TOTAL +
COMMUTATIVE winner yielding symmetric convergence with no data loss — holds and is proven
by `TestW_Commutative` (5000 cases) and the integration symmetric-name/no-loss test. The
deviation is internal and does not weaken the contract.

## Skeptical check
The end-to-end integration test is the strongest evidence: both independent engines mint
the identical conflict-copy filename and retain both byte-sets. UTC-truncation +
TZ-independence are directly tested (cross-platform determinism). No data-loss path found.

---

## Phase 7 resolution (round 1) — the FIXED verdict was REFUTED, then re-fixed

All three skeptics REFUTED this verdict (`votes/PR-3-skeptic1.md`, `PR-3-skeptic2.md`,
`votes/PR-3-skeptic3.md`), and they were RIGHT: "No data-loss path found" was too strong.
The resolver was sound, but the no-data-loss CONTRACT is enforced in the EXECUTION layer,
which had four data-loss defects (one reproduced deterministically this round):

- (A) live-vs-live under `fetchQ` saturation — split copy/winner enqueues (skeptic #1/#3).
- (B) delete-vs-modify, delete wins — synchronous `applyTombstone` removed the original
  BEFORE the async copy ran (skeptic #2 §1). Reproduced: `f.txt-removed=true,
  conflict-copies-on-disk=0` before any copy.
- (C) §6 MAX_PATH bounding unwired (skeptic #2 §2).
- (D) cross-peer FALSE DOMINATION — the conflict winner merged the loser's VV, so a
  broadcast winning tombstone dominated the loser's own custodian into a plain delete with
  no copy (found while building the (B) two-engine test; the merge also lost the loser on a
  3rd peer).

Re-fixed in **commit `9d1e0cca7ac3ef7bd66e042c665103f42947d9fc`** (Phase 7 round 1):
couple the loser-copy with the winner-install gating the destructive step on the copy
landing; drop the false-dominating merge (winner keeps its own VV — convergence preserved);
refuse+flag an over-MAX_PATH copy. New deterministic tests cover delete-wins, copy-gating,
full-queue atomicity, MAX_PATH refuse, and a two-engine delete-vs-modify no-loss scenario.
`go build ./... && go test ./... -race` green; Windows cross-compile clean. Decision:
`docs/audit/decisions/phase7/PR-3-conflict-no-data-loss-ordering.md`. **Verdict now: FIXED**
(the refutations are upheld and discharged). Note: the §3.1 reviewer remark above about the
"merged VV" no longer applies — the winner keeps its own VV by design.
