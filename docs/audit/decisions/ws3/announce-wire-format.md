# Decision (WS-3): announcement datagram wire format

- Area: ws3 / internal/discovery (announce.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-3 implementer
- Plan item: WS-3 #1 (discovered within the announce interval), #4 (discovery is a
  hint, never authorisation) — the datagram must be cheap to parse, robust to
  garbage (it is unauthenticated), and carry exactly what a *dial hint* needs.
- Reads-first: GR-7 (`encoding/binary` for the wire, never `gob` from the network),
  GR-8 (bounds-check before trusting a length), SKILL §5 (big-endian, fixed widths,
  length-prefixed — the project wire idiom), SKILL §7 (the DeviceID in an announce
  is a *claim*, verified later by TLS), `internal/protocol/{messages,deviceid}.go`
  (the existing `encbuf`/`decbuf` big-endian style).

## Context

A receiver gets raw UDP datagrams from anyone on the group — including unrelated
multicast traffic, malformed packets, and (with loopback on) its **own**
announcements. The format must let the receiver (a) reject non-msync / wrong-version
/ truncated datagrams in O(1) without panicking, (b) recover the peer's DeviceID
(a hint/label, not trusted) and the TCP port to dial, and (c) be byte-identical on
Mac and Windows. The dial target's **IP** is taken from the UDP source address, not
the payload (see Options).

## Options (scored 1–5: correctness / concurrency-safety / testability / cross-platform)

### Option A — hand-rolled fixed big-endian record: `magic[4] | version:u8 | deviceID[32] | tcpPort:u16`, IP from the UDP source (CHOSEN)
- correctness **5** — magic + version make rejection of foreign/garbage/future
  datagrams a 5-byte check before any allocation (GR-8); fixed widths + big-endian
  match SKILL §5 so the bytes are identical cross-platform; deriving the IP from the
  *observed UDP source* is the most robust LAN choice (the sender need not know its
  own routable IP; multi-homing/NAT can't make it lie about where packets actually
  came from). 39 bytes total.
- concurrency-safety **5** — pure function `encode`/`decode`, no shared state.
- testability **5** — table-driven decode over truncated / bad-magic / bad-version /
  self / good inputs; trivial to fuzz; no real socket needed.
- cross-platform **5** — `encoding/binary.BigEndian`, stdlib only (GR-7).

### Option B — reuse `internal/protocol` framing + a new `MsgType` for announce
- correctness **3** — the `[4-byte len][1-byte type][payload]` framing exists for
  the **TLS byte-stream** where messages must be delimited; a UDP datagram is
  already a message boundary, so the length prefix is dead weight, and minting a
  discovery `MsgType` pollutes a catalogue whose `0x01..0x07` are frozen for the
  authenticated stream (SKILL §5, message-type-enumeration decision). It also
  couples the unauthenticated discovery plane to the authenticated message plane.
- concurrency-safety **5**, testability **4**, cross-platform **5**.
- **Disqualifier:** conflates two planes and renumbers/extends a frozen catalogue
  for no gain.

### Option C — JSON or `encoding/gob` payload
- correctness **2** — `gob` from the network is **forbidden** (GR-7: "not designed
  to be hardened against adversarial inputs"); JSON adds a reflective parser at an
  unauthenticated trust boundary, is variable-width (not byte-deterministic), and is
  off-idiom (every other wire byte in this project is hand-rolled big-endian).
- concurrency-safety **5**, testability **4**, cross-platform **4**.
- **Disqualifier:** GR-7 violation (gob) / unnecessary attack surface (JSON).

### Option D — Option A but carry the full announced IP:port in the payload (ignore the UDP source)
- correctness **3** — lets a sender advertise an address it can't be reached at
  (stale cache, wrong NIC) and is trivially spoofable to point a peer at a third
  host; multi-homing makes "which IP" ambiguous. TLS still protects us (the dial to
  a spoofed IP fails the pin), but it wastes dials and bytes for no benefit on a LAN.
- concurrency-safety **5**, testability **5**, cross-platform **5**.
- **Disqualifier:** strictly worse hint quality than using the observed source IP.

## Decision

**Option A.** Datagram = `magic "MSYN" (4) | version 0x01 (1) | DeviceID (32) |
tcpPort uint16 BE (2)` = **39 bytes**. Decoding:
1. `len < 39` → drop (truncated).
2. `magic != "MSYN"` → drop (foreign traffic).
3. `version != 1` → drop (unknown/future — forward-compat: old peers ignore new
   versions rather than misparse).
4. `tcpPort == 0` → drop (not a dialable hint).
5. else yield `(DeviceID, tcpPort)`; the registry pairs it with the UDP source IP.

The DeviceID is recorded **as a claim only** (registry key + self-filter); it is
never an authorisation input (SKILL §7, WS-3 #4) — TLS pinning on dial is the
authority.

## Rationale

- A 5-byte magic+version guard turns the unauthenticated input into a cheap,
  panic-free, allocation-free reject path (GR-8) — exactly what an open UDP port
  needs. Big-endian fixed widths keep it cross-platform (SKILL §5).
- Source-derived IP + payload TCP port is the standard, robust LAN-discovery shape
  and keeps the payload minimal.
- Hand-rolled binary (not gob/JSON) honours GR-7 and matches the codebase idiom.

## Consequences

- IPv4 source IP + uint16 port ⇒ the hint is one `netip.AddrPort`. IPv6 and
  multi-address announces are out of v1 scope (room left via the version byte).
- A future format change bumps the version byte (or magic); current peers safely
  drop what they can't parse — graceful degradation, never a desync.
- Self-announcements are received (loopback) and dropped by the DeviceID self-filter
  in the reader before they reach the registry actor.
- Cross-refs: GR-7, GR-8, SKILL §5/§7, `protocol/messages.go` (encbuf/decbuf style).
