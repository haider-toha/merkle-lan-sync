# Decision (WS-3): registry concurrency model + eviction timing + clock/tick injection

- Area: ws3 / internal/discovery (registry.go, discovery.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-3 implementer
- Plan item: WS-3 #2 (a silent peer is evicted after the heartbeat timeout),
  WS-3 #3 (the registry is race-free under concurrent announce/evict/dial — GR-4
  single-goroutine actor).
- Reads-first: CDD-1 (the discovery registry is owned by a **GR-4 single-goroutine
  actor** that emits `peerEvents`, *not* a shared-lock map; the GR-5 RWMutex guards
  only the reconcile core, never discovery), GR-4 ("share by communicating";
  blocking reads made cancellable by closing the socket), GR-13 (`-race` mandatory;
  no naked `time.Sleep` for synchronisation; fan-in channels never closed), GR-2/GR-3
  (one ctx tree, every goroutine owned by a WaitGroup), the `transport` package's
  `New`/`teardown`/`emit` pattern (the sibling network layer to mirror).

## Context

The registry maps `DeviceID → {addr, lastSeen}`. Three activities touch discovery
concurrently: inbound announcements arrive (reader goroutine), silent peers must be
evicted on a timer, and a consumer (engine/test) drains events and may dial. CDD-1
mandates the registry be a single-goroutine actor, not a mutex-guarded map. Two
sub-questions: **who owns the map**, and **how is eviction timed so it can be tested
deterministically** (GR-13 forbids sleeping to "wait for" the timeout).

## Option set 1 — ownership of the registry map

### O1-A — `sync.RWMutex`-guarded map, methods called from reader + ticker + consumer
- correctness 4, concurrency-safety **2** (DISQUALIFIER), testability 4,
  cross-platform 5. A second lock outside the reconcile core is exactly what CDD-1's
  refutation of concurrency-critic-1 forbids ("non-tree shared state ... is owned by
  a GR-4 single-goroutine actor ... never a shared map a second goroutine touches").
  Rejected.

### O1-B — single-goroutine **actor** owning the map; reader and ticker send it
  messages on channels; the actor is the sole emitter of events (CHOSEN)
- correctness **5** — all map reads/writes happen in one goroutine, so eviction can
  never race a concurrent announce; the actor is the single writer (the discovery
  analogue of GR-5's single-writer reconcile core).
- concurrency-safety **5** — no shared mutable state crosses a goroutine boundary
  except via channels; `-race` has nothing to flag by construction (CDD-1 test).
- testability **5** — the actor is driven entirely by its inbound channels, so a
  test feeds announcements and eviction ticks directly and observes emitted events;
  no locks, no sleeps.
- cross-platform **5** — pure goroutines/channels.

### O1-C — lock-free `sync.Map`
- correctness 3 (eviction = read-modify-write across entries, racy without extra
  coordination), concurrency-safety 3 (`sync.Map` removes the data race but not the
  logical race between evict and announce), testability 3, cross-platform 5.
  Rejected — solves the wrong problem and still contradicts CDD-1's actor mandate.

## Option set 2 — eviction timing & how to make it testable (GR-13: no sleep-to-sync)

### O2-A — real `time.Ticker` for the eviction sweep + `time.Now()`; tests use tiny intervals and poll
- correctness 4, concurrency-safety 5, testability **2** (DISQUALIFIER) — verifying
  "evicted after the timeout" then means sleeping past a real timeout, the exact
  naked-`time.Sleep`-for-synchronisation GR-13 bans; inherently flaky under `-race`
  load. Rejected as the test path.

### O2-B — inject a `clock` (a `now()` seam) **and** the eviction-tick channel; production wires a real clock + real ticker, tests drive a manual clock + a manual tick channel (CHOSEN)
- correctness **5** — production behaviour is identical to O2-A (real ticker sweeps
  every `sweepInterval`, evicting entries whose `now - lastSeen > evictTimeout`); the
  seam only changes the *source* of "now" and "tick".
- concurrency-safety **5** — the manual clock is set on the test goroutine *before*
  it sends the tick; the channel send happens-before the actor's sweep, which reads
  the already-advanced clock — fully synchronised, no race, no sleep.
- testability **5** — eviction is proven deterministically: record a peer at t0,
  advance the manual clock past the timeout, send one tick, assert exactly one
  `Evicted` event. Discovery-within-interval is proven by the immediate-on-start
  announce arriving as an event within a bounded wait (an event wait, not a sleep).
- cross-platform **5**.

### O2-C — logical/event-count clock only (evict after N missed announces, no wall time)
- correctness 3 — "silent peer" is inherently a wall-time property (a peer that
  stops sending has no events to count); a pure logical clock cannot express
  "30 seconds of silence" without a tick anyway. Rejected; O2-B subsumes it.

## Decision

**O1-B + O2-B.** The registry is a single goroutine (`registry.run`) selecting over:
its inbound announcement channel (from the reader), its eviction-tick channel, and
`ctx.Done()`/`closed`. It owns `map[DeviceID]peerEntry{addr, lastSeen}` exclusively
and is the sole emitter on `Events()`. Time is a `clock` interface (`now()`);
production = `realClock{}` (wraps `time.Now`) + a `time.Ticker` at `sweepInterval`;
tests inject a `manualClock` + a manual tick channel (unexported options). Defaults:
`announceInterval = 10s`, `evictTimeout = 30s` (≈3 missed announces — the standard
heartbeat miss-count), `sweepInterval = 10s`; all overridable via options. A peer is
evicted within `[evictTimeout, evictTimeout + sweepInterval]` of its last announce.

Shutdown mirrors `transport`: `New(ctx, …)` owns every goroutine under one
`WaitGroup`; `Close()`/ctx-cancel runs an idempotent (`sync.Once`) teardown that
closes the socket (unblocking the reader's blocked `ReadDatagram` — GR-4
cancellable-read rule) and cancels ctx (stopping the announcer + actor). `Events()`
is a fan-in-style channel that is **never closed** (GR-13); `emit` selects on a
`closed` channel so it never blocks past shutdown (the transport `emit` pattern).

## Rationale

- The actor model is the literal design CDD-1 carried forward; it makes the `-race`
  acceptance (WS-3 #3) true by construction rather than by lock discipline.
- The clock+tick seam is the only way to satisfy "evicted after the timeout" *and*
  GR-13's no-sleep-for-synchronisation rule simultaneously, while keeping production
  timing real.
- Mirroring `transport`'s lifecycle keeps the two sibling network layers uniform for
  the engine and re-uses an already-reviewed shutdown shape (GR-2/GR-3/GR-13).

## Consequences

- Eviction granularity is `sweepInterval` (a silent peer disappears within timeout +
  one sweep); fine for a LAN hint. Re-announce before the sweep refreshes `lastSeen`
  and keeps the peer.
- A re-announce with a **changed** address re-emits `Discovered` (new dial hint);
  same-address re-announces only refresh `lastSeen` (no event spam).
- The manual-clock/tick options are unexported (white-box test seam); production
  callers use the public interval options only.
- `Events()` is single-producer (the actor) but follows the never-close convention
  so the engine's `select` over transport+discovery events is uniform and
  shutdown-safe. Cross-refs: CDD-1, GR-2/3/4/13, transport `transport.go`.
