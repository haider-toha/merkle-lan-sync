# Decision (WS-4): tombstone propagation + ack-gated GC, rename = delete+create, filesystem-verdict no-clobber

- Area: ws4 / internal/reconcile (tombstone.go, apply.go, transfer.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-4 implementer
- Plan items: WS-4 #6 (deletion propagates; stale peer cannot resurrect; ack-gated
  GC; premature-GC negative test), #7 (no-clobber by filesystem verdict), #8 (rename =
  lossless delete+create, zero needless transfer).
- Reads-first: PR-4 (tombstone = `SetDeleted`; bumped VV dominates a stale absent
  counter; ack-gated retention), `decisions/protocol/tombstone-retention-gc.md`
  (Option A — retain until the peer's advertised VV for the path dominates-or-equals
  the tombstone's, then GC symmetrically; never on a timer; fall back to retain if no
  peer), `decisions/protocol/vv-pruning-counter-cleanup.md` (`DropCounter` ack-gated,
  device-removal only — scoped to "un-pair the last peer" for v1, N6), CDD-7.2/.3,
  PR-5 (rename = delete+create, create-before-delete + content-addressed copy),
  CDD-5 + `decisions/crossplatform/case-and-normalization-collision-policy.md`
  (refuse+flag by the filesystem's own verdict), `merkle.SetDeleted`,
  `pathnorm.Fold`/`FromOSPath`.

## Context

Three coupled lifecycle questions on the apply path: (a) how a deletion propagates and
is GC'd without resurrecting (SR-9/SR-10); (b) how a rename moves bytes losslessly with
zero needless transfer (PR-5); (c) how to refuse a case/normalisation clobber by the
filesystem's actual verdict, not the engine's fold (CDD-5).

## Options — tombstone GC (scored: correctness / concurrency / testability / cross-platform)

### Option T1 — time-based TTL
- correctness **2** — a peer offline longer than the TTL reconnects with the pre-delete
  file and resurrects it (SR-10 broken). Rejected (tombstone-retention-gc Option B).

### Option T2 — never GC
- correctness **5**, but state grows unbounded. Kept only as the **fallback when no
  peer is paired** (tombstone-retention-gc Option C).

### Option T3 — ack-gated symmetric GC (CHOSEN)
Retain a tombstone until the peer's advertised index shows, for that path, a VV that
**dominates-or-equals** the tombstone's VV (the peer has applied the delete); only then
may both peers GC it. Evaluated by the single writer under the RWMutex from
already-exchanged index state (no new wire round-trip). If no peer is currently known,
retain (T2 fallback) — never a TTL.
- correctness **5** — GC strictly after the deletion is durably replicated ⇒
  resurrection impossible by construction; bounded retention; symmetric ⇒ no FM-3
  unequal-state false conflict.
- concurrency-safety **5** — pure check under the writer lock. testability **5** —
  `TestTombstone_NoResurrection` + a **premature-GC negative** test (`canGC` forced
  true before the ack ⇒ resurrection occurs, proving the gate is load-bearing).
- cross-platform **5**. `canGC(tombstone, peerIndex) bool` and `applyTombstone` are
  pure/testable.

## Options — rename

### Option N1 — dedicated `MOVE` message / hash-match detection
- correctness **4** but new wire surface + false-match risk; **deferred** to a future
  `0x08+` type (PR-5 §4, message-type-enumeration). Not v1.

### Option N2 — emergent delete+create, create-before-delete + content-addressed (CHOSEN)
A rename is observed by the scanner as old-path-absent (⇒ synthesized tombstone via
`SetDeleted`, bumped VV) + new-path-present (⇒ create). The broadcast orders **creates
before deletes** so a peer never transiently deletes the only copy; and the puller does
**local content-addressed reuse** — the new path's `content_hash` equals the old's, so
the peer materialises it by copying the still-present old file locally ⇒ **zero network
transfer** (assert no `REQUEST` for that hash). The old path is left a non-resurrecting
tombstone. A directory rename is the same per-file (the subtree reparents emergently).
- correctness **5** — composes from proven primitives (PR-4 tombstone + create); no new
  code paths; convergence unaffected (SR-5).
- concurrency-safety **5**, testability **5** (`TestRename_NoNetworkTransfer`,
  `TestDirRename_SubtreeReparents`), cross-platform **5** (content-addressed by hash,
  separator-agnostic).

## Options — no-clobber (CDD-5)

### Option C1 — trust the engine's in-memory fold index only
- correctness **3** — `SimpleFold(NFC)` is not provably equal to NTFS `$UpCase`/APFS;
  a fold miss then clobbers. Rejected as the *safety* mechanism.

### Option C2 — refuse+flag by the filesystem's own verdict (directory listing) (CHOSEN)
Before `os.Rename(tmp, dst)`, on a **case-insensitive target** (startup probe), list
the real parent directory; if an existing entry canonicalises to a **different**
canonical key that is **fold-equal** to the target key, **refuse + flag** (return a
typed `ErrCaseClobber`, leave the temp discarded, never rename over it). The fold is a
detection optimisation that errs toward over-refuse (the safe direction); the real
directory listing is the filesystem's own namespace verdict, so a fold mismatch or
mis-probe **fails safe to refuse, never to clobber**. On a case-sensitive target the
two keys coexist and no refusal occurs.
- correctness **5** — never clobbers; SR-7 no-data-loss spirit + XP-4.
- concurrency-safety **5** (listing happens in the per-transfer commit, off the lock;
  the flag is recorded under the lock). testability **5** —
  `TestApply_RefusesCaseClobber` runs the APFS case-insensitive variant on the Mac;
  the NTFS `$UpCase` matrix → Phase 6. cross-platform **5** (per-directory/per-volume
  by construction).

## Decision

**T3 + N2 + C2.** Deletions are `SetDeleted` tombstones (bumped VV) that dominate stale
absent counters (SR-10); they are **retained until the peer acks** (its advertised VV
for the path dominates-or-equals the tombstone's) and then **GC'd symmetrically**,
never on a timer, falling back to retain when no peer is known. `DropCounter` is
provided ack-gated, device-removal-only, scoped to un-pairing the last peer for v1.
Renames are emergent **delete+create**, broadcast **creates-before-deletes**, with
**local content-addressed reuse** giving zero network transfer. No-clobber is enforced
by a pre-rename **directory-listing verdict** on case-insensitive targets (refuse+flag,
typed `ErrCaseClobber`), with the engine fold as a safe-direction detection aid only.

## Rationale

- Ack-gating ties GC to the *exact* SR-10 safety condition (no live peer can still
  carry a pre-delete version) rather than a proxy (time), at zero extra wire cost — the
  signal is already in the peer's index.
- Delete+create needs nothing new and is lossless; content-addressing defangs its only
  cost (a re-send) to zero on the common still-on-disk rename.
- Letting the filesystem's own directory listing be the clobber verdict makes the
  guarantee independent of whether our fold equals `$UpCase`/APFS — it fails safe.

## Consequences

- Drives `tombstone.go` (`applyTombstone`, `canGC`, the symmetric GC sweep, `DropCounter`
  sweep over `files`+snapshot) and the apply commit path (`ErrCaseClobber` refuse+flag).
- **Tombstone-wipe v1 limitation (CDD-7.3):** a deleter wiped before propagation (sole
  tombstone destroyed) is an accepted v1 limitation (a zero-replica delete is
  unrecoverable by any decentralised design); the negative test asserts the file is
  never *silently* re-adopted as a clean live file. The rejected "quarantine every
  peer-only path on reseed" remedy is **not** adopted.
- The premature-GC negative test keeps the ack-gate honest (it must resurrect when the
  gate is bypassed).
- NTFS `$UpCase` divergence + mixed-sensitivity trees + Windows non-atomic replace →
  Phase 6 (CI matrix + CROSS_PLATFORM_CHECKLIST.md).
- Cross-refs: SR-9/10, PR-4/5, CDD-5/7, tombstone-retention-gc, vv-pruning-counter-cleanup.
