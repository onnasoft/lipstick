package manager

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
)

// Errors returned when a client asks for an intranet port. The upgrade handler
// turns them into a status line the client can read.
var (
	ErrPortNotAllowed = errors.New("this domain may not reserve intranet ports")
	ErrPortOutOfRange = errors.New("the port is outside the range the server allows")
	ErrPortTaken      = errors.New("the port is already reserved or cannot be opened")
)

// intranetPorts is the registry of reserved ports. The server owns the policy:
// which domains may reserve, which networks may connect, and which range of
// ports is allowed. A client only chooses a port inside that policy.
//
// Intranet ports are plain TCP. They have no TLS, so they cannot tell domains
// apart by name. Each reserved port serves exactly one domain, and the port
// number is the service identity for the people and systems in the intranet.
type intranetPorts struct {
	mu       sync.Mutex
	bind     string
	min, max int
	policy   map[string][]*net.IPNet
	reserved map[int]*portReservation
}

type portReservation struct {
	domain string
	ln     net.Listener
}

// ConfigureIntranetPorts enables intranet ports. policy maps each domain allowed
// to reserve a port to the networks that may connect to it.
func (m *Manager) ConfigureIntranetPorts(bind string, minPort, maxPort int, policy map[string][]*net.IPNet) {
	m.intranet = &intranetPorts{
		bind:     bind,
		min:      minPort,
		max:      maxPort,
		policy:   policy,
		reserved: make(map[int]*portReservation),
	}
}

// reserveIntranetPort opens port for domain. Asking again for a port the domain
// already holds does nothing.
func (m *Manager) reserveIntranetPort(domain string, port int) error {
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

	if r, ok := p.reserved[port]; ok {
		if r.domain == domain {
			return nil
		}
		return ErrPortTaken
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(p.bind, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPortTaken, err)
	}
	p.reserved[port] = &portReservation{domain: domain, ln: ln}
	go m.acceptIntranet(ln, domain, allowed)

	logger.Default.Info("WARNING: intranet port (plain TCP, no TLS) reserved:", net.JoinHostPort(p.bind, strconv.Itoa(port)), "domain:", domain)
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
	for port, r := range p.reserved {
		if r.domain == domain {
			r.ln.Close()
			delete(p.reserved, port)
			logger.Default.Info("Intranet port released:", port, "domain:", domain)
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
	for port, r := range p.reserved {
		r.ln.Close()
		delete(p.reserved, port)
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

	if !hub.submitVisitor(&helper.RemoteConn{Conn: conn, Domain: domain}) {
		rejectVisitor(conn)
	}
}

// sourceAllowed reports whether the remote address is inside one of the
// networks. An address that cannot be parsed is refused.
func sourceAllowed(addr net.Addr, allowed []*net.IPNet) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || tcp.IP == nil {
		return false
	}
	for _, n := range allowed {
		if n.Contains(tcp.IP) {
			return true
		}
	}
	return false
}
