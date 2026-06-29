# Skeptic #1 vote — MK-6 "FIXED" verdict

- Date: 2026-06-29
- Target: `docs/audit/findings/review/MK-6.md` (verdict: FIXED)
- Finding: `docs/audit/findings/merkle/MK-6-persisted-snapshot-restart-deletion.md`
- **Vote: REFUTED** (the "fixed" claim is not solidly evidenced at the level the
  finding itself mandates)

## Why the FIXED verdict is not solidly supported

### 1. The finding's own acceptance test is NOT implemented (primary refutation)
MK-6 defines an explicit acceptance criterion (`MK-6-…md:81-84`):
"create+sync a file on A and B; stop A; delete the file on A's disk; restart A;
assert A synthesizes a tombstone from the snapshot diff, B removes its copy, and the
file is **not** resurrected (extends the SR-10 scenario across a restart boundary)."

This is an *end-to-end two-node restart* test. It does not exist. Verified:
`test/integration/sync_test.go` contains only
`TestTwoNode_Converge`, `TestConflict_NeitherVersionLostSymmetricName`,
`TestDeletion_NoResurrection`, `TestBackpressure_BidirectionalConverges`,
`TestRename_PropagatesNoLoss`, `TestKilledTransfer_NoCorruptFileThenRecovers`.
There is no restart-boundary case (grep for "restart"/`startupReconcile` in
`test/integration/` returns nothing but comments in `helpers.go`).

The reviewer concedes this (`review/MK-6.md:44-52`) but waves it off by re-reading the
finding's *Status* line ("unit tests + WS-4 wiring") instead of its *Acceptance test*
section. A finding is "fixed" when its acceptance criteria are met. MK-6's stated
acceptance criterion is the two-node restart scenario, and it is unmet. The whole
*point* of MK-6 (synthesis risk **R-5**, severity **high** — "resurrection /
divergence after a restart") is the cross-peer behaviour, which is exactly what the
missing integration test would exercise. Unit tests on `SynthesizeDeletions` /
`restoreVVs` prove the pure functions; they do NOT prove the synthesized tombstone
actually propagates over the wire and that the peer drops its copy without
resurrection. That causal chain (snapshot diff → tombstone → broadcast → peer delete
→ no re-create) is the load-bearing claim and is untested end-to-end.

### 2. Untested correctness edge: recreate-over-tombstone across a restart loses to a peer
`startupReconcile` (`engine.go:219-220`) runs `restoreVVs` then `SynthesizeDeletions`.
For a path whose snapshot entry is a **tombstone** (`p.Deleted`) but which exists on
disk again (file recreated while the daemon was down), `restoreVVs` skips it
(`engine.go:245` `if !ok || p.Deleted { continue }`) so the recreated file keeps the
**empty** VV the fresh scan seeds. A peer that still holds the tombstone carries a
**non-empty** VV. On reconnect the peer's tombstone VV can dominate the empty VV of
the local recreate ⇒ the legitimate recreate is treated as stale ⇒ re-deleted
(resurrection of the deletion / lost local create). The code comment claims "A
reappeared path over a prior tombstone is a new create (empty VV)" but a create that
cannot win against the surviving tombstone is precisely the data-loss case MK-6 was
written to prevent. No unit or integration test covers recreate-over-tombstone across
a restart.

### 3. Crash-safety window (noted, lower weight)
The snapshot is persisted only on clean shutdown (`engine.go:349`) and on a periodic
ticker (`engine.go:339-340`). A crash between ticks leaves a stale snapshot, so
`restoreVVs` reattaches stale VVs and `SynthesizeDeletions` diffs against stale
content. This was the subject of `design/tree-critic/tree-critic-2` (refuted there),
so I weight it low, but combined with #1 and #2 it reinforces that the restart path is
only validated for the happy unit-level path.

## Conclusion
The implemented + unit-tested surface is real and clean, but the finding's declared
acceptance test (two-node deletion-across-restart) is absent, and at least one
in-domain correctness edge (recreate-over-tombstone) is untested. Per the default
("refuted=true if the fixed claim is not solidly evidenced"), the FIXED verdict is not
solidly evidenced for the high-severity end-to-end claim it makes.
