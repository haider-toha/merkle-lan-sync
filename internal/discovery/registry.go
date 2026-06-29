package discovery

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// EventKind classifies a discovery Event.
type EventKind int

const (
	// PeerDiscovered: a peer was seen for the first time, or re-announced at a new
	// address. Addr is the dial hint.
	PeerDiscovered EventKind = iota
	// PeerEvicted: a peer went silent past the eviction timeout. Addr is zero.
	PeerEvicted
)

func (k EventKind) String() string {
	switch k {
	case PeerDiscovered:
		return "PeerDiscovered"
	case PeerEvicted:
		return "PeerEvicted"
	default:
		return fmt.Sprintf("EventKind(%d)", int(k))
	}
}

// Event is the discovery hint surfaced to the engine (mirrors transport.Event so the
// engine drains both layers with one uniform select). The DeviceID is the peer's
// unauthenticated *claim* (SKILL §7); authorisation happens later at the TLS pin.
type Event struct {
	Kind     EventKind
	DeviceID protocol.DeviceID
	Addr     netip.AddrPort // dial hint on PeerDiscovered; zero on PeerEvicted
}

// peerEntry is the per-peer registry state, owned solely by the actor goroutine.
type peerEntry struct {
	addr     netip.AddrPort
	lastSeen time.Time
}

// inAnnounce is the actor's inbound command: a decoded announcement, its source
// address, and the receive time the reader stamped via the injected clock.
type inAnnounce struct {
	deviceID protocol.DeviceID
	addr     netip.AddrPort
	at       time.Time
}

// registry is the GR-4 single-goroutine actor that exclusively owns the peer map
// and is the sole emitter of Events (CDD-1). It never shares the map with another
// goroutine; producers reach it only through channels.
type registry struct {
	peers        map[protocol.DeviceID]peerEntry
	evictTimeout time.Duration

	inbound   <-chan inAnnounce // decoded announcements from the reader
	evictTick <-chan time.Time  // sweep ticks; each tick's time is "now" for the sweep
	stop      <-chan struct{}   // ctx.Done(): stop the actor
	emit      func(Event)       // non-blocking emit onto the Events channel
}

// run is the actor loop: the only place the peer map is read or written.
func (r *registry) run() {
	for {
		select {
		case <-r.stop:
			return
		case in := <-r.inbound:
			r.handleAnnounce(in)
		case now := <-r.evictTick:
			r.sweep(now)
		}
	}
}

// handleAnnounce records (or refreshes) a peer. A first sighting, or a re-announce
// at a changed address, emits PeerDiscovered (a fresh dial hint); a same-address
// re-announce only refreshes lastSeen (no event spam).
func (r *registry) handleAnnounce(in inAnnounce) {
	prev, existed := r.peers[in.deviceID]
	r.peers[in.deviceID] = peerEntry{addr: in.addr, lastSeen: in.at}
	if !existed || prev.addr != in.addr {
		r.emit(Event{Kind: PeerDiscovered, DeviceID: in.deviceID, Addr: in.addr})
	}
}

// sweep evicts every peer silent for longer than evictTimeout, using the tick's
// time as "now". Deleting during range is safe in Go.
func (r *registry) sweep(now time.Time) {
	for id, e := range r.peers {
		if now.Sub(e.lastSeen) > r.evictTimeout {
			delete(r.peers, id)
			r.emit(Event{Kind: PeerEvicted, DeviceID: id})
		}
	}
}
