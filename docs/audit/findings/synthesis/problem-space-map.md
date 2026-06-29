# Synthesis — Problem-space map (Phase 1 closer)

- Phase / role: Phase 1 — synthesizer (closes the problem-space map).
- Reads-first (all consumed): every file in `docs/audit/findings/literature/`
  (`merkle-tree`, `version-vectors`, `syncthing-bep`, `rsync-algorithm`,
  `cdc-chunking`), `docs/audit/findings/codebases/`
  (`syncthing-source`, `rsync-or-librsync`), and `docs/audit/rules/`
  (`sync-rules` SR-1..SR-13, `go-rules` GR-1..GR-13, `crossplatform-rules`
  XP-1..XP-6). Cross-read: the Phase 0 decisions, `docs/audit/plan/structure.md`,
  `.claude/skills/merkle-sync/SKILL.md`, `plan/README.md`, `plan/agent_roster.md`.
- Decision logged by this agent before acting: the binding scope/deferral
  boundary → `docs/audit/decisions/phase1/scope-boundary-vs-syncthing.md`
  (referenced in §2).
- Evidence policy (synthesizer contract): every claim **inherits the citation of
  its source finding** — this map introduces no new unsourced facts; it folds,
  reconciles, and prioritises what the findings already grounded. Contradictions
  between findings are resolved explicitly (§6) or escalated as open questions
  (§4), never silently dropped.
- Date / access date for inherited URLs: 2026-06-28.

> **One-paragraph orientation.** Merkle Sync = "Syncthing's Block Exchange
> `FileInfo` / version-vector / conflict-copy / tombstone / atomic-transfer
> **data model**" + "a recursive **Merkle tree + prune-equal diff** as the
> change-detection layer in place of Syncthing's persistent multi-device index
> DB and its sequence/`index_id` delta machinery" + "**ruthless removal** of the
> N-device / relay / global-discovery / GUI / encryption / compression surface."
> The tree decides *what differs* (O(log n) when little changed); version vectors
> decide *who is newer / did edits conflict*; tombstones make deletion
> first-class; everything materialises through temp→fsync→atomic-rename. The hard
> requirement that shapes every choice is **Mac↔Windows convergence**, which
> reduces to one rule: the same logical file must hash to the same bytes on both
> OSes (SR-5, SR-13).

---

## 1. Algorithm inventory

Every algorithm the findings surfaced, with its role, the layer it lives in, the
adopt/adapt/reject verdict the findings reached, and the source finding whose
citation it inherits.

| # | Algorithm | Role in Merkle Sync | Layer (pkg) | Verdict | Source finding |
|---|---|---|---|---|---|
| AL-1 | **Merkle / git n-ary directory hash tree** — dir hash = SHA-256 over name-sorted `(childName, childStructuralHash)`; root commits to whole tree | the source of truth for *what differs*; convergence oracle (equal root ⇔ converged) | `internal/merkle` (tree.go, node.go) | **ADOPT** | `merkle-tree` §2.2, §3.1; SKILL §2 |
| AL-2 | **Prune-equal-subtrees tree diff** — equal subtree hash ⇒ skip; recurse only into mismatching children | O(differences) diff, not O(n); the WS-1/SR-5 "one byte flips exactly that branch" property | `internal/merkle/differ.go` | **ADOPT** | `merkle-tree` §2.1, §5; `syncthing-source` D3-1 |
| AL-3 | **SHA-256 content hashing** — pure file bytes = `content_hash` | content identity; transfer/dedup key | `internal/merkle/hash.go` | **ADOPT** | `merkle-leaf-shape.md`; `syncthing-source` §1d |
| AL-4 | **Version vectors** (Parker 1983) — `map[DeviceID]uint64`; `Compare → {Equal,Dominates,DominatedBy,Concurrent}`; `Merge` = pointwise max; `Bump` on local authorship only | causal ordering; concurrent-vs-causal detection (the core of two-way sync) | `internal/protocol/versionvector.go` | **ADOPT ~verbatim** | `version-vectors` §2,§4; `syncthing-bep` §4; SKILL §3 |
| AL-5 | **Deterministic conflict winner** (`WinsConflict`) — invalid-loses → newer-mtime → VV `ConcurrentGreater` tiebreak | symmetric winner pick (both peers agree on the loser) | `internal/reconcile/conflict.go` | **ADOPT** | `syncthing-bep` §4.6; `syncthing-source` A2-3; SR-7 |
| AL-6 | **Conflict-copy policy** — loser renamed `<name>.sync-conflict-<UTC-date>-<time>-<deviceID>.<ext>`, never deleted, then synced as a normal file | the literal no-data-loss contract | `internal/reconcile/conflict.go` | **ADOPT** | `syncthing-bep` §5; SR-7 |
| AL-7 | **Tombstones** — `deleted=true` + bumped VV (`SetDeleted`) | deletion propagation; resurrection resistance | `internal/reconcile/tombstone.go` | **ADOPT** | `syncthing-bep` §4.5; SR-9/SR-10 |
| AL-8 | **Fixed-size content-addressed blocks** — split every N bytes, hash each, transfer only differing blocks | sub-file transfer plan (**v1 choice**) | `internal/reconcile/transfer.go` | **ADOPT (v1)** | `cdc-chunking` Opt 2; `syncthing-bep` §7; `rsync-or-librsync` §4 |
| AL-9 | **Content-defined chunking** (Rabin/LBFS; FastCDC/Gear, normalized chunking, sub-min skip) | insert/delete-shift-resistant boundaries; cross-file dedup | (deferred) | **ADAPT-LATER** (fixed shared table, never restic's randomized polynomial) | `cdc-chunking` §4,§5,§9 |
| AL-10 | **rsync rolling-checksum delta** — weak Adler-like rollsum + strong hash, all-offset search, signature/delta/patch | sub-file *delta* (WAN-oriented) | (none) | **REJECT** (LAN ⇒ rsync's own authors default to whole-file) | `rsync-algorithm` §3,§11; `rsync-or-librsync` DIFF-1 |
| AL-11 | **Two-tier checksum gating** — cheap weak filter gates the expensive strong hash (lazy strong sum) | scanner pre-filter: `size`+`mtime` reject before re-hashing bytes | `internal/merkle/scanner.go` | **ADOPT (the discipline)** | `rsync-or-librsync` ADOPT-1; `rsync-algorithm` §5 |
| AL-12 | **Verify-after-reconstruct** — recompute whole-file SHA-256 == expected `content_hash` *before* the atomic rename | catches reassembly/ordering/truncation/collision corruption | `internal/reconcile/transfer.go` | **ADOPT** | `rsync-algorithm` A1; `rsync-or-librsync` ADOPT-4; SR-3 |
| AL-13 | **Atomic write** — temp → `fsync` → `os.Rename` → parent-dir `fsync`; discard temp on error | no corruption on crash/kill mid-transfer | `internal/reconcile/transfer.go` | **ADOPT** | `syncthing-source` A2-5; SR-1/SR-2 |
| AL-14 | **Length-prefixed binary framing + max-length guard** — `[4-byte BE len][1-byte type][payload]`, `MaxFrameLen=16 MiB`, `io.ReadFull`, reject bad length before alloc | the wire frame | `internal/protocol/framing.go` | **ADOPT** | `syncthing-source` A2-2; `framing-format.md`; SR-12/GR-8 |
| AL-15 | **TLS 1.3 + TOFU device identity** — `DeviceID = SHA-256(cert DER)`, `VerifyConnection` pins peer ID against an allow-list | serverless auth + confidentiality + integrity | `internal/transport/{tls,identity}.go`, `internal/protocol/deviceid.go` | **ADOPT** | `syncthing-source` A2-1; `transport-security-tofu-vs-plaintext.md`; SKILL §7 |
| AL-16 | **UDP multicast discovery + heartbeat eviction** — announce `{DeviceID, addr, port}`; evict silent peers | LAN peer discovery (**a hint, never authorisation**) | `internal/discovery` | **ADOPT — implemented WS-3 (commit `fd36e70`)** | `structure.md` discovery; SKILL §7; `syncthing-source` D3-4 |
| AL-17 | **Index / IndexUpdate exchange** — full snapshot vs incremental `FileInfo` deltas | state exchange (adopt the split; delta-index `index_id`/`sequence` machinery deferred — the Merkle diff subsumes it) | `internal/protocol/messages.go` | **ADOPT split / DEFER machinery** | `syncthing-bep` §6, §6.4 |
| AL-18 | **Watcher + debounce + periodic rescan** — fsnotify advisory hints, ~150 ms debounce, rescan is truth, overflow→rescan | local change detection | `internal/reconcile/{watcher,scanloop}.go` | **ADOPT** | GR-9/GR-10; SR-11; XP-5 |
| AL-19 | **Canonical path normalisation** — forward-slash relative + NFC, Windows-unsafe escape/reject, case-fold collision index | cross-platform identity (same file ⇒ same key/hash on both OSes) | `internal/pathnorm` | **ADOPT** | XP-1..XP-6; SKILL §6 |
| AL-20 | **RFC-6962 domain separation** — prefix `0x00` to leaf, `0x01` to node before hashing | second-preimage / leaf-vs-node layout-collision resistance | `internal/merkle/codec.go` | **ADAPT — recommended spec change** (not yet in SKILL/leaf-shape) | `merkle-tree` §4.1, A2, §7(1) |
| AL-21 | **`BlocksHash` / `previous_blocks_hash` content-causality** — SHA-256 over block-hash list; "was this edit based on what I hold?" | conflict-precision refinement beyond pure VV | `internal/merkle` | **CONSIDER** | `syncthing-bep` §7.4; `syncthing-source` A2-3 |
| AL-22 | **Hybrid logical clock** — VV counter = `max(prev+1, unixNow)` | anti-rollback monotonicity across state resets | `internal/protocol/versionvector.go` | **CONTESTED** — see §6 (two findings disagree) | `version-vectors` §4.4; `syncthing-bep` §4.4 |

Notes that fold across the inventory:
- **The two "chunk" concepts must not be conflated** (`cdc-chunking` §9.1): a
  *transfer/streaming chunk* (bytes per `RESPONSE` frame, pure flow-control, keep
  ≤ `MaxFrameLen`) is independent of a *dedup/delta block* (the unit whose hash we
  compare to decide send-or-skip). "Fixed 32 KiB vs CDC" (AL-8/AL-9) is only about
  the latter. The plan/README phrase "32 KB chunk streaming" is the former.
- **Syncthing's per-file content identity is flat** (`BlocksHash`, one level),
  *not* a recursive folder Merkle tree (`syncthing-source` §1b, D3-1). The
  recursive folder tree (AL-1) is Merkle Sync's deliberate divergence and its
  central novelty (§2).

---

## 2. Novelty / scope map — what we deliberately do NOT build vs Syncthing

Binding boundary, logged in
`docs/audit/decisions/phase1/scope-boundary-vs-syncthing.md` (Option B adopted;
≥3 options scored there). This list is **binding on the Phase 4 planner's
deferral list** (synthesizer contract). The core kept is the novelty surface; the
"NOT built" column is the deferral list.

### 2.1 The novelty (what makes Merkle Sync *not* a Syncthing clone)

- A **recursive Merkle tree + prune-equal diff** (AL-1/AL-2) is the
  change-detection layer, replacing Syncthing's **persistent multi-device index
  database** and its **delta-index** lifecycle (`{index_id, sequence,
  max_sequence}`). This is not merely "simpler" — the tree *subsumes* delta
  indexes: an O(log n) subtree-hash diff tells two peers exactly which subtrees
  differ with **zero** per-change sequence bookkeeping (`syncthing-bep` §6.4;
  `merkle-tree` §5). Convergence is then a single statement: **equal root hash ⇔
  converged** (SR-5).
- Everything else is BEP's *proven* data model, kept ~verbatim because reinventing
  causality/conflict/tombstone semantics is how sync engines lose data
  (`version-vectors` FM-1..FM-6).

### 2.2 The binding "deliberately NOT built" list

| # | Not built (Syncthing has it) | Replaced by / why deferred | Owner of any future revisit |
|---|---|---|---|
| N1 | Global / cross-subnet discovery server | LAN UDP multicast only (both devices on one LAN) | out of scope (Phase 4 deferral) |
| N2 | Relays / NAT traversal | same LAN, no traversal needed | out of scope |
| N3 | GUI / web UI / REST API | headless `cmd/msync` daemon | out of scope |
| N4 | Persistent multi-device index DB (v2 SQLite / v1 LevelDB) | one in-memory Merkle tree under one RWMutex (GR-5) | (gap → OQ-5/R-5, not a DB) |
| N5 | Delta indexes (`index_id`, `sequence`, `max_sequence`) | Merkle root/subtree diff; optionally one last-synced root hash per peer | `merkle-researcher` |
| N6 | N-device cluster / introducer / `secondary` / multi-connection | 2-device, single conn; `global = WinsConflict(local, remote)` | out of scope |
| N7 | At-rest / untrusted-peer encryption (`RECEIVE_ENCRYPTED`) | TLS 1.3 in transit; LAN peers trusted | out of scope |
| N8 | LZ4 wire compression | fast LAN; framing stays forward-compatible | `protocol-researcher` (DEFER) |
| N9 | `DownloadProgress` swarm fetch from peers' temp files | 2-peer convergence needs no swarm | out of scope |
| N10 | Send-only / receive-only folder modes | send-receive only | out of scope |
| N11 | Protobuf wire format | hand-rolled `[len][type][payload]` binary (GR-7) | — |
| N12 | rsync rolling-search delta codec | fixed content-addressed blocks; tree of block hashes is the truth | — (AL-10 REJECT) |
| N13 | Content-defined chunking in v1 | fixed blocks; CDC is the *adapt-later* path behind an algo-version field | `merkle-researcher` (OQ-1) |
| N14 | `PlatformData` ownership / xattr sync | `mode` best-effort only (XP-6) | `crossplatform-researcher` |
| N15 | Human device-ID encoding flourish (Luhn/base32 chunking) | plain hex/base32 string; no GUI consumes it | — |

Sources for the whole table: `syncthing-source` §3 (D3-1..D3-4), `syncthing-bep`
§9, `rsync-or-librsync` DIFF-1/DIFF-2, `cdc-chunking` §9.3, decision
`scope-boundary-vs-syncthing.md`.

One item explicitly *handed forward rather than deferred*: **adaptive
power-of-two vs fixed-32 KiB block size** is NOT settled by this scope decision;
it is open question OQ-1 for `merkle-researcher`.

---

## 3. Dependency DAG

Two views. View A is the **compile-time package import graph** and is the
authoritative contract — it **agrees with `docs/audit/plan/structure.md`** (the
synthesizer contract requires this). View B overlays the two **data-flow
pipelines** named in the task onto those packages.

### 3.A Package import DAG (authoritative; matches structure.md)

```
internal/pathnorm ─┐                           (leaf: canonical path/Unicode/case)
                   ├─► internal/merkle ─┐       (FileInfo, tree, scanner, differ)
internal/protocol ─┘                    │
   ▲        ▲                           ├─► internal/reconcile ─► cmd/msync
   │        └──── internal/transport ───┤        (the only tree mutator; RWMutex)
   └───────────── internal/discovery ───┘
```

- **`internal/protocol`** and **`internal/pathnorm`** are the two foundational
  leaves (stdlib-only / `x/text` only). `protocol` owns the wire framing, the
  message-type catalogue, and the two shared identity types **`VersionVector`**
  and **`DeviceID`** (both needed by `merkle` *and* by the network packages);
  `protocol` **never** imports `merkle`, which is what keeps the graph acyclic
  (`structure.md` §"Dependency DAG").
- `merkle` imports `protocol` (FileInfo carries the VV) and `pathnorm`.
- `transport` and `discovery` each import `protocol` and are **siblings** —
  `discovery` does **not** import `transport` (see §3.C).
- `reconcile` imports `merkle` + `transport` + `discovery` + `protocol`; it is the
  **single writer** behind the one `RWMutex` (GR-5). `cmd/msync` wires everything
  with the root `context` (GR-2).

### 3.B Data-flow pipelines (the task's two chains, mapped to files)

**Reconciliation pipeline** (`pathnorm → scanner → tree → diff → chunk transfer →
conflict resolution`):

```
pathnorm.Canonicalize           internal/pathnorm/pathnorm.go      (XP-1, XP-2, GR-12)
  → merkle scanner              internal/merkle/scanner.go         (AL-3, AL-11; SR-11, GR-9)
  → merkle tree build/hash      internal/merkle/{tree,node,codec}.go (AL-1, AL-20)
  → merkle differ (prune-equal) internal/merkle/differ.go         (AL-2; SR-5)
  → reconcile chunk transfer    internal/reconcile/transfer.go    (AL-8, AL-12, AL-13; SR-1/2/3)
  → reconcile conflict/tombstone internal/reconcile/{conflict,tombstone}.go (AL-4..AL-7; SR-7/9/10)
```

**Transport pipeline** (`protocol framing → transport → discovery`, runtime
layering):

```
protocol framing  internal/protocol/framing.go   (AL-14; SR-12, GR-8)
  → transport     internal/transport/{conn,listener,dial,tls}.go (AL-15; GR-3, GR-4)
  ⟂ discovery     internal/discovery/*            (AL-16; GR-4)   -- sibling, not an import
```

### 3.C Resolved nuance — "transport → discovery" is a runtime layering, not an import edge

The task and roster sketch the transport chain as `protocol framing → transport →
discovery`, which reads as "discovery depends on transport." That would
contradict `structure.md`, where `discovery` and `transport` are **parallel**
siblings over `protocol`. **`structure.md` + GR-4 win** (the sketch is explicitly
"rough" per the roster). Reconciliation: discovery and transport are *coordinated
by channels through `reconcile`/`cmd`, not by importing each other* — GR-4:
"listeners do not call into each other directly; they communicate by sending
values on channels to the reconcile core." Runtime flow is `discovery` emits a
`peerEvents{DeviceID, addr}` hint → `reconcile`/`cmd` → `transport.dial(addr)` →
TLS pins the `DeviceID` (AL-15). So "framing → transport → discovery" is a *layer
ordering* (both are network layers built on the framing/identity types in
`protocol`), **not** a compile-time edge. The acyclic graph in §3.A is the binding
form.

---

## 4. Open questions

Each is either logged as a decision already, or flagged to the named Phase 2/3
owner who must enumerate ≥3 options and **log a decision before acting** (autonomy
contract). "Synthesis lean" is advisory input the findings support; it is not the
binding decision.

| ID | Question | Synthesis lean (advisory) | Owner → decision path | Evidence |
|---|---|---|---|---|
| OQ-1 | Chunking: **fixed-32 KiB vs adaptive power-of-two vs CDC**; and whole-file-vs-block transfer at all | Fixed blocks for v1 (all four findings agree); start ~64 KiB or scale by file size; **disambiguate streaming-chunk vs dedup-block**; CDC adapt-later only on measured need, with a *fixed shared* table | `merkle-researcher` → `decisions/phase2/` | `cdc-chunking` §9.3; `rsync-or-librsync` §4; `syncthing-bep` §7.3; `rsync-algorithm` §11,§13 |
| OQ-2 | VV counter seeding: **pure logical `prev+1` vs hybrid `max(prev+1, now)`** | **CONTRADICTION — see §6.1.** Lean: pure logical `prev+1` to keep SR-4 clean (mtime strictly a tiebreaker), *if* counter-rollback is independently prevented; else adopt the hybrid floor knowingly | `protocol-researcher` → `decisions/phase2/` | `syncthing-bep` §4.4, §10.1; `version-vectors` §4.4 (FM-4), §8 A5 |
| OQ-3 | VV pruning / **device-removal counter cleanup** (ghost counters) | Design `DropCounter`/`Compact` from day one; **ack-gated, never blind time/size pruning** (FM-3 unequal-pruning trap); ties to tombstone GC | `protocol-researcher` → `decisions/phase2/` | `version-vectors` FM-1 (issue #10590), FM-3, §8 A2; `syncthing-bep` §10.5 |
| OQ-4 | Exact **canonical FileInfo/VV serialization** for hashing (field order, widths, endianness, length-prefix) **+ RFC-6962 domain separation** (`0x00`/`0x01`) | Adopt domain separation (AL-20) into the structural-hash recipe; fix one byte-exact grammar; prove with a Mac↔Windows round-trip test | `merkle-researcher` → `decisions/ws1/` or `phase2/` | `merkle-tree` §4.1, §7(1,2); `version-vectors` §8 A3; `merkle-leaf-shape.md` Consequences |
| OQ-5 | **Persisted last-synced tree snapshot** for deletion detection across daemon restart (in-memory tree can't tell "deleted while down" from "never existed") | Persist a local-only snapshot (gob is allowed for *local* state, GR-7) and load on startup; reconcile rescan against it. **Do not re-add a multi-device DB** (N4) | `tree-critic` (Phase 3) → `merkle-researcher` / WS-1+WS-4 | `syncthing-source` D3-1 ("real gap … flag for tree-critic") |
| OQ-6 | **Tombstone retention period + GC** | 2-device: retain until both peers acknowledge, then GC (never GC while a live peer can carry a pre-delete version) | `protocol-researcher` → `decisions/phase2/` | SR-10; `version-vectors` FM-1; `syncthing-bep` §10.5 |
| OQ-7 | **Rename detection**: treat as delete+create vs hash-match heuristic | v1 may treat as delete+create (rsync can't detect either); `content_hash` *enables* optional hash-match detection later | `merkle-researcher` → `decisions/ws1/` | `rsync-algorithm` §9.5, §13(4); `merkle-tree` §4.6, A8 |
| OQ-8 | **`previous_blocks_hash` content-causality fast-forward** (AL-21) | Consider for conflict precision; safe to skip in v1 (treat any concurrent VV as a conflict — eager but safe) | `merkle-researcher` | `syncthing-bep` §4.6, §7.4; `syncthing-source` A2-3 |
| OQ-9 | Cross-platform confirmations: **NFC vs NFD canonical; escape-vs-reject for illegal/reserved names; case-collision policy; Windows `WithBufferSize`; debounce window; exec-bit/symlink mapping; UNC/`\\?\`/long-path** | Lean NFC (Windows/Linux majority), normalise at scan-time; refuse+flag case collisions (Syncthing posture); reversible escape for unsafe names | `crossplatform-researcher` → `decisions/crossplatform/` | XP-1..XP-6 ("confirm in Phase 2"); `syncthing-source` §1a, §1e |
| OQ-10 | Finalise the **~7-type message catalogue** + add an **`algo_version`/`chunking_scheme`** field for forward-compat | Keep the SKILL §5 7-type table; add a fail-closed algo-version field so CDC (N13) lands without a flag day; skip unknown types (Syncthing habit) | `protocol-researcher` → `decisions/phase2/` | `rsync-or-librsync` ADOPT-3; `syncthing-source` D3-2; `message-type-codes.md` |

---

## 5. Top-5 risk register

Risk · likelihood · impact · mitigation · owning workstream. Likelihood/impact
are this synthesizer's prioritisation of the findings' failure modes; each cites
the finding that grounds it. (Honourable mentions that are real but already
well-mitigated by an existing rule are listed after the table.)

| ID | Risk | Likelihood | Impact | Mitigation | Owner |
|---|---|---|---|---|---|
| **R-1** | **Cross-platform-divergent / non-deterministic structural serialization** — NFD vs NFC, stored `\`, unstable child ordering, variable integer widths, or missing leaf/node domain separation make "the same file" hash differently → roots **never converge** (SR-5 broken), or "different data" collides → silent divergence | **High** — `merkle-tree` §4.3 names this "the highest-probability convergence bug," and we are *literally* building Mac↔Windows; BEP filenames are raw bytes with no normalisation (`syncthing-bep` FM-6) | **High** — the engine cannot even tell it is done; or silently diverges | Pin one byte-exact serialization: forward-slash **NFC**, fixed widths, big-endian, length-prefixed names, sorted children, **+ RFC-6962 `0x00`/`0x01` domain separation** (AL-20); table-driven round-trip Mac→wire→Windows→wire→Mac (SR-13). Settle via OQ-4 + OQ-9 | **WS-1** (`merkle` + `pathnorm`); `merkle-researcher` + `crossplatform-researcher` |
| **R-2** | **Sync loop / watcher echo** — applying a received file makes fsnotify fire, the engine re-reads it as a "local change," bumps the VV and re-broadcasts → A→B→A→… ping-pong | **Med-High** — fsnotify *will* fire on our own temp-create+rename (`syncthing-source` A2-5; GR-9); the only question is whether the guards hold | **High** — CPU/network storm; repeated spurious conflict copies | Bump VV/broadcast **only on confirmed local authorship**, never on apply (SR-6); after apply, record the expected `content_hash` so the rescan sees no new authorship (SR-8); idempotent content-addressed apply (SR-3) — defence in depth; flow-verifier asserts "received file ⇒ zero outbound broadcasts" | **WS-4** (`reconcile`: broadcast.go, apply.go, scanloop.go) |
| **R-3** | **Tombstone resurrection / ghost VV counters** — a stale peer reconnecting with the pre-delete file resurrects it; or removed-device "ghost" counters create a *permanent* concurrent state → conflict storms | **Med** — needs offline-delete-reconnect or a device removal, but documented as the marquee long-lived bug: Syncthing #10590 reported **8,591 conflicts** | **High** — deleted files reappear (inverse data loss); conflict storms degrade the whole folder | Tombstone = `deleted=true` + bumped VV that **dominates** any stale pre-delete VV (SR-9/SR-10); retain until both peers ack, then GC (OQ-6); design `DropCounter`/`Compact` from day one, **ack-gated** (OQ-3, never blind — FM-3) | **WS-4** (`tombstone.go`) + `protocol-researcher` |
| **R-4** | **Non-atomic / interrupted-transfer corruption** — a transfer killed mid-stream leaves a half-written `dst`, or reassembly/ordering bugs produce a wrong-but-complete-looking file | **Med** — crashes/kills/disconnects happen; Windows `os.Rename` is **not** atomic the way POSIX is (SR-2 caveat) | **High** — direct user-file corruption = data loss | temp → `fsync` → atomic rename → dir `fsync`; **verify-after-reconstruct** (whole-file SHA-256 == `content_hash`) *before* the rename (AL-12); discard temp on error; platform-correct Windows replace semantics (SR-1/SR-2/SR-3) | **WS-4** (`transfer.go`) |
| **R-5** | **Persisted-state gap → deletion detection fails across restart** — the in-memory tree (no DB, N4) cannot distinguish "deleted while the daemon was down" from "never existed"; on restart a real deletion may be missed, or a remote file silently re-created | **Med** — every daemon restart is exposed; bites whenever a deletion happened while down | **Med-High** — missed deletions / resurrection / divergence; *no existing rule covers it yet* (least-mitigated risk here) | Persist a **local-only last-synced tree snapshot** (gob is fine for local state, GR-7) and load on startup; reconcile the rescan against it to derive deletions; **do not** re-introduce a multi-device DB (OQ-5) | `tree-critic` (Phase 3) → **WS-1/WS-4**; `merkle-researcher` |

**Honourable mentions (real, but already well-mitigated by an existing rule — monitored, not top-5):**
- **Framing length off-by-one / oversized length** → stream desync or OOM. Mitigated by the `MaxFrameLen` guard + `io.ReadFull` + `iotest.OneByteReader` tests (SR-12, GR-8); residual risk low. Owner WS-2.
- **Concurrency: deadlock from I/O-under-lock, or goroutine leak on peer disconnect.** Mitigated by one RWMutex with **zero I/O under the lock**, `context`+`WaitGroup` owners, and mandatory `-race` (GR-3/GR-5/GR-13); the concurrency-critic grades it. Owner WS-2/WS-4.
- **Case-collision data loss** (`File.txt` vs `file.txt` on Windows/macOS). Mitigated by refuse-+-flag, never clobber (XP-4); Windows-only, closed by the CI matrix + checklist. Owner WS-1, `crossplatform-researcher`.
- **Clock-skew via the hybrid VV clock** (AL-22). Lower impact because `Compare` stays correct regardless of seeding (a skewed clock only inflates *that device's own* future counters; a true conflict still surfaces as `Concurrent`). Tracked as OQ-2 / §6.1.

---

## 6. Contradictions between findings — resolved explicitly

Per the synthesizer contract, contradictions are resolved (state which wins and
why) or escalated as an open question; never silently dropped.

### 6.1 VV counter seeding: hybrid clock vs pure logical counter (escalated → OQ-2)

- **`syncthing-bep` §4.4/§10.1 says:** *prefer a pure logical counter
  (`Value = prev+1`)* so mtime stays *strictly* a tiebreaker per SR-4; Syncthing's
  `max(prev+1, unixNow)` "re-imports wall-clock into ordering," and a far-future
  clock mints counters that dominate honest `+1`s.
- **`version-vectors` §4.4/§8 A5 says:** *recommend adopting* the
  `max(value+1, now)` floor; it gives strict per-device monotonicity and **survives
  a state reset without counter rollback** (defends FM-4), while *not* making
  wall-clock the ordering authority (ordering is still `Compare` dominance).
- **Resolution:** this is a genuine, direct contradiction on a binding
  implementation choice. It is **not** resolved in Phase 1 — both are the
  legitimately-owned Phase 2 `protocol-researcher` decision (both findings route it
  there). **Escalated as OQ-2**, with the synthesis lean stated: *prefer pure
  logical `prev+1` to keep SR-4 maximally clean, but only if counter-rollback after
  a state wipe is independently prevented* (e.g. re-seed from the peer's vector via
  `Merge` on reconnect — `syncthing-bep` §4.4 notes a 2-device LAN tool can do
  exactly this). If that re-seed guarantee is not cheap to make hold, adopt the
  hybrid floor knowingly, documenting the skew caveat. Both findings agree the
  *causality math is correct either way*, so this affects skew/robustness, not
  correctness of conflict detection (hence R-4-adjacent, an honourable mention, not
  a top-5 risk).

### 6.2 "O(log n) diff" — over-claim vs honest bound (resolved: honest bound wins)

- **SKILL §2 / `syncthing-bep` §6.4 / general framing** call the tree diff
  "O(log n)."
- **`merkle-tree` §4.5/§5 says:** that label is only true for a *balanced binary*
  tree; a **directory hierarchy is unbalanced** (depth = filesystem nesting `D`,
  unrelated to `log N`). The honest, defensible property is **prune-equal-subtrees**
  — cost ∝ *differences*, not total size — not a strict `O(log n)`.
- **Resolution:** the honest bound **wins** for any acceptance-criterion or
  complexity claim. Test the *property* (SR-5: "one byte changed flips exactly that
  leaf's branch + root, nothing else"), not a big-O assertion. "O(log n)" may be
  used as informal shorthand for "sub-linear when little changed," but WS-1
  acceptance and the planner must state the prune-equal property, not `O(log n)`.
  (No conflict of *fact* — the findings agree on the algorithm; this aligns the
  *claim* with `merkle-tree` §4.5.)

### 6.3 Block size: plan's fixed-32 KiB vs Syncthing's adaptive (escalated → OQ-1)

- **plan/README + `structure.md`** baseline: **fixed 32 KiB**.
- **`syncthing-bep` §7.3 / `syncthing-source` D3-3** note fixed 32 KiB produces
  ~512× more `BlockInfo`s than adaptive for a 16 MiB file (index bloat), and
  Syncthing deliberately uses adaptive 128 KiB–16 MiB to bound index size.
- **Resolution:** not a contradiction of *fact* (everyone agrees fixed is simpler
  and correct; adaptive controls index size). It is a tuning trade-off **escalated
  as OQ-1** to `merkle-researcher`. The §2 scope decision deliberately does **not**
  pre-decide it — rejecting Option C (delta indexes, N-device) is independent of the
  fixed-vs-adaptive block choice.

### 6.4 No silent drops

All other findings are mutually consistent (they extensively cross-reference and
agree on the FileInfo/VV/conflict/tombstone/atomic-write/framing/TOFU core, and on
"fixed blocks v1, defer CDC, reject rsync-delta-on-LAN"). The one *recommended spec
change* — adding RFC-6962 domain separation (AL-20) — is not a contradiction with
the existing leaf-shape decision; it is an additive hardening the leaf-shape
decision explicitly left open to Phase 2 (`merkle-leaf-shape.md` Consequences →
"exact canonical serialization … merkle-researcher"). Captured as OQ-4.

---

## 7. Hand-off summary (for Phase 2 / Phase 3)

- **Binding now:** the scope/deferral boundary (§2,
  `scope-boundary-vs-syncthing.md`) and the acyclic package DAG (§3.A, =
  `structure.md`).
- **`merkle-researcher`** owns: OQ-1 (chunking), OQ-4 (canonical serialization +
  domain separation), OQ-7 (rename), OQ-8 (`previous_blocks_hash`), and the OQ-5
  rebuild side.
- **`protocol-researcher`** owns: OQ-2 (VV seeding — the §6.1 contradiction),
  OQ-3 (VV pruning), OQ-6 (tombstone retention), OQ-10 (message catalogue +
  algo-version).
- **`crossplatform-researcher`** owns: OQ-9 (NFC/NFD, escape/reject, case
  collisions, watcher buffer/debounce, mode mapping).
- **`tree-critic`** (Phase 3) owns: OQ-5 / R-5 (persisted last-synced snapshot for
  deletion-across-restart) — the most under-specified gap.
- **Risk owners:** R-1→WS-1, R-2→WS-4, R-3→WS-4+protocol, R-4→WS-4, R-5→WS-1/WS-4.
