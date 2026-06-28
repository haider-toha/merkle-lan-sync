# Decision: which Syncthing version to map for the codebase finding

- Area: phase1 / codebases
- Status: decided
- Date: 2026-06-28
- Decider: codebase-mapper (syncthing-source)

## Context

The task is to map the real Syncthing Go source and cite concrete `file:line`
references. Syncthing shipped a **major version 2** whose architecture diverges
from the long-stable v1 line in exactly the area this fork cares about: the
"last-known-state database" was rewritten from a hand-rolled LevelDB key/value
store to **SQLite**, and the protobuf layer was regenerated under
`internal/gen/bep`. Which version I read therefore determines (a) whether my
`file:line` refs resolve, and (b) which architecture I describe as "what
Syncthing does today." Merkle Sync's already-logged Phase 0 decisions
(`merkle-leaf-shape.md`, `transport-security-tofu-vs-plaintext.md`) cite the
**BEP v1 spec**, so there is a real tension between "current" and "matches the
spec we already cited." Downstream agents (merkle-researcher, protocol-researcher,
implementers) will trust these refs, so the choice is consequential and logged
before writing the finding.

Evidence gathered while deciding (all accessed 2026-06-28):
- Latest stable release is **v2.1.1**, published 2026-06-02
  (`GET https://api.github.com/repos/syncthing/syncthing/releases/latest`).
- `lib/db` no longer exists; the database is `internal/db` with a SQLite backend
  (`internal/db/sqlite/*.go`) and the old LevelDB store retained only for
  migration at `internal/db/olddb/backend/leveldb_backend.go`
  (`tar -tzf` of the v2.1.1 source tarball).
- `FileInfo`, `DeviceID`, version `Vector`, scanner block hashing, and the
  case-folding filesystem are **semantically unchanged** between v1 and v2; only
  their packaging/serialization moved.

## Options (scored 1–5, 5 = best)

Axes adapted to a "which source to read" choice:
**correctness** = accuracy/currency of refs; **concurrency-safety** = how well it
shows the goroutine/lifecycle patterns our `go-rules.md` cares about;
**testability** = reproducibility of the refs (pinned, downloadable, line-stable);
**cross-platform** = completeness of the Mac/Windows normalization story.

### Option A — map latest stable **v2.1.1** (PROPOSED)
- Correctness: **5** — it is what runs in 2026; "current facts" per the autonomy
  contract.
- Concurrency-safety: **5** — the connection's reader/dispatcher/writer +
  ping goroutines and `loopWG` WaitGroup are present and clean.
- Testability: **5** — pinned annotated tag `v2.1.1` (tag object
  `e105e5b6aa9f4e625e379b4a449c02db9c78dde3`); source tarball downloaded and read
  locally, so every line number is verified, not summarized.
- Cross-platform: **5** — `nativemodel_{darwin,windows}.go`, `wireformat.go`,
  and `lib/fs/casefs.go` give the full NFC/NFD + case-fold + slash story.

### Option B — map the classic **v1.x** line (matches the BEP v1 spec already cited)
- Correctness: **3** — matches the cited spec but no longer reflects the live
  codebase (DB layer especially); risks teaching a superseded design.
- Concurrency-safety: **4** — similar patterns, slightly older.
- Testability: **4** — taggable, but I would be describing code that 2026
  Syncthing has replaced.
- Cross-platform: **5** — same normalization model.
- Verdict: good for *spec* alignment, weaker for a "current source" map. The BEP
  v1 *spec* citations in the Phase 0 decisions remain valid regardless, because
  the wire-level FileInfo semantics are unchanged; I note v1/v2 deltas inline
  instead of mapping v1 wholesale.

### Option C — map `main`/HEAD or a `v2.1.2-rc.*`
- Correctness: **3** — bleeding edge, but unreleased; refs can move under callers.
- Concurrency-safety: **5**. Cross-platform: **5**.
- Testability: **2** — a moving target; `main` line numbers rot, and an `-rc` is
  not what users run. Poor reproducibility for downstream agents.

## Decision

Adopt **Option A**: map **v2.1.1** (pinned tag), read from the downloaded source
tarball so every `file:line` is verified. Where v2 diverges from the v1 design
that the Phase 0 decisions referenced — chiefly the database — I call out the v1
equivalent inline (e.g. "v1 LevelDB → now `internal/db/sqlite`, legacy store kept
at `internal/db/olddb`"). Blob URLs are pinned to the tag
(`https://github.com/syncthing/syncthing/blob/v2.1.1/<path>#Lx-Ly`).

## Rationale

- The autonomy contract requires grounding **current (2025–2026)** facts; v2.1.1
  is the live release.
- Reading a downloaded tarball (not WebFetch summaries) makes the line numbers
  trustworthy and reproducible — the highest-value property for a map other
  agents will cite.
- The pieces Merkle Sync actually borrows (DeviceID derivation, version-vector
  semantics, conflict-copy policy, atomic write, block hashing, case/Unicode
  handling) are unchanged across the v1→v2 boundary, so currency costs us nothing
  in fidelity to the borrowed patterns while gaining accuracy about the parts we
  deliberately *don't* copy (the DB).

## Consequences

- All `file:line` refs in `docs/audit/findings/codebases/syncthing-source.md`
  are against **v2.1.1**.
- The finding explicitly contrasts Syncthing's persistent multi-device index DB
  (now SQLite) with Merkle Sync's in-memory Merkle tree, which is cleaner to
  argue against the *current* design than the old LevelDB one.
- If a later agent needs v1-exact refs to match the BEP v1 spec prose, they can
  re-pin to a `v1.*` tag; the semantic mapping in the finding still holds.
