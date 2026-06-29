# Review verdict — PR-4 (Deletions via tombstones: propagation + anti-resurrection)

- Reviewed: 2026-06-29 (Phase 6 reviewer)
- Finding: `docs/audit/findings/protocol/PR-4-deletions-tombstones-resurrection.md` (claimed `status: fixed`, WS-4, commit `af12de0`)
- **Verdict: FIXED**

## What was claimed
A delete is a versioned tombstone (content zeroed, bumped VV) that DOMINATES any
pre-delete version, so a stale peer deletes locally and does not resurrect. Tombstones are
retained until the peer acks (canGC), then GC'd symmetrically — never on a timer.
`DropCounter` strips a de-paired device's ghost counter.

## Evidence verified against code
- Tombstone semantics: `internal/merkle/fileinfo.go:69-76` `SetDeleted` (zero content,
  size 0, `Deleted=true`, `Version.Bump(self)`, copy-on-write).
- Ack-gated GC: `internal/reconcile/tombstone.go:14-25` `canGC` (GC only when the peer
  advertises a tombstone whose VV `Dominates`-or-`Equal`s ours); `:33-55`
  `gcTombstonesLocked` (GC only when ALL known peers acked; retain if no peer known;
  never a timer).
- `DropCounter` (device-removal-only, copy-on-write): `tombstone.go:64-91`.
- Dominance via missing-counter-0 (PR-2): `internal/protocol/versionvector.go:158-200`.
- Applied tombstone re-advertised once for symmetric GC handshake, idempotent (origin VV):
  `internal/reconcile/engine.go:737-750` `applyTombstone`.

## Evidence verified against tests (all PASS under `-race`)
- `internal/reconcile/reconcile_test.go:689` `TestCanGC` — 6-case table (no-advert ⇒ no GC;
  stale tombstone ⇒ no GC; equal/newer tombstone ⇒ GC; concurrent ⇒ no GC).
- `:716` `TestTombstone_NoResurrectionAndPrematureGCNegative` — retained tombstone vs a
  stale peer file ⇒ `planNoOp` (no resurrection); had it been GC'd (local nil) the SAME
  advert ⇒ `planInstall` (resurrection) — proving the ack-gate is LOAD-BEARING.
- `:731` `TestGCTombstones_AckGated` — no peer ⇒ retain; peer not acked ⇒ retain; peer
  advertises the tombstone ⇒ GC proceeds.
- `internal/merkle/scanner_test.go:15` `TestSetDeleted_BumpsAndZeroes`;
  `internal/protocol/versionvector_test.go:31` "tombstone dominates stale absent".
- Integration `test/integration/sync_test.go:92` `TestDeletion_NoResurrection` — A deletes
  while disconnected; on reconnect the file is gone on BOTH and not resurrected on A.

## Run-log corroboration
- `docs/audit/runs/scenario-deletion.log:8` `--- PASS: TestDeletion_NoResurrection`;
  `docs/audit/runs/race-all.log:9,11`; fresh 2026-06-29 run all `--- PASS`.

## Skeptical check
The premature-GC NEGATIVE test is exactly what a skeptic would demand: it proves that
without the ack-gate the file resurrects, so the gate is doing real work. Retention falls
back to "retain", never a TTL (no offline-peer resurrection window).

## Phase 7 disposition (round 1) — REFUTED then FIXED

The above FIXED verdict over-credited the §5 stale-peer case to the WHOLE finding and was
correctly **REFUTED by 2/3 skeptics** (`votes/PR-4-skeptic2.md`, `protocol/votes/PR-4-skeptic1.md`):
this verdict verified that `DropCounter` *exists* (`tombstone.go:64`) but not that it is
*invoked* — and it had **zero production callers**, so the named ghost-counter mitigation
(#10590 / FM-1, finding §5.1) was dead, unreachable code and the resurrection it claims to
defend was not prevented in the binary; the exported method itself, and obligation #5 at
the integration level, were untested.

Resolved in Phase 7 round 1, commit `9b5e9c503ec89aa692abc1e7285d46a4c41a3cef`
(decision `docs/audit/decisions/phase7/PR-4-ghost-counter-wiring-and-test-obligations.md`):
- The ghost-counter prune is now **wired into the binary** — `Config.Peers` declares the
  paired set; `startupReconcile`'s `sweepDepairedCountersLocked` prunes any de-paired
  device's counter from every loaded leaf's VV (gated nil ⇒ retain-all; never a live
  device's counter); `cmd/msync` threads the `-peer` set in.
- Proven **load-bearing** by `TestGhostCounter_ResurrectionPreventedByDrop` (with the ghost
  ⇒ resurrection-as-conflict; after the drop ⇒ clean delete), plus
  `TestDropCounter_SweepsAllLeavesAndRebuilds`, `TestSweepDepairedCounters`,
  `TestEngine_StartupSweepsDepairedGhostCounter`.
- Obligation #5 covered end-to-end by integration
  `TestRestart_PendingTombstoneSurvivesAndNoResurrection`; obligation #4 by the PR-3 fix
  plus `TestResolver_ModifyWinsKeepsLiveFile`.

`go build ./... && go vet ./... && GOOS=windows GOARCH=amd64 go build ./cmd/msync &&
go test ./... -race` — all green (2026-06-29). **Verdict: FIXED** (the finding's "fixed"
scope is now truthful; runtime hot un-pair is the documented deferred path).
