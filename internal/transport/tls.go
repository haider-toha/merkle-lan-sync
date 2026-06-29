package transport

import (
	"crypto/tls"
	"errors"
	"fmt"
	"sync"

	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// Pinning sentinels (branchable with errors.Is, GR-6). Both surface as a failed
// TLS handshake when returned from VerifyConnection — so the peer is dropped
// before any application frame is read (PR-7 §4.1).
var (
	// ErrUntrustedDevice is returned when the peer's pinned DeviceID is not on the
	// allow-list.
	ErrUntrustedDevice = errors.New("transport: peer DeviceID not on allow-list")
	// ErrNoPeerCert is returned when the peer presented no certificate, so no
	// DeviceID can be pinned. With ClientAuth=RequireAnyClientCert on the server
	// and a client cert always configured, this is defensive.
	ErrNoPeerCert = errors.New("transport: peer presented no certificate")
)

// Allowlist is the explicit, mutable set of peer DeviceIDs this device will talk
// to: the trust-on-first-use paired allow-list (PR-7 §5), populated out-of-band
// before first sync. It is safe for concurrent use, so pairing may Add a peer
// while the accept loop verifies another.
type Allowlist struct {
	mu  sync.RWMutex
	ids map[protocol.DeviceID]struct{}
}

// NewAllowlist returns an Allowlist seeded with ids.
func NewAllowlist(ids ...protocol.DeviceID) *Allowlist {
	a := &Allowlist{ids: make(map[protocol.DeviceID]struct{}, len(ids))}
	for _, id := range ids {
		a.ids[id] = struct{}{}
	}
	return a
}

// Add adds id to the allow-list (idempotent).
func (a *Allowlist) Add(id protocol.DeviceID) {
	a.mu.Lock()
	a.ids[id] = struct{}{}
	a.mu.Unlock()
}

// Remove removes id from the allow-list (idempotent). Existing connections are not
// torn down by Remove; it only affects future handshakes.
func (a *Allowlist) Remove(id protocol.DeviceID) {
	a.mu.Lock()
	delete(a.ids, id)
	a.mu.Unlock()
}

// Allowed reports whether id is currently on the allow-list.
func (a *Allowlist) Allowed(id protocol.DeviceID) bool {
	a.mu.RLock()
	_, ok := a.ids[id]
	a.mu.RUnlock()
	return ok
}

// pinVerifier returns the VerifyConnection callback that pins the peer's DeviceID.
// crypto/tls runs VerifyConnection for every handshake regardless of
// InsecureSkipVerify and ClientAuth; a non-nil return aborts the handshake. So
// this callback — not the absent CA chain — is the authentication gate (PR-7
// §4.1). It computes SHA-256(PeerCertificates[0].Raw) and rejects unless that
// DeviceID is allow-listed.
func pinVerifier(allow *Allowlist) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return ErrNoPeerCert
		}
		id := protocol.DeviceIDFromCert(cs.PeerCertificates[0].Raw)
		if !allow.Allowed(id) {
			return fmt.Errorf("%w: %s", ErrUntrustedDevice, id)
		}
		return nil
	}
}

// baseTLSConfig is the shared TLS 1.3 config: present our cert, pin to TLS 1.3
// exactly, skip the (nonexistent) CA chain, and replace it with DeviceID pinning
// in VerifyConnection. Skipping the chain WITHOUT the pin would be plaintext in
// disguise, so VerifyConnection is mandatory (PR-7 §4.1, decision Option C).
func baseTLSConfig(id *Identity, allow *Allowlist) *tls.Config {
	return &tls.Config{
		Certificates:       []tls.Certificate{id.Certificate},
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // CA chain replaced by VerifyConnection pinning, NOT auth disabled
		VerifyConnection:   pinVerifier(allow),
	}
}

// serverTLSConfig additionally requires the dialer to present a certificate
// (RequireAnyClientCert: required but not chain-verified) so VerifyConnection can
// pin it; without it the server would receive no PeerCertificates and could not
// authenticate the client.
func serverTLSConfig(id *Identity, allow *Allowlist) *tls.Config {
	cfg := baseTLSConfig(id, allow)
	cfg.ClientAuth = tls.RequireAnyClientCert
	return cfg
}

func clientTLSConfig(id *Identity, allow *Allowlist) *tls.Config {
	return baseTLSConfig(id, allow)
}
