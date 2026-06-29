# Decision (WS-3): UDP multicast socket — pure stdlib vs golang.org/x/net/ipv4

- Area: ws3 / internal/discovery (multicast.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-3 implementer
- Plan item: WS-3 #1 (a second instance is discovered within the announce
  interval) — needs a working same-host multicast loopback path.
- Reads-first: SKILL §7 (discovery is a *hint*, never authorisation), GR-11
  (stdlib-first; any further dependency needs a logged decision), AL-16
  (UDP multicast announce + heartbeat eviction), `structure.md` discovery section,
  the synthesis §3.C note that `discovery` is a sibling of `transport` (imports only
  `protocol`).

## Context

Discovery announces `{DeviceID, addr, port}` on a UDP multicast group so two
instances on one LAN find each other without a server. The socket layer must:
(a) let **two instances on the same host** both join the group and both receive a
sent announcement (same-host loopback — the only thing the Mac can verify), and
(b) cross-compile and run on Mac **and** Windows (GR-11). It is the one piece of
WS-3 that touches the OS network stack directly; everything above it (registry
actor, announce codec) is pure Go.

### Evidence gathered before deciding (autonomy contract)

A runnable spike (`scratchpad/mcspike`, 2026-06-29, this Mac, go1.26 darwin/arm64):
- `golang.org/x/net` is **not** in the module cache (`GOMODCACHE/golang.org/x/`
  lists only `mod, sync, telemetry, text, tools`) — adding it needs a network
  fetch that may fail in the sandbox/CI.
- Pure stdlib `net.ListenMulticastUDP("udp4", ifi, group)` with **two** sockets
  bound to the same group:port (it sets address reuse) + a separate ephemeral
  sender: **both members received** the datagram on a real interface (`en0`),
  proving same-host loopback works with IP_MULTICAST_LOOP on by default.
- The **loopback** interface `lo0` did **not** deliver multicast to the two
  members (`i/o timeout`) — macOS does not route the group over `lo0` this way.
  So a "loopback multicast" test must select a real multicast-capable interface,
  and any environment without one (a locked-down CI sandbox) cannot run the real
  socket at all. This is exactly the plan's WS-3 risk ("multicast may block
  discovery: not verifiable on the Mac").

## Options (scored 1–5: correctness / concurrency-safety / testability / cross-platform)

### Option A — pure stdlib: `net.ListenMulticastUDP` to receive + a separate ephemeral `net.ListenUDP` to send (CHOSEN)
- correctness **4** — receives on a chosen interface, joins the group, sets
  address reuse so two same-host instances coexist; verified end-to-end in the
  spike. The one wart: the **outbound** multicast interface is the OS default
  route (stdlib does not expose `IP_MULTICAST_IF`), so on a multi-homed host the
  announce leaves the default interface — acceptable for a 2-device LAN and for a
  *hint*-only layer.
- concurrency-safety **5** — the socket is a thin adapter behind a `datagramConn`
  interface (`ReadDatagram`/`WriteDatagram`/`Close`); one reader goroutine and one
  announcer goroutine, no shared mutable state in the socket itself.
- testability **5** — the `datagramConn` seam lets the authoritative acceptance
  tests run against an **in-memory bus** (deterministic, no real interface, no
  firewall), while a best-effort test exercises the real socket and **skips** if
  the environment cannot multicast (per the spike, `lo0`-only / sandbox boxes).
- cross-platform **5** — `net.ListenMulticastUDP`/`ListenUDP` are stdlib, identical
  API on Mac/Windows/Linux; **zero new dependencies** (GR-11). `GOOS=windows`
  cross-compiles unchanged.

### Option B — `golang.org/x/net/ipv4.PacketConn`
- correctness **5** — full control: `JoinGroup` per interface, explicit
  `SetMulticastLoopback(true)`, `SetMulticastTTL`, `SetMulticastInterface` for the
  outbound side (fixes Option A's only wart). This is what mDNS libraries use.
- concurrency-safety **5**, testability **5** (same `datagramConn` seam).
- cross-platform **4** — works everywhere, **but** pulls in `golang.org/x/net`,
  which is not in the module cache and contradicts GR-11's stdlib-first default;
  it would need a logged dependency justification *and* a network fetch the
  sandbox may not allow. The extra control buys nothing the acceptance criteria
  require (a 2-device LAN hint), so the dependency is not justified for v1.
- **Disqualifier:** unnecessary dependency for the v1 scope; reserved as the
  documented upgrade path if multi-homed outbound-interface control is ever needed.

### Option C — UDP broadcast (`255.255.255.255`) via `net.ListenUDP` + `SetWriteBuffer`/`SetBroadcast`
- correctness **3** — off-spec (plan/README mandates *multicast*), broadcast is
  frequently filtered by switches/firewalls, is noisier (every host's stack sees
  it), and same-host self-delivery has the identical reuse-address requirement —
  so it is not even simpler.
- concurrency-safety **5**, testability **3**, cross-platform **3** (broadcast
  semantics vary; Windows treats directed vs limited broadcast differently).
- **Disqualifier:** contradicts the multicast spec for no benefit.

### Option D — `net.ListenUDP` + raw `setsockopt` (IP_ADD_MEMBERSHIP) via per-OS syscall
- correctness **4**, concurrency-safety **5**, testability **3**,
  cross-platform **2** — needs `//go:build darwin` / `windows` / `linux` syscall
  shims (different constant names and struct layouts per OS), which is precisely
  the maintenance burden stdlib exists to hide.
- **Disqualifier:** re-implements `net.ListenMulticastUDP` by hand; worst
  cross-platform score.

## Decision

**Option A — pure stdlib.** Receive with `net.ListenMulticastUDP("udp4", ifi,
group)`; send with a separate ephemeral `net.ListenUDP("udp4", ":0")` writing to
the group via `WriteToUDPAddrPort`. Hide both behind an unexported `datagramConn`
interface so the registry/announce logic never touches `net` directly. Default
group `239.192.0.77:21027` (an admin-scoped / organisation-local IPv4 multicast
address, RFC 2365 `239.192/14`), overridable via options. Interface defaults to
the first UP, multicast-capable, non-loopback IPv4 interface; overridable.

## Rationale

- GR-11 is explicit: stdlib-first, a new dependency needs justification. Option A
  meets every acceptance criterion with **zero** new dependencies and was proven
  end-to-end on this Mac.
- The `datagramConn` seam is the load-bearing testability move: it decouples the
  GR-4 registry actor (the thing the criteria actually grade) from a real
  interface that the spike proved is environment-dependent. The acceptance tests
  are deterministic; the real socket is covered best-effort.
- Option B's only real advantage (outbound `IP_MULTICAST_IF`) is irrelevant to a
  2-device LAN *hint* and is reserved as a clean future swap behind the same seam.

## Consequences

- On a multi-homed host the announce egresses the OS default multicast route, not
  a pinned interface. Documented; revisit via Option B (swap the `datagramConn`
  impl, no change above it) if multi-homing is ever in scope.
- `lo0`-only or multicast-blocked environments cannot run the real socket; the
  real-socket e2e test **skips** there (never fails). The deterministic bus tests
  remain the acceptance gate. Real Mac↔Windows multicast (and Windows Firewall)
  is closed by `CROSS_PLATFORM_CHECKLIST.md` + the CI matrix (plan §Cross-OS).
- IPv6 multicast is out of scope for v1 (IPv4 `udp4` only); the group/iface
  options leave room to add it later.
- Cross-refs: GR-11, SKILL §7, AL-16, spike `scratchpad/mcspike` (2026-06-29).
