---
name: literature-mapper
description: Phase 1 problem-space mapper. One instance per prior-art source (run in parallel). Extracts the core algorithm, exact data structures/formulas, failure modes, complexity, and how it maps to Merkle Sync, with citations.
---

# literature-mapper (Phase 1)

## Reads first
The rules dirs — `docs/audit/rules/{go,sync,crossplatform}-rules.md` — so each
finding is framed against the project's hard constraints.

## Produces
One finding per source in `docs/audit/findings/literature/<slug>.md`. Each finding
covers: core algorithm · exact data structures / formulas · failure modes ·
complexity · how it maps to Merkle Sync. Sources (one agent each):

| slug | what to extract |
|---|---|
| `syncthing-bep` | Block Exchange Protocol: FileInfo fields (version vector, deleted, modified_by), index exchange, how blocks are requested |
| `rsync-algorithm` | rolling-checksum + strong-hash delta transfer; why it beats whole-file copy; client/server assumption |
| `merkle-tree` | Merkle 1987 / git tree model: folder hashes from child hashes; O(log n) diff property |
| `version-vectors` | version vectors vs vector clocks vs Lamport timestamps; concurrent vs causal detection |
| `cdc-chunking` | content-defined chunking (FastCDC / rolling hash) vs fixed blocks; the "insert one byte shifts every boundary" problem |

## Contract
- One source per file; instances run in parallel.
- Every claim cites evidence — a URL with access date, or a paper / spec
  reference. No memory-only claims; ground current facts with web search.
- End each finding with an explicit "maps to Merkle Sync as…" paragraph tying the
  source to a package in `structure.md` (e.g. version-vectors → `internal/protocol`).
- Flag open modelling questions for the synthesizer / Phase 2; do not decide
  implementation here.
