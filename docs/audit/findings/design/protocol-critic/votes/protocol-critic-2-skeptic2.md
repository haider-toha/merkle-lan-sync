# Skeptic #2 vote — protocol-critic-2 (VV pruning has no wire signal / shrunk-INDEX unsound)

- Finding: `docs/audit/findings/design/protocol-critic/protocol-critic-2-vv-pruning-unsound-signal.md`
- Decision under attack: `docs/audit/decisions/protocol/vv-pruning-counter-cleanup.md` (Option A)
- Vote: **REFUTED** (severity overstated; central example out-of-scope; one sub-claim factually wrong; recommendations already substantially adopted)
- Confidence: medium

## What the finding gets right (steel-man)

The technical kernel is correct: `Compare` treats an absent counter as `0`
(`version-vectors.md` §4.3 lines 263-266; `PR-2` lines 52-56). So *if* one peer
exported a unilaterally-shrunk vector `{self:5}` and a live peer still carried
`{self:5, D:4}`, the shrunk side would be `DominatedBy` the unshrunk side
(`0 < 4` in D's slot) and the D-counter could be re-merged, or a self-bump could
read as `Concurrent`. The observation that an **implicit shrunk INDEX, used as the
signal, would be unsound** is valid in the abstract. That is the finding's only
load-bearing insight, and it is a wording/specification nit, not the dramatic
"resurrects the ghost / conflict storm" the title claims. The rest does not hold up.

## Why it is refuted

### 1. The decisive worked example requires a configuration the project scopes out — and the finding admits it.

The "Worked sequence" (lines 70-74) and the FM-3 attack both need **three live
identities**: `self`, the removed `D`, and a *surviving live peer* `E` with a
different prune state. That is the N-device / multi-connection topology that
`problem-space-map.md` §2.2 **N6** marks **out of scope** (verified: line 111,
"2-device, single conn"). The finding concedes this directly in its own point 3
("there is transiently a third identity (ghost D + self + new E), which is N6 'out
of scope'") and again in Impact: "The common case (2 devices, never re-paired) is
**genuinely unaffected**." So by the finding's own admission the unsoundness only
appears in a configuration the engine does not support. A defect that can only be
triggered outside the supported scope does not justify reopening a decided file.

### 2. The decision is **ack-gated/symmetric**, not "unilateral shrink." The worked sequence violates the decision's own invariant and then blames it.

The chosen mechanism's correctness rests entirely on: "the drop is only considered
complete once **both live peers have applied it** (so vectors stay *equally*
pruned — defeats FM-3)" (vv-pruning lines 47-49, restated in Decision lines 82-83
and Rationale line 93). The finding's worked sequence does the opposite — "self
prunes → `{self:5}`" while "peer E is briefly offline, still `{self:5, D:4}`" —
i.e. it executes a **unilateral** prune and exports the shrunk vector for
comparison before the live peer has acked. That is precisely the behaviour the
ack-gate forbids. Constructing the one sequence the decision rules out and
attributing its consequences to the decision is a strawman, not a refutation.

### 3. Point 3 ("the ack-gate cannot close when it fires") is factually wrong.

The finding argues the ack can never arrive because "you `DropCounter(D)` because D
was removed; D is gone and cannot acknowledge." This misreads the gate. The
required acknowledgement is from the **surviving live peer** (the decision says
"both **live** peers"), never from the removed device `D`. `D`'s departure is
irrelevant to whether `E` acks. The whole "the gate cannot close" argument
collapses once the actor of the ack is read correctly.

### 4. In the *supported* (2-device) scope the implicit shrink is sound — which is also recommended-change #3.

For the strict 2-device tool, removing the peer leaves **no live peer to be unequal
with**; pruning the dead counter is a purely local data operation with nobody to
manufacture an FM-3 conflict against. The finding's own recommended option 3
("Scope `DropCounter` to un-pair the last peer … sound because there is no peer
left to disagree") *is the configuration the project already is*. The finding thus
recommends, as a fix, the scope the decision already operates in (Consequences:
"the operator action 'remove a paired device' is the only pruning trigger;
automatic/aged pruning is explicitly out of scope", lines 104).

### 5. "No wire signal exists / catalogue can't carry a drop" is contradicted by the cited evidence.

The finding's own recommended option 2 ("explicit acked `DROP_COUNTER{shortID}` as
a reserved `0x08+` type behind `featureFlags`") notes "the catalogue already
supports this." It does: `message-type-enumeration.md` reserves `0x08+` and a
`featureFlags` negotiation bit precisely for forward-compatible control types
(lines 49, 104-110). So the safe wire signal the finding demands is already an
available, decision-sanctioned extension point — not a gap requiring the decision
to be reopened.

## Net assessment

The decision's core is exactly what the finding asks for: ack-gated, symmetric,
equal pruning, scoped to explicit device removal, with `0x08+`/`featureFlags`
reserved for a future explicit control message. The legitimate residual is small:
the decision *parenthetically* floats "the next INDEX simply no longer contains D's
counter" as a "cleaner" signal, and that particular phrasing, taken literally and
combined with a unilateral (un-gated) prune, would be unsound. That warrants a
one-line clarification ("the wire signal must be an explicit acked drop, not a
shrunk INDEX; never compare a shrunk vector against an un-shrunk one"), not a
reopen. The "resurrects the ghost / conflict storm on device replacement" impact is
overstated because it depends on the out-of-scope 3-identity transition the finding
itself flags as N6, and one of its three structural claims (point 3) is wrong.

Severity is overstated and the recommended change does not beat the status quo (it
largely restates it). **Refuted.**
