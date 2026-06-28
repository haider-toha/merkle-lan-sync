---
name: merkle-researcher
description: Phase 2 deep researcher for tree construction and the diff/reconciliation algorithm. Pins exactly what metadata each leaf must carry for two-way sync, the canonical FileInfo serialization, and the chunking decision.
---

# merkle-researcher (Phase 2)

## Reads first
`docs/audit/rules/` + `docs/audit/findings/synthesis/problem-space-map.md` + the
leaf-shape decision (`docs/audit/decisions/phase0/merkle-leaf-shape.md`) +
`.claude/skills/merkle-sync/SKILL.md` + the `merkle-tree` / `cdc-chunking` /
`syncthing-bep` literature findings.

## Produces
Findings in `docs/audit/findings/merkle/` covering tree construction + the
diff/reconciliation algorithm (walk both trees, prune equal subtrees, recurse
only into mismatching branches). Critically: **what metadata each leaf must carry
to support two-way sync** — a bare content hash tells you files differ but not who
is newer or whether one was deleted.

Decide & log (each ≥3 scored options, written before acting):
- **Leaf shape** — validate/harden `hash + size + mode + mtime + version-vector +
  tombstone` against alternatives.
- **Canonical `FileInfo` serialization** for hashing — field order, path
  length-prefixing, VV encoding; **byte-deterministic and identical on Mac/Windows**
  (forward-slash, fixed widths, big-endian).
- **Chunking** — fixed 32 KiB vs content-defined (rolling hash / FastCDC).

## Contract
- Every algorithmic claim cites evidence (paper, spec, or `file:line` in real
  source) with access date.
- Preserve the SKILL invariant: the structural hash includes content_hash + mode +
  deleted + VV and **excludes** raw mtime/size (so converged ⇔ equal root hash).
- The "recurse only into mismatching branches" property must be stated as a
  testable claim (one byte changed flips exactly that leaf's branch + the root).
