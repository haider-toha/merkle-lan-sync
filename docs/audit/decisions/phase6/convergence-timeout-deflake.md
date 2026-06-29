# Decision: de-flake the eventual-consistency convergence timeouts in the integration suite

- Area: phase6 / evidence-generator
- Date: 2026-06-29
- Status: accepted

## Context

The in-process integration suite (`test/integration`) asserts the SR-5 convergence
oracle with fixed wall-clock timeouts (`waitConverged(..., 15s/20s/40s)`). The
engine is **eventually** consistent — the oracle holds *at quiescence* (SR-5
amendment CDD-8) — so these timeouts are upper bounds on "how long until
eventual," not invariants in themselves.

While capturing evidence, the suite flaked:

- A `-v` full run stalled `TestBackpressure_BidirectionalConverges` at its 40s
  budget (`rootA != rootB`) — captured in the first `docs/audit/runs/integration.log`
  attempt.
- Under deliberate CPU contention (4 race-instrumented suites in parallel),
  `TestConflict_NeitherVersionLostSymmetricName` — a **tiny-file** test — stalled at
  its 20s budget (`sync_test.go:63`).

But standalone the tests are robust: `go test -run TestBackpressure -race -count=25`
→ 25/25; `-count=8` and `GOMAXPROCS=2 -count=6` → all green. So this is **not** a
deadlock and **not** a correctness bug (no data lost, no file corrupted — those are
asserted independently and always held). It is a **liveness/timeout-tuning flake**:
under scheduler starvation the eventually-consistent engine occasionally needs more
wall-clock than the tightest budgets allow.

Two harness choices amplify it, and both are **test-only** — the production
defaults do not have them:

1. `RescanInterval: 40ms` (helpers.go) — every 40ms the engine loop runs
   `merkle.Scan`, which re-hashes **every** file in the folder (scanner.go:59
   `HashFile`), including multi-MiB files, on the single writer goroutine (GR-5).
   During a transfer of otherwise-static files this is pure loop churn that
   competes with response routing. Production default is **30s** (engine.go:173).
2. `RequestTimeout: 5s` (helpers.go) — a chunk request that is delayed past 5s by
   loop starvation times out, and a timeout discards the whole temp and restarts
   the file from block 0 (transfer.go: `atomicWriteVerify` + `fetchOverWire` have
   no resume), so under load a large transfer can thrash. Production default is
   **30s** (engine.go:176).

## Options (scored: correctness / concurrency-safety / testability / cross-platform)

### A. Enlarge the budgets to CI-robust bounds; relax the test rescan + request timeout
Raise `waitConverged`/`waitRootChanged` budgets to upper bounds sized for a loaded,
shared runner; raise the harness `RequestTimeout` so loop-starvation does not cause
spurious full-file restarts; relax `RescanInterval` for the large-file scenarios
(their files are static after startup, so 40ms rehash churn buys nothing). No
production change.
- correctness: **high** — budgets are bounds on an eventual oracle; a genuine hang
  still fails (just later); the common case is unchanged (waitConverged returns as
  soon as roots match — sub-second).
- concurrency-safety: **high** — no production change; `-race` (the data-race
  oracle) is untouched.
- testability: **high** — deterministic under realistic single-suite CI load.
- cross-platform: **high** — most helps the slow, heavily-shared windows-latest
  runner the CI matrix targets.

### B. Shrink the large-file test sizes (2 MiB → 256 KiB)
- correctness: **medium** — 256 KiB is still multi-chunk (8 × 32 KiB) but weaker
  "large-file (multi-chunk)" evidence; and it does **not** fix the small-file
  conflict flake observed under contention.
- testability: medium. Rejected as insufficient.

### C. Mark flaky tests `t.Skip` under `-short`, or auto-retry them
- correctness: **low** — skipping/retrying hides the very signal the evidence phase
  exists to surface; a real regression could be masked. Rejected.

### D. Change production (off-loop rescan / resumable transfer / smaller defaults)
- correctness: **risky** — off-loop rescan breaks the single-writer model (GR-5);
  resumable transfer is a new feature; none belong in Phase 6. Rejected here;
  recorded as a future consideration in the finding.

## Decision

**Option A.** In `test/integration` only: (1) introduce CI-robust convergence
budgets (`budgetAuthor` 15s, `budgetConverge` 30s, `budgetLarge` 60s) and use them
in place of the inline literals; (2) raise the default harness `RequestTimeout`
from 5s to 15s, and to 20s for the large-file scenarios; (3) relax
`RescanInterval` from 40ms to 1s for the two large-file scenarios
(backpressure, killed-transfer recovery) via a new `withRescan` test option.
**No production code changes.**

## Rationale

The flake is an artifact of an intentionally-aggressive *test* configuration
(40ms/5s, chosen for fast deterministic change-detection) interacting with an
eventually-consistent engine under scheduler starvation. The production defaults
(30s/30s) never exhibit it. Enlarging the budgets and removing the self-inflicted
loop churn makes the evidence deterministic without weakening any invariant: the
no-loss / no-corruption / no-resurrection / symmetric-conflict-name assertions are
unchanged, and a true deadlock still fails the (larger) budget. This keeps the CI
matrix — the artifact that closes the OS gap — reliably green on slow runners.

## Consequences

- Common-case runtime is unchanged (budgets are upper bounds; polls return on
  match). Worst-case (genuine hang) is detected at the larger budget.
- The two large-file tests no longer re-hash MiB files every 40ms on the loop.
- A finding records the reproduction, the root cause, and the explicit note that
  production defaults are unaffected:
  `docs/audit/findings/review/phase6-convergence-timeout-flake.md`.
