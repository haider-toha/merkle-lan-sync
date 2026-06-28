# Skeptic #1 vote — protocol-critic-2 (VV pruning unsound signal)

- Finding: `docs/audit/findings/design/protocol-critic/protocol-critic-2-vv-pruning-unsound-signal.md`
- Decision under attack: `docs/audit/decisions/protocol/vv-pruning-counter-cleanup.md` (Option A)
- Role: refute.
- **VOTE: REFUTED** (confidence: medium)

## Summary

The finding has a real technical kernel — a *shrunk INDEX read naively under
`Compare`* is indeed an FM-3 trigger (a missing counter compares as `0`, so
`{self:5}` is `DominatedBy` `{self:5, D:4}`). That arithmetic is correct and is
backed by `PR-2 §2` and `version-vectors §4.3` (lines 263-266). But the finding
parlays that correct micro-fact into three over-reaching conclusions
("no wire signal can exist," "the decision is unsound," "virtual pruning is the
only fix") that do **not** survive a careful reading of the decision it attacks.
The recommended change largely **restates the decision's own chosen mechanism**,
so it does not beat the status quo. Net: a legitimate documentation-tightening
note inflated into an "unsound, reopen it" finding.

## Why refuted

### 1. The "unsound" verdict is built by ignoring the decision's explicit ack-gate.
The decision is **ack-gated and symmetric** by its own words: step 3 — "the drop
is only considered complete once **both** live peers have applied it (so vectors
stay *equally* pruned — defeats FM-3)"; Decision — "treat the drop as complete
only once **both live peers have applied it** (symmetric/equal pruning)"
(`vv-pruning-counter-cleanup.md:46-49, 78-84`). The finding's "Worked sequence"
(lines 70-74) manufactures the failure by having `self` *complete a unilateral
prune* while peer E is offline, then comparing E's unshrunk vector against self's
shrunk one. That is precisely the unequal-vector comparison the ack-gate exists to
forbid. Read charitably — and the decision's text demands the charitable reading —
`self` must retain the un-pruned vector as authoritative until E acks, then both
commit the shrink; an unshrunk-vs-shrunk `Compare` is never performed. The finding
refutes a *unilateral* prune the decision never endorses, not the *coordinated*
prune it actually chose.

### 2. The recommended fix does not beat the status quo — it is the status quo.
Recommendations #2 ("explicit acked `DROP_COUNTER` ... until both ack, retain the
counter and never let a shrunk vector be compared against an unshrunk one") and #3
("coordinated operation") are materially identical to the decision's "ack-gated,
symmetric, complete only once both applied, never compare unequally-pruned"
language. The catalogue *already* reserves `0x08+` behind `featureFlags` for
exactly such a control message (`message-type-enumeration.md:49, 104-110`), so the
"no wire signal can exist" framing (finding claim 1) is wrong: minting
`DROP_COUNTER` is an explicitly supported, no-flag-day extension. The actionable
delta over the existing decision is therefore "specify the signal more precisely
and fix one loose sentence," i.e. a refinement, not a reopen-as-unsound.

### 3. "Virtual pruning is the only sound fix" is false.
The finding asserts virtual pruning is "the only one that makes shrunk-vs-unshrunk
comparison safe" (line 92). The cited literature says pruning must be "coordinated
**or** provably safe" (`version-vectors §4.3 / FM-3`, lines 458-467). A coordinated
symmetric ack-gated drop that **never compares unequally-pruned vectors** is the
"coordinated" branch — independently sound, and the one the decision picked.
Virtual pruning is an alternative, not a uniqueness result. Presenting it as the
sole remedy misrepresents the cited source.

### 4. Claim 3 misapplies N6.
The finding argues the only configuration exercising the mechanism is the
3-identity device-replacement transition, which it labels "N6 out of scope."
N6 scopes out **N-device clusters / introducer / multi-connection** — *live*
devices/connections (`problem-space-map.md:111`). Device replacement keeps **two
live devices** at all times (self + one peer); the "third identity" is a *ghost
counter inside a vector*, not a third live cluster member. Conflating vector
arity with live-device count, the finding wrongly declares the marquee lifecycle
(#10590-style device rebuild) out of scope. It is in scope, and it is exactly what
the decision targets.

## What is legitimately true (and why it is not fatal)
- The decision's phrase "or, more cleanly, the next `INDEX` simply no longer
  contains D's counter" (line 46-48) is genuinely loose: a bare shrunk INDEX,
  absent the retain-until-ack discipline, would misfire under `Compare`. This one
  sentence should be tightened to make the retain-until-both-acked invariant
  explicit and to name the concrete signal (reuse the shared "peer acknowledged
  state ≥ X" primitive the decision already cites for tombstone GC, lines 100-102).
- That is a wording/spec-precision fix to an already-decided, already-coordinated
  mechanism — not evidence the approach is unsound.

## Severity
The finding self-rates **medium** and concedes "the common case (2 devices, never
re-paired) is genuinely unaffected" (lines 80-83). Given that the corrective
content reduces to a documentation tightening of a mechanism that already mandates
coordination, the *actionable* severity is **low**.

## Conclusion
The technical kernel is real but narrow; the conclusions drawn from it
(unsoundness, no-possible-signal, virtual-pruning-only, replacement-out-of-scope)
are overstated and partly self-contradicted by the decision's own ack-gate text.
The recommended change restates the chosen design rather than beating it. Refuted;
recommend the kernel be downgraded to a one-line clarification on the existing
decision rather than a reopen.
