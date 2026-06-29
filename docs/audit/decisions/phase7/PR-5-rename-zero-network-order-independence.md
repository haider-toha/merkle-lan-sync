# Decision: PR-5 — make the rename "zero network transfer" claim actually hold on the real cross-peer apply path (order-independent), and test it there

- Area: phase7 / reconcile (engine reconcileWithPeer + puller coupling)
- Status: decided
- Date: 2026-06-29
- Decider: Phase 7 fix agent (round 1), open item PR-5 (fixed-claim-refuted)
- Reads-first honoured: `plan/README.md`, `plan/agent_roster.md`,
  `docs/audit/findings/protocol/PR-5-rename-handling.md`,
  `docs/audit/findings/review/PR-5.md` (FIXED verdict under challenge),
  `docs/audit/findings/review/PR-5-skeptic1.md` (REFUTED),
  `docs/audit/findings/review/votes/PR-5-skeptic2.md` (REFUTED),
  `docs/audit/decisions/phase7/PR-3-conflict-no-data-loss-ordering.md` (the
  preserve→winner coupling machinery this fix reuses),
  `docs/audit/decisions/ws4/tombstone-lifecycle-rename-and-no-clobber.md`.
- Consumes: SR-6 (broadcast only on confirmed local authorship — a received file
  produces ZERO outbound), SR-8 (an apply is not authorship), PR-6 (no sync loop),
  SR-7 (no data loss — coupling precedent), CDD-1 (loop never blocks; transfer I/O off
  the loop; stop-and-wait pull), GR-5 (single writer, zero I/O under the lock),
  MK-4 (content-addressed local reuse).

## Context

Both Phase 6 skeptics REFUTED the FIXED verdict on PR-5. The disputed claim is the
*entire value proposition* of PR-5 over a naive delete+create: that a rename whose old
bytes are still on disk costs **ZERO network transfer** because the new path reuses the
old file's bytes via content-addressed `localSource` (`transfer.go:197-218`).

Correctness / no-data-loss on a rename is NOT in dispute and is proven across two live
engines (`test/integration/sync_test.go` `TestRename_PropagatesNoLoss`). What is refuted
is the efficiency claim on the **receiver's real apply path**:

1. `orderCreatesBeforeDeletes` (`broadcast.go:42-49`) orders only the SENDER's wire
   batch. The receiver's `onIndexUpdate` (`engine.go:633-643`) drops the whole delta
   into the unordered `ps.index` map and calls `reconcileWithPeer` ONCE; that rebuilds
   whole trees and runs `merkle.Diff`, which emits in **path-sorted** order
   (`differ.go:119-134`, `unionChildren` sorts). The wire ordering is discarded at apply
   time.
2. In the resulting per-path execution (`engine.go execute`): a create →
   `enqueueFetch` (queued to the ASYNC per-peer puller); a tombstone →
   `applyTombstone` → **synchronous `os.Remove` on the engine loop**
   (`engine.go:849-862`).
3. So when the puller later runs `materialise(new)` → `localSource(hash,new)`, the only
   local holder of those bytes — the old file — has already been (a) recorded as a
   tombstone (`localSource` skips `fi.Deleted`) AND (b) `os.Remove`d from disk
   (re-hash fails). `localSource` returns false → `fetchOverWire` issues a network
   `REQUEST`.

Consequence (skeptic #2, high confidence): the zero-network optimisation **fails
deterministically** for any rename whose new path sorts AFTER the old (`a.txt`→`z.txt`:
the diff yields `delete(a.txt)` first, removed synchronously, before the puller fetches
`z.txt`), and is a **timing race** when the new path sorts before the old. The only test
that asserted `MsgRequest == 0` (`reconcile_test.go:795` `TestRename_NoNetworkTransfer`)
calls `materialise` directly while keeping the old file alive — it never applies the
tombstone, so it bypasses the exact interaction that breaks the optimisation. No test
exercises the realistic receiver path.

The bug is **efficiency, not data loss** — the network fallback still converges (the
source peer retains the bytes). That is precisely why "fixed" was over-claimed: the only
thing distinguishing PR-5 from naive delete+create is the zero-network win, and it is not
delivered on the path that matters nor tested there.

## Options (≥3, scored on correctness / concurrency-safety / testability / cross-platform)

### Option A — Couple the rename in `reconcileWithPeer`: materialise the new path (reusing the still-present old bytes) FIRST, then apply the old tombstone (deferred `os.Remove`). CHOSEN.

Detect a rename pair in the diff: a live-install (create) whose `content_hash` equals a
tombstone-install whose OLD on-disk bytes are still local (the receiver's current live
leaf at the old path, `d.Local`). Enqueue ONE coupled puller task — reusing the proven
PR-3 `preserve`→`applyTomb` machinery — where the puller materialises the new create
(which finds the old file still live on disk via `localSource`, copies locally, zero
network) and ONLY if that copy lands does the loop apply the old tombstone (the
destructive `os.Remove` is deferred to the `applyTomb` completion, i.e. AFTER the copy).
A new `preserveAdvertise=false` flag keeps the rename create from being re-broadcast
(SR-6/SR-8/PR-6 — it is a received file).

- Correctness: **high.** Order-independent: the create's local reuse always precedes the
  old's removal because both ride ONE sequential puller task. Does NOT change the SET of
  operations (still create-new + delete-old) — only their order and the create's byte
  source — so even a content-hash *false* match (two unrelated identical files) produces
  the exact same converged end-state, never data loss (unlike a MOVE-suppresses-tombstone
  scheme).
- Concurrency-safety: **high.** Reuses the already-race-tested coupling; transfer I/O
  stays OFF the loop (CDD-1); the deferred remove runs on the loop AFTER the copy
  completion (no new lock-held I/O, GR-5). `inflight` on BOTH paths closes the
  re-entrancy window the deferral opens (a second reconcile during the in-flight task
  skips the still-live old tombstone).
- Testability: **high.** Drives the REAL receiver path (`onIndexUpdate` →
  `reconcileWithPeer` → `merkle.Diff` → pairing → puller → `materialise` →
  `localSource`) with a recording `fakeConn`; `fc.count(MsgRequest)` directly asserts 0
  for BOTH orderings (`a→z` and `z→a`).
- Cross-platform: **high.** Operates purely on canonical forward-slash keys; all disk
  access via `ToOSPath(HostTarget())`; covered by Windows-hostile key cases (reserved
  device stems) in the table.

### Option B — Make `localSource` also reuse a tombstoned / old file whose bytes are still on disk; do NOT defer the remove.

- Correctness: **low.** Does not fix order-dependence: the synchronous `applyTombstone`
  `os.Remove` on the loop still deletes the old file from disk before the async puller's
  `localSource` reads it — deterministically for new-sorts-after-old. Rejected.
- Concurrency-safety: low (the loop-remove vs puller-read race remains).
- Testability: a test would be flaky (the race), or still fail (a→z). Rejected.

### Option C — Mirror the wire ordering on the receiver: iterate creates before deletes AND make the create's install COMPLETE before the matching delete's `os.Remove`.

- Correctness: medium only if the create's I/O is forced to finish first.
- Concurrency-safety: **low.** Forcing the create's materialise to complete before the
  delete either blocks the engine loop on network/disk I/O (violates CDD-1 — the loop
  must never block) or requires exactly Option A's coupling anyway. Ordering the diff
  alone does not help: the create is async, the delete is synchronous, so the delete
  still wins. Rejected (it degenerates into A or breaks CDD-1).

### Option D — Detect the rename at the SENDER and emit a dedicated `MOVE` wire type.

- Correctness: medium, but PR-5 §4/§5 explicitly DEFERS `MOVE` to a future `0x08+`
  type, and a suppress-the-tombstone MOVE introduces false-match data-loss risk
  (two identical files). Adds wire surface for a v1 efficiency win. Rejected on
  scope + the finding's own deferral + cross-platform/test cost.

### Option E — Deletion grace period: delay every tombstone `os.Remove` by N ms / until the next rescan so a concurrent create can reuse.

- Correctness/concurrency: **low.** Timing-based, not an order-independent guarantee;
  leaves a window where GC/convergence reason about a tombstone whose disk file still
  exists; complicates the watcher echo guards. Flaky by construction. Rejected.

## Decision

Implement **Option A**. Concretely:

1. `fetchTask` gains `preserveAdvertise bool` — whether the coupled FIRST step
   (`preserve`) broadcasts on success. `enqueueConflict` sets it `true` (a minted
   conflict copy the peer may lack — unchanged SR-7 behaviour); the new rename coupling
   sets it `false` (a received create must not re-broadcast — SR-6/PR-6).
2. New `enqueueRename(ps, newCreate, oldTomb)`: dedups + marks `inflight` on BOTH
   paths and enqueues `fetchTask{leaf: oldTomb, preserve: &newCreate,
   preserveAdvertise: false}` (atomic full-or-nothing on a saturated queue, like
   `enqueueConflict`). `runFetch` already materialises `preserve` first and, since
   `leaf.Deleted`, reports `applyTomb` (the deferred loop-side `os.Remove`) only after
   the copy lands; on copy failure it reports `conflictAbort` and the old file is left
   intact for a rescan-paced retry (no data loss).
3. `reconcileWithPeer` resolves all diff entries, buckets live-installs vs
   tombstone-installs, pairs each create with a same-`content_hash` tombstone whose OLD
   bytes are still local (skipping any path already `inflight`), enqueues the coupled
   rename task for matches, and executes the rest exactly as before. A standalone
   tombstone whose path is `inflight` (the deferral window) is skipped — the coupled
   task owns its application.

## Rationale

- It fixes the refuted claim at its root (the receiver's apply order) and makes
  zero-network **order-independent** — the property the finding asserts but did not
  deliver — without a new wire type (PR-5's explicit v1 constraint).
- It reuses the PR-3 coupling that is already proven race- and saturation-safe, so the
  new surface is minimal and the destructive `os.Remove` is gated on the copy landing —
  the same copy-before-destroy discipline SR-7 already relies on.
- It is data-loss-safe even on false content matches because it never suppresses either
  half of the delete+create pair; it only reorders execution and sources the create's
  bytes locally.
- It preserves the load-bearing invariants: CDD-1 (no loop blocking; transfer off the
  loop), GR-5 (no I/O under the lock), SR-6/SR-8/PR-6 (the rename create is applied, not
  authored ⇒ zero outbound).

## Consequences

- A genuine cross-peer rename whose old bytes are on the receiver now costs **0**
  `REQUEST`/`RESPONSE` regardless of the lexicographic relationship of old/new paths.
- The old tombstone's on-disk removal is deferred from "synchronously during reconcile"
  to "after the coupled create lands" (a sub-second puller round-trip). `inflight` on the
  old path prevents a concurrent reconcile from double-applying it; convergence/GC are
  unaffected at quiescence (the tombstone is still recorded + GC-handshake-broadcast by
  `applyTombstone`, unchanged).
- New/updated tests:
  - `reconcile_test.go` `TestRename_CrossPeer_ZeroNetwork_OrderIndependent` — the REAL
    receiver path via `onIndexUpdate`, table-driven over `a→z` (deterministic pre-fix
    miss), `z→a` (pre-fix race), and a Windows-hostile reserved-stem key pair; asserts
    `MsgRequest == 0`, new path holds the bytes, old path gone.
  - `TestRename_NoNetworkTransfer` retained but re-scoped in its comment to "the
    `localSource` dedup unit in isolation" (no longer over-claimed as the rename proof).
  - `sync_test.go` `TestRename_AfterSortingOrder_PropagatesNoLoss` — end-to-end
    convergence + no-loss for the `a→z` ordering across two live engines (the
    previously deterministic-miss ordering), complementing the existing `new<old` test.
- The zero-`REQUEST` assertion lives at the engine-receiver level (where REQUEST frames
  are observable before TLS), which is strictly more precise than a TLS integration test
  (encrypted frames are opaque) while driving the identical apply code path — satisfying
  skeptic #1 obligation #1 and skeptic #2 obligation (1) in substance, and (2) directly.
