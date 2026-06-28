# Skeptic #2 vote — protocol-critic-4 (Tombstone resurrection via author-wipe before propagation)

**VOTE: refuted=true (confidence: medium)**

## Summary

The finding's *bare mechanism* is technically real (a delete authored on M, then M
wiped with no surviving snapshot, then reunion with a partitioned peer P holding the
pre-delete copy, yields a resurrected file). But the finding's load-bearing rhetorical
claims — the "false mitigation," the "unconditionally wrong" framing, the HIGH
severity, and the flagship recommended fix — do not hold up. The residual gap is an
inherent information-theoretic limit the finding itself concedes is unfixable, and its
proposed remedy regresses the dominant case catastrophically. Net: not a HIGH-severity
design defect; at most a one-line doc/test hygiene note.

## Why I refute

### 1. The "false mitigation claim" is a misreading — the case is *omitted*, not *misclaimed*

The finding's headline is that "PR-4's 'persisted snapshot + reseed mitigates the
wipe' claim is false for deletions" and is "unconditionally wrong." Read the actual
text. PR-4 §5.1 (lines 84-96) lists three vectors; the only one citing the
snapshot+reseed mitigation is **"Counter rollback (FM-4): a wiped peer *re-authoring*
with a low counter could make a tombstone fail to dominate"** (lines 94-96). That
bullet is explicitly about a wiped peer that **re-creates content** and whose new low
counter might lose to an *existing* tombstone — a different vector from "the deleter is
wiped and the only tombstone is destroyed." The finding even admits the deleter-wipe
case is **"a direct fourth"** that PR-4 **"omits"** (lines 14-20, 39-40). You cannot
simultaneously assert the case is *omitted from the enumeration* and that the design
*falsely claims to mitigate it*. PR-4 §5.1 makes no mitigation claim for the
deleter-wipe scenario; the "false-mitigation" framing attacks a claim the document does
not make. (`PR-4-deletions-tombstones-resurrection.md:94-96`,
`vv-counter-seeding.md:56-62`.)

### 2. The gap is an inherent, unfixable limit the finding itself concedes — not a design defect

The finding's own recommendation #3 (lines 104-108) states the truth: "a deletion not
yet acknowledged by the peer is **not durable against author-wipe**." This is
information theory, not a flaw in *this* design: if the sole record of an operation
exists on one machine and that machine is destroyed before the operation replicates,
the operation is lost. No sync system can recover information that never escaped the
wiped device. Given that **no delete record survives on either peer** (the finding
concedes this, lines 58-60), the only state spanning both machines after reunion is
"file exists." Convergence to "file present" is therefore the *consistent* outcome, not
a betrayal of an accepted deletion — the system never durably accepted it (not acked,
not propagated, not even snapshotted). Calling the inevitable consequence of a destroyed
sole-copy "the marquee bug the tombstone design exists to prevent" (lines 83-90) is
unfair: the tombstone design exists to stop a *stale peer* from resurrecting a delete
that the system *did* durably record — exactly the SR-10 case it does handle
(`PR-4 ...:67-82`).

### 3. The flagship recommended fix (#2) is actively worse than the status quo

Recommendation #2 (lines 97-103) says a snapshot-less reseeding device must not treat
"peer has a path I lack" as an authoritative remote create, and should instead
quarantine such paths as conflict copies. But a freshly-wiped/reinstalled device lacks
**every** file the peer holds — that is the entire point of a reseed. The finding also
concedes the device "genuinely cannot distinguish delete-then-wipe from
never-received" (lines 99-100), so there is **no heuristic to narrow** the rule. The
only literal implementation is: quarantine the *entire folder* as `.sync-conflict`
copies on every legitimate reinstall / new-device-join. That is a catastrophic
regression to the dominant cold-start case (the common reason reseed exists per
`vv-counter-seeding.md:56-62`) in exchange for blunting a rare conjunction. The
recommended change does not beat the status quo; it is strictly worse for the common
path.

### 4. The "internal inconsistency" argument is not an inconsistency

The finding claims it is inconsistent that guard 2 handles wipe for edits but the
tombstone design does not for deletes (lines 61-68). This asymmetry is *correct*, not
inconsistent: after a wipe, an edited file's **bytes survive on disk**, so guard 2 can
re-assert authorship of content that physically exists; a not-yet-propagated delete
leaves **nothing** on disk to re-assert. Guard 2 works for edits *because* the evidence
survives the wipe and provably cannot work for deletes *because* it does not. Treating
them differently is information-theoretically forced, not an oversight.

### 5. Severity HIGH is overstated

The finding itself concedes the conjunction (delete pending replication + total
author-wipe-with-snapshot-loss + peer simultaneously offline holding a pre-delete copy)
"bounds the likelihood" (lines 87-89). The design already minimizes the only window
that matters: deletes broadcast eagerly on confirmed scan (SR-6) and retention is
ack-gated (`tombstone-retention-gc.md`), so on a normal LAN the delete propagates in
seconds and the vulnerable interval is delete-confirm-to-peer-ack. The finding's own
mitigation suggestion ("propagate deletes eagerly, ack-gate retention as already
designed," line 107) is **already the design**. Residual exposure is a narrow,
already-minimized window for a rare event whose outcome is the only consistent state.

## What is genuinely useful (and survives as a minor note, not a HIGH finding)

- Recommendation #1 (add this vector explicitly to the SR-10 resurrection enumeration)
  is fair doc hygiene — enumerations of resurrection vectors *should* list it as a
  documented known limitation per rec #3.
- Recommendation #4 (a negative test asserting no *silent clean* re-adoption) is
  reasonable test coverage.

These are documentation/test improvements, not a HIGH-severity design defect, and they
do not require the harmful rec #2. Because the finding's central claims (false
mitigation, unconditional wrongness, HIGH severity) are overstated/misread and its
headline fix regresses the common case, I vote **refuted**.
