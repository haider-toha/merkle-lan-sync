# Decision: 1-byte message-type code assignment for the wire frame

- Area: phase0 / protocol
- Status: decided (Phase 0 baseline — protocol-researcher finalises the catalogue
  in Phase 2; framing stays backward-compatible)
- Date: 2026-06-28
- Decider: contracts-architect

## Context

`docs/audit/decisions/phase0/framing-format.md` fixed the frame as
`[4-byte BE length][1-byte type][payload]` and explicitly deferred the *message
catalogue* to the Phase 2 protocol-researcher, naming a tentative seven types
(HELLO, INDEX, INDEX_UPDATE, REQUEST, RESPONSE, PING, CLOSE). `docs/audit/plan/structure.md`
pins those same seven into `internal/protocol/messages.go`.

The distilled spec I am writing now — `.claude/skills/merkle-sync/SKILL.md` — must
contain a concrete, testable **message-type table**: a spec with a `type` byte but
no assigned values is not implementable and cannot be round-trip tested. So I must
pin the actual byte values. That assignment is the wire ABI: once two peers ship,
changing a code breaks interop. It is therefore a log-first choice, even though the
*set* of types is inherited from the framing decision.

This decision only assigns the integer codes and reserves the zero value. It does
not invent new message types; protocol-researcher may add, rename, or split types
in Phase 2 (e.g. a separate REQUEST/RESPONSE for index vs chunk), and the 1-byte
type space (256 values) leaves ample room to do so without a frame change.

## Options (scored 1–5, 5 = best)

### Option A — Sequential `0x01`..`0x07`; reserve `0x00` as an invalid/never-sent type (PROPOSED)

| code | type | direction / purpose |
|---|---|---|
| `0x00` | (reserved — invalid) | never sent; `ReadFrame` of type `0x00` is a protocol error |
| `0x01` | `HELLO` | handshake: protocol version + DeviceID + folder id |
| `0x02` | `INDEX` | full index snapshot: a set of `FileInfo` |
| `0x03` | `INDEX_UPDATE` | incremental `FileInfo` deltas since last index |
| `0x04` | `REQUEST` | request bytes of a file: path + content_hash + offset + length |
| `0x05` | `RESPONSE` | chunk data for a prior REQUEST |
| `0x06` | `PING` | keepalive / liveness; empty payload (frame length `1`) |
| `0x07` | `CLOSE` | graceful shutdown; optional reason payload |

- Correctness: **5** — reserving `0x00` means the Go zero value of a `byte` is an
  *explicit* protocol error, so a truncated frame, an uninitialised struct, or a
  garbage zero byte cannot be silently mis-dispatched as a valid message.
- Concurrency-safety: **5** — codes are stateless constants; dispatch is a pure
  function of the type byte. No shared state.
- Testability: **5** — trivial table-driven encode/decode round-trip; `0x00` and
  `0x08`+ give clean "unknown/invalid type → typed error" negative cases.
- Cross-platform: **5** — a single byte has no endianness; identical on Mac/Windows.

### Option B — Sequential `0x00`..`0x06` (use the zero value for HELLO)

- Correctness: **3** — `0x00 == HELLO` makes the byte zero value a *valid* message.
  A truncated read, a zeroed buffer, or a default-constructed envelope decodes as a
  legitimate HELLO instead of erroring — a silent-misdispatch footgun on the exact
  trust boundary the framing decision hardened.
- Concurrency-safety: **5**. Testability: **4** (loses the clean zero-is-invalid
  test). Cross-platform: **5**. Rejected for the zero-value hazard.

### Option C — Range-partitioned codes (control `0x00–0x0F`, bulk-data `0x10–0x1F`, …)

- Correctness: **5** — lets a dispatcher branch on a range (control vs data).
- Concurrency-safety: **5**. Cross-platform: **5**.
- Testability: **4** — more surface to track for seven types.
- **Cost:** partitioning is machinery for a catalogue that does not need it yet
  (YAGNI); seven contiguous codes are simpler to read and audit. Deferred — if
  Phase 2 grows many types, a range scheme can be adopted then within the same
  1-byte space.

## Decision

Adopt **Option A**: sequential `0x01`..`0x07` for the seven Phase 0 types, with
`0x00` permanently reserved as an invalid/never-sent type that `ReadFrame` rejects
with a typed error. Codes `0x08`+ are unassigned and reserved for Phase 2.

## Rationale

- Reserving the zero value turns the single most likely corruption/initialisation
  bug (a stray `0x00` type byte) into a loud, typed protocol error rather than a
  silent mis-dispatch — the same "simplicity is a correctness property" logic that
  drove the framing decision.
- Sequential codes are the minimum machinery for seven types; the 1-byte space
  keeps 248 codes free, so protocol-researcher can extend the catalogue in Phase 2
  without any frame change (forward-compatible exactly as the framing decision
  promised).
- Single bytes are byte-order-free, so Mac and Windows agree with zero effort.

## Consequences

- `internal/protocol/messages.go` declares these as typed constants
  (`type MsgType byte`); `messages_test.go` round-trips each and asserts that
  `0x00` and an unassigned code (`0x08`) decode to a typed `ErrUnknownMsgType`.
- The SKILL message-type table and the framing spec in
  `.claude/skills/merkle-sync/SKILL.md` use these exact values.
- Cross-references `docs/audit/decisions/phase0/framing-format.md` (frame layout,
  `MaxFrameLen`, `ErrFrameTooLarge`), SR-12 (length guard), and GR-7/GR-8 (binary
  framing, `io.ReadFull`, no `gob` from the network).
- Open to Phase 2 (protocol-researcher): final catalogue — possibly splitting
  REQUEST/RESPONSE into index-request vs chunk-request, or adding a CLOSE reason
  enum. Any addition takes an unassigned code; existing codes are frozen.
