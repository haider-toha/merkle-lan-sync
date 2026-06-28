# Skeptic vote — protocol-critic-2 (skeptic #3)

- Finding: `protocol-critic-2-vv-pruning-unsound-signal.md` — "Version-vector
  pruning (ack-gated DropCounter) has no wire signal, and the implicit shrunk-INDEX
  signal is unsound under the project's own Compare rule."
- Role: skeptic #3 of 3. Job: refute.
- Vote: **REFUTED** (refuted = true). Confidence: medium.

## What I checked

- The finding file in full.
- The decision it attacks: `docs/audit/decisions/protocol/vv-pruning-counter-cleanup.md`
  (Option A, lines 39-60; Decision, lines 78-84).
- The Compare semantics it relies on: `docs/audit/findings/protocol/PR-2-version-vector-comparison.md`
  §2 (lines 31-55) — "Treat a missing device entry as `0`." Confirmed: the
  finding's `0 < 4 ⇒ DominatedBy` reasoning is mechanically correct *in isolation*.
- The wire catalogue: `docs/audit/decisions/protocol/message-type-enumeration.md`
  lines 39-49, 104-110 — 7 frozen types, `0x08+` reserved/skipped behind
  `featureFlags` (HELLO carries `featureFlags u32`, line 42).
- The scope boundary: `docs/audit/findings/synthesis/problem-space-map.md` §2.2
  N6 (line 111) — "N-device cluster / introducer ... **2-device, single conn** ...
  **out of scope**."

## Why it is refuted

The technical kernel is real but the finding fails on impact, on novelty of its
fix, and on charitable reading of the decision. Four independent reasons:

1. **The only failure path requires ≥3 devices, which is explicitly out of scope.**
   The worked sequence (finding lines 70-74) needs `self` + ghost `D` + a
   *surviving peer* `E` — three identities. N6 (problem-space-map line 111) binds
   the project to **2 devices, single connection**. In the supported 2-device
   configuration, `DropCounter` only ever fires on de-pairing the single peer,
   after which **there is no other live peer to hold an unequal vector** — so no
   `DominatedBy`-resurrection and no `Concurrent` false conflict can arise. The
   finding concedes this directly: "The common case (2 devices, never re-paired)
   is genuinely unaffected" (line 81). The unsound case is precisely the
   3-identity device-replacement lifecycle the finding itself labels N6
   out-of-scope (lines 64-66). A soundness defect that can only manifest in a
   configuration the project does not build is not a live defect; it is a
   guardrail for a future N6 lift. Severity "medium" is therefore overstated for
   the in-scope system — the actionable, supported-config harm is nil.

2. **The decision already mandates the coordination the finding demands.** The
   finding's headline is "pruning must be a *coordinated* operation, not an
   implicit shrink" (recommended-change, line 87). But Option A already says the
   drop "is only considered complete once **both** live peers have applied it (so
   vectors stay *equally* pruned — defeats FM-3)" (decision lines 47-49, restated
   lines 82-84) and "**never** drop a live device's counter." That *is* ack-gated,
   symmetric, equal pruning — the exact property ("never let a shrunk vector be
   compared against an unshrunk one") the finding's own option 2 asks for (line
   95). The finding reaches its unsound result only by reading the parenthetical
   transport hint ("or, more cleanly, the next INDEX ...") as *the mechanism* and
   treating the ack-gate as toothless. The charitable and natural reading is the
   reverse: the ack-gate is the mechanism; INDEX/CLOSE is merely the carrier hint.
   Under that reading the transient unequal-vector window the worked sequence
   exploits is exactly what "drop complete only once both applied" forbids.

3. **The "no wire signal" objection is a non-defect.** The catalogue freezes 7
   types **and reserves `0x08+` behind `featureFlags`** specifically for forward-
   compatible extensions (message-type-enumeration lines 49, 104-110). The
   finding's own option 2 admits "the catalogue already supports this" (line 95).
   So there is no design-level dead end — only a wire encoding deferred to
   implementation. The decision's "from day one" (lines 78-84) scopes the
   `DropCounter`/`Compact` **API** to v1, not a fully-specified wire frame; a
   decision doc legitimately defers exact framing to Phase 5. Calling a deferred
   implementation detail "under-specified in the one way that matters" inflates a
   normal decision-vs-implementation boundary into an unsoundness claim.

4. **The recommended changes do not beat the status quo.** Option 3 ("scope
   DropCounter to un-pair the last peer," lines 97-101) is *already* the effective
   behaviour under the 2-device N6 scope — it adds nothing. Option 2 restates the
   decision's existing ack-gate plus the reserved `0x08+` slot. Only option 1
   (virtual pruning / known-removed set) is genuinely new machinery — and it is
   gratuitous complexity for a 2-device tool where the sound path is simply "drop
   the last peer's counter when there's no peer left." Adopting a literature
   mechanism designed for large unequally-pruned clusters into a 2-device LAN tool
   is the kind of premature N-device surface the project deliberately sheds
   (cf. N6, and Option D rejection in message-type-enumeration lines 95-100).

## Residual merit (not enough to verify)

There is a legitimate kernel: the decision's offhand "or, more cleanly, the next
INDEX simply no longer contains D's counter" line is a footgun if ever
implemented as a literal unilateral shrink without the ack-gate, and the prose
"defeats FM-3 / kills FM-1" overclaims for the unbuilt N-device case. That is a
**documentation-wording tightening** (clarify that the ack-gate, not the INDEX
shrink, is the mechanism; soften the FM-3/FM-1 claim to "in the in-scope 2-device
case"), not a soundness bug that loses data in any supported configuration. A
one-line clarification to the decision would fully address the real content. That
does not rise to an open design finding of medium severity.

## Vote

REFUTED — true. The finding's exploitable path is out-of-scope (N6, ≥3 devices),
its core safety demand is already in the decision (ack-gated symmetric pruning),
its wire-signal gap is covered by the reserved `0x08+`/`featureFlags` mechanism it
itself acknowledges, and its recommendations largely duplicate the status quo. The
residual is a doc-wording nit, not a data-loss defect; severity is overstated.
