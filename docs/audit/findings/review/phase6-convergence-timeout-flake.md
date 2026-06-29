# Finding (Phase 6 / review): integration convergence timeouts flake under CPU starvation

- ID: REV-FLAKE-1
- Severity: low (liveness / test-robustness — NOT correctness)
- Status: resolved (harness de-flaked; production unaffected) — landed in commit `b2b298b`
- Date: 2026-06-29
- Evidence: reproductions below; fix in `test/integration/helpers.go` + `sync_test.go`;
  decision `docs/audit/decisions/phase6/convergence-timeout-deflake.md`.

## Claim

The two-node integration suite intermittently fails its `waitConverged` timeout on
a loaded machine, even though the engine does converge given a little more
wall-clock. The failure is a **timeout-tuning / liveness** artifact of the
aggressive *test* configuration, not a deadlock and not a data-correctness bug.

## Evidence

Observed failures:

- First `-v` capture of the full suite:
  `TestBackpressure_BidirectionalConverges` — `did not converge within 40s:
  rootA=71bf8c…b1bd rootB=4572e2…592b` (FAIL at 40.11s).
- 4× parallel race suites (deliberate contention):
  `TestConflict_NeitherVersionLostSymmetricName` — `sync_test.go:63: did not
  converge within 20s` — a **tiny-file** test, so the stall is not large-file
  specific.

Not a deadlock (counter-evidence):

- `go test -run '^TestBackpressure_BidirectionalConverges$' -race -count=25` → 25/25 PASS.
- Same, `-count=8` → 8/8; `GOMAXPROCS=2 -count=6` → 6/6.
- Full suite (non-verbose) ×5 → 5/5 PASS.

So the engine reaches quiescence reliably; only the **wall-clock budget** is
occasionally exceeded under scheduler starvation.

## Root cause

The engine is eventually-consistent (SR-5 holds *at quiescence*, CDD-8). The suite
asserts convergence with fixed timeouts. Two **test-only** knobs amplify the time
under load:

1. `RescanInterval: 40ms` (helpers.go) makes the single engine-loop goroutine
   (GR-5 sole writer) run `merkle.Scan` 25×/s, re-hashing every file
   (scanner.go:59) — including multi-MiB files — competing with chunk-response
   routing. **Production default: 30s** (engine.go:173).
2. `RequestTimeout: 5s` (helpers.go): a chunk response delayed past 5s by loop
   starvation times out, and a timeout restarts the whole file from block 0
   (no resume in `fetchOverWire`/`atomicWriteVerify`, transfer.go), thrashing a
   large transfer under load. **Production default: 30s** (engine.go:176).

Because the production defaults are 30s/30s, a real deployment never sees this; the
flake is an artifact of the fast-detection test config.

## Invariants NOT affected

The correctness oracles are independent of the timeout and always held in every
run: no data lost on conflict (both versions present, symmetric copy name), no
file corruption (verify-before-rename; killed-transfer leaves no partial/temp),
no deletion resurrection. The `-race` data-race oracle never fired.

## Fix (resolved)

`test/integration` only (decision A): CI-robust convergence budgets
(`budgetAuthor`/`budgetConverge`/`budgetLarge`), a larger harness `RequestTimeout`
(15s default, 20s for large-file tests), and a relaxed `RescanInterval` (1s) for
the two large-file scenarios via a new `withRescan` option. No production change.
Budgets are upper bounds — the common case stays sub-second and a genuine hang
still fails.

## Validation after fix

- **Realistic CI command** `go test ./... -race -count=1` (package-parallel — exactly
  how the CI matrix runs it): **8/8 PASS**, 4-6s each. This is the bar that matters.
- Integration suite alone, sequential `-race` ×6: **6/6 PASS**.
- **Residual**: under *pathological* oversubscription — 4 separate
  `go test ./test/integration -race` binaries in parallel (= 8 engines + ~32
  goroutines competing on one laptop, far beyond any CI profile, which runs **one**
  integration binary) — ~2/12 suites still stall even at the 60s budget. That is a
  test-infrastructure oversubscription artifact (the single engine-loop goroutine,
  GR-5, is simply not scheduled), not a CI or production concern: production runs one
  engine per machine with no oversubscription, and CI runs the integration binary
  once. Documented here rather than chased further, because raising budgets past 60s
  trades real hang-detection latency for no realistic benefit.

## Future consideration (out of Phase-6 scope; not a defect)

Resumable transfer (re-fetch only the missing tail after an interruption rather
than restarting the file) would make large transfers robust to mid-stream timeouts
in production too. It is a feature, not a fix for this flake, and is deferred.
