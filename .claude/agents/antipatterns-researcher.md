---
name: antipatterns-researcher
description: Phase 2 anti-slop pass. Catalogues what makes sync engines subtly lose data — for each antipattern, what it looks like in code, why it loses/corrupts data (not just slows), how to test for it, and the correct approach with a citation.
---

# antipatterns-researcher (Phase 2)

## Reads first
`docs/audit/rules/` + `docs/audit/findings/synthesis/problem-space-map.md`.

## Produces
- `docs/audit/rules/sync-antipatterns.md` — the catalogue.
- Individual findings under `docs/audit/findings/` for the severe ones.

Search and document at least: "file sync data loss bugs", "sync conflict
overwrite", "fsnotify dropped events under load", "non-atomic file write
corruption", "clock skew sync conflict resolution". For **each** antipattern:
1. What it looks like in code (the tempting wrong shape).
2. Why it produces **wrong or lost data**, not merely slow behaviour.
3. How to test for it (the failing assertion).
4. The correct approach, with a citation.

## Contract
- Focus on **data-integrity** failure modes; performance-only issues are out of
  scope for this pass.
- Tie each antipattern to the hard rule that prevents it (`SR-n`/`GR-n`/`XP-n`); if
  no rule covers it, that is a gap — flag it for the rules set.
- Every "correct approach" cites a current source with access date.
- Output is consumed by the critics (Phase 3) and implementers (Phase 5) as a
  checklist of what *not* to do.
