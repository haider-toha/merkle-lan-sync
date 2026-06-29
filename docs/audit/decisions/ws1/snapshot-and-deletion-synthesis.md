# Decision: Local snapshot format + deletion-across-restart synthesis (R-5)

- Area: WS-1 / merkle (snapshot persist/load + the startup snapshot diff)
- Status: decided
- Date: 2026-06-29
- Decider: WS-1 implementer
- Consumes: MK-6 (persisted last-synced snapshot is required to detect
  deletions-while-down), CDD-7.1 (distinguish locally-authored vs remotely-applied
  deletions; gate conservatively on a missing/corrupt snapshot), GR-7 (gob is
  permitted for *local* state we wrote ourselves, never for peer bytes).
- Implements plan WS-1 criterion 6 (R-5 gate) + criterion 5 (tombstone).

## Context

The in-memory tree cannot tell "deleted while the daemon was down" from "never
existed here" — both are "absent from the startup rescan" (MK-6, synthesis R-5, the
least-mitigated risk). Without a persisted last-synced snapshot to diff the rescan
against, a deletion that happened while off is missed and a peer resurrects the file
(SR-10 violation). The fix: persist a **local-only** snapshot of the FileInfo set
(with VV + `deleted`) and, on startup, synthesize tombstones for in-snapshot /
absent-on-disk paths.

## Options — serialization format (scored 1-5)

### Option A — `gob` of the FileInfo set (CHOSEN)
- Correctness **5**: GR-7 explicitly permits gob for local on-disk state we wrote
  ourselves; never decoded from a peer. Self-describing, evolves with the struct.
- Concurrency **5** · Testability **5** (round-trip) · Cross-platform **5** (the
  snapshot is local; it never crosses the OS boundary, so gob's
  non-byte-identical-across-arch property is irrelevant — it is read only by the
  same install). Chosen.

### Option B — the wire FileInfo grammar (hand-rolled) for the snapshot too
- Correctness **4** but redundant: forces the local path through the
  trust-boundary-hardened decoder for no benefit; couples local durability to the
  wire ABI. Rejected (use the wire grammar only on the wire).

### Option C — JSON
- Correctness **3**: `[32]byte` hashes and `int64` mtimes serialize awkwardly /
  lossily (base64, number precision), larger, slower. Rejected.

## Options — deletion synthesis on a missing/corrupt snapshot (scored 1-5)

### Option D — synthesize deletions for every absent path even with no snapshot
- Correctness **1**: with no baseline, *every* not-yet-created file looks deleted →
  mass false tombstones on first run / after a snapshot loss. Rejected (this is the
  mass-delete-empty-scan antipattern).

### Option E — conservative create-only when snapshot missing/corrupt (CHOSEN)
- Correctness **5**: no snapshot ⇒ absence is genuinely ambiguous ⇒ synthesize **no**
  deletions; treat the rescan as create-only and let the normal VV/tombstone
  exchange with the peer converge. Logged. This is MK-6 step 3 / CDD-7.1. Chosen.

## Decision

1. **Format: gob** (`SaveSnapshot(path, []FileInfo)` / `LoadSnapshot(path)`),
   wrapped in a small versioned header `snapshot{Version uint32; Files []FileInfo}`
   so a future format change is detectable. Written **atomically** (temp in the same
   dir → `Sync` → `os.Rename`) even though it is local — SR-1 hygiene means a crash
   mid-write never corrupts the last good snapshot. A missing file → `(nil, nil)`; a
   corrupt/unknown-version file → a typed error the caller treats as "no snapshot".

2. **`SynthesizeDeletions(prev, cur []FileInfo, self ShortID) []FileInfo`:**
   - `prev == nil` (missing/corrupt) ⇒ return `cur` unchanged (create-only, Option E).
   - For a path in `prev` (and not already a tombstone) that is **absent** from `cur`
     ⇒ append a **synthesized tombstone**: `SetDeleted` on the snapshot's FileInfo,
     which zeroes content, flips `deleted`, and **bumps `self`'s VV counter** (a
     delete is a versioned local-authorship event, SR-9). This is marked
     locally-authored (CDD-7.1: a restart must not re-stamp a *peer*-authored
     tombstone as local — a tombstone already in `prev` is carried forward
     unchanged, never re-bumped).
   - A path present in both is returned from `cur` as-is (the reconcile layer, WS-4,
     bumps the VV on a confirmed content change — SR-6; WS-1 does not bump on edit).

3. **`FileInfo.SetDeleted(self ShortID) FileInfo`** (criterion 5): returns a copy
   with `ContentHash` zeroed, `Size = 0`, `Deleted = true`, `Version =
   Version.Bump(self)`. The flipped `deleted` byte + bumped VV make the tombstone's
   structural hash distinct from the pre-delete leaf, so the deletion shows in the
   diff and changes the root.

## Rationale

- gob is the GR-7-sanctioned, lowest-friction choice for purely local durability;
  the wire-grammar and JSON alternatives add cost or coupling with no benefit.
- Conservative create-only on a missing snapshot is the only choice that does not
  manufacture mass deletions on first run / after snapshot loss, while a present
  snapshot still closes R-5.
- Carrying an existing tombstone forward unchanged (no re-bump) is the CDD-7.1
  guard against a restart turning a pending *remote* tombstone into a *local*
  concurrent one.

## Consequences

- Drives `internal/merkle/{snapshot.go, fileinfo.go (SetDeleted)}`.
- Tests: `TestSnapshot_RoundTrip`, `TestSnapshot_AtomicAndMissing`,
  `TestSnapshotDiff_SynthesizesDeletion`, `TestSnapshotMissing_CreateOnly`,
  `TestSetDeleted_BumpsAndZeroes`, `TestTombstone_DistinctHash`.
- WS-4 calls `LoadSnapshot` + `SynthesizeDeletions` at startup before the first
  peer exchange, and `SaveSnapshot` on clean shutdown / periodically.
- The kill-9-between-snapshots and wipe-recovery scenarios are WS-4/Phase-6
  (CDD-7.2/.3).
- Cross-refs: SR-9/10/11, GR-7; MK-6; CDD-7.1.
