# Decision — Phase 6 flow-verifier verdict disposition (how to grade layered/indirect system proof)

- Area: phase6 / flow-verification
- Date: 2026-06-29
- Status: accepted
- Author: flow-verifier (Phase 6)

## Context

The flow-verifier is the whole-system oracle for four end-to-end invariants
(plan/agent_roster.md; `.claude/agents/flow-verifier.md`):

1. **Eventual consistency** — at quiescence both trees expose the identical root hash (SR-5).
2. **No data loss** — every conflict left a recoverable copy; the loser is renamed, never deleted (SR-7, SR-9).
3. **No sync loop** — a received file produced ZERO outbound hash broadcasts (SR-6, SR-8).
4. **Clean goroutine shutdown** — `runtime.NumGoroutine()` returns to baseline after peer churn / context cancel; no leaked readers/writers (GR-3).

The contract says: "Each verdict cites the run log / test / goroutine count that
proves it; an **unprovable invariant is a finding, not a pass**" and "Treat any of
the four invariants failing as a release blocker." Verifying the four exposed two
disposition judgment calls that change the verdict, so they are logged before the
verdicts are written:

- **(4) is proven by a *dedicated* `NumGoroutine` baseline test at the transport
  layer** (`TestConnChurn_NoGoroutineLeak`) but the **engine layer** (per-peer
  `pullLoop`/`serveLoop`, dial/watcher goroutines) has **no dedicated
  `NumGoroutine`-churn assertion** — its reaping is verified *indirectly* (the
  terminating `e.wg.Wait()` in `Engine.Run` + the integration harness cleanup that
  blocks on `Run` returning, which would hang on a leak).
- **(3) holds with one bounded, intentional exception:** a received **conflict-copy
  loser** *is* re-advertised once (`handleCompletion` `c.advertise`), by design
  (SR-7 "the copy syncs as a normal file"). It is loop-free (fixed VV ⇒ peer sees
  `Equal` ⇒ idempotent, no re-broadcast). The question is whether this is a PASS
  with a noted exception or a finding.

## Options (>=3, scored on correctness / concurrency-safety / testability / cross-platform)

### Option A — Strict "dedicated system-level assertion or it's a finding"
Any invariant lacking a dedicated *two-engine* `NumGoroutine`/zero-outbound assertion
is graded INSUFFICIENT and routed to Phase 7, regardless of indirect proof.
- correctness: 3/5 — defensible bar, but mislabels a *proven* invariant as failing; the contract's bar is "unprovable", not "not proven by my single preferred test shape".
- concurrency-safety: 3/5 — would force adding tests (good) but blocks release on a thing already true under `-race`.
- testability: 2/5 — rejects valid indirect oracles (a hanging `Run` on leak is a real, if implicit, assertion).
- cross-platform: 3/5 — neutral.
- Risk: cries wolf; a false release blocker erodes the signal the contract wants protected.

### Option B — Evidence-pragmatic: PASS when proven by (dedicated test at the most relevant layer) AND (independent system-level corroboration), record thin coverage as a non-blocking recommendation  [CHOSEN]
Grade an invariant PASS when there is a green dedicated test at the layer that owns
the mechanism **plus** an independent end-to-end signal that would fail if the
invariant were violated (e.g. a leak ⇒ `Run` never returns ⇒ integration cleanup
hangs ⇒ suite times out; a sync loop ⇒ roots never quiesce ⇒ `waitConverged` times
out). Note any missing dedicated assertion as a recommendation, not a blocker.
- correctness: 5/5 — matches the contract's actual bar ("unprovable ⇒ finding"); both invariants ARE provable and proven, just at layered granularity.
- concurrency-safety: 5/5 — all evidence is `-race`-clean and re-run live; the indirect oracles are genuine (terminating `wg.Wait`, quiesce-stable roots).
- testability: 5/5 — credits valid multi-layer evidence and still itemises the gap so Phase 7 can harden it.
- cross-platform: 4/5 — the Go-level invariants are OS-agnostic; cross-OS gaps (case/NFD) are out of scope here (CI matrix + checklist own them).

### Option C — Defer the verdict and write the missing engine-level tests myself first
Withhold the verdict; author an engine-level `NumGoroutine`-churn test + a two-engine
zero-outbound test, then grade.
- correctness: 4/5 — would maximise coverage.
- concurrency-safety: 4/5 — fine.
- testability: 4/5 — best coverage, but…
- cross-platform: 3/5 — neutral.
- Risk: **scope violation** — the flow-verifier contract says this agent "does not
  fix"; authoring tests is implementer/Phase-7 work. Conflates oracle with fixer and
  delays the verdict the pipeline needs.

## Decision

**Option B.** Grade each invariant against the contract's real bar — *provable and
proven* vs *unprovable* — accepting layered evidence (dedicated test at the owning
layer + independent end-to-end corroboration). All four are proven, so all four PASS.
Record the two coverage observations (engine-level `NumGoroutine`-churn assertion;
an explicit two-engine zero-outbound assertion) as **non-blocking recommendations**
for Phase 7, not as findings, because the invariants they would re-cover are already
proven by other green, `-race`-clean evidence.

## Rationale

- The contract blocks release on an invariant **failing**, and treats only an
  **unprovable** invariant as a finding. Neither (3) nor (4) is unprovable: both have
  green dedicated tests (`TestApply_ZeroOutboundBroadcasts`,
  `TestConnChurn_NoGoroutineLeak`) plus independent end-to-end oracles that would
  fail on violation.
- The indirect oracles are not hand-waving: `Engine.Run` calls `e.wg.Wait()`
  (engine.go:348) before returning, and `startNode`'s cleanup does `<-n.done` which
  closes only after `Run` returns (helpers.go:105-106) — a single leaked engine
  goroutine ⇒ `Run` blocks ⇒ the whole integration suite hits the test timeout. The
  suite returns in ~2.7s, so no leak. Symmetrically, `waitConverged` requires the
  roots to *stay* equal across a 5x20ms settle window (helpers.go:130-140); a
  sync loop is non-quiescence and would time out.
- Option B keeps the verifier honest in both directions: it neither suppresses a real
  gap (the recommendations are recorded) nor manufactures a false release blocker.

## Consequences

- The flow-verification report grades all four invariants PASS with cited tests +
  run logs + the live re-runs in this session.
- Two **non-blocking recommendations** carried to Phase 7 (defense-in-depth, not
  release blockers): (R1) add an engine-level `runtime.NumGoroutine()` baseline
  assertion across peer connect/disconnect churn while `Run` stays live; (R2) add a
  two-engine assertion that a plain received file yields zero outbound `INDEX_UPDATE`
  (lift `TestApply_ZeroOutboundBroadcasts` to the integration layer).
- The conflict-copy re-advertisement is documented as a verified **bounded, loop-free
  exception** to "received file ⇒ zero outbound" (it is authorship of a NEW path with
  a fixed VV), not a violation of SR-6/SR-8.
- If either recommendation, once implemented, ever fails, that is a real failing
  invariant and a release blocker per the contract.
