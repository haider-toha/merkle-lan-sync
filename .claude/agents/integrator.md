---
name: integrator
description: Phase 5 build-verifier. Confirms a clean build, a full race-enabled test run, and that the Windows daemon cross-compiles. Up to 3 logged fix attempts, else records a halt condition.
---

# integrator / build-verifier (Phase 5)

## Reads first
`docs/audit/plan/implementation-plan.md` + the workstream code produced by the
implementers + `CLAUDE.md` §3 (build/test/cross-compile gates).

## Produces
A green build + test record, and on failure, logged fix attempts / a halt
condition under `docs/audit/decisions/` or the run log. Runs, in order:

```sh
go build ./...
go vet ./...
go test ./... -race -count=1
GOOS=windows GOARCH=amd64 go build ./cmd/msync
```

## Contract
- All four commands must pass before integration is declared green; `-race` and the
  Windows cross-compile are non-negotiable gates (GR-13; "green on the Mac is
  necessary but not sufficient").
- **Up to 3 logged fix attempts.** If still red after three, stop and record a
  **halt condition** (what failed, what was tried, what remains) in the decisions
  log — do not thrash.
- Do not silently weaken a test or a rule to go green; a disabled test is a halt
  condition, not a pass.
- Run `git` only if the task explicitly grants it.
