# Decision (WS-0): message envelope codec, the wireFileInfo seam, and the split unknown-type policy

- Area: ws0 / internal/protocol (messages.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-0 implementer
- Plan items discharged: WS-0 acceptance #3 (RESPONSE chunk budget), #4 (unknown-type
  policy split + total; all 7 types round-trip).
- Reads-first: `decisions/protocol/message-type-enumeration.md`,
  `decisions/phase0/message-type-codes.md`, PR-1 §3/§4, CDD-2, GR-6/GR-7, SKILL §5,
  the WS-0/WS-1 `wireFileInfo` seam note in the implementation plan.

## Context

Seven frozen types (`0x01 HELLO`…`0x07 CLOSE`), `0x00` reserved-invalid, `0x08+`
unassigned. Payload grammars are pinned in PR-1 §4 (big-endian; `len-pfx` =
`uint16` length + bytes). Two WS-0-specific design choices remain: (1) the Go API
for encode/decode given that the **`INDEX`/`INDEX_UPDATE` body is the
`wireFileInfo` byte grammar finalized in WS-1** (an explicit seam — WS-0 must
round-trip the *envelope* without fixing the per-entry grammar), and (2) the
mechanism for the **split unknown-type policy** (`0x00` fatal / `0x08+` skip),
which must be *total* over all 256 byte values and panic-free on adversarial
payloads.

## Options (scored 1–5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — typed structs implementing a small `Message` interface; sticky-error decoder; opaque `INDEX` body; `RecvAction()` trichotomy method (CHOSEN)
Each type is a struct (`Hello`, `Index`, `IndexUpdate`, `Request`, `Response`,
`Ping`, `Close`) with `Type() MsgType` and `encode() []byte`; `DecodeMessage(t,
payload)` dispatches on `t`. Decoding uses a panic-free **sticky-error** cursor
(`ErrShortPayload` on underflow). `INDEX`/`INDEX_UPDATE` carry `FolderID string`,
`Count uint32`, and an **opaque `Body []byte`** (the concatenation of `Count`
`wireFileInfo`s, parsed in WS-1) — matching PR-1's `folderID|count|count×wireFileInfo`
with the body's internal grammar deferred. Unknown-type policy is a total method
`func (t MsgType) RecvAction() RecvAction` returning `ActionFatal` (`0x00`),
`ActionDispatch` (`0x01..0x07`), `ActionSkip` (`0x08..0xFF`); `DecodeMessage` of a
non-dispatch type returns typed `ErrUnknownMsgType`.
- correctness **5** — one struct per grammar; the opaque body honours the seam
  *exactly* (no premature per-entry framing that WS-1 would have to match); the
  trichotomy is total by construction; sticky-error decode never panics on truncated
  peer bytes (GR-7 spirit).
- concurrency-safety **5** — structs are values; encode/decode are pure; no shared
  state.
- testability **5** — per-type encode→decode round-trip; `RecvAction` table over
  `0x00/0x01../0x07/0x08/0xFF`; truncated-payload→`ErrShortPayload` negative tests.
- cross-platform **5** — `BigEndian` fixed widths + `uint16` len-prefixes; identical
  bytes Mac/Windows.

### Option B — one union struct with every field + a type tag
- correctness **3** (invalid field combinations representable), concurrency **5**,
  testability **3**, cross-platform **5**. Rejected: weak typing invites mis-encoding.

### Option C — `encoding/gob` / reflection
- **Disqualified by GR-7**: gob is outside Go's security policy for adversarial input;
  never decode it from the network. Rejected outright.

### Option D — a `map[MsgType]codec` registry of free encode/decode funcs
- correctness **4**, concurrency **4** (global mutable map unless frozen at init),
  testability **4**, cross-platform **5**. Rejected: indirection with no benefit at
  seven static types; the typed structs are more legible and fuzz-friendly.

## Decision

Adopt **Option A**. `internal/protocol/messages.go` declares `type MsgType byte`
with the eight constants (`MsgInvalid=0x00`, `MsgHello=0x01`, … `MsgClose=0x07`),
the seven envelope structs, `DecodeMessage(t MsgType, payload []byte) (Message,
error)`, the total `RecvAction()` classifier, an `ErrorCode uint8` enum
(`ErrOK=0, ErrGeneric=1, ErrNoSuchFile=2, ErrInvalidFile=3` per PR-1), and the
sticky-error decoder helper. The `RESPONSE` builder
`(Response).EncodeChecked()` (and a `Response` whose `Data` exceeds the budget on
plain `encode`) enforces `len(Data) ≤ MaxChunkLen` → `ErrChunkTooLarge` on the
**sender** (CDD-2). `INDEX`/`INDEX_UPDATE` keep an opaque `Body []byte`; WS-1 owns
its internal `wireFileInfo` grammar.

`CLOSE` always encodes a `uint16` reason-length prefix (0 for no reason) for a
canonical round-trip, and *also* tolerates a fully empty payload as `reason=""` on
decode. `PING` has an empty payload (`length==1`).

## Rationale

- Typed structs + a pure codec give a fuzz-friendly, panic-free wire layer at the
  trust boundary (GR-7), and the *opaque INDEX body* is the minimal honest
  expression of the WS-0/WS-1 seam — WS-0 proves the envelope round-trips while WS-1
  fills the body without renegotiating framing.
- A total `RecvAction()` makes the split policy provable: every one of 256 type
  bytes maps to drop / dispatch / skip, so a stray `0x00` is a loud fatal and a
  future `0x08+` is a graceful skip (forward-compat), exactly per
  `message-type-enumeration.md`.
- Sticky-error decoding converts any truncated/adversarial payload into a typed
  error, never a panic — required for bytes a peer controls.

## Consequences

- `messages.go` + `messages_test.go` (`TestMessages_RoundTrip` over all 7,
  `TestMsgType_UnknownPolicy` total trichotomy, `TestResponse_ChunkBudget`,
  `TestDecode_TruncatedPayload`).
- The `Body []byte` opacity is the documented hand-off; WS-1's `wireFileInfo` codec
  plugs in with no envelope change.
- `ErrInvalidRequest` (PR-1/CDD-2 optional `0x04` error code) is intentionally **not**
  minted here; WS-4 may add it when it implements REQUEST validation. Existing codes
  are frozen.
- Cross-refs `message-type-enumeration.md`, PR-1, CDD-2, GR-7.
