# Decision: PR-6 — pin the in-flight apply guard with a deterministic regression test, remove the dead `expected` map, and bound the outbound broadcast count, so the no-sync-loop "fixed" claim is solidly evidenced and the code matches the finding

- Area: phase7 / reconcile (engine change-detection guards + broadcast counter)
- Status: decided
- Date: 2026-06-29
- Decider: Phase 7 fix agent (round 1), open item PR-6 (fixed-claim-refuted)
- Reads-first honoured: `plan/README.md`, `plan/agent_roster.md`,
  `docs/audit/findings/protocol/PR-6-sync-loop-invariant.md` (status `fixed`,
  commit `af12de0`, under challenge),
  `docs/audit/findings/review/PR-6.md` (FIXED verdict),
  `docs/audit/findings/review/PR-6.skeptic-1.vote.md` (REFUTED, medium),
  `docs/audit/findings/review/votes/PR-6-skeptic2.md` (REFUTED, medium),
  `docs/audit/findings/review/flow-verification.md` (Invariant 3 PASS + R2),
  `docs/audit/decisions/phase7/PR-5-rename-zero-network-order-independence.md`
  (the `inflight` semantics this fix preserves).
- Consumes: SR-6 (broadcast only on confirmed local authorship), SR-8 (an apply is
  not authorship; guard c = the in-flight window), SR-3 (idempotent content-addressed
  apply), SR-11 (the periodic full rescan is the source of truth), CDD-1 (transfer I/O
  off the loop), GR-5 (no I/O under the lock).

## Context

Both Phase 6 skeptics REFUTED the FIXED verdict on PR-6. The load-bearing half of the
invariant — "applying a received update produces zero outbound `INDEX_UPDATE`" — is
real, agreed by both skeptics, and tested (`TestApply_ZeroOutboundBroadcasts`,
`TestApply_IdempotentRedelivery`, `TestRescan_RecoversDroppedEvent`; integration
roots are stable/equal, ruling out a *self-sustaining* loop). The refutation is about
three OVER-CLAIMS in the finding + verdict that the code/tests do not back:

1. **The in-flight guard — the exact race the finding is written to close — has ZERO
   targeted coverage.** `inflightLocked` (SR-8 "guard c") suppresses authorship over the
   brief atomic-rename → `handleCompletion` window (`engine.go` `onLocalChange`
   `:1112-1116`, `rescan` `:1183-1185`). But `inflight` is set `true` in NO test (its only
   appearance is the map init in `registerFakePeer`, `reconcile_test.go:130`).
   `TestApply_ZeroOutboundBroadcasts` fakes an *already-completed* apply by pre-setting
   `e.files`, so it exercises only the post-window content-identity filter, never the
   in-flight window. **Deleting the `inflightLocked` guard fails no test** — a load-bearing
   race fix with no regression pin. (skeptic #1 §2, skeptic #2 §1.)

2. **The `expected` map is dead, write-only state — the documented guard-2 mechanism is
   not the mechanism running.** The finding's guard 2 (SR-8) is described as "record the
   expected content_hash on apply; when the rescan recomputes that path's hash it equals
   the recorded one ⇒ no bump." In code `e.expected` is WRITTEN
   (`engine.go` `applyTombstone`/`handleCompletion`) and DELETED
   (`onLocalChange`/`rescan`) but **never read** (verified: no read site anywhere in
   `internal/reconcile/*.go`). The real echo filter compares against
   `e.files[key].ContentHash`. Functionally equivalent, so this is not itself a loop bug,
   but it is drift-prone dead state and means the fix was not validated against its own
   spec. (skeptic #2 §2.)

3. **The finding's Section 5 + obligation #4 claim a property the code does NOT deliver,
   and no test covers it.** Section 5 explicitly says the guards key on *content-hash
   equality*, NOT a blanket watcher mute, so "a real concurrent user edit to the same
   file during the [apply] window" is "still detected and broadcast" (obligation #4). The
   actual in-flight guard is an **unconditional blanket mute** (`if inflight { return }` /
   `continue` — no content comparison). A genuine user edit landing while an apply for
   that path is in flight is therefore suppressed by the watcher/rescan-delta path and
   recovered only by a *later* full rescan once `inflight` clears — i.e. delayed, not
   "still detected and broadcast" as worded. Obligation #4 is untested. (skeptic #1 §1+§2.)

4. **No bounded-broadcast-count oracle.** Obligation #3 ("exactly one converged update,
   no ping-pong, **bounded total broadcast count**") rests only on `waitConverged`
   stable-equal-roots, which disproves an *infinite* loop but not a finite redundant echo
   storm. No test counts outbound `INDEX_UPDATE` frames. Flow-verifier R2 recommends
   lifting a zero-outbound assertion to the two-engine layer. (skeptic #1 §3.)

The honest tension this fix must resolve: the in-flight blanket mute is **correct and
necessary** for the no-loop invariant (during the window the disk holds the APPLIED bytes
while `e.files` still holds the OLD leaf, so a content-identity-only filter would see
APPLIED ≠ OLD → mistake it for authorship → bump+broadcast → the A→B→A loop). The cost is
that a *genuine* concurrent edit during that sub-millisecond window is also deferred to
the next rescan. The finding over-stated this as "still detected and broadcast" and
claimed a content-keyed mechanism that is not what runs. The fix must make the code,
tests, and finding tell ONE truthful story — without re-opening the loop and without
inventing a conflict-on-apply path (PR-3 territory) that PR-6 never scoped.

## Options (>=3, scored on correctness / concurrency-safety / testability / cross-platform)

### Option A — Truthful blanket-mute: keep the in-flight guard, REMOVE the dead `expected` map, PIN the guard with deterministic tests, add a bounded outbound-frame counter, and correct the finding. CHOSEN.

Keep `inflightLocked` exactly as-is (it is the correct, necessary window guard). Delete the
write-only `expected` map and its field/writes/deletes (the `e.files` comparison already IS
the echo filter). Add: (1) a deterministic unit test that sets `inflight=true`, writes the
APPLIED bytes to disk (the window state: disk=APPLIED, `e.files`=OLD), and asserts
`onLocalChange`/`rescan` emit ZERO `INDEX_UPDATE` and do NOT bump — so deleting either
guard fails it; (2) a test proving a genuine concurrent edit suppressed during the window
is caught by the next rescan EXACTLY ONCE once `inflight` clears (obligation #4, honestly:
delayed, never lost, exactly once); (3) an `atomic.Int64` outbound-`INDEX_UPDATE` counter
on the engine + a two-engine integration test asserting the RECEIVER emits ZERO frames
applying the author's edit and the AUTHOR emits exactly one (obligation #3 + R2). Reword
the finding's status, Section 5, and obligation #4 to describe the blanket mute + rescan
backstop, and drop the `expected`-map framing of guard 2 (replace with the `e.files` hash).

- Correctness: **high.** Zero behaviour change to the proven no-loop path; the guard that
  closes the window is untouched. Removing `expected` is a pure dead-code deletion (no read
  site, not persisted in the snapshot) — it cannot change behaviour. The rescan backstop
  (SR-11) is real and now tested. No new conflict-handling surface, so no new data-loss
  risk.
- Concurrency-safety: **high.** The guard stays loop-only. The counter is `atomic.Int64`
  incremented in the single outbound path (`broadcastUpdate`, called only on the loop) and
  read by the test via an exported accessor — race-clean under `-race`.
- Testability: **high.** The guard gets a deterministic regression pin (set `inflight`,
  drive the real change-detection functions, count frames directly); the two-engine test
  counts real broadcast events, not stable-roots. Windows-hostile keys folded into the unit
  pin (paths involved).
- Cross-platform: **high.** Operates on canonical forward-slash keys; disk access via
  `ToOSPath(HostTarget())`; reserved-device-stem key in the table.

### Option B — Content-key the in-flight guard (`expected` set at ENQUEUE) and broadcast a genuine concurrent edit DURING the window.

Wire `expected[path]=targetHash` at enqueue time so it is known during the window, replace
the blanket mute with "suppress iff disk hash ∈ {prev, expected}", and broadcast a third
content immediately.

- Correctness: **LOW.** Broadcasting a concurrent edit during the window introduces a
  clobber: `handleCompletion` then overwrites `e.files[path]` with the APPLIED leaf,
  producing a double-broadcast + momentary state revert on the next rescan, and a
  `Bump`-on-top of the peer's VV is a causality lie (the user edited the OLD bytes, not
  APPLIED) that can dominate and lose the peer's version with NO conflict copy. Making this
  safe requires full conflict-on-apply detection — PR-3 scope, large and risky.
- Concurrency-safety: medium (more loop logic + the clobber race window).
- Testability: medium. **Rejected:** trades a documented, tested, bounded limitation for a
  subtle data-loss/echo-storm bug, expanding PR-6 beyond its scope.

### Option C — Mute the RAW fsnotify event stream for the path during the window, instead of guarding in `onLocalChange`/`rescan`.

- Correctness: **low/medium.** The periodic full rescan (SR-11) is the source of truth and
  runs regardless of raw events; it would STILL see disk=APPLIED ≠ `e.files`=OLD during the
  window, so the change-detection guard is still required — raw-event muting adds nothing
  and removes nothing. It also makes a genuinely-dropped edit HARDER to recover (no rescan
  delta path to lean on). Strictly worse than A. **Rejected.**
- Concurrency-safety: medium. Testability: low (event-timing dependent).

### Option D — Documentation-only downgrade: relabel the verdict "FIXED-with-caveat", change no code, add no tests.

- Correctness: n/a. Concurrency-safety: n/a.
- Testability: **FAILS the skeptics' core ask.** The guard still has zero coverage
  (deleting it still fails nothing) and the dead `expected` map remains. This renames the
  obligations instead of discharging them. **Rejected.**

### Option E — Remove the in-flight guard entirely; rely solely on the `e.files` content-identity echo filter.

- Correctness: **LOW (regression).** Re-opens the exact race PR-6 fixes: during the window
  `e.files` still holds OLD, so the content filter sees APPLIED ≠ OLD → authorship →
  bump+broadcast → the A→B→A echo loop. The guard is load-bearing. **Rejected.**
- Concurrency-safety: n/a. Testability: n/a.

## Decision

Implement **Option A**. Concretely:

1. **Remove the `expected` map** — the field (`engine.go:169`), its `make` (`:223`), and
   every write/delete (`applyTombstone`, `handleCompletion`, `onLocalChange` x2, `rescan`
   x2), plus the three dead `e.expected[...]=` setup lines in `reconcile_test.go`
   (`:405,741,1006`) and the `mu` doc comment. The echo filter already compares against
   `e.files[key].ContentHash`; this is pure dead-state removal.
2. **Keep `inflightLocked` unchanged** and make the `onLocalChange`/`rescan` guard comments
   tell the truth: a blanket suppression over the brief in-flight window; a genuine
   concurrent edit that lands during it is caught by the next full rescan (SR-11) once
   `inflight` clears — delayed, exactly once, never lost, no loop.
3. **Add an `atomic.Int64` `outboundIndexUpdates` counter** to the engine, incremented once
   per non-empty `broadcastUpdate` (the single outbound path), exposed as
   `Engine.OutboundIndexUpdates() int64` (observability accessor, sibling of `RootHash` /
   `Snapshot`).
4. **New tests:**
   - `reconcile_test.go` `TestInflightGuard_SuppressesApplyWindowEcho` — sets
     `inflight=true`, writes the APPLIED bytes (window state), asserts `onLocalChange` AND
     `rescan` emit 0 `INDEX_UPDATE` and do not bump; then clears `inflight` + records the
     applied leaf (as `handleCompletion` does) and asserts the post-window echo is also 0.
     Deleting either `inflightLocked` guard makes APPLIED ≠ OLD look like authorship → 1
     broadcast → FAIL. Table includes a Windows-hostile reserved-device-stem key.
   - `reconcile_test.go` `TestInflightGuard_ConcurrentEditCaughtByRescanExactlyOnce` —
     obligation #4, honest: while `inflight=true`, a genuine third-content edit is
     suppressed (0 outbound during the window); after `inflight` clears (apply completes,
     `e.files`=APPLIED), the next `rescan` broadcasts it EXACTLY ONCE and a second `rescan`
     re-broadcasts 0 (idempotent) — proving the edit is delayed, not lost, and never storms.
   - `test/integration/sync_test.go` `TestTwoNode_ReceiverEmitsZeroIndexUpdates` —
     obligation #3 + flow-verifier R2: connect two engines on empty dirs, converge, snapshot
     both counters; edit one file on A; converge; assert B's `OutboundIndexUpdates` DELTA is
     0 (the receiver authored nothing) and A's DELTA is exactly 1 (one bounded broadcast,
     no ping-pong).
5. **Correct the finding** `PR-6-sync-loop-invariant.md`: status block, Section 5, and
   obligation #4 reworded to the blanket-mute + rescan-backstop truth; guard 2 reworded to
   the `e.files` comparison (drop the `expected`-map framing); add the new test names and
   the Phase-7 fix commit SHA.

## Rationale

- It discharges every point both skeptics raised: the guard now has a deterministic
  regression pin (point 1), the dead `expected` map is gone (point 2), obligation #4 is
  tested honestly as delayed-but-exactly-once (point 3), and a direct bounded-frame oracle
  exists at both the unit and two-engine layers (point 4 + R2).
- It changes NO load-bearing behaviour: the window guard that prevents the loop is
  untouched, and the only deletion is provably dead state. So the property the skeptics
  agreed is real stays real, now with coverage.
- It refuses to over-reach: making a concurrent-edit-during-window broadcast immediately
  (Option B) is a conflict-resolution change with real data-loss risk that PR-6 never
  scoped; deferring that edit to the rescan (the existing, now-tested behaviour) is the
  honest, safe contract.

## Consequences

- `go build ./... && go test ./... -race` must be green before the finding is set to
  fixed; the commit is `fix(PR-6): <desc>` and its SHA is recorded in the finding.
- The engine gains one exported observability accessor (`OutboundIndexUpdates`) and an
  atomic counter — a benign, race-clean addition useful beyond the test (a daemon metric).
- A genuine user edit that lands in the sub-millisecond in-flight apply window is, by
  documented design, broadcast by the NEXT full rescan rather than immediately — bounded by
  the rescan interval, never lost, exactly once. This is now stated in the finding and the
  code comments and proven by `TestInflightGuard_ConcurrentEditCaughtByRescanExactlyOnce`.
- Removing `expected` deletes drift-prone state with no behavioural effect (no read site,
  not in the snapshot), simplifying the change-detection paths.
