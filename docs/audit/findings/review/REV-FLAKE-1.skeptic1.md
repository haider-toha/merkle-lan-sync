# Skeptic #1 vote — REV-FLAKE-1 "FIXED" (challenge)

- Date: 2026-06-29
- Vote: **refuted = true** (confidence: medium)

## What I verified (supports "fixed")
- Code matches the verdict's citations: `test/integration/helpers.go:31-35` budgets,
  `:54` `withRescan`, `:58-60` `withRequestTimeout`, `:86` harness `RequestTimeout 15s`.
- Production defaults UNCHANGED: `internal/reconcile/engine.go:173,176` → 30s/30s via `orDur`.
- `waitConverged` (`helpers.go:124-145`) is poll + 5×20ms settle, so budgets are upper
  bounds, not sleeps. A genuine hang still fails.
- Fresh runs green: `go test ./test/integration -race -count=1` ok 2.6s; and 4 parallel
  race binaries all `ok` (3.3–4.2s) on this host.

## Why I still vote REFUTED
1. **The flake is mitigated, not eliminated — by the authors' own disclosure.**
   `phase6-convergence-timeout-flake.md:77-85` admits that under oversubscription
   ~2/12 suites STILL stall *even at the 60s budget*. A defect that still reproduces is
   not "fixed"; it is papered over with larger timeouts. There is no deterministic
   guard (fake clock / injected scheduler) that proves convergence under starvation.

2. **The "far beyond any CI profile" justification contradicts the code's own comment.**
   `helpers.go:25-30` sizes the budgets for "the windows-latest runner [which] is slow
   + heavily shared." Shared/loaded == oversubscribed. The finding (lines 80-85)
   simultaneously claims oversubscription is "far beyond any CI profile." Both cannot be
   true: CI is exactly the contended environment that produced the residual stalls, so
   the residual is a CI risk, not a purely-pathological lab artifact.

3. **The originally-observed small-file flake got no root-cause treatment.**
   The reproduced conflict failure was `TestConflict_NeitherVersionLostSymmetricName`
   (a TINY-file test) stalling at 20s under contention. The fix for THAT path is only a
   budget bump 20s→30s (`sync_test.go:50-63`, `startNode` with no opts ⇒ still 40ms
   rescan, 15s req-timeout). Only the two large-file scenarios (`:140-141`, `:208-209`)
   get `withRescan(1s)`. So for the path that actually flaked first, the 40ms on-loop
   rehash churn remains and the "fix" is a 1.5× timeout gamble with no evidence that 30s
   survives the same contention that broke 20s.

4. **Root cause persists; the real fix is deferred.** The finding names the mechanism
   (full-file restart from block 0 on chunk timeout — `transfer.go` no-resume — plus
   on-loop rescan) and parks the actual remedy (resumable transfer) as "future
   consideration." The landed change tunes timeouts around an unfixed mechanism.

## Bottom line
Scoped narrowly as "make the single-binary CI run green," the change is reasonable and
the green logs are real. But "FIXED" overstates it: a known residual stall reproduces
under the same shared/loaded conditions CI runs in, the first-observed (small-file) flake
got only a timeout bump, and the underlying restart-on-timeout + on-loop-rehash mechanism
is untouched. Not solidly evidenced as fixed → refuted.
