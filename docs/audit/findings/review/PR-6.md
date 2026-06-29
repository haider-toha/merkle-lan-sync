# Review verdict — PR-6 (Sync-loop invariant: broadcast only after confirmed local change)

- Reviewed: 2026-06-29 (Phase 6 reviewer)
- Finding: `docs/audit/findings/protocol/PR-6-sync-loop-invariant.md` (claimed `status: fixed`, WS-4, commit `af12de0`)
- **Verdict: FIXED**

## What was claimed
Bump VV + broadcast ONLY on confirmed local authorship. Applying a received file uses
`Merge`, never `Bump`, and never broadcasts; filtered by content identity PLUS an
in-flight-apply guard so the brief atomic-rename→completion window is not mistaken for
authorship; idempotent content-addressed apply makes a redelivery a literal no-op. Proof
obligation: apply ⇒ zero outbound `INDEX_UPDATE`.

## Evidence verified against code
- Broadcast gated on local authorship: `internal/reconcile/broadcast.go:51-78`
  `broadcastUpdate` (doc: "called ONLY after confirmed local authorship");
  call sites are `onLocalChange` (`engine.go:826-877`) and `rescan` (`engine.go:883-934`).
- Apply uses Merge/never Bump and never broadcasts: `internal/reconcile/engine.go:793-817`
  `handleCompletion` (updates `files`+`expected`, rebuilds; broadcasts only for an
  advertised conflict copy that carries a fixed VV, l.804-806).
- In-flight-apply guard: `engine.go:688-700` `inflightLocked`, consulted in
  `onLocalChange` (`:835-839`) and `rescan` (`:906-908`) — SR-8 guard c.
- Content-identity filter (apply echo ⇒ no bump): `engine.go:861-869`
  (`prev.ContentHash == nfi.ContentHash ⇒ return`, no broadcast).
- Idempotent content-addressed apply: `internal/reconcile/transfer.go:218-222`
  (`materialise` skips when on-disk hash already equals the leaf hash).

## Evidence verified against tests (all PASS under `-race`)
- `internal/reconcile/reconcile_test.go:444` `TestApply_ZeroOutboundBroadcasts` — an apply
  echo on a recorded path produces `0` outbound `INDEX_UPDATE`; a GENUINE differing edit
  produces EXACTLY `1` and bumps our counter on top of the peer's history (proves the guard
  filters by content, not by muting the watcher).
- `:479` `TestApply_IdempotentRedelivery` — re-materialising already-present content sends
  `0` `REQUEST`s (idempotent no-op).
- `:577` `TestRescan_RecoversDroppedEvent` — a silently-created file is still caught by the
  rescan and broadcast exactly once (the watcher-advisory / rescan-as-truth half).

## Run-log corroboration
- Stable equal roots in the convergence/large-file scenarios prove no ping-pong:
  `docs/audit/runs/scenario-convergence.log:8`, `scenario-large-file.log:8`,
  `docs/audit/runs/two-process-demo.log:34` ("CONVERGED"); `race-all.log:9`.
  Fresh 2026-06-29 run all named tests `--- PASS`.

## Skeptical check
The test asserts BOTH halves of the invariant in one place — apply echo ⇒ 0, genuine edit
⇒ 1 — so a naive "mute the watcher after every apply" (which would drop a real concurrent
edit) would fail the second assertion. The in-flight guard closes the rename→completion
race the finding identifies. No self-sustaining loop path found.
