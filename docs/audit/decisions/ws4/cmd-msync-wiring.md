# Decision (WS-4): cmd/msync daemon wiring

- Area: ws4 / cmd/msync (main.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-4 implementer
- Plan item: WS-4 "cmd/msync wiring (Phase 5)".
- Reads-first: GR-2 (one `signal.NotifyContext` root ctx threaded everywhere; no deep
  `context.Background()`), GR-3 (every goroutine owned by a WaitGroup), GR-4 (three
  listeners coordinated by channels into the reconcile core), the public constructors
  `transport.New`/`Listen`/`Dial`, `discovery.New`, `transport.LoadOrCreateIdentity`,
  `reconcile.New`/`Run`, `transport.Allowlist`.

## Context

`cmd/msync` is the headless daemon: it must build the device identity, start the
transport listener + discovery, construct the reconcile engine over a folder, dial
discovered peers, and shut everything down cleanly on SIGINT/SIGTERM. The engine and
the two network layers each already own their goroutines under a context; `main` only
wires them and owns the top-level lifecycle.

## Options (scored: correctness / concurrency / testability / cross-platform)

### Option M1 — engine drives discovery/transport internally; main just calls `engine.Run` (CHOSEN)
`main` builds the root ctx (`signal.NotifyContext`), loads/creates the identity,
constructs `transport.New(ctx, id, allow, WithHello(engine.hello))` + a TCP
`Listen`, constructs `discovery.New(ctx, id, port)`, then constructs
`reconcile.New(...)` **handed the transport + discovery handles** and calls
`engine.Run(ctx)` which owns the select loop, dials discovered peers (in goroutines,
per the transport usage contract), and tears down on ctx cancel.
- correctness **5** — one ctx tree (GR-2); the engine is the single consumer of both
  event streams (GR-4); shutdown propagates by ctx + each subsystem's `Close`.
- concurrency-safety **5** — no goroutine is spawned in `main` except the blocking
  `Run`; every long-lived goroutine is owned by transport/discovery/engine WaitGroups
  (GR-3). testability **5** — `reconcile.New` takes interfaces/handles so the engine is
  integration-tested in-process on loopback without `main`. cross-platform **5**.

### Option M2 — main wires transport↔discovery↔engine by hand with its own goroutines + channels
- correctness **3** — re-implements fan-in `main` already delegated to the engine;
  more places to leak a goroutine. concurrency **3**. Rejected — duplicates GR-4 wiring
  the engine already owns.

### Option M3 — global singletons / package-level state
- correctness **2** — untestable, hidden lifetimes, GR-2 violated (background ctx
  deep in the stack). Rejected.

## Decision

**Option M1.** `main`: parse `-dir`/`-port`/`-config`/`-peer` flags → root ctx via
`signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` (GR-2) →
`transport.LoadOrCreateIdentity(config)` → seed the allow-list from `-peer` DeviceIDs
(out-of-band pairing; TOFU) and **always trust the engine's own identity** → construct
transport (with a `WithHello` that returns the engine's current root hash + folderID +
feature flags) + `Listen("tcp", :port)` → construct discovery announcing `port` →
construct the reconcile engine over `-dir` → `engine.Run(ctx)` (blocks until ctx is
cancelled, then closes discovery + transport and persists the snapshot). The engine
dials each `PeerDiscovered` address in a goroutine it owns.

## Rationale

- Keeping fan-in inside the engine (GR-4) means `main` is just lifecycle + config; the
  engine is the single writer and the single consumer, exactly as GR-5/GR-4 specify.
- One `signal.NotifyContext` root threaded into transport, discovery, and the engine is
  the consensus graceful-shutdown shape (GR-2); cancelling it tears every subsystem
  down through its own already-reviewed teardown.

## Consequences

- `cmd/msync/main.go` grows from the pre-flight stub to the real wiring; it stays thin
  (flags + ctx + construct + `Run`).
- Allow-list seeding from `-peer` flags is the v1 out-of-band pairing (PR-7 TOFU);
  SAS/interactive pairing is deferred.
- The engine exposes `New(ctx, cfg)` + `Run(ctx)` + a `hello()` provider so it can be
  driven in-process by integration tests with two transports on loopback (no `main`).
- Cross-refs: GR-2/3/4/5, PR-7, the transport/discovery public APIs.
