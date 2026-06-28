# Decision: the scope / novelty boundary vs Syncthing (binding deferral list)

- Area: phase1 / synthesis
- Status: decided (Phase 1 — **binding on the planner's deferral list**, per the
  synthesizer contract `.claude/agents/synthesizer.md`: "The scope/novelty
  boundaries set here are binding on the planner's deferral list.")
- Date: 2026-06-28
- Decider: synthesizer (Phase 1)
- Consumes: all of `docs/audit/findings/literature/`,
  `docs/audit/findings/codebases/`, `docs/audit/rules/`, and the Phase 0 decisions.

## Context

Syncthing is Merkle Sync's primary reference (`syncthing-bep`,
`syncthing-source`). It ships a large feature surface: global discovery servers,
relays/NAT traversal, a web GUI + REST API, a persistent multi-device index
database (v2 SQLite), delta indexes keyed by `{index_id, sequence}`, N-device
clusters with introducers, untrusted-peer at-rest encryption, LZ4 wire
compression, adaptive 128 KiB–16 MiB blocks, swarm `DownloadProgress`, and
send-only/receive-only folder modes.

Merkle Sync's stated target (`plan/README.md`, `plan/agent_roster.md`) is far
narrower: a **2-device, LAN-only, Mac↔Windows** folder sync, no central server,
raw TCP + UDP multicast, with the Merkle tree as the source of truth for what
differs. The honest constraint (`plan/README.md`) is that real cross-OS
behaviour cannot be verified from the Mac, so every line of scope we add that we
*cannot* test is pure risk.

The synthesizer must set the binding boundary of what we **deliberately do NOT
build**, because that boundary constrains the Phase 4 planner's deferral list and
every downstream workstream. This is a consequential, downstream-binding choice,
so it is logged before being written into `problem-space-map.md`.

Scoring axes (adapted from the contract's correctness / concurrency-safety /
testability / cross-platform, as the `syncthing-version-to-map` decision also
adapted them):
- **correctness** = does the scope still deliver the contract (no data loss,
  convergence, atomic transfer, no sync loop)?
- **concurrency-safety** = does it keep the concurrency model (three listeners +
  one RWMutex, GR-3/GR-4/GR-5) simple and sound?
- **testability** = can it be verified on the Mac + the CI matrix, given the
  cross-OS gap?
- **cross-platform** = does it serve the hard Mac↔Windows requirement without
  unverifiable surface?

## Options (scored 1–5, 5 = best)

### Option A — Full Syncthing parity

Build (eventually) the whole surface: global discovery, relays, GUI/REST,
persistent multi-device index DB, delta indexes, N-device clusters, at-rest
encryption, compression, adaptive blocks, swarm fetch.

- correctness **3** — more moving parts = more places to lose data; the global/
  local multi-device reconciliation is itself a known correctness surface
  (`syncthing-bep` §10.4, issues #7649/#4460).
- concurrency-safety **2** — N-device clustering, relays, and a DB layer multiply
  goroutines and lock domains far beyond the GR-5 single-RWMutex model.
- testability **1** — most of it (relays, NAT, global discovery, N-device) is
  un-testable on the Mac and largely un-testable even in CI.
- cross-platform **3** — adds surface (DB files, GUI) that itself needs cross-OS
  validation we cannot do.
- Verdict: rejected — enormous, mostly unverifiable, contradicts the LAN-only
  brief.

### Option B — Minimal LAN 2-device core (PROPOSED)

Keep exactly: recursive in-memory Merkle tree + prune-equal diff; BEP's
`FileInfo` / version-vector / `WinsConflict` / conflict-copy / tombstone core;
fixed content-addressed blocks (CDC deferred); hand-rolled `[len][type][payload]`
binary framing; TLS 1.3 + TOFU device IDs; UDP-multicast-only discovery; headless
`cmd/msync` daemon. Drop everything in the "NOT built" list below.

- correctness **5** — the kept core is exactly what the no-data-loss/convergence
  contract requires; the Merkle tree *subsumes* delta indexes
  (`syncthing-bep` §6.4) so we drop machinery without losing the capability.
- concurrency-safety **5** — three listeners + one RWMutex (GR-4/GR-5); no DB, no
  relay, no N-device fan-out.
- testability **5** — two loopback instances on the Mac exercise the whole core
  (`evidence-generator`); the CI matrix + `CROSS_PLATFORM_CHECKLIST.md` close the
  rest.
- cross-platform **5** — minimal surface, all of it forced through one canonical
  path/serialization choke point (SR-13, GR-12).
- Verdict: **adopted.**

### Option C — LAN core + selected Syncthing extras

Option B plus a few "cheap" extras: delta indexes (`index_id`/`sequence`),
adaptive power-of-two blocks, and maybe N-device support.

- correctness **4** — fine, but delta indexes re-introduce the
  sequence/`index_id` lifecycle and its desync bugs (`syncthing-bep` §10.3, issue
  #3457) for a capability the Merkle diff already provides for free.
- concurrency-safety **4** — N-device support alone breaks the 2-peer
  simplifications (`WinsConflict` reduction, single-peer registry).
- testability **3** — N-device and delta-index edge cases are harder to cover.
- cross-platform **4** — neutral.
- Verdict: rejected for v1 — adds complexity the Merkle tree makes redundant
  (delta indexes) or the 2-device target does not need (N-device). Adaptive block
  size is the *one* extra worth a real Phase 2 look, so it is **not** rejected
  outright — it is handed to `merkle-researcher` as the chunking open question
  (OQ-1), not pulled into scope here.

## Decision

Adopt **Option B**. The binding "deliberately NOT built" list for v1:

| # | Not built | Replaced by / why | Evidence |
|---|---|---|---|
| N1 | Global / cross-subnet discovery server | LAN UDP multicast only | `syncthing-source` D3-4 |
| N2 | Relays / NAT traversal | both devices on one LAN | `syncthing-source` D3-4; `rsync-or-librsync` DIFF-2 |
| N3 | GUI / web UI / REST API | headless `cmd/msync` daemon | `syncthing-source` D3-4 |
| N4 | Persistent multi-device index DB (SQLite/LevelDB) | one in-memory Merkle tree under an RWMutex | `syncthing-source` D3-1 |
| N5 | Delta indexes (`index_id`, `sequence`, `max_sequence`) | Merkle root/subtree diff is the "what changed" mechanism (O(log n)); optionally keep one last-synced root hash per peer | `syncthing-bep` §6.4; `merkle-tree` §5 |
| N6 | N-device cluster / introducer / `secondary` / multi-connection | 2-device, single connection; `global = WinsConflict(local, remote)` | `syncthing-bep` §8, §9 |
| N7 | At-rest / untrusted-peer encryption (`RECEIVE_ENCRYPTED`) | TLS 1.3 in transit only; peers are trusted | `syncthing-bep` §9 |
| N8 | LZ4 wire compression | fast LAN; framing stays forward-compatible | `syncthing-bep` §9 |
| N9 | `DownloadProgress` swarm fetch from peers' temp files | 2-peer convergence needs no swarm | `syncthing-bep` §7.5 |
| N10 | Send-only / receive-only folder modes | send-receive only | `syncthing-bep` §9 |
| N11 | Protobuf wire format | hand-rolled `[4-byte len][1-byte type][payload]` binary (GR-7) | `syncthing-source` D3-2; `framing-format.md` |
| N12 | rsync rolling-search delta codec (signature/delta/patch) | fixed content-addressed blocks; the Merkle tree of block hashes is the source of truth | `rsync-or-librsync` DIFF-1; `rsync-algorithm` §11 |
| N13 | Content-defined chunking (FastCDC) in v1 | fixed blocks v1; CDC is the *adapt-later* escape hatch behind an algo-version field | `cdc-chunking` §9.3; `rsync-or-librsync` ADOPT-3 |
| N14 | `PlatformData` ownership / xattr sync | `mode` best-effort only; non-portable (XP-6) | `syncthing-bep` §9 |
| N15 | Human device-ID encoding flourish (Luhn/base32 chunking) | a plain hex/base32 string; no GUI consumes it | `syncthing-source` A2-1 |

The **core kept** (the novelty surface): a recursive Merkle tree + prune-equal
diff as the change-detection layer *in place of* BEP's sequence/index delta
machinery, wrapped around BEP's proven `FileInfo`/version-vector/`WinsConflict`/
conflict-copy/tombstone/atomic-transfer data model, with the N-device/relay/DB/
encryption/compression surface removed.

## Rationale

- The Merkle tree is not just a simplification of Syncthing — it is the
  *substitute* for the single biggest piece we drop (N4/N5: the multi-device
  index DB and its delta-index lifecycle). We lose no capability there; an
  O(log n) subtree-hash diff tells two peers exactly which subtrees differ with no
  per-change sequence bookkeeping (`syncthing-bep` §6.4).
- Everything else dropped is either physically out of reach (N1/N2 — not on this
  LAN), un-needed at 2 devices (N6/N9), a trust-model mismatch (N7 — we trust LAN
  peers behind TLS), or a complexity-for-no-LAN-benefit trade (N8/N11/N12/N13 —
  bandwidth is cheap, simplicity and fuzzability are not).
- Keeping the surface minimal is what makes the cross-OS gap *closable*: the
  whole core runs as two loopback instances on the Mac, and the residual Windows
  behaviour fits one CI job + one manual checklist (`plan/README.md`).

## Consequences

- **Binding on the Phase 4 planner:** N1–N15 are the deferral list; the planner
  justifies each as out-of-scope and does not schedule them for v1.
- One item is explicitly *not* closed here and is handed forward, not deferred:
  **adaptive vs fixed-32 KiB block size** → `merkle-researcher` (OQ-1). Rejecting
  Option C does not pre-decide that; it only declines delta indexes and N-device.
- The escape hatch for N13 (CDC) is an `algo_version`/`chunking_scheme` field in
  the INDEX/chunk-map message so CDC can land later without a flag day
  (`rsync-or-librsync` ADOPT-3) — owned by `protocol-researcher`.
- N4 (no persistent DB) creates a real gap — detecting deletions across a daemon
  restart needs a persisted last-synced tree snapshot. That is **not** a reason to
  re-add the DB; it is open question OQ-5 (a local-only gob snapshot, GR-7) for
  `tree-critic` / `merkle-researcher`, and risk R-5 in the register.
