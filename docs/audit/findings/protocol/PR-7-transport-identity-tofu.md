# PR-7 — Transport identity: TLS 1.3 + trust-on-first-use device IDs (vs plaintext)

- Phase / role: Phase 2 — protocol-researcher
- Severity: **high** (authentication gates *all* file data; an unauthenticated peer
  that can feed us writes is a direct corruption + exfiltration channel)
- Status: fixed (WS-2 — see Implementation status; backs
  `decisions/protocol/transport-security-tofu-confirm.md` and confirms Phase 0
  `transport-security-tofu-vs-plaintext.md`)
- Reads-first honoured: `decisions/phase0/transport-security-tofu-vs-plaintext.md`,
  `go-rules.md` GR-11, `findings/codebases/syncthing-source.md` A2-1, SKILL §7.
- Evidence: device-ID derivation + TOFU re-verified at
  [Syncthing — Understanding Device IDs](https://docs.syncthing.net/dev/device-ids.html) and
  [Security Principles](https://docs.syncthing.net/users/security.html) (accessed
  2026-06-28); `NewDeviceID`/`Short` ground-truth `deviceid.go:44-48,106-108`.

---

## 1. Claim

Peers authenticate **without a server or CA** via **TLS 1.3 with self-signed
per-device certificates**, where `DeviceID = SHA-256(cert DER)` and trust is
**trust-on-first-use against an explicit allow-list**: on connect, a custom
`VerifyConnection` computes the peer's DeviceID from the presented cert and **drops
the connection unless it is on the allow-list**. The in-band `HELLO` re-asserts the
DeviceID (defence-in-depth), and UDP discovery is a **hint, never authorisation**.
This is the only option delivering confidentiality + integrity + per-device
authentication under the project's serverless constraint. Plaintext is rejected.

## 2. Why not plaintext (the rejected baseline)

Peers sync over raw TCP on an **untrusted LAN** (coffee-shop Wi-Fi, shared office,
home network with IoT). Plaintext lets any LAN attacker read every file, inject or
rewrite chunks in flight, and impersonate a peer. For an engine whose prime directive
is *no data loss / no corruption*, an **unauthenticated peer that can feed us writes**
is a direct route to corruption and exfiltration — the `RESPONSE` bytes we atomically
commit (SR-1) would be attacker-chosen. Plaintext fails the trust-boundary requirement
outright (`decisions/protocol/transport-security-tofu-confirm.md` Option A).

## 3. The identity scheme (verified)

- **Derivation:** "To form a device ID the SHA-256 hash of the certificate data in DER
  form is calculated" ([Device IDs](https://docs.syncthing.net/dev/device-ids.html),
  accessed 2026-06-28); ground-truth `NewDeviceID(rawCert) = sha256.Sum256(rawCert)`
  (`deviceid.go:44-48`). `DeviceID` is `[32]byte`.
- **Authentication (TOFU):** on connect "Calculate the remote device ID by processing
  the received certificate as above" then "Verify the remote device ID against the
  configuration. If it is not a device ID we are expecting to talk to, drop the
  connection" (Device IDs page, accessed 2026-06-28). Pairing adds device IDs to the
  allow-list **out-of-band** before the first sync.
- **`ShortID` reuse:** the high 64 bits of the DeviceID (`Short()`, `deviceid.go:106-108`)
  are the **version-vector counter key** (PR-2; `version-vectors` §8 A-adopt-4) — one
  cryptographic identity serves both auth and causality, and keeps each VV counter 8
  bytes. Collision risk at LAN scale (≤ tens of devices) is negligible; documented.
- **Human encoding** (base32 + Luhn + dash chunking, `deviceid.go:178-239`) is
  presentation-only; we keep a plain hex/base32 string and skip the GUI flourish (N15).

## 4. The hardening (protocol-visible — see the decision for the full rationale)

1. **Pin in `tls.Config.VerifyConnection`, before any frame is read.** `MinVersion:
   tls.VersionTLS13`; `InsecureSkipVerify: true` (we *intend* to skip the CA chain —
   there is no CA) **plus** a `VerifyConnection` callback that computes
   `SHA-256(cs.PeerCertificates[0].Raw)` and errors out (→ handshake fails) unless that
   DeviceID is allow-listed. **Skipping the CA chain without the fingerprint check would
   be plaintext in disguise** — the fingerprint check is mandatory.
2. **`HELLO` re-asserts identity in-band.** First post-TLS frame carries `deviceID`
   (PR-1 §4); receiver checks `HELLO.deviceID == TLS-pinned DeviceID`, drops on mismatch.
   Catches a cert swapped between pinning and use.
3. **Discovery is a hint, never authorisation.** A spoofed UDP announce at worst points
   us at an address whose TLS identity then fails the allow-list; authentication is
   *exclusively* at the TLS layer.

## 5. Honest disclosure — the first-connection TOFU weakness

TOFU's weak point is first contact: "susceptible to man-in-the-middle attacks on first
contact" ([Trust on first use, Wikipedia](https://en.wikipedia.org/wiki/Trust_on_first_use), accessed 2026-06-28). We
mitigate with a **paired allow-list** (device IDs exchanged out-of-band *before* the
first sync), so an attacker must MitM **and** know/inject the exact expected DeviceID —
stronger than blind SSH-style accept-on-first-sight. A short-authentication-string
verification is a deferred enhancement. After first contact, every connection is
cryptographically verified.

## 6. Why it fits the project (the four axes)

- **correctness:** TLS 1.3 = confidentiality + integrity; SHA-256 cert pinning = strong
  per-device auth after pairing.
- **concurrency-safety:** `crypto/tls` wraps the `net.Conn`; framing + the per-conn
  reader/writer goroutine model (GR-3/GR-4) are unchanged; pinning is a pure function.
- **testability:** two in-memory self-signed certs over loopback/`net.Pipe`; assert a
  wrong fingerprint is rejected and a right one pins — entirely on one Mac.
- **cross-platform:** `crypto/tls` + `crypto/x509` behave identically on Mac and
  Windows; DeviceIDs are raw bytes (GR-11 stdlib-only — we write zero crypto).

## 7. Test obligations

1. Right fingerprint → handshake succeeds, DeviceID pinned; wrong fingerprint →
   connection dropped in `VerifyConnection` (no frame read).
2. `HELLO.deviceID` ≠ TLS-pinned DeviceID → dropped (defence-in-depth).
3. Spoofed discovery announce pointing at a non-allow-listed peer → TLS auth fails;
   no data exchanged.
4. `DeviceIDFromCert` determinism + `Short()` (high 64 bits) round-trip.

## 8. Cross-references

- Decision: `protocol/transport-security-tofu-confirm.md` (the ≥3-option re-score),
  Phase 0 `transport-security-tofu-vs-plaintext.md`.
- Findings: PR-1 (HELLO carries + re-asserts DeviceID; framing runs inside TLS),
  PR-2 (ShortID is the VV key); `codebases/syncthing-source.md` A2-1.
- Rules: GR-11 (stdlib-only crypto), SR-1 (we atomically commit RESPONSE bytes — hence
  they must be authenticated).
- Lands in `internal/transport/{identity.go, tls.go, dial.go, conn.go}`,
  `internal/protocol/deviceid.go`.

## Implementation status (WS-0 — partial)

**Landed in WS-0** — commit `801d0949561e648646782b10a3d514abd0981242` on branch `feat/merkle-sync-engine`
(`internal/protocol/deviceid.go`): the §3 identity **primitives** —
`DeviceIDFromCert(der) = SHA-256(cert DER)` (deterministic `[32]byte`) and `Short()`
= the high 64 bits big-endian (the version-vector counter key), plus hex `String()`
and `ParseDeviceID`. Tests (`deviceid_test.go`, green under `-race`):
`TestDeviceIDFromCert_Deterministic`, `TestShort_HighBits`,
`TestShort_IsVersionVectorKey`, `TestDeviceID_HexRoundTrip`, `TestDeviceID_Comparable`
— this is §7 obligation #4.

## Implementation status (WS-2 — complete)

**Landed in WS-2** — commit `<WS2-COMMIT-SHA>` on branch `feat/merkle-sync-engine`
(`internal/transport/{identity.go, tls.go, conn.go, transport.go, listener.go,
dial.go, doc.go}`): all of the previously-remaining §4 hardening + §5 TOFU allow-list
+ §7 obligations 1–3.

- **§3 cert side of identity:** `GenerateIdentity` / `LoadOrCreateIdentity` mint a
  self-signed ECDSA P-256 cert and derive `DeviceID = SHA-256(leaf DER)` via the
  WS-0 `protocol.DeviceIDFromCert`; the cert is persisted (atomic 0600 key) so the
  DeviceID is stable across restarts (decision
  `decisions/ws2/device-identity-cert-and-persistence.md`).
- **§4.1 pin in `VerifyConnection`, before any frame is read:** `baseTLSConfig` sets
  `MinVersion == MaxVersion == VersionTLS13`, `InsecureSkipVerify: true`, and
  `VerifyConnection = pinVerifier(allow)` computing `SHA-256(PeerCertificates[0].Raw)`
  and rejecting (`ErrUntrustedDevice`) unless allow-listed; the server adds
  `ClientAuth: RequireAnyClientCert` so the dialer's cert is pinned too. A wrong
  fingerprint fails the handshake before any HELLO/frame is read
  (`TestTLS_WrongFingerprintRejected`, `TestPinVerifier`; decision
  `decisions/ws2/tls-config-and-deviceid-pinning.md`).
- **§4.2 HELLO re-asserts identity in-band:** `establish` exchanges HELLO after the
  TLS pin and drops on `HELLO.DeviceID != TLS-pinned`
  (`ErrHelloDeviceMismatch`, `TestHELLO_DeviceIDMismatchDropped`); the engine's HELLO
  fields ride out on `PeerConnected` (`TestHello_CarriesEngineFields`; decision
  `decisions/ws2/connection-establishment-events-and-hello.md`).
- **§4.3 discovery is a hint:** transport authenticates exclusively at the TLS layer;
  no state is trusted from any announce (WS-3 carries the announce side).
- **§5 paired allow-list:** `Allowlist` (concurrency-safe `Add`/`Remove`/`Allowed`)
  is the TOFU allow-list (`TestAllowlist_*`).
- **§7 obligations 1–3:** #1 right/wrong fingerprint (`TestTLS_PinsIdentity` +
  `TestTLS_WrongFingerprintRejected`), #2 HELLO mismatch dropped
  (`TestHELLO_DeviceIDMismatchDropped`), #3 spoofed-announce⇒unknown TLS identity
  rejected (covered by the same pin + allow-list path; the announce-spoof side is
  WS-3's `TestDiscovery_AnnounceIsNotAuth`). Obligation #4 was already WS-0.

All green under `go test ./... -race`.

## Implementation status (WS-0 — partial, superseded)
