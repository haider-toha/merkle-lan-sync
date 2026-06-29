# PR-6 — Skeptic #1 vote (challenging the FIXED verdict)

- Date: 2026-06-29
- Reviewer: skeptic #1 of 3
- Vote: **REFUTED (fixed claim not fully evidenced)** — confidence: medium
- Subject: `docs/audit/findings/protocol/PR-6-sync-loop-invariant.md` (status `fixed`, commit `af12de0`), review verdict `docs/audit/findings/review/PR-6.md` (FIXED).

## What is genuinely solid (not contested)
The load-bearing half of the invariant — "applying a received update produces zero outbound `INDEX_UPDATE`" — is real and tested:
- `internal/reconcile/broadcast.go:51-78` `broadcastUpdate` is reached only from `onLocalChange`/`rescan`/conflict-advertise; `handleCompletion` (`engine.go:793-817`) updates `files`+`expected` and never broadcasts a normal fetch.
- Post-window apply echo is filtered by content identity (`engine.go:861-869`).
- Idempotent content-addressed apply (`transfer.go materialise`) makes redelivery a no-op.
- `reconcile_test.go:444` `TestApply_ZeroOutboundBroadcasts`, `:479` `TestApply_IdempotentRedelivery`, `:577` `TestRescan_RecoversDroppedEvent` pass. Integration roots are stable/equal, ruling out a *self-sustaining* loop.

## Why I still vote REFUTED

### 1. The finding's Section 5 design claim is contradicted by the code
The finding (Section 5, and the SR-8 "guard c" wording) explicitly asserts the guards do **not** mute the watcher and instead key on **content-hash equality**, precisely so "a real concurrent user edit to the same file during the [apply] window" is not dropped.

The actual in-flight guard does the opposite — it is an **unconditional blanket mute** keyed on in-flight state, not on hash:
- `engine.go:837-839` (`onLocalChange`): `if inflight { return }` — no content comparison.
- `engine.go:906-908` (`rescan`): `if e.inflightLocked(s.Path) { continue }` — no content comparison.

So a genuine user edit landing on a path while an apply for that same path is in flight **is silently dropped** by the watcher/rescan-delta path. It is recovered only by a *later* periodic full rescan once `inflight` clears — i.e. delayed, not "still detected and broadcast" as claimed. This is the exact "blanket mute drops a concurrent edit" failure mode the finding says the design avoids. The verdict did not flag this discrepancy.

### 2. Test obligation #4 is not actually covered
The finding's own §6 obligation #4 — "Genuine edit during the apply window → still detected and broadcast (proving the guards filter by content, not by muting the watcher)" — is **not tested**:
- In `TestApply_ZeroOutboundBroadcasts` the "apply" is faked by directly setting `files`/`expected`; `ps.inflight[key]` is **never set true**. The genuine edit therefore occurs with `inflight == false` (after the window), so it exercises the content-identity filter, **not** the in-flight guard.
- The only `inflight` reference in `reconcile_test.go` is the map init in `registerFakePeer` (line 129). No test drives a genuine concurrent edit while `inflight` is true. The verdict's "skeptical check" claims this case is covered; it is not.

### 3. No bounded-broadcast-count oracle for the ping-pong scenario
Obligation #3 ("exactly one converged update, no ping-pong, **bounded total broadcast count**") is asserted only via `waitConverged` stable-equal-roots (`test/integration/helpers.go:122-135`). Stable roots disprove an *infinite* loop but do not bound the broadcast count — a finite redundant echo storm (e.g. a few extra laps before quiescence) would still pass. No test counts outbound `INDEX_UPDATE` frames across the two-instance edit scenario.

## Disposition
The core no-sync-loop property holds and convergence is real, so this is not a hard regression. But the FIXED verdict adopts the finding's stronger claims (no watcher muting; concurrent-edit-during-window detected; bounded broadcast count) that are (a) contradicted by the blanket in-flight `return`/`continue`, and (b) unbacked by any test. Per the skeptic default ("refuted if the fixed claim is not solidly evidenced"), I vote REFUTED. Recommended to downgrade to FIXED-with-caveat or add: a test that sets `inflight=true` then injects a genuine concurrent edit and asserts it is eventually broadcast exactly once, plus an outbound-frame-count cap in the two-node loop test.
