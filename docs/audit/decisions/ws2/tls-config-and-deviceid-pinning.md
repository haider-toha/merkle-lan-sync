# Decision (WS-2): TLS 1.3 config + where/how DeviceID pinning happens

- Area: ws2 / internal/transport (tls.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-2 implementer
- Plan item: WS-2 #3 — "The TLS handshake pins a device identity ... a wrong
  fingerprint fails the handshake **before any frame is read**."
- Reads-first: PR-7 §4 (pin in `VerifyConnection`, before any frame read),
  `decisions/protocol/transport-security-tofu-confirm.md` (Option C hardening),
  GR-11 (stdlib `crypto/tls`).

## Context

Authentication gates every file byte (PR-7 §1). There is no CA, so the chain check
is meaningless; the **fingerprint pin against an allow-list** must replace it, and
it must run **inside the TLS handshake** so a wrong peer is dropped before any
application frame is read. The choice is the exact `crypto/tls` mechanism and the
allow-list shape. Mutual authentication is required (both peers prove identity), so
the **server must request a client cert**.

## Options (scored 1–5: correctness / concurrency-safety / testability / cross-platform)

### Option A — pin in `VerifyPeerCertificate`
- correctness **4**, concurrency **5**, testability **4**, cross-platform **5**.
  Works, but `VerifyPeerCertificate` receives raw/verified chains and is the lower-
  level hook; with `InsecureSkipVerify` the `verifiedChains` arg is nil, so you must
  re-parse `rawCerts[0]` yourself. Functional but more error-prone than the
  connection-level hook, and historically the spot people get subtly wrong.

### Option B — handshake first, then verify the pinned ID *after* `Handshake()` returns, in application code
- correctness **2** (DISQUALIFIER), concurrency **5**, testability **5**,
  cross-platform **5**. Fails the explicit requirement "before any frame is read":
  the handshake *completes* (the peer is told "ok") and only then does app code
  inspect `ConnectionState`. A buggy caller could read a frame first. Rejected — the
  pin must abort the handshake itself.

### Option C — `InsecureSkipVerify: true` + `VerifyConnection` pinning `SHA-256(PeerCertificates[0].Raw)` against the allow-list; server sets `ClientAuth: RequireAnyClientCert` (CHOSEN)
- correctness **5** — `VerifyConnection` is documented to run **for all connections
  regardless of `InsecureSkipVerify` or `ClientAuth`**, *during* the handshake; a
  non-nil return **aborts the handshake**. So a wrong fingerprint fails the
  handshake (no frame read), exactly the requirement. `InsecureSkipVerify` skips the
  (nonexistent) CA chain; the pin replaces it — skipping the chain **without** the
  pin would be plaintext-in-disguise (PR-7 §4.1), so the pin is mandatory.
  `MinVersion == MaxVersion == VersionTLS13`. The server uses
  `ClientAuth: RequireAnyClientCert` so the dialer is forced to present a cert
  (required but **not** chain-verified) which `VerifyConnection` then pins — without
  it the server gets no `PeerCertificates` and cannot authenticate the client.
- concurrency **5** — the verifier is a pure closure over an immutable-by-contract
  allow-list (its own RWMutex); `*tls.Config` is read-only after construction and
  safe to share across goroutines.
- testability **5** — `VerifyConnection` is unit-testable directly with a synthetic
  `tls.ConnectionState`; the end-to-end pin is testable over loopback `tls.Conn` on
  one Mac (`TestPinVerifier`, `TestTLS_PinsIdentity`,
  `TestTLS_WrongFingerprintRejected`).
- cross-platform **5** — `crypto/tls`/`crypto/x509` behave identically on Mac and
  Windows.

## Decision

**Option C.** A `baseTLSConfig` sets `Certificates`, `MinVersion=MaxVersion=
VersionTLS13`, `InsecureSkipVerify=true`, and `VerifyConnection=pinVerifier(allow)`;
`serverTLSConfig` additionally sets `ClientAuth: RequireAnyClientCert`. The
allow-list is a small **concurrency-safe `Allowlist`** type (`RWMutex` set of
`DeviceID`) with `Add`/`Remove`/`Allowed`, so out-of-band pairing can update it
while the accept loop verifies another peer.

`pinVerifier` returns `ErrNoPeerCert` if no cert was presented and
`ErrUntrustedDevice` (wrapped, with the offending DeviceID) if the pinned ID is not
allow-listed — surfaced as a failed handshake.

## Rationale

- `VerifyConnection` is the one hook that (1) runs even under `InsecureSkipVerify`,
  (2) aborts the handshake on error, and (3) exposes the parsed
  `PeerCertificates[0].Raw` for `SHA-256` — giving "pin before any frame is read"
  with the least code. (Go `crypto/tls` docs, `VerifyConnection` field.)
- A mutable concurrency-safe allow-list matches TOFU pairing (PR-7 §5) without a
  restart.

## Consequences

- `internal/transport/tls.go`: `Allowlist`, `NewAllowlist`, `pinVerifier`,
  `serverTLSConfig`/`clientTLSConfig`/`baseTLSConfig`, sentinels `ErrUntrustedDevice`,
  `ErrNoPeerCert`.
- The pinned DeviceID is recomputed from `ConnectionState().PeerCertificates[0]`
  after the handshake in `establish()` and is the identity the in-band HELLO must
  re-assert (see `connection-establishment-events-and-hello.md`).
- Cross-refs: PR-7 §4, transport-security-tofu-confirm Option C.
