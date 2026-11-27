package manager

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
)

// UDP intranet ports. Each source address that sends to the port gets a flow: one
// stream in a client session. Datagrams travel framed on that stream. A flow with
// no traffic for udpFlowIdle is closed, and the next datagram from that source
// opens a new one. Datagrams that do not fit the flow's queue are dropped, as UDP
// does on a congested path.
const (
	udpFlowIdle   = 60 * time.Second
	udpFlowQueue  = 256
	udpMaxFlows   = 4096
	udpSweepEvery = 10 * time.Second
)

// udpFlow is one source address on a UDP intranet port.
type udpFlow struct {
	reply  func(p []byte) // sends a datagram back to the source
	stream net.Conn
	queue  chan []byte // datagrams from the source, waiting to be framed to the client
	done   chan struct{}
	once   sync.Once
	seen   atomic.Int64 // unix nanoseconds of the last datagram from the source

	onClose func() // removes the flow from its listener's table
}

func (f *udpFlow) touch() { f.seen.Store(time.Now().UnixNano()) }

func (f *udpFlow) idleFor(now time.Time) time.Duration {
	return now.Sub(time.Unix(0, f.seen.Load()))
}

// stop closes the flow once: its stream, its loops, and its table entry.
func (f *udpFlow) stop() {
	f.once.Do(func() {
		close(f.done)
		f.stream.Close()
		if f.onClose != nil {
			f.onClose()
		}
	})
}

// send queues a datagram for the client. When the queue is full the datagram is dropped.
func (f *udpFlow) send(p []byte) {
	select {
	case f.queue <- p:
	case <-f.done:
	default:
	}
}

// start begins the two loops of the flow: source to client, and client to source.
func (f *udpFlow) start() {
	go f.writeLoop()
	go f.readLoop()
}

func (f *udpFlow) writeLoop() {
	for {
		select {
		case p := <-f.queue:
			if err := helper.WriteFrame(f.stream, p); err != nil {
				f.stop()
				return
			}
		case <-f.done:
			return
		}
	}
}

// readLoop sends what the client returns to the source it belongs to.
func (f *udpFlow) readLoop() {
	for {
		p, err := helper.ReadFrame(f.stream)
		if err != nil {
			f.stop()
			return
		}
		f.reply(p)
	}
}

// newUDPFlow opens the stream for a new source on a domain's client session. port
// is the server port the source came in on. reply sends datagrams back to the
// source. It returns nil when no client session of the domain can take the flow.
func (m *Manager) newUDPFlow(domain string, port int, reply func(p []byte)) *udpFlow {
	hub, ok := m.GetHub(domain)
	if !ok {
		return nil
	}
	stream, ok := hub.openStream("LIPSTICK-UDP", port)
	if !ok {
		return nil
	}
	f := &udpFlow{
		reply:  reply,
		stream: stream,
		queue:  make(chan []byte, udpFlowQueue),
		done:   make(chan struct{}),
	}
	f.touch()
	return f
}

// serveIntranetUDP reads the datagrams of one reserved UDP port.
func (m *Manager) serveIntranetUDP(pc net.PacketConn, domain string, allowed []*net.IPNet) {
	var mu sync.Mutex
	flows := make(map[string]*udpFlow)
	stopSweep := make(chan struct{})
	defer close(stopSweep)

	// Close the flows that have gone quiet.
	go func() {
		ticker := time.NewTicker(udpSweepEvery)
		defer ticker.Stop()
		for {
			select {
			case <-stopSweep:
				return
			case now := <-ticker.C:
				var idle []*udpFlow
				mu.Lock()
				for _, f := range flows {
					if f.idleFor(now) > udpFlowIdle {
						idle = append(idle, f)
					}
				}
				mu.Unlock()
				for _, f := range idle {
					f.stop()
				}
			}
		}
	}()

	// When the port is released, every flow closes with it.
	defer func() {
		mu.Lock()
		var all []*udpFlow
		for _, f := range flows {
			all = append(all, f)
		}
		mu.Unlock()
		for _, f := range all {
			f.stop()
		}
	}()

	buf := make([]byte, helper.MaxDatagram)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			logger.Default.Error("Intranet UDP read error:", err)
			time.Sleep(acceptRetryDelay)
			continue
		}
		src, ok := addr.(*net.UDPAddr)
		if !ok {
			continue
		}
		key := src.String()

		mu.Lock()
		f := flows[key]
		full := len(flows) >= udpMaxFlows
		mu.Unlock()

		if f == nil {
			if !sourceAllowed(src, allowed) {
				logger.Default.Error("Intranet UDP datagram from outside the allowed networks refused, domain:", domain, "from:", key)
				continue
			}
			if full {
				continue
			}
			port := pc.LocalAddr().(*net.UDPAddr).Port
			f = m.newUDPFlow(domain, port, func(p []byte) { pc.WriteTo(p, src) })
			if f == nil {
				continue // no client session right now: the datagram is dropped, as UDP allows
			}
			f.onClose = func() {
				mu.Lock()
				if flows[key] == f {
					delete(flows, key)
				}
				mu.Unlock()
			}
			mu.Lock()
			flows[key] = f
			mu.Unlock()
			f.start()
		}
		f.touch()
		f.send(append([]byte(nil), buf[:n]...))
	}
}
