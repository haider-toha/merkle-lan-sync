---
id: protocol-critic-4
title: Tombstone resurrection via author-wipe before propagation — cold-start reseed (the pure prev+1 design) actively re-creates a deleted file, and PR-4's "persisted snapshot + reseed mitigates the wipe" claim is false for deletions
severity: high
status: rejected
area: tombstone resurrection by a stale peer
---

# protocol-critic-4 — A delete authored but not yet replicated is lost if the author is wiped, and the reseed logic resurrects it; the resurrection-defense enumeration omits this vector and misclaims mitigation

## Claim

The resurrection defense (SR-10 / PR-4 §5.1) enumerates three failure modes —
premature GC, ghost counters, counter rollback — and claims each is mitigated. It
omits a direct fourth: **the device that authored a deletion is wiped (no snapshot
survives) while the peer is partitioned.** In that case the *only* record of the
delete is destroyed, and the chosen **pure `prev+1` cold-start reseed** treats the
peer's still-present file as a fresh remote create → the file is **resurrected and
the deletion silently lost**.

Critically, the design's stated mitigations do not apply to deletions:

- PR-4 §5.1 says the wipe/rollback case is "mitigated by the persisted-snapshot +
  cold-start reseed." That is true for a *content edit* (reseed merges the peer's VV
  and re-asserts authorship), but **false for a deletion**: reseed has no record that
  the path was deleted, so it re-creates it.
- The "equal-VV-but-differing-content ⇒ conflict" backstop cannot fire — it requires
  the wiped device to *hold* a version with an equal VV; after a wipe it holds
  **nothing** for that path.
- The documented fallback (the hybrid `max(prev+1, now)` clock) does not help either:
  the problem is not a rolled-back counter, it is the **total absence of any delete
  record** on both peers.

## Evidence

- PR-4 §5.1 failure-mode list — premature GC, ghost counters (#10590), counter
  rollback "mitigated by the persisted-snapshot + cold-start reseed
  (`vv-counter-seeding.md`)" — **no** "deleter wiped before propagation" entry
  (`docs/audit/findings/protocol/PR-4-deletions-tombstones-resurrection.md`
  lines 84-96).
- Cold-start reseed re-creates the file:
  `docs/audit/decisions/protocol/vv-counter-seeding.md` guard 2 — reseed "`Merge`s the
  peer's VV for every shared path before asserting any local authorship; a local file
  whose `content_hash` differs from the merged-VV version is then bumped" (lines 56-62,
  98-102). A path the wiped device no longer has is either **not "shared"** (→ normal
  diff treats a peer-only path as a candidate to FETCH — `.claude/skills/merkle-sync/SKILL.md`
  §2 diff, lines 104-106) or merged with no local content (→ FETCH). Either way the
  deleted bytes are pulled back.
- The backstop needs an equal VV to compare: guard 3 "equal-VV-but-differing-content
  ⇒ conflict" (`vv-counter-seeding.md` lines 63-66) — inapplicable when the wiped
  device has no entry at all.
- Snapshot covers **restart, not wipe**: `tombstone-retention-gc.md` §7 — "the
  persisted snapshot must store tombstones so a **daemon restart** does not forget a
  not-yet-acked deletion" (lines 113-116); OQ-5/R-5 persist a "last-synced tree
  snapshot ... load on startup"
  (`docs/audit/findings/synthesis/problem-space-map.md` lines 217, 239). A wipe by
  definition destroys the snapshot.
- The surviving peer also has no tombstone (it was offline during the delete:
  PR-4 §5 partition scenario, lines 68-82), so after the wipe **neither** peer holds
  any delete record — resurrection is guaranteed regardless of clock scheme.
- **Internal inconsistency:** the VV design treats a "true wipe / first run" as
  in-scope and builds guard 2 *specifically* for it (`vv-counter-seeding.md`
  lines 56-62), yet the tombstone design implicitly treats wipe as out-of-scope. A
  wipe (disk failure + reinstall) is a normal multi-year laptop lifecycle and must be
  handled consistently. STEERING §C.1 already flags the reseed guarantee as
  conditional ("If that re-seed guarantee can't be made to hold, adopt the hybrid
  floor") — this is a class where reseed *cannot* hold **and** the hybrid floor is
  also no help, so the steering fallback is itself insufficient for deletions.
- **Worked sequence.** `F` exists `{M:5, P:3}` on both. P goes offline. M deletes F →
  tombstone `{M:6, P:3}` (cannot propagate). **M's disk fails; reinstall — no
  snapshot.** M rescans: F is absent on disk and absent from any snapshot, so M has
  *no* entry for F. P returns holding `F {M:5, P:3}`. Diff: P has F, M has nothing →
  candidate to FETCH; reseed has no tombstone to make F dominated → M fetches F. **F
  is resurrected on M and stays on P; the delete is permanently undone, with no
  conflict marker.**
- Real-world class: deletions reappearing when a long-inactive / re-added device
  reconnects (https://forum.syncthing.net/t/syncthing-constantly-reinstating-old-deleted-files/18026,
  accessed 2026-06-28); ghost-counter resurrection #10590
  (https://github.com/syncthing/syncthing/issues/10590, accessed 2026-06-28).

## Impact

The marquee bug the entire tombstone design exists to prevent — a deleted file comes
back — occurs on a realistic lifecycle (laptop A dies / is reinstalled while laptop B
is asleep or travelling with a pre-delete copy; on reunion B's copy resurrects
everywhere). It is **silent inverse-data-loss with no conflict marker**, and the
design currently asserts it is mitigated. The conjunction (wipe + concurrent
partition + a delete pending replication) bounds the *likelihood*, but the
false-mitigation claim in PR-4 §5.1 is **unconditionally wrong** and will mislead the
planner/implementer into believing the case is covered.

## Recommended-change

1. **Correct PR-4 §5.1** first: the persisted-snapshot + reseed does **not** mitigate
   wipe-loss of a tombstone; for deletions it *converts* the wipe into a resurrection.
   Add this as an explicit, named failure mode under SR-10.
2. **Make a reseeding (snapshot-less) device conservative.** During reseed it must
   not treat "peer has a path I lack" as an authoritative remote create. Because it
   genuinely cannot distinguish *delete-then-wipe* from *never-received*, the safe
   degrade is to surface such a path as a **conflict copy / quarantined adoption**
   rather than a silent live file — this bounds blast radius to "an extra file
   appears" instead of "a delete is silently undone." (Note: this still does not
   *recover* the lost delete; it only stops silent propagation.)
3. **Acknowledge the architectural limit honestly:** a deletion not yet acknowledged
   by the peer is **not durable against author-wipe**. Either accept and *document*
   it as a known v1 limitation (and minimise the window by propagating deletes
   eagerly, ack-gating retention as already designed), or — out of v1 scope, flag for
   the planner — journal/replicate a delete before removing the file's bytes.
4. **Test obligation:** a scripted `delete-on-A → partition-B → wipe-A (drop
   snapshot) → reconnect` scenario asserting the file is **not** silently resurrected
   as a clean live file (it must be deleted, conflicted, or quarantined — never
   silently re-adopted). This is the negative test the current SR-10 suite omits.
