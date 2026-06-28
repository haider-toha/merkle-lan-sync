---
finding: concurrency-critic-1
skeptic: skeptic2
vote: refute
verdict: refuted
severity-assessment: overstated (claimed high; real residue is a doc-wording nit)
date: 2026-06-28
---

# Skeptic #2 vote on concurrency-critic-1 — REFUTE

## Summary

The finding claims the concurrency model is specified by exactly *one* primitive
("one `RWMutex`, guards the tree") and that all non-tree shared state therefore has
"no owner or lock," with the discovery registry being a live `fatal error:
concurrent map read and map write`. This rests on reading GR-5 in isolation and
**ignoring GR-4**, which the finding itself quotes and then dismisses. Once GR-4 is
read as part of the design, the central race claim collapses: the discovery
registry already has a documented owner (the orchestrator / actor goroutine), and
the finding's own preferred remedy is a verbatim restatement of that existing rule.
Severity "high" is not supported.

## Point-by-point

### Item #1 (discovery registry — the only "high"/concrete claim) is not supported by the design as written

- GR-4 (`docs/audit/rules/go-rules.md:85-88`) is an explicit, named concurrency
  rule for exactly these subsystems: "listeners do not call into each other
  directly. They communicate by sending values on channels to the reconcile core
  ... This is the classic 'share memory by communicating' shape." The discovery
  subsystem is one of the three listeners GR-4 governs (`go-rules.md:79-83`), not
  something outside the model.
- The design assigns the registry a single owner: `discovery.go` is the
  "orchestrator: listener + announce goroutines, ctx shutdown, emits `peerEvents`
  channel" (`structure.md:106`). That is the actor pattern — one goroutine `select`ing
  over the multicast-receive path and the eviction ticker, owning the map, emitting
  add/evict events on a channel.
- GR-4's own discovery row (`go-rules.md:81`) lists only **two** discovery
  goroutines: "one goroutine reading multicast + one ticker goroutine announcing."
  The announce ticker sends packets; it does not touch the registry. The "three
  goroutines hammering one map with no lock" is the finding's *pessimistic
  implementation choice*, not the documented design.
- Decisive tell: the finding's recommended fix #2(b) — "make the registry a
  single-goroutine actor ... emits add/evict `peerEvents` on a channel (consistent
  with GR-4's 'share by communicating')" — is **literally the rule that already
  exists** (GR-4) and the orchestrator already specified (`structure.md:106`). A
  finding whose preferred remedy is already the documented design rule is not
  describing a design defect; it is describing a possible *implementer* mistake of
  ignoring GR-4. That is a Phase 5 `-race` catch, not a Phase 3 design hole.

### Item #3 ("rule and decisions are mutually inconsistent") is overstated

- GR-5's own text is not "the tree" narrowly — it reads "The Merkle tree /
  **last-known state**" (`go-rules.md:101`) and the rule is the single-writer model:
  reconcile "is the only package that mutates tree state and is the single writer
  behind the `RWMutex`" (`structure.md:37`; `engine.go` "owns `RWMutex` + tree;
  single writer", `structure.md:114`). Per-peer ack/last-index state is derived from
  the peer's `INDEX`/`INDEX_UPDATE` and is exactly part of the reconcile core's
  last-known state. The tombstone-GC decision saying it is "evaluated by the single
  writer under the `RWMutex`" (`tombstone-retention-gc.md:42-43`) is *consistent*
  with the single-writer model, not contradictory. Nothing forces an implementer to
  place reconcile-owned state outside the reconcile lock.

### Items #2 and #4 are hypothetical mis-implementations, not design defects

- Both `scanloop.go` and `apply.go` live **inside `reconcile`** (`structure.md:116,118`),
  the package GR-4 names as "the single consumer that mutates tree state." The
  finding concedes #4 is covered "if it is the tree's `FileInfo`." These are
  "an implementer *might* introduce an off-lock side map" speculations. The design
  already routes this state through the single-writer engine; speculating that a
  future coder will break that is what the mandatory `-race` gate (GR-13) and the
  existing `reconcile_test.go`/`discovery_test.go` exist to catch.

### Severity and "beats status quo"

- The only concrete crash (item #1) materialises *only if* an implementation
  violates GR-4. The design already contains both halves of a complete model:
  GR-4 (share-by-communicating, channels, single reconcile consumer) for
  cross-goroutine hand-off, and GR-5 (single writer behind one lock) for the
  reconcile state. Calling the model "necessary but incomplete" requires pretending
  GR-4 is not there.
- Recommendations #1 and #2 are largely redundant with existing rules (GR-5's
  "last-known state" wording and GR-4's actor pattern). Recommendation #5 (sharpen
  the `-race` test scenarios) is genuinely useful but GR-13 already mandates `-race`
  and the test files already exist (`structure.md:107,122`); tightening their
  scenarios is a minor enhancement, not a high-severity design correction.

## What (small) residue is real

There is a legitimate but minor documentation nit: GR-5's headline "guards the
tree" would be clearer as "guards the reconcile core's in-memory state," and a one
-line cross-reference noting that the discovery registry's owner is GR-4's actor
goroutine would remove any ambiguity. That is a wording tightening, not a
high-severity unowned-race / data-loss finding.

## Conclusion

The load-bearing claim ("shared mutable state with no owner; registry is a
concurrent-map race") is contradicted by GR-4, which the finding quotes and then
sets aside, and whose actor model is the finding's own recommended fix. The
"mutual inconsistency" claim is overstated, and items #2/#4 are speculative
implementer errors. Severity "high" is not supported by the design as written.

REFUTE. (confidence: medium — a real but minor doc-clarity residue remains.)
