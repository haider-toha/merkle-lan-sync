# Decision (WS-2): per-connection goroutine model + outbound ownership + close

- Area: ws2 / internal/transport (conn.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-2 implementer
- Plan item: WS-2 #1 (split-frame survives), #2 (malformed length drops THAT peer
  cleanly without desyncing others), #4 (no goroutine leak; outbound never blocks
  the select loop; `conn.Close()` idempotent).
- Reads-first: CDD-1 (GR-4 companion "outbound never blocks the select loop,
  buffered-with-shed"; GR-5 boundary; GR-13 fan-in + `sync.Once` owner-only close),
  go-rules GR-3/GR-4/GR-13, SR-12, `internal/protocol/framing.go`
  (`ReadFrame`/`WriteMessage`), `decisions/ws0/concurrency-oracle-rule-amendments.md`.

## Context

Each peer connection is a `tls.Conn` carrying length-prefixed frames in both
directions. The model must satisfy four hard constraints simultaneously:
1. a blocking `ReadFrame` must be **cancellable** (GR-4) and leak no goroutine on
   disconnect (GR-3);
2. a malformed/oversized length on ONE conn drops only THAT peer, never desyncs
   others (SR-12) — i.e. the reader abandons the stream and never tries to "resync";
3. **outbound must never block the engine's select loop** — buffered-with-shed,
   owned by a per-conn writer (CDD-1 / GR-4 companion);
4. `Close()` is idempotent and owner-only (GR-13, `sync.Once`).

## Options (scored 1–5: correctness / concurrency-safety / testability / cross-platform)

### Option A — one goroutine per conn doing read+write (half-duplex)
- correctness **2** (DISQUALIFIER), concurrency **3**, testability **4**,
  cross-platform **5**. A single goroutine cannot block on `ReadFrame` and still
  service outbound; you would need non-blocking polling/deadlocks. Reading and
  writing a TLS conn concurrently is explicitly safe (one reader + one writer), so
  splitting them is the idiomatic shape. Rejected.

### Option B — reader + writer goroutines, but the engine writes by a **blocking** send to the writer's channel
- correctness **4**, concurrency **2** (DISQUALIFIER), testability **4**,
  cross-platform **5**. A blocking send couples the engine's select loop to a slow
  peer's socket throughput — exactly the back-pressure deadlock cycle CDD-1/GR-4
  forbid ("the reconcile core must never perform a blocking send to a peer from its
  main select loop"). Rejected.

### Option C — reader + writer goroutines; outbound is a **buffered channel with shed**; one `sync.Once` close that both unblocks the reader (`tls.Conn.Close`) and stops the writer (`close(closed)`); a transport supervisor `Wait`s both and emits the disconnect event (CHOSEN)
- correctness **5** — reader uses `protocol.ReadFrame` (io.ReadFull + pre-alloc
  length guard) so a split frame reassembles (#1) and an oversized length returns
  `ErrFrameTooLarge` → drop THIS conn only (#2), never a resync attempt. Writer
  drains the outbound channel via `WriteMessage`. `RecvAction` is applied per frame:
  `0x00`→drop, `0x08+`→skip, `0x01..0x07`→decode+deliver.
- concurrency **5** — `Send` is a **non-blocking** 3-arm select (`<-closed` / `buf<-`
  / `default:shed`): a full buffer **drops the peer** and returns false, so the
  caller's select loop is never blocked (#3, #4). Close handshake is GR-3 verbatim:
  `closeWith` (a `sync.Once`) records the first cause, `close(closed)` (stops the
  writer + fails `Send`), and `tls.Conn.Close()` (unblocks the reader's parked
  `ReadFull`). Reader and writer each `wg.Done` on exit; a per-conn supervisor
  (owned by the transport `WaitGroup`) `wg.Wait()`s both, then emits
  `PeerDisconnected{DeviceID, Err}` and deregisters — so the engine learns of a drop
  **immediately**, not at discovery-eviction time (CDD-1.3).
- testability **5** — split-frame, malformed-length-isolation, and churn-leak are all
  loopback `tls.Conn` tests on one Mac; the leak test asserts
  `runtime.NumGoroutine()` returns to baseline.
- cross-platform **5** — pure `net`/`crypto/tls`/goroutines.

## Decision

**Option C.** `Conn` owns `readLoop` + `writeLoop` (in `conn.wg`); the transport
owns one `superviseConn` per conn (in `transport.wg`). `Send(msg) bool` is
non-blocking buffered-with-shed (`ErrOutboundOverflow` is the shed cause). Close is a
single `closeWith(err)` guarded by `sync.Once`: record cause → `close(closed)` →
`tls.Conn.Close()`. Inbound dispatchable frames are delivered on the transport's
fan-in `events` channel via a select that also watches `closed`/`transport.closed`
so delivery never deadlocks at shutdown.

## Rationale

- One reader + one writer per `tls.Conn` is the documented-safe concurrency shape;
  buffered-with-shed severs the back-pressure cycle at its only edge (a blocking
  outbound send), which is precisely the kernel CDD-1 carried forward.
- `tls.Conn.Close()` is the unblock mechanism for a parked `ReadFull`; wrapping it in
  `sync.Once` makes close idempotent and owner-only (GR-13), and a second
  `net.Conn.Close()` merely returns an error.

## Consequences

- A sustained-slow peer is **dropped**, not tolerated — acceptable and intended
  (the engine re-dials on the next discovery hint). Buffer size is tunable
  (`WithOutboundBuffer`, default 64).
- The disconnect `Event.Err` carries the drop cause (EOF/`ErrFrameTooLarge`/
  decode error/`ErrOutboundOverflow`/`ErrTransportClosed`) for engine-side logging.
- Steady-state reads have no idle deadline in v1 (liveness via PING is an engine
  concern); a read idle-timeout is a noted future enhancement.
- Cross-refs: CDD-1, GR-3/GR-4/GR-13, SR-12, `framing.go`.
