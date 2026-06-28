---
name: evidence-generator
description: Phase 6 — the key difference from CAIM. Spins up TWO msync instances on the Mac (two folders, loopback) and runs the convergence/conflict/deletion/rename/killed-transfer/large-file scenarios, then emits the two cross-OS artifacts (CI matrix + manual checklist).
---

# evidence-generator (Phase 6)

## Reads first
`docs/audit/plan/implementation-plan.md` (acceptance criteria) +
`.claude/skills/merkle-sync/SKILL.md` + `docs/audit/plan/structure.md` +
`test/integration/` helpers.

## Produces
- **Scenario evidence** in `docs/audit/runs/` (gitignored): spin up **two**
  in-process `msync` instances on loopback with temp dirs and run —
  convergence · conflict (no data loss) · deletion (tombstone, no resurrection) ·
  rename · killed-transfer (no corrupt file) · large-file. Capture logs.
- **Windows cross-compile check** (`GOOS=windows GOARCH=amd64 go build ./cmd/msync`).
- **The two cross-OS artifacts** that close the gap the Mac cannot:
  - `.github/workflows/ci.yml` — ubuntu/macos/**windows-latest** matrix running the
    suite (refine the existing skeleton).
  - `docs/audit/CROSS_PLATFORM_CHECKLIST.md` — the manual Mac↔Windows steps a human
    runs against a real Windows box.

## Contract
- It is a **two-instance** oracle, not a single process — convergence is only
  meaningful between two peers (SR-5).
- Each scenario asserts a named invariant (`SR-n`) and writes pass/fail evidence a
  reviewer + skeptics can re-check; no "looks converged".
- Explicitly tag every check that the Mac **cannot** prove and route it to the CI
  windows job + the manual checklist.
