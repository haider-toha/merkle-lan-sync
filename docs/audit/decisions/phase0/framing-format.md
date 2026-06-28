# Decision: wire framing format for the TCP sync protocol

- Area: phase0 / protocol
- Status: decided (Phase 0 baseline — protocol-researcher hardens the message-type catalogue in Phase 2)
- Date: 2026-06-28
- Decider: rules-architect

## Context

Merkle Sync moves two kinds of traffic over a single raw TCP connection between
two LAN peers: small control messages (peer handshake, index/tree exchange,
chunk requests) and bulk file-chunk payloads. TCP is a byte stream with **no
message boundaries** — a single `Read` may return half a message or three
messages glued together — so we must impose our own framing. The framing layer
is the foundation every other protocol message sits on; getting it wrong
corrupts the *whole* stream (a single off-by-one in a length prefix
desynchronises every subsequent message), so it is a consequential, log-first
choice.

Requirements:

1. **Self-delimiting** — the receiver can read exactly one message from the
   stream regardless of how TCP chunked the bytes.
2. **Typed** — the receiver dispatches on a message type without parsing the
   payload first.
3. **DoS-resistant** — a malicious or buggy peer must not be able to make us
   allocate unbounded memory by claiming a huge length. Length-prefixed readers
   "can be given a huge message size ... [which] may result in an
   OutOfMemoryException, so one must include a maximum message size 'sanity
   check'" ([Stephen Cleary, *Message Framing*](https://blog.stephencleary.com/2009/04/message-framing.html), accessed 2026-06-28).
4. **Cheap to implement and test** in pure Go with `io.ReadFull` +
   `encoding/binary`, decodable on both Mac and Windows identically.

## Options (scored 1–5, 5 = best)

### Option A — `[4-byte big-endian length][1-byte type][payload]` + max-length guard (PROPOSED)

The reader does `io.ReadFull(conn, hdr[:4])`, decodes a `uint32` big-endian
length `L`, rejects if `L == 0 || L > MaxFrameLen`, then `io.ReadFull` exactly
`L` bytes. Byte 0 of that body is the 1-byte message type; bytes 1..L-1 are the
payload. `L` therefore counts `type + payload` (so an empty-payload message has
`L == 1`).

- Correctness: **5** — unambiguous, trivially round-trips, network byte order
  matches the convention used by Syncthing's BEP ("All length values use network
  byte order (big-endian)" — [BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html), accessed 2026-06-28).
- Concurrency-safety: **5** — framing is per-connection and stateless between
  frames; one reader goroutine owns the read side, one writer goroutine owns the
  write side, no shared mutable parser state.
- Testability: **5** — table-driven tests: split a frame across N synthetic
  `Read` boundaries via `iotest.OneByteReader`; feed an oversized length and
  assert rejection without allocation; fuzz the header.
- Cross-platform: **5** — `encoding/binary.BigEndian` is byte-order-explicit, so
  a Mac little-endian host and a Windows host agree on the wire bytes.

### Option B — `encoding/gob` self-describing stream

Use `gob.NewEncoder(conn)` / `gob.NewDecoder(conn)` and send Go structs
directly; gob handles its own framing and typing.

- Correctness: **4** — works, but the framing is opaque and we cannot reason
  about or fuzz the bytes on the wire.
- Concurrency-safety: **4** — a `gob.Decoder` is single-reader like ours.
- Testability: **2** — hard to construct adversarial byte streams by hand.
- Cross-platform: **4** — gob is portable, but...
- **Disqualifier (security):** "The gob package is not designed to be hardened
  against adversarial inputs, and is outside the scope of Go's security policy.
  ... care should be taken when decoding gob data from untrusted sources, which
  may consume significant resources" ([encoding/gob docs](https://pkg.go.dev/encoding/gob), accessed 2026-06-28). Peers on a LAN are
  semi-trusted at best (TOFU, see transport-security decision), so a parser
  outside Go's security policy is the wrong default for the network boundary.

### Option C — Protobuf message + varint/16-bit length prefix (BEP-style, two-level header)

Syncthing's BEP frames each post-auth message as `[16-bit header length][protobuf
Header{type,compression}][32-bit message length][protobuf message]` ([BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html), accessed 2026-06-28).

- Correctness: **5** — battle-tested in Syncthing.
- Concurrency-safety: **5**.
- Testability: **4** — needs a protobuf toolchain + generated code to exercise.
- Cross-platform: **5**.
- **Cost:** pulls in `google.golang.org/protobuf` + codegen and a two-level
  header (header-len, header, msg-len) to support per-message compression
  negotiation we do not need on a fast LAN at Phase 0. More machinery than the
  problem requires.

### Option D — delimiter framing (e.g. newline/`\0`-terminated, JSON Lines)

- Correctness: **2** — binary chunk payloads contain every possible byte, so any
  in-band delimiter requires escaping; escaping binary defeats the purpose.
- Concurrency-safety: **4**.
- Testability: **3**.
- Cross-platform: **3** (CRLF/LF hazards). Rejected for binary payloads.

## Decision

Adopt **Option A**: `[4-byte big-endian length][1-byte type][payload]` where the
4-byte length is the count of `type-byte + payload` and is validated against a
hard `MaxFrameLen` guard *before* any allocation. Encode/decode with
`encoding/binary.BigEndian` and `io.ReadFull`.

- `MaxFrameLen = 16 MiB` as the Phase 0 hard ceiling (rejects DoS-sized
  lengths). Bulk file content is streamed as many small chunk messages, not one
  giant frame; the per-chunk size (fixed 32 KiB vs content-defined) is a
  separate decision deferred to the merkle/reconcile workstream, but it stays
  well under the ceiling. `MaxFrameLen` is a single tunable constant in
  `internal/protocol`.
- Length `0` is rejected (every frame has at least a type byte).
- The 1-byte type gives 256 message types — ample; protocol-researcher (Phase 2)
  fixes the ~7-message catalogue (HELLO, INDEX, INDEX_UPDATE, REQUEST, RESPONSE,
  PING, CLOSE — modelled on BEP's 8-type enum).

## Rationale

- **Simplicity is a correctness property here.** The framing bug class
  (length-prefix off-by-one desynchronising the stream) is exactly what the
  protocol-critic is told to hunt for; a format a human can verify by counting
  bytes minimises that surface. A length prefix lets the receiver
  "deterministically read exactly one envelope from the stream, reject oversized
  or malformed payloads early, and avoid ambiguity arising from partial reads or
  stream concatenation" ([Stephen Cleary](https://blog.stephencleary.com/2009/04/message-framing.html); [Eli Bendersky, *Length-prefix framing*](https://eli.thegreenplace.net/2011/08/02/length-prefix-framing-for-protocol-buffers), both accessed 2026-06-28).
- **Big-endian** matches the dominant network convention and Syncthing BEP, so
  the wire is order-explicit across Mac↔Windows.
- **`encoding/binary` over `gob`** because the network boundary takes untrusted
  bytes and gob is explicitly out of Go's security policy for adversarial input
  (citation above). `encoding/binary` "favors simplicity over efficiency"
  ([encoding/binary docs](https://pkg.go.dev/encoding/binary), accessed 2026-06-28) — simplicity is what we want for the
  trust boundary; payload bodies inside the frame can use whatever structured
  encoding the protocol layer picks (likely a hand-rolled binary or protobuf for
  index messages), but the *frame* is dumb and auditable.
- **The max-length guard is mandatory, not optional** — it is the single line
  that turns a textbook DoS into a dropped connection.

## Consequences

- Cross-references hard rule **SR-12** (framing length guard) in
  `docs/audit/rules/sync-rules.md` and the Go I/O idiom (`io.ReadFull`, never a
  bare `Read`) in `docs/audit/rules/go-rules.md`.
- `internal/protocol/framing.go` implements `WriteFrame(w, type, payload)` and
  `ReadFrame(r) (type, payload, error)`; `ReadFrame` returns a typed error
  (`ErrFrameTooLarge`) so the transport layer can distinguish "drop this peer"
  from a transient read error.
- The 4-byte length caps a single frame at 4 GiB by encoding, but the 16 MiB
  policy guard binds first; revisit only if a legitimate message type needs
  larger frames (none currently planned).
- Open sub-decision handed to Phase 2: per-chunk payload size and whether index
  messages compress. Framing is forward-compatible with both (compression would
  become a type-byte variant or a payload-internal flag, no frame change).
