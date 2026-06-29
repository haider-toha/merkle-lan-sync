# Decision: MK-6 deletion-across-restart — fix recreate-over-tombstone VV + prove the end-to-end restart path

- Area: phase7 / reconcile (startup restore) + integration suite
- Status: decided
- Date: 2026-06-29
- Decider: Phase 7 fix agent (round 1), open item MK-6 (fixed-claim-refuted)
- Reads-first honoured: `plan/README.md`, `plan/agent_roster.md`,
  `docs/audit/findings/merkle/MK-6-persisted-snapshot-restart-deletion.md`,
  `docs/audit/findings/review/MK-6.md`,
  `docs/audit/findings/review/votes/MK-6-skeptic1.md`,
  `docs/audit/findings/review/MK-6.skeptic-3.vote.md`,
  `docs/audit/decisions/ws1/snapshot-and-deletion-synthesis.md`,
  `docs/audit/decisions/ws4/watcher-debounce-rescan-and-startup.md`
- Consumes: MK-6 / R-5 (persisted snapshot ⇒ detect deletions across a restart),
  SR-6 (bump only on confirmed local authorship), SR-7 (no data loss on conflict),
  SR-9/SR-10 (versioned tombstones, anti-resurrection), CDD-3 (initial scan is not
  authorship), CDD-7.1 (carry a peer tombstone forward unchanged), GR-5 (single
  writer behind the RWMutex; pure helpers off the lock).

## Context

Two Phase 6 skeptics REFUTED the FIXED verdict on MK-6
(`review/votes/MK-6-skeptic1.md`, `review/MK-6.skeptic-3.vote.md`). MK-6 is the
highest-severity merkle-lane risk (R-5, "the least-mitigated risk" — resurrection /
divergence across a restart). The refutation has three load-bearing points:

1. **The finding's own named acceptance test does not exist** (both skeptics).
   `MK-6-…md:81-84` mandates an end-to-end two-node scenario: create+sync on A and B,
   stop A, delete on A's disk, **restart A**, assert A synthesizes a tombstone *from
   the snapshot diff*, B removes its copy, no resurrection. The integration suite
   (`test/integration/sync_test.go`) has convergence / conflict / deletion / rename /
   killed-transfer / large, but **no restart-boundary case** — `TestDeletion_NoResurrection`
   deletes the file while A's daemon is *alive* (`:105`), exercising the live-rescan
   path, NOT the MK-6 startup-snapshot-diff path. A data-loss-class finding asserted
   FIXED while its named acceptance scenario is unexecuted is the exact "fixed-but-
   unproven" pattern the skeptic vote exists to catch.

2. **recreate-over-tombstone across a restart is an unhandled data-loss bug**
   (skeptic #1 §2). `restoreVVs` (`engine.go:233-255`) skips any snapshot entry with
   `p.Deleted` (`:245` `if !ok || p.Deleted { continue }`). So a path whose snapshot
   entry is a TOMBSTONE but which exists on disk again (the file was recreated while
   the daemon was down) keeps the **empty** VV the fresh scan seeds. A peer that still
   holds the tombstone carries a **non-empty** VV. On reconnect the peer's tombstone
   VV `Dominates` the empty VV of the local recreate ⇒ `resolve` returns
   `planInstall(tombstone)` ⇒ the legitimate recreate is **re-deleted** (the local
   create is lost; the deletion is resurrected). Reproduced by trace:
   `restoreVVs` leaves recreate VV `nil`; `resolve(local=recreate{VV:nil},
   remote=peerTombstone{VV:{A:1}})` → `Compare(nil,{A:1})=DominatedBy` →
   `planInstall(remote tombstone)` → file removed. This is precisely the data-loss
   case MK-6 was raised to prevent, and it is the divergence between the restart path
   and the two LIVE recreate paths, which both bump correctly:
   `onLocalChange` (`engine.go:892` `nfi.Version = prev.Version.Bump(self)`) and
   `rescan` (`engine.go:931` `s.Version = prev.Version.Bump(self)`) — where `prev` is
   the tombstone. Only the restart path drops the tombstone VV.

3. **delete-while-down vs a concurrent remote edit is untested** (skeptic #3 §3).
   If A deletes a file while down and peer B *edits* the same file concurrently, A's
   synthesized tombstone (`prevVV.Bump(A)`) is **Concurrent** with B's edited VV — a
   delete-vs-edit conflict, not a clean delete-wins. The skeptic notes the outcome is
   "neither tested nor obviously handled."

Disposition of the three points:
- (1) and (2) are genuine gaps — (2) is a real correctness/data-loss bug in
  `restoreVVs`; (1) is a missing acceptance test for a high-severity finding.
- (3): tracing the existing resolver shows the behavior is **already correct and
  data-loss-free** (see Rationale): the concurrent delete-vs-edit resolves to
  modify-beats-delete (Syncthing-style), and on either mtime ordering B's edited
  bytes survive on BOTH peers (as the live file when the edit wins, or as a
  `.sync-conflict` copy when the stale-mtime delete wins, because the holder of the
  bytes mints the copy). It is untested, not unhandled. The fix is to PIN this with a
  test, not to change code.

## Options (scored 1-5 on correctness / concurrency-safety / testability / cross-platform)

The consequential code choice is HOW to fix the recreate-over-tombstone VV bug (2).
(1) and (3) are test additions regardless of which option is chosen.

### Option A — Bump the tombstone's VV in `restoreVVs` when the path is present on disk (CHOSEN)
Split the `!ok || p.Deleted` guard: a brand-new path (`!ok`) keeps the empty VV
(CDD-3); a path whose snapshot entry is a tombstone but which is present on disk again
sets `out[i].Version = p.Version.Bump(self)` — the recreate DOMINATES the prior delete,
identical to the two live recreate paths. `SynthesizeDeletions` then keeps this `cur`
entry (path present ⇒ tombstone dropped), so the bumped VV reaches `e.files`.
- Correctness **5**: the restart recreate now carries the same dominating VV a live
  recreate would; a peer holding the tombstone adopts the file instead of re-deleting
  it. No data loss; mirrors the established live semantics exactly.
- Concurrency **5**: `restoreVVs` is a PURE function called only on the single-goroutine
  `New`/`startupReconcile` path (no lock, no I/O) — no new concurrency surface (GR-5).
- Testability **5**: pure-function unit test (snapshot tombstone + present-on-disk ⇒
  bumped, dominating VV) PLUS an end-to-end restart-with-recreate integration test.
- Cross-platform **5**: VV/path-agnostic; the integration test uses canonical
  forward-slash paths and runs on every CI OS.
- **Chosen.**

### Option B — Move the recreate-VV bump into `SynthesizeDeletions` (merkle layer)
Keep `restoreVVs` skipping tombstones; in `SynthesizeDeletions`, when a `cur` path
matches a `prev` tombstone, bump `cur`'s VV from the tombstone.
- Correctness **5** (same end state).
- Concurrency **5** (also pure).
- Testability **4**: works, but overloads `SynthesizeDeletions` (whose single job is
  "synthesize tombstones for ABSENT paths") with VV restoration for PRESENT paths —
  the exact responsibility `restoreVVs` already owns. Splits VV-restore logic across
  two functions and two packages, lowering cohesion and discoverability.
- Cross-platform **5**.
- **Rejected** — wrong layer; `restoreVVs` is the named owner of "re-attach persisted
  VVs to the fresh scan," and a recreate-over-tombstone is exactly that case.

### Option C — Seed the snapshot tombstones into `e.files`, then let the first rescan detect the recreate and bump
Install the tombstones at startup; rely on the async periodic rescan to notice the
on-disk file and bump via the existing `rescan` path.
- Correctness **3**: there is a startup WINDOW where `e.files` holds the recreate with
  an empty VV (or the bare tombstone) and a peer connects before the rescan fires —
  the engine could advertise/resolve the empty-VV recreate and lose to the peer's
  tombstone, the very bug, now made timing-dependent.
- Concurrency **3**: depends on rescan-vs-peer-connect ordering on the live loop.
- Testability **2**: non-deterministic; hard to assert without sleeps.
- Cross-platform **4**.
- **Rejected** — reintroduces the race the synchronous `startupReconcile` (run inside
  `New`, before peers/watcher start) was designed to avoid.

### Option D — Add only tests; leave `restoreVVs` as-is
Argue the recreate-over-tombstone case is rare/acceptable.
- Correctness **1**: leaves a real data-loss bug; a recreated file silently re-deleted.
- Rejected outright — the finding is HIGH severity precisely about resurrection/loss.

## Decision

Adopt **Option A**, plus the two test additions.

Code (`internal/reconcile/engine.go`, `restoreVVs`):
- Replace `if !ok || p.Deleted { continue }` with: `if !ok { continue }` (brand-new ⇒
  empty VV, CDD-3) followed by an explicit `if p.Deleted { out[i].Version =
  p.Version.Bump(self); continue }` (recreate-over-tombstone ⇒ bump so it dominates),
  leaving the existing unchanged-keeps / changed-bumps branch intact. Update the
  function comment (it currently DOCUMENTS the bug: "A reappeared path over a prior
  tombstone is a new create (empty VV)").

Tests:
- `internal/reconcile/reconcile_test.go`: extend `TestRestoreVVs` (or add a focused
  case) — a snapshot tombstone whose path is present on disk yields a VV that
  `Dominates` the tombstone's VV (proves the recreate beats a stale peer tombstone).
- `test/integration/sync_test.go`: add the harness to stop + restart a node reusing
  the SAME folder, identity, and snapshot path, and three scenarios:
  - `TestRestart_SynthesizesDeletionFromSnapshot` — MK-6's named acceptance test:
    stop A, delete on A's disk while down, restart A ⇒ B removes its copy, no
    resurrection on A (the tombstone came from the snapshot diff, since A's daemon
    never observed the delete live).
  - `TestRestart_RecreateOverTombstoneSurvives` — file deleted (both hold a
    tombstone), A stopped, file recreated on A's disk while down, restart A ⇒ the
    recreate is present on BOTH peers (would be re-deleted under the bug).
  - `TestRestart_DeleteWhileDownVsRemoteEdit` — A deletes while down, B edits
    concurrently, restart A ⇒ B's edited bytes survive on BOTH peers (no data loss),
    asserted robustly (bytes present as the live file OR as a `.sync-conflict` copy),
    so the assertion is immune to the mtime/ShortID conflict-winner nondeterminism.

## Rationale

- Option A fixes the bug at the named layer and makes the three VV-restore paths
  (live `onLocalChange`, live `rescan`, restart `restoreVVs`) treat a recreate-over-
  tombstone IDENTICALLY: bump the tombstone's VV so the new create is causally newer
  than the delete it supersedes. That is the only way an empty-VV scan result can win
  against a peer that still remembers the deletion.
- Point (3) needs no code change. Trace of the existing resolver for a delete-while-
  down (A: tombstone `{A:2}`, mtime = pre-delete) vs a concurrent edit (B: live
  `{A:1,B:1}`, mtime = edit time), `Compare = Concurrent`, content differs ⇒
  `conflictPlan`:
  - Edit newer (the normal case): `winner = edit`, the losing side is a tombstone ⇒
    no copy (a delete has no bytes); the file is resurrected with B's edit on both,
    merged VV `{A:2,B:1}`. B's bytes preserved as the live file; the delete intent
    (carrying no data) loses. This is modify-beats-delete, matching Syncthing and the
    SR-7 "never lose DATA" intent.
  - Tombstone newer (stale-mtime / clock-skew): `winner = tombstone`, `loser = edit`
    (has bytes). On B `hasLocalContent(edit)` is true ⇒ B mints the `.sync-conflict`
    copy from its own bytes and advertises it, then applies the tombstone; A fetches
    that copy. B's bytes preserved as the conflict copy on both. No data lost either
    way. The test asserts the invariant (B's bytes survive on both) rather than the
    winner, so it is deterministic despite random ShortIDs.
- `startupReconcile` runs inside `New` before peers/watcher start, so the fix lands
  before any inbound index — no startup race (skeptic #3 acknowledged this is "good").

## Consequences

- Touches `internal/reconcile/engine.go` (`restoreVVs`, ~4 lines + comment) only for
  the code fix — additive, no signature change, no new lock/channel/goroutine.
- Touches `test/integration/helpers.go` (a `stop` helper, a configurable
  identity/snapshot path so a node can be restarted, exposed `cancel`/`snapPath` on
  `node`) and `test/integration/sync_test.go` (+3 restart scenarios), and
  `internal/reconcile/reconcile_test.go` (+ recreate-over-tombstone VV case). The
  helper change is structured so existing scenarios compile and behave unchanged
  (transport now tied to the node's own child context so `stop` tears the node down
  cleanly; cleanup still cancels + waits exactly as before).
- The MK-6 acceptance scenario is now executed end-to-end on every CI OS
  (ubuntu/macos/windows), closing both refutation points (1) and (2); point (3) is
  pinned by a data-loss assertion.
- `internal/merkle` `SynthesizeDeletions` and its unit tests are unchanged — the bump
  for a recreate now happens in `restoreVVs` (which runs first in `startupReconcile`),
  and `SynthesizeDeletions` still correctly drops a tombstone for a present path.
- Cross-refs: MK-6 / R-5; SR-6/SR-7/SR-9/SR-10; CDD-3/CDD-7.1; GR-5;
  `decisions/ws1/snapshot-and-deletion-synthesis.md`,
  `decisions/ws4/watcher-debounce-rescan-and-startup.md`.
