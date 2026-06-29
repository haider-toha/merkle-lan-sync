# Decision (WS-2): connection establishment, the HELLO re-assert seam, and the engine event surface

- Area: ws2 / internal/transport (transport.go, listener.go, dial.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-2 implementer
- Plan item: WS-2 #3 ("HELLO re-asserts the DeviceID — drop on mismatch"), #4
  (transport-sourced disconnect event; ctx + WaitGroup shutdown).
- Reads-first: PR-1 §5 (handshake state machine: TLS pin → HELLO re-assert → root
  short-circuit), PR-7 §4.2 (HELLO re-asserts identity in-band), CDD-1 (fan-in
  `peerEvents` never closed; transport→engine disconnect event), GR-2 (one ctx
  tree), GR-4 (listeners coordinate by channels), GR-13 (fan-in never closed).

## Context

After the TLS pin (`tls-config-and-deviceid-pinning.md`), PR-1 §5 step 2 requires an
in-band HELLO exchange that **re-asserts the DeviceID** (`HELLO.DeviceID == the
TLS-pinned ID`, drop on mismatch) and also carries engine-level fields (rootHash for
the SR-5 short-circuit, folderID, featureFlags). Two questions: **who runs the HELLO
exchange** (transport vs engine), and **how the engine learns of peers, inbound
messages, and disconnects**.

## Options — HELLO ownership (scored: correctness / concurrency-safety / testability / cross-platform)

### Option A — transport ships only authenticated raw frames; the engine does the entire HELLO exchange
- correctness **3**, concurrency **4**, testability **3**, cross-platform **5**.
  Splits the identity check (TLS-pin in transport) from its in-band re-assert (HELLO
  in engine), so the security-critical "drop on HELLO/TLS mismatch" lands outside the
  layer that holds the pinned ID. The plan explicitly assigns
  `TestHELLO_DeviceIDMismatchDropped` to WS-2, i.e. transport. Rejected.

### Option B — transport runs the HELLO exchange with an engine-supplied HELLO provider; validated peer HELLO is surfaced to the engine (CHOSEN)
- correctness **5** — the DeviceID re-assert lives next to the pin (both in
  transport's `establish`), so a mismatch drops the connection during establishment,
  before any steady-state frame. Engine-owned fields (rootHash/folderID/flags) come
  from a `func() protocol.Hello` provider the transport calls at connect time and
  whose `DeviceID` the transport overwrites with its own identity; the peer's
  validated HELLO rides out on the `PeerConnected` event for the engine's
  short-circuit.
- concurrency **5** — the exchange is synchronous handshake I/O (write ours, read
  theirs) before the reader/writer goroutines start; no shared state.
- testability **5** — a raw `tls.Client` that passes the TLS pin but sends a wrong
  `HELLO.DeviceID` proves the drop (`TestHELLO_DeviceIDMismatchDropped`) on one Mac.
- cross-platform **5**.

### Option C — transport runs HELLO but hard-codes rootHash=0 (no provider)
- correctness **3** — defeats the SR-5 root short-circuit (the engine could never
  advertise its real root at connect). Rejected; the provider (Option B) costs one
  func field.

## Decision — HELLO

**Option B.** `establish(tlsConn)`: `HandshakeContext` (TLS pin) → recompute pinned
DeviceID from `PeerCertificates[0]` → **write our HELLO** (`helloFn()` with our
`DeviceID`) → **read peer HELLO** (first frame must be `MsgHello`) → require
`peerHello.DeviceID == pinned` else `ErrHelloDeviceMismatch` → clear the handshake
deadline → spawn the steady-state `Conn` and emit `PeerConnected`. Write-then-read is
deadlock-free because a HELLO is tiny (well under the socket/TLS buffer); a
`handshakeTimeout` deadline (default 30s) bounds the whole exchange so a stuck peer
parks no goroutine (GR-3).

## Options — engine event surface

### S1 — callback handlers (`OnConnect`/`OnMessage`/`OnDisconnect`)
- Inverts control into transport-owned goroutines; harder to integrate with the
  engine's single `select` loop. Rejected.

### S2 — one fan-in `Events()` channel carrying `{PeerConnected, PeerMessage, PeerDisconnected}` (CHOSEN)
- correctness **5**, concurrency **5**, testability **5**, cross-platform **5**.
  Matches GR-4 "share by communicating" and the engine's single-consumer `select`
  loop. The channel is a **fan-in (many conns → one engine): never closed**; shutdown
  is `ctx` cancellation + a `WaitGroup` (GR-13). `emit` selects on `events<-` or
  `transport.closed`, so shutdown never blocks a sender.

### S3 — separate channels per event kind
- More plumbing for no gain; the engine wants one loop. Rejected.

## Decision — event surface + lifecycle

**Option S2.** `Transport` is constructed with the caller's root `ctx` (GR-2),
derives a cancellable child, and owns a `WaitGroup` over: the accept loop(s),
per-accept establish goroutines, and per-conn supervisors. `Close()` (idempotent via
`sync.Once`) sets `closing` under a mutex, closes listeners, `closeWith`s every conn,
cancels the ctx, then `Wait`s. A ctx-cancel watcher triggers the same teardown, so
cancelling the parent ctx tears the transport down without an explicit `Close()`.
`register` is mutex-guarded and **rejects** new conns once `closing` is set, which
(with the ctx-cancel of in-flight dials/handshakes) closes the WaitGroup-reuse race:
no `wg.Add` ever happens after teardown has begun.

A minor structural note: the orchestrator (the `Transport` type, `Event` surface,
establish, supervisor) lives in `transport.go`, an addition beyond the
`structure.md` file sketch (doc/identity/tls/conn/listener/dial); the split keeps
`conn.go` to the `Conn` type. Connection **dedup** when both peers dial each other is
left to the engine (it sees every `PeerConnected`); v1 is 2-device (N6) so this is a
documented deferral, not a correctness gap for WS-2.

## Rationale

- Keeping the HELLO re-assert in transport puts the full identity decision (TLS pin +
  in-band re-assert) in one place, satisfying the WS-2 test mandate and PR-7 §4.
- A single never-closed fan-in channel is the idiomatic engine seam (GR-4/GR-13) and
  makes the disconnect-fires-immediately property (CDD-1.3) trivial to test.

## Consequences

- `Transport`: `New(ctx, id, allow, opts...)`, `Events()`, `Listen`, `Dial`,
  `Close`; options `WithHello`, `WithOutboundBuffer`, `WithHandshakeTimeout`.
- **Usage contract (documented in `doc.go`):** drain `Events()` in a dedicated
  goroutine and do **not** call `Dial` from that same goroutine — `Dial` blocks
  through HELLO and the `PeerConnected` emit, so calling it from the drain loop would
  self-deadlock. This matches GR-4 (spawn work off the select loop).
- Sentinels: `ErrTransportClosed`, `ErrHelloDeviceMismatch`, `ErrHelloExpected`.
- Cross-refs: PR-1 §5, PR-7 §4, CDD-1, GR-2/GR-4/GR-13.
