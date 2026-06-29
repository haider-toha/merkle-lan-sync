package transport

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// ErrOutboundOverflow is the close cause recorded when Send sheds: a peer whose
// outbound buffer is full is dropped rather than blocking the caller's select loop
// (CDD-1 / GR-4 companion — "outbound never blocks the select loop,
// buffered-with-shed").
var ErrOutboundOverflow = errors.New("transport: outbound buffer full, peer dropped")

// Conn is one authenticated, framed peer connection over TLS 1.3. It owns two
// goroutines — a reader draining frames into the transport's Events channel and a
// writer draining the outbound channel onto the wire — both reaped on close
// (GR-3). Close is idempotent and owner-only via sync.Once (GR-13): the one close
// both unblocks the reader (tls.Conn.Close) and stops the writer (close(closed)).
type Conn struct {
	connID   uint64
	deviceID protocol.DeviceID
	hello    protocol.Hello
	tlsConn  *tls.Conn
	t        *Transport

	outbound chan protocol.Message

	// registered is closed by Transport.register AFTER it has emitted PeerConnected for
	// this conn. deliver blocks on it so NO inbound PeerMessage can reach the fan-in
	// events channel before PeerConnected does. Without this gate the reader (started in
	// newConn) races register's PeerConnected emit on the shared events channel; a peer's
	// first message (e.g. its one-shot INDEX) could arrive before the engine has
	// registered the peer, and handleMessage silently drops a message for an unknown peer
	// — permanently wedging convergence with no re-exchange (REV-FLAKE-1, the backpressure
	// residual; decisions/phase7/REV-FLAKE-1-torn-scan-size-hash.md).
	registered chan struct{}

	closeOnce sync.Once
	closeErr  error // set exactly once inside closeOnce; read after wg.Wait()
	closed    chan struct{}
	wg        sync.WaitGroup // reader + writer
}

// DeviceID is the TLS-pinned identity of the peer (equal to its re-asserted HELLO
// DeviceID).
func (c *Conn) DeviceID() protocol.DeviceID { return c.deviceID }

// Hello is the peer's validated handshake HELLO — rootHash/folderID/featureFlags
// the engine uses for the SR-5 root short-circuit and feature negotiation.
func (c *Conn) Hello() protocol.Hello { return c.hello }

// Done is closed when the connection has begun tearing down.
func (c *Conn) Done() <-chan struct{} { return c.closed }

// Send enqueues msg for the writer goroutine. It NEVER blocks: if the conn is
// closed it returns false; if the outbound buffer is full the peer is dropped
// (buffered-with-shed) and it returns false. This is what stops a slow or stuck
// peer from wedging the engine's select loop (GR-4 companion rule, CDD-1).
func (c *Conn) Send(msg protocol.Message) bool {
	select {
	case <-c.closed:
		return false
	case c.outbound <- msg:
		return true
	default:
		c.closeWith(ErrOutboundOverflow)
		return false
	}
}

// closeWith tears the connection down exactly once, recording the first cause. It
// closes the done channel (stops the writer, fails Send) and closes the tls.Conn
// (unblocks a reader parked in ReadFrame). Safe to call from any goroutine and any
// number of times.
func (c *Conn) closeWith(err error) {
	c.closeOnce.Do(func() {
		c.closeErr = err
		close(c.closed)
		_ = c.tlsConn.Close()
	})
}

func (t *Transport) newConn(tlsConn *tls.Conn, id protocol.DeviceID, hello protocol.Hello) *Conn {
	c := &Conn{
		connID:     t.nextConnID.Add(1),
		deviceID:   id,
		hello:      hello,
		tlsConn:    tlsConn,
		t:          t,
		outbound:   make(chan protocol.Message, t.outBuf),
		registered: make(chan struct{}),
		closed:     make(chan struct{}),
	}
	c.wg.Add(2)
	go c.readLoop()
	go c.writeLoop()
	return c
}

// readLoop reads frames until an error or a fatal/undecodable frame, delivering
// dispatchable messages to the transport's fan-in Events channel. Any exit closes
// the conn (which unblocks the writer); the supervisor then emits the disconnect.
func (c *Conn) readLoop() {
	defer c.wg.Done()
	defer c.closeWith(nil) // EOF / closed exit with no recorded cause

	for {
		typ, payload, err := protocol.ReadFrame(c.tlsConn)
		if err != nil {
			// A malformed length (ErrFrameTooLarge / ErrZeroLength) is validated
			// before any allocation and we abandon the whole stream — so it drops
			// THIS peer and can never desync another conn (SR-12). EOF / closed is
			// a normal disconnect and records no cause.
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				c.closeWith(fmt.Errorf("transport: read frame from %s: %w", c.deviceID, err))
			}
			return
		}
		switch typ.RecvAction() {
		case protocol.ActionFatal:
			c.closeWith(fmt.Errorf("transport: fatal frame type 0x%02x from %s", byte(typ), c.deviceID))
			return
		case protocol.ActionSkip:
			// 0x08+ unassigned: the length prefix already delimited it, discard and
			// continue (forward-compat).
			continue
		case protocol.ActionDispatch:
			msg, derr := protocol.DecodeMessage(typ, payload)
			if derr != nil {
				c.closeWith(fmt.Errorf("transport: decode %s from %s: %w", typ, c.deviceID, derr))
				return
			}
			if !c.deliver(msg) {
				return
			}
		}
	}
}

// deliver hands an inbound message to the engine via the fan-in Events channel,
// without deadlocking at shutdown. It first waits until this conn has been registered
// (PeerConnected emitted) so the engine cannot receive a PeerMessage for a peer it has
// not yet registered — which handleMessage would silently drop (REV-FLAKE-1). A conn
// closed before registration (transport shutting down) unblocks the reader via closed.
func (c *Conn) deliver(msg protocol.Message) bool {
	select {
	case <-c.registered:
	case <-c.closed:
		return false
	case <-c.t.closed:
		return false
	}
	select {
	case c.t.events <- Event{Kind: PeerMessage, DeviceID: c.deviceID, Conn: c, Message: msg}:
		return true
	case <-c.closed:
		return false
	case <-c.t.closed:
		return false
	}
}

// writeLoop drains the outbound channel onto the wire. It exits on close or
// transport-context cancellation; a write error drops the peer.
func (c *Conn) writeLoop() {
	defer c.wg.Done()
	defer c.closeWith(nil)

	for {
		select {
		case msg := <-c.outbound:
			if err := protocol.WriteMessage(c.tlsConn, msg); err != nil {
				c.closeWith(fmt.Errorf("transport: write %s to %s: %w", msg.Type(), c.deviceID, err))
				return
			}
		case <-c.closed:
			return
		case <-c.t.ctx.Done():
			return
		}
	}
}
