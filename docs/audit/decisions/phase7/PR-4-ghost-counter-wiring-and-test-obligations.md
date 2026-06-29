# Decision: PR-4 ghost-counter (#10590) wiring + the un-discharged test obligations

- Area: phase7 (fix loop) — reconcile / protocol
- Status: decided
- Date: 2026-06-29
- Decider: fix agent (Phase 7, round 1) for open item PR-4 (fixed-claim-refuted)
- Reads-first: `docs/audit/findings/protocol/PR-4-deletions-tombstones-resurrection.md`,
  `docs/audit/findings/review/PR-4.md` (verdict FIXED),
  `docs/audit/findings/review/votes/PR-4-skeptic2.md` (REFUTED),
  `docs/audit/findings/protocol/votes/PR-4-skeptic1.md` (REFUTED),
  `docs/audit/decisions/protocol/vv-pruning-counter-cleanup.md` (Option A, CHOSEN),
  `docs/audit/decisions/protocol/tombstone-retention-gc.md`.

## Context

PR-4's `status: fixed` was REFUTED by **2 of 3 review skeptics**. The marquee
stale-peer anti-resurrection invariant (§5) is solid and load-bearing-tested — that is
**not** contested. The refutation is that `fixed` *overclaims* its full scope:

1. **Ghost-counter mitigation (#10590 / FM-1) is unreachable dead code** (both skeptics).
   The finding's status line and §5.1 claim "`DropCounter` strips a de-paired device's
   counter" as a delivered mitigation. But `internal/reconcile/tombstone.go:64`
   `DropCounter` has **zero production callers** (`grep -rn "DropCounter" --include=*.go`
   ⇒ only the definition). No un-pair / device-removal path is wired in `cmd/msync`, and
   the exported method itself (lock + rebuild) is never exercised — only the private
   `dropFromVV` helper is unit-tested (`reconcile_test.go:1217`). So in the running
   binary, removing a device leaves its ghost counter in every leaf's VV permanently; the
   exact #10590 resurrection the finding claims to defend against is **not prevented in
   the binary**. Evidence: `votes/PR-4-skeptic2.md` Gap 1; `protocol/votes/PR-4-skeptic1.md`
   Gap 2.

2. **Obligation #5 (restart-with-pending-tombstone) has no integration coverage**
   (skeptic #2 Gap 2). It is proven only at the pure-function level
   (`SynthesizeDeletions` carry-forward, snapshot round-trip). No test constructs an
   engine holding a **not-yet-acked** tombstone, tears it down, reconstructs it from the
   on-disk snapshot, and asserts no resurrection through the full reconcile path.
   (`TestRestart_SynthesizesDeletionFromSnapshot` covers the *delete-while-down →
   synthesize* path — a fresh tombstone minted at restart — NOT a pre-authored tombstone
   *loaded* from the snapshot.)

3. **Obligation #4 (delete-vs-concurrent-modify → conflict copy, no loss)** was flagged
   by skeptic #1 as untested. This is **already resolved by the later PR-3 fix**
   (commit `9d1e0cc`, which postdates the votes): `TestResolver_DeleteWinsPreservesModification`
   (resolver), `TestConflict_DeleteWins_ModificationPreservedAsCopy` (engine, with
   Windows-hostile path variants), `TestConflict_DeleteVsModify_NoLossBothPeers`
   (integration). Action here: verify + a one-line complementary *modify-wins* resolver
   assertion; document the mtime-tiebreak honestly (lossless either way — not a data-loss
   bug; a deterministic modify-wins policy is a possible future refinement).

The **binding prior decision** `vv-pruning-counter-cleanup.md` (Option A, CHOSEN) already
mandates the resolution direction: "provide `DropCounter` from day one … Prune a counter
only when its device is **explicitly removed from the allow-list** … apply the sweep to
every stored `FileInfo` and the snapshot … **the operator action 'remove a paired device'
is the only pruning trigger**; automatic/aged pruning is explicitly out of scope (and
would be unsafe)." So the fix must *wire* the device-removal trigger, not delete the
primitive. The wrinkle: `cmd/msync` pairs via the `-peer` allow-list (out-of-band TOFU,
PR-7) and has **no runtime de-pair event** — pairing changes happen between runs. The
engine itself has no notion of the paired set (the allow-list lives in the transport).

## Options (scored 1–5; axes = correctness / concurrency-safety / testability / cross-platform)

### Option 1 — Startup de-pair sweep keyed on an explicitly-declared paired set, sharing the `DropCounter` core; + the missing tests (CHOSEN)
Add `Config.Peers []protocol.DeviceID` (the explicitly-paired devices). On `New` →
`startupReconcile`, after `SynthesizeDeletions`, sweep from every loaded leaf's VV any
counter whose ShortID is **not** in `{self} ∪ {paired}` — i.e. a device the operator has
de-paired (removed from the allow-list). Gate the sweep on the paired set being **known**
(`Config.Peers != nil`); if nil, retain every counter (the safe fallback, mirroring
tombstone GC's retain-when-no-peer). `cmd/msync` passes the parsed `-peer` set (nil when no `-peer` is given ⇒ retain-all;
non-nil with the declared peers otherwise ⇒ sweep active).
The public `DropCounter` and the sweep share one `dropCounterLocked` core; add a unit test
that exercises the public method (lock + COW + rebuild) and a load-bearing ghost-counter
resurrection negative/positive test. Add the obligation-#5 pending-tombstone restart
integration test. The swept in-memory state becomes the next persisted snapshot (so the
snapshot is pruned too, as the decision requires).
- correctness **5** — kills the #10590 ghost-counter class at its only real trigger
  (device removal, expressed as "absent from the declared paired set"); by construction it
  **never** drops a live/paired device's counter (so SR-10 dominance among live devices is
  preserved). In `cmd/msync` the `-peer` list *is* the complete paired set every run, so
  the sweep fires only on a genuine de-pair. The nil-gate makes the engine safe when the
  paired set is unknown.
- concurrency **5** — copy-on-write `dropFromVV`; applied by the single writer under the
  write lock (or the single-goroutine `New` path); one `rebuildLocked` after the sweep.
- testability **5** — deterministic: engine `DropCounter` unit test (COW + rebuild under
  `-race`); a pure resolver ghost-counter negative test (with the counter ⇒ resurrection;
  after the drop ⇒ clean delete) — the load-bearing proof skeptics demand; a `New`-from-
  crafted-snapshot test for the startup sweep + the nil-retain fallback; an integration
  test for the loaded pending tombstone (obligation #5).
- cross-platform **5** — pure data operation; no path separators; the pending-tombstone
  test is OS-agnostic and the existing conflict tests already carry Windows-hostile keys.

### Option 2 — Keep `DropCounter` as a tested primitive, defer the *trigger*; narrow the claim
Do not wire any production caller; just add the engine `DropCounter` unit test + the
ghost-counter resurrection proof + obligation-#5 test, and narrow PR-4's `fixed` so it does
not assert the ghost-counter mitigation is wired (mark the trigger deferred).
- correctness **4** — honest, but leaves the binary still unable to prevent the
  ghost-counter resurrection on a real de-pair; it only proves the mechanism in tests.
- concurrency **5**, testability **5**, cross-platform **5**.
- **Rejected** — under-delivers vs the binding `vv-pruning-counter-cleanup.md` decision,
  which calls for an actual removal path. A re-review skeptic could still say "no
  production caller — the binary doesn't prevent #10590." Option 1 closes that for the
  same test cost.

### Option 3 — Full runtime, ack-gated, symmetric hot un-pair (CLOSE-with-reason handshake)
A runtime device-removal command in `cmd/msync` that strips the counter, broadcasts the
drop, and completes only once the peer acks (the decision's "both live peers applied it").
- correctness **5** — the most complete realisation of the decision.
- concurrency **3** — a new wire handshake + state machine on the hot reconcile loop;
  meaningful new surface to get right under `-race`.
- testability **3** — needs a two-process ack/round-trip harness.
- cross-platform **5**.
- **Rejected for this fix** — over-scoped for v1 (2-device, no device-management UI). The
  decision explicitly scopes "automatic/aged pruning out" and names the *operator removal*
  as the trigger; in v1 that removal is a between-runs config change, which Option 1's
  startup sweep models exactly. Runtime hot un-pair is logged here as the future path.

### Option 4 — Delete the dead `DropCounter`/`dropFromVV`; mark #10590 open/deferred (skeptic option b)
- correctness **3** — honest and removes the dead-code smell, but **contradicts the logged
  `vv-pruning-counter-cleanup.md` decision** ("provide `DropCounter` from day one") and
  throws away correct, already-tested COW infrastructure a multi-device future needs.
- testability **5**, concurrency **5**, cross-platform **5**.
- **Rejected** — deleting decision-mandated infrastructure to satisfy a "dead code"
  objection is the wrong direction when wiring it (Option 1) is cheap and safe.

## Decision

Adopt **Option 1**. Wire the ghost-counter (#10590) prune as a **startup de-pair sweep**
keyed on an explicitly-declared paired set (`Config.Peers`), gated on the set being known
(nil ⇒ retain-all), sharing one COW core with the public `DropCounter`; thread the
`-peer` set from `cmd/msync`. Add: (a) an engine `DropCounter` unit test; (b) a
load-bearing ghost-counter resurrection negative/positive resolver test; (c) a `New`-from-
snapshot startup-sweep test + nil-retain fallback; (d) an integration test for a loaded,
not-yet-acked tombstone surviving a restart (obligation #5); (e) a complementary
modify-wins resolver assertion (obligation #4 completeness). Then rewrite PR-4's `status`
to be **truthful**: stale-peer + premature-GC + restart obligations fixed & tested;
ghost-counter prune now **wired at the device-removal (de-pair) boundary and proven
load-bearing**, with runtime *hot* un-pair (Option 3) the documented deferred path.

## Rationale

- Discharges the binding `vv-pruning-counter-cleanup.md` Option A literally: a sweep over
  every stored `FileInfo` triggered by device removal, never by time/size, never touching
  a live device's counter.
- Closes both skeptics' core objection: the ghost-counter resurrection is now prevented in
  the **binary** (cmd/msync → New → startupReconcile → sweep), and the prune mechanism is
  proven load-bearing by a negative test (with the ghost counter the delete resurrects as a
  conflict copy — the #10590 symptom; after the drop it cleanly dominates).
- The nil-gate keeps the engine safe-by-default and leaves every existing test (the harness
  pairs dynamically via the transport, so `Config.Peers` is nil there) untouched.
- Honest scope: v1 has no device-management UI, so a *between-runs* `-peer` change is the
  real de-pair event; runtime hot un-pair is genuinely future work and is logged as such
  rather than claimed.

## Consequences

- `internal/reconcile/engine.go`: `Config.Peers`; `pairedShorts`/`pairedKnown` fields set
  in `New`; `sweepDepairedCountersLocked` called in `startupReconcile` before the rebuild.
- `internal/reconcile/tombstone.go`: `DropCounter` refactored over a shared
  `dropCounterLocked` core (used by both the public method and the sweep).
- `cmd/msync/main.go`: collect parsed `-peer` DeviceIDs (nil when none given, non-nil
  otherwise) and pass as `Config.Peers` — so the sweep is active in the daemon precisely
  when the operator has declared a paired set.
- New tests in `internal/reconcile/reconcile_test.go` and `test/integration/sync_test.go`.
- **Operator note (documented in PR-4):** de-pairing is destructive to the removed
  device's VV history and must be applied on **both** peers (symmetric pruning) — a
  one-sided `-peer` change transiently un-prunes (FM-3) until both sides have swept; it is
  never data loss. For a 2-device de-pair (removing the only peer) there is no remaining
  live peer, so the prune is unconditionally safe.
- Cross-references: `vv-pruning-counter-cleanup.md` (Option A), `tombstone-retention-gc.md`,
  PR-4 finding §5.1, issue #10590, SR-9/SR-10, FM-1/FM-3.
