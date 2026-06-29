# Review verdict — REV-FLAKE-1 (integration convergence timeouts flake under CPU starvation)

- Reviewed: 2026-06-29 (Phase 6 reviewer)
- Finding: `docs/audit/findings/review/phase6-convergence-timeout-flake.md`
- **Phase-6 Verdict: FIXED** — OVERTURNED. The 2/3 skeptic REFUTED votes were UPHELD.

> **Phase-7 round-1 outcome (2026-06-29):** the skeptics were correct. The flake was NOT a
> harness-only timeout-tuning artifact: it was two real engine defects — a torn
> (Size,ContentHash) scan leaf that is permanently un-transferable, and a lost-on-connect
> INDEX that permanently wedges (the backpressure residual). Both are now fixed at the root
> in commit `68c65f777fba27c6eeb590824f16862cd1954e10`; verified by the skeptics' own
> oversubscription reproduction (120 race binaries → 0 failures, vs 8/12 failing before).
> See `phase6-convergence-timeout-flake.md` (Phase-7 update) and
> `docs/audit/decisions/phase7/REV-FLAKE-1-torn-scan-size-hash.md`. The Phase-6 review
> below (which accepted the mitigation as "FIXED") is retained for the audit trail.

## What was claimed
The two-node suite intermittently exceeded its `waitConverged` timeout on a loaded machine
— a liveness/timeout-tuning artifact of the aggressive TEST config (`RescanInterval 40ms`,
`RequestTimeout 5s`), NOT a deadlock or data-correctness bug. Fix (test/integration only):
CI-robust budgets, larger harness `RequestTimeout` (15s, 20s for large files), relaxed
`RescanInterval` (1s) for the two large-file scenarios via a new `withRescan` option. No
production change (production defaults 30s/30s).

## Evidence verified against code
- CI-robust budgets present: `test/integration/helpers.go:31-35`
  `budgetAuthor=15s`, `budgetConverge=30s`, `budgetLarge=60s`.
- New relaxation options: `helpers.go:54` `withRescan`, `:58-60` `withRequestTimeout`.
- Harness `RequestTimeout` raised to 15s: `helpers.go:86`.
- Large-file scenarios relax rescan to 1s + request-timeout to 20s:
  `test/integration/sync_test.go:140-141` (backpressure) and `:208-209` (killed-transfer).
- Production defaults UNCHANGED at 30s/30s: `internal/reconcile/engine.go:173`
  (`orDur(cfg.RescanInterval, 30*time.Second)`), `:176`
  (`orDur(cfg.RequestTimeout, 30*time.Second)`) — confirming "no production change".
- `waitConverged` is quiesce-then-compare with a 5×20ms settle window
  (`helpers.go:124-145`), so it returns sub-second in the common case and a genuine hang
  still fails — budgets are upper bounds, not sleeps.
- Decision logged: `docs/audit/decisions/phase6/convergence-timeout-deflake.md` (exists).

## Evidence verified against tests / run logs
- Fresh CI-realistic run 2026-06-29: `go test ./... -race -count=1` → all packages `ok`
  (TEST_EXIT=0), `test/integration` 3.4s; the integration suite `-v` ⇒ all 6 scenarios
  `--- PASS` (TestTwoNode_Converge, Conflict, Deletion, Backpressure, Rename,
  KilledTransfer).
- `docs/audit/runs/integration.log:5-18` all 6 scenarios PASS;
  `docs/audit/runs/race-all.log:11` `ok test/integration`.

## Correctness independence (confirmed)
The finding asserts the correctness oracles were never affected. Verified: the conflict
(`TestConflict_NeitherVersionLostSymmetricName`), deletion (`TestDeletion_NoResurrection`),
and killed-transfer (`TestKilledTransfer_NoCorruptFileThenRecovers`, plus unit
`TestAtomicWriteVerify_KillMidStreamLeavesDstUntouched`) oracles assert no-loss /
verify-before-rename / no-resurrection independent of any timeout, and `-race` never fired.

## Skeptical check
The finding honestly discloses a RESIDUAL: under pathological 4× oversubscription (8 engines
on one laptop, far beyond any CI/production profile) ~2/12 suites still stall at the 60s
budget. This is a test-infrastructure oversubscription artifact (the single GR-5 engine-loop
goroutine simply isn't scheduled), documented rather than chased, and does not affect the
CI-realistic path (one integration binary) which is green, nor production (one engine/host,
30s defaults). The verdict is FIXED for the de-flake as scoped; this is not a correctness
defect. (Resumable transfer is noted as a deferred production enhancement, not a fix.)
