package discovery

import (
	"encoding/binary"
	"errors"

	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// Announcement wire format (decisions/ws3/announce-wire-format.md):
//
//	+----------+-----------+--------------+----------------+
//	| magic[4] | version:1 | DeviceID[32] | tcpPort:u16 BE |
//	| = "MSYN" |  = 0x01   |              |                |
//	+----------+-----------+--------------+----------------+
//
// Total 39 bytes. All integers big-endian (SKILL §5) so the bytes are identical on
// Mac and Windows. The DeviceID is an unauthenticated claim (SKILL §7); the dial
// target's IP is taken from the UDP source, only the TCP port travels in the
// payload. Hand-rolled binary, never gob/JSON from the network (GR-7).
const (
	announceVersion byte = 0x01
	deviceIDLen          = 32
	// announceSize is the exact encoded length; decode accepts >= this (trailing
	// bytes ignored) so a future same-version extension degrades gracefully.
	announceSize = 4 + 1 + deviceIDLen + 2 // 39
)

var announceMagic = [4]byte{'M', 'S', 'Y', 'N'}

// Decode sentinels (branchable with errors.Is in tests, GR-6). At runtime the
// reader treats any of these as "drop this datagram" — an open, unauthenticated UDP
// port must reject foreign/garbage/truncated input without panicking (GR-8).
var (
	ErrAnnounceTooShort = errors.New("discovery: announcement shorter than 39 bytes")
	ErrAnnounceMagic    = errors.New("discovery: announcement magic mismatch")
	ErrAnnounceVersion  = errors.New("discovery: unsupported announcement version")
	ErrAnnouncePort     = errors.New("discovery: announcement TCP port is zero")
)

// announcement is the decoded payload: a peer's claimed DeviceID and the TCP port
// it listens on for sync.
type announcement struct {
	DeviceID protocol.DeviceID
	TCPPort  uint16
}

// encodeAnnounce serialises a into the 39-byte datagram. It is a pure function with
// no error path (every field is fixed-width).
func encodeAnnounce(a announcement) []byte {
	b := make([]byte, announceSize)
	copy(b[0:4], announceMagic[:])
	b[4] = announceVersion
	copy(b[5:5+deviceIDLen], a.DeviceID[:])
	binary.BigEndian.PutUint16(b[5+deviceIDLen:announceSize], a.TCPPort)
	return b
}

// decodeAnnounce parses a received datagram. It is bounds-checked and total: any
// malformed/foreign/truncated/zero-port input yields a typed error and never
// panics. Bytes beyond the 39th are ignored (forward-compat within this version).
func decodeAnnounce(b []byte) (announcement, error) {
	var a announcement
	if len(b) < announceSize {
		return a, ErrAnnounceTooShort
	}
	if [4]byte{b[0], b[1], b[2], b[3]} != announceMagic {
		return a, ErrAnnounceMagic
	}
	if b[4] != announceVersion {
		return a, ErrAnnounceVersion
	}
	copy(a.DeviceID[:], b[5:5+deviceIDLen])
	a.TCPPort = binary.BigEndian.Uint16(b[5+deviceIDLen : announceSize])
	if a.TCPPort == 0 {
		return a, ErrAnnouncePort
	}
	return a, nil
}
