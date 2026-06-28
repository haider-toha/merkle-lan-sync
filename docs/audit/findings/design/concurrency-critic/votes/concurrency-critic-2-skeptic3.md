---
finding: concurrency-critic-2
skeptic: skeptic3
vote: REFUTE
confidence: medium
date: 2026-06-28
---

# Skeptic #3 vote on concurrency-critic-2 — REFUTE

## Summary

The finding correctly names a real anti-pattern (request/response served on the
same goroutine that must also read replies; "both peers write, neither reads").
But as a critique of Merkle Sync's Phase 0 design it is **overstated and partly
self-refuting**: the deadlock only forms under an implementation choice the design
does not mandate and that contradicts the design's own rules, and the two
headline recommendations are already in the structure being critiqued. The
`high` severity is not supported by the cited evidence. I refute.

## Where the evidence does not support the claim

### 1. The load-bearing step is an assumption, not the design

The whole cycle requires the engine to perform a **blocking** outbound send while
it is the sole inbound drainer. The finding inserts this in step 1: "It writes
them either directly to B's conn **or by sending to B-conn's bounded outbound
channel**." Neither is in the design.

- `structure.md:93` — `conn.go | ... per-conn reader + writer goroutines`.
- GR-4, `go-rules.md:82` — "one reader + one **writer** goroutine **per**
  connection."

That writer goroutine is exactly the layer that owns `conn.Write` and absorbs TCP
back-pressure. It is the writer, not the engine, that parks in `conn.Write`. The
finding has to *invent* "engine does a blocking send" to route the wait cycle
back through the engine. Per the autonomy contract's EVIDENCE rule, a finding may
not rest on a memory-only assumption about an unspecified implementation detail —
and the cited lines (`structure.md:114`, GR-4) say the engine *consumes* inbound
and is the *single writer of tree state*; they nowhere say it calls `conn.Write`
or blocks on outbound.

### 2. The primary recommendations restate the status quo

- Recommended-change #1 ("decouple outbound via per-conn writer goroutines that
  own the socket") is verbatim `structure.md:93` / GR-4. It *is* the status quo,
  so it cannot "beat" it.
- Recommended-change #2 ("make the bulk-transfer producer its own goroutine")
  matches the existing split: bulk streaming lives in a **separate file**
  `transfer.go` ("request/stream chunks", `structure.md:119`), distinct from
  `engine.go`'s select loop, and GR-3 ("spawn-and-own with WaitGroup") is the
  house pattern for exactly this long-running work. So this too is the expected
  default, not a departure.

### 3. The cycle needs assumptions that conflict with existing rules

The deadlock closes only if (a) the engine streams hundreds of RESPONSE chunks
*inline on its select goroutine* — contradicting `transfer.go` + GR-3 — and (b)
the per-conn outbound channel is bounded **and** the engine blocks on a full
buffer with no shed / non-blocking / `ctx.Done()`-guarded send. The send policy is
not yet decided (this is Phase 0 layout, not code). The finding picks the single
worst policy and grades the design as if it had committed to it.

## Counter-example

A conformant implementation of the *cited* design exhibits no deadlock under
symmetric bidirectional bulk transfer: engine emits via
`select { case outCh <- frame: case <-ctx.Done(): }` (or an adequately buffered
per-conn queue drained by the writer goroutine that owns the socket), and chunk
streaming runs in a GR-3 spawn-and-own goroutine out of `transfer.go`. The engine
is never parked indefinitely on outbound, so it keeps draining `inboundMsgs`,
`fsChanges`, and `peerEvents`. This satisfies every cited constraint
(`structure.md:93,114,119`, GR-3/4/5) with no change, so the deadlock is not
entailed by the design — only by an implementation the design does not require.

## The one legitimate sliver (and why it isn't high)

GR-4 (`go-rules.md:89`) states the cancellability precondition for *reads* but is
silent on the symmetric *send* side. Recommended-change #3 (add the companion
rule "the reconcile core must never perform a blocking send from its select loop")
and #4 (a bidirectional-back-pressure integration test) are reasonable, cheap
additions. But these are a one-line rule clarification plus a test for an
already-correctly-shaped design — `low`/`info` hygiene, not a `high`
"walk-straight-into-it" silent deadlock.

## Verdict

REFUTE. The finding manufactures the deadlock from an unstated, worst-case
implementation assumption that contradicts the design's own per-conn writer split,
separate `transfer.go`, and GR-3; its headline fixes duplicate the existing
design; and its residual contribution is a minor symmetric-rule note plus a test.
Severity is inflated. Confidence: medium — the underlying concurrency reasoning is
sound *if* an implementer ignores the existing writer goroutine, so the doc-rule
sliver carries some value and should be folded in as a low-severity note.
