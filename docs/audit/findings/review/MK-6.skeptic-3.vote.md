# Skeptic #3 vote — MK-6 "fixed" verdict

- Date: 2026-06-29
- Role: Phase 6 skeptic #3 (refute the FIXED verdict)
- Target: `docs/audit/findings/review/MK-6.md` (verdict FIXED), finding
  `docs/audit/findings/merkle/MK-6-persisted-snapshot-restart-deletion.md`
- **Vote: REFUTED (verdict not solidly evidenced) — confidence: medium**

## What is actually proven
The implemented surface is real and unit-tested:
- `internal/merkle/scanner.go:102-123` `SynthesizeDeletions` — create-only on empty
  prev, tombstone on live-in-prev/absent, carry-forward unchanged for an existing
  tombstone.
- `internal/reconcile/engine.go:205-226` `startupReconcile` wiring; `:233-255`
  `restoreVVs`; snapshot persisted on shutdown/ticker/post-rescan
  (`engine.go:349,340,947`); snapshot set includes tombstones
  (`broadcast.go:13-20`).
- `startupReconcile` runs inside `NewEngine` (`engine.go:196`) before peers/watcher
  start, so there is no startup race with inbound indexes. Good.

These are necessary but **not sufficient** for a HIGH-severity finding whose entire
risk (R-5, "the least-mitigated risk") is *resurrection / divergence across a restart*.

## Why the FIXED verdict is not solidly evidenced

### 1. The finding's own named acceptance test does not exist.
MK-6's "Acceptance test (deletion-across-restart)" is explicit: two nodes, stop A,
delete on A's disk, **restart A**, assert A synthesizes a tombstone *from the snapshot
diff*, B removes its copy, no resurrection. No such test exists. The integration
suite (`test/integration/sync_test.go`) has only `TestDeletion_NoResurrection`
(`:92-119`) — and that test deletes the file while **A's daemon is still running**
(line 105 `os.Remove` then `waitRootChanged`; comment "DISCONNECTED" but the process
is alive). That exercises the **live-rescan** delete path, *not* the MK-6
startup-snapshot-diff path. The reviewer concedes this ("no restart-boundary case")
but waves it through on the technicality that the Status text only claims unit tests.
For a data-loss-class finding, asserting FIXED while the named acceptance scenario is
unexecuted is exactly the "fixed-but-unproven" pattern this vote exists to catch.

### 2. The end-to-end resurrection-resistance is never exercised.
The unit test proves `SynthesizeDeletions` returns a tombstone *struct in isolation*.
It does **not** prove the synthesized tombstone (born at startup, before any peer
connects) is actually served in the INDEX, that the peer applies it, and — the real
risk — that its bumped VV **dominates** the peer's live copy so the peer does not
re-send the file back (resurrection). None of that propagation chain is tested.

### 3. Unhandled edge case: delete-while-down vs concurrent remote edit.
`SetDeleted(self)` does a single `Bump(self)` on the snapshot's stored VV
(`scanner.go:120`). If peer B **edited** the same file while A was down, B's VV has
advanced beyond the VV A persisted. A's synthesized tombstone = `prevVV.Bump(A)` is
then **concurrent** with B's edited VV (each side holds a counter the other lacks),
i.e. a delete-vs-edit *conflict*, not a clean delete-wins. Whether the resolver
suppresses resurrection or lets B's edit win (resurrecting the file) is undetermined
by any test here. This is precisely the divergence MK-6 was raised to prevent, and it
is neither tested nor obviously handled. The reviewer's "skeptical check" only
considered the missing/corrupt-snapshot and tombstone-re-stamp cases, not the
concurrent-edit case.

## Disposition
The code likely handles the simple case correctly, but "FIXED" for a HIGH-severity
resurrection risk rests on unit tests plus a code read of the wiring, with (a) the
finding's own acceptance test absent and (b) the adversarial concurrent-edit-across-
restart sub-case untested. That is insufficient evidence. Recommend Phase 7 add the
two-node stop/delete/restart integration test AND a delete-while-down-vs-remote-edit
case before MK-6 is marked FIXED. Until then: REFUTED.
