# Skeptic #1 vote — PR-5 "FIXED" verdict (REFUTE attempt)

- Date: 2026-06-29
- Reviewing: `docs/audit/findings/review/PR-5.md` (verdict FIXED) and
  `docs/audit/findings/protocol/PR-5-rename-handling.md` (status fixed).
- Vote: **refuted = true** (the headline non-trivial claim is not solidly evidenced
  for the real path; confidence medium).

## What is solid
- Correctness / no-loss on a rename IS proven across two live engines:
  `test/integration/sync_test.go:160` `TestRename_PropagatesNoLoss` converges with the
  payload at the new path and the old path gone on BOTH sides. Ran green under `-race`
  (2026-06-29). Rescan-detects-rename and the local-reuse unit test also pass.
- No new wire type; delete+create baseline is a safe, well-trodden choice.

## The gap — the "ZERO network transfer" claim is tested only in a synthetic
## scenario that bypasses the real propagation ordering

The verdict's own "Skeptical check" concedes: *"The 'zero network' claim is the only
non-trivial one and it is asserted directly (`MsgRequest == 0`)."* That assertion lives
in `internal/reconcile/reconcile_test.go:508` `TestRename_NoNetworkTransfer`, which is
**not a rename**. It manually seeds `old.txt` (live, on disk, recorded) and then calls
`e.materialise(... new.txt ...)` directly while `old.txt` is still present. Of course
`localSource` (`transfer.go:180-201`) finds the live `old.txt` on disk and copies it —
zero `REQUEST`. This proves `localSource` works in isolation; it does **not** prove a
rename costs zero network.

In the REAL cross-peer rename the receiving peer does the opposite, because the
create-before-delete wire ordering is discarded at apply time:

1. `broadcast.go:42-49` `orderCreatesBeforeDeletes` orders the batch on the wire so the
   create precedes the tombstone. But the receiver's `onIndexUpdate`
   (`engine.go:578-588`) loops the ENTIRE decoded set into `ps.index` and then calls
   `reconcileWithPeer` ONCE. `reconcileWithPeer` (`engine.go:631-657`) builds whole
   trees and runs `merkle.Diff` — **the per-leaf wire order is thrown away**. So the
   ordering buys nothing at apply time.
2. In that whole-tree diff, the old path resolves to a tombstone → `applyTombstone`
   (`engine.go:737-756`) which calls `os.Remove(old)` **synchronously inside the
   reconcile loop**. The new path resolves to a live install → `enqueueFetch`
   (`engine.go:711`), which only queues onto the per-peer puller (async goroutine).
3. The puller later runs `materialise(new)` → `localSource(hash, "new")`. By then the
   only local holder of those bytes — `old.txt` — has been tombstoned
   (`fi.Deleted` ⇒ skipped) AND removed from disk (re-hash fails). `localSource`
   returns false → the engine falls through to **`fetchOverWire`** (`transfer.go:240`).

So on the receiving peer a genuine rename issues a network `REQUEST`, contradicting
"a rename where the old bytes are still on disk costs **zero network transfer**"
(PR-5 §3). The integration test deliberately does **not** assert `MsgRequest == 0`
(its own comment defers that to the unit test), so NO test exercises the realistic
zero-network path. The one test that asserts it is constructed to keep the source file
alive — the exact condition the real delete-side destroys first.

## Secondary concern — the create-before-delete ordering is decorative
The ordering was specified (PR-5 §2 "the one correctness subtlety") to stop a peer
transiently deleting the only copy and to preserve the old bytes for local reuse. Both
purposes are defeated by the batch-then-whole-tree-diff apply model (point 1 above).
No-loss still holds, but only because of `atomicWriteVerify` + the source peer always
retaining the bytes + diff-persistence retry — NOT because of the ordering the finding
credits. The credited mechanism does not do what is claimed.

## Why this refutes "FIXED" rather than merely nags
The finding is medium severity precisely because its value proposition is efficiency
(zero-network), and the verdict signed off on that being "asserted directly." It is not
asserted for the path that matters; it is asserted for a path engineered to make it
true. A reviewer relying on `TestRename_NoNetworkTransfer` would believe real renames
are free; they are not on the receiver. That is a missing test + an over-claimed,
under-covered code path — the bar for refuting a "fixed" claim.

## What would flip me to "fixed"
- A two-engine test that renames on A and asserts B issued **0** `MsgRequest` for the
  content hash (the §6 test obligation #1, which says "assert no REQUEST for that
  content_hash" — currently unmet), OR
- Down-scope the finding text/verdict to state zero-network holds only for
  dedup/same-engine reuse, not for cross-peer renames, and have the ordering claim
  reflect that it does not survive to apply time.
