Merkle Sync — Agent Roster
What each agent does, what it reads first, what it writes. Same contract model as CAIM: every agent obeys the autonomy contract (enumerate ≥3 options, score, log a decision before acting, proceed), every finding cites evidence, every decision is logged before the change.

These are deliberately "rough" — the point is to let the research phases come back and then tell Claude to tighten the workstreams against what was actually found. The two structural changes from CAIM worth knowing:

crossplatform-researcher is promoted to a first-class Phase 2 track, not a sub-bullet. Mac↔Windows is the hard requirement, so it gets its own agent, findings dir, and skeptic votes.
The correctness oracle is different. CAIM verified energy conservation. Here the invariants are convergence, no data loss on conflict, atomic transfer, and no sync loop — and the evidence-generator runs two instances, not one.
Phase 0
rules-architect
Reads first: nothing (it bootstraps the rules). WebSearch-grounded — cite current 2025-2026 sources, no memory-based claims. Produces:

docs/audit/rules/go-rules.md — Go idioms for this domain: goroutine/channel patterns for the three concurrent listeners (UDP discovery, TCP conns, fs watcher); context.Context cancellation; sync.RWMutex separating watcher writes from sync reads; error wrapping; when to use encoding/binary vs gob.
docs/audit/rules/sync-rules.md — sync-engine invariants as hard constraints: "Never write directly to a destination file — temp + atomic rename." "Only broadcast a hash after a local change." "A received file is not a local change until the tree rebuilds." "No data loss on conflict — losing side is renamed, never deleted."
docs/audit/rules/crossplatform-rules.md — path/filename hard rules for Mac↔Windows (see crossplatform-researcher for the evidence behind them).
docs/audit/plan/structure.md — proposed internal/ + cmd/ layout, each file one-line purpose + which finding creates it. Decide & log: framing format, TLS-vs-plaintext, tree node shape.
.claude/agents/*.md — contract per role below.
.claude/skills/merkle-sync/SKILL.md — distilled: the Merkle diff algorithm, the chosen version-vector scheme, the binary framing spec [4-byte len][1-byte type] [payload], and the canonical path-normalisation rules.
CLAUDE.md — hard constraints + how to build/test/cross-compile/add-a-feature.
Phase 1 — Problem-space map
literature-mapper (one per source, run in parallel)
Reads first: the rules dirs. Produces one finding per source in docs/audit/findings/literature/<slug>.md — core algorithm, exact data structures / formulas, failure modes, complexity, and how it maps to Merkle Sync. Sources:

slug	what to extract
syncthing-bep	Block Exchange Protocol: FileInfo fields (version vector, deleted, modified_by), index exchange, how blocks are requested
rsync-algorithm	rolling-checksum + strong-hash delta transfer; why it beats whole-file copy; client/server assumption
merkle-tree	Merkle 1987 / git tree model: how folder hashes derive from child hashes; O(log n) diff property
version-vectors	version vectors vs vector clocks vs Lamport timestamps; how they detect concurrent vs causal edits
cdc-chunking	content-defined chunking (FastCDC / rolling hash) vs fixed blocks; the "insert one byte shifts every boundary" problem
codebase-mapper (×2)
Reads first: go-rules.md. Produces docs/audit/findings/codebases/<slug>.md with specific file paths + line refs.

syncthing-source — directly adoptable, it's Go. How it structures the protocol package, the database of last-known state, conflict-copy creation, the scanner. ≥2 patterns to adopt, ≥1 thing Merkle Sync does differently (simpler: LAN-only multicast, no global discovery server).
rsync-or-librsync — the delta-encoding implementation, for the chunking decision.
synthesizer
Reads first: all of literature/ + codebases/ + rules/. Produces docs/audit/findings/synthesis/problem-space-map.md: algorithm inventory, novelty/scope map (what we deliberately do NOT build vs Syncthing — no relays, no global discovery, no GUI), dependency DAG (pathnorm → scanner → tree → diff → chunk transfer → conflict resolution; protocol framing → transport → discovery), open questions (log a decision or flag for Phase 2), top-5 risk register.

Phase 2 — Deep research (parallel)
merkle-researcher
Tree construction + the diff/reconciliation algorithm (walk both trees, recurse only into mismatching branches). Critically: what metadata each leaf must carry to support two-way sync — a bare content hash tells you files differ but not who is newer or whether one was deleted. Decide & log: leaf = hash+size+mode+mtime+version-vector+tombstone. Decide & log: fixed 32KB chunks vs content-defined chunking. → docs/audit/findings/merkle/.

protocol-researcher
The wire protocol + the hard conflict cases: version-vector comparison to detect concurrent edits; the conflict-copy policy (.sync-conflict-<host>-<n>, loser renamed never deleted); the mtime-tie tiebreaker; deletion via tombstones (how a delete propagates and isn't "resurrected" by a stale peer); rename handling; the sync-loop invariant. Defines the 7-ish message types and the [len][type][payload] framing. Decide & log: TLS trust model (trust-on-first-use device IDs, Syncthing-style) vs plaintext. → docs/audit/findings/protocol/.

crossplatform-researcher ← the elevated track
The reason this fork exists. Reads first: rules/. Produces docs/audit/findings/crossplatform/ covering:

Filename legality: Windows-illegal chars (: * ? " < > |), reserved names (CON, PRN, AUX, NUL, COM1…), trailing dots/spaces, MAX_PATH. Decide & log the escape/reject strategy.
Case sensitivity: macOS (usually case-insensitive-preserving) vs Windows (case-insensitive) — how to detect and handle File.txt vs file.txt collisions without clobbering (Syncthing's approach: refuse + flag).
Unicode normalisation: macOS stores filenames decomposed (NFD), Windows/most else composed (NFC) — the "same" name has two byte representations. Decide & log the canonical form and where normalisation happens.
Path separators: / vs \, and never storing OS-specific separators in the tree (canonical = forward-slash relative paths).
Watcher reality per OS: macOS FSEvents coalescing + advisory history (full scan still required); Windows ReadDirectoryChangesW buffer can overflow and silently drop events under load; fsnotify doesn't recurse (add a watch per subdir) and removes watches on rename/delete. Decide & log: events are hints, periodic full rescan is the source of truth, debounce ~150ms.
antipatterns-researcher (anti-slop pass)
What makes sync engines subtly lose data. Search e.g. "file sync data loss bugs", "sync conflict overwrite", "fsnotify dropped events under load", "non-atomic file write corruption", "clock skew sync conflict resolution". For each: what it looks like in code, why it produces wrong/lost data (not just slow), how to test for it, the correct approach with citation. → docs/audit/rules/sync-antipatterns.md

individual findings for severe ones.
Phase 3 — Design critique
critic (several, one focus each)
Reads first: all rules + structure.md + synthesis + Phase 2 findings. Adversarial design findings (status: open) in docs/audit/findings/design/<critic>/. Focuses:

tree-critic — leaf shape, how folder hashes recompute on change, persistence of last-synced state across restarts (needed to detect deletions).
protocol-critic — framing edge cases (length-prefix off-by-one corrupts the stream), version-vector growth, conflict policy soundness.
concurrency-critic — Go race conditions; is the RWMutex boundary right; can the watcher and a sync write deadlock; goroutine leaks on peer disconnect.
crossplatform-critic — does the pathnorm design actually round-trip Mac→Windows→Mac without mangling names; case-collision handling.
skeptic (×3 per finding)
Reads first: the one finding. Job: refute it. Check the evidence supports the claim, search for counter-examples, check the recommended action beats the status quo, check severity isn't overstated. Writes a vote file. Kill weak findings.

design-consolidator
Keep findings where ≥2/3 skeptics failed to refute (verified); else rejected. Merge duplicates into project-wide design decisions. → docs/audit/findings/design/consolidated/overview.md.

Phase 4 — Plan
planner
Reads first: consolidated design + synthesis + Phase 2 findings + structure.md. Produces docs/audit/plan/implementation-plan.md with ordered workstreams and acceptance criteria phrased as sync invariants:

WS-1 — Merkle tree + scanner + pathnorm. Accept: scanning the same folder twice yields the identical root hash; pathnorm round-trips a Windows-hostile name set without loss; one byte changed in one file changes the root and exactly that leaf's branch.
WS-2 — Transport (TCP framing + TLS). Accept: a message survives being split across TCP reads; malformed length is rejected without corrupting the stream; TLS handshake establishes a pinned device identity.
WS-3 — Discovery (UDP multicast registry). Accept: a second instance is discovered within the announce interval; a silent peer is evicted after the heartbeat timeout.
WS-4 — Reconciliation (diff + chunk stream + conflict resolution). Accept: two instances with divergent folders converge to identical root hashes; simultaneous edits to one file produce a .sync-conflict copy with neither version lost; a transfer killed mid-stream leaves no corrupt file (temp discarded); receiving a file does not trigger a re-broadcast loop.
Plus dependency order (WS-1 → {WS-2, WS-3} → WS-4), per-WS risk + mitigation, and a deferral list (global discovery / NAT traversal / GUI — out of scope, justified).

Phase 5 — Implementation
implementer (one per workstream, single working tree)
Reads first: SKILL.md + all rules + the plan section + all verified consolidated findings + relevant Phase 2 findings (name the ids used). Per plan item: enumerate ≥3 options, score (correctness / concurrency-safety / testability / cross-platform), log a decision, implement, write table-driven tests (Windows-input cases where paths are involved), run go build ./... && go test ./... -race, mark the item done only when green, set the finding to fixed with the commit SHA. Commit format: feat(ws1): <desc> [fixes finding-<id>].

integrator / build-verifier
Clean build + full race-enabled test run + GOOS=windows GOARCH=amd64 go build ./cmd/msync to confirm Windows compiles. Up to 3 logged fix attempts, else record a halt condition.

Phase 6 — Correctness review
evidence-generator ← the key difference from CAIM
Doesn't run a single-process scenario — spins up two msync instances on the Mac (two folders, loopback) and runs the convergence/conflict/deletion/rename/ killed-transfer/large-file scenarios, capturing logs to docs/audit/runs/. Then runs the Windows cross-compile check, and emits the two cross-OS artifacts: .github/workflows/ci.yml (ubuntu/macos/windows matrix running the suite) and docs/audit/CROSS_PLATFORM_CHECKLIST.md (manual Mac↔Windows steps a human runs).

reviewer + skeptic (×3 per fixed finding)
Verdict per finding: fixed / regressed / insufficient, evidence-backed, skeptics try to refute the "fixed" claim.

flow-verifier
End-to-end invariants across the whole system: eventual consistency (after a change settles, both trees expose the identical root hash), no data loss (every conflict left a recoverable copy), no sync loop (a received file produced zero outbound hash broadcasts), clean goroutine shutdown on peer loss.

Phase 7 — Fix loop
fix → re-review
Fix open/regressed findings, re-run Phase 6, repeat until clean or budget/stuck. Every fix logs a decision.

Final
summary + PR
docs/audit/SUMMARY.md (before/after, what converged, what's deferred, the cross-platform checklist status) + optional gh pr create.

Suggested tweak loop
Run pilot first ({ pilot: true }). When Phase 2 comes back, read docs/audit/findings/crossplatform/ and protocol/ and tell Claude which decisions to harden (e.g. "lock chunking to fixed 32KB, defer CDC" or "use NFC as canonical, normalise at scan time") before unlocking { full: true }. The whole framework is built to be re-pointed mid-flight from what the research actually finds.