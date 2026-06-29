# Review verdict — PR-5 (Rename handling: delete+create, zero-network via content reuse)

- Reviewed: 2026-06-29 (Phase 6 reviewer)
- Finding: `docs/audit/findings/protocol/PR-5-rename-handling.md` (claimed `status: fixed`, WS-4, commit `af12de0`)
- **Verdict: FIXED**

## What was claimed
v1 rename = emergent delete-old + create-new (no new wire type). The scanner synthesizes a
tombstone for the old path and a create for the new; broadcasts order creates-before-deletes
so a peer never transiently loses the only copy; content-addressed local reuse makes the
new path cost ZERO network when the bytes are still local.

## Evidence verified against code
- Rename detected as delete+create by the rescan: `internal/reconcile/engine.go:883-924`
  (`rescan` stamps a create for the new path and `SetDeleted` for the now-absent old path).
- Creates-before-deletes ordering on the wire: `internal/reconcile/broadcast.go:42-49`
  `orderCreatesBeforeDeletes` (live leaves sort before tombstones), applied in
  `broadcast.go:56-78` `broadcastUpdate`.
- Zero-network content-addressed reuse: `internal/reconcile/transfer.go:180-201`
  `localSource` (finds an on-disk file with the same content_hash, re-hashes it on disk to
  avoid a stale-index copy), used by `materialise` before any wire fetch
  (`transfer.go:229-245`).

## Evidence verified against tests (all PASS under `-race`)
- `internal/reconcile/reconcile_test.go:508` `TestRename_NoNetworkTransfer` — materialising
  the same content at a new path is satisfied locally: asserts `fc.count(MsgRequest) == 0`
  and the new path holds the original bytes.
- `:600` `TestRescan_DetectsRenameAsDeleteCreate` — renaming `a/`→`b/` on disk ⇒ old path
  tombstoned, new path created with the original content hash.
- Integration `test/integration/sync_test.go:160` `TestRename_PropagatesNoLoss` — two live
  engines converge to the new path holding the payload, old path gone on BOTH (no loss).

## Run-log corroboration
- `docs/audit/runs/scenario-rename.log:8` `--- PASS: TestRename_PropagatesNoLoss`;
  `docs/audit/runs/race-all.log:9,11`; fresh 2026-06-29 run all `--- PASS`.

## Skeptical check
The "zero network" claim is the only non-trivial one and it is asserted directly
(`MsgRequest == 0`), with `localSource` re-hashing the candidate on disk so a stale index
cannot feed wrong bytes. Create-before-delete ordering is enforced and the integration test
confirms no transient loss. No new wire type was added (catalogue stays at 7). No gap found.

---

## Phase 7 resolution (round 1) — verdict REVISED: the refutation was correct; now fixed

Both skeptics (`PR-5-skeptic1.md`, `votes/PR-5-skeptic2.md`) REFUTED this FIXED verdict,
and **they were right**. The reviewer's "Skeptical check" accepted the zero-network claim
on the strength of `TestRename_NoNetworkTransfer`, which is a synthetic direct
`materialise` that keeps the old file alive and never applies the tombstone — it proves
`localSource` works in isolation, not that a real cross-peer rename is zero-network. On the
actual receiver path the wire `orderCreatesBeforeDeletes` is discarded (`onIndexUpdate`
patches an unordered map then runs ONE whole-tree `merkle.Diff`, which is path-sorted), and
a tombstone's `os.Remove` is synchronous on the loop while the create's fetch is async on
the puller — so the old file was destroyed before the create could reuse it, forcing a
network `REQUEST` (deterministically for new-sorts-after-old, e.g. `a.txt`→`z.txt`; racily
otherwise). The efficiency win that was the entire point of PR-5 was not delivered on the
path that matters and was untested there.

**Fix (Phase 7 round 1, commit `88d93b22fa437b7e776bd50ebddbfa5a4506571a`).** `reconcileWithPeer`
(`internal/reconcile/engine.go`) now pairs a create with a same-`content_hash` tombstone
whose old bytes are still local and enqueues ONE COUPLED puller task (reusing the PR-3
preserve→applyTomb coupling): the new path is materialised FIRST — reusing the still-present
old file via `localSource`, zero network — and the old tombstone's destructive `os.Remove`
is DEFERRED until after that copy lands. The optimisation is now ORDER-INDEPENDENT (no
lexicographic luck, no race), and is data-loss-safe even on a content-hash false match
because the coupling never drops or suppresses either half of the delete+create pair — it
only reorders execution and sources the create's bytes locally. `preserveAdvertise=false`
keeps the received create from being re-broadcast (SR-6/SR-8/PR-6).

**New evidence (all PASS under `-race`):**
- `internal/reconcile/reconcile_test.go` `TestRename_CrossPeer_ZeroNetwork_OrderIndependent`
  — drives the REAL receiver path (`onIndexUpdate`→`reconcileWithPeer`→`merkle.Diff`→pairing
  →coupled puller→`materialise`→`localSource`) and asserts `MsgRequest == 0` for `a→z`
  (the deterministic pre-fix miss), `z→a` (the pre-fix race), AND a Windows-hostile
  reserved-stem key pair. Verified to FAIL with the pairing disabled (the receiver issues
  `REQUEST`s), so it is a genuine regression catch — meeting skeptic #1 obligation #1 and
  skeptic #2 obligation (1) in substance (REQUEST frames observed pre-TLS) and (2) directly
  (the deferred `os.Remove`).
- `test/integration/sync_test.go` `TestRename_AfterSortingOrder_PropagatesNoLoss` —
  end-to-end convergence + no-loss across two live engines for the previously-mishandled
  `a.txt`→`z.txt` sort order.
- `TestRename_NoNetworkTransfer` retained (re-scoped to "localSource dedup unit in
  isolation"); `TestRename_PropagatesNoLoss` / `TestRescan_DetectsRenameAsDeleteCreate`
  still pass.

Decision: `docs/audit/decisions/phase7/PR-5-rename-zero-network-order-independence.md`.
Finding `docs/audit/findings/protocol/PR-5-rename-handling.md` Status updated to fixed by
the Phase 7 commit. **Revised verdict: FIXED** (the zero-network claim now holds, and is
tested, on the real cross-peer rename path, order-independently).
