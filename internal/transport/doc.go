// Package transport carries Merkle Sync's length-prefixed frames over an
// authenticated TLS 1.3 session. It is the network trust boundary: every byte the
// engine ever applies (SR-1) arrives through here, so authentication happens
// before a single application frame is read.
//
// # Identity and trust-on-first-use
//
// Each device has a self-signed certificate; its DeviceID is
// SHA-256(certificate DER) (protocol.DeviceIDFromCert). Trust is an explicit,
// mutable Allowlist of DeviceIDs exchanged out-of-band before first sync (TOFU,
// PR-7). The TLS config sets MinVersion == MaxVersion == VersionTLS13 and
// InsecureSkipVerify with a VerifyConnection callback that pins the peer's
// DeviceID against the Allowlist: there is no CA, so the fingerprint pin replaces
// the (skipped) chain check. Skipping the chain WITHOUT the pin would be plaintext
// in disguise — the pin is mandatory. A wrong fingerprint makes VerifyConnection
// return an error, which aborts the handshake before any frame is read. The first
// post-TLS frame is a HELLO that re-asserts the DeviceID in-band (defence in
// depth); a mismatch drops the connection.
//
// # Concurrency model (GR-3 / GR-4 / GR-13, CDD-1)
//
// A Transport is constructed with the caller's root context (GR-2) and owns every
// goroutine it spawns under one WaitGroup: an accept loop per listener, a
// short-lived establish goroutine per accepted socket, and — once a connection is
// established — a per-conn reader, writer, and supervisor. Each Conn:
//
//   - reads frames with protocol.ReadFrame (io.ReadFull + a pre-allocation length
//     guard), so a frame split across TCP/TLS reads reassembles and an oversized
//     length drops THAT peer with a typed error without ever desyncing others
//     (SR-12);
//   - sends outbound through a buffered channel drained by the writer goroutine.
//     Conn.Send NEVER blocks: a full buffer sheds by dropping the peer, so the
//     engine's select loop can never be wedged by a slow peer (GR-4 companion);
//   - closes idempotently via sync.Once: the one close both unblocks the reader
//     (tls.Conn.Close) and stops the writer (close(closed)).
//
// The engine consumes one fan-in Events() channel carrying PeerConnected,
// PeerMessage, and PeerDisconnected. Per GR-13 this channel is never closed;
// shutdown is context cancellation plus the WaitGroup. A disconnect Event fires
// immediately on conn teardown (not at discovery-eviction time) and carries the
// drop cause in Err.
//
// # Usage contract
//
// Drain Events() in a dedicated goroutine. Do NOT call Dial from that same
// goroutine: Dial blocks through the HELLO exchange and the PeerConnected emit, so
// calling it from the drain loop would self-deadlock. Spawn Dial as its own
// goroutine (GR-4: keep work off the select loop).
//
// See docs/audit/decisions/ws2/*.md, PR-1 §5, PR-7, and the go-rules concurrency
// amendments (CDD-1).
package transport
