# Decision: transport security — TLS with trust-on-first-use device IDs vs plaintext

- Area: phase0 / transport (also tagged ws2 per roster)
- Status: decided (Phase 0 baseline — protocol-researcher confirms the device-ID
  derivation and handshake details in Phase 2)
- Date: 2026-06-28
- Decider: rules-architect

## Context

Peers sync over a raw TCP connection on an untrusted LAN (coffee-shop Wi-Fi, a
shared office network, a home network with IoT devices). There is **no central
server and no CA/PKI** — the whole point of the project is decentralised LAN
sync. The transport must answer three questions before a single file byte moves:

1. **Confidentiality** — can a passive sniffer on the LAN read the files?
2. **Integrity** — can an active attacker tamper with chunks in flight?
3. **Authentication** — is the peer who they claim to be, so we do not sync our
   folder into an attacker's machine or accept their writes into ours?

This decision gates *all* network data, so it is logged before any transport
code is written.

## Options (scored 1–5, 5 = best)

### Option A — plaintext TCP (no encryption, no authentication)

- Correctness: **3** — moves bytes correctly...
- Concurrency-safety: **5** — irrelevant to this axis.
- Testability: **5** — trivial.
- Cross-platform: **5** — `net.Dial`/`net.Listen` only.
- **Disqualifier (security):** anyone on the LAN can read every file, inject or
  rewrite chunks, and impersonate a peer. For a file-sync engine whose explicit
  invariant is "no data loss / no corruption," an unauthenticated peer that can
  feed us writes is a direct route to corruption and exfiltration. Rejected.

### Option B — TLS with a CA / PKI (certificates signed by a trusted CA)

- Correctness: **5**.
- Concurrency-safety: **5**.
- Testability: **3** — needs a test CA and cert provisioning fixtures.
- Cross-platform: **5** — `crypto/tls` is portable.
- **Disqualifier (scope):** requires a certificate authority and an enrolment
  story. There is no server to run a CA and no operator to issue certs; forcing
  PKI onto two laptops on a LAN is the wrong shape for a serverless tool. Rejected.

### Option C — TLS 1.3, self-signed per-device certs, device ID = hash of cert, trust-on-first-use (PROPOSED, Syncthing model)

Each instance generates a keypair + self-signed certificate on first run. The
**device ID is the SHA-256 fingerprint of the certificate**, shown to the user
in a human-friendly encoding. To pair two devices you exchange device IDs
out-of-band (read them to each other / copy-paste) and add each to the other's
allow-list. On connect: TLS 1.3 handshake, both present certs, each side
computes the peer's device ID from the presented cert and **drops the connection
if it is not on the allow-list**.

This is exactly Syncthing's model: "At first startup, Syncthing creates a
public/private keypair ... and a self-signed certificate ... The fingerprint is
computed as the SHA-256 hash of the certificate ... called Device ID ... A TCP
connection is established and a TLS handshake performed. As part of the handshake
both devices present their certificates ... the remote device ID is calculated
from the received certificate ... If it is not a device ID we are expecting to
talk to, the connection is dropped"
([Syncthing Security Principles](https://docs.syncthing.net/users/security.html); [Understanding Device IDs](https://docs.syncthing.net/dev/device-ids.html), both accessed 2026-06-28).

- Correctness: **5** — TLS 1.3 gives confidentiality + integrity; cert pinning
  by SHA-256 gives strong peer authentication after pairing.
- Concurrency-safety: **5** — `crypto/tls` wraps the `net.Conn`; the framing and
  goroutine model above are unchanged.
- Testability: **5** — generate two in-memory self-signed certs in a test, wire
  two `tls.Conn`s over a `net.Pipe`/loopback, assert that a wrong fingerprint is
  rejected. The whole two-instance integration harness runs on one Mac.
- Cross-platform: **5** — `crypto/tls` + `crypto/x509` behave identically on Mac
  and Windows; device IDs are just bytes.

### Option D — pre-shared key / symmetric (shared passphrase → Noise/AEAD)

- Correctness: **4** — confidential + authenticated if the PSK is strong.
- Concurrency-safety: **5**.
- Testability: **4**.
- Cross-platform: **5**.
- **Cost:** worse identity model (one shared secret for the whole cluster; no
  per-device identity, so you cannot revoke one device or tell peers apart), and
  pulls in a non-stdlib Noise implementation. TLS+TOFU gives per-device identity
  for free from the stdlib. Not chosen, but kept as a fallback if a future
  feature needs a low-overhead path.

## Decision

Adopt **Option C**: **TLS 1.3, self-signed per-device certificates, device ID =
SHA-256 fingerprint of the certificate, trust-on-first-use with an explicit
allow-list and connection drop on unknown device ID.** Use the Go standard
library `crypto/tls` + `crypto/x509` only.

- `tls.Config{MinVersion: tls.VersionTLS13}`.
- Both `InsecureSkipVerify` *and* a custom `VerifyPeerCertificate` /
  `VerifyConnection` callback: we deliberately skip the **CA chain** check (there
  is no CA — self-signed is expected) but we **must** still verify the pinned
  fingerprint against the allow-list inside the callback. Skipping CA verification
  without the fingerprint check would be Option A in disguise.
- Device IDs are stored canonically and compared as raw 32-byte SHA-256 values;
  the human-readable encoding is presentation-only.

## Rationale

- It is the only option that satisfies confidentiality + integrity +
  per-device authentication **without** a central server or CA — exactly the
  project's constraint set.
- It reuses a proven, audited design (Syncthing) and the Go standard library,
  minimising novel crypto (we write zero crypto primitives; we only pin a hash).
- It is fully testable on a single Mac, which matters because cross-OS hardware
  is the one thing this project cannot assume (see plan/README.md "the one thing
  that is NOT fully autonomous").

## Consequences

- **First-connection weakness (honest disclosure).** TOFU's "weakness is the
  first connection, its strength is that every connection after that is
  cryptographically verified ... susceptible to man-in-the-middle attacks on
  first contact" ([Trust on first use, Wikipedia](https://en.wikipedia.org/wiki/Trust_on_first_use), accessed 2026-06-28; see also
  [agwa, *Why TOFU doesn't work*](https://www.agwa.name/blog/post/why_tofu_doesnt_work), accessed 2026-06-28). We mitigate by NOT auto-trusting:
  device IDs are exchanged **out-of-band** and added to an allow-list before the
  first sync, so an attacker would have to MitM *and* know/inject the exact
  expected device ID. This is a paired-allow-list TOFU, stronger than blind
  SSH-style "accept on first sight." A future enhancement (deferred) could add a
  short-authentication-string verification.
- Discovery (UDP multicast) is unauthenticated by nature — it only *advertises*
  candidate peers; authentication happens at the TLS layer, so a spoofed
  discovery packet at worst points us at an address whose TLS identity then fails
  the allow-list. Discovery must therefore be treated as a hint, never as
  authorisation. (Logged again in discovery design, Phase 2/WS-3.)
- Certs/keys persist per device under the config dir; key generation is a
  one-time first-run step. Loss of the key = new device ID = re-pair (acceptable
  for a 2-device LAN tool).
- Cross-references go-rules.md (stdlib-only, `crypto/tls` config) and the
  framing decision (TLS wraps the `net.Conn`; framing runs *inside* the TLS
  session, so the length-prefix bytes are themselves encrypted).
