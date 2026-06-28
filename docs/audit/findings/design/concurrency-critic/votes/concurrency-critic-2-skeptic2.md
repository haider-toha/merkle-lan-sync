---
finding: concurrency-critic-2
skeptic: skeptic2
vote: REFUTE
confidence: medium
date: 2026-06-28
---

# Skeptic #2 vote on concurrency-critic-2 — REFUTE

## Summary

The finding describes a genuine *textbook* failure mode (request/response served on
the same goroutine that must also read replies; "both peers write, neither reads").
But it mischaracterises the proposed design to manufacture that deadlock, and the
two strongest "recommended changes" are **already present in the structure the
finding is critiquing**. The high severity is not supported. I refute.

## Where the evidence does NOT support the claim

1. **The decoupling the finding "recommends" already exists.** The cited evidence
   itself includes `structure.md:93` — `conn.go | ... per-conn reader + writer
   goroutines`. The per-conn *writer* goroutine that owns `conn.Write` is the
   standard mechanism for absorbing TCP back-pressure without involving the
   producer. Recommended-change #1 ("decouple outbound via per-conn writer
   goroutines that own the socket") is therefore restating an existing design
   element, not a fix. The finding hand-waves this away ("the cycle runs through
   the engine, not the conn"), but that re-routing only happens under an
   *additional* unstated assumption (see #3).

2. **The engine is never shown to call `conn.Write`.** The load-bearing citation,
   `structure.md:114`, says the engine *consumes* `fsChanges`/`peerEvents`/
   `inboundMsgs` and is the *single writer of tree state*. It says nothing about
   the engine performing blocking socket writes. The claim "the engine is a
   blocking producer of outbound" is an inference, and the structure contradicts
   it: bulk streaming lives in a **separate file** `transfer.go` ("request/stream
   chunks", `structure.md:119`), distinct from `engine.go`'s select loop. "Single
   consumer of inbound" does not entail "blocks the select loop on outbound."

3. **The deadlock requires two assumptions the design does not make** — and that
   contradict existing rules:
   - (a) that the engine streams hundreds of `RESPONSE` chunks *inline on its
     select goroutine*. But `transfer.go` is separate and **GR-3** (`go-rules.md`,
     "spawn-and-own with WaitGroup") is the house pattern for exactly this kind of
     long-running work. Recommended-change #2 ("make the bulk-transfer producer its
     own goroutine") is again the *expected* implementation under GR-3, not a
     departure from the design.
   - (b) that the per-conn outbound channel is bounded **and** the engine blocks on
     a full buffer with no shed/non-blocking policy. The structure has not yet
     specified the channel's send policy (this is Phase 0/Phase 3 design-layout, not
     code). The finding picks the single worst policy and treats it as decided.
   With either assumption relaxed — and both are the *default* given GR-3 and the
   existing writer goroutine — the cited cycle never forms.

## On the one legitimate kernel

The finding's recommended-change #3 — add a GR-4 companion: "the reconcile core must
never perform a blocking send to a peer from its select loop" — is the only genuinely
additive item. GR-4 (`go-rules.md:89`) does state the *read*-side precondition
("blocking reads must be cancellable") and is silent on the symmetric *send* side.
Documenting that symmetric precondition is reasonable hygiene. But that is a
one-line rule clarification of an already-correctly-shaped design, not a `high`
"walk-straight-into-it" deadlock. The design already supplies every primitive
needed to honour the rule (writer goroutine, separate transfer.go, GR-3).

## Severity / status-quo check

- **Severity overstated.** "Two peers mid-bulk-transfer deadlock silently" is true
  only for a naive implementation the design does not mandate and that conflicts
  with GR-3. As a design-phase critique, the real residual is "make one
  precondition explicit," which is `low`/`info`, not `high`.
- **Recommended change does not clearly beat the status quo**, because #1 and #2
  largely *are* the status quo. Only #3 (and the #4 proof test, which is a fine
  addition) are net-new, and neither requires the alarming framing.

## Verdict

REFUTE. The finding inflates an unspecified implementation detail into a high-severity
deadlock by assuming an inline-blocking engine that contradicts the design's own
per-conn writer split (`structure.md:93`), separate `transfer.go` (`:119`), and
GR-3. The salvageable part — explicitly stating the no-blocking-send-from-select-loop
rule and adding the bidirectional-back-pressure integration test — should be folded
in as a minor rule/test note, not carried as a `high` open finding.
