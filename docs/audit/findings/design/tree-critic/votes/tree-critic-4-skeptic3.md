# Vote — tree-critic-4 (skeptic #3): REFUTE

- Finding: tree-critic-4 — "Tombstone GC breaks the root-equality convergence oracle"
- Vote: **REFUTED** (severity overstated; primary remedy is actively worse)
- Date: 2026-06-28
- Reviewer role: skeptic #3 of 3

## Summary

The finding's central technical claim — that SR-5's "converged ⇔ equal root hash"
is *violated* by tombstone GC — rests on a strawman reading of the oracle as an
**instantaneous** biconditional. SR-5's actual rule text and the flow-verifier's
actual contract both define the oracle over the **settled/quiescent** state, not
"at every observable instant." Under the correct (as-written) reading the oracle is
sound, and the finding's headline failure mode does not exist. The finding also
mis-frames a normal propagation transient as a special hazard, declares an already-
total reconciliation step "under-specified," and recommends a primary fix (1a) that
would **break** the load-bearing pure-function property of the structural hash.

## Why the core claim fails

### 1. SR-5 is an eventual oracle; GC is just another propagating change.

- SR-5 rule text: *"the reconciliation algorithm must drive any two connected peers
  to bit-identical root hashes **once propagation quiesces**"* and *"**after changes
  settle**, both peers expose the identical Merkle root hash"*
  (`docs/audit/rules/sync-rules.md:76-88`).
- The flow-verifier consumes it identically: *"eventual consistency (**after a change
  settles**, both trees expose the identical root hash)"* (`plan/agent_roster.md:95`).

GC is a state change that propagates (each peer removes the tombstone `FileInfo`).
During the GC-skew window the system has **not** quiesced — a change is still in
flight, exactly as when peer A writes a file and A's root flips before B applies it.
*Every* file propagation produces a transient root inequality; the finding singles
out GC as if it were special, but it is the same class of transient the oracle has
always excluded by the words "after settle / once propagation quiesces." At full
quiescence both peers have GC'd → bit-identical roots. The biconditional holds where
it is claimed to hold.

### 2. "Data-identical ⇒ converged" is the finding's own false premise.

The design deliberately commits the tombstone (`deleted` + bumped VV) to the
structural hash *specifically so that the tombstone set is part of replicated state*:
"`version_vector` … **yes** (so 'converged ⇔ equal root' holds even for … tombstones)"
and "`deleted` … **yes**" (`leaf-shape-and-structural-hash.md:113-114`). Therefore two
peers with **different tombstone sets are not in the same replicated state**, even if
their live file bytes match. During GC skew the tombstone sets genuinely differ, so
unequal roots **correctly** report "not yet converged." The oracle is *sound*; it is
the finding that conflates user-visible file data with full replicated state and then
calls the resulting (correct) inequality a bug.

### 3. The "under-specified post-GC reconciliation" is already total.

The finding claims that when X has GC'd and Y still advertises the tombstone, X "has
nothing to cross it against" and the design "never specifies X's response." It does,
by composition of existing rules:

- MK-2: a single-sided node is emitted as a *candidate*, resolved by crossing VV +
  tombstones (`MK-2-diff-reconciliation.md:36-40,67-74`). Crossing "remote tombstone
  (file absent) vs local absent" = **already in the desired state ⇒ no-op**. There is
  no ambiguity here: a delete advertisement for a path you do not have means "ensure
  absent," and it is already absent.
- SR-6: applying/ignoring a remote update is **not** a local authorship event, so it
  produces **zero outbound broadcasts** (`sync-rules.md:89-106`). This forecloses the
  finding's feared "re-introduce a propagatable delete" loop by construction.
- Even the benign "re-learn the tombstone" outcome the finding worries about is
  self-healing: the GC gate ("peer's advertised VV dominates-or-equals the tombstone
  VV", `tombstone-retention-gc.md:34-48`) is satisfied immediately by the very index
  that re-taught it, so X re-GCs on the next pass. Bounded churn, not a correctness
  break, and never a resurrection (SR-10 is gated on VV domination, untouched here).

So the differ/resolver is already total over this case; nothing new is required for
correctness. At most, an explicit one-line note "absent + remote tombstone = no-op"
is a doc nicety — informational, not a medium defect.

## Why the recommended change does NOT beat the status quo

- **Rec 1a (exclude acked tombstones from the root hash): actively harmful.** It makes
  the hash depend on per-peer *ack observation state*, destroying the property the
  whole leaf-shape decision is built on — that the structural hash is a **pure function
  of an immutable `FileInfo` snapshot** (`leaf-shape-and-structural-hash.md:89-96`).
  The hash would become peer-relative and non-deterministic, defeating golden-vector
  and Mac↔Windows round-trip testability (SR-13). Worse, "acked" is itself observed at
  different instants on each peer — so it **moves the skew, not removes it**.
- **Rec 1b (two oracles: live sub-state + separate tombstone reconciliation):** more
  machinery and a second reconciliation path to solve a transient that is identical to
  every other propagation transient. Strictly more complexity, no correctness gain.
- **Rec 3 (gate GC on "peer has seen my GC intent"):** re-introduces a wire round-trip
  the decision *deliberately avoided* — Option A was chosen precisely because the ack
  signal is "already on the wire for free … no new wire round-trip needed"
  (`tombstone-retention-gc.md:34-48,79-81`). This adds cost to fix a harmless,
  self-healing transient.
- **Rec 4 (conflict-copy naming):** the finding itself disclaims this as out-of-lane
  (protocol-critic) and "not the core." It cannot carry the finding's severity.

The only survivable recommendation is Rec 2 as a **documentation clarification**
(state the no-op explicitly, and tighten SR-5's wording to say "at quiescence"). That
is an informational nit, not the medium-severity correctness/spec gap claimed.

## Severity

Overstated. The "flaky oracle / flap" impact assumes a consumer that treats
*instantaneous* root-equality as a hard control signal mid-propagation — which no
correct sync engine (and not the flow-verifier, which samples after settle) does, and
which SR-6 prevents from looping regardless. The "high tail" on post-GC reconciliation
evaporates once MK-2 + SR-6 are composed. Real residual = a doc clarification →
**informational at most**, not medium.

## Verdict

REFUTED. The core claim depends on reading an explicitly eventual oracle as
instantaneous; the design's tombstone-in-hash choice makes the GC-skew inequality a
*correct* "not yet converged" signal; the allegedly missing reconciliation step is
already total via MK-2 + SR-6; and the headline remedy (1a) would break the pure-
function hash invariant. A one-line doc clarification is the only defensible
takeaway, which does not support a medium open finding.
