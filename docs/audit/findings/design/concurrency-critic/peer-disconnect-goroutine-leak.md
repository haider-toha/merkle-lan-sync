---
id: concurrency-critic-3
title: Per-connection reader/writer have no specified mutual-close handshake and the root context only covers global shutdown, so every single-peer disconnect leaks the sibling goroutine plus the engine's per-peer routing state
severity: high
status: rejected
---

# concurrency-critic-3 — Goroutine (and per-peer state) leak on a single peer disconnect

## Claim

GR-3 makes "no leaks on peer disconnect" a **graded** invariant and
`transport_test.go` is required to include a "goroutine-leak-on-disconnect" test
(`structure.md:96`). Yet the design's teardown mechanism, as specified, only
handles **global** shutdown, not the disconnect of **one** peer. `conn.go` lists
"per-conn reader + writer goroutines; ... **ctx-cancel/close**"
(`structure.md:93`) and `listener.go` waits them with a `WaitGroup` on "graceful
shutdown **on ctx**" (`structure.md:94`). But the root `ctx` (GR-2,
`signal.NotifyContext`) fires only on process interrupt — it does **not** fire when
a single peer drops. So on an ordinary single-peer disconnect there is no specified
signal that makes the *sibling* goroutine and the *engine's per-peer state* go
away. The reader exits on its read error; the writer is left blocked forever on its
outbound channel; the engine's per-peer outbound channel, registry entry, and
ack/last-index state are never reclaimed. That is a goroutine leak **and** a memory
leak on every peer churn — exactly the failure GR-3 and the required leak test
target.

## Evidence

Teardown is specified only against the root ctx:

- `docs/audit/plan/structure.md:93` — "`conn.go` | ... per-conn reader + writer
  goroutines; frame loop via `protocol`; **ctx-cancel/close**."
- `docs/audit/plan/structure.md:94` — "`listener.go` | TCP `Accept` loop; spawn
  per-conn handlers; `WaitGroup`; graceful shutdown **on ctx**."
- Root ctx is global-only: `docs/audit/rules/go-rules.md:36-49` (GR-2) — one root
  context from `signal.NotifyContext(..., os.Interrupt, syscall.SIGTERM)`. It is
  cancelled by **signals**, not by a peer's `conn.Read` returning `io.EOF`.
- GR-3 states the obligation precisely but the design does not implement its two
  halves: `go-rules.md:62-65` — "On peer disconnect, the per-connection reader and
  writer goroutines must **both** exit (close the conn → unblock the reader →
  cancel the writer) and be `Wait`ed." The parenthetical *is* the missing
  handshake; `structure.md` never names it (it says only "ctx-cancel/close").
- The graded test exists but the mechanism to pass it does not:
  `structure.md:96` — `transport_test.go` ... "**goroutine-leak-on-disconnect**";
  `go-rules.md:66-72` — the leak test asserts `runtime.NumGoroutine()` returns to
  baseline after connect/disconnect churn.

The concrete leak interleaving (single peer drops; root ctx still live):

```
1. Peer P's TCP connection drops (P crashes / network blip).
2. A's per-conn READER: conn.Read returns io.EOF/err → reader goroutine returns.
   (Good — but nothing else is signalled.)
3. A's per-conn WRITER is parked on `<-outboundCh` (or in conn.Write). Nothing
   closes outboundCh or the conn from the reader's exit path (not specified), and
   the root ctx has NOT fired (only P died) → writer blocks FOREVER. LEAK #1.
4. The engine still holds P's entry in its outbound-routing map / registry and may
   still send to outboundCh (now drained by nobody) → either it blocks (ties to
   concurrency-critic-2) or the buffered frames + the channel + P's ack/last-index
   state are retained forever. LEAK #2 (memory + per-peer state).
5. There is no specified "peer P disconnected" event from transport back to the
   engine to deregister P. peerEvents flows FROM discovery (structure.md:106),
   not from transport on disconnect. So the engine never learns to drop P.
```

After N connect/disconnect cycles, `runtime.NumGoroutine()` grows by ~N (one
orphaned writer each) and per-peer maps grow unbounded — the leak test fails, and
in production a flaky peer slowly exhausts goroutines/FDs.

## Impact

- **High (graded invariant).** This is the exact failure the concurrency-critic is
  tasked to find and that `transport_test.go` must catch. A LAN sync daemon
  *expects* peer churn (laptops sleep, Wi-Fi drops); each churn leaks a goroutine
  and pins per-peer state, so the daemon degrades over days of uptime until it
  exhausts goroutines / file descriptors and stops accepting peers.
- It also **compounds concurrency-critic-2**: a leaked, undrained outbound channel
  is precisely what makes the engine block on send to a dead peer.

## Recommended-change

Specify a **per-connection lifecycle** independent of the root ctx:

1. **Per-conn child context.** Derive `connCtx, connCancel := context.WithCancel(rootCtx)`
   per accepted/dialled connection. Either goroutine that errors (reader on
   `Read`, writer on `Write` or on a closed outbound) calls `connCancel()` and a
   `sync.Once`-guarded `conn.Close()`. Closing the conn unblocks the sibling's
   blocked `Read`/`Write`; cancelling `connCtx` unblocks any `select`. This is the
   "close the conn → unblock the reader → cancel the writer" handshake GR-3
   already prescribes (`go-rules.md:62-65`) but `structure.md` omits.
2. **Per-conn WaitGroup, owned by whoever spawned the pair** (listener for inbound,
   dialer for outbound). The owner `Wait`s both goroutines, then tears down — so
   the pair is reclaimed on *single-peer* disconnect, not only on global shutdown.
3. **A transport→engine "peer disconnected" event.** Add a `peerEvents{kind:
   disconnected, DeviceID}` (or a dedicated channel) emitted when a conn tears
   down, so the engine deregisters P: drop P's outbound channel from the routing
   map and release P's ack/last-index state. Without this the goroutines can exit
   but the engine's per-peer *state* still leaks (LEAK #2).
4. **Test exactly the churn:** connect/disconnect N peers in a loop and assert
   `runtime.NumGoroutine()` returns to a baseline (± a small constant) after the
   owner `Wait`s, with `-race` on (GR-3, GR-13). Also assert the engine's per-peer
   map size returns to baseline (catches LEAK #2, which a goroutine-count-only test
   misses).

This beats the status quo ("ctx-cancel/close" + a root-ctx `WaitGroup`) because the
status quo only reclaims goroutines when the **whole process** shuts down; a
per-conn ctx + close-once + a disconnect event reclaims both goroutines **and**
per-peer state on every individual peer drop, which is the actual runtime condition.
