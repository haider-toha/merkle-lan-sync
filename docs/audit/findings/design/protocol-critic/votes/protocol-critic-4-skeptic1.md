# Skeptic #1 vote — protocol-critic-4 (Tombstone resurrection via author-wipe before propagation)

**VOTE: REFUTED (confidence: medium)**

## What the finding gets right (conceded)

The bare resurrection mechanism is technically real and I verified each link:

- After a true author-wipe, no tombstone survives on M (`tombstone-retention-gc.md`
  §7 / PR-4 §7 persist tombstones against *restart*, not *wipe*).
- The partitioned peer P, offline during the delete, also holds no tombstone (PR-4 §5).
- The diff treats a peer-only path with no local tombstone as a FETCH candidate
  (`SKILL.md` §2 lines 105-106, confirmed: "only remote has it → candidate to FETCH,
  OR a local deletion → tombstone check"; with no tombstone the check cannot fire).
- Reseed guard 2 merges P's VV for the path but M has no differing local content to
  bump, so nothing dominates P's copy (`vv-counter-seeding.md` lines 56-62).

So F is resurrected on the worked sequence. I am not disputing that a file comes back.

## Why I vote REFUTED anyway

### 1. The headline "false mitigation" claim mischaracterizes its own evidence

The finding's title and Impact assert PR-4 §5.1's "persisted snapshot + reseed
mitigates the wipe" claim is "unconditionally wrong." But read §5.1 line 94-96
verbatim: the mitigation is attached to **"Counter rollback (FM-4): a wiped peer
*re-authoring* with a low counter could make a tombstone fail to dominate."** That is
the *edit*-after-wipe data-loss case (`vv-counter-seeding.md` Context lines 31-48),
and for that case the reseed mitigation **is correct**. §5.1 never claims the
"deleter wiped before propagation" case is mitigated — the finding itself admits
(Evidence line 40) there is *"**no** 'deleter wiped before propagation' entry."*
You cannot simultaneously say the case is absent from the list AND that the list
falsely claims to mitigate it. The accurate charge is "an unenumerated mode," not a
"misclaim." Reclassifying an omission as an "unconditionally wrong" high-severity
false-claim is the overstatement the protocol asks me to catch.

### 2. The primary recommended fix is strictly worse than the status quo

Recommendation #2 (the only concrete protocol change) says a snapshot-less reseeding
device must treat "peer has a path I lack" as a **conflict copy / quarantined
adoption** rather than a live file. By the finding's own admission (line 99) the
device "genuinely cannot distinguish *delete-then-wipe* from *never-received*."

The dominant population of "peer has a path I lack during reseed" is **not**
delete-then-wipe — it is the **normal new-device-join / first-sync** case, where a
fresh device legitimately lacks *every* file the peer holds. Recommendation #2 would
quarantine **every file on first sync** into conflict copies. That converts the most
common reseed scenario (onboarding a device, the exact scenario guard 2 was built for,
`vv-counter-seeding.md` line 59 "true wipe / first run") into a directory full of
`.sync-conflict` junk. Trading a guaranteed regression of the common path to defend an
extremely rare conjunction is a net loss. The recommended change does **not** beat the
status quo; it is a counter-example against itself.

### 3. The residual is information-theoretically unrecoverable — i.e. not a design defect

A delete authored on exactly one device, replicated to **zero** peers, where that one
device is destroyed: there is no longer any bit anywhere in the system recording the
deletion. P holds a valid, causally-consistent, pre-delete file with a legitimate VV.
**No sync design can distinguish this from "P has a file the wiped device never saw."**
The finding concedes this in Recommendation #3 ("a deletion not yet acknowledged by
the peer is **not durable against author-wipe**"). A flaw that is provably unrecoverable
by *any* design is not a defect of *this* design — it is a fundamental property of
zero-replica durability. The actionable residue collapses to "document a known
limitation" (rec #1/#3), which is a low-severity doc nit, not a high-severity protocol
hole.

### 4. Severity "high" is overstated

- Likelihood is gated by a **triple conjunction**: (a) a delete that has propagated to
  no peer — but deletes are broadcast eagerly via INDEX_UPDATE the moment connectivity
  exists (PR-4 §4, SR-6), so the window is only "peer offline at delete time"; AND
  (b) total disk wipe + reinstall of the authoring device; AND (c) the wipe landing
  inside that same partition window. The finding admits this "bounds the likelihood."
- The blast radius is one un-acked file, and the design already minimizes the window
  (ack-gated retention, eager delete broadcast).
- Combined with #3 (unrecoverable) and #1 (the "false mitigation" framing is wrong),
  "high" is not justified. At most this is a low-severity documentation addendum.

## Net

The resurrection mechanism is real, but (1) the central "false-mitigation" charge
misreads §5.1, (2) the only concrete fix regresses the common first-sync path, (3) the
case is information-theoretically unrecoverable by admission, and (4) severity is
overstated. The finding is overstated and its remedy is harmful. **Refuted.** The
single defensible residue — add a one-line "delete authored on a single replica is not
durable against that replica's destruction" caveat to PR-4 — does not require a
high-severity finding or the proposed quarantine mechanism.
