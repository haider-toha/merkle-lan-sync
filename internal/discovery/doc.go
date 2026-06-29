// Package discovery finds Merkle Sync peers on the local network by exchanging
// small UDP multicast announcements, and surfaces them to the engine as dial
// hints. It is the sibling of internal/transport over internal/protocol: it
// imports only protocol (the DeviceID type), never transport, so the two network
// layers stay decoupled and the import graph stays acyclic (synthesis §3.C).
//
// # Discovery is a hint, never authorisation (SKILL §7, PR-7, WS-3 #4)
//
// A multicast datagram is unauthenticated — anyone on the group can send any
// DeviceID. So the DeviceID in an announcement is treated as a *claim*: a routing
// label used as the registry key and to filter out this device's own loopback
// echoes, nothing more. Discovery applies NO allow-list and takes NO authorisation
// decision. The runtime flow is: discovery emits Event{PeerDiscovered, DeviceID,
// Addr} -> the engine dials Addr via transport -> the TLS 1.3 handshake pins
// SHA-256(peer cert DER) against the allow-list and drops anything unknown. The
// dial of a spoofed/unknown announce simply fails the pin. No state from an
// announcement is ever trusted.
//
// # Concurrency model — a GR-4 single-goroutine actor (CDD-1)
//
// The peer registry is owned by exactly one goroutine (registry.run); it is the
// only reader/writer of the peer map and the only emitter on Events(). It is NOT a
// mutex-guarded map touched by several goroutines — that is precisely what CDD-1
// rules out for non-tree shared state. The GR-5 RWMutex guards the reconcile core,
// never discovery. Three goroutines coordinate by channels ("share by
// communicating"):
//
//   - readLoop: drains datagrams from the socket, decodes + self-filters them, and
//     sends decoded announcements (stamped with a receive time) to the actor;
//   - announceLoop: sends this device's announcement immediately on start and then
//     every announce interval;
//   - registry.run (the actor): records announcements, sweeps out peers silent past
//     the eviction timeout on each tick, and emits PeerDiscovered / PeerEvicted.
//
// All goroutines are owned by one WaitGroup under the caller's context (GR-2/GR-3).
// Close() (or cancelling the context) runs an idempotent teardown (sync.Once) that
// closes the socket — unblocking the reader's blocked ReadDatagram, the GR-4
// cancellable-read rule — and cancels the context, stopping the announcer and the
// actor. Per GR-13 the Events() channel is never closed; shutdown is context +
// WaitGroup, and emit selects on a closed channel so it never blocks past shutdown
// (the transport emit pattern).
//
// # Cross-platform multicast reality (decisions/ws3/multicast-socket-stdlib-vs-xnet.md)
//
// The socket uses pure stdlib (net.ListenMulticastUDP to receive on a chosen
// interface + a separate ephemeral socket to send), so there is no new dependency
// (GR-11) and GOOS=windows cross-compiles unchanged. A spike (2026-06-29) proved
// two same-host instances discover each other over a real interface, but that the
// loopback interface (lo0) does NOT route the group — so the socket is hidden
// behind the datagramConn seam and the authoritative acceptance tests run against
// an in-memory bus (deterministic, no real interface, no firewall). Real
// Mac<->Windows multicast and Windows Firewall behaviour are closed by the CI
// matrix + docs/audit/CROSS_PLATFORM_CHECKLIST.md.
//
// See docs/audit/decisions/ws3/*.md, AL-16, SKILL §7, and the go-rules concurrency
// amendments (CDD-1, GR-4/GR-13).
package discovery
