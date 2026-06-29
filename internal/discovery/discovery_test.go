package discovery

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// NOTE ON HOSTILE INPUT. internal/discovery handles NO filesystem paths, so the
// Windows-hostile-path axis (illegal chars / reserved device names / NFD-NFC /
// case collisions / backslashes) does not apply to this package — those are
// exercised in internal/pathnorm and internal/merkle where paths actually live.
// The equivalent hostile-input surface here is the UNAUTHENTICATED UDP datagram:
// anyone on the multicast group can send any bytes. TestDecodeAnnounce covers that
// adversarial surface (truncation at every boundary, foreign/garbage bytes, a
// cross-plane protocol frame, bad magic/version, zero port) and asserts the decoder
// is total and never panics (GR-8, SKILL §7).

// ---------- helpers ----------

func devID(seed byte) protocol.DeviceID {
	var d protocol.DeviceID
	for i := range d {
		d[i] = seed + byte(i)
	}
	return d
}

func srcAddr(last byte, port uint16) netip.AddrPort {
	return netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, last}), port)
}

func repeatByte(n int, v byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}

func mutate(b []byte, i int, v byte) []byte {
	c := append([]byte(nil), b...)
	c[i] = v
	return c
}

// waitEvent reads events until one of the wanted kind+DeviceID arrives, discarding
// others. Fails the test on timeout. This is an event wait (not a sleep standing in
// for synchronisation — GR-13).
func waitEvent(t *testing.T, ch <-chan Event, kind EventKind, id protocol.DeviceID, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			if e.Kind == kind && e.DeviceID == id {
				return e
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %v for %s", kind, id)
		}
	}
}

// waitEventOrSkip is waitEvent that returns ok=false on timeout instead of failing,
// for best-effort real-socket tests that must skip (not fail) where the environment
// cannot deliver multicast (plan WS-3 risk).
func waitEventOrSkip(ch <-chan Event, kind EventKind, id protocol.DeviceID, timeout time.Duration) (Event, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			if e.Kind == kind && e.DeviceID == id {
				return e, true
			}
		case <-deadline:
			return Event{}, false
		}
	}
}

// --- an in-memory multicast bus implementing datagramConn (the deterministic test
// medium; decisions/ws3/registry-actor-and-clock-injection.md). WriteDatagram fans
// out to ALL members including the sender, mimicking IP_MULTICAST_LOOP so the
// self-filter is exercised in every bus test.

type bus struct {
	mu      sync.Mutex
	members []*busConn
}

type busPkt struct {
	data []byte
	src  netip.AddrPort
}

type busConn struct {
	bus  *bus
	src  netip.AddrPort
	in   chan busPkt
	done chan struct{}
	once sync.Once
}

func (b *bus) join(src netip.AddrPort) *busConn {
	c := &busConn{bus: b, src: src, in: make(chan busPkt, 256), done: make(chan struct{})}
	b.mu.Lock()
	b.members = append(b.members, c)
	b.mu.Unlock()
	return c
}

func (c *busConn) ReadDatagram(buf []byte) (int, netip.AddrPort, error) {
	select {
	case p := <-c.in:
		return copy(buf, p.data), p.src, nil
	case <-c.done:
		return 0, netip.AddrPort{}, net.ErrClosed
	}
}

func (c *busConn) WriteDatagram(data []byte) error {
	c.bus.mu.Lock()
	members := make([]*busConn, len(c.bus.members))
	copy(members, c.bus.members)
	c.bus.mu.Unlock()
	cp := append([]byte(nil), data...)
	for _, m := range members {
		select {
		case m.in <- busPkt{data: cp, src: c.src}:
		case <-m.done:
		default: // mimic UDP loss under a full queue rather than block
		}
	}
	return nil
}

func (c *busConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

// manualClock is a concurrency-safe injectable clock: the reader goroutine reads
// now() while a test goroutine sets/advances it (atomic, no race).
type manualClock struct{ ns atomic.Int64 }

func newManualClock(t time.Time) *manualClock {
	c := &manualClock{}
	c.ns.Store(t.UnixNano())
	return c
}

func (c *manualClock) now() time.Time          { return time.Unix(0, c.ns.Load()) }
func (c *manualClock) advance(d time.Duration) { c.ns.Add(int64(d)) }

// ---------- announcement codec (the hostile unauthenticated input surface) ----------

func TestEncodeAnnounce_SizeAndRoundTrip(t *testing.T) {
	id := devID(0x42)
	pkt := encodeAnnounce(announcement{DeviceID: id, TCPPort: 8443})
	if len(pkt) != announceSize || announceSize != 39 {
		t.Fatalf("encoded size = %d, want %d (39)", len(pkt), announceSize)
	}
	got, err := decodeAnnounce(pkt)
	if err != nil {
		t.Fatalf("decode round-trip: %v", err)
	}
	if got.DeviceID != id || got.TCPPort != 8443 {
		t.Fatalf("round-trip mismatch: got %x/%d", got.DeviceID, got.TCPPort)
	}
}

func TestDecodeAnnounce(t *testing.T) {
	id := devID(0x11)
	good := encodeAnnounce(announcement{DeviceID: id, TCPPort: 8443})
	oversized := append(append([]byte(nil), good...), 0xDE, 0xAD, 0xBE, 0xEF) // trailing junk ignored
	zeroPort := append([]byte(nil), good...)
	zeroPort[37], zeroPort[38] = 0, 0
	maxPort := encodeAnnounce(announcement{DeviceID: id, TCPPort: 65535})
	// A real protocol PING frame ([len=1][type=0x06]) accidentally arriving on the
	// discovery port: 5 bytes, far under 39 — must be dropped, never misparsed.
	pingFrame := []byte{0x00, 0x00, 0x00, 0x01, byte(protocol.MsgPing)}

	cases := []struct {
		name     string
		in       []byte
		wantErr  error
		wantPort uint16 // checked only when wantErr == nil
	}{
		{"good exact 39", good, nil, 8443},
		{"good oversized trailing junk", oversized, nil, 8443},
		{"good max port", maxPort, nil, 65535},
		{"nil", nil, ErrAnnounceTooShort, 0},
		{"empty", []byte{}, ErrAnnounceTooShort, 0},
		{"one byte", good[:1], ErrAnnounceTooShort, 0},
		{"magic prefix only", good[:4], ErrAnnounceTooShort, 0},
		{"38 bytes (one short)", good[:38], ErrAnnounceTooShort, 0},
		{"cross-plane ping frame", pingFrame, ErrAnnounceTooShort, 0},
		{"bad magic byte0", mutate(good, 0, 'X'), ErrAnnounceMagic, 0},
		{"bad magic byte3", mutate(good, 3, 0x00), ErrAnnounceMagic, 0},
		{"all zeros 39", make([]byte, 39), ErrAnnounceMagic, 0},
		{"garbage 50", repeatByte(50, 0xAB), ErrAnnounceMagic, 0},
		{"bad version 0", mutate(good, 4, 0x00), ErrAnnounceVersion, 0},
		{"bad version 2", mutate(good, 4, 0x02), ErrAnnounceVersion, 0},
		{"bad version 255", mutate(good, 4, 0xFF), ErrAnnounceVersion, 0},
		{"zero port", zeroPort, ErrAnnouncePort, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeAnnounce(tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("decode = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode = %v, want nil", err)
			}
			if got.DeviceID != id {
				t.Fatalf("DeviceID = %x, want %x", got.DeviceID, id)
			}
			if got.TCPPort != tc.wantPort {
				t.Fatalf("TCPPort = %d, want %d", got.TCPPort, tc.wantPort)
			}
		})
	}
}

// ---------- WS-3 #1: a second instance is discovered within the announce interval ----------

func TestDiscovery_SecondInstanceWithinInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := &bus{}
	idA, idB := devID(0xA0), devID(0xB0)
	srcA, srcB := srcAddr(10, 40000), srcAddr(11, 40001)
	connA, connB := b.join(srcA), b.join(srcB)

	const portA, portB uint16 = 7001, 7002
	dA, err := New(ctx, idA, portA, withConn(connA), WithAnnounceInterval(50*time.Millisecond))
	if err != nil {
		t.Fatalf("New A: %v", err)
	}
	defer dA.Close()
	dB, err := New(ctx, idB, portB, withConn(connB), WithAnnounceInterval(50*time.Millisecond))
	if err != nil {
		t.Fatalf("New B: %v", err)
	}
	defer dB.Close()

	// Each side announces immediately on start, so discovery happens well within one
	// announce interval. A's hint for B must be B's UDP source IP + B's announced TCP
	// port (the dial target — decisions/ws3/announce-wire-format.md).
	evAB := waitEvent(t, dA.Events(), PeerDiscovered, idB, 2*time.Second)
	if want := netip.AddrPortFrom(srcB.Addr(), portB); evAB.Addr != want {
		t.Fatalf("A discovered B at %v, want %v", evAB.Addr, want)
	}
	evBA := waitEvent(t, dB.Events(), PeerDiscovered, idA, 2*time.Second)
	if want := netip.AddrPortFrom(srcA.Addr(), portA); evBA.Addr != want {
		t.Fatalf("B discovered A at %v, want %v", evBA.Addr, want)
	}
}

// ---------- WS-3 #2: a silent peer is evicted after the heartbeat timeout ----------

func TestDiscovery_SilentPeerEvicted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := &bus{}
	self, peer := devID(0x01), devID(0x02)
	connSelf := b.join(srcAddr(20, 50000))
	peerSrc := srcAddr(21, 50001)
	connPeer := b.join(peerSrc)

	t0 := time.Unix(1_000_000, 0)
	mc := newManualClock(t0) // reader stamps lastSeen = t0 deterministically
	evictTick := make(chan time.Time)

	d, err := New(ctx, self, 9000,
		withConn(connSelf), withClock(mc), withEvictTick(evictTick),
		WithEvictTimeout(30*time.Second),
		WithAnnounceInterval(time.Hour), // don't self-announce during the test
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	// The peer announces once (lastSeen = t0). Reading the Discovered event proves the
	// actor has recorded it before we drive any sweep.
	_ = connPeer.WriteDatagram(encodeAnnounce(announcement{DeviceID: peer, TCPPort: 6000}))
	ev := waitEvent(t, d.Events(), PeerDiscovered, peer, 2*time.Second)
	if want := netip.AddrPortFrom(peerSrc.Addr(), 6000); ev.Addr != want {
		t.Fatalf("discovered at %v, want %v", ev.Addr, want)
	}

	// A sweep before the timeout evicts nothing; a sweep past it evicts. The unbuffered
	// tick channel serialises: the first sweep fully completes before the second is
	// received (the actor processes one select case at a time).
	evictTick <- t0.Add(20 * time.Second) // 20s < 30s: no eviction
	evictTick <- t0.Add(31 * time.Second) // 31s > 30s: evict

	ev = waitEvent(t, d.Events(), PeerEvicted, peer, 2*time.Second)
	if ev.Addr.IsValid() {
		t.Fatalf("evicted event carried a non-zero addr %v", ev.Addr)
	}
}

// TestDiscovery_RefreshKeepsPeer: a peer that keeps announcing is NOT evicted (its
// lastSeen advances past each sweep's window).
func TestDiscovery_RefreshKeepsPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := &bus{}
	self, peer, barrier := devID(0x01), devID(0x02), devID(0x09)
	connSelf := b.join(srcAddr(22, 50100))
	connPeer := b.join(srcAddr(23, 50101))
	connBarrier := b.join(srcAddr(24, 50102))

	t0 := time.Unix(1_000_000, 0)
	mc := newManualClock(t0)
	evictTick := make(chan time.Time)

	d, err := New(ctx, self, 9000,
		withConn(connSelf), withClock(mc), withEvictTick(evictTick),
		WithEvictTimeout(30*time.Second), WithAnnounceInterval(time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	_ = connPeer.WriteDatagram(encodeAnnounce(announcement{DeviceID: peer, TCPPort: 6000}))
	_ = waitEvent(t, d.Events(), PeerDiscovered, peer, 2*time.Second)

	// Advance the clock, then re-announce the SAME (DeviceID, addr) so lastSeen moves
	// to t0+25s but no new event is emitted. A barrier peer announced immediately
	// after is the synchronisation point: the actor processes inbound FIFO, so when we
	// observe the barrier's Discovered the peer's refresh is guaranteed already applied
	// (so the following sweep cannot race ahead of it).
	mc.advance(25 * time.Second)
	_ = connPeer.WriteDatagram(encodeAnnounce(announcement{DeviceID: peer, TCPPort: 6000}))
	_ = connBarrier.WriteDatagram(encodeAnnounce(announcement{DeviceID: barrier, TCPPort: 6001}))
	_ = waitEvent(t, d.Events(), PeerDiscovered, barrier, 2*time.Second)

	// Sweep at t0+31s: the peer has been silent only 6s (< 30s) ⇒ NOT evicted.
	evictTick <- t0.Add(31 * time.Second)

	select {
	case e := <-d.Events():
		if e.Kind == PeerEvicted {
			t.Fatalf("unexpected eviction despite a recent re-announce: %+v", e)
		}
	case <-time.After(200 * time.Millisecond):
		// no eviction — correct
	}
}

// ---------- WS-3 #3: race-free under concurrent announce / evict / dial ----------

func TestDiscovery_RaceAnnounceEvictDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := &bus{}
	self := devID(0x00)
	connSelf := b.join(srcAddr(30, 60000))

	mc := newManualClock(time.Unix(2_000_000, 0))
	evictTick := make(chan time.Time, 16)

	d, err := New(ctx, self, 9100,
		withConn(connSelf), withClock(mc), withEvictTick(evictTick),
		WithEvictTimeout(5*time.Millisecond), WithEventsBuffer(256))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	// Dial consumer: drains Events and "dials" (a stand-in for transport.Dial) on
	// each discovery — concurrent with announce + eviction (CDD-1 -race acceptance).
	var dials atomic.Int64
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for {
			select {
			case e := <-d.Events():
				if e.Kind == PeerDiscovered {
					_ = e.Addr.String() // stand-in for transport.Dial(addr.String())
					dials.Add(1)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	var wg sync.WaitGroup
	const peers, rounds = 8, 40

	for p := 0; p < peers; p++ {
		conn := b.join(srcAddr(byte(100+p), uint16(40000+p)))
		wg.Add(1)
		go func(p int, conn *busConn) {
			defer wg.Done()
			id := devID(byte(0xC0 + p))
			for r := 0; r < rounds; r++ {
				_ = conn.WriteDatagram(encodeAnnounce(announcement{DeviceID: id, TCPPort: uint16(5000 + p)}))
			}
		}(p, conn)
	}
	wg.Add(1)
	go func() { // concurrent eviction sweeps with an advancing clock
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			mc.advance(time.Millisecond)
			select {
			case evictTick <- mc.now():
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
	cancel()
	<-consumerDone
	// The assertions are the race detector + the bounded test timeout: no data race
	// on the registry map (single-goroutine actor), no panic, no deadlock.
	_ = dials.Load()
}

// ---------- WS-3 #4: discovery is a hint, never authorisation ----------

func TestDiscovery_AnnounceIsNotAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := &bus{}
	self := devID(0x01)
	connSelf := b.join(srcAddr(40, 41000))
	spoofSrc := srcAddr(41, 41001)
	connSpoof := b.join(spoofSrc)

	d, err := New(ctx, self, 9200, withConn(connSelf), WithAnnounceInterval(time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	// An entirely unknown / never-paired DeviceID. Discovery has NO allow-list, so it
	// surfaces this as a hint regardless: it takes no authorisation decision (SKILL §7,
	// WS-3 #4). The TLS pin in transport is the sole authority and rejects this peer on
	// dial (cross-checked by WS-2's TestTLS_WrongFingerprintRejected).
	unknown := devID(0xEE)
	_ = connSpoof.WriteDatagram(encodeAnnounce(announcement{DeviceID: unknown, TCPPort: 6543}))

	ev := waitEvent(t, d.Events(), PeerDiscovered, unknown, 2*time.Second)
	if want := netip.AddrPortFrom(spoofSrc.Addr(), 6543); ev.Addr != want {
		t.Fatalf("hint addr = %v, want %v", ev.Addr, want)
	}
	// Structural proof that no auth decision is taken in discovery: New's signature
	// has no allow-list parameter and the package does not import internal/transport
	// (the import graph enforces this at compile time — synthesis §3.C).
}

// TestDiscovery_SelfAnnounceNotEmitted: a device hears its own announcements over
// loopback (the bus fans out to the sender too) and must never surface them.
func TestDiscovery_SelfAnnounceNotEmitted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := &bus{}
	self := devID(0x77)
	connSelf := b.join(srcAddr(50, 42000))
	d, err := New(ctx, self, 9300, withConn(connSelf), WithAnnounceInterval(20*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	// Inject a real peer too, so "no self event" is not merely a dead pipe.
	peer := devID(0x88)
	peerSrc := srcAddr(51, 42001)
	pc := b.join(peerSrc)
	_ = pc.WriteDatagram(encodeAnnounce(announcement{DeviceID: peer, TCPPort: 6000}))

	deadline := time.After(600 * time.Millisecond)
	sawPeer := false
	for !sawPeer {
		select {
		case e := <-d.Events():
			if e.DeviceID == self {
				t.Fatalf("self announce surfaced as an event: %+v", e)
			}
			if e.Kind == PeerDiscovered && e.DeviceID == peer {
				sawPeer = true
			}
		case <-deadline:
			t.Fatal("did not see the real peer (pipe dead?)")
		}
	}
	// Keep draining briefly: across many self-announce ticks, none may leak through.
	drain := time.After(150 * time.Millisecond)
	for {
		select {
		case e := <-d.Events():
			if e.DeviceID == self {
				t.Fatalf("self announce surfaced as an event after warmup: %+v", e)
			}
		case <-drain:
			return
		}
	}
}

// TestDiscovery_ReannounceAddrChange: a re-announce at the SAME address emits no new
// event (no spam); a re-announce at a CHANGED address re-emits a fresh dial hint.
func TestDiscovery_ReannounceAddrChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := &bus{}
	self, peer := devID(0x01), devID(0x02)
	connSelf := b.join(srcAddr(60, 43000))
	peerSrc := srcAddr(61, 43001)
	connPeer := b.join(peerSrc)
	d, err := New(ctx, self, 9400, withConn(connSelf), WithAnnounceInterval(time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	_ = connPeer.WriteDatagram(encodeAnnounce(announcement{DeviceID: peer, TCPPort: 6000}))
	ev := waitEvent(t, d.Events(), PeerDiscovered, peer, 2*time.Second)
	if want := netip.AddrPortFrom(peerSrc.Addr(), 6000); ev.Addr != want {
		t.Fatalf("first hint = %v, want %v", ev.Addr, want)
	}

	// Same address, then a changed port (changed addr). The actor processes in FIFO
	// order, so the next Discovered we observe must be the changed one — proving the
	// same-address re-announce emitted nothing.
	_ = connPeer.WriteDatagram(encodeAnnounce(announcement{DeviceID: peer, TCPPort: 6000}))
	_ = connPeer.WriteDatagram(encodeAnnounce(announcement{DeviceID: peer, TCPPort: 7000}))
	ev = waitEvent(t, d.Events(), PeerDiscovered, peer, 2*time.Second)
	if want := netip.AddrPortFrom(peerSrc.Addr(), 7000); ev.Addr != want {
		t.Fatalf("addr-change re-announce = %v, want port 7000", ev.Addr)
	}
}

// ---------- lifecycle / leaks (GR-3 / GR-13) ----------

func TestDiscovery_NoGoroutineLeakOnChurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	base := stableGoroutines(150 * time.Millisecond)
	const N = 20
	for i := 0; i < N; i++ {
		b := &bus{}
		conn := b.join(srcAddr(byte(i%200+1), uint16(45000+i)))
		d, err := New(ctx, devID(byte(i)), uint16(9000+i), withConn(conn), WithAnnounceInterval(5*time.Millisecond))
		if err != nil {
			t.Fatalf("iter %d New: %v", i, err)
		}
		if err := d.Close(); err != nil {
			t.Fatalf("iter %d Close: %v", i, err)
		}
	}
	assertGoroutinesReturn(t, base, 3*time.Second)
}

func TestDiscovery_CloseIsIdempotent(t *testing.T) {
	b := &bus{}
	conn := b.join(srcAddr(99, 47000))
	d, err := New(context.Background(), devID(0xAB), 9999, withConn(conn), WithAnnounceInterval(time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = d.Close() }()
	}
	wg.Wait()
}

func TestDiscovery_CtxCancelTearsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	base := stableGoroutines(100 * time.Millisecond)

	b := &bus{}
	conn := b.join(srcAddr(70, 46000))
	d, err := New(ctx, devID(0x33), 9500, withConn(conn), WithAnnounceInterval(5*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cancel()      // no explicit Close: the ctx watcher must tear everything down
	_ = d.Close() // also waits; idempotent with the watcher-triggered teardown
	assertGoroutinesReturn(t, base, 3*time.Second)
}

// ---------- real socket (best-effort; the deterministic bus tests above are the
// authoritative acceptance gate) ----------

// TestDiscovery_RealMulticastLoopback exercises the production udpMulticast adapter
// end-to-end on a real interface. It SKIPS (never fails) where the environment
// cannot deliver same-host multicast — a sandbox with no multicast interface, or a
// firewall — because discovery is a hint and the real Mac<->Windows / Windows
// Firewall path is closed by the CI matrix + CROSS_PLATFORM_CHECKLIST.md
// (decisions/ws3/multicast-socket-stdlib-vs-xnet.md).
func TestDiscovery_RealMulticastLoopback(t *testing.T) {
	ifi := defaultMulticastInterface()
	if ifi == nil {
		t.Skip("no multicast-capable interface; real socket covered by CI matrix + checklist")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	group := netip.MustParseAddrPort("239.192.0.78:21099") // test-specific, avoid clashing with a live daemon
	idA, idB := devID(0xA1), devID(0xB2)
	const portA, portB uint16 = 7101, 7102

	dA, err := New(ctx, idA, portA, WithGroup(group), WithInterface(ifi), WithAnnounceInterval(100*time.Millisecond))
	if err != nil {
		t.Skipf("cannot open multicast socket on %s: %v", ifi.Name, err)
	}
	defer dA.Close()
	dB, err := New(ctx, idB, portB, WithGroup(group), WithInterface(ifi), WithAnnounceInterval(100*time.Millisecond))
	if err != nil {
		t.Skipf("cannot open multicast socket on %s: %v", ifi.Name, err)
	}
	defer dB.Close()

	ev, ok := waitEventOrSkip(dA.Events(), PeerDiscovered, idB, 6*time.Second)
	if !ok {
		t.Skip("real multicast not delivered in this environment; covered by CI matrix + checklist")
	}
	// A genuine discovery happened: assert the dial hint carries B's announced port.
	if ev.Addr.Port() != portB {
		t.Fatalf("real discovery hint = %v, want port %d", ev.Addr, portB)
	}
	if !ev.Addr.Addr().IsValid() {
		t.Fatalf("real discovery hint had an invalid address: %v", ev.Addr)
	}
}

// ---------- goroutine-count helpers (bounded polls, not sleeps-for-sync) ----------

func stableGoroutines(window time.Duration) int {
	prev := runtime.NumGoroutine()
	ticker := time.NewTicker(window)
	defer ticker.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		<-ticker.C
		runtime.GC()
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}
		prev = n
	}
	return prev
}

func assertGoroutinesReturn(t *testing.T, target int, timeout time.Duration) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.Now().Add(timeout)
	var n int
	for time.Now().Before(deadline) {
		runtime.GC()
		n = runtime.NumGoroutine()
		if n <= target {
			return
		}
		<-ticker.C
	}
	t.Fatalf("goroutine leak: NumGoroutine=%d did not return to baseline %d within %v", n, target, timeout)
}
