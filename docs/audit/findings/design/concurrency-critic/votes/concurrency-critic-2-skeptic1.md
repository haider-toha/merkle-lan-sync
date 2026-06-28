---
finding: concurrency-critic-2
skeptic: skeptic1
vote: REFUTE
confidence: medium
date: 2026-06-28
---

# Skeptic #1 vote on concurrency-critic-2 — REFUTE

## Summary

The finding is technically literate and names a real anti-pattern
("request/response on the same goroutine that must read the replies"), but as a
**design** finding against Merkle Sync's Phase 0 plan it is **overstated and
self-defeating**: the deadlock only materialises under a specific naive
implementation choice that the design does not mandate, and the finding's
headline recommendation is **already in the design it critiques**. Net: severity
"high / silent guaranteed deadlock" is not supported by the cited evidence.

## Where the evidence does not support the claim

### 1. The load-bearing step of the deadlock chain is an assumption, not the design

The deadlock requires the engine to perform a *blocking* outbound send while it
is the sole inbound drainer. The finding supplies this in step 1:

> "It writes them either directly to B's conn **or by sending to B-conn's bounded
> outbound channel.**"

Neither branch is in the design. `structure.md:93` specifies
`conn.go | ... per-conn reader **+ writer** goroutines`, and GR-4
(`go-rules.md:82`) repeats "one reader + one **writer** goroutine **per**
connection." That is precisely the decoupling layer that absorbs TCP
back-pressure: the **writer goroutine** is what blocks in `conn.Write`, not the
engine. The finding has to *invent* "engine does a blocking send on a bounded
channel" to route the wait cycle back through the engine. The design nowhere
states the engine blocks on outbound, that the outbound channel is bounded, or
that the send is not a `select` case alongside `inboundMsgs`/`fsChanges`/
`ctx.Done()`. A memory-only assumption about an unspecified implementation detail
is exactly what the autonomy contract's EVIDENCE rule forbids as the basis of a
finding.

### 2. The primary recommendation restates the status quo

Recommended-change #1 is: "Decouple outbound from the engine via per-conn writer
goroutines that own the socket, fed by a per-conn outbound channel." That is
verbatim what `structure.md:93` and GR-4 already prescribe. A recommendation that
re-describes the existing design does not "beat the status quo" — it *is* the
status quo. The only genuinely new sub-points are: non-blocking-or-shed send
(#1's tail), bulk transfer on its own goroutine (#2 — and `transfer.go` already
exists as a separate file from `engine.go`, `structure.md:119`), a doc rule (#3),
and a test (#4). Those are reasonable Phase 5 implementation hygiene items, not a
high-severity design defect.

### 3. The "silent, no cancellation" claim ignores the existing cancellability rule

The finding leans on "no ctx cancellation fires (nothing crashed)." But GR-4
(`go-rules.md:89-92`) already mandates that blocking network reads be made
cancellable via read deadline / close-on-ctx, and GR-2/GR-3 thread one ctx +
WaitGroup through every goroutine. With the writer goroutine owning `conn.Write`,
the reader is never starved by the engine, so the symmetric wedge the finding
draws does not close. The finding's own text concedes "The reader/writer split
*would* solve the pure-transport version" — and the split is in the design.

## What is actually left (and why it isn't "high")

There is a sliver of real signal: GR-4 states the cancellability rule for
*reads* but not symmetrically for *the engine's outbound sends*. Adding that one
sentence (recommendation #3) is a cheap doc improvement. But that is a
low-severity documentation gap, not a "two daemons hang silently mid-sync"
high-severity deadlock. Severity is inflated by assuming the worst-possible
implementation of an as-yet-undecided detail and then grading the design as if it
had committed to it.

## Counter-example

A conformant implementation of the cited design — engine's select loop with
`case outCh <- frame:` guarded by `ctx.Done()` (or simply an unbounded/large
per-conn outbound queue drained by the writer goroutine that owns the socket) —
exhibits no deadlock under symmetric bidirectional bulk transfer, because the
engine is never parked indefinitely on outbound. This satisfies every cited
design constraint (`structure.md:93,114`, `structure.md:119`, GR-3/4/5) without
any change. The deadlock therefore is not entailed by the design.

## Vote

REFUTE. The finding manufactures the deadlock via an unstated implementation
assumption, its headline fix duplicates the existing reader/writer-split design,
and its residual contribution (a symmetric outbound-cancellability note) is a
minor doc tweak, not a high-severity flaw. Confidence: medium (the underlying
concurrency reasoning is sound *if* an implementer ignores the existing writer
goroutine, so the doc-rule sliver has some value).
