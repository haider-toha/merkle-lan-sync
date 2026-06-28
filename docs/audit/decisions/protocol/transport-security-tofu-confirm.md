# Decision: confirm & harden transport security — TLS 1.3 + TOFU device IDs vs plaintext

- Area: protocol / transport (Phase 2 — protocol-researcher)
- Status: decided (confirms and hardens the Phase 0 baseline
  `docs/audit/decisions/phase0/transport-security-tofu-vs-plaintext.md`)
- Date: 2026-06-28
- Decider: protocol-researcher (Phase 2)
- Task mandate: "DECIDE & LOG (decisions/protocol/): TLS trust-on-first-use device
  IDs (Syncthing-style) vs plaintext."

## Context

The Phase 0 rules-architect already chose **Option C** (TLS 1.3, self-signed
per-device certs, `DeviceID = SHA-256(cert DER)`, trust-on-first-use against an
explicit allow-list). My task is to **confirm or harden** that choice from the
protocol layer, where the handshake actually runs, and to nail down the
**protocol-visible** consequences: where DeviceID pinning happens relative to the
in-band `HELLO`, what the handshake state machine is, and how discovery (an
unauthenticated UDP hint) composes with it. This re-opens the decision honestly
(≥3 options re-scored) rather than rubber-stamping it, because every byte of file
data rides on it.

Constraint set (unchanged from Phase 0, restated so the re-score is grounded):
no central server, no CA/PKI, two peers on an untrusted LAN; the transport must
deliver **confidentiality + integrity + per-device authentication** before a
single file byte moves. Stdlib-only is a hard rule (GR-11).

## Options (scored 1–5, 5 = best; axes = correctness / concurrency-safety / testability / cross-platform)

### Option A — plaintext TCP (no encryption, no authentication)
- correctness **2**, concurrency **5**, testability **5**, cross-platform **5**.
- **Disqualifier (security/correctness):** any LAN attacker can read every file,
  inject/rewrite chunks, and impersonate a peer. For an engine whose prime
  directive is *no data loss / no corruption*, an unauthenticated peer that can
  feed us writes is a direct corruption + exfiltration channel. The "correctness"
  score is for *moving bytes*; it fails the *trust-boundary* requirement outright.
  Rejected (same verdict as Phase 0).

### Option B — TLS 1.3 with a CA / PKI (CA-signed certs)
- correctness **5**, concurrency **5**, testability **3** (needs a test CA +
  enrolment fixtures), cross-platform **5**.
- **Disqualifier (scope):** requires a certificate authority and an enrolment
  story. There is no server to host a CA and no operator to issue certs; PKI on two
  laptops is the wrong shape for a serverless tool. Rejected.

### Option C — TLS 1.3, self-signed per-device certs, `DeviceID = SHA-256(cert DER)`, TOFU allow-list, **pinning in `VerifyConnection`** (CHOSEN — confirms Phase 0)
Each instance generates a keypair + self-signed cert on first run; the device ID
is the SHA-256 of the cert DER. Pairing exchanges device IDs out-of-band into each
side's allow-list. On connect: TLS 1.3 handshake; a custom `VerifyConnection`
callback computes the peer's DeviceID from the presented leaf cert and **drops the
connection if it is not on the allow-list**. The in-band `HELLO` then *re-asserts*
the DeviceID as defence-in-depth (it must equal the TLS-pinned one).
- correctness **5** — TLS 1.3 = confidentiality + integrity; SHA-256 cert pinning =
  strong per-device auth after pairing. Verified current: "To form a device ID the
  SHA-256 hash of the certificate data in DER form is calculated"; on connect
  "Calculate the remote device ID … Verify the remote device ID against the
  configuration. If it is not a device ID we are expecting to talk to, drop the
  connection" ([Syncthing — Understanding Device IDs](https://docs.syncthing.net/dev/device-ids.html), accessed 2026-06-28).
- concurrency **5** — `crypto/tls` wraps the `net.Conn`; the framing + per-conn
  reader/writer goroutine model (GR-3/GR-4) is unchanged; pinning is a pure
  function of the presented cert.
- testability **5** — generate two in-memory self-signed certs, wire two `tls.Conn`
  over loopback/`net.Pipe`, assert a wrong fingerprint is rejected and a right one
  pins; runs entirely on one Mac (closes the cross-OS gap that Phase 6 CI handles).
- cross-platform **5** — `crypto/tls` + `crypto/x509` behave identically on Mac and
  Windows; DeviceIDs are raw bytes.

### Option D — pre-shared key / symmetric (shared passphrase → AEAD/Noise)
- correctness **4**, concurrency **5**, testability **4**, cross-platform **5**.
- **Cost:** one shared secret for the whole pair (no per-device identity → cannot
  tell peers apart or revoke one device), and pulls in a non-stdlib Noise
  implementation (violates GR-11). TLS+TOFU gives per-device identity for free from
  the stdlib. Kept only as a documented fallback. Not chosen.

## Decision

**Confirm Option C and harden three protocol-visible details:**

1. **Pinning happens in `tls.Config.VerifyConnection`, before any frame is read.**
   Set `InsecureSkipVerify: true` (we *intend* to skip the CA-chain check — there is
   no CA) **and** a `VerifyConnection` callback that (a) takes the verified leaf
   certificate `cs.PeerCertificates[0]`, (b) computes `DeviceID =
   SHA-256(cert.Raw)`, (c) returns a non-nil error (→ handshake fails, conn closed)
   unless that DeviceID is in the allow-list. Skipping the CA chain **without** the
   fingerprint check would be Option A in disguise — the fingerprint check is
   mandatory. `MinVersion: tls.VersionTLS13`.
2. **`HELLO` re-asserts identity in-band (defence-in-depth).** After the TLS
   handshake the first frame each side sends is `HELLO{protoVersion, deviceID,
   folderID, rootHash}` (see `message-type-enumeration.md`). The receiver verifies
   `HELLO.deviceID == the TLS-pinned DeviceID`; a mismatch drops the connection.
   This costs nothing and catches a class of implementation bugs (e.g. a cert
   swapped between pinning and use).
3. **Discovery is a hint, never authorisation.** UDP multicast announcements are
   unauthenticated by nature; a spoofed announce at worst points us at an address
   whose TLS identity then fails the allow-list. Authentication is *exclusively* at
   the TLS layer. (Re-affirmed from Phase 0; binds WS-3.)

DeviceIDs are stored/compared as raw 32-byte SHA-256 values; the human encoding is
presentation-only. The high 64 bits (`ShortID`) are reused as the version-vector
counter key (`version-vectors` §8 A-adopt-4; `vv-counter-seeding.md`).

## Rationale

- It is the **only** option satisfying confidentiality + integrity + per-device
  authentication **without** a server or CA — exactly the project's constraint set.
- It reuses a proven, audited design (Syncthing) and the Go standard library: we
  write **zero** crypto primitives, we only pin a hash.
- It is **fully testable on one Mac**, which matters because cross-OS hardware is
  the one thing this project cannot assume (plan/README.md).
- Hardening the pinning *location* (`VerifyConnection`, pre-frame) and the in-band
  `HELLO` re-assert closes the two implementation footguns the protocol-critic will
  hunt for: trusting a cert before pinning, and trusting `HELLO.deviceID` without
  cross-checking the TLS identity.

## Consequences

- **First-connection TOFU weakness (honest disclosure, unchanged):** TOFU's weak
  point is first contact ("susceptible to man-in-the-middle attacks on first
  contact" — [Trust on first use, Wikipedia](https://en.wikipedia.org/wiki/Trust_on_first_use), accessed 2026-06-28). We mitigate with a
  *paired allow-list* (device IDs exchanged out-of-band **before** first sync), so
  an attacker must MitM *and* know/inject the exact expected DeviceID — stronger
  than blind SSH-style accept-on-first-sight. A short-authentication-string upgrade
  is deferred.
- Drives `internal/transport/{identity.go, tls.go, dial.go, conn.go}` and
  `internal/protocol/deviceid.go` (`DeviceIDFromCert`, `Short`). The framing layer
  (`internal/protocol/framing.go`) runs **inside** the TLS session, so length-prefix
  bytes are themselves encrypted.
- Certs/keys persist per device under the config dir; loss of the key = new
  DeviceID = re-pair (acceptable for a 2-device LAN tool).
- Cross-references: `transport-security-tofu-vs-plaintext.md` (Phase 0),
  `message-type-enumeration.md` (HELLO carries + re-asserts DeviceID),
  GR-11 (stdlib-only), and finding `PR-7-transport-identity-tofu.md` (the research
  artifact backing this decision).
