---
id: tree-critic-2
title: Periodic / clean-shutdown snapshot persistence is not crash-safe, reopening the version-vector counter-rollback data-loss hole the snapshot was supposed to close
severity: high
status: rejected
phase: 3
role: tree-critic
area: persistence of last-synced state across restarts
date: 2026-06-28
---

# tree-critic-2 — A stale (crash-survived) snapshot silently rolls back version-vector counters → data loss, and fabricates false-authorship tombstones

## Claim

The persisted last-synced snapshot is the single mechanism three separate decisions
lean on: deletion-across-restart (MK-6 / R-5), version-vector **anti-rollback**
(vv-counter-seeding Option A guard 1), and "don't forget a not-yet-acked tombstone"
(PR-4 §7, tombstone-retention). But the snapshot is specified to be written **"on
clean shutdown (and/or periodically)"** — i.e. *not* synchronously with each VV
bump. A **crash** (the exact failure model the entire SR-1/SR-2 atomic-write edifice
exists for) therefore leaves a **stale** snapshot whose version vectors are behind
the counters the daemon already minted and *already broadcast to the peer before
crashing*. On restart the device reloads those stale counters and a subsequent local
edit re-issues an already-used counter value — the FM-4 counter-rollback trap that
vv-counter-seeding Option A claims to have closed. None of Option A's three guards
catch it, because the stale-snapshot case is neither "no snapshot" (so reseed does
not fire) nor "equal-VV-differing-content" (so the backstop does not fire). The
result is **silent data loss**, plus a sibling failure where the restart diff
fabricates a *false-authorship* tombstone.

## Evidence

- **Persistence is explicitly not per-bump.** MK-6 step 1: "On clean shutdown
  (and/or periodically), persist a **local-only** snapshot"
  (`docs/audit/findings/merkle/MK-6-persisted-snapshot-restart-deletion.md:52-54`).
  vv-counter-seeding guard 1 phrases the guarantee as covering only "a **normal
  daemon restart**": "Persist VVs in the last-synced tree snapshot … and restore on
  startup ⇒ a normal daemon restart never rolls back"
  (`docs/audit/decisions/protocol/vv-counter-seeding.md:53-56,98-99`). A crash is
  not a normal restart, and "periodically" means the snapshot lags the live tree by
  up to one snapshot interval.

- **The project's own failure model is crash-during-operation.** SR-1/SR-2 are built
  around "a crash mid-transfer," "kill the transfer mid-stream," power-loss 0-byte
  files (`docs/audit/rules/sync-rules.md:14-46`). A daemon that must survive a crash
  mid-transfer can equally crash between periodic snapshots; the snapshot must meet
  the same bar as the data it guards, and the design does not require that.

- **Worked rollback (silent data loss).** Reusing the FM-4 frame that
  vv-counter-seeding itself uses (`vv-counter-seeding.md:31-48`):
  1. A and B are synced; file `f` has VV `{A:5}` on both. Last snapshot on A
     recorded `{A:5}`.
  2. A edits `f` → `{A:6}` (broadcast to B), edits again → `{A:7}` (broadcast). B now
     holds `{A:7}`. **No periodic snapshot has fired**, so A's on-disk snapshot still
     says `{A:5}`.
  3. A **crashes** and restarts. It loads the stale snapshot `{A:5}`.
  4. A edits `f` (new content) → `Bump` = `prev+1` = `{A:6}` — a counter value already
     spent on different bytes.
  5. `Compare({A:6}, {A:7})` → **DominatedBy** (6 < 7). A's genuinely-new
     post-restart edit looks *stale*. B pushes `{A:7}` back; A applies it, overwriting
     A's new content. **Silent data loss** — the conflict path never fires (it looks
     causal, not concurrent), exactly the FM-4 trap.

- **All three Option A guards miss it.** (`vv-counter-seeding.md:53-74`) Guard 2
  (cold-start reseed via `Merge` before authorship) is gated on **"if no snapshot is
  found"** — here a snapshot *is* found, just stale, so reseed never runs. Guard 3
  (equal-VV-differing-content ⇒ conflict) requires the VVs to be **equal**; here they
  are `{A:6}` vs `{A:7}`, a clean `DominatedBy`, so the backstop never trips. Guard 1
  (persistence) is the one that is supposed to prevent this and is precisely the one
  that is too weak as specified.

- **It also undermines a cited mitigation in PR-4.** PR-4 §5.1 lists "Counter
  rollback (FM-4): a wiped peer re-authoring with a low counter could make a
  tombstone fail to dominate; mitigated by the persisted-snapshot + cold-start
  reseed" (`docs/audit/findings/protocol/PR-4-deletions-tombstones-resurrection.md:95-96`).
  A *stale* snapshot is the un-mitigated middle ground: not wiped (reseed off), but
  rolled back — so a post-crash tombstone can also fail to dominate and resurrect.

- **Sibling failure: false-authorship tombstones.** MK-6 step 2a: "present-in-
  snapshot, absent-on-disk ⇒ a deletion that happened while down ⇒ synthesize a
  tombstone (**bump VV**)"
  (`MK-6-persisted-snapshot-restart-deletion.md:57-60`). If the file was actually
  deleted by the **peer** (A applied B's tombstone to disk) but A crashed before
  persisting that into the snapshot, the stale snapshot still shows the file as a
  *live* pre-delete entry. On restart A sees snapshot-present/disk-absent and
  synthesizes a **new tombstone stamped with A's own bumped counter** for a deletion
  A never authored. That VV can be `Concurrent` with B's original tombstone VV →
  spurious conflict-on-a-deletion and a corrupted causal chain, rather than A simply
  re-learning B's tombstone on reconnect.

- **The "missing/corrupt snapshot" guard does not cover "stale."** MK-6 step 3 only
  handles the *missing/corrupt* case conservatively
  (`MK-6-persisted-snapshot-restart-deletion.md:63-66`); a syntactically-valid but
  stale snapshot passes every check and is trusted fully.

## Impact

- **Silent data loss (high):** a local edit made after a crash-restart can be
  overwritten by the peer's older-but-higher-counter copy, with no conflict copy
  created (the loss is invisible — the prime-directive violation the whole VV scheme
  exists to prevent).
- **Tombstone resurrection (high):** a post-crash rolled-back counter can stop a
  tombstone dominating, resurrecting a deleted file (SR-10 hole).
- **Spurious deletion conflicts / causal corruption (medium):** false-authorship
  tombstones from a stale snapshot inject concurrent delete events.
- Exposure is **every crash**, not a rare race: any non-clean exit between periodic
  snapshots, and the window is as large as the snapshot interval × edit rate.

## Recommended change (beats the status quo)

The fix is to make the *durability of the VV high-water mark* match the durability
the rest of the engine already guarantees:

1. **Persist the per-device counter high-water mark synchronously, before it leaves
   the machine.** A `Bump` that will be broadcast must be durable *no later than* the
   outbound `INDEX_UPDATE`. Cheapest correct form: an append-only, fsync'd
   monotonic counter log (or a tiny "max counter per (path,device)" record) written
   with the SR-1/SR-2 temp→fsync→rename discipline *before* the broadcast — not the
   whole-tree gob snapshot, just the high-water mark. This guarantees `prev` on
   restart is ≥ any counter ever sent, so `prev+1` can never collide. (This is the
   monotonic-counter half of what the hybrid `max(prev+1, now)` clock bought for
   free; Option B in vv-counter-seeding remains the documented fallback if a durable
   counter proves too costly — `vv-counter-seeding.md:76-89`.)
2. **Treat a stale snapshot like an untrusted cache, not ground truth.** On startup,
   detect staleness (e.g. snapshot generation/epoch < the durable counter log) and,
   for any path where the durable high-water mark exceeds the snapshot's VV, enter
   the **reseed/Merge path** (guard 2) rather than trusting the snapshot — i.e. make
   guard 2's trigger "snapshot missing **or** provably behind," not "snapshot
   missing."
3. **Distinguish locally-authored from remotely-applied deletions in the snapshot
   diff.** Persist enough (the applied tombstone's origin VV) that step-2a does not
   re-stamp a peer-authored deletion with local authorship; on restart, an
   absent-on-disk path whose last known state was a *remote* tombstone is re-derived
   from the peer, not re-authored locally.
4. **Add the crash case to the acceptance suite.** Extend MK-6's
   deletion-across-restart test with a **kill -9 between snapshots** variant:
   edit-edit-broadcast, hard-kill, restart, edit again — assert the post-restart edit
   is preserved (never silently overwritten) and that a pending remote tombstone is
   not turned into a local-authored concurrent tombstone.

This is the load-bearing dependency vv-counter-seeding already flagged: "**Hard
dependency on OQ-5/R-5** … If that snapshot is not delivered, the rollback guarantee
weakens … flagged for the Phase 3 protocol-critic and concurrency-critic"
(`vv-counter-seeding.md:126-129`). This finding sharpens it: the snapshot as
*specified* (periodic/clean-shutdown) does not deliver the guarantee even when it
*is* present.
