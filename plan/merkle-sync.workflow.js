/*
 * Merkle Sync — autonomous 7-phase build workflow (Claude Code Workflow-tool script).
 *
 * Builds a decentralised LAN file-sync engine in Go (Mac<->Windows, no central
 * server, raw TCP + UDP multicast, Merkle trees as the source of truth).
 *
 * This is NOT a file you pass to the CLI — it is run via the Workflow tool:
 *     "run the merkle-sync workflow at plan/merkle-sync.workflow.js"
 *
 * Scope is controlled by `args`:
 *     { pilot: true }                 research only, phases 0..2 (the SAFE default)
 *     { full: true,  remote: <url|null> }   design + implement + verify + PR, phases 0..7 + final
 *     { from: N, to: M, finalize: bool }    run an explicit phase range (segmented runs)
 *
 * Fail-safe: with NO args (or args that fail to thread through) it runs pilot
 * (research only). The expensive design/build/verify phases require explicit
 * { full: true } or an explicit { to: >=3 } range.
 *
 * Phase graph (see plan/README.md + plan/agent_roster.md for the contracts):
 *   Pre-flight -> 0 rules -> 1 problem-space map -> 2 deep research (barrier)
 *     -> 3 design critique + 3 skeptics/finding -> 4 plan
 *     -> 5 implementation (WS-1..WS-4, single tree) + build verify
 *     -> 6 correctness review (two-instance evidence, reviewer+skeptics, flow)
 *     -> 7 fix loop -> Final summary + PR
 */

export const meta = {
  name: 'merkle-sync',
  description: 'Autonomous 7-phase build of Merkle Sync: a decentralised Go LAN file-sync engine (Mac<->Windows)',
  whenToUse: 'Run to research, design, implement, and verify the Merkle Sync engine. Default {pilot:true} is research-only; pass {full:true} for the full build.',
  phases: [
    { title: 'Pre-flight', detail: 'ensure go module + dir skeleton + stub cmd/msync + CI skeleton exist' },
    { title: 'Phase 0 — Rules & contracts', detail: 'rules-architect (WebSearch-grounded) + contracts-architect: rules, structure, SKILL, CLAUDE.md' },
    { title: 'Phase 1 — Problem-space map', detail: 'literature-mappers + 2 codebase-mappers (Syncthing/rsync) -> synthesizer' },
    { title: 'Phase 2 — Deep research', detail: 'merkle / protocol / crossplatform (elevated) / antipatterns researchers' },
    { title: 'Phase 3 — Design critique', detail: '4 critics -> 3 adversarial skeptics per finding -> consolidator' },
    { title: 'Phase 4 — Plan', detail: 'planner: ordered workstreams with acceptance criteria phrased as sync invariants' },
    { title: 'Phase 5 — Implementation', detail: 'implementers WS-1..WS-4 (single tree) + integrator/build-verifier (incl. windows cross-compile)' },
    { title: 'Phase 6 — Correctness review', detail: 'two-instance evidence-generator + reviewer+skeptics + flow-verifier; emits CI matrix + cross-platform checklist' },
    { title: 'Phase 7 — Fix loop', detail: 'fix open/regressed findings -> re-review, bounded' },
    { title: 'Final — Summary & PR', detail: 'SUMMARY.md + commit; PR only if a remote is provided' },
  ],
}

// ----------------------------------------------------------------------------
// Config from args (fail-safe default = pilot / research-only)
// ----------------------------------------------------------------------------
const A = (args && typeof args === 'object') ? args : {}
const full = A.full === true
const pilot = A.pilot === true || (!full && A.from === undefined && A.to === undefined)
const START = (A.from !== undefined) ? A.from : 0
const END = (A.to !== undefined) ? A.to : (pilot ? 2 : 7)
const DO_FINAL = !pilot && END >= 7 && A.finalize !== false
const HAS_REMOTE = !!A.remote
const on = (n) => n >= START && n <= END
const MODULE = 'github.com/haider-toha/merkle-sync'

log(`Merkle Sync workflow — ${pilot ? 'PILOT (research-only)' : (full ? 'FULL build' : 'RANGE')}; phases ${START}..${END}${DO_FINAL ? ' + final' : ''}`)

// ----------------------------------------------------------------------------
// Schemas (used for control flow; prose goes in the files agents write)
// ----------------------------------------------------------------------------
const MANIFEST = {
  type: 'object', additionalProperties: false, required: ['files', 'summary'],
  properties: {
    files: { type: 'array', items: { type: 'string' }, description: 'repo-relative paths written' },
    decisions: { type: 'array', items: { type: 'string' }, description: 'decision-file paths written' },
    summary: { type: 'string', description: 'one-paragraph summary of what was produced' },
  },
}
const FINDINGS = {
  type: 'object', additionalProperties: false, required: ['findings'],
  properties: {
    findings: {
      type: 'array', items: {
        type: 'object', additionalProperties: false, required: ['id', 'title', 'severity', 'file'],
        properties: {
          id: { type: 'string' }, title: { type: 'string' },
          severity: { type: 'string', enum: ['low', 'medium', 'high', 'critical'] },
          file: { type: 'string', description: 'path of the finding markdown' },
          claim: { type: 'string' },
        },
      },
    },
  },
}
const VOTE = {
  type: 'object', additionalProperties: false, required: ['refuted', 'reason'],
  properties: {
    refuted: { type: 'boolean' }, reason: { type: 'string' },
    confidence: { type: 'string', enum: ['low', 'medium', 'high'] },
  },
}
const PLAN = {
  type: 'object', additionalProperties: false, required: ['workstreams'],
  properties: {
    workstreams: {
      type: 'array', items: {
        type: 'object', additionalProperties: false, required: ['id', 'title', 'acceptance'],
        properties: {
          id: { type: 'string' }, title: { type: 'string' },
          packages: { type: 'array', items: { type: 'string' } },
          deps: { type: 'array', items: { type: 'string' } },
          acceptance: { type: 'array', items: { type: 'string' } },
        },
      },
    },
  },
}
const WS_RESULT = {
  type: 'object', additionalProperties: false, required: ['ws', 'done', 'testsPassed'],
  properties: {
    ws: { type: 'string' }, done: { type: 'boolean' }, testsPassed: { type: 'boolean' },
    commit: { type: 'string' }, packages: { type: 'array', items: { type: 'string' } },
    findingsFixed: { type: 'array', items: { type: 'string' } }, notes: { type: 'string' },
  },
}
const BUILD = {
  type: 'object', additionalProperties: false, required: ['buildGreen', 'testGreen', 'windowsBuild'],
  properties: {
    buildGreen: { type: 'boolean' }, testGreen: { type: 'boolean' }, windowsBuild: { type: 'boolean' },
    attempts: { type: 'integer' }, halt: { type: 'boolean' }, notes: { type: 'string' },
  },
}
const EVIDENCE = {
  type: 'object', additionalProperties: false, required: ['scenarios'],
  properties: {
    scenarios: {
      type: 'array', items: {
        type: 'object', additionalProperties: false, required: ['name', 'pass'],
        properties: { name: { type: 'string' }, pass: { type: 'boolean' }, log: { type: 'string' }, notes: { type: 'string' } },
      },
    },
    ciEmitted: { type: 'boolean' }, checklistEmitted: { type: 'boolean' },
  },
}
const REVIEW = {
  type: 'object', additionalProperties: false, required: ['verdicts'],
  properties: {
    verdicts: {
      type: 'array', items: {
        type: 'object', additionalProperties: false, required: ['findingId', 'verdict'],
        properties: {
          findingId: { type: 'string' },
          verdict: { type: 'string', enum: ['fixed', 'regressed', 'insufficient'] },
          evidence: { type: 'string' },
        },
      },
    },
  },
}
const FLOW = {
  type: 'object', additionalProperties: false, required: ['invariants'],
  properties: {
    invariants: {
      type: 'array', items: {
        type: 'object', additionalProperties: false, required: ['name', 'pass'],
        properties: { name: { type: 'string' }, pass: { type: 'boolean' }, evidence: { type: 'string' } },
      },
    },
    allPass: { type: 'boolean' },
  },
}
const FIX = {
  type: 'object', additionalProperties: false, required: ['findingId', 'resolved'],
  properties: {
    findingId: { type: 'string' }, resolved: { type: 'boolean' },
    commit: { type: 'string' }, notes: { type: 'string' },
  },
}

// ----------------------------------------------------------------------------
// Shared autonomy contract (appended to every agent prompt)
// ----------------------------------------------------------------------------
const CONTRACT = `
=== AUTONOMY CONTRACT (obey exactly) ===
- Working dir is the repo root of Merkle Sync (Go module ${MODULE}): a decentralised LAN file-sync engine, Mac<->Windows, no central server, raw TCP + UDP multicast, Merkle trees as the source of truth for what differs.
- FIRST read plan/README.md and plan/agent_roster.md for the full design + your role, then read the "Reads first" inputs named in your task.
- DECISIONS: before any consequential choice, enumerate >=3 real options, score each on (correctness / concurrency-safety / testability / cross-platform), pick one, and WRITE the decision to docs/audit/decisions/<area>/<slug>.md BEFORE acting on it. Sections: Context / Options (>=3, scored) / Decision / Rationale / Consequences.
- EVIDENCE: every finding or claim cites evidence — a URL (with access date), a file:line, or a runnable reproduction. No memory-only claims; ground current (2025-2026) facts with WebSearch/WebFetch and cite them.
- Canonical paths are forward-slash relative; never store OS-specific separators. Create parent dirs with the Write tool as needed.
- Do NOT run git unless your task block explicitly grants it.
- IMPORTANT: your final message is a RETURN VALUE parsed by a program, not a message to a human. Put ALL prose/analysis in the files you write. Return ONLY the structured object requested.`

const GIT_OK = `
=== GIT (granted to you) ===
- You may run git. Commit your own work. End EVERY commit message with these two trailer lines, exactly:
Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_013KW2AXjZSWomqBxvwKCSnX
- Never commit a fake green: if build/tests are red, say so in your return and do not claim done.`

const P = (body) => `${body}\n${CONTRACT}`
const PG = (body) => `${body}\n${CONTRACT}\n${GIT_OK}`

// ----------------------------------------------------------------------------
// Static rosters
// ----------------------------------------------------------------------------
const LIT = [
  ['syncthing-bep', 'Syncthing Block Exchange Protocol (BEP v1): FileInfo fields (version vector, deleted, modified_by, blocks), Index/IndexUpdate exchange, how blocks are Requested/Responded, the local vs global model.'],
  ['rsync-algorithm', 'The rsync algorithm (Tridgell & Mackerras): rolling weak checksum + strong hash, delta transfer, why it beats whole-file copy, the sender/receiver assumption.'],
  ['merkle-tree', 'Merkle (1987) hash tree + the git tree-object model: how a folder hash derives from sorted child hashes; the prune-equal-subtrees / O(log n) diff property.'],
  ['version-vectors', 'Version vectors vs vector clocks vs Lamport timestamps: how a version vector distinguishes concurrent (conflicting) from causal (superseding) edits; growth/pruning concerns.'],
  ['cdc-chunking', 'Content-defined chunking (Rabin / FastCDC) vs fixed-size blocks: the "insert one byte shifts every fixed boundary" problem; min/avg/max chunk sizing; dedup tradeoffs.'],
]
const CODE = [
  ['syncthing-source', 'github.com/syncthing/syncthing (Go). Map lib/protocol (BEP, FileInfo, DeviceID, TLS device identity), lib/model + lib/db (last-known-state DB, .sync-conflict creation), lib/scanner (hashing/blocks), lib/fs (case + normalization). Cite concrete file paths + line ranges.'],
  ['rsync-or-librsync', 'librsync (or rsync) delta-encoding implementation (signature/delta/patch, rolling checksum) to inform the fixed-32KB-vs-CDC chunking decision. Cite file paths.'],
]
const RESEARCHERS = [
  ['merkle', `You are the merkle-researcher (Phase 2). Reads first: docs/audit/rules/, docs/audit/findings/synthesis/, docs/audit/findings/literature/merkle-tree.md + .../cdc-chunking.md.
Cover tree construction + the diff/reconciliation algorithm (walk both trees, recurse only into MISMATCHING branches). CRITICAL: define exactly what metadata each LEAF must carry to support TWO-WAY sync — a bare content hash tells you files differ but not who is newer or whether one was deleted.
DECIDE & LOG (decisions/merkle/): leaf shape = hash + size + mode + mtime + version-vector + tombstone (justify each field; note how folder hashes recompute on a single leaf change). DECIDE & LOG: fixed 32KB chunks vs content-defined chunking — pick ONE for v1 (recommend fixed 32KB, defer CDC) with rationale + a forward-compat note.
Write findings under docs/audit/findings/merkle/. Return FINDINGS.`],
  ['protocol', `You are the protocol-researcher (Phase 2). Reads first: docs/audit/rules/, synthesis, docs/audit/findings/literature/syncthing-bep.md + .../version-vectors.md, docs/audit/findings/codebases/syncthing-source.md.
Define the wire protocol AND the hard conflict cases: version-vector comparison (concurrent vs causal); conflict-copy policy (.sync-conflict-<host>-<n>, loser RENAMED never deleted); the mtime-tie tiebreaker (host-id ordering, Syncthing-style); deletions via tombstones (propagation + anti-resurrection by a stale peer); rename handling; the sync-loop invariant (only broadcast after a confirmed local change). Specify the ~7 message types and the [4-byte big-endian len][1-byte type][payload] framing WITH a max-length guard.
DECIDE & LOG (decisions/protocol/): TLS trust-on-first-use device IDs (Syncthing-style) vs plaintext; and the message-type enumeration.
Write findings under docs/audit/findings/protocol/. Return FINDINGS.`],
  ['crossplatform', `You are the crossplatform-researcher (Phase 2) — the ELEVATED track; this is why this fork exists. Reads first: docs/audit/rules/crossplatform-rules.md.
Produce findings under docs/audit/findings/crossplatform/, each with cited evidence:
- Filename legality: Windows-illegal chars (: * ? " < > | and control chars), reserved names (CON, PRN, AUX, NUL, COM1.., LPT1..), trailing dots/spaces, MAX_PATH / long paths. DECIDE & LOG the escape-vs-reject strategy.
- Case sensitivity: macOS (case-insensitive-preserving, usually) vs Windows (case-insensitive) vs case-sensitive Linux — detect & handle File.txt vs file.txt collisions WITHOUT clobber (Syncthing: refuse + flag). DECIDE & LOG.
- Unicode normalisation: macOS NFD vs Windows/most NFC — same name, two byte reps. DECIDE & LOG the canonical form (recommend NFC) and WHERE normalisation happens (at scan time).
- Path separators: / vs backslash; canonical = forward-slash relative; never store OS separators in the tree.
- Watcher reality: FSEvents coalescing + advisory history (full scan still required); Windows ReadDirectoryChangesW buffer overflow silently DROPS events under load; fsnotify is not recursive + drops watches on rename/delete. DECIDE & LOG: events are hints, periodic full rescan is the source of truth, debounce ~150ms.
Return FINDINGS.`],
  ['antipatterns', `You are the antipatterns-researcher (Phase 2, anti-slop pass). Reads first: docs/audit/rules/, synthesis.
Investigate what makes sync engines SUBTLY LOSE DATA. WebSearch e.g. "file sync data loss bug", "sync conflict overwrite", "fsnotify dropped events under load", "non-atomic file write corruption", "clock skew sync conflict resolution", "TOCTOU rename sync". For each anti-pattern: what it looks like in code; why it produces wrong/LOST data (not merely slow); how to TEST for it; the correct approach — all cited.
Write the consolidated catalogue to docs/audit/rules/sync-antipatterns.md, and an individual finding for each SEVERE one. Return FINDINGS.`],
]
const CRITICS = [
  ['tree-critic', 'leaf shape (does hash+size+mode+mtime+version-vector+tombstone really support two-way sync?), how folder hashes recompute on a single-file change, and persistence of last-synced state across restarts (without it you cannot distinguish a deletion from a never-seen file).'],
  ['protocol-critic', 'framing edge cases (length-prefix off-by-one / unbounded length DoS corrupts or hangs the stream), version-vector growth + pruning, soundness of the conflict-copy policy and the mtime-tie tiebreaker, tombstone resurrection by a stale peer.'],
  ['concurrency-critic', 'Go race conditions; is the sync.RWMutex boundary (watcher-writes vs sync-reads) correct & complete; can the watcher and a sync write deadlock; goroutine leaks on peer disconnect; context cancellation paths; channel close semantics.'],
  ['crossplatform-critic', 'does the pathnorm design actually round-trip Mac(NFD)->Windows(NFC)->Mac without mangling names; case-insensitive collision handling without clobber; illegal-char/reserved-name strategy; never storing OS separators in the tree.'],
]
const WORKSTREAMS = [
  {
    id: 'ws1', name: 'WS-1', title: 'Merkle tree + scanner + pathnorm',
    packages: ['internal/pathnorm', 'internal/merkle'],
    acceptance: 'scanning the same folder twice yields an IDENTICAL root hash; pathnorm round-trips a Windows-hostile name set without loss; changing one byte in one file changes the root hash and EXACTLY that leaf\'s branch.',
  },
  {
    id: 'ws2', name: 'WS-2', title: 'Transport (TCP framing + TLS)',
    packages: ['internal/protocol', 'internal/transport'],
    acceptance: 'a message survives being split across TCP reads; a malformed/oversized length prefix is rejected without corrupting the stream; the TLS handshake establishes a pinned (trust-on-first-use) device identity.',
  },
  {
    id: 'ws3', name: 'WS-3', title: 'Discovery (UDP multicast registry)',
    packages: ['internal/discovery'],
    acceptance: 'a second instance is discovered within the announce interval; a silent peer is evicted after the heartbeat timeout; a rejoining peer is re-discovered.',
  },
  {
    id: 'ws4', name: 'WS-4', title: 'Reconciliation (diff + chunk stream + conflict resolution)',
    packages: ['internal/reconcile', 'cmd/msync', 'test/integration'],
    acceptance: 'two instances with divergent folders converge to identical root hashes; simultaneous edits to one file produce a .sync-conflict copy with NEITHER version lost; a transfer killed mid-stream leaves no corrupt file (temp discarded, atomic rename); receiving a file does NOT trigger a re-broadcast loop.',
  },
]

// ----------------------------------------------------------------------------
// Pre-flight (idempotent guard — no-op if the skeleton already exists)
// ----------------------------------------------------------------------------
if (on(0)) {
  phase('Pre-flight')
  await agent(P(`You are the pre-flight guard. Ensure (create only if MISSING — never overwrite existing work) the project skeleton:
- go.mod (module ${MODULE}); if absent, create it targeting go 1.23.
- dirs: internal/{merkle,pathnorm,protocol,transport,discovery,reconcile}, cmd/msync, test/integration, docs/audit/{decisions,plan,runs,rules}, docs/audit/findings/{literature,codebases,merkle,protocol,crossplatform,design,review,synthesis}, .claude/{agents,skills/merkle-sync}, .github/workflows.
- a compiling stub at cmd/msync/main.go (package main) IF none exists.
- a .github/workflows/ci.yml CI skeleton (ubuntu/macos/windows matrix) IF none exists.
Run 'go build ./...' to confirm the tree compiles. Do NOT touch files that already exist. Return MANIFEST of anything you created.`),
    { label: 'preflight-guard', phase: 'Pre-flight', schema: MANIFEST })
}

// ----------------------------------------------------------------------------
// Phase 0 — Rules & contracts (WebSearch-grounded)
// ----------------------------------------------------------------------------
if (on(0)) {
  phase('Phase 0 — Rules & contracts')
  await agent(P(`You are the rules-architect (Phase 0). Bootstrap the project's hard rules, WebSearch-grounded with current 2025-2026 sources (cite URLs + dates). Produce, each substantive:
1) docs/audit/rules/go-rules.md — Go idioms for THIS domain: goroutine/channel patterns for the three concurrent listeners (UDP discovery, TCP conns, fs watcher); context.Context cancellation + clean shutdown; sync.RWMutex separating watcher-writes from sync-reads; error wrapping (%w); when encoding/binary beats gob for framing; fsnotify realities (not recursive — watch per subdir; watches dropped on rename/delete; debounce).
2) docs/audit/rules/sync-rules.md — sync-engine invariants as HARD constraints (rule + why + how tested). MUST include: temp-write + atomic rename (never write a destination in place); only broadcast a hash after a confirmed LOCAL change; a received file is not a local change until the tree rebuilds (no echo loop); no data loss on conflict (loser renamed, never deleted); deletions propagate as tombstones and a stale peer must not resurrect them.
3) docs/audit/rules/crossplatform-rules.md — PRELIMINARY Mac<->Windows path/filename rules (Phase 2 will harden): illegal chars + reserved names; case-insensitivity collisions; NFC vs NFD; forward-slash canonical relative paths; watcher event drops. Mark each "preliminary — confirm in Phase 2".
4) docs/audit/plan/structure.md — proposed internal/ + cmd/ + test/ layout; for EACH file a one-line purpose + which finding/workstream creates it. Then DECIDE & LOG (decisions/phase0/): (a) framing format — propose [4-byte big-endian length][1-byte type][payload] + max-length guard; (b) TLS trust-on-first-use device IDs vs plaintext; (c) the Merkle leaf shape (hash+size+mode+mtime+version-vector+tombstone).
Return MANIFEST.`),
    { label: 'rules-architect', phase: 'Phase 0 — Rules & contracts', schema: MANIFEST })

  await agent(P(`You are the contracts-architect (Phase 0, after rules-architect). Read docs/audit/rules/*.md, docs/audit/plan/structure.md, and the two plan/*.md specs. Produce:
1) CLAUDE.md (repo root) — the agent contract / hard rules for anyone working in this repo: the sync invariants + cross-platform rules (link the rules files), how to build/test/cross-compile ('go build ./...', 'go test ./... -race', 'GOOS=windows GOARCH=amd64 go build ./cmd/msync'), and the README's "how to add a feature" checklist (finding -> protocol message -> handler -> table-driven test w/ Windows inputs -> integration scenario -> windows build).
2) .claude/skills/merkle-sync/SKILL.md — the distilled spec: the Merkle diff algorithm (walk both trees, prune equal subtrees, recurse only mismatching branches), the chosen version-vector scheme + concurrent-vs-causal decision, the binary framing spec [4-byte len][1-byte type][payload] with the message-type table, the canonical path-normalisation rules.
3) .claude/agents/<role>.md — one short contract file per role in plan/agent_roster.md (rules-architect, literature-mapper, codebase-mapper, synthesizer, merkle-researcher, protocol-researcher, crossplatform-researcher, antipatterns-researcher, tree-critic, protocol-critic, concurrency-critic, crossplatform-critic, skeptic, design-consolidator, planner, implementer, integrator, evidence-generator, reviewer, flow-verifier): reads-first / produces / contract.
Return MANIFEST.`),
    { label: 'contracts-architect', phase: 'Phase 0 — Rules & contracts', schema: MANIFEST })
}

// ----------------------------------------------------------------------------
// Phase 1 — Problem-space map (mappers in parallel -> synthesizer)
// ----------------------------------------------------------------------------
if (on(1)) {
  phase('Phase 1 — Problem-space map')
  const mappers = []
  for (const [slug, desc] of LIT) {
    mappers.push(() => agent(P(`You are a literature-mapper (Phase 1) for source: ${slug}. Reads first: docs/audit/rules/.
Research this source (${desc}) with WebSearch + WebFetch — papers AND real source where it exists; cite URLs + access dates.
Write docs/audit/findings/literature/${slug}.md: core algorithm; the EXACT data structures / formulas / field layouts; failure modes; complexity; and a concrete "how it maps to Merkle Sync" section (adopt vs adapt).
Return MANIFEST.`), { label: `lit:${slug}`, phase: 'Phase 1 — Problem-space map', schema: MANIFEST }))
  }
  for (const [slug, desc] of CODE) {
    mappers.push(() => agent(P(`You are a codebase-mapper (Phase 1) for: ${slug}. Reads first: docs/audit/rules/go-rules.md.
Study the real source (${desc}) via WebFetch of the repo (raw files / specific paths); cite file:line refs.
Write docs/audit/findings/codebases/${slug}.md: how it structures the relevant package(s) with refs; >=2 concrete patterns to ADOPT; >=1 thing Merkle Sync deliberately does DIFFERENTLY (simpler: LAN-only multicast, no global discovery server, no GUI).
Return MANIFEST.`), { label: `code:${slug}`, phase: 'Phase 1 — Problem-space map', schema: MANIFEST }))
  }
  await parallel(mappers)

  await agent(P(`You are the synthesizer (Phase 1). Reads first: ALL of docs/audit/findings/literature/, docs/audit/findings/codebases/, docs/audit/rules/.
Write docs/audit/findings/synthesis/problem-space-map.md: (1) algorithm inventory; (2) novelty/scope map — what we deliberately do NOT build vs Syncthing (no relays, no global discovery, no GUI); (3) the dependency DAG (pathnorm -> scanner -> tree -> diff -> chunk transfer -> conflict resolution; protocol framing -> transport -> discovery); (4) open questions (log a decision or flag for Phase 2); (5) a top-5 risk register (risk / likelihood / impact / mitigation).
Return MANIFEST.`), { label: 'synthesizer', phase: 'Phase 1 — Problem-space map', schema: MANIFEST })
}

// ----------------------------------------------------------------------------
// Phase 2 — Deep research (parallel; runs after Phase 1 so researchers build
// on completed literature + synthesis rather than reading half-written files)
// ----------------------------------------------------------------------------
if (on(2)) {
  phase('Phase 2 — Deep research')
  await parallel(RESEARCHERS.map(([slug, prompt]) => () =>
    agent(P(prompt), { label: `research:${slug}`, phase: 'Phase 2 — Deep research', schema: FINDINGS })))
}

// ----------------------------------------------------------------------------
// Phase 3 — Design critique -> 3 adversarial skeptics per finding -> consolidate
// ----------------------------------------------------------------------------
if (on(3)) {
  phase('Phase 3 — Design critique')
  const perCritic = await pipeline(
    CRITICS,
    ([slug, focus]) => agent(P(`You are ${slug} (Phase 3 design critic). Reads first: all docs/audit/rules/, docs/audit/plan/structure.md, docs/audit/findings/synthesis/, and the Phase 2 findings.
Adversarially critique the DESIGN (not yet the code) for: ${focus}
Write each finding as its own file docs/audit/findings/design/${slug}/<id>.md with YAML front-matter (id, title, severity, status: open) + Claim / Evidence / Impact / Recommended-change. Be specific and evidence-backed — a finding that just says "be careful" is worthless. Quality over quantity (aim 2-4 strongest).
Return FINDINGS with id like "${slug}-1".`), { label: `critic:${slug}`, phase: 'Phase 3 — Design critique', schema: FINDINGS })
      .then((r) => ({ slug, findings: (r && r.findings) || [] })),
    (cr) => parallel((cr.findings).map((f) => () =>
      parallel([0, 1, 2].map((k) => () =>
        agent(P(`You are skeptic #${k + 1} of 3 for design finding ${f.id} ("${f.title}"). Your job is to REFUTE it.
Read the finding file ${f.file} and any evidence it cites. Check: does the evidence support the claim? Is there a counter-example? Does the recommended change really beat the status quo? Is the severity overstated? Default refuted=true if the finding is weak, vague, or unsupported.
Write your vote to docs/audit/findings/design/${cr.slug}/votes/${f.id}-skeptic${k + 1}.md.
Return VOTE.`), { label: `skeptic:${f.id}#${k + 1}`, phase: 'Phase 3 — Design critique', effort: 'medium', schema: VOTE })))
        .then((votes) => ({ finding: f, votes: votes.filter(Boolean) }))))
      .then((vf) => ({ slug: cr.slug, voted: vf.filter(Boolean) })),
  )

  const voted = perCritic.filter(Boolean).flatMap((c) => c.voted)
  const tally = voted.map((v) => ({
    id: v.finding.id, title: v.finding.title, file: v.finding.file, severity: v.finding.severity,
    refuted: v.votes.filter((x) => x && x.refuted).length, total: v.votes.length,
  }))
  log(`Phase 3: ${tally.length} design findings voted; ${tally.filter((t) => t.refuted <= 1).length} survive (refuted<=1 of 3)`)

  await agent(P(`You are the design-consolidator (Phase 3). Skeptic tallies (KEEP a finding only when >=2 of 3 FAILED to refute it, i.e. refuted<=1):
${JSON.stringify(tally, null, 2)}
For each finding read its file + its votes. Mark VERIFIED (kept) or REJECTED per the rule, and update each finding file's front-matter status accordingly. Merge duplicates/overlaps into project-wide design decisions. Write docs/audit/findings/design/consolidated/overview.md: verified design decisions (each actionable + tied to a workstream), the rejected list with why, and any merged decisions.
Return MANIFEST.`), { label: 'design-consolidator', phase: 'Phase 3 — Design critique', schema: MANIFEST })
}

// ----------------------------------------------------------------------------
// Phase 4 — Plan
// ----------------------------------------------------------------------------
if (on(4)) {
  phase('Phase 4 — Plan')
  await agent(P(`You are the planner (Phase 4). Reads first: docs/audit/findings/design/consolidated/overview.md, synthesis, the Phase 2 findings, docs/audit/plan/structure.md.
Write docs/audit/plan/implementation-plan.md with ordered workstreams whose ACCEPTANCE CRITERIA are phrased as sync invariants:
- WS-1 Merkle tree + scanner + pathnorm. Accept: same folder scanned twice => identical root hash; pathnorm round-trips a Windows-hostile name set without loss; one byte changed => root hash changes and exactly that leaf's branch.
- WS-2 Transport (TCP framing + TLS). Accept: a message survives being split across TCP reads; a malformed/oversized length is rejected without corrupting the stream; TLS handshake pins a device identity.
- WS-3 Discovery (UDP multicast registry). Accept: a 2nd instance discovered within the announce interval; a silent peer evicted after the heartbeat timeout.
- WS-4 Reconciliation (diff + chunk stream + conflict resolution). Accept: two divergent instances converge to identical root hashes; simultaneous edits => .sync-conflict copy, neither version lost; a transfer killed mid-stream leaves no corrupt file; receiving a file triggers no re-broadcast loop.
Include dependency order (WS-1 -> {WS-2, WS-3} -> WS-4), per-WS risk + mitigation, and a justified deferral list (global/cross-subnet discovery, NAT traversal, GUI — out of scope).
Return PLAN.`), { label: 'planner', phase: 'Phase 4 — Plan', schema: PLAN })
}

// ----------------------------------------------------------------------------
// Phase 5 — Implementation (sequential, single working tree) + build verify
// ----------------------------------------------------------------------------
let buildResult = null
if (on(5)) {
  phase('Phase 5 — Implementation')
  for (const ws of WORKSTREAMS) {
    await agent(PG(`You are the implementer for ${ws.name} — ${ws.title} (Phase 5). You are the ONLY agent running now (single working tree), so git is safe.
Reads first: .claude/skills/merkle-sync/SKILL.md, all docs/audit/rules/, the ${ws.name} section of docs/audit/plan/implementation-plan.md, the verified decisions in docs/audit/findings/design/consolidated/overview.md, and the relevant Phase 2 findings (name the finding ids you use).
Target packages: ${ws.packages.join(', ')}. Module path: ${MODULE}.
For each plan item: enumerate >=3 options, score (correctness/concurrency-safety/testability/cross-platform), LOG a decision (decisions/${ws.id}/), then implement. Write TABLE-DRIVEN tests; wherever paths are involved include Windows-hostile input cases (illegal chars, reserved names, NFD/NFC, case collisions, backslashes).
Run 'go build ./...' and 'go test ./... -race'. Mark a plan item done ONLY when green.
ACCEPTANCE for ${ws.name}: ${ws.acceptance}
When green: 'git add' your packages + tests + decision/finding files and commit 'feat(${ws.id}): <desc> [fixes <finding-ids>]'. Set any finding you resolved to status: fixed with the commit SHA.
Return WS_RESULT. If you cannot get green, return done:false + notes — do NOT fake it.`),
      { label: `impl:${ws.id}`, phase: 'Phase 5 — Implementation', schema: WS_RESULT })
  }

  buildResult = await agent(PG(`You are the integrator / build-verifier (Phase 5 tail). Run, capturing output:
- 'go build ./...'
- 'go vet ./...'
- 'go test ./... -race -count=1'
- 'GOOS=windows GOARCH=amd64 go build ./cmd/msync' (confirm Windows compiles)
- 'gofmt -l .' (must be empty)
If anything fails, make up to 3 LOGGED fix attempts (decisions/phase5/build-fix-<n>.md), re-running after each. If still red after 3, record a halt in docs/audit/decisions/phase5/HALT.md and return halt:true. Commit genuine fixes only.
Return BUILD.`), { label: 'build-verifier', phase: 'Phase 5 — Implementation', schema: BUILD })
  if (buildResult) log(`Phase 5 build: build=${buildResult.buildGreen} test=${buildResult.testGreen} windows=${buildResult.windowsBuild}${buildResult.halt ? ' HALT' : ''}`)
}

// ----------------------------------------------------------------------------
// Phase 6 — Correctness review (two-instance evidence + reviewer + flow)
// ----------------------------------------------------------------------------
let reviewFails = []
if (on(6)) {
  phase('Phase 6 — Correctness review')
  const evidence = await agent(PG(`You are the evidence-generator (Phase 6) — the key difference from a single-process verifier. Prove the sync INVARIANTS with two instances and emit the cross-OS artifacts.
1) Run the two-node convergence scenarios. Prefer the in-process integration tests in test/integration ('go test ./test/integration -v -race', capture to docs/audit/runs/integration.log). THEN, if cmd/msync can run two daemons against two folders over loopback, also do that two-process demo and capture its logs. Cover: convergence (divergent folders -> identical root hash), conflict (simultaneous edit -> .sync-conflict copy, nothing lost), deletion (tombstone propagates, no resurrection), rename, killed-transfer (no corrupt file), large-file (multi-chunk).
2) Run 'GOOS=windows GOARCH=amd64 go build ./cmd/msync'.
3) EMIT the two cross-OS artifacts:
   - .github/workflows/ci.yml — refine the matrix (ubuntu/macos/windows) so it builds + vets + runs the full -race suite; KEEP the windows-latest job (this closes the OS gap).
   - docs/audit/CROSS_PLATFORM_CHECKLIST.md — the manual Mac<->Windows steps a human runs once against a real Windows box (case collisions, reserved names, NFD/NFC, firewall/multicast, watcher overflow).
Write a per-scenario log under docs/audit/runs/. Return EVIDENCE.`), { label: 'evidence-generator', phase: 'Phase 6 — Correctness review', schema: EVIDENCE })
  if (evidence) log(`Phase 6 evidence: ${(evidence.scenarios || []).filter((s) => s.pass).length}/${(evidence.scenarios || []).length} scenarios pass; ci=${evidence.ciEmitted} checklist=${evidence.checklistEmitted}`)

  const review = await agent(P(`You are the reviewer (Phase 6). For each finding marked status: fixed (search docs/audit/findings/), verify against the actual code + tests + the run logs in docs/audit/runs/. Verdict per finding: fixed / regressed / insufficient, EVIDENCE-backed (file:line, test name, or log line). Write verdicts to docs/audit/findings/review/<id>.md.
Return REVIEW.`), { label: 'reviewer', phase: 'Phase 6 — Correctness review', schema: REVIEW })

  const fixedVerdicts = ((review && review.verdicts) || []).filter((v) => v.verdict === 'fixed')
  const skepticResults = await parallel(fixedVerdicts.map((v) => () =>
    parallel([0, 1, 2].map((k) => () =>
      agent(P(`You are skeptic #${k + 1} of 3 challenging the verdict that finding ${v.findingId} is FIXED. Try to REFUTE "fixed": find a missing test, an unhandled edge case, a code path the fix doesn't cover, or a regression. Read the finding, the code, and the run logs. Default refuted=true if the "fixed" claim is not solidly evidenced.
Return VOTE.`), { label: `rev-skeptic:${v.findingId}#${k + 1}`, phase: 'Phase 6 — Correctness review', effort: 'medium', schema: VOTE })))
      .then((votes) => ({ id: v.findingId, refuted: votes.filter((x) => x && x.refuted).length, total: votes.filter(Boolean).length }))))

  const flow = await agent(P(`You are the flow-verifier (Phase 6). Verify END-TO-END system invariants with evidence (point at tests/logs):
- Eventual consistency: after a change settles, both trees expose the IDENTICAL root hash.
- No data loss: every conflict left a recoverable copy (loser renamed, not deleted).
- No sync loop: a received file produced ZERO outbound hash broadcasts.
- Clean shutdown: no goroutine leak on peer disconnect / context cancel.
Write docs/audit/findings/review/flow-verification.md. Return FLOW.`), { label: 'flow-verifier', phase: 'Phase 6 — Correctness review', schema: FLOW })

  reviewFails = [
    ...((review && review.verdicts) || []).filter((v) => v.verdict !== 'fixed').map((v) => ({ id: v.findingId, why: v.verdict })),
    ...skepticResults.filter(Boolean).filter((s) => s.refuted >= 2).map((s) => ({ id: s.id, why: 'fixed-claim-refuted' })),
    ...((flow && flow.invariants) || []).filter((i) => !i.pass).map((i) => ({ id: `flow:${i.name}`, why: 'invariant-failed' })),
  ]
  if (buildResult && buildResult.halt) reviewFails.push({ id: 'build', why: 'build-halt' })
  log(`Phase 6: ${reviewFails.length} open item(s) heading into the fix loop`)
}

// ----------------------------------------------------------------------------
// Phase 7 — Fix loop (bounded; "two zero-progress rounds" halt)
// ----------------------------------------------------------------------------
if (on(7)) {
  phase('Phase 7 — Fix loop')
  let round = 0
  let fails = reviewFails
  while (fails.length && round < 2) {
    round++
    log(`Fix round ${round}: ${fails.length} open item(s)`)
    for (const f of fails) {
      await agent(PG(`You are a fix agent (Phase 7, round ${round}) for open item: ${f.id} (${f.why}). Single working tree — git is safe.
Read the relevant finding / review / flow-verification file and the code. Enumerate >=3 fix options, score, LOG a decision (decisions/phase7/), implement the fix, add/adjust tests (Windows-input cases if paths), run 'go build ./... && go test ./... -race' until green, then commit 'fix(${f.id}): <desc>'. Set the finding status to fixed with the SHA.
Return FIX. If unresolved, resolved:false + notes (do not fake).`), { label: `fix:${f.id}`, phase: 'Phase 7 — Fix loop', schema: FIX })
    }
    const re = await agent(P(`You are the re-reviewer (Phase 7, round ${round}). These items were open: ${JSON.stringify(fails.map((f) => f.id))}. Re-verify each against the CURRENT code + a fresh 'go test ./... -race' run + the flow invariants (eventual consistency / no data loss / no sync loop / clean shutdown).
Return FLOW where each invariant 'name' is one of the item ids above and pass=true means resolved.`), { label: `re-review-r${round}`, phase: 'Phase 7 — Fix loop', schema: FLOW })
    fails = ((re && re.invariants) || []).filter((i) => !i.pass).map((i) => ({ id: i.name, why: 'still-failing' }))
    log(fails.length ? `Fix round ${round}: ${fails.length} still failing` : `Fix round ${round}: all clear`)
  }
  if (fails.length) log(`HALT: ${fails.length} item(s) unresolved after ${round} round(s) — see docs/audit/decisions/`)
}

// ----------------------------------------------------------------------------
// Final — Summary & PR
// ----------------------------------------------------------------------------
if (DO_FINAL) {
  phase('Final — Summary & PR')
  await agent(P(`You are the summary agent (Final). Read the audit trail (decisions/, findings/, runs/, plan/) and the code. Write docs/audit/SUMMARY.md: what was built (per package), before/after, which sync invariants converged (with evidence), what is deferred and why, the Phase 6/7 outcome, and the CROSS-PLATFORM CHECKLIST status — the honest "green on the Mac is necessary but not sufficient" caveat (what still needs a real Windows box). Keep it skimmable with a status table.
Return MANIFEST.`), { label: 'summary', phase: 'Final — Summary & PR', schema: MANIFEST })

  await agent(PG(`You are the PR agent (Final).
1) Ensure everything is committed: 'git add -A' then commit any remaining audit files with 'docs(audit): final audit trail + summary' (skip if nothing to commit).
2) Check 'git remote -v'. ${HAS_REMOTE ? 'A remote URL was provided: push the branch and run gh pr create with a body summarising the build + linking docs/audit/SUMMARY.md, ending with the Claude Code footer.' : 'NO remote was provided: do NOT push or open a PR. Just report the local branch name and the exact commands a human would run to push + open a PR.'}
Return MANIFEST (summary = branch state + PR url or 'local only').`), { label: 'pr', phase: 'Final — Summary & PR', schema: MANIFEST })
}

return {
  mode: pilot ? 'pilot' : (full ? 'full' : 'range'),
  phases: [START, END],
  finalized: DO_FINAL,
  build: buildResult,
  openItemsAfterReview: reviewFails.map((f) => f.id),
}
