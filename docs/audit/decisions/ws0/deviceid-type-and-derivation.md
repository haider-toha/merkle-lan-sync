# Decision (WS-0): DeviceID type, derivation, and the ShortID VV key

- Area: ws0 / internal/protocol (deviceid.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-0 implementer
- Plan items discharged: WS-0 acceptance #6 (`DeviceIDFromCert` deterministic;
  `Short()` is the high-64-bit VV counter key).
- Reads-first: PR-7 §3 (`DeviceID = SHA-256(cert DER)`, `Short()` high 64 bits =
  VV key, human encoding presentation-only / N15),
  `decisions/protocol/transport-security-tofu-confirm.md`, GR-11 (stdlib-only).

## Context

`DeviceID = SHA-256(certificate DER)` (32 bytes). The high 64 bits, `Short()`, are
reused as the version-vector counter key (PR-2/leaf-shape: counters are keyed by an
8-byte id). DeviceIDs are compared as raw bytes; any human encoding is
presentation-only (no Luhn/base32 flourish — N15). Stdlib-only (GR-11): we compute
one hash, write zero crypto. The open choice is the Go *type* for the 32-byte id.

## Options (scored 1–5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — `type DeviceID [32]byte` value type (CHOSEN)
`DeviceIDFromCert(der []byte) DeviceID { return sha256.Sum256(der) }`;
`func (d DeviceID) Short() ShortID { return ShortID(binary.BigEndian.Uint64(d[:8])) }`;
`String()` = lowercase hex; `ParseDeviceID(string)` for round-trip.
- correctness **5** — fixed 32-byte array is exactly the SHA-256 output;
  `sha256.Sum256` returns `[32]byte` with zero copying; `Short()` reads the high 64
  bits big-endian (stable, deterministic).
- concurrency-safety **5** — a value array is immutable when passed by value and is
  `comparable`, so it is a safe `map[DeviceID]…` key and `==`-comparable with no
  aliasing (cf. the VV slice footgun this deliberately avoids).
- testability **5** — determinism is a trivial equality of two calls; `Short()` is a
  byte-slice assertion; hex `String()`↔`ParseDeviceID` round-trips.
- cross-platform **5** — raw bytes + big-endian; identical Mac/Windows; `crypto/sha256`
  behaves identically (GR-11).

### Option B — `type DeviceID string` (hex/base32 string)
- correctness **4**, concurrency **5** (strings immutable), testability **4**,
  cross-platform **5**.
- **Cost:** stores the *presentation* form as identity; every raw-byte compare
  (VerifyConnection pinning, `Short()`) must re-decode; invites encoding drift between
  peers. Identity should be the raw bytes, encoding a view. Rejected.

### Option C — `type DeviceID []byte` slice
- correctness **3** — not `comparable` (can't be a map key or `==`-compared; needs
  `bytes.Equal`), and a slice aliases its backing array (mutation/retention footgun on
  a security-critical value).
- concurrency **2**, testability **4**, cross-platform **5**. Rejected.

### Option D — `struct{ raw [32]byte; short uint64 }` (cache the short id)
- correctness **5**, concurrency **5**, testability **4**, cross-platform **5**.
- **Cost:** caching an 8-byte derivation of an in-struct array is premature; `Short()`
  is a single `binary.BigEndian.Uint64`. Adds a consistency invariant for no gain.
  Rejected.

## Decision

Adopt **Option A**: `type DeviceID [32]byte`; `type ShortID uint64`;
`DeviceIDFromCert(der) = sha256.Sum256(der)`; `Short() =
ShortID(binary.BigEndian.Uint64(d[:8]))`; `String()` lowercase hex (64 chars);
`ParseDeviceID(string) (DeviceID, error)`. Comparison is the array `==`/raw bytes;
the hex form is presentation-only.

## Rationale

- A fixed `[32]byte` is the natural, zero-copy result of `sha256.Sum256` and is the
  one representation that is simultaneously immutable-by-value, `comparable` (map key
  / `==`), and free of slice-aliasing hazards — the right shape for a value that
  gates all file data and is compared on every handshake.
- `Short()` as a pure big-endian read keeps "one cryptographic identity serves both
  auth and causality" (PR-7 §3) with no cached state to keep consistent.
- Hex presentation is the minimal N15-compliant human form; raw bytes remain the
  identity.

## Consequences

- `deviceid.go` + `deviceid_test.go` (`TestDeviceIDFromCert_Deterministic`,
  `TestShort_HighBits`, `TestDeviceID_HexRoundTrip`).
- `ShortID` is the `VersionVector` counter key (shared with versionvector.go); WS-2's
  `VerifyConnection` pins on `DeviceIDFromCert(PeerCertificates[0].Raw)` and compares
  raw bytes against the allow-list.
- Cross-refs PR-7, `transport-security-tofu-confirm.md`, GR-11, PR-2.
