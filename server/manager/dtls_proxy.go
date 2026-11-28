package manager

import (
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
	"github.com/pion/dtls/v3"
)

// DTLS proxy: the UDP counterpart of the TLS proxy port. One UDP socket receives
// the handshakes of many clients. The first datagram of each source carries the
// ClientHello, whose SNI names the domain. The source then becomes its own DTLS
// connection, terminated here with the server's certificate. Its decrypted
// datagrams go to the domain's client, as an intranet UDP flow does.
const (
	dtlsMaxPeers    = 4096
	dtlsPeerQueue   = 256
	dtlsHandshakeTO = 10 * time.Second
)

// peerPacketConn is the packet connection of one DTLS peer. It hands the peer's
// datagrams, which the shared socket received, to the DTLS stack, and sends the
// stack's datagrams back to the peer.
type peerPacketConn struct {
	shared  net.PacketConn
	peer    *net.UDPAddr
	in      chan []byte
	done    chan struct{}
	once    sync.Once
	onClose func()
}

func newPeerPacketConn(shared net.PacketConn, peer *net.UDPAddr, onClose func()) *peerPacketConn {
	return &peerPacketConn{
		shared:  shared,
		peer:    peer,
		in:      make(chan []byte, dtlsPeerQueue),
		done:    make(chan struct{}),
		onClose: onClose,
	}
}

// deliver queues a datagram from the peer. A full queue drops it, as UDP does.
func (p *peerPacketConn) deliver(d []byte) {
	select {
	case p.in <- d:
	case <-p.done:
	default:
	}
}

func (p *peerPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	select {
	case d := <-p.in:
		return copy(b, d), p.peer, nil
	case <-p.done:
		return 0, nil, net.ErrClosed
	}
}

func (p *peerPacketConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	return p.shared.WriteTo(b, p.peer)
}

func (p *peerPacketConn) Close() error {
	p.once.Do(func() {
		close(p.done)
		if p.onClose != nil {
			p.onClose()
		}
	})
	return nil
}

func (p *peerPacketConn) LocalAddr() net.Addr              { return p.shared.LocalAddr() }
func (p *peerPacketConn) SetDeadline(time.Time) error      { return nil }
func (p *peerPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (p *peerPacketConn) SetWriteDeadline(time.Time) error { return nil }

// ServeDTLSProxy reads the shared socket and starts one DTLS peer per new source.
func (m *Manager) ServeDTLSProxy(pc net.PacketConn, tlsConf *tls.Config, port int) {
	var mu sync.Mutex
	peers := make(map[string]*peerPacketConn)
	buf := make([]byte, helper.MaxDatagram)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			logger.Default.Error("DTLS proxy read error:", err)
			time.Sleep(acceptRetryDelay)
			continue
		}
		src, ok := addr.(*net.UDPAddr)
		if !ok {
			continue
		}
		key := src.String()
		datagram := append([]byte(nil), buf[:n]...)

		mu.Lock()
		p := peers[key]
		full := len(peers) >= dtlsMaxPeers
		mu.Unlock()

		if p == nil {
			if full {
				continue
			}
			sni, ok := dtlsServerName(datagram)
			if !ok {
				continue // not a DTLS ClientHello with SNI: nothing to route it by
			}
			domain := strings.ToLower(strings.TrimRight(sni, "."))
			if _, ok := m.GetHub(domain); !ok {
				continue
			}
			// DTLS carries no HTTP, so a restricted domain cannot be checked over it.
			if rec, err := m.authManager.GetDomain(domain); err != nil || rec.AuthLatched {
				continue
			}
			p = newPeerPacketConn(pc, src, func() {
				mu.Lock()
				delete(peers, key)
				mu.Unlock()
			})
			mu.Lock()
			peers[key] = p
			mu.Unlock()
			go m.serveDTLSPeer(p, src, domain, port, tlsConf)
		}
		p.deliver(datagram)
	}
}

// serveDTLSPeer runs the DTLS connection of one peer and carries its datagrams to
// the domain's client.
func (m *Manager) serveDTLSPeer(p *peerPacketConn, src *net.UDPAddr, domain string, port int, tlsConf *tls.Config) {
	defer p.Close()
	conn, err := dtls.Server(p, src, &dtls.Config{Certificates: tlsConf.Certificates})
	if err != nil {
		return
	}
	defer conn.Close()

	flow := m.newUDPFlow(domain, port, func(d []byte) { conn.Write(d) })
	if flow == nil {
		return // no client session for the domain right now
	}
	flow.onClose = func() { conn.Close() }
	flow.start()

	buf := make([]byte, helper.MaxDatagram)
	for {
		// A peer quiet for udpFlowIdle is closed, as an intranet UDP flow is.
		conn.SetReadDeadline(time.Now().Add(udpFlowIdle))
		n, err := conn.Read(buf)
		if err != nil {
			flow.stop()
			return
		}
		flow.touch()
		flow.send(append([]byte(nil), buf[:n]...))
	}
}
