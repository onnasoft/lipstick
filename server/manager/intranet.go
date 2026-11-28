package manager

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/logger"
)

// Errors returned when a client asks for an intranet port. The upgrade handler
// turns them into a status line the client can read.
var (
	ErrPortNotAllowed = errors.New("this domain may not reserve intranet ports")
	ErrPortOutOfRange = errors.New("the port is outside the range the server allows")
	ErrPortTaken      = errors.New("the port is already reserved or cannot be opened")
)

// Transports of an intranet port.
const (
	protoTCP = "tcp"
	protoUDP = "udp"
)

// intranetPorts is the registry of reserved ports. The server owns the policy:
// which domains may reserve, which networks may connect, and which range of
// ports is allowed. A client only chooses a port inside that policy.
//
// Intranet ports are plain TCP or UDP. They have no TLS, so they cannot tell
// domains apart by name. Each reserved port serves exactly one domain, and the
// port number is the service identity for the people and systems in the intranet.
// A TCP port and a UDP port may have the same number.
type intranetPorts struct {
	mu       sync.Mutex
	bind     string
	min, max int
	policy   map[string][]*net.IPNet
	reserved map[portKey]*portReservation
}

type portKey struct {
	proto string
	port  int
}

type portReservation struct {
	domain string
	ln     net.Listener   // TCP
	pc     net.PacketConn // UDP
}

func (r *portReservation) close() {
	if r.ln != nil {
		r.ln.Close()
	}
	if r.pc != nil {
		r.pc.Close()
	}
}

// ConfigureIntranetPorts enables intranet ports. policy maps each domain allowed
// to reserve a port to the networks that may connect to it.
func (m *Manager) ConfigureIntranetPorts(bind string, minPort, maxPort int, policy map[string][]*net.IPNet) {
	m.intranet = &intranetPorts{
		bind:     bind,
		min:      minPort,
		max:      maxPort,
		policy:   policy,
		reserved: make(map[portKey]*portReservation),
	}
}

// reserveIntranetPort opens a TCP port for domain.
func (m *Manager) reserveIntranetPort(domain string, port int) error {
	return m.reserveIntranet(domain, protoTCP, port)
}

// reserveIntranetUDPPort opens a UDP port for domain.
func (m *Manager) reserveIntranetUDPPort(domain string, port int) error {
	return m.reserveIntranet(domain, protoUDP, port)
}

// reserveIntranet opens port for domain under the policy. Asking again for a port
// the domain already holds does nothing.
func (m *Manager) reserveIntranet(domain, proto string, port int) error {
	p := m.intranet
	if p == nil {
		return ErrPortNotAllowed
	}
	allowed, ok := p.policy[domain]
	if !ok {
		return ErrPortNotAllowed
	}
	if port < p.min || port > p.max {
		return ErrPortOutOfRange
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	key := portKey{proto: proto, port: port}
	if r, ok := p.reserved[key]; ok {
		if r.domain == domain {
			return nil
		}
		return ErrPortTaken
	}

	addr := net.JoinHostPort(p.bind, strconv.Itoa(port))
	res := &portReservation{domain: domain}
	if proto == protoUDP {
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrPortTaken, err)
		}
		res.pc = pc
		go m.serveIntranetUDP(pc, domain, allowed)
	} else {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrPortTaken, err)
		}
		res.ln = ln
		go m.acceptIntranet(ln, domain, allowed)
	}
	p.reserved[key] = res

	logger.Default.Info("WARNING: intranet port (plain "+proto+", no TLS) reserved:", addr, "domain:", domain)
	return nil
}

// releaseIntranetPorts closes every port the domain holds. Called when the
// domain has no clients left, or when its hub is dropped.
func (m *Manager) releaseIntranetPorts(domain string) {
	p := m.intranet
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, r := range p.reserved {
		if r.domain == domain {
			r.close()
			delete(p.reserved, key)
			logger.Default.Info("Intranet port released:", key.proto, key.port, "domain:", domain)
		}
	}
}

// releaseAllIntranetPorts closes every reserved port. Called on shutdown.
func (m *Manager) releaseAllIntranetPorts() {
	p := m.intranet
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, r := range p.reserved {
		r.close()
		delete(p.reserved, key)
	}
}

func (m *Manager) acceptIntranet(ln net.Listener, domain string, allowed []*net.IPNet) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			logger.Default.Error("Intranet port accept error:", err)
			time.Sleep(acceptRetryDelay)
			continue
		}
		go m.handleIntranet(conn, domain, allowed)
	}
}

func (m *Manager) handleIntranet(conn net.Conn, domain string, allowed []*net.IPNet) {
	if !sourceAllowed(conn.RemoteAddr(), allowed) {
		logger.Default.Error("Intranet connection from outside the allowed networks refused, domain:", domain, "from:", conn.RemoteAddr())
		conn.Close()
		return
	}

	hub, ok := m.GetHub(domain)
	if !ok {
		// The client for this domain is not connected right now.
		rejectVisitor(conn)
		return
	}

	admitted, ok := m.admitVisitor(conn, domain, false)
	if !ok {
		return
	}
	if !hub.submitVisitor(admitted) {
		rejectVisitor(conn)
	}
}

// sourceAllowed reports whether the remote address is inside one of the
// networks. An address that cannot be parsed is refused. TCP and UDP addresses
// are both accepted.
func sourceAllowed(addr net.Addr, allowed []*net.IPNet) bool {
	var ip net.IP
	switch a := addr.(type) {
	case *net.TCPAddr:
		ip = a.IP
	case *net.UDPAddr:
		ip = a.IP
	}
	if ip == nil {
		return false
	}
	for _, n := range allowed {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
