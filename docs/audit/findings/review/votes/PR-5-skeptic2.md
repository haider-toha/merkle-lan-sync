# Skeptic #2 vote — PR-5 "FIXED" verdict — REFUTED

- Date: 2026-06-29
- Role: skeptic #2 of 3, challenging the Phase-6 verdict that PR-5 is FIXED
- Verdict under challenge: `docs/audit/findings/review/PR-5.md` (FIXED)
- Finding: `docs/audit/findings/protocol/PR-5-rename-handling.md` (status: fixed)
- My vote: **REFUTED** (the headline non-trivial claim is not delivered on the real path and is untested there)

## The claim being challenged

The reviewer wrote: *"The 'zero network' claim is the only non-trivial one and it is
asserted directly (`MsgRequest == 0`), with `localSource` re-hashing the candidate on
disk so a stale index cannot feed wrong bytes."* The finding's status line states the
v1 rename "makes the new path cost ZERO network when the bytes are still local," and
that create-before-delete ordering plus content-addressed reuse make this safe.

Data-loss / convergence is NOT in dispute — that part is solidly evidenced and the
integration test confirms it. What I refute is that the **zero-network efficiency win
(the entire point of PR-5) is actually achieved on the real cross-peer rename path**.

## The gap: the wire ordering does not control the receiver's apply order

`orderCreatesBeforeDeletes` (`internal/reconcile/broadcast.go:42-49`) only orders the
**broadcast batch on the sender's wire**. The receiver does **not** honour that order:

1. `onIndexUpdate` (`engine.go:578-588`) drops the incoming leaves into an unordered
   map `ps.index[fi.Path] = fi`, then calls `reconcileWithPeer`.
2. `reconcileWithPeer` (`engine.go:633-660`) recomputes a **fresh `merkle.Diff`** of the
   whole tree and iterates the result. `merkle.Diff` emits entries in **path-sorted**
   order (`internal/merkle/differ.go:84` "Recurse the union of child names (sorted...)"),
   NOT creates-before-deletes.
3. `execute` (`engine.go:668-693`) handles the two halves **asymmetrically**:
   - a create → `enqueueFetch` → pushed onto `ps.fetchQ`, consumed **asynchronously**
     by the `pullLoop` goroutine (`engine.go:751-763`);
   - a delete → `applyTombstone` (`engine.go:737-749`) which calls `os.Remove(osPath)`
     **synchronously on the engine loop**, deleting the old file from disk immediately.

`localSource` (`transfer.go:180-201`) re-hashes the candidate **on disk**
(`merkle.HashFile(osPath)`); if the old file has already been `os.Remove`d, it returns
`false` and `materialise` falls through to the full **network fetch** (`transfer.go:240-245`).

## Consequence: zero-network fails deterministically for half of all renames

Because the receiver iterates the diff in **lexicographic path order**:

- Rename where **new path sorts AFTER old path** (e.g. `a.txt` -> `z.txt`): the diff
  yields `delete(a.txt)` BEFORE `create(z.txt)`. `applyTombstone(a.txt)` runs first and
  synchronously deletes `a.txt` from disk. When the puller later runs `localSource` for
  `z.txt`, the only on-disk holder of that content is gone, so it does a **full
  32KB-chunked network fetch of the entire file**. This is a *deterministic* miss of the
  zero-network optimisation, not merely a race.
- Rename where **new path sorts BEFORE old path** (e.g. `z.txt` -> `a.txt`): `create`
  is enqueued first, but `enqueueFetch` only *queues*; the `pullLoop` goroutine runs
  concurrently with the engine loop, which proceeds to `applyTombstone` (synchronous
  `os.Remove`) on the very next iteration. Whether `localSource` reads the old file
  before it is removed is a genuine **data race in timing** — sometimes zero network,
  sometimes a full fetch.

So the PR-5 efficiency claim holds on the receiver only by luck, and is *guaranteed to
fail* for any rename whose target name sorts after the source name.

## Why the tests do not catch this

- `internal/reconcile/reconcile_test.go:508` `TestRename_NoNetworkTransfer` is a
  **synthetic local materialise**: it manually pre-places `old.txt` on disk + in the
  recorded set, then calls `e.materialise(new.txt)` directly and **never applies the
  tombstone for old.txt**. It deliberately keeps the source bytes on disk, so it cannot
  exercise the delete-before-create-fetch interaction that breaks the optimisation in
  the real flow. It proves `localSource` works in isolation, not that the receiver
  reaches it during a real rename.
- `test/integration/sync_test.go:160` `TestRename_PropagatesNoLoss` is the only
  end-to-end rename test, and it asserts **convergence + no loss only** — it never
  asserts `MsgRequest == 0` (or any network-cost bound) on receiver B. So the headline
  "zero network" claim is **untested on the actual cross-peer path**.
- The chosen rename names in the integration test (`old.txt` -> `new.txt`) happen to
  hit the favourable-but-racy ordering (`new` < `old`), so even if a cost assertion
  were added it would pass intermittently and mask the deterministic `a->z` failure.

## Severity / honesty note

This is **not data loss** — the network-fetch fallback still converges (A retains the
bytes), which is why `TestRename_PropagatesNoLoss` passes. The finding itself is rated
"medium — the difference is transfer efficiency, not data loss." Precisely for that
reason, the *only* thing that distinguishes "fixed" from "delete+create with naive
transfer" is the zero-network win — and that win is not delivered on the real path and
is not tested there. The reviewer singled out the zero-network claim as "the only
non-trivial one" and accepted it on a test that bypasses the tombstone. The FIXED
verdict therefore over-claims.

## What "fixed" would actually require

1. An integration assertion that a real cross-peer rename (including a `a->z`,
   new-sorts-after-old case) costs **0** `REQUEST`/`RESPONSE` for the moved content on
   the receiver; and
2. Either deferring `applyTombstone`'s on-disk `os.Remove` until after the
   create's `localSource` copy lands, or having the create-fetch path search the
   tombstoned/old on-disk file before it is removed — so the optimisation is
   order-independent rather than lexicographically lucky.

Until (1) exists and (2) closes the delete-before-reuse window, the zero-network claim
is unproven on the path that matters.

## Vote

REFUTED — confidence high. Evidence: `engine.go:578-588,633-693,737-763`,
`broadcast.go:42-49`, `transfer.go:180-201,229-245`, `differ.go:84`,
`reconcile_test.go:508`, `test/integration/sync_test.go:160`.
