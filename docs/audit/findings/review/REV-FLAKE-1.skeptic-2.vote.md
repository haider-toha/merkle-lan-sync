# Skeptic #2 vote — REV-FLAKE-1 ("FIXED" verdict)

- Date: 2026-06-29
- Role: skeptic #2 of 3, charged with refuting the "FIXED" verdict
- Vote: **REFUTED = true** (confidence: medium)

## What I checked (runnable evidence, this machine: Darwin arm64, 10 cores, go1.26.4)

1. Baseline `go test ./test/integration -race -count=1` → ok (2.76s). Verdict's
   green-path claim reproduces.
2. CI-realistic command `GOMAXPROCS=2 go test ./... -race -count=1` ×10
   (single integration binary on a 2-vCPU runner profile) → **10/10 PASS, 0 stalls.**
   The verdict's core claim ("CI-realistic path is green") holds under my testing.
3. Oversubscription `12 × (GOMAXPROCS=2 race test binary)` in parallel,
   `-test.timeout=180s` → **5/12 binaries FAILED.** Every failure was the SAME test:
   `TestConflict_NeitherVersionLostSymmetricName` — a TINY-FILE test —
   `sync_test.go:63: did not converge within 30s` (durations 30.28–30.46s).

## Why this dents "FIXED"

The fix (decision Option A) raised the conflict-test budget only **20s → 30s** and the
harness `RequestTimeout` 5s → 15s, but did **NOT** apply the `withRescan(1s)`
relaxation to the small-file scenarios — `withRescan` is wired only into the two
large-file tests (`sync_test.go:140-141`, `:208-209`). So `TestConflict` still runs
with the aggressive `RescanInterval: 40ms` (`helpers.go:84`) that the finding's own
root-cause names as the GR-5 single-writer starvation amplifier.

Consequence I reproduced: the *exact* test the finding reported flaking at 20s now
flakes at the new 30s budget. The chosen remedy is a probabilistic budget band-aid
that leaves the root cause in place for the small-file path; I showed the new bound
is still exceeded under load. That is "made less likely," not "fixed."

## Why confidence is only medium (not high)

- The flake is genuinely confined to heavy oversubscription (~12×). At the
  CI-realistic single-binary profile it did not reproduce in 10 constrained runs, so
  the verdict's *scoped* claim ("FIXED for the de-flake as scoped; CI path green") is
  largely defensible, and the finding honestly disclosed the residual.
- It is a test-harness liveness issue, severity low; correctness oracles
  (no-loss / verify-before-rename / no-resurrection) are timeout-independent and
  `-race` stayed clean. No correctness regression found.

## Residual gaps that keep me from accepting "FIXED" outright

1. **The fix demonstrably does not eliminate the flake** — I reproduced it at the new
   30s budget on the very test it was meant to de-flake.
2. **No evidence on the actual target runner.** The decision sells Option A as "most
   helps the slow, heavily-shared windows-latest runner," yet ALL validation
   (`integration.log`, `race-all.log`, the verdict's "fresh run") is on a 10-core Mac.
   A "heavily shared" 2-vCPU Windows runner under co-tenant CPU pressure is precisely
   an oversubscription scenario; there is no windows-latest run proving the residual
   cannot bite there. The reviewer's "far beyond any CI profile" is asserted, not
   evidenced against the runner the fix targets.
3. **No regression guard.** Nothing detects or bounds the residual stall; the only
   signal that it is "fixed" is unloaded local passes, which were already passing.

## Recommendation (not part of the vote)

Either (a) apply `withRescan(1s)` to the small-file conflict/deletion/rename tests too
(removes the 40ms churn that I showed is the amplifier), or (b) downgrade the verdict
to "mitigated, residual open" and attach one real windows-latest CI run as evidence.
