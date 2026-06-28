# Decision (WS-0): how to land the CDD-1 / CDD-8 rule amendments (GR-4, GR-5, GR-13, SR-5)

- Area: ws0 / docs/audit/rules
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-0 implementer
- Plan items discharged: WS-0 acceptance #7 (rule amendments landed — GR-5 widened,
  GR-4 companion, GR-13 fan-in clarified, SR-5 "at quiescence"; the *tests* that
  exercise them live in WS-2/WS-3/WS-4).
- Reads-first: `findings/design/consolidated/overview.md` CDD-1 (sources
  concurrency-critic-1..4) and CDD-8 (sources tree-critic-1, tree-critic-4),
  go-rules.md GR-4/GR-5/GR-13, sync-rules.md SR-5, the implementation plan WS-0 #7.

## Context

CDD-1 (low severity, doc + tests) and CDD-8 (low, doc/scope) route concrete *rule
wording* changes to the rules files, with the runnable `-race`/convergence tests
deferred to the network/engine workstreams. WS-0 owns the doc edits so every later
workstream builds on a settled concurrency + oracle contract:
- **GR-5** widen "guards the tree" → "guards the reconcile core's in-memory state
  (tree **+** per-peer ack/last-index state **+** the apply-time expected-hash
  record)", and cross-ref that non-tree shared state is a GR-4 single-goroutine actor.
- **GR-4** add the companion rule: never block on outbound from the select loop;
  per-conn writer owns outbound (buffered-with-shed or drop the peer); bulk RESPONSE
  in its own GR-3 goroutine.
- **GR-13** clarify fan-in: a many-senders/one-receiver channel (`inboundMsgs`,
  `peerEvents`) is **never closed** — shutdown is `ctx` + a `WaitGroup` over senders;
  "exactly one closer (sender side)" applies only to single-sender channels; per-conn
  `conn.Close()` idempotent via `sync.Once`, owner-only.
- **SR-5** reword the oracle to hold "**at quiescence** / after a change settles,"
  not at every instant, so a transient root inequality during propagation or
  tombstone-GC skew is not read as "diverged"; the flow-verifier quiesce-then-asserts.

The choice is *how* to land these without breaking the stable rule-ID citations that
decisions/findings/tests reference.

## Options (scored 1–5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — amend the existing GR-4/GR-5/GR-13/SR-5 entries in place, citing CDD-1/CDD-8 and keeping their IDs (CHOSEN)
- correctness **5** — single source of truth; the consolidator explicitly routed
  these to "Rules (go-rules/sync-rules)" against these very IDs, so amending them is
  the intended landing.
- concurrency-safety **5** — the widened GR-5 boundary + GR-4 companion + GR-13 fan-in
  rule are exactly the contract the WS-2/3/4 `-race` tests will assert against.
- testability **5** — the rules name their WS-2/3/4 test obligations (churn leak test,
  discovery `-race`, bidirectional back-pressure); WS-0 verifies by review (doc gate).
- cross-platform **5** — wording only; OS-independent.

### Option B — add new rules GR-14/GR-15/SR-14 instead of editing
- correctness **3** — orphans the dozens of existing `GR-4/GR-5/GR-13/SR-5` citations
  and splits one concept across two IDs.
- testability **3**, others **5**. Rejected: fragments the rule surface.

### Option C — encode the contract only in code comments / package docs, leave rules untouched
- correctness **2** — the rules files *are* the enforceable contract (reviewed,
  `-race`/`vet`-gated); burying amendments in comments defeats CDD-1/CDD-8's purpose.
  Rejected.

## Decision

Adopt **Option A**: amend GR-4, GR-5, GR-13 in `docs/audit/rules/go-rules.md` and
SR-5 in `docs/audit/rules/sync-rules.md` in place, each with an explicit "Amendment
(WS-0, CDD-1/CDD-8)" paragraph that states the widened wording and cross-references
the consolidated decision and the workstream that carries the exercising test. Rule
IDs are unchanged. The quick-reference checklists are updated to match.

## Rationale

- The consolidator's CDD rollup table maps these CDDs to these exact rule IDs;
  amend-in-place is the literal instruction and preserves every downstream citation.
- Keeping IDs stable means the WS-2/WS-3/WS-4 acceptance tests (which cite GR-4/5/13)
  and the flow-verifier (which cites SR-5) bind to the amended text automatically.
- New IDs or comment-only encodings would either orphan citations or move the
  contract off the reviewed rule surface.

## Consequences

- `go-rules.md` (GR-4, GR-5, GR-13 + checklist) and `sync-rules.md` (SR-5 + the
  invariant map) gain the amendment paragraphs; no IDs change.
- WS-0 marks acceptance #7 done by the doc gate; the runnable assertions are owned by
  WS-2 (`TestConnChurn_NoGoroutineLeak`, writer-owned outbound), WS-3
  (`TestDiscovery_RaceAnnounceEvictDial`), WS-4 (`TestBackpressure_BidirectionalConverges`,
  quiesce-then-compare convergence).
- Cross-refs CDD-1, CDD-8, concurrency-critic-1..4, tree-critic-1/-4.
