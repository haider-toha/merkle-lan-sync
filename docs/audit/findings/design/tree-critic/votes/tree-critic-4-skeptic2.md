# Skeptic #2 vote — tree-critic-4 (tombstone GC breaks root-equality oracle)

- Vote: **REFUTED**
- Confidence: **medium**
- Skeptic: skeptic #2 of 3
- Date: 2026-06-28

## Summary

The finding's *citations* are accurate (the quoted lines from
`leaf-shape-and-structural-hash.md`, `tombstone-retention-gc.md`, `sync-rules.md`
SR-5, and `MK-2-diff-reconciliation.md` all exist and say what is quoted). But the
*reasoning built on top of them is flawed in three load-bearing ways*, the claimed
under-specification is already resolved by composition of existing invariants, the
headline recommendation would regress the design, and the severity is overstated. I
vote to refute.

## Why the core claim does not hold

### 1. SR-5 is explicitly a *quiescence* oracle; the finding attacks an "at an instant" reading SR-5 never asserts.

SR-5 reads: "**after changes settle**, both peers expose the identical Merkle root
hash" and the rule body says "once propagation **quiesces**"
(`sync-rules.md:76-88`). GC is itself a tree mutation — it removes a leaf and
re-hashes the ancestor chain (the finding's own evidence,
`leaf-shape-and-structural-hash.md:178-180,186-198`). A mutation that one peer has
applied and the other has not is, by definition, *mid-propagation* — the system is
**not quiesced**. The GC-skew window is therefore squarely inside the propagation
interval that SR-5 explicitly excludes from its claim. The finding's "transiently
false" is an attack on a strawman instantaneous reading of SR-5, not on what SR-5
states.

### 2. The finding redefines "converged" as "data-identical," then complains the oracle doesn't match the redefinition (begging the question).

The design defines convergence as **equal full state**, and deliberately commits
`deleted` and the version vector into the structural hash *precisely so that*
"converged ⇔ equal root holds even for ... tombstones"
(`leaf-shape-and-structural-hash.md:113-114`). Under the design's own definition,
two peers where one still holds a tombstone record and the other has GC'd it have
**genuinely different state** and are therefore **not converged**. An unequal root
in that window is the *correct* answer, not a false negative. The finding manufactures
the contradiction only by silently substituting "data-identical" for "converged" —
question-begging. The finding even concedes the sound direction holds
("Equal-hash⇒equal-data is still sound, so pruning is safe"), which means the differ
is never *incorrect* — at worst it does a no-op extra walk.

### 3. The "post-GC absence is ambiguous, never specified" claim is false — existing invariants compose to a total, safe function.

The scenario: X has GC'd, Y still advertises the tombstone. The finding claims X
"has nothing to cross against" and the design "never specifies X's response." Both
are wrong:

- The advertised remote `FileInfo` is a **tombstone** (`deleted=true`,
  `content_hash = 32×0x00`). It is not a live file, so the "absence is ambiguous /
  is-this-a-create" hazard (which is about a *live* single-sided file) does not
  apply: a tombstone carries no content, so X **cannot** "re-create the file" — the
  finding's headline bad outcome is physically impossible.
- MK-2 already specifies the handling: a single-sided node "is emitted as a
  candidate and handed to the VV+tombstone resolver — it must not itself decide
  create-vs-delete" (`MK-2-diff-reconciliation.md:73-74`). With no local record, the
  remote tombstone trivially dominates the absent local state; applying it means
  "ensure the path is deleted," which is a **no-op** because the path is already
  absent (SR-3 idempotent content-addressed apply, `sync-rules.md:48-61`).
- The "re-introduce a delete event / propagate it back" bad outcome requires X to
  **broadcast on a non-local change**, which SR-6 independently forbids
  ("Receiving and applying a remote file must not bump our counter and must not
  broadcast", `sync-rules.md:89-106`). So the only reachable outcomes are (a) no-op
  or (b) harmless re-learn of the tombstone — and the finding *itself* admits the
  re-learn is "harmless churn."
- Re-learning a tombstone is the **safe** direction. SR-10 is about *resurrection*
  (re-creating deleted data); learning a tombstone is the opposite and can never
  resurrect anything. The finding's "re-opens SR-10 (high tail)" inverts the actual
  risk direction.

## The recommended change does not beat the status quo

- **Rec 1(a) — "exclude GC-eligible tombstones from the root hash once acked"**: this
  is actively *worse*. It makes the root hash depend on per-peer **ack state**, which
  is not a property of the tree. The root would become peer-relative ("which
  tombstones has *this* peer's view acked"), destroying the deterministic
  content-address property, breaking the golden-vector / Mac↔Windows round-trip
  determinism the design fought for (`leaf-shape-and-structural-hash.md:92-96`,
  SR-13), and making two peers' roots differ *more*, not less.
- **Rec 1(b) — split convergence into live-substate + separate tombstone
  reconciliation**: a significant re-architecture that replaces one deterministic
  root hash with two reconciliation domains — strictly more code/test surface for a
  transient, cosmetic skew. The single-root design (leaf-shape Option D) was chosen
  deliberately over exactly this kind of split.
- **Rec 3 — gate GC on "peer saw my GC intent too"**: adds a new wire handshake the
  decision explicitly avoided ("no new wire round-trip needed",
  `tombstone-retention-gc.md:42-43`) and **does not even close the window** — two
  independently-clocked observers can never be simultaneous; this just relocates the
  skew to the GC-intent-ack exchange. Chasing simultaneity between distributed
  observers is chasing a property that does not exist.
- **Rec 2 / Rec 5** (state the no-op explicitly + add a test) are the only benign
  parts, but Rec 2 is already entailed by SR-3 + SR-6 + SR-9 + the MK-2 resolver, so
  it is a documentation nicety, not a correctness fix.
- **Rec 4** (conflict-copy naming determinism) is, by the finding's own admission,
  out of scope ("protocol-critic's lane") — padding an unrelated hazard into the
  finding rather than strengthening its thesis.

## Severity is overstated

By the finding's own Impact section the live consequences are: "wasted work (low) —
extra walks, not incorrect results" and post-GC reconciliation whose "benign outcome
is churn." No data-integrity invariant is actually breached (SR-10 holds; the only
movement is in the safe tombstone-learning direction). The remaining issue is a
narrow, true-but-minor non-monotonicity of the root across the GC lifecycle that
*could* make a naive "idle when roots equal" health check blink during a
sub-second propagation window the oracle's own definition already excludes. That is a
**low**-severity flow-verifier timing nuance plus a doc clarification, not a
medium-severity design defect warranting the proposed re-architecture.

## Kernel of truth (acknowledged, but insufficient)

There is a real, narrow observation worth a one-line doc note: GC mutates the root,
so a consumer must treat "roots equal" as meaningful only *at quiescence*, and the
"advertised-tombstone-for-no-local-record ⇒ no-op" rule is worth writing down
explicitly next to MK-2's single-sided rule. That is a minor clarification the
planner can fold in for free. It does not validate the finding's central claim ("the
convergence oracle is unsound" / "re-opens SR-10") and does not justify the headline
recommendation, which would damage the design.

## Verdict

The oracle is sound under its stated quiescence scope; the "under-specified post-GC"
step is already total via SR-3/SR-6/SR-9 + the MK-2 resolver; the bad outcomes
require violating independently-enforced invariants or are physically impossible; the
primary recommendation regresses determinism; severity is overstated. **REFUTED**
(confidence medium — a small doc clarification is warranted, but the finding as
written does not stand).

VOTE: REFUTED
