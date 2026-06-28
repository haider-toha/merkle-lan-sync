---
name: protocol-researcher
description: Phase 2 deep researcher for the wire protocol and the hard conflict cases — version-vector comparison, conflict-copy policy, tombstone deletions and resurrection, rename handling, the sync-loop invariant, the message catalogue, and the TLS trust model.
---

# protocol-researcher (Phase 2)

## Reads first
`docs/audit/rules/` + `docs/audit/findings/synthesis/problem-space-map.md` + the
framing, transport-security, and message-type-code decisions in
`docs/audit/decisions/phase0/` + `.claude/skills/merkle-sync/SKILL.md` + the
`syncthing-bep` / `version-vectors` literature findings.

## Produces
Findings in `docs/audit/findings/protocol/` covering:
- **Version-vector comparison** to detect concurrent vs causal edits; VV growth /
  pruning as device counts rise.
- **Conflict-copy policy** — `.sync-conflict-<host>-<n>`, loser renamed never
  deleted; the **mtime-tie tiebreaker**; deterministic so both peers agree.
- **Deletion via tombstones** — how a delete propagates and is not resurrected by
  a stale peer; tombstone retention/GC.
- **Rename handling** — delete+create vs hash-match heuristic.
- **The sync-loop invariant** — only broadcast after a confirmed local change.
- **The message catalogue** — finalise the ~7 types and the `[len][type][payload]`
  framing (extend the `0x08`+ range, never renumber existing codes).

Decide & log: **TLS trust model** — trust-on-first-use device IDs (Syncthing-style)
vs plaintext (confirm/harden the Phase 0 decision).

## Contract
- Every conflict-resolution rule must be **deterministic and symmetric** — both
  peers independently reach the same outcome; state the proof obligation.
- Honour SR-6/SR-7/SR-9/SR-10/SR-12 and the framing decision; any change to the
  wire format is a logged decision and stays backward-compatible.
- Cite Syncthing BEP / version-vector sources with access dates; no memory-only
  protocol claims.
