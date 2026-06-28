---
id: tree-critic-4
title: The structural hash commits to retained tombstones, but ack-gated tombstone GC removes them non-simultaneously — so "converged ⇔ equal root hash" is transiently false for data-identical peers, and the post-GC index exchange is an under-specified reconciliation
severity: medium
status: rejected
phase: 3
role: tree-critic
area: folder-hash recompute / convergence oracle (structural-hash include/exclude set)
date: 2026-06-28
---

# tree-critic-4 — Tombstone GC mutates the root hash with no data change, so root-equality is not a sound "are we synced?" signal at an instant

## Claim

The design commits **retained tombstones into the structural hash** (a tombstone is
a hashed leaf, `content_hash=0`, `deleted=1`, included in its parent's
`nodeEncoding`) and simultaneously declares **"converged ⇔ identical root hash"**
(SR-5) the system's eventual-consistency oracle. But tombstones are **GC'd**
(removed from the tree) under an **ack-gated, two-sided** policy whose two sides fire
at *different instants* on each peer. Removing a tombstone leaf re-hashes its
ancestor chain up to the root. Therefore two peers that are **fully reconciled and
byte-for-byte data-identical** can hold **different root hashes** for the entire
GC-skew window — the biconditional SR-5 asserts is transiently **false** in a
correct, lossless steady state. Worse, the post-GC index exchange (one peer has GC'd
the tombstone, the other still advertises it) is precisely the "absence is
ambiguous" situation the design forbids resolving naively — and the design never
specifies how a peer reacts to "a tombstone I have already GC'd is advertised by my
peer."

## Evidence

- **Retained tombstones are in the structural hash.** D.3 tombstone rule: "`content_hash
  = 32×0x00`, `deleted = 0x01`, VV bumped. The flipped `deleted` byte + bumped VV
  make the tombstone's leaf hash distinct … so the deletion … changes the root"
  (`docs/audit/decisions/merkle/leaf-shape-and-structural-hash.md:178-180`). A
  tombstone is a `leafEncoding` and appears in its parent's child list
  (`leaf-shape-and-structural-hash.md:147-158`). So while retained, a tombstone is
  part of the root hash.

- **GC removes the whole tombstone `FileInfo`, two-sided and observation-driven.**
  tombstone-retention Option A: "A tombstone is retained until the engine observes,
  in the peer's advertised index, a VV … that dominates-or-equals the tombstone's VV
  … Only then may **both** peers GC it. GC is symmetric: a peer GCs only after it has
  both *sent* its tombstone and *seen* the peer's acknowledgement"
  (`docs/audit/decisions/protocol/tombstone-retention-gc.md:34-48,70-75`); "GC
  removes the whole tombstone `FileInfo`" (`tombstone-retention-gc.md:91-93`). Each
  peer's "seen the acknowledgement" event happens when *it* next processes the
  other's index — **not** at the same wall-clock instant. So there is necessarily a
  window where peer X has removed the tombstone (root R1) and peer Y still holds it
  (root R2 ≠ R1).

- **The oracle is stated as a clean biconditional and is consumed as a control
  signal.** SR-5: "after changes settle, both peers expose the **identical** Merkle
  root hash … 'converged' and 'equal root' are the same statement"
  (`docs/audit/rules/sync-rules.md:76-88`); leaf-shape Consequences: "their **root
  hashes are bit-identical**" (`leaf-shape-and-structural-hash.md:115-117`). The
  flow-verifier checks it by sampling: "after a change settles, both trees expose the
  identical root hash" (`plan/agent_roster.md:95`). The prune-equal differ uses
  subtree-hash equality to decide what to even look at
  (`MK-2-diff-reconciliation.md:16-23`). Equal-hash⇒equal-data is still *sound* (so
  pruning is safe); the problem is the *converse* the oracle relies on: here
  equal-data does **not** imply equal-hash during GC skew.

- **GC manufactures a fresh "absence is ambiguous" event.** Once X has GC'd and Y
  has not, the next `INDEX`/`INDEX_UPDATE` from Y advertises a tombstone for a path X
  no longer has any record of. To X this is "a path present only on the peer" — the
  exact single-sided case MK-2 says must be "crossed against version vectors and
  tombstones before acting" and "must not itself decide create-vs-delete"
  (`MK-2-diff-reconciliation.md:36-40,67-74`). But X has *nothing* to cross it
  against (it GC'd the record). The design's symmetric-GC assumption ("both may GC
  it") quietly assumes simultaneity that two independently-clocked observers do not
  have, and never specifies X's correct response ("I already GC'd this tombstone —
  ignore the advertisement, do not re-create the file and do not re-mint the
  tombstone"). A naive implementation re-learns the tombstone (harmless churn) or, if
  it instead reasons "peer has a delete I don't, maybe I should propagate it back,"
  re-introduces a delete event — and the symmetric-GC invariant is broken.

- **Conflict-copy creation has the same non-monotonic-root flavor** (cross-ref, not
  the core of this finding): a `.sync-conflict-<UTC-date>-<UTC-time>-<DeviceID>.<ext>`
  copy changes the root when created, and the design does not pin the timestamp to a
  deterministic source (e.g. the loser's mtime) — if each peer stamps its own
  wall-clock `now()`, the two peers create *differently named* copies and the roots
  never converge (`SKILL.md:163-169`, `sync-rules.md:108-129`). That naming
  determinism is protocol-critic's lane, but it is a second way "equal root ⇔
  converged" can be violated by the conflict/tombstone lifecycle rather than by data.

## Impact

- **Flaky convergence oracle (medium):** the flow-verifier and any "are we synced?"
  health/idle signal can read unequal roots on a fully-converged, lossless pair
  during GC skew → false "diverged" verdicts, or worse, an engine that treats
  "roots equal" as "stop / go idle" can flap (idle → GC changes root → not-equal →
  re-diff → no-op → idle …).
- **Wasted work (low):** the prune-equal differ recurses into subtrees that are
  data-identical but tombstone-skewed, then VV-compares to a no-op — extra walks, not
  incorrect results.
- **Under-specified post-GC reconciliation (medium, with a high tail):** the
  "peer advertises a tombstone I already GC'd" step has no specified handling; the
  benign outcome is churn, the bad outcome (if mis-handled as a propagatable delete or
  a resurrection candidate) re-opens SR-10. The design's claim that GC is "symmetric"
  papers over the fact that the two GCs are not simultaneous.

## Recommended change (beats the status quo)

1. **Define "converged" precisely and make the oracle monotonic.** Either (a)
   **exclude GC-eligible tombstones from the root hash** once they are acked by the
   peer (so GC does not change the root — the tombstone has already done its job and
   both sides agree it is dead), turning GC into a hash-neutral operation; or (b)
   define the convergence oracle over the **live (non-deleted) sub-state** plus a
   separately-reconciled tombstone set, so root-equality reflects user-visible data,
   not GC lifecycle. Option (a) is the smaller change and keeps a single root hash.
2. **Specify the post-GC reconciliation step:** receiving an advertised tombstone for
   a path with **no local record** is a *no-op* (already GC'd) — never a create, never
   a re-mint, never a propagatable delete. State this alongside MK-2's single-sided
   rule so the differ/resolver is total.
3. **Make GC genuinely two-sided-safe:** gate GC on "I have seen the peer ack **and**
   I have confirmed the peer has seen *my* GC intent" (or simply retain the tombstone
   as a hash-neutral, GC-eligible marker per (1a) until both sides quiesce), so the
   two peers never sit in the "one has it, one doesn't, and it's in the root hash"
   state.
4. **Pin conflict-copy naming to a deterministic source** (the loser's mtime +
   DeviceID, not `now()`), so both peers generate the identical conflict-copy name and
   the root converges — and add a test that two peers independently resolving the same
   conflict produce **one** identically-named copy. (Hand to protocol-critic; noted
   here because it is the sibling "root never converges" hazard.)
5. **Acceptance test:** delete a file, let both peers converge and ack; then drive the
   GC on each peer and assert that at every observable instant the convergence oracle
   used by the flow-verifier does not report the data-identical pair as diverged, and
   that a tombstone advertised after the peer GC'd it is a no-op (no resurrection, no
   re-mint).
