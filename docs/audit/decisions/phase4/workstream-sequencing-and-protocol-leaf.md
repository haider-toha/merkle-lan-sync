# Decision — Workstream sequencing and where the `internal/protocol` foundational leaf is built

- Phase / role: Phase 4 — planner
- Date / access date: 2026-06-28
- Status: decided; acted on in `docs/audit/plan/implementation-plan.md`
- Reads-first honoured: `docs/audit/findings/design/consolidated/overview.md` (CDD-1..CDD-8),
  `docs/audit/findings/synthesis/problem-space-map.md` (§3 DAG, R-1, R-5),
  `docs/audit/plan/structure.md` (per-file workstream annotations + acyclic DAG),
  `docs/audit/decisions/STEERING.md`, the Phase 2 findings (MK-1..6, PR-1..7),
  `docs/audit/rules/{sync,go,crossplatform}-rules.md`, `.claude/skills/merkle-sync/SKILL.md`.

## Context

The roster (`plan/agent_roster.md`) and this task pin a four-workstream spine with
fixed sync-invariant acceptance criteria and the dependency order
**WS-1 → {WS-2, WS-3} → WS-4**. But the authoritative import DAG
(`structure.md`, synthesis §3.A) has **two** foundational leaves, not one:

```
pathnorm ─┐
          ├─► merkle ─┐
protocol ─┘            ├─► reconcile ─► cmd/msync
   ▲  ▲                │
   │  └──── transport ─┤
   └────── discovery ──┘
```

`internal/protocol` is a stdlib-only leaf that owns the wire framing, the
message-type catalogue, and the **two shared identity types `VersionVector` and
`DeviceID`**. Crucially, **`merkle` (WS-1) imports `protocol`**: a `FileInfo`
carries a `VersionVector`, which is a `map[DeviceID]uint64`, and the structural
hash commits to the VV (MK-3, SKILL §1). So the merkle state model cannot compile
or be hashed without `VersionVector` + `DeviceID` already existing. Meanwhile
`transport` (WS-2) needs framing + `DeviceIDFromCert`, and `reconcile` (WS-4)
needs the message envelopes.

`structure.md` already splits the protocol files across workstreams
(`versionvector.go` = "WS-1 (needed early)"; `framing.go`/`messages.go`/
`deviceid.go` = "WS-2"). That split is a real tension the planner must resolve:
the spine says "WS-1 first," yet WS-1 depends on protocol types the annotations
hand to WS-2. Two further facts raise the stakes:

- **R-1 (synthesis §5, STEERING §B.1) is the single highest-probability convergence
  bug**: non-deterministic / cross-platform-divergent serialization. It lives in
  the byte-exact codec + RFC-6962 domain separation (MK-1) and the big-endian
  framing (PR-1) — i.e. squarely in the `protocol` + `merkle` codecs. Front-loading
  that surface into standalone, Mac-runnable unit tests de-risks the whole project.
- **CDD-1 (concurrency rule amendments) and CDD-8 (SR-5 "at quiescence" wording)**
  are cross-cutting *doc* edits the consolidator routed to "Rules
  (go-rules / sync-rules)", logically prior to the network/engine workstreams.

## Options (each scored on correctness / concurrency-safety / testability / cross-platform; 1–5, higher better)

### Option A — Keep exactly four workstreams; split `internal/protocol` across WS-1 and WS-2 per `structure.md`
`VersionVector` + the `DeviceID` *type* land at the start of WS-1 (because merkle
imports them); framing + messages + `DeviceIDFromCert` + TLS land in WS-2. The
`wireFileInfo` payload codec is a WS-1/WS-2 seam.

- correctness **4** — compiles incrementally and respects the DAG, but the `deviceid.go`
  file is split mid-file (type in WS-1, cert-derivation in WS-2), and the framing/codec
  determinism (R-1) is scattered across two workstreams instead of proven as one unit.
- concurrency-safety **3** — the CDD-1 rule amendments have no natural home; they get
  bolted onto WS-2/WS-3/WS-4 piecemeal, easy to forget.
- testability **3** — framing's split-read / oversized-length tests (a self-contained,
  network-free property) only appear in WS-2, gated behind transport scaffolding.
- cross-platform **3** — R-1's "Mac→wire→Windows→wire→Mac bit-identical" gate is split
  between the merkle codec (WS-1) and the framing/VV encoding (WS-2); no single gate.
- **Net 13.** Faithful to the literal four-WS count but fragments the one leaf the DAG
  says is foundational and disperses the highest risk.

### Option B — Add an explicit foundational **WS-0** (protocol leaf + rule amendments) beneath the four named workstreams
WS-0 builds `internal/protocol` in full (framing, message catalogue/envelope,
`VersionVector`, `DeviceID` incl. `DeviceIDFromCert`, codec primitives: len-prefix,
big-endian, RFC-6962 `0x00`/`0x01` domain-separation helpers, `MaxChunkLen` budget)
**and** folds the CDD-1/CDD-8 rule edits. WS-1..WS-4 keep their mandated acceptance
criteria verbatim and sit on WS-0. Dependency spine among the four is unchanged.

- correctness **5** — mirrors the import DAG exactly (`protocol` + `pathnorm` are the
  two leaves); `VersionVector`/`DeviceID` exist before merkle needs them; the
  `wireFileInfo` payload codec is the one documented seam (envelope in WS-0, payload
  finalized with WS-1's `FileInfo`).
- concurrency-safety **5** — the framing core, the copy-on-write VV ops (PR-2 §3), and
  the CDD-1 contract amendments are established and `-race`-tested first, so every later
  workstream builds on a settled concurrency contract.
- testability **5** — framing (split-read via `iotest.OneByteReader`, oversized/zero
  length), VV `Compare` antisymmetry (PR-2 §7), `DeviceIDFromCert` determinism, and the
  `MaxChunkLen` boundary are all standalone, network-free, Mac-runnable units.
- cross-platform **5** — the big-endian framing + deterministic VV/`FileInfo` byte
  grammar (R-1, the #1 convergence bug) is front-loaded and unit-proven before any
  feature depends on it.
- **Net 20.** Cost: one workstream beyond the literal four. Mitigated by keeping
  WS-1..WS-4 and their acceptance criteria exactly as the task mandates — WS-0 is purely
  additive scaffolding, not a re-interpretation of the spine.

### Option C — Fold the entire `protocol` leaf into WS-1 (WS-1 = pathnorm + protocol + merkle)
- correctness **3** — compiles, but conflates the wire ABI (framing/messages, which
  `transport`/`reconcile` own) with the state model; WS-1 becomes three packages.
- concurrency-safety **3** — same homeless-CDD-1 problem as Option A.
- testability **2** — WS-1 balloons; "mark the item green" becomes coarse; framing tests
  are buried inside a state-model workstream.
- cross-platform **3** — R-1 is at least unified, but inside an over-large WS-1.
- **Net 11.** Largest single workstream; worst separation of concerns.

## Decision

**Option B.** Introduce an explicit foundational **WS-0 — Protocol leaf +
concurrency/oracle rule amendments**, then the roster spine
**WS-1 → {WS-2, WS-3} → WS-4** on top of it. Net dependency order:
**WS-0 → WS-1 → {WS-2, WS-3} → WS-4**.

The four task-mandated workstreams retain their sync-invariant acceptance criteria
**verbatim**; WS-0 is additive. The `wireFileInfo` payload codec is the one
explicit seam: WS-0 ships framing + the message *type catalogue/envelope* + the
codec primitives; WS-1 finalizes the `wireFileInfo`/structural-hash byte grammar
on top of those primitives (it depends on WS-1's `FileInfo` shape).

Secondary clarifications recorded here so the plan does not re-decide them:

- **Hard compile DAG vs recommended sequence.** `transport` (WS-2) and `discovery`
  (WS-3) import only `protocol`, *not* `merkle`/`pathnorm` — so their *minimal* compile
  dependency is WS-0, not WS-1. The roster's "WS-1 → {WS-2, WS-3}" is retained as the
  **recommended implementation sequence** (build and prove the state model + the R-1
  convergence gate before the network layers, since WS-4 integration is meaningless
  without WS-1 and WS-1 carries the top risk), and is encoded in the plan's `deps`.
  Phase 5 runs sequentially in a single tree (README), so this conservative ordering
  costs nothing and matches the mandated spine.
- **OQ-2 (VV seeding) is already decided, not re-opened.** `decisions/protocol/
  vv-counter-seeding.md` (pure logical `prev+1` + cold-start `Merge` reseed + the
  `Equal`-VV-but-differing-`content_hash` backstop) stands; STEERING §C.1's lean
  matches it. The plan adopts it; no new planner decision.
- **Persisted snapshot (MK-6 / R-5 / CDD-7.1) is split as the findings already
  specify:** the snapshot persist/load *primitive* in WS-1 (`internal/merkle`), the
  startup rescan-vs-snapshot *reconcile* in WS-4 (`internal/reconcile`). No new decision.

## Rationale

The decisive axis is **correctness + cross-platform**: the import DAG names
`protocol` a foundational leaf, `merkle` depends on it, and the project's #1
risk (R-1, deterministic cross-platform serialization) plus the CDD-2 framing
budget live precisely in that leaf. Building and unit-proving it first — framing,
copy-on-write VV semantics, deterministic byte grammar, device identity — turns
the highest-probability convergence bug into a standalone, Mac-runnable gate
before any feature can depend on a wrong recipe. Option B scores highest on every
axis at the cost of a single additive workstream, and it does so **without
weakening the task's four mandated workstreams or their acceptance criteria**.
Options A and C either fragment the leaf (A) or conflate the wire ABI with the
state model (C), and both leave the CDD-1 concurrency-rule amendments homeless.

## Consequences

- The plan presents **five** workstreams (WS-0..WS-4). WS-1..WS-4 match the roster
  and this task one-for-one; WS-0 is the foundation the DAG requires.
- WS-0 must complete (build + `-race` green, framing/VV/DeviceID tests passing,
  CDD-1/CDD-8 rule edits landed) before WS-1 starts; this is the first build gate.
- The `wireFileInfo` payload codec seam is explicit: a WS-0/WS-1 hand-off the
  implementers must coordinate (envelope vs payload). The plan flags it.
- Framing acceptance appears at two levels (defense in depth, not redundancy):
  unit-level in WS-0 (raw `io` reader) and through-TLS in WS-2 (`tls.Conn`). The
  task's WS-2 criteria ("split across reads", "malformed length rejected") are kept
  on WS-2 and additionally proven in isolation in WS-0.
- If a future re-plan wants parallel implementation, WS-2/WS-3 can drop their WS-1
  sequencing dep down to WS-0 (their true minimal dep) without changing any code.
