# Decision (WS-4): reconcile engine concurrency model + bidirectional back-pressure

- Area: ws4 / internal/reconcile (engine.go, transfer.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-4 implementer
- Plan items: WS-4 #1 (converge), #11 (bidirectional back-pressure cannot deadlock).
- Reads-first: CDD-1 (GR-4 companion — outbound never blocks the select loop;
  per-conn writer owns outbound buffered-with-shed; bulk RESPONSE in its own GR-3
  goroutine), GR-5 (one RWMutex guards the reconcile core's in-memory state — tree +
  per-peer ack/last-index + the apply-time expected-hash record; zero I/O under the
  lock), GR-3/GR-4/GR-13, the `transport` `Conn.Send` contract (non-blocking,
  buffered-with-shed — drops the peer on overflow, never blocks the caller), PR-6,
  MK-4, the WS-3 registry-actor decision (the sibling shape to mirror).

## Context

`internal/reconcile` is the single writer of tree state (GR-5). It consumes three
asynchronous streams — `transport.Events()` (PeerConnected / PeerDisconnected /
PeerMessage{INDEX, INDEX_UPDATE, REQUEST, RESPONSE, PING, CLOSE}),
`discovery.Events()` (PeerDiscovered → dial, PeerEvicted), and local filesystem
change hints (debounced watcher + periodic rescan) — and drives diff → transfer →
apply → conflict → tombstone. Two hard constraints shape the architecture:

1. **GR-5:** all reconcile-core state (the `FileInfo` set + cached tree, per-peer
   last-index/ack state, the apply-time expected-hash record) is serialised by one
   `RWMutex`, and **no network/disk I/O happens while the lock is held** (the
   watcher↔apply deadlock the concurrency-critic hunts).
2. **CDD-1 / criterion 11:** the engine must **never block on an outbound send from
   its main loop**, and **bulk chunk transfer must run off the main loop**, or two
   peers pulling large files from each other simultaneously can deadlock.

`transport.Conn.Send` is already non-blocking (it sheds — drops the peer — if the
per-conn outbound buffer is full). That makes *control* messages safe to send from
the loop, but a naive "serve the whole file as N RESPONSE frames from the loop"
would either block (it doesn't, Send sheds) or **shed mid-transfer and drop the
peer** under back-pressure — turning a slow link into a dropped peer + retry storm,
which threatens the "converge within a timeout" acceptance.

## Options (scored 1-5: correctness / concurrency-safety / testability / cross-platform)

### Option A — single engine goroutine (select loop) + per-peer transfer goroutines; stop-and-wait pull (CHOSEN)

One `run()` goroutine owns the `select` over the three event sources + a periodic
rescan ticker + an internal `completions` channel + `ctx.Done()`. It takes the
`RWMutex` only for short critical sections (snapshot the tree / mutate the `FileInfo`
map) and does **zero I/O under the lock**. Bulk transfer is off the loop:

- **one puller goroutine per peer** drains a per-peer fetch queue and fetches one
  file at a time; within a file it is **stop-and-wait** — send `REQUEST(offset,len)`
  via `Conn.Send`, block on a per-(peer,reqID) response channel, write the chunk,
  repeat. At most **one outstanding REQUEST per peer**, so the outbound queue never
  accumulates beyond ~1 RESPONSE per active transfer ⇒ `Send` never sheds under
  normal flow ⇒ no peer drop, no deadlock, bounded memory.
- **one server goroutine per peer** drains a per-peer REQUEST queue, reads the
  requested range from disk, and `Send`s one RESPONSE. Off the loop (CDD-1).
- the loop **routes** RESPONSE → the waiting puller's channel and REQUEST → the
  server queue; both routes are non-blocking.

- correctness **5** — single writer serialises every tree mutation (GR-5); the
  resolver/transfer/conflict primitives are pure functions the loop invokes.
- concurrency-safety **5** — the only shared mutable state is behind the one RWMutex;
  transfer goroutines own their buffers and communicate by channels (`-race` clean by
  construction); the deadlock cycle is severed (no blocking outbound on the loop +
  bounded outstanding requests).
- testability **5** — pure pieces (resolve, W, conflictName, atomicWriteVerify, block
  plan, GC ack-gate) unit-test directly; the engine is driven by feeding events; a
  two-instance loopback integration test asserts bidirectional back-pressure
  convergence within a timeout.
- cross-platform **5** — pure Go + the existing portable transport.

### Option B — lock-per-operation: each inbound message handled by its own goroutine taking `Lock()`

- correctness **3** — apply, rescan, and diff interleave; ordering bugs (an apply
  racing a rescan re-hash of the same path) are easy.
- concurrency-safety **2** (DISQUALIFIER) — multiple writers invite the GR-5
  watcher↔apply deadlock and lock-ordering hazards; tends toward holding the lock
  across I/O.
- testability **3**, cross-platform **5**. **Rejected** — contradicts the
  single-writer model GR-5 mandates.

### Option C — serve bulk transfer inline on the select loop, rely on `Send` shedding for flow control

- correctness **3** — a multi-chunk file served inline blocks the loop on disk reads
  and, under back-pressure, `Send` sheds and **drops the peer** mid-transfer.
- concurrency-safety **3** — shedding-induced peer drops + re-dials churn; the
  "converge within a timeout" acceptance becomes flaky.
- testability **3**, cross-platform **5**. **Rejected** — violates CDD-1 ("bulk
  RESPONSE in its own goroutine") and risks criterion 11.

## Decision

**Option A.** A single engine goroutine owns the `select` loop and is the sole writer
behind one `sync.RWMutex` (GR-5); it performs **zero I/O under the lock**. Outbound
control messages use the non-blocking `Conn.Send`. Bulk transfer runs in **per-peer
puller and server goroutines** (GR-3 spawn-and-own, reaped on peer disconnect /
shutdown). The pull protocol is **stop-and-wait** (≤1 outstanding REQUEST per peer),
which bounds the outbound queue so `Send` never sheds under normal flow and the
bidirectional-transfer deadlock cycle cannot form. Local content-addressed reuse is
tried before any network REQUEST (a file whose `content_hash` is already on disk is
copied locally — MK-4 §3, the zero-network-rename substrate, PR-5).

## Rationale

- It is the literal CDD-1 design: outbound off the loop, bulk transfer in its own
  goroutine, one writer behind the RWMutex.
- Stop-and-wait is the cheapest provably-deadlock-free flow control given the
  existing `Conn.Send` shed semantics (which I must not change — WS-2 is frozen):
  by capping outstanding requests at 1/peer the buffer cannot fill, so the shed path
  is never taken and no peer is dropped for being slow.
- It maximises testability — the decision-bearing logic is pure functions; only the
  wiring needs an integration test.

## Consequences

- Drives `engine.go` (the loop, RWMutex, routing, per-peer goroutine lifecycle) and
  `transfer.go` (puller stop-and-wait, server, local reuse, atomic commit).
- Per-peer goroutines are owned by the engine `WaitGroup` and cancelled on
  PeerDisconnected / ctx (GR-3); the response-router map is cleaned on peer drop so
  no puller leaks.
- Throughput is one chunk-RTT per 32 KiB on a fresh transfer; acceptable on a LAN for
  v1 and never a correctness risk. A windowed pull is a future optimisation behind the
  same REQUEST/RESPONSE protocol (no wire change).
- Cross-refs: CDD-1, GR-3/4/5/13, PR-6, MK-4, PR-5; WS-2 `conn.go` Send contract.
