# PR-2 — Version-vector comparison: concurrent vs causal, the decision procedure, growth

- Phase / role: Phase 2 — protocol-researcher
- Severity: **high** (the comparison result drives every reconciliation branch;
  getting concurrent-vs-causal wrong is either silent data loss or a conflict storm)
- Status: fixed (WS-0 — VV Compare/Merge/Bump/encode landed + `-race` tested; see
  Implementation status below. Research finding; backs `decisions/protocol/vv-counter-seeding.md`
  and `vv-pruning-counter-cleanup.md`)
- Reads-first honoured: `sync-rules.md` (SR-4, SR-6, SR-9, SR-10),
  `findings/literature/version-vectors.md`, `findings/literature/syncthing-bep.md` §4,
  `decisions/phase0/merkle-leaf-shape.md`, SKILL §3.
- Evidence: re-verified VV semantics at
  [Version vector, Wikipedia](https://en.wikipedia.org/wiki/Version_vector) and
  [BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html) (both accessed
  2026-06-28); the `Compare`/`Update`/`Merge` source is reproduced verbatim in
  `findings/literature/version-vectors.md` §4 (`vector.go @2775f424f228`).

---

## 1. Claim

A per-file **version vector** (`map[ShortID]uint64`, a device bumps **only its own**
counter and **only on a confirmed local change**) is the *sole* source of truth for
"who is causally newer" and "did two edits diverge." Comparing two vectors yields
exactly one of four outcomes — `Equal`, `Dominates`, `DominatedBy`, `Concurrent` —
which map 1:1 onto the engine's actions. Wall-clock/mtime is **never** consulted for
ordering (SR-4); it appears only as the deterministic tiebreaker *after* the VV says
`Concurrent` (PR-3).

## 2. The decision procedure (the core)

Treat a missing device entry as `0`. For vectors `a`, `b`:

```
aGreater = ∃ d : a[d] > b[d]
bGreater = ∃ d : b[d] > a[d]

!aGreater && !bGreater  → Equal         # identical history
 aGreater && !bGreater  → Dominates     # a happened-after b  (a strictly newer)
!aGreater &&  bGreater  → DominatedBy   # b happened-after a
 aGreater &&  bGreater  → Concurrent    # neither dominates → CONFLICT
```

This is the partial order from the literature: `b` **dominates** `a` iff every slot
`a[d] ≤ b[d]` and at least one `a[d] < b[d]`; **concurrent** iff "neither `a < b` nor
`b < a`, yet vectors differ" ([Version vector, Wikipedia](https://en.wikipedia.org/wiki/Version_vector), accessed 2026-06-28).
The reference implementation walks two **ID-sorted** counter slices in lock-step and
flips to a `Concurrent` result "the moment it sees one side strictly greater in one
slot *and* the other side strictly greater in another" (`vector.go:262-329`, verbatim
in `version-vectors.md` §4.3). For a 2-device LAN tool each vector has ≤ 2 counters, so
`Compare` is effectively **O(1)** (`syncthing-bep` §11).

**Why a missing counter = 0 matters (SR-10):** a tombstone whose VV includes the
deleter's bumped counter is strictly greater (present `>0`) where a stale peer's
pre-delete VV is absent (`0`) ⇒ the tombstone **Dominates** the stale version ⇒ the
file is deleted on the stale peer, not resurrected (`version-vectors` §4.3; PR-4).

## 3. Operations and where they fire

| op | rule | when |
|---|---|---|
| `Bump(self)` | `vv[self] = prev + 1` (pure logical — `vv-counter-seeding.md`) | **only** on confirmed local authorship (SR-6); **never** on applying a received file (PR-6) |
| `Merge(a,b)` | per-device `max` | when accepting an update, so the local vector reflects received history; and in the **cold-start reseed** before a wiped device asserts authorship (`vv-counter-seeding.md`) |
| `Compare(a,b)` | the §2 procedure | every differing leaf during the diff (PR-1 step 4) |
| `DropCounter(id)` / `Compact(live)` | strip a dead device's counter, copy-on-write | **only** on explicit device removal, ack-gated (`vv-pruning-counter-cleanup.md`) |

All ops are **copy-on-write** — they return a fresh vector with its own backing array,
never mutating the receiver's shared slice. This fixes the Syncthing value-receiver
aliasing footgun (`version-vectors` §4.7 / §8 A4) and is required by GR-5 (immutable
snapshots under the `RWMutex`); verify with `-race`.

## 4. Causal vs concurrent → what the engine does

- **`Dominates` / `DominatedBy` (causal):** the dominating side wins outright; the
  dominated side applies the newer file (or tombstone). **No conflict copy.** A stale
  peer reconnecting with an old version is `DominatedBy` the newer version/tombstone,
  so it just catches up (SR-10).
- **`Equal`:** same history. If `content_hash` also matches → the apply is a no-op
  (idempotent, content-addressed — SR-3). **Anomaly guard:** `Equal` VV but *differing*
  `content_hash` must be treated as a **conflict**, never a silent overwrite — the
  defensive backstop from `vv-counter-seeding.md` (closes any residual reseed anomaly).
- **`Concurrent` AND contents differ → CONFLICT** (SR-7): keep both, lose nothing →
  PR-3 for the deterministic winner + `.sync-conflict` copy.

## 5. Why version vectors (not vector clocks, not Lamport, not mtime)

- **Lamport timestamps** give a total order but "it is not evident from Lamport
  timestamps if an event definitely occurred after another event or if the two events
  are concurrent" ([exhypothesi, *Clocks and Causality*](https://www.exhypothesi.com/clocks-and-causality/), accessed 2026-06-28) — they
  cannot *detect* concurrency, so they would force a winner on a true conflict =
  silent loss. Rejected.
- **Vector clocks** increment on *every* event (incl. message send/receive); we are
  versioning *files*, not ordering *messages*, and a per-frame bump would manufacture
  spurious causality and feed the sync loop. Rejected.
- **Version vectors** increment "only when data is modified … not on every event"
  ([exhypothesi](https://www.exhypothesi.com/clocks-and-causality/), accessed
  2026-06-28) — exactly SR-6. Chosen.
- **mtime/wall clock** for ordering is the classic data-loss trap ("last write wins"
  by timestamp silently drops data; clocks skew/step backwards — `version-vectors`
  FM-5, aphyr). SR-4 forbids it; mtime is *only* the PR-3 tiebreaker.

## 6. Growth / pruning (device counts rising)

Cost is **O(d)** per op where `d` = distinct devices that ever authored the file —
independent of file size or count (`version-vectors` §6). For Merkle Sync `d` is small
and bounded (LAN, ~2). The real risk is **not** growth but **ghost counters**: a
de-paired device's counter persists forever, creating a permanent concurrent state →
deletions can't dominate (resurrection) and conflict storms (Syncthing #10590, 8,591
conflicts; verified 2026-06-28). Mitigation is the ack-gated `DropCounter` on explicit
device removal, never blind time/size pruning (which causes FM-3 unequal-pruning false
conflicts) — fully specified in `decisions/protocol/vv-pruning-counter-cleanup.md`.

## 7. Proof obligation (determinism + symmetry)

`Compare` must be **antisymmetric in result**: `Compare(a,b) == Dominates ⟺
Compare(b,a) == DominatedBy`, `Compare(a,b) == Concurrent ⟺ Compare(b,a) ==
Concurrent`, `Equal` ⟺ `Equal`. This is what lets both peers independently reach the
*same* classification of every differing leaf from the same two vectors — the
prerequisite for the symmetric conflict resolution in PR-3. Test as a table-driven
property: for random vector pairs, assert the dual relationship holds, plus the named
cases (dominates / dominated / concurrent / equal / tombstone-dominates-absent).

## 8. Cross-references

- Decisions: `protocol/vv-counter-seeding.md` (pure `prev+1` + reseed + backstop),
  `protocol/vv-pruning-counter-cleanup.md` (ghost-counter cleanup),
  `merkle/leaf-shape-and-structural-hash.md` (VV is in the structural hash; encoding).
- Rules: SR-4 (VV is ordering truth), SR-6 (bump on local change only), SR-9/SR-10
  (tombstone dominance), SR-3 (idempotent apply), GR-5 (copy-on-write snapshots).
- Findings: PR-1 (compare runs in diff step 4), PR-3 (Concurrent → tiebreaker),
  PR-4 (tombstone dominance), PR-6 (Bump only on local authorship);
  `literature/version-vectors.md` (verbatim source + failure modes).

## Implementation status (WS-0)

**Fixed in WS-0** — commit `801d0949561e648646782b10a3d514abd0981242` on branch `feat/merkle-sync-engine`.
`internal/protocol/versionvector.go` implements the full §2 decision procedure
(`Compare` → `Equal` / `Dominates` / `DominatedBy` / `Concurrent`, a missing entry
read as 0), the §3 operations (`Bump` = pure `prev+1`, `Merge` = pointwise max,
`Get`, `Copy`, `NewVersionVector` normalisation), and the canonical sorted /
no-zero-value `Encode` + `DecodeVersionVector` grammar (leaf-shape §D.3). Every op
is copy-on-write (a fresh backing array; the receiver is never mutated).

Tests (`versionvector_test.go`, all green under `-race`):
`TestCompare_Antisymmetry` (named table + a 5000-case seeded property — the §7
proof obligation), `TestCompare_Cases` (incl. tombstone-dominates-absent, the
SR-10 substrate), `TestMerge_PointwiseMax`, `TestBump_PrevPlusOne`,
`TestOps_CopyOnWrite` + `TestOps_CopyOnWrite_Race`, `TestEncode_GoldenVector`,
`TestDecodeVersionVector_RejectsMalformed`, `TestNewVersionVector_Normalize`.

Applied in later workstreams (uses these ops; not part of this finding's core
claim): the cold-start reseed `Merge`-before-authorship and the
equal-VV/differing-content backstop in WS-4 (`apply.go` / `conflict.go`), and the
ack-gated `DropCounter` in WS-4 (`vv-pruning-counter-cleanup.md`). Representation
choice logged in
`docs/audit/decisions/ws0/versionvector-representation-and-cow-ops.md`.
