# Decision: Merkle leaf shape (per-file FileInfo)

- Area: phase0 / merkle (also tagged ws1 per roster)
- Status: decided (Phase 0 baseline — merkle-researcher hardens the exact
  serialization and the version-vector encoding in Phase 2)
- Date: 2026-06-28
- Decider: rules-architect

## Context

The Merkle tree is "the source of truth for what differs" between two peers. A
naive Merkle tree leaf is just a content hash: walk both trees, recurse into
mismatching branches, and where the leaf hashes differ you know the files
differ. That is enough for a **one-way mirror**, but Merkle Sync is **two-way**.
A bare content hash tells you *that* two files differ; it cannot tell you:

- **who is newer / which edit causally precedes which** (needed to pick a winner
  without a clock you can trust);
- **whether the files truly conflict (concurrent edits) or one simply hasn't
  received the other's update yet** (causal vs concurrent);
- **whether a file was deleted** (absence is ambiguous: "deleted here" vs "not
  yet created here" vs "deleted there and must be resurrected here?");
- **executable bit / permissions** changes that carry no content change.

So the leaf must carry metadata, and *which* metadata is a consequential,
log-first choice because it constrains the entire reconciliation algorithm and
the wire index format. The roster pre-commits the field list
(`hash+size+mode+mtime+version-vector+tombstone`); this decision validates that
list against alternatives and pins exactly what each field is for and how it
participates in the tree hash.

## Options (scored 1–5, 5 = best)

### Option A — bare content hash only

- Correctness (two-way sync): **1** — cannot express deletions, cannot order
  edits, cannot distinguish concurrent from causal. Only correct for one-way
  mirroring. Rejected.
- Concurrency-safety: **5** (trivial). Testability: **5**. Cross-platform: **4**.

### Option B — hash + size + mtime (rsync / basic-mirror style)

- Correctness: **2** — size+mtime is a fast "probably changed" heuristic, but
  mtime is not a reliable global ordering across machines (clock skew) and there
  is still no deletion or concurrency model. Wall-clock comparison across hosts
  is a known sync-bug source. Rejected as the primary identity.

### Option C — hash + size + mode + mtime + **version vector** + **tombstone** (PROPOSED)

A per-path `FileInfo`:

| field | type | purpose |
|---|---|---|
| `path` | canonical forward-slash relative string | identity / tree position (see crossplatform-rules) |
| `content_hash` | 32-byte SHA-256 of file bytes (dirs: derived from children) | transfer key, dedup, "do the bytes differ" |
| `size` | uint64 | cheap mismatch reject + transfer planning |
| `mode` | uint32 (perm bits + type) | executable bit / file-vs-dir / symlink; *preliminary on Windows — confirm in Phase 2* |
| `mtime` | int64 ns | **conflict tiebreaker only**, never the source of truth for ordering |
| `version_vector` | map[deviceID]counter | causal ordering: detect concurrent vs happens-before |
| `deleted` (tombstone) | bool + the VV at delete time | a delete is a versioned event, not an absence |

- Correctness: **5** — version vectors "allow the participants to determine if
  one update preceded another (happened-before), followed it, or if the two
  updates happened concurrently (and therefore might conflict)"
  ([Version vector, Wikipedia](https://en.wikipedia.org/wiki/Version_vector), accessed 2026-06-28). Tombstones make deletion a
  first-class, propagatable, resurrection-resistant event (see SR-9/SR-10 in
  sync-rules.md and the tombstone citations there). This mirrors the per-file
  versioned metadata Syncthing's Block Exchange Protocol exchanges via its
  INDEX / INDEX_UPDATE messages (one of eight BEP message types over a
  length-prefixed wire) ([Syncthing BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html), accessed 2026-06-28); per the
  roster's literature track, BEP's FileInfo is the reference the Phase 1
  syncthing-bep finding will document in detail.
- Concurrency-safety: **5** — `FileInfo` is an immutable value snapshot taken
  under the scanner's lock; readers copy it. No shared mutability in the leaf.
- Testability: **5** — pure-data struct; table-driven tests for VV comparison
  (dominates / dominated / concurrent), tombstone propagation, mode round-trip,
  and "one byte changed flips exactly this leaf + its ancestor hashes."
- Cross-platform: **4** — `mode` and `mtime` semantics differ Mac↔Windows
  (executable bit, ns precision); flagged preliminary, handled by pathnorm +
  crossplatform-researcher in Phase 2.

### Option D — full per-file CRDT (op-based log per file)

- Correctness: **5** but **overkill**: an op-log per file is heavier than LAN
  file sync needs, complicates the tree hash, and balloons state. Version
  vectors give the causality we need at a fraction of the cost. Deferred /
  rejected for scope.

## Decision

Adopt **Option C**: the leaf is a `FileInfo{path, content_hash, size, mode,
mtime, version_vector, deleted}`. Two refinements pinned now:

1. **`content_hash` is pure file bytes** (SHA-256), independent of metadata, so
   it doubles as the transfer/dedup key and answers "do the bytes differ."
   Directory nodes get a hash derived from a canonical serialization of their
   children (see below), giving the git-tree / Merkle property: a change to any
   leaf changes that leaf's hash and every ancestor up to the root, enabling the
   O(log n) "recurse only into mismatching branches" diff.

2. **What the structural (tree) hash commits to** — this is the subtle part and
   is pinned here to keep the convergence invariant sound:
   - **Included** in the node/structural hash: child `name` (canonical),
     `content_hash`, `mode`, `deleted` flag, and `version_vector`.
   - **Excluded** from the structural hash: raw `mtime` and `size`. `mtime` is
     volatile and differs across machines without a meaningful change, so
     hashing it would manufacture spurious whole-tree diffs; it is retained in
     `FileInfo` purely as a conflict tiebreaker. `size` is redundant with
     `content_hash` for difference detection.

   Consequence: after two peers fully converge they hold identical `FileInfo`
   sets with identical version vectors and tombstones, so their **root hashes are
   bit-identical** — which is exactly the eventual-consistency oracle the
   flow-verifier checks ("after a change settles, both trees expose the identical
   root hash"). Including the version vector in the structural hash is what makes
   "converged ⇔ equal root hash" true even for files whose bytes match but whose
   history differed (and for tombstones, whose bytes are absent).

## Rationale

- Directly satisfies the two-way-sync requirements the bare hash cannot:
  causal ordering (VV), deletion (tombstone), permission changes (mode), and a
  deterministic conflict tiebreak (mtime, then device-ID, mirroring Syncthing —
  see SR-7).
- Keeps content hashing and structural hashing **separate concerns**, which lets
  the transfer layer dedup by `content_hash` while the diff layer reasons over
  full `FileInfo`. This separation is what prevents the two classic failures:
  (a) mtime jitter causing endless false diffs, and (b) deletions/permission
  changes being invisible to the tree.
- Matches the well-trodden Syncthing FileInfo model the literature track will
  document, so we inherit known-good semantics rather than inventing them.

## Consequences

- Drives the `internal/merkle` types (`FileInfo`, `Node`, `Tree`) and the
  `internal/protocol` index message shape (INDEX / INDEX_UPDATE carry
  `FileInfo`s).
- Hands two sub-decisions to Phase 2 (logged as open):
  - **Exact canonical serialization** of a `FileInfo` for hashing (field order,
    length-prefixing of the path, VV encoding) — merkle-researcher. Must be
    byte-deterministic and identical on Mac/Windows (forward-slash paths, fixed
    integer widths, big-endian) so the same tree hashes the same on both OSes.
  - **Version-vector pruning / compaction** as device counts grow (bounded for a
    LAN 2-device tool, but document the growth) — protocol-researcher.
  - **Chunking** (fixed 32 KiB vs content-defined) is *not* part of the leaf
    shape; it lives under `content_hash`'s transfer story and is deferred to the
    merkle/reconcile workstream.
- Cross-references sync-rules.md SR-6 (broadcast a hash only after a confirmed
  local change — the VV is bumped on local change, never on a received apply),
  SR-7 (conflict tiebreak), SR-9/SR-10 (tombstones), and crossplatform-rules.md
  (canonical paths, mode/mtime caveats).
