# PR-4 skeptic #2 vote — REFUTE "fixed"

- Date: 2026-06-29
- Role: skeptic #2 of 3 challenging the FIXED verdict for PR-4
  (`docs/audit/findings/protocol/PR-4-deletions-tombstones-resurrection.md`).
- Vote: **refuted = true** (confidence: medium)

## What is genuinely solid (not contested)

The marquee case — §5 "anti-resurrection by a stale peer" — is correctly implemented
and load-bearing-tested:
- `SetDeleted` bumps the deleter's VV counter and zeroes content
  (`internal/merkle/fileinfo.go:69-76`).
- `canGC` ack-gates GC; retains when no peer is known; never a timer
  (`internal/reconcile/tombstone.go:14-25,33-55`).
- `TestTombstone_NoResurrectionAndPrematureGCNegative` proves the gate is load-bearing.
- Restart-with-pending-tombstone logic is sound at the unit level:
  `SynthesizeDeletions` carries an existing tombstone forward UNCHANGED
  (`internal/merkle/scanner.go:110-120`), snapshot persists `Deleted`+VV
  (`internal/merkle/snapshot.go`), loaded at engine start (`engine.go:207-220`).

## Why I still vote REFUTED — the "fixed" claim overreaches

The finding's status line AND §5.1 explicitly enumerate **three** resurrection failure
modes and claim all are mitigated as part of "fixed". One of them is NOT mitigated in
the shipped system:

### Gap 1 — Ghost-counter mitigation (#10590) is dead, unwired, untested code

- The finding states (status line): "`DropCounter` strips a de-paired device's counter".
- §5.1 lists "**Ghost counters (#10590 / FM-1)**" — and #10590 is the very issue the
  finding calls "the marquee long-lived sync bug" — as "Mitigated by ack-gated
  `DropCounter` on explicit device removal."
- **Reality:** `DropCounter` (the exported method, `internal/reconcile/tombstone.go:64`)
  has **ZERO callers anywhere** — not in `cmd/msync`, not in any un-pair/device-removal
  path, not in any test. Verified:
  `grep -rn "DropCounter" --include="*.go"` returns only the definition.
  Only the private helper `dropFromVV` is unit-tested (`reconcile_test.go:796`).
- Consequence: in the running daemon, removing/un-pairing a device leaves its ghost
  counter in every leaf's VV permanently. Neither vector can then dominate ⇒ the exact
  #10590 resurrection ("deleted files resurrect as clean copies, no conflict marker")
  the finding claims to defend against is **not actually prevented** in the binary.
  The mitigation exists only as an unreachable method.
- The review verdict claims it "verified `DropCounter` (device-removal-only) at
  `tombstone.go:64-91`" — but it verified only that the code *exists*, not that it is
  *invoked* or that the ghost-counter resurrection scenario is *tested*. A skeptic
  demands the negative/scenario test; there is none.

### Gap 2 — Test obligation #5 has no integration-level coverage

PR-4 §8 obligation #5 ("Restart-with-pending-tombstone → deletion survives the restart")
is covered only at the pure-function level (`TestSnapshotDiff_CarriesTombstoneUnchanged`,
`TestSnapshot_RoundTrip`). There is no integration test that actually constructs an
engine holding a not-yet-acked tombstone, tears it down, reconstructs it from the
on-disk snapshot, and asserts no resurrection through the full reconcile/broadcast path.
`grep` for restart/reload in `test/integration/` finds none. The wiring (load→
SynthesizeDeletions→rebuild) is plausible but unexercised end-to-end.

## Disposition

The central stale-peer invariant is fixed. But "fixed" as written bundles a named
mitigation (`DropCounter` / ghost-counter #10590) that is unreachable dead code with no
test, plus an obligation (#5) lacking integration coverage. The verdict is not solidly
evidenced for the full scope it claims. Recommend: either (a) wire `DropCounter` into a
device-removal path + add a ghost-counter resurrection scenario test, or (b) narrow the
finding's "fixed" scope to exclude the un-implemented ghost-counter mitigation and mark
it open/deferred.
