---
id: protocol-critic-2
title: Version-vector pruning (ack-gated DropCounter) has no wire signal, and the proposed implicit signal (a shrunk INDEX) is unsound under the system's own Compare rule — it re-adds the ghost counter or manufactures FM-3 false conflicts
severity: medium
status: rejected
area: version-vector growth + pruning
---

# protocol-critic-2 — `DropCounter` as specified is either a no-op or actively harmful; the #10590 defense it claims is not actually built

## Claim

The pruning decision adopts an "ack-gated, symmetric `DropCounter(id)` applied only
on explicit device removal," and asserts this "kills the ghost-counter class (FM-1)"
and "defeats FM-3." It does not, because the mechanism is under-specified in the one
way that matters and the only concrete mechanism it *does* propose is unsound:

1. **No wire signal exists.** The frozen 7-type catalogue has no message that tells
   a peer "drop device D's counter." The decision suggests "a `CLOSE`-with-reason
   or, more cleanly, the next `INDEX` simply no longer contains D's counter."
2. **The implicit "shrunk INDEX" signal is provably unsafe under the project's own
   `Compare`.** A counter that is *absent* compares as `0`. So a vector that has
   dropped D (`{self:5}`) is **DominatedBy** an otherwise-equal vector that still
   carries D (`{self:5, D:4}`), because in the D-slot `0 < 4`. The un-pruned peer
   therefore treats the pruned peer's `FileInfo` as **stale** and pushes the
   D-bearing version back — *re-adding the ghost counter* (the exact thing pruning
   was meant to remove); or, if the pruned peer has meanwhile bumped its own
   counter, `Compare` returns `Concurrent` → a **spurious conflict copy** (the
   exact FM-3 the decision claims to defeat).
3. **The ack-gate cannot close when it fires.** You `DropCounter(D)` *because* D was
   removed; D is gone and cannot acknowledge. For a strict 2-device tool, removing
   the only peer leaves nobody to be symmetric with (so "both live peers applied it"
   is vacuous); for the device-*replacement* lifecycle the decision explicitly
   exists to handle, there is transiently a third identity (ghost D + self + new E),
   which is N6 "out of scope" — so the only configuration that exercises the
   mechanism is one the project does not support, yet `DropCounter` ships "from day
   one."

The literature's actual fix for comparing unequally-pruned vectors — **virtual
pruning** — is named in the evidence the decision cites but is **not adopted**.

## Evidence

- `docs/audit/decisions/protocol/vv-pruning-counter-cleanup.md` Option A: ack-gated,
  "the drop is only considered complete once **both** live peers have applied it (so
  vectors stay *equally* pruned — defeats FM-3)" (lines 47-49); proposed signal "a
  `CLOSE`-with-reason or, more cleanly, the next `INDEX` simply no longer contains
  D's counter" (lines 46-48); Decision (lines 78-84) ships it "from day one."
- No message type carries a drop: `docs/audit/decisions/protocol/message-type-enumeration.md`
  freezes the catalogue at 7 and reserves `0x08+` (lines 39-49, 104-110); `CLOSE`
  is "graceful shutdown" only (line 48).
- `Compare` treats a missing counter as `0`:
  `docs/audit/findings/protocol/PR-2-version-vector-comparison.md` §2 (lines 52-56);
  `docs/audit/findings/literature/version-vectors.md` §4.3 (lines 263-266) — "a
  present `Value>0` on one side and absent on the other makes that side strictly
  greater in that slot." This is the same rule that makes a tombstone dominate a
  stale peer (PR-4 §5) — it cuts the other way for a *deliberately shrunk* vector.
- FM-3 and its real fix: "two nodes ... exchanging version vectors [that] have not
  been equally pruned ... may lead to a false reporting of conflicts. **Virtual
  pruning** can be used to address the unequally pruned vector problem"
  (`version-vectors.md` lines 458-467; LinkedIn *Version Vector(III)*,
  https://www.linkedin.com/pulse/version-vectoriii-pratik-pandey, accessed
  2026-06-28). The design implements neither virtual pruning nor a coordinated drop.
- The decision's own motivation is device *replacement* (vv-pruning lines 64-68,
  Option B: "the moment a device is ever replaced"), i.e. the 3-identity transition
  that N6 scopes out (`docs/audit/findings/synthesis/problem-space-map.md` §2.2 N6).
- Marquee evidence the decision is reacting to: Syncthing #10590, 8,591 conflicts on
  device rebuild (https://github.com/syncthing/syncthing/issues/10590, accessed
  2026-06-28).
- **Worked sequence.** Operator removes D. `self` prunes → `{self:5}`. Surviving
  peer E is briefly offline, still `{self:5, D:4}`. On reconnect, `E.Compare(self)`
  → self is **DominatedBy** E (D-slot `0 < 4`) → E re-pushes the D-bearing
  `FileInfo` → self re-`Merge`s `D:4` back ⇒ ghost resurrected. If self had bumped
  to `{self:6}` meanwhile → `Compare` = `Concurrent` ⇒ spurious conflict copy.

## Impact

The mechanism added specifically to avoid Syncthing #10590 does not prevent it and
can itself **resurrect the ghost counter or trigger a conflict storm** on device
replacement — a false sense of security written into a "decided" file. The common
case (2 devices, never re-paired) is genuinely unaffected, which is why this is
medium and not high; but the one lifecycle the decision exists to handle is unsound,
and "defeats FM-3 / kills FM-1" is overclaimed.

## Recommended-change

Reopen the decision; pruning must be a *coordinated* operation, not an implicit
shrink. Pick one, with a logged decision:

1. **Virtual pruning** — carry a per-vector low-water / "known-removed set" so a
   pruned counter compares as *known-removed*, not as `0`. This is the literature's
   named fix and the only one that makes shrunk-vs-unshrunk comparison safe.
2. **Explicit acked `DROP_COUNTER{shortID}` control message** as a reserved `0x08+`
   type behind `featureFlags` (the catalogue already supports this). Both peers
   apply it atomically; until both ack, retain the counter and never let a shrunk
   vector be compared against an unshrunk one.
3. **Scope `DropCounter` to "un-pair the last peer."** For strict v1, only drop a
   counter when the operator removes the *final* paired device (no live peer remains
   to be unequal with); explicitly defer multi-device counter cleanup, citing
   #10590, instead of claiming it is solved. This matches the actual 2-device scope
   and is sound because there is no peer left to disagree.

In all cases, correct the decision's claim: the current "shrunk INDEX" signal does
**not** defeat FM-3 — it is a textbook trigger for it.
