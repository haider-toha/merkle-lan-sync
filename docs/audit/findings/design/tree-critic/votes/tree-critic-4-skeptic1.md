# Skeptic vote — tree-critic-4 (skeptic #1)

- Finding: tree-critic-4 — "Tombstone GC mutates the root hash with no data change,
  so root-equality is not a sound convergence oracle at an instant; post-GC index
  exchange under-specified."
- Vote: **REFUTED**
- Confidence: **high**
- Date: 2026-06-28

## Summary

The finding's factual premises (tombstones are committed to the structural hash;
GC removes the whole `FileInfo`; the two peers' GC events are not simultaneous) are
all true and well-cited. But the conclusion does not follow: it rests on a misreading
of SR-5, inflates an ordinary self-healing propagation transient into an "oracle
defect," fabricates a resurrection "high tail" that the tombstone semantics make
impossible by construction, and recommends changes that are strictly worse than the
status quo. The finding should be rejected.

## Why the claim fails

### 1. SR-5 is a *quiescent* biconditional, not an instant-by-instant one — the finding attacks a strawman

SR-5's own text is "after changes settle, both peers expose the identical Merkle root
hash … once propagation quiesces" (`docs/audit/rules/sync-rules.md:76-88`). It never
asserts equality at every wall-clock instant. The finding's title concedes the defect
is only "transiently false … at an instant" — i.e. it concedes the violation does not
exist at the only moment SR-5 actually quantifies over (quiescence).

GC is itself a state change that propagates. The "data-identical but root-divergent"
window the finding describes is the period during which the GC operation has *not yet
quiesced on both sides*. That is identical in kind to the root divergence that exists
during the propagation of *any* edit (A edits a file → roots differ until B applies
it → roots re-converge). Convergence is eventual, not instantaneous; complaining that
roots differ mid-propagation of a change is complaining that the system is not
strongly consistent, which it never claimed to be.

The lifecycle has two quiescent states, **both with equal roots**:
(i) both peers hold the tombstone (post-delete, pre-GC — SR-5 holds), and
(ii) both peers have removed it (post-GC — SR-5 holds). The skew exists only during
the transition between them and self-heals because GC is symmetric (both peers
eventually fire). SR-5 is satisfied at every point it actually asserts anything.

### 2. "Data-identical" is a category error — tombstone/VV state IS replicated state

The leaf-shape decision deliberately commits `version_vector` and `deleted` to the
structural hash precisely so that "converged ⇔ equal root holds even for
same-bytes-different-history files and for tombstones"
(`leaf-shape-and-structural-hash.md:113`, D.1 table). A peer that holds a tombstone
`FileInfo` and a peer that has GC'd it do **not** hold identical replicated state —
one has a record, the other does not. The design is *correct* to give them different
roots: they genuinely differ in replicated metadata. The finding's "byte-for-byte
data-identical" framing smuggles in the assumption that tombstones are mere "absence";
the entire design (and SR-9/SR-10) treats a tombstone as a first-class versioned event,
not an absence. Equal user-visible bytes ≠ equal replicated state, and SR-5 is defined
over the latter by design.

### 3. The "resurrection high tail" is impossible by construction — the post-GC exchange is already total

The finding's most severe claim ("medium, with a high tail" / re-opens SR-10) is that
a tombstone advertised by Y to an already-GC'd X is an "absence is ambiguous" event
that could be mis-resolved into a create or a propagatable delete. This is wrong:

- The advertised record is itself a **tombstone** (`content_hash = 32×0x00`,
  `deleted = 0x01` — `leaf-shape-and-structural-hash.md:178-180`). MK-2's "absence is
  ambiguous" rule concerns a child *present on only one side with no versioned record*
  to disambiguate create-vs-delete. A `deleted=true` FileInfo is the disambiguation —
  it explicitly says "delete." The resolver can never read a `deleted=1`,
  `content_hash=0` record as a "create"/resurrection. The high-tail outcome is
  structurally unreachable.
- The "propagate the delete back" outcome is bounded by SR-3 idempotency / VV
  comparison: Y's tombstone VV equals-or-is-dominated-by what X already applied
  (X only GC'd *because* it saw Y's ack — `tombstone-retention-gc.md:38-48`), so the
  comparison yields a no-op. The finding itself concedes this path is "harmless churn."

So the only real residual is whether the spec explicitly writes the sentence
"a tombstone for a path with no local record is a no-op." That is already entailed by
composing MK-2 (single-sided records are crossed against VV+tombstones, never
self-classified) with the tombstone semantics above. At most this is a one-line
documentation clarification, not a medium correctness finding.

### 4. The recommended changes are strictly worse than the status quo

- **Rec 1(a) "exclude acked tombstones from the root hash"** breaks the load-bearing
  property the whole leaf-shape decision was built to guarantee: the root hash is a
  **deterministic pure function of the local FileInfo snapshot**
  (`leaf-shape-and-structural-hash.md:88-96`, Option D correctness rationale, golden-
  vector testability). Making inclusion depend on per-peer ack state turns the root
  into a *peer-relative, non-deterministic* value — it destroys golden-vector tests,
  the Mac↔Windows round-trip equality test (SR-13), and is ill-defined for the >2-peer
  generalisation already noted in scope (`tombstone-retention-gc.md:97`). This trades a
  benign, self-healing transient for a permanent loss of the convergence model's
  foundational guarantee.

- **Rec 3 "gate GC on confirmation the peer saw my GC intent"** cannot achieve its
  stated goal. Two independently-clocked observers can never transition simultaneously;
  a GC-intent ack is itself non-simultaneous, so this merely relocates the skew to the
  intent-exchange (infinite regress), while re-introducing exactly the extra wire
  round-trip the decision deliberately avoided ("no new wire round-trip needed" —
  `tombstone-retention-gc.md:41`). More cost, same unavoidable transient.

- **Rec 5 (conflict-copy naming)** is admitted by the author to be out of lane
  ("protocol-critic's lane," "not the core of this finding") and is also factually
  stale: SR-7 already pins the deterministic name
  `<name>.sync-conflict-<UTC-date>-<UTC-time>-<deviceID>.<ext>` with a deterministic
  loser-selection tiebreaker so "both peers independently pick the same loser," and the
  copy then syncs as a normal file (`sync-rules.md:108-129`). The finding cites SKILL.md
  without checking that SR-7 already closes the gap. It is padding that should not bolster
  severity.

### 5. Severity is overstated

The genuine, residual effect is: a brief root inequality during a deletion's GC phase,
self-healing, no data loss, no resurrection, identical in nature to normal edit
propagation. The flow-verifier is specified to assert "after a change settles"
(`plan/agent_roster.md:95`) — sampling mid-GC and declaring divergence would be a
test-harness quiesce-then-assert bug, not a design defect. The "idle↔not-idle flap"
is bounded (re-diff → VV no-op → idle). This is low at most, and arguably a non-finding
once the one-line "tombstone-for-unknown-path is a no-op" clarification (already
entailed) is acknowledged.

## Conclusion

Premises true, conclusion unsupported. The finding misreads SR-5, mislabels distinct
replicated states as "identical," fabricates an impossible resurrection tail, and
recommends a fix (peer-relative hash) that destroys the design's core determinism
property plus a fix (GC-intent handshake) that cannot in principle eliminate the
transient. **REFUTED**, high confidence.
