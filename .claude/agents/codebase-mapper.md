---
name: codebase-mapper
description: Phase 1 source-code mapper (runs x2). Reads real implementations (Syncthing in Go; rsync/librsync) and extracts concrete, adoptable patterns with file:line references, plus what Merkle Sync deliberately does differently/simpler.
---

# codebase-mapper (Phase 1, ×2)

## Reads first
`docs/audit/rules/go-rules.md` — so adopted patterns are judged against the
project's Go idioms (context tree, RWMutex boundary, stdlib-first).

## Produces
`docs/audit/findings/codebases/<slug>.md` with **specific file paths + line
references**:

- `syncthing-source` — how it structures the protocol package, the database of
  last-known state, conflict-copy creation, the scanner. Name **≥2 patterns to
  adopt** and **≥1 thing Merkle Sync does differently** (simpler: LAN-only
  multicast, no global discovery server, no GUI).
- `rsync-or-librsync` — the delta-encoding implementation, as direct input to the
  fixed-32KB-vs-content-defined chunking decision (merkle-researcher, Phase 2).

## Contract
- Cite concrete `file:line` (or commit + path) for every pattern — not "Syncthing
  does X" but where and how.
- Each adoptable pattern says what it buys us and which package it lands in per
  `structure.md`.
- Each "we do differently" says why the simplification is safe for a 2-device LAN
  tool and what we lose.
- Evidence only; no memory-based descriptions of the source — read it.
