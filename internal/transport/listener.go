package transport

import (
	"crypto/tls"
	"fmt"
	"net"
)

// Listen opens a TCP listener and accepts inbound peers in the background. Each
// accepted socket is wrapped as a TLS server and run through establish (TLS pin +
// HELLO re-assert). Returns the bound address (useful with ":0"). The accept loop
// and per-accept handshake goroutines are owned by the transport WaitGroup and
// stop on Close / ctx cancellation.
func (t *Transport) Listen(network, addr string) (net.Addr, error) {
	t.mu.Lock()
	closing := t.closing
	t.mu.Unlock()
	if closing {
		return nil, ErrTransportClosed
	}

	ln, err := net.Listen(network, addr)
	if err != nil {
		return nil, fmt.Errorf("transport: listen %s %s: %w", network, addr, err)
	}

	// Register the listener under the lock; if a teardown raced in, close and bail
	// so the listener can never be leaked.
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		_ = ln.Close()
		return nil, ErrTransportClosed
	}
	t.listeners[ln] = struct{}{}
	t.wg.Add(1)
	t.mu.Unlock()

	go t.acceptLoop(ln)
	return ln.Addr(), nil
}

func (t *Transport) acceptLoop(ln net.Listener) {
	defer t.wg.Done()
	for {
		raw, err := ln.Accept()
		if err != nil {
			// On Close the listener is closed and Accept returns a permanent error;
			// exit the loop (the deferred wg.Done lets Close's Wait complete).
			return
		}
		tlsConn := tls.Server(raw, t.serverCfg)
		// The establish goroutine is added while acceptLoop (already counted) runs,
		// so every Add happens before this loop's wg.Done — no WaitGroup-reuse race.
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			// establish closes tlsConn itself on any error; nothing connected.
			_ = t.establish(tlsConn)
		}()
	}
}
