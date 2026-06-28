---
finding: concurrency-critic-1
skeptic: 3
vote: REFUTE
refuted: true
confidence: medium
---

# Skeptic #3 vote on concurrency-critic-1 — REFUTE

## Summary

The finding's marquee claim — "the discovery peer registry is a concurrent-map
race, three goroutines over one `map[DeviceID]peer` with no lock" — is **not what
the design specifies**. It is reconstructed from a hypothetical bad
implementation, and it ignores the rule (GR-4) that already governs every piece
of non-tree shared state. The severity ("high," borrowed from GR-13's race ⇒
data-loss grading) is overstated because no race is actually specified by the
design. What survives is a minor editorial nit (GR-5 says "the tree" where the
tombstone-GC decision means "reconcile state"), which is low-severity, not high.

## Point-by-point rebuttal

### 1. The "three goroutines over one map" count is inflated; the design specifies an actor, not an unlocked map.

The finding asserts three concurrent accessors of the registry map: the announce
ticker, the multicast receiver, and the dial path. The design text does not
support this:

- `structure.md:81` (the authoritative goroutine table) lists **exactly two**
  discovery goroutines: "one goroutine reading multicast + one ticker goroutine
  announcing." There is **no third (eviction) goroutine** in the table.
- The **announce ticker** (`announce.go`, `structure.md:104`) periodically sends
  the device's *own* `{DeviceID, addr, port}`. It does not read or write the peer
  registry map at all. So it is not a second accessor of the map.
- **Heartbeat eviction** (`registry.go`, `structure.md:105`) is naturally a
  `time.Ticker` channel that the single multicast-reader goroutine `select`s on
  alongside the recv socket. The goroutine table lists no separate goroutine for
  it, so a faithful implementation owns add+evict in one goroutine.
- The **dial path** (`dial.go`, `structure.md:95`) lives in `internal/transport`,
  not `internal/discovery`. It receives peers via the **`peerEvents` channel**
  that the discovery orchestrator emits (`discovery.go`, `structure.md:106`), not
  by reaching into discovery's internal map. So dial is not a third reader of the
  map either.

Once the inflated accessors are removed, the registry is owned by a single
goroutine that emits add/evict events on a channel — which is precisely the
finding's own "preferred" fix (Recommended-change option 2b). The design already
prescribes it.

### 2. GR-4 already specifies the concurrency model for all non-tree state; the finding pretends only GR-5 exists.

The finding's thesis is that "the only documented invariant is one RWMutex for
the tree," so non-tree state is unspecified. That is false. `go-rules.md:85-88`
(GR-4) states the governing rule: listeners "communicate by sending values on
channels to the reconcile core (e.g. `peerEvents`, `fsChanges`, `inboundMsgs`).
The reconcile core is the single consumer that mutates tree state. This is the
classic 'share memory by communicating' shape." GR-4 — not GR-5 — is the
documented owner-model for the registry, the scanloop hand-off, and inbound
messages. The finding never engages with GR-4 except to wave it away as "cross-
subsystem hand-off," even though `structure.md:106` explicitly makes the registry
an actor that emits `peerEvents`. The concurrency model is specified; the finding
just read the wrong rule.

### 3. GR-5 already contains the lock-discipline clauses the finding "recommends."

`go-rules.md:116-118` already says: "if more than one lock ever exists, define and
document a total lock order; acquire in that order everywhere. Prefer a single
lock + immutable snapshots to avoid the problem entirely." Recommended-change
items 1–2a (lock order, leaf lock, single lock + snapshots) restate existing
rule text. A finding whose remedy is largely already in the rules it critiques is
weak.

### 4. The alleged "mutual inconsistency" (item 3) is not a contradiction.

The finding claims GR-5 ("guards the tree") and the tombstone-GC decision
("evaluated by the single writer under the `RWMutex`," `tombstone-retention-gc.md:42`)
are inconsistent. They are not. The GC decision evaluates per-peer ack state
"from already-exchanged index state" — i.e. data the single reconcile writer
already owns under its lock — with "no new wire round-trip." There is one writer,
one lock, no second unguarded map. At worst GR-5's prose says "the tree" where it
should say "reconcile state owned by the single writer." That is a one-word
documentation tightening, not a race and not a design defect.

### 5. Severity "high" is unsupported.

The "high" grade is imported from GR-13 ("a race that double-applies or drops a
change is data loss"). But that grading presupposes a race exists. The design
specifies an actor (GR-4 + `structure.md:106`) and a single lock with snapshots
(GR-5). The finding manufactures the race with the phrase "a faithful
implementation *will* build as a `map`... with no lock" — a prediction about
implementer error, not a property of the design. An implementer following GR-4
builds the channel actor. Grading a speculated future implementation bug as a
high-severity *design* finding is severity inflation.

## What I concede

There is a thin, legitimate residue: GR-5's quick-ref and rule text literally
say "the tree," while ack state is conceptually broader "reconcile state." A
one-line wording widening (and an explicit cross-ref from GR-5 to GR-4 for
non-tree state) would be a reasonable, cheap doc tweak. That is a low-severity
editorial improvement — nowhere near the "high-severity, three-other-pieces-of-
unguarded-state, daemon-crashing concurrent-map race" the finding asserts.

## Verdict

REFUTE. The central data-race claim is not supported by the design as written
(`structure.md:81`, `:104-106`, `:95`); the concurrency model for non-tree state
is already specified by GR-4; the lock-discipline remedies already exist in
GR-5; the "inconsistency" is a non-contradiction; and the high severity rests on
a speculated implementation bug rather than the design. Confidence: medium
(the surviving doc-nit has a sliver of merit, and a few accessor details are
implementation-dependent rather than nailed down in the design).
