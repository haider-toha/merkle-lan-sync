---
finding: concurrency-critic-3
skeptic: 1
vote: REFUTE
severity_assessment: overstated (high → low/none as a *new* finding)
---

# Skeptic 1 vote on concurrency-critic-3 — REFUTE

## Summary

The finding argues the design only specifies teardown against the root ctx and
therefore leaks the sibling goroutine + per-peer engine state on a *single* peer
disconnect. I refute it: the finding misreads a deliberately terse file-layout
table (`structure.md`) as the totality of the design, while the binding rule
GR-3 already prescribes the exact per-conn close-handshake the finding
"recommends," the required leak test already enforces it, and the claimed
engine-state leak (LEAK #2) is contradicted by discovery heartbeat eviction
feeding `peerEvents` into the engine. The substantive recommendation duplicates
the existing design; severity "high" is unjustified.

## Why the evidence does not support the claim

1. **The recommended fix is already a normative, binding design rule.** The
   finding's own evidence, `go-rules.md:62-65` (GR-3), states verbatim: "On peer
   disconnect, the per-connection reader and writer goroutines must **both** exit
   (close the conn → unblock the reader → cancel the writer) and be `Wait`ed."
   That *is* the per-conn close handshake the Recommended-change section asks for
   (items 1-2). The finding concedes this ("The parenthetical *is* the missing
   handshake"). A rule the implementer is contractually bound to follow
   (agent_roster Phase 5: implementer "Reads first … all rules") is part of the
   design, not absent from it. The finding recommends adding what the design
   already mandates.

2. **`structure.md` is a one-line-per-file layout, and it explicitly cross-refs
   GR-3.** `structure.md:93` lists `conn.go` as "created by … **WS-2 · GR-3 ·
   GR-4 · GR-8**" and `:94` lists `listener.go` as "WS-2 · GR-3 · GR-4". The
   per-conn teardown semantics are carried by GR-3, which the cells reference.
   Criticising a terse layout cell for not re-stating the full rule it points to
   is a documentation-granularity complaint, not a design defect. The finding's
   "structure.md never names it" treats the absence of restated prose in a
   summary table as proof the mechanism is absent from the design — a non
   sequitur.

3. **The phrase the finding leans on actually contains the missing mechanism.**
   `structure.md:93` says "**ctx-cancel/close**" — i.e. cancel (ctx) *and* close
   (the conn). `close` here is conn.Close(), the very reader-unblock the finding
   says is unspecified. The finding silently reduces "ctx-cancel/close" to
   "root-ctx only," dropping the "close" half that undercuts its thesis.

4. **LEAK #2 (engine per-peer state "retained forever") is contradicted by the
   design.** `registry.go` (`structure.md:105`) does "heartbeat eviction
   timeout"; `discovery.go` (`:106`) "emits `peerEvents`"; `engine.go` (`:114`)
   "consumes … `peerEvents`." So when a peer drops, discovery stops hearing its
   heartbeat, evicts it, and emits a peerEvents removal that the engine consumes
   to deregister P and release its routing/ack/last-index state. The deregister
   path exists; it simply originates from discovery (the registry that owns peer
   liveness) rather than transport. The finding even notes "peerEvents flows FROM
   discovery" but mislabels this correct separation of concerns as the bug. "the
   engine never learns to drop P" and state is "retained forever" are false.

5. **The test that "the mechanism to pass does not exist" is itself part of the
   design and pins the behaviour.** `structure.md:96` already requires
   `transport_test.go` to include "**goroutine-leak-on-disconnect**" and
   `go-rules.md:69-72` defines it (NumGoroutine returns to baseline after
   connect/disconnect churn, `-race` on). A required, graded test that asserts no
   leak on disconnect is exactly the enforcement that makes the implementer build
   the per-conn handshake. The finding cites this test as evidence of a gap when
   it is evidence the concern is already designed-for.

## Counter-position

Read as intended — rules (normative) + structure (layout) + required tests —
the design already specifies: per-conn reader/writer that both exit on
disconnect via close-the-conn + cancel + Wait (GR-3), an engine deregister path
via discovery eviction → peerEvents (structure.md:105-106,114), and a leak test
gating it (structure.md:96, go-rules.md:69-72). The only genuinely novel
sub-item is a *transport-originated* immediate disconnect event (rec item 3),
which is at most a latency optimisation over heartbeat eviction — minor, not a
"high" memory/goroutine leak, and it does not make the status quo leak; it just
shortens the window before discovery evicts.

## Severity

"High (graded invariant)" is the severity of the *invariant*, not of any defect
in the design. The finding demonstrates no actual gap between the design and the
invariant — it restates GR-3 as if it were missing. As a new finding its
marginal value is low.

## Vote

REFUTE — finding is unsupported: it equates terseness of a layout table with
absence of mechanism, recommends a fix the binding rule already prescribes, and
its LEAK #2 is contradicted by the discovery→engine peerEvents deregister path.
