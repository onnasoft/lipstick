package manager

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
	"github.com/hashicorp/yamux"
)

// Mux connections. The client opens a few TLS connections and runs a yamux session
// on each. Every visitor becomes one stream inside a session, so no new TLS
// connection is needed per visitor. A session is authenticated once, when it is
// opened, with a proof over a one-time nonce. The server opens streams and the
// client accepts them, so only the client that opened a session can be served on it.
const (
	muxMaxSessionsPerHub = 64
	muxWriteTimeout      = 5 * time.Second
	maxNonceEntries      = 100000
)

// muxSession is one yamux session and the connection that carries it.
type muxSession struct {
	session     *yamux.Session
	carrier     net.Conn
	passthrough bool // the client's service ends TLS itself, so the session can take passthrough visitors
}

func muxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 15 * time.Second
	cfg.ConnectionWriteTimeout = 10 * time.Second
	cfg.LogOutput = io.Discard
	return cfg
}

// nonceCache remembers the mux nonces seen inside the time window, so a captured
// request cannot be replayed. Its zero value is ready to use.
type nonceCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// take records nonce and reports whether it was new. A nonce seen before is refused.
func (c *nonceCache) take(nonce string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = make(map[string]time.Time)
	}
	if len(c.seen) >= maxNonceEntries {
		for n, t := range c.seen {
			if now.Sub(t) > helper.MuxNonceWindow {
				delete(c.seen, n)
			}
		}
	}
	if _, used := c.seen[nonce]; used {
		return false
	}
	c.seen[nonce] = now
	return true
}

// handleMux authenticates a mux connection and keeps it as a session of the domain's hub.
// On any failure the connection is closed without sending bytes.
func (manager *Manager) handleMux(conn net.Conn, req *http.Request) {
	domainName := strings.Split(req.Host, ":")[0]
	nonce := req.Header.Get(helper.MuxNonceHeader)
	now := time.Now()

	if _, ok := helper.MuxNonceTime(nonce, now); !ok {
		logger.Default.Error("Mux connection with an expired or malformed nonce for domain:", domainName)
		countTunnelFailure("mux_nonce")
		conn.Close()
		return
	}
	record, err := manager.authManager.GetDomain(domainName)
	if err != nil || !helper.VerifyMuxProof(record.ApiKey, nonce, req.Header.Get(helper.ProofHeader)) {
		logger.Default.Error("Mux connection without a valid proof for domain:", domainName)
		countTunnelFailure("mux_proof")
		conn.Close()
		return
	}
	if !manager.nonces.take(nonce, now) {
		logger.Default.Error("Mux connection with a replayed nonce for domain:", domainName)
		countTunnelFailure("mux_replayed_nonce")
		conn.Close()
		return
	}

	hub, ok := manager.GetHub(domainName)
	if !ok {
		countTunnelFailure("mux_no_hub")
		conn.Close()
		return
	}
	session, err := yamux.Server(conn, muxConfig())
	if err != nil {
		countTunnelFailure("mux_session")
		conn.Close()
		return
	}
	ms := &muxSession{
		session:     session,
		carrier:     conn,
		passthrough: req.Header.Get(helper.PassthroughHeader) == "1",
	}
	if !hub.addMux(ms) {
		countTunnelFailure("mux_refused")
		session.Close()
		return
	}
	// Forget the session as soon as it closes, so no visitor is sent to it.
	go func() {
		<-session.CloseChan()
		hub.removeMux(ms)
	}()
	go hub.probeTunnel(ms)
}

// tunnelProbeEvery is how often a session's round trip is measured. A variable so tests
// can make it short.
var tunnelProbeEvery = 5 * time.Second

// probeTunnel measures the round trip of a mux session with yamux's ping, every
// tunnelProbeEvery, until the session closes. The ping goes through the same connection
// as the visitors' data, so the time includes any wait behind data frames: it is the
// latency a request would see on the tunnel, not the idle wire time.
func (hub *NetworkHub) probeTunnel(ms *muxSession) {
	ticker := time.NewTicker(tunnelProbeEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ms.session.CloseChan():
			return
		case <-ticker.C:
		}
		type result struct {
			rtt time.Duration
			err error
		}
		done := make(chan result, 1)
		go func() {
			rtt, err := ms.session.Ping()
			done <- result{rtt, err}
		}()
		select {
		case r := <-done:
			if r.err == nil {
				tunnelLatency.Observe(hub.HubName, r.rtt.Seconds())
			}
		case <-ms.session.CloseChan():
			return
		case <-time.After(10 * time.Second):
			// No answer in ten seconds: yamux's keep-alive closes a stalled session soon.
		}
	}
}

// addMux adds a session to the hub. It refuses when the hub has stopped or is full.
func (hub *NetworkHub) addMux(ms *muxSession) bool {
	select {
	case <-hub.done:
		return false
	default:
	}
	hub.muxMu.Lock()
	defer hub.muxMu.Unlock()
	if len(hub.muxes) >= muxMaxSessionsPerHub {
		return false
	}
	hub.muxes = append(hub.muxes, ms)
	return true
}

func (hub *NetworkHub) removeMux(ms *muxSession) {
	hub.muxMu.Lock()
	defer hub.muxMu.Unlock()
	for i, m := range hub.muxes {
		if m == ms {
			hub.muxes = append(hub.muxes[:i], hub.muxes[i+1:]...)
			return
		}
	}
}

// takeMux opens a stream for the visitor on a live session, and relays the visitor
// over it. The first line gives the client the server port the visitor came in
// on. It returns false when no session can take the visitor.
func (hub *NetworkHub) takeMux(visitor *helper.RemoteConn, port int) bool {
	conn, ok := hub.openStream("LIPSTICK-PORT", port, false)
	if !ok {
		return false
	}
	go hub.syncConnections(visitor, conn)
	return true
}

// openStream opens a stream on a live session and writes its first line, "kind port".
// It returns false when no session can take it.
func (hub *NetworkHub) openStream(kind string, port int, needPassthrough bool) (net.Conn, bool) {
	hub.muxMu.Lock()
	sessions := append([]*muxSession(nil), hub.muxes...)
	hub.muxNext++
	start := hub.muxNext
	hub.muxMu.Unlock()

	for i := range sessions {
		ms := sessions[(start+i)%len(sessions)]
		if ms.session.IsClosed() || (needPassthrough && !ms.passthrough) {
			continue
		}
		stream, err := ms.session.OpenStream()
		if err != nil {
			continue
		}
		stream.SetWriteDeadline(time.Now().Add(muxWriteTimeout))
		_, err = stream.Write([]byte(kind + " " + strconv.Itoa(port) + "\n"))
		stream.SetWriteDeadline(time.Time{})
		if err != nil {
			stream.Close()
			continue
		}
		return helper.NewStreamConn(stream, ms.carrier), true
	}
	return nil, false
}

// takePassthrough gives the visitor to a session whose client ends TLS itself. The
// visitor's TLS is forwarded without being read. It returns false when no such
// session is connected.
func (hub *NetworkHub) takePassthrough(visitor *helper.RemoteConn, port int) bool {
	conn, ok := hub.openStream("LIPSTICK-PASS", port, true)
	if !ok {
		return false
	}
	go hub.syncConnections(visitor, conn)
	return true
}

// closeMuxes closes every session. Their visitors see the connection end.
func (hub *NetworkHub) closeMuxes() {
	hub.muxMu.Lock()
	sessions := hub.muxes
	hub.muxes = nil
	hub.muxMu.Unlock()
	for _, ms := range sessions {
		ms.session.Close()
	}
}
