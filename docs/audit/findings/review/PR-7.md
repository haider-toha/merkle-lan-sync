# Review verdict — PR-7 (Transport identity: TLS 1.3 + trust-on-first-use device IDs)

- Reviewed: 2026-06-29 (Phase 6 reviewer)
- Finding: `docs/audit/findings/protocol/PR-7-transport-identity-tofu.md` (claimed `status: fixed`, WS-0 `801d094` + WS-2 `31ef9c6`)
- **Verdict: FIXED**

## What was claimed
Peers authenticate with no server/CA via TLS 1.3 + self-signed per-device certs, where
`DeviceID = SHA-256(cert DER)` and trust is TOFU against an explicit allow-list: a custom
`VerifyConnection` drops the connection before any frame is read unless the pinned DeviceID
is allow-listed; HELLO re-asserts the DeviceID in-band; discovery is a hint, never auth.

## Evidence verified against code
- `DeviceID = SHA-256(cert DER)`: `internal/protocol/deviceid.go` `DeviceIDFromCert`
  (used at `internal/transport/identity.go:83,135`).
- TLS 1.3 only + pinning replaces the absent CA chain: `internal/transport/tls.go:89-97`
  `baseTLSConfig` (`MinVersion==MaxVersion==VersionTLS13`, `InsecureSkipVerify:true` with
  the comment "CA chain replaced by VerifyConnection pinning, NOT auth disabled",
  `VerifyConnection: pinVerifier(allow)`); `:72-83` `pinVerifier` computes
  `SHA-256(PeerCertificates[0].Raw)` and returns `ErrUntrustedDevice` unless allow-listed;
  `:103-107` server `ClientAuth: RequireAnyClientCert` so the dialer's cert is pinned too.
- Concurrency-safe allow-list: `tls.go:29-64` `Allowlist` (RWMutex).
- HELLO re-asserts identity in-band, dropped on mismatch: established in the transport
  (`TestHELLO_DeviceIDMismatchDropped` exercises it end-to-end); engine HELLO fields ride
  out via `WithHello` with the transport overriding DeviceID.
- Stable identity persisted atomically (0600 key): `identity.go:139-189`
  (`persistIdentity` + `writeFileAtomic`), fail-closed on an inconsistent dir (`:92-116`).

## Evidence verified against tests (all PASS under `-race`)
- `internal/transport/transport_test.go:272` `TestPinVerifier` — allow-listed passes,
  unknown ⇒ `ErrUntrustedDevice`, no cert ⇒ `ErrNoPeerCert`.
- `:322` `TestTLS_WrongFingerprintRejected` — 3 cases (server-rejects / client-rejects /
  mutual): `Dial` fails AND no `PeerConnected` event is emitted (dropped before any frame).
- `:368` `TestHELLO_DeviceIDMismatchDropped` — TLS pins idB but HELLO claims idWrong ⇒ no
  `PeerConnected` and the connection is closed.
- `:305` `TestTLS_PinsIdentity`; `:407` `TestHello_CarriesEngineFields` (transport
  overrides a wrong provider DeviceID with its own); `:213/239` `TestAllowlist_*`;
  `:132/149/178` identity persist/reload/inconsistent.
- `internal/protocol/deviceid_test.go` `TestDeviceIDFromCert_Deterministic`,
  `TestShort_HighBits`, `TestShort_IsVersionVectorKey`, `TestDeviceID_HexRoundTrip`,
  `TestDeviceID_Comparable` (PR-7 §7 obligation #4) — all PASS.

## Run-log corroboration
- `docs/audit/runs/race-all.log:10` `ok internal/transport`;
  `docs/audit/runs/two-process-demo.log:9-10,24,30` (two daemons mint distinct DeviceIDs,
  pair via allow-list, converge); fresh 2026-06-29 run all named tests `--- PASS`.

## Skeptical check
The crucial property — auth happens at TLS via the fingerprint, not the absent CA chain — is
proven by `TestTLS_WrongFingerprintRejected` asserting NO `PeerConnected` (the peer is
dropped in the handshake, before any application frame). The HELLO-mismatch test closes the
cert-swap defence-in-depth gap. `InsecureSkipVerify:true` is paired with a mandatory
`VerifyConnection`, so it is not "plaintext in disguise". No gap found.
