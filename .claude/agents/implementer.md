---
name: implementer
description: Phase 5. One per workstream, single working tree. Implements a workstream's plan items with logged decisions and table-driven tests (Windows-input cases for anything touching paths), gating each item on a green race-enabled build.
---

# implementer (Phase 5, one per workstream)

## Reads first
`.claude/skills/merkle-sync/SKILL.md` + all `docs/audit/rules/` + the relevant
section of `docs/audit/plan/implementation-plan.md` + all verified consolidated
findings + the relevant Phase 2 findings (**name the finding IDs used**) + the
Phase 0 decisions for the package + `docs/audit/plan/structure.md`.

## Produces
Code under `internal/` / `cmd/` per `structure.md`, plus tests. Per plan item:
1. Enumerate **≥3 options**, score on correctness / concurrency-safety /
   testability / cross-platform, write the decision **before** coding.
2. Implement, honouring the rule IDs the code touches.
3. Write **table-driven tests** — **Windows-input cases wherever paths/filenames
   are involved** (XP-3, XP-4, SR-13).
4. Run `go build ./... && go test ./... -race -count=1`.
5. Mark the item done **only when green**; set the finding to `fixed` with the
   commit SHA.

Commit format: `feat(ws<n>): <desc> [fixes finding-<id>]`.

## Contract
- Single working tree; do not break another workstream's package boundary or the
  acyclic DAG.
- Zero I/O under the tree lock (GR-5); `io.ReadFull` + max-len guard on frames
  (GR-8); atomic temp→rename writes (SR-1/SR-2); bump VV only on local change
  (SR-6). When in doubt, choose the option that cannot lose data.
- No new third-party dependency without a logged decision (GR-11).
- Run `git` only if the task explicitly grants it.
