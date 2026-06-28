package protocol

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// DeviceID is the stable identity of a peer: the SHA-256 of its self-signed
// certificate in DER form (PR-7,
// docs/audit/decisions/protocol/transport-security-tofu-confirm.md). It is a
// value array so it is comparable (a map key, ==-comparable) and free of the
// slice-aliasing hazards a []byte identity would carry on a security-critical
// value. DeviceIDs are compared as raw bytes; the hex String form is
// presentation-only (N15).
type DeviceID [32]byte

// ShortID is the high 64 bits of a DeviceID, reused as the version-vector
// counter key so one cryptographic identity serves both authentication and
// causality and each VV counter stays 8 bytes (PR-7 §3, PR-2).
type ShortID uint64

// ErrInvalidDeviceID is returned by ParseDeviceID for a string that is not a
// 32-byte hex encoding.
var ErrInvalidDeviceID = errors.New("protocol: invalid device ID encoding")

// DeviceIDFromCert derives the DeviceID from a certificate's DER bytes. It is
// deterministic: the same DER always yields the same DeviceID, on Mac and
// Windows alike (crypto/sha256 is stdlib, GR-11).
func DeviceIDFromCert(der []byte) DeviceID {
	return sha256.Sum256(der)
}

// Short returns the high 64 bits of the DeviceID, big-endian, as the
// version-vector counter key. It is a pure function of the id with no cached
// state to keep consistent.
func (d DeviceID) Short() ShortID {
	return ShortID(binary.BigEndian.Uint64(d[:8]))
}

// String returns the lowercase hex encoding of the DeviceID (64 chars). This is
// a human/log presentation form only; identity is the raw bytes.
func (d DeviceID) String() string {
	return hex.EncodeToString(d[:])
}

// ParseDeviceID parses the hex encoding produced by String back into a DeviceID.
func ParseDeviceID(s string) (DeviceID, error) {
	var d DeviceID
	b, err := hex.DecodeString(s)
	if err != nil {
		return d, fmt.Errorf("%w: %v", ErrInvalidDeviceID, err)
	}
	if len(b) != len(d) {
		return d, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidDeviceID, len(b), len(d))
	}
	copy(d[:], b)
	return d, nil
}
