package discovery

import (
	"fmt"
	"net"
	"net/netip"
)

// datagramConn abstracts the multicast socket so the registry actor, reader, and
// announcer are testable against an in-memory bus (no real interface / firewall —
// decisions/ws3/multicast-socket-stdlib-vs-xnet.md, registry-actor-and-clock-injection.md).
// The production implementation is udpMulticast.
type datagramConn interface {
	// ReadDatagram reads one datagram into buf, returning its length and source
	// address. It blocks until a datagram arrives or the conn is closed (a closed
	// conn returns a non-nil error, unblocking the reader — GR-4 cancellable read).
	ReadDatagram(buf []byte) (n int, src netip.AddrPort, err error)
	// WriteDatagram sends data to the multicast group.
	WriteDatagram(data []byte) error
	// Close releases the socket and unblocks a goroutine parked in ReadDatagram.
	Close() error
}

// udpMulticast is the production datagramConn: a group-joined receive socket plus a
// separate ephemeral send socket. Pure stdlib (GR-11); GOOS=windows compiles
// unchanged.
type udpMulticast struct {
	rx    *net.UDPConn
	tx    *net.UDPConn
	group netip.AddrPort
}

// newUDPMulticast joins the multicast group on ifi (nil = system-assigned) for
// receive and opens an ephemeral socket for send. When ifi has an IPv4 address the
// send socket is bound to it so the outbound multicast egresses the joined
// interface and loops back to the receive socket reliably (the spike showed lo0
// does not route the group; a real interface does).
func newUDPMulticast(group netip.AddrPort, ifi *net.Interface) (*udpMulticast, error) {
	if !group.IsValid() || !group.Addr().Is4() {
		return nil, fmt.Errorf("discovery: invalid IPv4 multicast group %q", group)
	}
	gaddr := &net.UDPAddr{IP: group.Addr().AsSlice(), Port: int(group.Port())}
	rx, err := net.ListenMulticastUDP("udp4", ifi, gaddr)
	if err != nil {
		return nil, fmt.Errorf("discovery: join multicast group %s: %w", group, err)
	}

	laddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	if ifi != nil {
		if ip := firstIPv4(ifi); ip != nil {
			laddr.IP = ip
		}
	}
	tx, err := net.ListenUDP("udp4", laddr)
	if err != nil && !laddr.IP.Equal(net.IPv4zero) {
		// Fall back to the unspecified address (OS default multicast route).
		tx, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	}
	if err != nil {
		_ = rx.Close()
		return nil, fmt.Errorf("discovery: open send socket: %w", err)
	}
	return &udpMulticast{rx: rx, tx: tx, group: group}, nil
}

func (m *udpMulticast) ReadDatagram(buf []byte) (int, netip.AddrPort, error) {
	return m.rx.ReadFromUDPAddrPort(buf)
}

func (m *udpMulticast) WriteDatagram(data []byte) error {
	_, err := m.tx.WriteToUDPAddrPort(data, m.group)
	return err
}

func (m *udpMulticast) Close() error {
	errRx := m.rx.Close()
	errTx := m.tx.Close()
	if errRx != nil {
		return errRx
	}
	return errTx
}

// defaultMulticastInterface returns the first UP, multicast-capable, non-loopback
// interface that has an IPv4 address, or nil to let the OS pick (system-assigned).
// Loopback is skipped because the spike proved lo0 does not deliver the group.
func defaultMulticastInterface() *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for i := range ifaces {
		f := ifaces[i].Flags
		if f&net.FlagUp == 0 || f&net.FlagMulticast == 0 || f&net.FlagLoopback != 0 {
			continue
		}
		if firstIPv4(&ifaces[i]) != nil {
			return &ifaces[i]
		}
	}
	return nil
}

func firstIPv4(ifi *net.Interface) net.IP {
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip4 := ip.To4(); ip4 != nil {
			return ip4
		}
	}
	return nil
}
