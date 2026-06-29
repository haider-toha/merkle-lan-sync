# PR-4 skeptic #1 vote — REFUTE "fixed" (partial)

- Date: 2026-06-29
- Reviewing: `docs/audit/findings/review/PR-4.md` (verdict FIXED) against
  `docs/audit/findings/protocol/PR-4-deletions-tombstones-resurrection.md`,
  commit `af12de0`.
- Vote: **refuted = true** (confidence: medium)

## What is genuinely solid (not disputed)
The marquee anti-resurrection path is real and well-tested:
- Tombstone semantics `internal/merkle/fileinfo.go:69-76` (`SetDeleted`: zero
  content, size 0, `Deleted=true`, `Version.Bump(self)`, copy-on-write).
- Ack-gated GC `internal/reconcile/tombstone.go:14-25` / `:33-55`; retain-on-no-peer,
  never a timer.
- Stale-peer dominance + load-bearing premature-GC NEGATIVE test
  (`reconcile_test.go:716`) and integration `TestDeletion_NoResurrection`
  (`test/integration/sync_test.go:92`).
- Restart-survival (obligation 5): snapshot stores tombstones
  (`internal/merkle/snapshot_test.go:17`), reload carries them forward UNCHANGED
  (`internal/merkle/scanner.go:116-118` `SynthesizeDeletions`), engine wires
  save/load (`engine.go:208`, `:975`). Correct — original VV preserved so the
  ack-gate still converges.

So obligations 1, 2, 3, 5 are met. This is why the vote is "partial," not a flat
rejection of the fix.

## Gap 1 — Obligation #4 (delete-vs-concurrent-modify → conflict copy) is UNTESTED
PR-4 §8 obligation 4 explicitly requires: "Delete-vs-concurrent-modify → conflict
copy per PR-3 (no loss)." There is **no test** exercising a Concurrent pair where
one side is a tombstone and the other a live modification:
- Resolver matrix `reconcile_test.go:142-162`: the only Concurrent-diff case
  (`:160`) is **live-vs-live**; `:158` is a clean dominated-by tombstone; `:161`
  is **both** tombstones. None is tombstone-vs-live-concurrent.
- Integration: `TestConflict_NeitherVersionLostSymmetricName` is live-vs-live;
  `TestDeletion_NoResurrection` is a clean (dominated) delete, no concurrent edit.

The code path itself (`apply.go:51-83` → `conflictPlan` → `aWins`) does avoid data
loss in either branch, but the tie-break is suspect and unverified: `aWins`
(`conflict.go:30-40`) ranks by `ModTimeNS` first, and `SetDeleted` does **not**
update `ModTimeNS` — a tombstone carries the *stale pre-delete* mtime. So whether a
fresh concurrent modification "wins" over a delete is decided by the deleted file's
old mtime. No data is dropped (loser → `.sync-conflict` copy when live), but the
chosen resolution is semantically arbitrary and has **zero** test coverage. The
finding's own obligation #4 is unmet.

## Gap 2 — `DropCounter` (#10590 ghost-counter mitigation) is unreachable dead code
The finding status line and §5.1 claim the ghost-counter resurrection class
(Syncthing #10590, FM-1) is "Mitigated by ack-gated `DropCounter` on explicit
device removal." But:
- `DropCounter` (`tombstone.go:64-79`) has **no production caller** — `grep` across
  `internal/`, `cmd/`, `test/` finds only its definition; no un-pair / device-removal
  path is wired (`cmd/msync/main.go` has none).
- Only the pure helper `dropFromVV` is unit-tested (`reconcile_test.go:796`);
  `DropCounter` itself (lock + rebuild + symmetric semantics) is never exercised.

For v1's stated 2-device scope the ghost-counter case may not arise in practice, but
the finding presents this as part of the delivered fix. As shipped it is unreachable,
so the claimed mitigation is not evidenced.

## Conclusion
The core resurrection bug is fixed and convincingly tested. However "status: fixed"
overclaims: one of the finding's five explicit test obligations (#4) has no coverage
and exercises a tie-break that depends on a stale tombstone mtime, and one of its
named mitigations (`DropCounter` for #10590) is dead, unreachable code. Per the
"default refuted if not solidly evidenced" rule, **refuted = true**.
