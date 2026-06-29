# Decision (WS-3): peer-event API shape + "hint, never authorisation" boundary

- Area: ws3 / internal/discovery (discovery.go, registry.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-3 implementer
- Plan item: WS-3 #1 (emit `peerEvents{discovered, DeviceID, addr}`), WS-3 #2
  (emit `peerEvents{evicted, DeviceID}`), WS-3 #4 (discovery is a hint, never
  authorisation — no auth decision is taken in discovery).
- Reads-first: SKILL §7 ("Discovery (UDP multicast) is unauthenticated — a hint,
  never authorisation; authentication happens at the TLS layer"), synthesis §3.C
  (`discovery` emits `peerEvents{DeviceID, addr}` → reconcile/cmd → `transport.dial`
  → TLS pins the DeviceID; discovery does **not** import transport), the `transport`
  `Event{Kind, DeviceID, Conn, …}` + `Events()` pattern (the sibling to mirror),
  PR-7 §3 (TLS pin is the authority).

## Context

The engine consumes peer lifecycle from two sibling layers — `transport` (connected/
disconnected/message) and `discovery` (discovered/evicted). The shapes should
compose in one `select` loop. Two questions: **what surface** does discovery expose,
and **where exactly is the trust boundary** — specifically, may discovery consult an
allow-list (and is filtering to self different from authorising)?

## Option set 1 — the consumer-facing surface

### O1-A — a `Discovery` struct exposing `Events() <-chan Event` with `Event{Kind, DeviceID, Addr}` (CHOSEN)
- correctness **5** — mirrors `transport.Event`/`Events()` exactly, so the engine
  drains both with one uniform pattern; `Kind ∈ {PeerDiscovered, PeerEvicted}`,
  `Addr` is a `netip.AddrPort` ready for `transport.Dial(addr.String())`.
- concurrency-safety **5** — the channel is the actor's only output (CDD-1); the
  consumer never touches registry state.
- testability **5** — assert on emitted events (kind/DeviceID/addr), the same
  `waitForKind` style the transport tests already use.
- cross-platform **5**.

### O1-B — callbacks (`OnDiscovered`/`OnEvicted func(...)`)
- correctness 3, concurrency-safety **2** (DISQUALIFIER) — callbacks fire on the
  actor goroutine, so a slow/blocking callback stalls eviction (couples consumer
  latency into the actor), and ordering across the two layers is harder to reason
  about than a single drained channel. Rejected.

### O1-C — expose the registry map (getter returning a snapshot/`[]Peer`)
- correctness 3, concurrency-safety 3, testability 3, cross-platform 5 — a polling
  surface invites the shared-map access CDD-1 forbids and loses the edge-triggered
  "discovered/evicted" signal the engine needs to dial/deregister. Rejected.

## Option set 2 — the trust boundary (criterion 4)

### O2-A — discovery filters announcements against the transport allow-list (only emit allow-listed peers)
- correctness **2** (DISQUALIFIER) — this *is* taking an authorisation decision in
  discovery, the exact thing WS-3 #4 / SKILL §7 forbid, and it would force a
  `discovery → transport` import that synthesis §3.C rules out (the two are
  siblings). It also conflates the *claimed* DeviceID in a spoofable UDP packet with
  the *verified* TLS identity. Rejected.

### O2-B — discovery emits **every** announced peer (DeviceID is an unverified claim/label); self is filtered; authorisation happens only at TLS dial (CHOSEN)
- correctness **5** — matches the layered model: a spoofed/unknown announce only
  produces a dial *hint*; `transport.Dial` then runs the TLS pin and drops anything
  not on the allow-list, so no state from the announce is ever trusted (PR-7 §3).
  Self-filtering (`DeviceID == self → drop`) is not authorisation — it just avoids
  dialling oneself over loopback — so it stays in discovery.
- concurrency-safety **5**, testability **5** — `TestDiscovery_AnnounceIsNotAuth`
  asserts an unknown/spoofed DeviceID still yields a `PeerDiscovered` event (no
  filtering, no auth), and the WS-2 pin test proves the dial of such a peer fails.
- cross-platform **5**.

## Decision

**O1-A + O2-B.** `discovery.Discovery` exposes `Events() <-chan Event` with
`Event{Kind EventKind, DeviceID protocol.DeviceID, Addr netip.AddrPort}` and
`EventKind ∈ {PeerDiscovered, PeerEvicted}` (`Addr` is zero on eviction). Discovery
applies exactly one filter — drop announcements whose DeviceID equals this device's
own — and otherwise registers and emits **every** well-formed announce regardless of
allow-list. It does not import `transport` and takes no authorisation decision; the
TLS pin on dial is the sole authority. The DeviceID carried in an event is the
sender's *claim*; the engine treats it as a routing label and lets TLS confirm it.

## Rationale

- A single channel mirroring `transport.Events()` gives the engine one uniform
  fan-in and keeps the actor non-blocking (no consumer code on the actor goroutine).
- Emitting unauthenticated hints and authorising only at TLS is the project's
  security model (SKILL §7, PR-7); putting any allow-list check in discovery would
  both violate the layering (§3.C, no `discovery→transport` edge) and create a false
  sense that the discovery DeviceID is trustworthy.

## Consequences

- The engine must be prepared for `PeerDiscovered` events for peers it will refuse
  to connect to; the wasted dial is cheap and fails fast at the TLS pin — the
  intended, documented behaviour.
- `Addr` uses `netip.AddrPort` (comparable value type); `Addr.String()` feeds
  `transport.Dial`. Eviction events carry a zero `Addr` (only the DeviceID matters
  to deregister).
- Re-discovery on address change is surfaced as a fresh `PeerDiscovered` (re-dial
  hint); the engine de-dupes by DeviceID as needed.
- Cross-refs: SKILL §7, PR-7 §3, synthesis §3.C, `transport` Event/Events pattern.
