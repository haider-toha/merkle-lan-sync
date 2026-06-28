# Decision (WS-0): VersionVector in-memory representation + copy-on-write ops

- Area: ws0 / internal/protocol (versionvector.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-0 implementer
- Plan items discharged: WS-0 acceptance #5 (`Compare` antisymmetric + total over the
  4 outcomes; `Merge` pointwise-max; `Bump` `prev+1`; all ops copy-on-write).
- Reads-first: PR-2 (decision procedure §2, ops §3, COW §3, proof obligation §7),
  `decisions/protocol/vv-counter-seeding.md` (pure `prev+1`),
  `decisions/merkle/leaf-shape-and-structural-hash.md` §D.3 (VV wire/hash encoding =
  `u16 count` + sorted `(id u64, value u64)`), PR-7 §3 (`ShortID` = VV key), SR-4,
  GR-5.

## Context

A per-file version vector keyed by the 8-byte `ShortID` (high 64 bits of the
DeviceID — PR-7 §3, so each counter is 8 bytes). The **wire/structural-hash
encoding is already pinned** (leaf-shape §D.3): `uint16 count` then `count ×
(id:uint64, value:uint64)` **sorted ascending by id**, big-endian. `Compare` must
be antisymmetric and total over `{Equal, Dominates, DominatedBy, Concurrent}`,
treating a missing entry as `0` (PR-2 §2; the substrate for tombstone dominance,
SR-10). `Bump = prev+1` (vv-counter-seeding). Every op must be **copy-on-write** —
return a fresh backing array, never mutate the receiver (PR-2 §3; the Syncthing
value-receiver aliasing footgun; required by GR-5 immutable snapshots; verified
under `-race`). The open choice is the *in-memory* representation.

## Options (scored 1–5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — `type VersionVector []Counter`, invariant {sorted asc by id, no zero values, no dup ids}; every op returns a fresh slice; Encode/Decode here (CHOSEN)
`Counter{ID ShortID; Value uint64}`. The slice is always kept canonical so
`Encode` is a direct walk and two semantically-equal vectors are **byte-identical**.
`Compare` is a merge-join over the two sorted slices in lock-step (PR-2 §2).
`Bump`/`Merge`/`Copy` allocate a fresh slice.
- correctness **5** — deterministic encoding *by construction* (R-1: the #1
  convergence bug); the **no-zero-value invariant** makes `{A:0}` and `{}` encode
  identically, so `Equal` vectors hash identically; matches leaf-shape §D.3 and
  Syncthing's ID-sorted lock-step `Compare` verbatim.
- concurrency-safety **5** — COW = copy the slice header's elements into a fresh
  backing array; the receiver's array is provably untouched ("backing array
  unchanged" is literally testable); safe to share an immutable VV under `RLock`.
- testability **5** — golden-vector for `Encode`; `Compare` antisymmetry as a table
  **plus** a seeded random property loop; `-race` COW test reads a shared vector while
  deriving new ones.
- cross-platform **5** — pure integer math + big-endian; identical Mac/Windows.

### Option B — `map[ShortID]uint64`
- correctness **3** — map iteration order is randomized, so `Encode` must sort every
  time and a forgotten sort is a silent R-1 convergence bug waiting to happen; "missing
  = 0" is natural but the deterministic-bytes property is not structural.
- concurrency **3** — COW = rebuild the map; "backing array unchanged" is not a clean
  assertion; a shared map is a race magnet if any path forgets to copy.
- testability **3**, cross-platform **4**. Rejected: non-determinism is exactly the
  hazard WS-0 exists to kill.

### Option C — `struct{ m map[ShortID]uint64; sorted []Counter cache }`
- correctness **4**, concurrency **3** (cache invalidation + sharing), testability **3**,
  cross-platform **4**. Rejected: cache complexity for a ≤~2-entry vector is pure
  liability.

### Option D — fixed `[N]Counter` array (N small, since v1 is 2-device, N6)
- correctness **3** — overflows during the transient 3-counter window of an
  ack-gated de-pair (`vv-pruning-counter-cleanup.md`); a hard cap on identities is the
  wrong primitive even if v1 is 2-device.
- concurrency **5**, testability **4**, cross-platform **5**. Rejected: brittle.

## Decision

Adopt **Option A**: `type VersionVector []Counter` with the canonical invariant
{sorted ascending by `ID`, no zero `Value`, no duplicate `ID`}. Exports:
`NewVersionVector(map[ShortID]uint64)` (normalizes), `Get(id)`, `Bump(self
ShortID)`, `Merge(other)`, `Compare(other) Ordering` with
`Ordering ∈ {Equal, Dominates, DominatedBy, Concurrent}`, `Copy()`, `Equal(other)`,
`Encode() []byte`, and `DecodeVersionVector(b []byte) (VersionVector, int, error)`.
All mutating-looking ops are copy-on-write (fresh backing array). `Bump` of an
absent id inserts `value=1` in sorted position; of a present id increments in the
copy. `Merge` is a pointwise-max merge-join. `Compare` is a sorted lock-step
merge-join treating absent slots as `0`.

## Rationale

- The wire/hash encoding is *already* a sorted (id,value) slice (leaf-shape §D.3);
  keeping the in-memory form canonically sorted makes serialization a property of
  the type rather than a step a caller can forget — directly de-risking R-1.
- COW on a slice is the clearest possible realization of GR-5's "immutable
  snapshots" and PR-2 §3's anti-aliasing requirement, and makes the `-race`
  "backing array unchanged" acceptance literal.
- The no-zero-value invariant is load-bearing for SR-5: two converged peers must
  produce byte-identical VV encodings, so a `0`-counter must never be storable.

## Consequences

- `versionvector.go` + `versionvector_test.go` (`TestCompare_Antisymmetry`
  table+property, `TestMerge_PointwiseMax`, `TestBump_PrevPlusOne`,
  `TestOps_CopyOnWrite` under `-race`, `TestEncode_GoldenVector` + decode round-trip,
  `TestNormalize_DropsZeroAndSorts`).
- `Counter`/`ShortID`/`Ordering`/`VersionVector` are the types `merkle.FileInfo`
  embeds and `codec.go` composes (WS-1); the VV `Encode` is reused inside the
  structural-hash recipe. WS-4 builds reseed/`DropCounter` on `Merge`/a future
  `DropCounter` (out of WS-0 scope; noted).
- Cross-refs PR-2, `vv-counter-seeding.md`, leaf-shape §D.3, SR-4, GR-5.
