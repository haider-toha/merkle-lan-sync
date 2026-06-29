# Decision: CI matrix refinement and the cross-platform checklist scope

- Area: phase6 / evidence-generator
- Date: 2026-06-29
- Status: accepted

## Context

`.github/workflows/ci.yml` already fans out an `ubuntu / macos / windows` matrix
that runs build + vet + `go test ./... -race`, plus a dedicated GOOS=windows
cross-compile job. Phase 6 must **refine** it — keeping the `windows-latest` job,
which is the artifact the README says closes the OS gap — and author
`docs/audit/CROSS_PLATFORM_CHECKLIST.md`, the manual Mac↔Windows steps a human
runs once against a real Windows box.

The cross-OS risks the project flagged but cannot verify from one Mac
(plan/README.md "the one thing that is NOT fully autonomous"; findings under
`docs/audit/findings/crossplatform/`): NTFS case-insensitive collisions, reserved
device names, NFD/NFC Unicode, Windows Firewall / multicast for discovery, and
`ReadDirectoryChangesW` watcher buffer overflow.

## Options for the matrix (scored: correctness / concurrency-safety / testability / cross-platform)

### A. Refine in place — keep the 3-OS matrix + windows job; add cheap drift/order guards
Keep the matrix and the dedicated cross-compile job; on every OS run build + vet +
**gofmt drift check** + `go test ./... -race -shuffle=on`; pin the toolchain to
the go.mod version; keep minimal `permissions: contents: read`.
- correctness: **high** — the full race suite runs on a real Windows runner; a
  gofmt gate catches formatting drift; `-shuffle=on` surfaces test-order coupling.
- concurrency-safety: **high** — `-race` on all three OSes is the data-race oracle.
- testability: **high** — deterministic, fast-failing, no new external services.
- cross-platform: **high** — windows-latest runs the same suite the Mac does.

### B. Replace the matrix with hand-written per-OS jobs
- More YAML, more drift, no benefit over a matrix; rejected.

### C. Add heavy tooling now (golangci-lint, coverage upload, integration-only stage)
- correctness: neutral for the OS gap; adds maintenance + external deps
  (codecov tokens etc.) that do not help close the cross-OS gap. Defer.

## Decision

**Option A.** Keep the `ubuntu-latest / macos-latest / windows-latest` matrix and
the dedicated `GOOS=windows GOARCH=amd64` cross-compile job. On every matrix OS:
checkout → setup-go (version from go.mod) → `go build ./...` → `go vet ./...` →
gofmt drift check → `go test ./... -race -shuffle=on -count=1`. Keep
`fail-fast: false` so one OS failing still reports the others, and minimal
read-only permissions. Defer golangci-lint / coverage (Option C).

## Rationale

The matrix with a real `windows-latest` runner executing the `-race` suite is
exactly the artifact the README calls "<-- closes the OS gap". The added gofmt
gate and `-shuffle=on` are near-zero-cost guards against two classes of silent rot
(formatting drift, test-order coupling) with no new dependencies. The separate
cross-compile job stays as a fast early "does Windows still build" signal even if
a `windows-latest` runner is slow to schedule.

## Checklist scope (CROSS_PLATFORM_CHECKLIST.md)

The checklist documents the steps that **cannot** be proven from one Mac and must
be run by hand once a real Windows box is on the same LAN, each tied to its
finding/rule:

1. **Case-insensitive collision** (`File.txt` vs `file.txt`) — refuse + flag, no
   clobber (XP / SR; transfer.go `noClobberConflict`, ErrCaseClobber).
2. **Reserved device names + trailing dot/space + MAX_PATH** (CON, PRN, AUX, NUL,
   COM1…, `\\?\` long paths) — pathnorm escape/round-trip
   (`findings/crossplatform/filename-legality.md`).
3. **Unicode NFD↔NFC** — a macOS-decomposed name and a Windows-composed name map
   to one canonical leaf, no duplicate/ghost file
   (`findings/crossplatform/unicode-normalization.md`, SR-13).
4. **Firewall / multicast discovery** — Windows Defender Firewall prompt; UDP
   21027 on 239.192.0.77; the two boxes discover each other
   (discovery/discovery.go DefaultGroup).
5. **Watcher overflow** — `ReadDirectoryChangesW` 64 KiB buffer overflow under a
   bulk change; the periodic rescan still converges (SR-11; fsnotify
   `ErrEventOverflow`).
6. **End-to-end convergence + conflict + deletion + atomic transfer** across the
   two OSes, mirroring the in-process scenarios on real hardware.

## Consequences

- Every push runs the full `-race` suite on a real Windows runner; formatting
  drift and test-order coupling fail CI.
- The checklist is the documented hand-off for the genuinely-manual cross-OS pass;
  "green on the Mac" remains necessary-but-not-sufficient until it is worked
  through on real hardware.
