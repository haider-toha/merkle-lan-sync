package transport

import (
	"crypto/tls"
	"fmt"
	"net"
)

// Dial connects to a discovered peer at addr, runs the TLS pin + HELLO re-assert
// handshake, and on success registers the connection and emits PeerConnected. It
// returns the handshake error on failure (e.g. a wrong fingerprint surfaces as a
// failed TLS handshake; a HELLO mismatch as ErrHelloDeviceMismatch).
//
// Dial BLOCKS through the handshake and the PeerConnected emit. Per the package
// usage contract, call it from a goroutine, not from the Events() drain loop.
func (t *Transport) Dial(network, addr string) error {
	select {
	case <-t.closed:
		return ErrTransportClosed
	default:
	}

	var d net.Dialer
	raw, err := d.DialContext(t.ctx, network, addr)
	if err != nil {
		return fmt.Errorf("transport: dial %s %s: %w", network, addr, err)
	}
	tlsConn := tls.Client(raw, t.clientCfg)
	return t.establish(tlsConn)
}
