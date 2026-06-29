# Decision (WS-2): device identity — key type, self-signed cert template, persistence

- Area: ws2 / internal/transport (identity.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-2 implementer
- Plan item: WS-2 #3 substrate (the cert whose DER hash *is* the pinned DeviceID).
- Reads-first: PR-7 §3 (DeviceID = SHA-256(cert DER), TOFU allow-list),
  `decisions/protocol/transport-security-tofu-confirm.md`,
  `decisions/phase0/transport-security-tofu-vs-plaintext.md`, GR-11 (stdlib-only),
  GR-12/SR-1 (atomic persistence), `internal/protocol/deviceid.go`
  (`DeviceIDFromCert`).

## Context

The transport authenticates peers by **TLS 1.3 + trust-on-first-use**, where
`DeviceID = SHA-256(leaf certificate DER)`. WS-0 already shipped the *derivation*
primitive (`protocol.DeviceIDFromCert`). WS-2 must (a) **mint** a self-signed
per-device certificate + private key, and (b) **persist** them so a daemon keeps a
stable DeviceID across restarts (losing the key = a new DeviceID = a forced
re-pair, PR-7 Consequences). Two sub-choices: the **key/signature algorithm** and
the **persistence shape**. Constraint: stdlib only (`crypto/{ecdsa,ed25519,rsa,
x509,tls}`, GR-11); identical behaviour on Mac and Windows; cheap enough that a
test can mint many distinct identities.

## Options — key/signature algorithm (scored 1–5: correctness / concurrency-safety / testability / cross-platform)

### Option A — RSA-2048 self-signed
- correctness **5**, concurrency **5**, testability **2** (RSA-2048 keygen is
  10–100 ms; a churn/leak test minting dozens of identities pays seconds),
  cross-platform **5**. Universally interoperable but the slowest keygen — a real
  cost for the table-driven, identity-per-iteration tests this WS mandates.

### Option B — Ed25519 self-signed
- correctness **5**, concurrency **5**, testability **5** (fastest keygen, smallest
  keys), cross-platform **4**. Fully TLS-1.3-capable in Go's stdlib since 1.13 and
  OS-independent (it is Go's own implementation, not the platform's). Excellent;
  the only reason it is not chosen is interop conservatism (see Decision).

### Option C — ECDSA P-256 self-signed (CHOSEN)
- correctness **5** — a standard TLS device-cert algorithm; signature + handshake
  fully supported by `crypto/tls` TLS 1.3.
- concurrency **5** — keygen/sign are pure; the `tls.Certificate` is immutable once
  built and shared read-only across goroutines.
- testability **5** — P-256 keygen is sub-millisecond, so minting a fresh identity
  per churn iteration (WS-2 #4) is free; two mints yield distinct DER ⇒ distinct
  DeviceIDs deterministically.
- cross-platform **5** — `crypto/ecdsa` + `crypto/x509` are pure-Go stdlib,
  byte-identical on Mac and Windows; the DeviceID is `SHA-256(DER)`, OS-independent.

## Decision (algorithm)

**Option C — ECDSA P-256.** It is the conventional, Syncthing-aligned choice for a
self-signed TLS device cert, has sub-ms keygen (matters for the leak/churn tests),
and is unambiguously TLS-1.3-supported on every Go target. Ed25519 (Option B) is an
equally sound stdlib alternative and is recorded as the drop-in fallback if a future
need (e.g. even smaller certs) arises; RSA is rejected on keygen cost alone.

Cert template: self-signed (issuer == subject), random 128-bit serial,
`CommonName: "merkle-sync"`, `NotBefore = now-1h`, `NotAfter = now+100y`,
`KeyUsageDigitalSignature`, `ExtKeyUsage{ServerAuth, ClientAuth}`. Validity is a
generous fixed window because identity is pinned by **DER hash**, not chain
validity: `InsecureSkipVerify` skips the chain+expiry check and `VerifyConnection`
is the gate (see `tls-config-and-deviceid-pinning.md`), so expiry never gates the
handshake — the long window only avoids a confusing "expired" cert in tooling.

## Options — persistence

### P1 — in-memory only (regenerate every start)
- A new DeviceID every restart ⇒ re-pair on every restart. Rejected for the daemon;
  retained only as `GenerateIdentity()` for tests/first-run.

### P2 — single combined PEM file
- Workable but mixes a 0600-secret (key) and a public artifact (cert) in one file,
  forcing the whole file to 0600. Minor; rejected for clarity.

### P3 — separate `device.crt` (0644) + `device.key` (0600), atomic write (CHOSEN)
- correctness **5**, concurrency **5**, testability **5**, cross-platform **5**.
  Mirrors conventional TLS on-disk layout; the secret key is mode 0600 and written
  via **temp → fsync → rename** (SR-1 spirit, so a crash never leaves a half-written
  key); the public cert is 0644. `LoadOrCreateIdentity(dir)` loads when both files
  exist, mints+persists on first run (neither exists), and **refuses** (does not
  silently regenerate) if exactly one exists or a read fails — silent regeneration
  would rotate the DeviceID and break pairing.

## Decision (persistence)

**Option P3.** `GenerateIdentity()` (pure, no I/O) + `LoadOrCreateIdentity(dir)`
(separate PEM files, atomic 0600 key, fail-closed on inconsistency).

## Rationale

- Stdlib-only (GR-11); zero third-party crypto (we only hash a DER we already
  produced with `protocol.DeviceIDFromCert`).
- Fast keygen keeps the mandated identity-per-iteration tests cheap and
  deterministic (distinct DER ⇒ distinct DeviceID).
- Atomic key write + fail-closed load protect the one piece of state whose loss
  forces a re-pair.

## Consequences

- `internal/transport/identity.go`: `Identity{Certificate tls.Certificate; DeviceID
  protocol.DeviceID}`, `GenerateIdentity()`, `LoadOrCreateIdentity(dir)`.
- `Certificate.Leaf` is populated explicitly after load (robust across Go versions),
  so `DeviceIDFromCert(Certificate.Certificate[0])` is always well-defined.
- The config-dir path is operator-chosen (not a synced canonical path); Windows
  reserved-name *directories* are out of transport scope. The load-bearing
  cross-platform property — that path-bearing *payloads* survive the wire
  byte-for-byte — is enforced by `TestConn_HostilePathPayloadsRoundTrip`.
- Cross-refs: PR-7 §3, `tls-config-and-deviceid-pinning.md`.
