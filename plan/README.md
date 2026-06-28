Merkle Sync Workflow — How to Run
Forked from the CAIM autonomous-workflow framework. Same 7-phase pipeline (research → adversarial design → parallel implementation → adversarial verification → PR), retargeted at Merkle Sync: a decentralised file-sync engine in Go that keeps a folder mirrored between a Mac and a Windows machine over the local network — no central server, raw TCP + UDP multicast

Merkle trees.
What this does
Autonomous 7-phase pipeline that:

Researches prior art (Syncthing's Block Exchange Protocol, rsync's delta algorithm, Merkle tree / git tree model, version vectors) — papers + real Go source.
Designs the Go architecture (4 layers: discovery, state, transport, reconciliation) with adversarial critique.
Implements in ordered workstreams (Go 1.22+, fsnotify, crypto/tls, table-driven tests).
Verifies correctness — sync convergence, conflict handling without data loss, atomic interrupted-transfer recovery, no sync loops — plus a cross-platform CI matrix.
Loops until findings are resolved.
Opens a PR with a full audit trail.
Every decision is logged to docs/audit/decisions/ before it's acted on.

⚠️ The one thing that is NOT fully autonomous
You're writing this on a Mac, but the requirement is Mac↔Windows sync. Real cross-OS behaviour cannot be verified from one machine. Here's the honest split:

Verifiable autonomously on the Mac	Needs a real Windows target
Protocol correctness (two local instances converge, handle conflicts, recover from killed transfers)	NTFS case-insensitive collisions (File.txt vs file.txt)
GOOS=windows GOARCH=amd64 go build compiles clean	Windows Firewall / multicast actually allowing discovery
Path-normalisation logic via table-driven tests with Windows-style inputs	ReadDirectoryChangesW buffer-overflow event drops vs macOS FSEvents coalescing
Unicode NFC/NFD normalisation unit tests	Reserved names (CON, PRN, AUX), trailing dots/spaces, MAX_PATH
So Phase 6 emits two things for the cross-OS gap: a GitHub Actions matrix (ubuntu / macos / windows) that runs the suite on a real Windows runner, and a docs/audit/CROSS_PLATFORM_CHECKLIST.md you run by hand once against a real Windows box. Treat "green on the Mac" as necessary but not sufficient.

Prerequisites
# Claude Code CLI (recent version — the Workflow tool must be available)
claude --version

# Go toolchain (1.22 or newer)
go version

# Linters / vetting
go install golang.org/x/tools/cmd/goimports@latest
# golangci-lint (brew or go install)
brew install golangci-lint

# GitHub CLI (for PR creation at the end)
brew install gh
gh auth login

# A second machine on the SAME LAN for real cross-platform testing.
# Ideally a Windows box (or a Windows VM with bridged networking).
# Without one, the workflow still runs — it just can't close the cross-OS gap.
Setup
mkdir merkle-sync && cd merkle-sync
git init
git remote add origin <your-repo-url>   # or skip if local only

# Drop the workflow script into the repo
mkdir -p plan
cp merkle-sync.workflow.js plan/
Run
This is a Claude Code Workflow-tool script, not a file you pass to the CLI (claude file.js does not execute JS — same lesson as CAIM). You ask Claude Code to run it inside a session.

# In a Claude Code session:
"run the merkle-sync workflow at plan/merkle-sync.workflow.js"
It invokes the Workflow tool with a scriptPath. Control scope via args:

// Pilot — research only, stops after Phase 2. THIS IS THE DEFAULT.
{ "pilot": true, "repoDir": "merkle-sync" }

// Full — design + implement + verify + PR.
{ "full": true, "repoDir": "merkle-sync", "remote": "<git-url-or-null>" }
Fail-safe default is research-only. The expensive design+build+verify phases (3–7) run only with explicit { full: true }. If args fail to thread through, the small safe run happens — never an unsupervised full build.

For a true walk-away run, start the session with --dangerously-skip-permissions so it doesn't pause for per-action approval:

claude --dangerously-skip-permissions
# then: "run the merkle-sync workflow at plan/merkle-sync.workflow.js with { full: true }"
Do not interact with it while running. The autonomy contract handles every decision.

What gets produced
docs/audit/
  decisions/                  # Every decision, logged before it was acted on
  findings/
    literature/               # Syncthing BEP, rsync, Merkle tree, version vectors
    codebases/                # Syncthing source + rsync/librsync analysis
    merkle/                   # tree construction, diff algorithm, chunking findings
    protocol/                 # version vectors, conflict resolution, deletions, framing
    crossplatform/            # Windows<->Mac filename / Unicode / watcher findings
    design/                   # architecture critique findings (+ skeptic votes)
    review/                   # post-implementation correctness verdicts
  plan/
    structure.md              # proposed file layout
    implementation-plan.md    # ordered workstreams with acceptance criteria
  runs/                       # scenario logs (gitignored)
  CROSS_PLATFORM_CHECKLIST.md # manual Mac<->Windows test steps
  SUMMARY.md                  # final report

internal/
  merkle/        # tree + node + scanner (metadata-rich leaves, not just a hash)
  pathnorm/      # cross-platform path normalisation (the Mac<->Windows layer)
  protocol/      # binary message framing + version-vector types
  transport/     # TCP framing + TLS (trust-on-first-use device identity)
  discovery/     # UDP multicast peer registry + heartbeat eviction
  reconcile/     # tree diff + 32KB chunk streaming + conflict copies + atomic rename
cmd/
  msync/         # main entrypoint (daemon: watch a folder, sync with peers)
test/
  integration/   # two-instance, same-machine sync scenarios

.github/workflows/
  ci.yml         # cross-platform matrix: ubuntu / macos / windows  <-- closes the OS gap

.claude/
  agents/        # contract file per agent role
  skills/merkle-sync/SKILL.md   # Merkle diff algo, version-vector scheme, framing spec, path rules
CLAUDE.md        # agent contract (hard rules)
go.mod
Phase graph
Pre-flight  (go mod init, dirs, branch, stub cmd/msync + CI skeleton)
    │
Phase 0 — rules-architect (WebSearch grounded: Go idioms, concurrency, fsnotify, framing)
    │
┌───┴───────────────────────────────┐
Phase 1                            Phase 2 (parallel)
problem-space map                  ├── merkle-researcher
├── literature-mapper xN           ├── protocol-researcher
├── codebase-mapper x2 (Syncthing) └── crossplatform-researcher   ← elevated track
└── synthesizer                    └── antipatterns-researcher
└───┬───────────────────────────────┘
    │ barrier
Phase 3 — design critique → adversarial vote (3 skeptics per finding) → consolidate
    │
Phase 4 — planner (ordered workstreams)
    │
Phase 5 — implementation (sequential, single tree)
    │  WS-1: Merkle tree + scanner + pathnorm   (foundational)
    │  WS-2: Transport (TCP framing + TLS)       ┐ parallel after WS-1
    │  WS-3: Discovery (UDP multicast registry)  ┘
    │  WS-4: Reconciliation (diff + chunk stream + conflict resolution)  (after WS-2)
    │  Build + cross-compile (GOOS=windows) verify
    │
Phase 6 — correctness review
    │  evidence-generator (two local instances run sync scenarios)
    │  reviewer + 3 skeptics per fixed finding
    │  flow-verifier (eventual-consistency invariants)
    │  → emits CI matrix + CROSS_PLATFORM_CHECKLIST.md
    │
Phase 7 — fix → review loop (until clean or budget/stuck)
    │
Final summary + PR
Halt conditions
The workflow stops early only if:

Claude API budget hits 80% (remaining work itemised in the decisions log).
Two Phase 6 rounds with zero progress (stuck, blockers logged).
Build/integration cannot be made green after 3 logged attempts.
In all cases, docs/audit/decisions/ says exactly what happened and what's left.

After it finishes
# Check the PR + audit trail
gh pr view
cat docs/audit/SUMMARY.md
ls docs/audit/findings/

# Build + test on the Mac
go build ./...
go test ./... -race

# Confirm it cross-compiles for Windows
GOOS=windows GOARCH=amd64 go build ./cmd/msync

# Run the two-instance local sync demo (proves protocol convergence)
go test ./test/integration -run TestTwoNodeConverge -v

# THEN: do the real cross-OS pass
#   1. push the branch so CI runs the windows-latest job
#   2. work through docs/audit/CROSS_PLATFORM_CHECKLIST.md on a real Windows box
Key decisions the workflow makes autonomously
Things you'd normally decide, handled with logged rationale:

Decision	Where logged
Tree leaf metadata: hash + size + mode + mtime + version vector + deleted tombstone	decisions/ws1/
Chunking: fixed 32KB vs content-defined (rolling hash / FastCDC)	decisions/phase2/
Conflict resolution policy: .sync-conflict-<host>-<n> copy, never clobber	decisions/phase2/
Conflict tiebreaker when mtimes equal (host-id ordering, Syncthing-style)	decisions/phase2/
Deletion handling: tombstones + how long to retain them	decisions/phase2/
Rename detection (treat as delete+create, or hash-match heuristic)	decisions/ws1/
Watcher trust model: events as hints + periodic full rescan as source of truth	decisions/ws1/
Path normalisation: Unicode NFC vs NFD, case folding, illegal-char escaping	decisions/crossplatform/
Transport security: TLS trust-on-first-use device IDs vs plaintext	decisions/ws2/
Discovery: multicast interval + heartbeat eviction timeout	decisions/ws3/
Sync-loop invariant: only broadcast hash after a local change	decisions/ws4/
Global (cross-subnet) discovery / relay — likely deferred	decisions/phase4/
Extending after the run
CLAUDE.md has the full checklist. To add a new message type or sync feature:

1. Write a finding in docs/audit/findings/ describing what it needs + why.
2. Add the message type in internal/protocol/ (keep framing backward-compatible).
3. Implement the handler in the relevant internal/ package.
4. Add a table-driven test; if it touches paths, add Windows-input cases.
5. Add a two-instance scenario in test/integration/ proving convergence.
6. Confirm GOOS=windows build still passes + update the CI matrix if needed.
Stack
Go · net (TCP/UDP) · crypto/sha256 · crypto/tls · encoding/binary (custom framing) · sync (RWMutex separating watcher-writes from sync-reads) · fsnotify (cross-platform file watching, treated as advisory).