package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
)

// TestDeviceIDFromCert_Deterministic: the same DER always yields the same
// DeviceID; different DER yields a different DeviceID (PR-7 §3).
func TestDeviceIDFromCert_Deterministic(t *testing.T) {
	der := []byte("a self-signed certificate in DER form")
	a := DeviceIDFromCert(der)
	b := DeviceIDFromCert(der)
	if a != b {
		t.Fatalf("non-deterministic: %x != %x", a, b)
	}
	// It is exactly SHA-256(DER).
	want := sha256.Sum256(der)
	if a != DeviceID(want) {
		t.Fatalf("DeviceIDFromCert = %x, want SHA-256(der) = %x", a, want)
	}
	// A different cert ⇒ a different id.
	if c := DeviceIDFromCert([]byte("a different cert")); c == a {
		t.Fatalf("distinct certs produced the same DeviceID: %x", c)
	}
	// Determinism across many calls.
	for i := 0; i < 100; i++ {
		if DeviceIDFromCert(der) != a {
			t.Fatalf("call %d diverged", i)
		}
	}
}

// TestShort_HighBits: Short() is the high 64 bits of the DeviceID, big-endian —
// the version-vector counter key.
func TestShort_HighBits(t *testing.T) {
	var d DeviceID
	// Known first 8 bytes; the rest must not affect Short().
	copy(d[:8], []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08})
	for i := 8; i < len(d); i++ {
		d[i] = 0xFF
	}
	const want ShortID = 0x0102030405060708
	if got := d.Short(); got != want {
		t.Fatalf("Short() = %#x, want %#x", uint64(got), uint64(want))
	}

	// Cross-check against an independent big-endian decode of the derived id.
	der := []byte("cert-for-short-check")
	id := DeviceIDFromCert(der)
	if got, want := id.Short(), ShortID(binary.BigEndian.Uint64(id[:8])); got != want {
		t.Fatalf("Short() = %#x, want %#x", uint64(got), uint64(want))
	}
}

// TestShort_IsVersionVectorKey: the Short id is usable directly as a VV counter
// key (the one-identity-for-auth-and-causality property, PR-7 §3).
func TestShort_IsVersionVectorKey(t *testing.T) {
	id := DeviceIDFromCert([]byte("device-A")).Short()
	vv := VersionVector(nil).Bump(id)
	if vv.Get(id) != 1 {
		t.Fatalf("VV keyed by Short id: Get = %d, want 1", vv.Get(id))
	}
}

// TestDeviceID_HexRoundTrip: String() ⇄ ParseDeviceID is lossless; malformed
// input is a typed error.
func TestDeviceID_HexRoundTrip(t *testing.T) {
	id := DeviceIDFromCert([]byte("round-trip-cert"))
	s := id.String()
	if len(s) != 64 {
		t.Fatalf("hex String length = %d, want 64", len(s))
	}
	back, err := ParseDeviceID(s)
	if err != nil {
		t.Fatalf("ParseDeviceID: %v", err)
	}
	if back != id {
		t.Fatalf("round-trip mismatch: %x != %x", back, id)
	}

	for _, bad := range []string{
		"",           // empty
		"xyz",        // not hex
		s[:62],       // too short (31 bytes)
		s + "00",     // too long (33 bytes)
		"zz" + s[2:], // invalid hex digits
	} {
		if _, err := ParseDeviceID(bad); !errors.Is(err, ErrInvalidDeviceID) {
			t.Errorf("ParseDeviceID(%q) err = %v, want ErrInvalidDeviceID", bad, err)
		}
	}
}

// TestDeviceID_Comparable: DeviceID is a usable map key / ==-comparable value
// (the reason it is an array, not a slice).
func TestDeviceID_Comparable(t *testing.T) {
	m := map[DeviceID]string{}
	a := DeviceIDFromCert([]byte("A"))
	b := DeviceIDFromCert([]byte("B"))
	m[a] = "alice"
	m[b] = "bob"
	if m[a] != "alice" || m[b] != "bob" || len(m) != 2 {
		t.Fatalf("DeviceID map keying failed: %v", m)
	}
	a2 := DeviceIDFromCert([]byte("A"))
	if a != a2 || !bytes.Equal(a[:], a2[:]) {
		t.Fatalf("raw-byte compare failed")
	}
}
