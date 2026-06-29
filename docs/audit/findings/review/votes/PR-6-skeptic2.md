# PR-6 skeptic #2 vote — challenge "FIXED"

- Date: 2026-06-29
- Role: skeptic #2 of 3, charged with refuting the "FIXED" verdict for PR-6
  (sync-loop invariant: broadcast only after confirmed local authorship).
- Verdict reviewed: `docs/audit/findings/review/PR-6.md` (FIXED).
- **Vote: refuted = true (confidence: medium).**

## What is solidly evidenced (in fixed's favour)
- Broadcast is gated on local authorship and the apply path never calls it:
  `internal/reconcile/broadcast.go:55-78`, `engine.go:790-807` (`handleCompletion`
  updates `files` and rebuilds, broadcasts only for an advertised conflict copy).
- Steady-state echo filter works and is tested: `TestApply_ZeroOutboundBroadcasts`
  (`reconcile_test.go:444`) asserts apply echo ⇒ 0 outbound, genuine differing edit
  ⇒ exactly 1. `TestApply_IdempotentRedelivery` (:479) and
  `TestRescan_RecoversDroppedEvent` (:577) pass under `-race`.
- End-to-end loop does not sustain: `test/integration` `TestTwoNode_Converge`
  reaches equal-root quiescence (`docs/audit/runs/scenario-convergence.log:8`).
  A sustained ping-pong would never quiesce, so this is real (if indirect) evidence.

## Why I still vote refuted — the load-bearing guard is untested
1. **The in-flight-apply guard has zero targeted coverage.** Both the finding and
   the verdict name `inflightLocked` (SR-8 "guard c") as the fix for the subtle
   race the finding is *about*: "the brief atomic-rename → handleCompletion window
   is never mistaken for authorship." Yet:
   - `inflight` appears in the entire test suite exactly once, as map
     initialisation in a fake-peer helper (`reconcile_test.go:129`). No test ever
     sets `ps.inflight[path] = true` and asserts that `onLocalChange` / `rescan`
     skips it.
   - The cited `TestApply_ZeroOutboundBroadcasts` simulates an *already-completed*
     apply: it pre-populates `e.files` and `e.expected` and never marks the path
     in-flight. It therefore exercises only the content-identity echo filter
     (`prev.ContentHash == nfi.ContentHash`, `engine.go:861-869`), NOT the inflight
     window that the finding identifies as the reason content-identity alone is
     insufficient.
   - The integration `TestTwoNode_Converge` explicitly **disables the watcher**
     (`test/integration/helpers.go:63-65`, "The watcher is disabled and the rescan
     interval is short ... without depending on a live OS watcher"). It hits the
     inflight window only if a 40ms rescan happens to fire between a fetch's atomic
     rename and its `handleCompletion` — incidental and nondeterministic, never
     asserted.
   Net: deleting or breaking `inflightLocked` in `onLocalChange` (`:835-839`) and
   `rescan` (`:906-908`) would not fail any test. A load-bearing race fix with no
   regression guard does not meet "solidly evidenced."

2. **The `expected` map is dead state — implementation diverges from the documented
   guard.** The finding's guard 2 (SR-8) is described as: "record the expected
   content_hash on apply; when the rescan recomputes that path's hash it equals the
   recorded one ⇒ no bump." In the code `e.expected` is written
   (`engine.go:744,797`) and deleted (`:846,873,911,921`) but **never read anywhere**
   (verified: no read site in `internal/reconcile/*.go`). The actual echo filter
   compares against `e.files[key].ContentHash`, not `expected`. Functionally the
   files-map comparison achieves the same result, so this is not itself a loop bug —
   but it means the mechanism the finding claims is in place is NOT the mechanism
   running, which lowers confidence that the fix was validated against its own spec
   and leaves unused, drift-prone state.

3. **Finding test obligation #4 is only partially met and #3 has no unit test.**
   Obligation #4 ("genuine edit *during the apply window* still detected") is the
   one that distinguishes the correct content-keyed guard from a naive watcher-mute;
   the cited test exercises the edit *after* a completed apply, not during an
   in-flight one (see point 1). Obligation #3 (bounded broadcast count, no ping-pong)
   exists only as an integration run, not a unit assertion on broadcast count.

## Conclusion
The core invariant is plausibly correct and the steady-state path is well tested,
so this is not a blatant failure. But the specific subtle race the finding was
written to close — the rename→completion window, guarded by `inflightLocked` — has
no deterministic test, and the documented guard-2 mechanism (`expected` map) is not
actually wired into change detection. Per the skeptic default ("refuted=true if the
fixed claim is not solidly evidenced"), I vote **refuted = true** with medium
confidence, and recommend adding a unit test that sets `inflight[path]=true`,
fires `onLocalChange`/`rescan`, and asserts zero outbound `INDEX_UPDATE`, plus
either wiring or removing the `expected` map.
