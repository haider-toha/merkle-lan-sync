package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/haider-toha/merkle-sync/internal/protocol"
)

const (
	certFileName = "device.crt"
	keyFileName  = "device.key"

	// certValidity is the self-signed cert lifetime. Identity is pinned by
	// DeviceID = SHA-256(cert DER), not by chain validity: InsecureSkipVerify
	// skips the chain+expiry check and VerifyConnection is the gate (see tls.go),
	// so expiry never gates the handshake. The generous window only avoids a
	// confusing "expired" cert in external tooling.
	certValidity = 100 * 365 * 24 * time.Hour
)

// Identity is this device's cryptographic identity: a self-signed TLS certificate
// (with its private key) and the DeviceID derived from it as SHA-256(leaf DER)
// (PR-7 §3). The zero value is unusable; construct via GenerateIdentity or
// LoadOrCreateIdentity.
type Identity struct {
	// Certificate is the device's keypair + self-signed leaf, ready to drop into
	// tls.Config.Certificates. Leaf is always populated.
	Certificate tls.Certificate
	// DeviceID is SHA-256(Certificate.Certificate[0]); the stable peer identity.
	DeviceID protocol.DeviceID
}

// GenerateIdentity mints a fresh ECDSA P-256 key + self-signed certificate and
// derives the DeviceID. It does no I/O, so callers (first run, tests) can mint
// distinct identities cheaply; two calls yield distinct DER and therefore
// distinct DeviceIDs.
func GenerateIdentity() (*Identity, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("transport: generate key: %w", err)
	}
	// Random 128-bit serial (best practice; also guarantees DER uniqueness).
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("transport: generate serial: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "merkle-sync"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("transport: create certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("transport: parse certificate: %w", err)
	}
	return &Identity{
		Certificate: tls.Certificate{
			Certificate: [][]byte{der},
			PrivateKey:  priv,
			Leaf:        leaf,
		},
		DeviceID: protocol.DeviceIDFromCert(der),
	}, nil
}

// LoadOrCreateIdentity loads the device identity from dir, or mints and persists a
// new one on first run. It is fail-closed: if exactly one of the cert/key files
// exists (or a read fails for a reason other than "not exist"), it returns an
// error rather than silently regenerating — a silent regen would rotate the
// DeviceID and force a re-pair.
func LoadOrCreateIdentity(dir string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("transport: create config dir %q: %w", dir, err)
	}
	certPath := filepath.Join(dir, certFileName)
	keyPath := filepath.Join(dir, keyFileName)

	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		return identityFromPEM(certPEM, keyPEM)
	case errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist):
		id, err := GenerateIdentity()
		if err != nil {
			return nil, err
		}
		if err := persistIdentity(id, certPath, keyPath); err != nil {
			return nil, err
		}
		return id, nil
	default:
		return nil, fmt.Errorf("transport: inconsistent identity in %q (cert: %v, key: %v); refusing to regenerate", dir, certErr, keyErr)
	}
}

func identityFromPEM(certPEM, keyPEM []byte) (*Identity, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("transport: load keypair: %w", err)
	}
	if len(cert.Certificate) == 0 {
		return nil, errors.New("transport: certificate PEM contained no certificate")
	}
	// Populate Leaf explicitly so callers never depend on Go-version-specific
	// auto-population, and so DeviceID is well-defined.
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("transport: parse leaf: %w", err)
	}
	cert.Leaf = leaf
	return &Identity{
		Certificate: cert,
		DeviceID:    protocol.DeviceIDFromCert(cert.Certificate[0]),
	}, nil
}

func persistIdentity(id *Identity, certPath, keyPath string) error {
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.Certificate.Certificate[0]})
	if certPEM == nil {
		return errors.New("transport: encode certificate PEM")
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(id.Certificate.PrivateKey)
	if err != nil {
		return fmt.Errorf("transport: marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if keyPEM == nil {
		return errors.New("transport: encode key PEM")
	}
	// The secret key is 0600 and written atomically (temp -> fsync -> rename) so a
	// crash never leaves a half-written key (SR-1 spirit). The public cert is 0644.
	if err := writeFileAtomic(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("transport: write key: %w", err)
	}
	if err := writeFileAtomic(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("transport: write certificate: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Remove on any early-return path; a no-op once the rename has consumed it.
	defer os.Remove(tmpName)

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
