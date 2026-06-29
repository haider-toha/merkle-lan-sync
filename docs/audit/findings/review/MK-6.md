# Review verdict — MK-6 (Persisted snapshot ⇒ detect deletions across a daemon restart)

- Reviewed: 2026-06-29 (Phase 6 reviewer)
- Finding: `docs/audit/findings/merkle/MK-6-persisted-snapshot-restart-deletion.md` (claimed `status: fixed`, WS-1 `182ff00` + WS-4 `af12de0`)
- **Verdict: FIXED** (with one coverage note, below — does not change the verdict)

## What was claimed
Persist a local-only snapshot (gob, includes VV + deleted); on startup load it, full-scan,
and diff: present-in-snapshot/absent-on-disk ⇒ synthesize a tombstone (bumped VV);
missing/corrupt snapshot ⇒ create-only (no synthesized deletions). WS-4 wiring:
`startupReconcile`/`restoreVVs` load + restore + `SynthesizeDeletions` at boot; snapshot
persisted on shutdown + periodically; missing snapshot ⇒ cold-start reseed.

## Evidence verified against code
- Snapshot persist/load: `internal/merkle/snapshot.go:33-70` `SaveSnapshot` (atomic
  temp→fsync→rename + parent fsync), `:76-92` `LoadSnapshot` (missing ⇒ `(nil,nil)`,
  corrupt/unknown-version ⇒ `ErrSnapshotFormat`), gob justified for local-only state (GR-7).
- `SynthesizeDeletions`: `internal/merkle/scanner.go:102-123` — `len(prev)==0` ⇒ return
  `cur` (create-only, avoids mass-delete); live-in-prev/absent ⇒ `SetDeleted(self)`;
  existing tombstone carried forward UNCHANGED (no re-bump); reappeared path ⇒ cur wins.
- WS-4 startup wiring: `internal/reconcile/engine.go:205-226` `startupReconcile`
  (LoadSnapshot → Scan → `restoreVVs` → `SynthesizeDeletions` → `reseed = len(prev)==0`);
  `engine.go:233-255` `restoreVVs` (unchanged keeps history, changed bumps, new keeps empty).
- Snapshot persisted on shutdown + periodically: `engine.go:349` (post-`Run` shutdown),
  `engine.go:339-340` (snapshot ticker), `engine.go:947` (after each rescan);
  `engine.go:975-985` `saveSnapshot`.

## Evidence verified against tests (all PASS under `-race`)
- `internal/merkle/scanner_test.go:60` `TestSnapshotDiff_SynthesizesDeletion` — snapshot
  has b.txt, disk does not ⇒ tombstone with bumped self counter; input not mutated.
- `scanner_test.go:96` `TestSnapshotMissing_CreateOnly`; `:112`
  `TestSnapshotDiff_CarriesTombstoneUnchanged` (no re-stamp of a peer-authored tombstone);
  `:131` `TestSnapshotDiff_RecreatedPathDropsTombstone`.
- `internal/merkle/snapshot_test.go:13/34/46/58`
  `TestSnapshot_RoundTrip` (incl. tombstone+empty VV) / `_MissingReturnsNil` /
  `_CorruptRejected` / `_AtomicNoTempLeft`.
- `internal/reconcile/reconcile_test.go:766` `TestRestoreVVs` — unchanged keeps VV,
  changed bumps on top, fresh keeps empty.

## Run-log corroboration
- `docs/audit/runs/race-all.log:6,9` `ok internal/merkle`, `ok internal/reconcile`;
  fresh 2026-06-29 run all named tests `--- PASS`.

## Coverage note (not a regression)
The finding's "Acceptance test (deletion-across-restart)" describes a two-node
stop/delete/restart scenario. That end-to-end path is covered at the UNIT level
(`SynthesizeDeletions` + `restoreVVs` + snapshot round-trip) plus verified engine wiring,
not as a dedicated two-node restart integration test (the integration suite,
`test/integration/sync_test.go`, has convergence/conflict/deletion/rename/killed/large but
no restart-boundary case). The finding's own Status text claims unit tests + WS-4 wiring —
which is exactly what exists — so the documented claim is met. A two-node restart
integration test would strengthen this; flagged for Phase 7 as an enhancement, not a defect.

## Skeptical check
The create-only fallback on a missing/corrupt snapshot is the key anti-footgun (it prevents
mass deletions on first run / snapshot loss) and is directly tested. The carry-forward-
unchanged test prevents a restart from re-stamping a peer's tombstone as a local delete.
No correctness gap found in the implemented + tested surface.

## Phase 7 resolution (round 1) — this FIXED verdict was REFUTED, then properly closed
Both Phase 6 skeptics REFUTED this verdict (`votes/MK-6-skeptic1.md`,
`MK-6.skeptic-3.vote.md`). The refutation was correct on two counts and prompted a real
fix, NOT a re-assertion:
- The coverage note above understated a genuine DEFECT: `restoreVVs` dropped the persisted
  tombstone VV on a recreate-over-tombstone (`engine.go:245` skipped `p.Deleted`), so a
  file recreated while the daemon was down kept an empty VV and was DominatedBy-re-deleted
  by a peer's tombstone — data loss, not just a missing test (skeptic #1 §2). This is now
  fixed: `restoreVVs` bumps the tombstone VV so the recreate dominates the delete.
- The named two-node deletion-across-restart acceptance test (and the recreate +
  concurrent delete-vs-edit sub-cases) now exist as end-to-end scenarios
  (`test/integration/sync_test.go` `TestRestart_*`), backed by a stop/restart harness.
Closed by commit `14f60d1ea47e10170d4c6488efe8340ecde1de3e`; decision
`docs/audit/decisions/phase7/MK-6-restart-recreate-and-concurrent-edit.md`. Verdict now:
**FIXED (evidenced end-to-end)**.
