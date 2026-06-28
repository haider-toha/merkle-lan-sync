# PR-1 — The wire protocol: framing, message catalogue, payload grammars, handshake state machine

- Phase / role: Phase 2 — protocol-researcher
- Severity: **high** (this is the wire ABI; a framing off-by-one desynchronises the
  whole stream, and an unbounded length read is a textbook OOM/DoS — SR-12)
- Status: open (research finding; backs `decisions/protocol/message-type-enumeration.md`
  and the Phase 0 `framing-format.md` / `message-type-codes.md`)
- Reads-first honoured: `docs/audit/rules/` (SR-6, SR-12; GR-7, GR-8),
  `docs/audit/findings/synthesis/problem-space-map.md`,
  `docs/audit/decisions/phase0/{framing-format,message-type-codes,transport-security-tofu-vs-plaintext}.md`,
  `.claude/skills/merkle-sync/SKILL.md` §5, `docs/audit/findings/literature/syncthing-bep.md`,
  `docs/audit/findings/codebases/syncthing-source.md`.
- Evidence: BEP framing/types independently re-verified at
  [BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html) (accessed 2026-06-28);
  source pins `@2775f424f228` / `v2.1.1` inherited from the literature/codebase findings.

---

## 1. Claim

Merkle Sync's wire protocol is **seven message types over a single TLS-1.3 session**,
each carried in a `[4-byte big-endian length][1-byte type][payload]` frame with a
hard **16 MiB max-length guard checked before any allocation**. The framing is
self-delimiting, typed, DoS-resistant, and byte-identical on Mac and Windows. Seven
types (`HELLO INDEX INDEX_UPDATE REQUEST RESPONSE PING CLOSE`) cover the full
adapted-BEP loop; the Merkle root hash and a feature-negotiation field ride inside
`HELLO` so the catalogue needs no eighth type and stays forward-compatible.

## 2. Framing (the frame is dumb and auditable)

```
+----------------------+--------------+---------------------------+
| length: uint32 BE    | type: 1 byte | payload: (length - 1) B   |
| = 1 + len(payload)   |  (MsgType)   |                           |
+----------------------+--------------+---------------------------+
```

Rules (SR-12, GR-7, GR-8; `framing-format.md`):

- `length` counts **`type byte + payload`**. An empty-payload message (`PING`) has
  `length == 1`. Length `0` is **rejected** (every frame has at least a type byte).
- All multi-byte integers are **big-endian** (`encoding/binary.BigEndian`). Verified
  convention: "the length words are in network byte order (big endian)" and
  "Non protocol buffer integers are always represented in network byte order"
  ([BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html), accessed 2026-06-28).
- **`MaxFrameLen = 16 MiB`.** `ReadFrame` does `io.ReadFull(r, hdr[:4])`, decodes the
  `uint32`, and **rejects `length == 0 || length > MaxFrameLen` before allocating the
  body**, returning the typed sentinel `ErrFrameTooLarge`; the transport then drops
  that peer. Never desync, never wedge, never allocate on a bad length. "one must
  include a maximum message size 'sanity check' in the socket reading code"
  ([Stephen Cleary, *Message Framing*](https://blog.stephencleary.com/2009/04/message-framing.html), accessed 2026-06-28).
- **Both** the 4-byte header and the body are read with `io.ReadFull` — a single
  `conn.Read` may return a partial message; treating its return as a whole message is
  a stream-desync bug (GR-8). Bulk file content is streamed as many `RESPONSE` chunk
  frames, each well under the ceiling — never one giant frame.
- **Never decode `gob` from the network** (GR-7): "The gob package is not designed to
  be hardened against adversarial inputs … outside the scope of Go's security policy"
  ([encoding/gob docs](https://pkg.go.dev/encoding/gob), accessed 2026-06-28). The
  frame uses `encoding/binary`; payloads use a hand-rolled binary codec.

Why 16 MiB (vs BEP's 500 MB `MaxMessageLen`, `protocol.go:43-44`): BEP can ship a
whole `Index` as one message; Merkle Sync streams chunks and sends `FileInfo`s in
bounded batches, so a far smaller ceiling is safe and a tighter DoS bound.

`WriteFrame(w, type, payload)` / `ReadFrame(r) (type, payload, error)` live in
`internal/protocol/framing.go`.

## 3. Message catalogue (finalised — `decisions/protocol/message-type-enumeration.md`)

`type MsgType byte`. `0x00` reserved/invalid; `0x08`+ reserved/unassigned.

| code | type | direction | purpose |
|---|---|---|---|
| `0x00` | *(invalid)* | — | never sent; receipt is a **fatal** protocol error → drop conn |
| `0x01` | `HELLO` | both, once | handshake: proto version, DeviceID, folder id, **root hash**, **feature flags** |
| `0x02` | `INDEX` | both | full snapshot: a set of wire-`FileInfo` for the folder |
| `0x03` | `INDEX_UPDATE` | both | incremental `FileInfo` deltas since the last index (the SR-6 post-local-change broadcast) |
| `0x04` | `REQUEST` | puller→source | want bytes: path, content hash, offset, length |
| `0x05` | `RESPONSE` | source→puller | chunk data (or a typed error) for a prior `REQUEST` |
| `0x06` | `PING` | both | keepalive / liveness — empty payload (`length == 1`) |
| `0x07` | `CLOSE` | both | graceful shutdown — optional reason |

Modelled on BEP's verified 8-type enum "CLUSTER_CONFIG, INDEX, INDEX_UPDATE,
REQUEST, RESPONSE, DOWNLOAD_PROGRESS, PING, CLOSE"
([BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html), accessed 2026-06-28):
we drop `CLUSTER_CONFIG` (N-device, replaced by a leaner `HELLO`) and
`DOWNLOAD_PROGRESS` (swarm, N9), per synthesis §2.2.

**Unknown-type policy (split):** `0x00` → **fatal** (drop — signals corruption /
zero-init; reserving the zero value makes a stray zero byte a loud error, not a silent
mis-dispatch — `message-type-codes.md`). `0x08`+ unknown → **skip the frame and
continue** (the length prefix makes the payload safe to discard), preserving
forward-compat — Syncthing's habit: unknown message types are skipped
(`protocol.go:412-415`, `syncthing-source.md` §1a).

## 4. Payload grammars (hand-rolled binary; big-endian; `len-pfx` = `uint16` length prefix then bytes)

> The **wire `FileInfo`** byte grammar inside `INDEX`/`INDEX_UPDATE` is owned by
> `decisions/merkle/leaf-shape-and-structural-hash.md` §D.3 (merkle-researcher). This
> finding fixes only the *message envelope around* it. Note the wire `FileInfo`
> carries the **full canonical path + size + mtime** (the peer needs them to place the
> file, plan transfer, and run the SR-7 tiebreaker) even though `size`/`mtime` and the
> parent-committed leaf name are *excluded from the structural hash* — i.e. the wire
> encoding is a **superset** of the structural-hash encoding.

```
HELLO        = protoVersion : uint16
             | deviceID     : [32]byte           # re-asserts the TLS-pinned identity
             | folderID     : len-pfx (UTF-8)
             | rootHash     : [32]byte            # current Merkle root (SR-5 short-circuit)
             | featureFlags : uint32              # algo_version / chunking-scheme negotiation (OQ-10)

INDEX        = folderID : len-pfx
             | count    : uint32
             | count × wireFileInfo               # full snapshot

INDEX_UPDATE = folderID : len-pfx
             | count    : uint32
             | count × wireFileInfo               # changed FileInfo only (SR-6)

REQUEST      = reqID       : uint32               # correlates RESPONSE→REQUEST
             | path        : len-pfx (canonical NFC, forward-slash)
             | contentHash : [32]byte             # expected SHA-256 of the file content
             | offset      : uint64
             | length      : uint32               # ≤ MaxFrameLen - overhead

RESPONSE     = reqID     : uint32
             | errorCode : uint8                  # 0=OK,1=GENERIC,2=NO_SUCH_FILE,3=INVALID_FILE
             | data      : remaining bytes         # chunk bytes when errorCode==0

PING         = (empty)                            # length == 1

CLOSE        = reason : len-pfx (UTF-8, optional)
```

Notes:
- `REQUEST`/`RESPONSE` mirror BEP's verified shape (`Request{id,name,offset,size,hash}`
  / `Response{id,data,code}`, `bep.proto:207-232`) minus `from_temporary`/`block_no`
  (swarm/adaptive-block features we defer). `reqID` lets multiple requests be
  outstanding on one connection and correlated to responses (BEP's `awaiting` map
  pattern, `protocol.go:357-384`).
- `errorCode` codes match BEP's `ErrorCode` enum (`NO_ERROR/GENERIC/NO_SUCH_FILE/
  INVALID_FILE`) so a source that no longer has a requested block answers cleanly
  rather than hanging the puller.
- `featureFlags` is the **fail-closed** negotiation hook: if the two peers' required
  feature bits are incompatible (e.g. one requires a CDC chunking scheme the other
  lacks, N13), they refuse to sync rather than diverge silently. This is how the
  structural-hash recipe (`decisions/merkle/…` §D.3 Consequences) and chunking
  (`decisions/merkle/chunking-*.md`) evolve without a flag day.

## 5. Connection lifecycle / handshake state machine

```
        dial(peerAddr)  ──┐                        ┌── accept()
                          ▼                        ▼
                 ┌─────────────────────────────────────────┐
                 │ 1. TLS 1.3 handshake                     │
                 │    VerifyConnection pins peer DeviceID    │   unknown ID ─► DROP
                 │    against the allow-list (PR-7)          │
                 └───────────────────┬─────────────────────┘
                                     ▼
                 ┌─────────────────────────────────────────┐
                 │ 2. exchange HELLO (both sides)            │
                 │    verify HELLO.deviceID == TLS-pinned ID │   mismatch ─► DROP
                 │    verify protoVersion + featureFlags OK  │   incompat ─► CLOSE(reason)
                 │    verify folderID matches                │
                 └───────────────────┬─────────────────────┘
                                     ▼
                 ┌─────────────────────────────────────────┐
                 │ 3. compare HELLO.rootHash                 │
                 │    equal  ─► folders converged: skip INDEX │──► steady state
                 │    differ ─► exchange INDEX (full FileInfo)│
                 └───────────────────┬─────────────────────┘
                                     ▼
                 ┌─────────────────────────────────────────┐
                 │ 4. local Merkle diff (prune-equal); per   │
                 │    differing leaf: VV-compare (PR-2) →     │
                 │    pull (REQUEST/RESPONSE) / push / conflict│
                 │    (PR-3) / tombstone (PR-4)               │
                 └───────────────────┬─────────────────────┘
                                     ▼
        steady state:  on a *confirmed local change* → INDEX_UPDATE (SR-6, PR-6)
                       PING keepalive; CLOSE on shutdown
```

Key protocol invariants surfaced by the state machine:

- **Identity is pinned at the TLS layer (step 1) and re-asserted in-band (step 2).**
  Authentication never depends on `HELLO` alone; discovery is only a hint (PR-7).
- **The root-hash short-circuit (step 3)** makes "already converged ⇒ no index, no
  transfer" explicit, directly serving SR-5 and avoiding needless index shipping on
  every reconnect (synthesis §6.4). A cold-start (wiped) device must still send/accept
  `INDEX` and run the reseed before broadcasting any `INDEX_UPDATE`
  (`vv-counter-seeding.md`).
- **`INDEX_UPDATE` is the only steady-state outbound on a local edit, and only after a
  *confirmed local change*** (SR-6, PR-6) — never on applying a received file (no
  echo loop).

## 6. Test obligations (SR-12, GR-8; `framing_test.go`, `messages_test.go`)

1. Split a valid frame across N read boundaries via `iotest.OneByteReader` → correct
   reassembly (partial-read survival).
2. Oversized length (`> 16 MiB`) → `ErrFrameTooLarge`, **no large allocation**,
   connection dropped (assert allocation bound).
3. `length == 0` → typed error, conn dropped.
4. Type `0x00` → **fatal** (drop); type `0x08` → **skip and continue** (next valid
   frame still parses) — proves the split unknown-type policy.
5. Per-type encode/decode round-trip for all seven types.
6. `HELLO.rootHash` equal vs differ drives the two-instance "skip-INDEX when
   converged" path (integration, SR-5).

## 7. Cross-references

- Decisions: `protocol/message-type-enumeration.md`, `protocol/transport-security-tofu-confirm.md`,
  Phase 0 `framing-format.md` / `message-type-codes.md`;
  `merkle/leaf-shape-and-structural-hash.md` (wire-`FileInfo` byte grammar / VV encoding).
- Rules: SR-6 (INDEX_UPDATE = post-local-change broadcast), SR-12 (max-length guard),
  GR-7 (no gob), GR-8 (`io.ReadFull` + guard).
- Findings: PR-2 (VV compare driving step 4), PR-3 (conflict), PR-4 (tombstones),
  PR-6 (sync-loop invariant on INDEX_UPDATE), PR-7 (TLS/TOFU identity in steps 1–2).
