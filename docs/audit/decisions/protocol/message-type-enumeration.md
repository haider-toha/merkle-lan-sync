# Decision: finalise the message-type enumeration (the ~7-type catalogue)

- Area: protocol (Phase 2 — protocol-researcher)
- Status: decided (finalises the Phase 0 baseline
  `docs/audit/decisions/phase0/message-type-codes.md`; closes synthesis **OQ-10**)
- Date: 2026-06-28
- Decider: protocol-researcher (Phase 2)
- Task mandate: "DECIDE & LOG (decisions/protocol/): … the message-type
  enumeration." Contract: "extend the `0x08`+ range, never renumber existing codes."

## Context

Phase 0 (`framing-format.md`, `message-type-codes.md`) fixed the frame as
`[4-byte BE len][1-byte type][payload]`, reserved `0x00` as invalid, and assigned a
**tentative** seven types (`HELLO INDEX INDEX_UPDATE REQUEST RESPONSE PING CLOSE`),
explicitly deferring the *final* catalogue to me. The type byte is the **wire ABI**:
once two peers ship, changing a code breaks interop. So even though the *set* is
inherited, finalising the codes, their semantics, and the unknown-type policy is a
log-first choice.

The substantive question is **what the catalogue must support**, given Merkle
Sync's novelty: a recursive **Merkle tree** is the change-detection layer (synthesis
§2.1), replacing Syncthing's `index_id`/`sequence` delta machinery (N5). Syncthing
needs 8 types including `CLUSTER_CONFIG` (N-device) and `DOWNLOAD_PROGRESS` (swarm),
both of which we deliberately do not build (synthesis §2.2). The open design choice
is whether v1 needs a *dedicated wire message to walk subtree hashes*, or whether
`INDEX` + a root-hash short-circuit suffices.

Reference (verified current): Syncthing's post-auth catalogue is exactly
"CLUSTER_CONFIG, INDEX, INDEX_UPDATE, REQUEST, RESPONSE, DOWNLOAD_PROGRESS, PING,
CLOSE" ([BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html), accessed
2026-06-28), and unknown message types are **skipped** for forward-compatibility
(`protocol.go:412-415 @2775f424f228`, `syncthing-source.md` §1a).

## Options (scored 1–5; axes = correctness / concurrency-safety / testability / cross-platform)

### Option A — confirm the seven; fold the Merkle root hash + feature negotiation into `HELLO`; reserve `0x08`+ (CHOSEN)

| code | type | payload (finalised — exact grammar in `PR-1-wire-protocol-and-framing.md`) |
|---|---|---|
| `0x00` | *(reserved — invalid)* | never sent; receipt is a **fatal** protocol error → drop conn |
| `0x01` | `HELLO` | `protoVersion u16`, `deviceID [32]`, `folderID` (len-pfx), **`rootHash [32]`**, **`featureFlags u32`** (algo/CDC negotiation) — sent once, first frame post-TLS |
| `0x02` | `INDEX` | full snapshot: `folderID` + `count u32` + `count × FileInfo` |
| `0x03` | `INDEX_UPDATE` | incremental `FileInfo` deltas since the last index (the SR-6 "broadcast after a confirmed local change" carrier) |
| `0x04` | `REQUEST` | `reqID u32`, `path` (len-pfx), `contentHash [32]`, `offset u64`, `length u32` |
| `0x05` | `RESPONSE` | `reqID u32`, `errorCode u8`, `data` (rest of frame) |
| `0x06` | `PING` | empty payload (`len == 1`) |
| `0x07` | `CLOSE` | optional UTF-8 reason (len-pfx) |
| `0x08`+ | *(reserved — unassigned)* | **skipped** by an older peer (forward-compat); candidates named below |

Two refinements vs Phase 0, both forward-compatible (no renumber):
1. **`HELLO` carries `rootHash` + `featureFlags`.** The Merkle root hash lets a
   reconnect where roots already match **skip `INDEX` entirely** (synthesis §6.4 /
   SR-5 convergence oracle). `featureFlags` is the fail-closed `algo_version`/
   chunking-scheme negotiation OQ-10 asks for, so CDC (N13) and any structural-hash
   recipe change (`decisions/merkle/leaf-shape-and-structural-hash.md` §D.3
   Consequences) land **without a flag day** and never as a silent change.
2. **Unknown-type policy is split:** `0x00` → **fatal** (drop conn — it signals
   corruption/zero-init); `0x08`+ unknown → **skip the frame and continue**
   (length-prefixed ⇒ safe to discard), preserving forward-compat. This refines
   Phase 0's "`0x08` → `ErrUnknownMsgType`": the typed error still exists, but the
   *dispatcher* drops on `0x00` and skips on `0x08`+.

- correctness **5** — covers handshake, full + incremental state exchange, chunk
  transfer, liveness, teardown; the root-hash short-circuit makes "already
  converged ⇒ no work" explicit; fail-closed feature negotiation prevents
  silent-divergence on version skew.
- concurrency **5** — codes are stateless constants; dispatch is a pure function of
  the type byte; no shared parser state (one reader goroutine per conn, GR-4).
- testability **5** — per-type encode/decode round-trip; `0x00`→fatal and
  `0x08`→skip are clean negative cases; `HELLO.rootHash` equal/!= drives a
  two-instance "skip-index when converged" test.
- cross-platform **5** — a single byte has no endianness; multi-byte payload ints
  are big-endian (identical Mac/Windows).

### Option B — the seven **plus** a dedicated `TREE_HASH` / `SUBTREE_REQUEST` pair (`0x08`, `0x09`) to walk subtree hashes over the wire in v1
- correctness **5** (could make the wire diff truly O(differences) without shipping
  the whole index), concurrency **5**, testability **4** (a recursive request/response
  walk is more state to test), cross-platform **5**.
- **Cost / why deferred:** this is the genuine "exploit the Merkle tree on the wire"
  optimisation, but it is *premature* for a 2-device LAN tool where a full `INDEX` of
  a small folder is cheap and the local diff already gets the O(differences) property
  (SKILL §2; synthesis §6.4 says rely on the tree diff, keep only a per-peer last
  root hash). Adopting it now adds a recursive protocol sub-machine before we have
  evidence it's needed. **Reserved as the first `0x08`+ candidate** (`TREE_NODE`
  request/response) for a future version, behind `featureFlags`.

### Option C — collapse `INDEX` + `INDEX_UPDATE` into one `INDEX{full|delta flag}`
- correctness **4**, concurrency **5**, testability **4**, cross-platform **5**.
- **Cost:** saves one code at the price of an in-payload mode flag and a subtler
  invariant (a "delta" with no prior full index is undefined). Two explicit types
  are easier to reason about, fuzz, and match Syncthing's proven split. The 1-byte
  space has 248 free codes — saving one is a false economy. Rejected.

### Option D — adopt BEP's 8 types verbatim (keep `CLUSTER_CONFIG`, `DOWNLOAD_PROGRESS`)
- correctness **5** (battle-tested), concurrency **5**, testability **3**, cross-platform **5**.
- **Cost:** `CLUSTER_CONFIG` carries N-device/`index_id`/`introducer` machinery (N5,
  N6) and `DOWNLOAD_PROGRESS` is swarm fetch (N9) — both on the deliberate
  "NOT built" list (synthesis §2.2). Importing them is dead surface and invites the
  delta-index desync failure mode (`syncthing-bep` §10.3). Rejected for scope.

## Decision

Adopt **Option A**: the finalised catalogue is `0x01 HELLO`, `0x02 INDEX`,
`0x03 INDEX_UPDATE`, `0x04 REQUEST`, `0x05 RESPONSE`, `0x06 PING`, `0x07 CLOSE`,
with `0x00` permanently reserved/invalid and `0x08`+ reserved/unassigned.
`HELLO` carries `rootHash` + `featureFlags`. Unknown-type policy: `0x00` fatal,
`0x08`+ skipped. Existing codes are **frozen**; future types take the next free
`0x08`+ code behind a `featureFlags` bit. First reserved future candidate:
`TREE_NODE` request/response (Option B) for a wire-level Merkle subtree walk.

## Rationale

- Seven types are the minimum that cover the full BEP loop adapted to a 2-device
  Merkle engine; the tree subsumes the delta-index machinery (synthesis §2.1), so we
  shed `CLUSTER_CONFIG`/`DOWNLOAD_PROGRESS` and gain a root-hash short-circuit
  instead.
- Folding `rootHash` + `featureFlags` into `HELLO` (rather than minting new types)
  keeps the catalogue at seven while delivering the SR-5 short-circuit and the OQ-10
  forward-compat field — extensibility without a frame change, exactly as the framing
  decision promised.
- `0x00`-fatal / `0x08`+-skip is the right asymmetry: a zero type byte is almost
  always corruption or zero-init (loud failure is correct), whereas an unassigned
  high code is almost always a newer peer (graceful skip preserves rolling-upgrade
  interop, matching Syncthing's habit).

## Consequences

- Drives `internal/protocol/messages.go` (`type MsgType byte` constants + envelope
  structs) and `messages_test.go` (per-type round-trip; `0x00`→fatal `0x08`→skip).
- The `featureFlags`/`algo_version` bit is the negotiation point for the chunking
  scheme (`decisions/merkle/chunking-*.md`) and any future structural-hash recipe
  change — a **fail-closed** handshake: if the peers' required feature sets are
  incompatible, refuse to sync rather than diverge silently.
- `INDEX`/`INDEX_UPDATE` payloads carry the **wire `FileInfo`** envelope, whose
  byte grammar is owned by `decisions/merkle/leaf-shape-and-structural-hash.md` §D.3
  (VV = `count u16` + sorted `(id u64, value u64)` pairs); this decision fixes only
  the *message framing around* those `FileInfo`s. Note the wire `FileInfo` carries
  the **full canonical path + size + mtime** (the peer needs them for placement,
  transfer planning, and the SR-7 tiebreaker) even though `size`/`mtime` and the
  parent-committed name are *excluded from the structural hash* — wire encoding ⊃
  structural-hash encoding.
- Cross-references: `framing-format.md`, `message-type-codes.md` (Phase 0),
  `transport-security-tofu-confirm.md` (HELLO re-asserts DeviceID), SR-6 (INDEX_UPDATE
  is the post-local-change broadcast), SR-12/GR-7/GR-8 (framing), and findings
  `PR-1-wire-protocol-and-framing.md` (full grammars + state machine).
