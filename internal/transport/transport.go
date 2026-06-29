package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// ProtoVersion is the wire protocol version advertised in the default HELLO.
const ProtoVersion uint16 = 1

const (
	defaultOutboundBuffer   = 64
	defaultEventsBuffer     = 64
	defaultHandshakeTimeout = 30 * time.Second
)

// Lifecycle / handshake sentinels (branchable with errors.Is, GR-6).
var (
	// ErrTransportClosed is returned by Dial/Listen after the transport has begun
	// shutting down, and is the close cause stamped on conns torn down by Close.
	ErrTransportClosed = errors.New("transport: closed")
	// ErrHelloDeviceMismatch is returned when the peer's in-band HELLO DeviceID
	// does not match the TLS-pinned identity (PR-7 §4.2 defence in depth).
	ErrHelloDeviceMismatch = errors.New("transport: HELLO DeviceID does not match TLS-pinned identity")
	// ErrHelloExpected is returned when the first post-TLS frame is not a HELLO.
	ErrHelloExpected = errors.New("transport: first frame was not HELLO")
)

// EventKind classifies a transport Event.
type EventKind int

const (
	// PeerConnected: a peer finished the TLS+HELLO handshake. Conn is its handle.
	PeerConnected EventKind = iota
	// PeerDisconnected: a peer's connection was torn down. Err is the cause (nil
	// for a clean EOF).
	PeerDisconnected
	// PeerMessage: an inbound dispatchable message. Conn and Message are set.
	PeerMessage
)

func (k EventKind) String() string {
	switch k {
	case PeerConnected:
		return "PeerConnected"
	case PeerDisconnected:
		return "PeerDisconnected"
	case PeerMessage:
		return "PeerMessage"
	default:
		return fmt.Sprintf("EventKind(%d)", int(k))
	}
}

// Event is the single fan-in stream the engine consumes (GR-4 "share by
// communicating"). Per GR-13 the Events channel is never closed; shutdown is ctx +
// WaitGroup. Conn is set on PeerConnected and PeerMessage; Message on PeerMessage;
// Err (the drop cause, possibly nil) on PeerDisconnected.
type Event struct {
	Kind     EventKind
	DeviceID protocol.DeviceID
	Conn     *Conn
	Message  protocol.Message
	Err      error
}

// Transport accepts and dials authenticated TLS 1.3 connections and surfaces all
// peer lifecycle + inbound messages on a single Events channel. Construct with New
// and tear down with Close (or by cancelling the context passed to New).
type Transport struct {
	identity  *Identity
	allow     *Allowlist
	serverCfg *tls.Config
	clientCfg *tls.Config
	helloFn   func() protocol.Hello

	outBuf           int
	handshakeTimeout time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	events chan Event
	closed chan struct{}

	nextConnID atomic.Uint64

	mu        sync.Mutex
	closing   bool
	conns     map[uint64]*Conn
	listeners map[net.Listener]struct{}

	closeOnce sync.Once
	wg        sync.WaitGroup
}

// Option configures a Transport at construction.
type Option func(*Transport)

// WithHello sets the provider for the outbound HELLO (the engine supplies the
// current rootHash/folderID/featureFlags; the transport always overrides DeviceID
// with its own identity).
func WithHello(fn func() protocol.Hello) Option {
	return func(t *Transport) {
		if fn != nil {
			t.helloFn = fn
		}
	}
}

// WithOutboundBuffer sets the per-conn outbound buffer depth (buffered-with-shed).
func WithOutboundBuffer(n int) Option {
	return func(t *Transport) {
		if n >= 0 {
			t.outBuf = n
		}
	}
}

// WithHandshakeTimeout bounds the TLS handshake + HELLO exchange.
func WithHandshakeTimeout(d time.Duration) Option {
	return func(t *Transport) {
		if d > 0 {
			t.handshakeTimeout = d
		}
	}
}

// New builds a Transport bound to ctx (GR-2). Cancelling ctx tears the transport
// down; so does Close.
func New(ctx context.Context, id *Identity, allow *Allowlist, opts ...Option) *Transport {
	cctx, cancel := context.WithCancel(ctx)
	t := &Transport{
		identity:         id,
		allow:            allow,
		serverCfg:        serverTLSConfig(id, allow),
		clientCfg:        clientTLSConfig(id, allow),
		helloFn:          func() protocol.Hello { return protocol.Hello{ProtoVersion: ProtoVersion} },
		outBuf:           defaultOutboundBuffer,
		handshakeTimeout: defaultHandshakeTimeout,
		ctx:              cctx,
		cancel:           cancel,
		events:           make(chan Event, defaultEventsBuffer),
		closed:           make(chan struct{}),
		conns:            make(map[uint64]*Conn),
		listeners:        make(map[net.Listener]struct{}),
	}
	for _, o := range opts {
		o(t)
	}
	// Watch ctx so cancelling the parent tears the transport down without an
	// explicit Close (GR-2). Funnels through the same idempotent teardown.
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		<-t.ctx.Done()
		t.closeOnce.Do(t.teardown)
	}()
	return t
}

// Identity returns this device's identity.
func (t *Transport) Identity() *Identity { return t.identity }

// Allowlist returns the live allow-list (mutable for out-of-band pairing).
func (t *Transport) Allowlist() *Allowlist { return t.allow }

// Events is the fan-in channel of peer lifecycle + inbound messages. It is never
// closed (GR-13); drain it in a dedicated goroutine and do NOT call Dial from that
// goroutine (see package doc).
func (t *Transport) Events() <-chan Event { return t.events }

// Close tears down every listener and connection and waits for all goroutines to
// exit (GR-3). Idempotent.
func (t *Transport) Close() error {
	t.closeOnce.Do(t.teardown)
	t.wg.Wait()
	return nil
}

func (t *Transport) teardown() {
	t.mu.Lock()
	t.closing = true
	conns := make([]*Conn, 0, len(t.conns))
	for _, c := range t.conns {
		conns = append(conns, c)
	}
	lns := make([]net.Listener, 0, len(t.listeners))
	for ln := range t.listeners {
		lns = append(lns, ln)
	}
	t.mu.Unlock()

	close(t.closed) // unblocks emit; signals deliver
	t.cancel()      // unblocks writeLoop's ctx arm; fails in-flight dials/handshakes
	for _, ln := range lns {
		_ = ln.Close() // unblocks Accept
	}
	for _, c := range conns {
		c.closeWith(ErrTransportClosed) // unblocks reader/writer
	}
}

// emit sends an event without blocking past shutdown.
func (t *Transport) emit(ev Event) {
	select {
	case t.events <- ev:
	case <-t.closed:
	}
}

// register records an established conn, spawns its supervisor, and emits
// PeerConnected. It rejects (and closes) the conn if the transport is shutting
// down — together with the ctx-cancel of in-flight handshakes this guarantees no
// wg.Add happens after teardown begins (no WaitGroup-reuse race).
func (t *Transport) register(c *Conn) bool {
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		c.closeWith(ErrTransportClosed)
		return false
	}
	t.conns[c.connID] = c
	t.wg.Add(1)
	t.mu.Unlock()

	go t.superviseConn(c)
	t.emit(Event{Kind: PeerConnected, DeviceID: c.deviceID, Conn: c})
	// Open the delivery gate ONLY now: PeerConnected is enqueued on the fan-in events
	// channel, so every subsequent PeerMessage this conn's reader delivers is ordered
	// strictly after it (FIFO) — the engine always registers the peer before receiving
	// any of its messages, so the one-shot INDEX can never be dropped as "unknown peer"
	// (REV-FLAKE-1 backpressure wedge).
	close(c.registered)
	return true
}

func (t *Transport) deregister(connID uint64) {
	t.mu.Lock()
	delete(t.conns, connID)
	t.mu.Unlock()
}

// superviseConn waits for a conn's reader+writer to exit, then emits the
// disconnect (immediately, not at discovery-eviction time — CDD-1.3) and reclaims
// per-peer state.
func (t *Transport) superviseConn(c *Conn) {
	defer t.wg.Done()
	c.wg.Wait() // reader + writer exited; closeErr is now safely readable
	t.deregister(c.connID)
	t.emit(Event{Kind: PeerDisconnected, DeviceID: c.deviceID, Err: c.closeErr})
}

// establish runs the post-socket handshake shared by Dial and the accept loop: TLS
// 1.3 (VerifyConnection pins the DeviceID before any frame is read) then the
// in-band HELLO exchange that re-asserts the DeviceID (PR-1 §5, PR-7 §4). On
// success it spawns the steady-state Conn and emits PeerConnected; on any error it
// closes tlsConn and returns the error.
func (t *Transport) establish(tlsConn *tls.Conn) error {
	// Bound the handshake + HELLO so a stuck peer parks no goroutine (GR-3).
	_ = tlsConn.SetDeadline(time.Now().Add(t.handshakeTimeout))

	if err := tlsConn.HandshakeContext(t.ctx); err != nil {
		_ = tlsConn.Close()
		return fmt.Errorf("transport: TLS handshake: %w", err)
	}
	cs := tlsConn.ConnectionState()
	if len(cs.PeerCertificates) == 0 {
		_ = tlsConn.Close()
		return ErrNoPeerCert
	}
	pinned := protocol.DeviceIDFromCert(cs.PeerCertificates[0].Raw)

	// HELLO exchange (PR-1 §5 step 2). Write-then-read is deadlock-free: a HELLO is
	// tiny (well under the socket/TLS buffer), and the handshake deadline bounds it.
	myHello := t.helloFn()
	myHello.DeviceID = t.identity.DeviceID
	if err := protocol.WriteMessage(tlsConn, myHello); err != nil {
		_ = tlsConn.Close()
		return fmt.Errorf("transport: write HELLO: %w", err)
	}
	peerHello, err := readHello(tlsConn)
	if err != nil {
		_ = tlsConn.Close()
		return fmt.Errorf("transport: read HELLO: %w", err)
	}
	if peerHello.DeviceID != pinned {
		_ = tlsConn.Close()
		return fmt.Errorf("%w: pinned=%s hello=%s", ErrHelloDeviceMismatch, pinned, peerHello.DeviceID)
	}

	// Steady state: clear the handshake deadline. Cancellation is now via Close,
	// which closes the conn and unblocks the reader (GR-4).
	_ = tlsConn.SetDeadline(time.Time{})

	c := t.newConn(tlsConn, pinned, peerHello)
	if !t.register(c) {
		return ErrTransportClosed
	}
	return nil
}

// readHello reads exactly one frame and requires it to be a HELLO.
func readHello(r io.Reader) (protocol.Hello, error) {
	typ, payload, err := protocol.ReadFrame(r)
	if err != nil {
		return protocol.Hello{}, err
	}
	if typ != protocol.MsgHello {
		return protocol.Hello{}, fmt.Errorf("%w: got %s", ErrHelloExpected, typ)
	}
	msg, err := protocol.DecodeMessage(typ, payload)
	if err != nil {
		return protocol.Hello{}, err
	}
	h, ok := msg.(protocol.Hello)
	if !ok {
		return protocol.Hello{}, ErrHelloExpected
	}
	return h, nil
}
