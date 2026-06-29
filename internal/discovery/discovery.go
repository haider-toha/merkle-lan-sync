package discovery

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/haider-toha/merkle-sync/internal/protocol"
)

const (
	// DefaultGroup is the IPv4 multicast group:port discovery announces on. It lies
	// in the administratively-scoped / organisation-local range (RFC 2365, 239.192/14).
	DefaultGroup = "239.192.0.77:21027"

	defaultAnnounceInterval = 10 * time.Second
	defaultEvictTimeout     = 30 * time.Second // ~3 missed announces (heartbeat miss-count)
	defaultSweepInterval    = 10 * time.Second
	defaultEventsBuffer     = 32
	inboundBuffer           = 64
	maxDatagram             = 512 // an announcement is 39 bytes; larger reads are foreign and dropped
)

// defaultGroup is the parsed DefaultGroup; MustParse is safe for a constant we own.
var defaultGroup = netip.MustParseAddrPort(DefaultGroup)

// clock is the time seam (now()). Production is realClock; tests inject a manual
// clock so eviction is provable without a naked time.Sleep (GR-13).
type clock interface{ now() time.Time }

type realClock struct{}

func (realClock) now() time.Time { return time.Now() }

// Discovery announces this device and surfaces discovered/evicted peers as dial
// hints on Events(). Construct with New; tear down with Close (or by cancelling the
// context passed to New). It imports only internal/protocol — never transport.
type Discovery struct {
	self    protocol.DeviceID
	tcpPort uint16
	group   netip.AddrPort
	iface   *net.Interface
	conn    datagramConn // production socket, or an injected bus in tests

	announceInterval time.Duration
	evictTimeout     time.Duration
	sweepInterval    time.Duration
	eventsBuf        int

	clk        clock
	evictTickC <-chan time.Time // injected in tests; nil => a real ticker

	ctx    context.Context
	cancel context.CancelFunc

	events  chan Event
	inbound chan inAnnounce
	closed  chan struct{}

	closeOnce sync.Once
	wg        sync.WaitGroup
}

// Option configures a Discovery at construction.
type Option func(*Discovery)

// WithGroup overrides the multicast group:port (must be a valid IPv4 group).
func WithGroup(g netip.AddrPort) Option {
	return func(d *Discovery) {
		if g.IsValid() {
			d.group = g
		}
	}
}

// WithInterface pins the multicast interface (default: the first UP,
// multicast-capable, non-loopback IPv4 interface; nil lets the OS choose).
func WithInterface(ifi *net.Interface) Option { return func(d *Discovery) { d.iface = ifi } }

// WithAnnounceInterval sets how often this device announces (default 10s).
func WithAnnounceInterval(dur time.Duration) Option {
	return func(d *Discovery) {
		if dur > 0 {
			d.announceInterval = dur
		}
	}
}

// WithEvictTimeout sets how long a peer may be silent before eviction (default 30s).
func WithEvictTimeout(dur time.Duration) Option {
	return func(d *Discovery) {
		if dur > 0 {
			d.evictTimeout = dur
		}
	}
}

// WithSweepInterval sets the eviction sweep period (default 10s).
func WithSweepInterval(dur time.Duration) Option {
	return func(d *Discovery) {
		if dur > 0 {
			d.sweepInterval = dur
		}
	}
}

// WithEventsBuffer sets the Events channel buffer depth (default 32).
func WithEventsBuffer(n int) Option {
	return func(d *Discovery) {
		if n >= 0 {
			d.eventsBuf = n
		}
	}
}

// --- unexported test seams (decisions/ws3/registry-actor-and-clock-injection.md) ---

func withConn(c datagramConn) Option           { return func(d *Discovery) { d.conn = c } }
func withClock(c clock) Option                 { return func(d *Discovery) { d.clk = c } }
func withEvictTick(ch <-chan time.Time) Option { return func(d *Discovery) { d.evictTickC = ch } }

// New builds a Discovery bound to ctx (GR-2), opens its socket, and starts its
// goroutines (reader, announcer, registry actor, ctx watcher). tcpPort is the TCP
// port this device listens on for sync; it is announced so peers know where to
// dial. Cancelling ctx tears the Discovery down; so does Close.
func New(ctx context.Context, self protocol.DeviceID, tcpPort uint16, opts ...Option) (*Discovery, error) {
	cctx, cancel := context.WithCancel(ctx)
	d := &Discovery{
		self:             self,
		tcpPort:          tcpPort,
		group:            defaultGroup,
		announceInterval: defaultAnnounceInterval,
		evictTimeout:     defaultEvictTimeout,
		sweepInterval:    defaultSweepInterval,
		eventsBuf:        defaultEventsBuffer,
		clk:              realClock{},
		ctx:              cctx,
		cancel:           cancel,
		closed:           make(chan struct{}),
	}
	for _, o := range opts {
		o(d)
	}
	d.events = make(chan Event, d.eventsBuf)
	d.inbound = make(chan inAnnounce, inboundBuffer)

	// Open the socket (real, or an injected bus for tests). On failure, undo the
	// context so the caller leaks nothing.
	if d.conn == nil {
		conn, err := newUDPMulticast(d.group, d.resolveInterface())
		if err != nil {
			cancel()
			return nil, err
		}
		d.conn = conn
	}

	// Eviction tick source: injected (tests) or a real ticker (production).
	evictTick := d.evictTickC
	var ticker *time.Ticker
	if evictTick == nil {
		ticker = time.NewTicker(d.sweepInterval)
		evictTick = ticker.C
	}

	r := &registry{
		peers:        make(map[protocol.DeviceID]peerEntry),
		evictTimeout: d.evictTimeout,
		inbound:      d.inbound,
		evictTick:    evictTick,
		stop:         cctx.Done(),
		emit:         d.emit,
	}

	// ctx watcher → idempotent teardown (mirrors transport: cancelling the parent
	// tears down without an explicit Close, funnelling through the same teardown).
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		<-d.ctx.Done()
		d.closeOnce.Do(d.teardown)
	}()

	d.wg.Add(1)
	go d.readLoop()

	d.wg.Add(1)
	go d.announceLoop()

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		if ticker != nil {
			defer ticker.Stop()
		}
		r.run()
	}()

	return d, nil
}

// Events is the fan-in stream of peer hints. Per GR-13 it is never closed; drain it
// in a dedicated goroutine. Shutdown is ctx + WaitGroup.
func (d *Discovery) Events() <-chan Event { return d.events }

// Group returns the multicast group:port in use (for logging).
func (d *Discovery) Group() netip.AddrPort { return d.group }

// Close tears down the socket and all goroutines and waits for them to exit (GR-3).
// Idempotent.
func (d *Discovery) Close() error {
	d.closeOnce.Do(d.teardown)
	d.wg.Wait()
	return nil
}

func (d *Discovery) teardown() {
	close(d.closed)    // unblocks emit
	d.cancel()         // stops the actor + announcer (ctx.Done)
	_ = d.conn.Close() // unblocks the reader parked in ReadDatagram (GR-4)
}

func (d *Discovery) resolveInterface() *net.Interface {
	if d.iface != nil {
		return d.iface
	}
	return defaultMulticastInterface()
}

// emit sends an event without blocking past shutdown (the transport emit pattern).
func (d *Discovery) emit(ev Event) {
	select {
	case d.events <- ev:
	case <-d.closed:
	}
}

// readLoop drains datagrams, decodes + self-filters them, and forwards decoded
// announcements (stamped with the receive time) to the actor. Malformed/foreign
// datagrams are dropped (an unauthenticated UDP port must never panic — GR-8); a
// socket error (e.g. Close on teardown) ends the loop.
func (d *Discovery) readLoop() {
	defer d.wg.Done()
	buf := make([]byte, maxDatagram)
	for {
		n, src, err := d.conn.ReadDatagram(buf)
		if err != nil {
			return // closed on teardown, or a broken socket: stop hearing peers
		}
		ann, derr := decodeAnnounce(buf[:n])
		if derr != nil {
			continue // foreign / garbage / truncated / bad-version / zero-port
		}
		if ann.DeviceID == d.self {
			continue // drop our own loopback echo (a filter, not authorisation)
		}
		addr := netip.AddrPortFrom(src.Addr().Unmap(), ann.TCPPort)
		in := inAnnounce{deviceID: ann.DeviceID, addr: addr, at: d.clk.now()}
		select {
		case d.inbound <- in:
		case <-d.ctx.Done():
			return
		}
	}
}

// announceLoop sends this device's announcement immediately (so a new peer is seen
// well within one interval) and then every announce interval. Write errors are
// ignored — a failed announce is a missed hint, not a correctness bug.
func (d *Discovery) announceLoop() {
	defer d.wg.Done()
	pkt := encodeAnnounce(announcement{DeviceID: d.self, TCPPort: d.tcpPort})
	_ = d.conn.WriteDatagram(pkt)

	ticker := time.NewTicker(d.announceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = d.conn.WriteDatagram(pkt)
		case <-d.ctx.Done():
			return
		}
	}
}
