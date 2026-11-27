package manager

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/OnnaSoft/lipstick/server/subscriptions"
	"github.com/hashicorp/yamux"
	"github.com/nats-io/nats.go"
)

// Cluster routing. Servers that share a database, NATS and a certificate can serve
// the same domains. Each server publishes, on NATS, the domains it holds tunnel
// sessions for. A visitor that reaches a server with no session for its domain is
// relayed to a server that has one. The relay is a TLS connection to that server's
// manager port, authenticated with a proof made with the domain key.
const (
	presenceSubject  = "lipstick.presence"
	presenceEvery    = 5 * time.Second
	presenceTTL      = 15 * time.Second
	relayDialTimeout = 5 * time.Second
	relayWriteWait   = 5 * time.Second
)

// presenceMsg says that server holds a number of sessions for domain.
type presenceMsg struct {
	Server   string `json:"server"`
	Domain   string `json:"domain"`
	Sessions int    `json:"sessions"`
}

// cluster is the state of this server in its cluster.
type cluster struct {
	advertise string      // host:port of this server's manager, as the others reach it
	tls       *tls.Config // trust for dialing the other servers

	mu    sync.Mutex
	peers map[string]map[string]time.Time // domain -> server -> last presence

	relayMu sync.Mutex
	relays  map[string]*relaySession // peer|domain -> session
}

// observe records a presence message. Messages from this server are ignored, and a
// server that reports no sessions is forgotten.
func (c *cluster) observe(m presenceMsg, now time.Time) {
	if m.Server == "" || m.Domain == "" || m.Server == c.advertise {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if m.Sessions <= 0 {
		delete(c.peers[m.Domain], m.Server)
		return
	}
	if c.peers[m.Domain] == nil {
		c.peers[m.Domain] = make(map[string]time.Time)
	}
	c.peers[m.Domain][m.Server] = now
}

// peerFor returns the other server that most recently reported sessions for
// domain. Entries not renewed within presenceTTL are dropped.
func (c *cluster) peerFor(domain string, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var best string
	var bestSeen time.Time
	for server, seen := range c.peers[domain] {
		if now.Sub(seen) > presenceTTL {
			delete(c.peers[domain], server)
			continue
		}
		if best == "" || seen.After(bestSeen) {
			best, bestSeen = server, seen
		}
	}
	return best, best != ""
}

// StartCluster joins the cluster. It needs NATS, which the configuration requires
// for a cluster. The certificate of this server is trusted for the other servers
// unless cluster.ca_file names their CA.
func (manager *Manager) StartCluster(conf config.ClusterConfig, tlsConfig *tls.Config) error {
	roots, err := clusterRoots(conf.CAFile, tlsConfig)
	if err != nil {
		return err
	}
	mgr, err := subscriptions.GetSubscriptionManager()
	if err != nil {
		return fmt.Errorf("cluster needs NATS: %w", err)
	}
	c := &cluster{
		advertise: conf.AdvertiseAddress,
		tls:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		peers:     make(map[string]map[string]time.Time),
		relays:    make(map[string]*relaySession),
	}
	if _, err := mgr.SubscribeBroadcast(presenceSubject, func(msg *nats.Msg) {
		var m presenceMsg
		if err := json.Unmarshal(msg.Data, &m); err == nil {
			c.observe(m, time.Now())
		}
	}); err != nil {
		return fmt.Errorf("cluster cannot subscribe to presence: %w", err)
	}
	manager.cluster = c
	go manager.publishPresence(mgr)
	logger.Default.Info("Cluster joined as", conf.AdvertiseAddress)
	return nil
}

// clusterRoots returns the certificates trusted for the other servers.
func clusterRoots(caFile string, tlsConfig *tls.Config) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read cluster CA %q: %w", caFile, err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("cluster CA %q has no PEM certificates", caFile)
		}
		return pool, nil
	}
	if tlsConfig == nil || len(tlsConfig.Certificates) == 0 || len(tlsConfig.Certificates[0].Certificate) == 0 {
		return nil, fmt.Errorf("cluster needs a certificate or cluster.ca_file")
	}
	leaf, err := x509.ParseCertificate(tlsConfig.Certificates[0].Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("cannot read this server's certificate: %w", err)
	}
	pool.AddCert(leaf)
	return pool, nil
}

// publishPresence announces, every presenceEvery, the domains with sessions here.
func (manager *Manager) publishPresence(mgr *subscriptions.SubscriptionManager) {
	ticker := time.NewTicker(presenceEvery)
	defer ticker.Stop()
	for range ticker.C {
		manager.hubs.Range(func(key, value any) bool {
			hub := value.(*NetworkHub)
			n := hub.muxCount()
			if n == 0 {
				return true
			}
			data, err := json.Marshal(presenceMsg{Server: manager.cluster.advertise, Domain: key.(string), Sessions: n})
			if err == nil {
				err = mgr.Publish(presenceSubject, data)
			}
			if err != nil {
				logger.Default.Error("Cannot publish presence:", err)
			}
			return true
		})
	}
}

// relaySession is a persistent session from this server to one peer, for one
// domain. The peer accepts streams on it; each stream is one visitor.
type relaySession struct {
	session *yamux.Session
	carrier net.Conn
}

// relayTimeout bounds the header line and the first write on a relayed stream.
const relayTimeout = 5 * time.Second

// routeVisitor relays a visitor to another server of the cluster. It does not block:
// the relay runs in its own goroutine, so the hub loop is never held. Without a
// server that has a session for the domain, the visitor gets 502.
func (manager *Manager) routeVisitor(domain string, visitor *helper.RemoteConn, port int) {
	go manager.relayVisitor(domain, visitor, port)
}

func (manager *Manager) relayVisitor(domain string, visitor *helper.RemoteConn, port int) {
	c := manager.cluster
	if c == nil {
		rejectVisitor(visitor)
		return
	}
	peer, ok := c.peerFor(domain, time.Now())
	if !ok {
		logger.Default.Error("No server of the cluster has a tunnel for domain:", domain)
		rejectVisitor(visitor)
		return
	}
	// The key is read only when a session must be opened, not for every visitor.
	apiKey := func() (string, error) {
		record, err := manager.authManager.GetDomain(domain)
		if err != nil {
			return "", err
		}
		return record.ApiKey, nil
	}
	rs, err := c.relayFor(peer, domain, apiKey)
	if err != nil {
		logger.Default.Error("Cannot relay to peer", peer, "for domain", domain, ":", err)
		rejectVisitor(visitor)
		return
	}

	stream, err := rs.session.OpenStream()
	if err != nil {
		c.dropRelay(peer, domain, rs)
		rejectVisitor(visitor)
		return
	}
	stream.SetWriteDeadline(time.Now().Add(relayTimeout))
	_, err = stream.Write([]byte("LIPSTICK-RELAY " + strconv.Itoa(port) + "\n"))
	stream.SetWriteDeadline(time.Time{})
	if err != nil {
		stream.Close()
		c.dropRelay(peer, domain, rs)
		rejectVisitor(visitor)
		return
	}
	relayBothWays(visitor, helper.NewStreamConn(stream, rs.carrier))
}

// relayFor returns the session to peer for domain, opening it if there is none.
func (c *cluster) relayFor(peer, domain string, apiKey func() (string, error)) (*relaySession, error) {
	key := peer + "|" + domain
	c.relayMu.Lock()
	current := c.relays[key]
	c.relayMu.Unlock()
	if current != nil && !current.session.IsClosed() {
		return current, nil
	}

	k, err := apiKey()
	if err != nil {
		return nil, err
	}
	fresh, err := dialRelay(c.tls, peer, domain, k)
	if err != nil {
		return nil, err
	}

	c.relayMu.Lock()
	defer c.relayMu.Unlock()
	if cur := c.relays[key]; cur != nil && !cur.session.IsClosed() {
		// Another visit opened one meanwhile. Keep that one.
		fresh.session.Close()
		return cur, nil
	}
	c.relays[key] = fresh
	return fresh, nil
}

// dropRelay forgets a session that failed, so the next visit opens a new one.
func (c *cluster) dropRelay(peer, domain string, rs *relaySession) {
	key := peer + "|" + domain
	c.relayMu.Lock()
	if c.relays[key] == rs {
		delete(c.relays, key)
	}
	c.relayMu.Unlock()
	rs.session.Close()
}

// dialRelay opens the authenticated relay session to a peer. The peer checks the
// proof once, when the session opens.
func dialRelay(base *tls.Config, peer, domain, apiKey string) (*relaySession, error) {
	nonce, err := helper.NewMuxNonce(time.Now())
	if err != nil {
		return nil, err
	}
	tlsConf := base.Clone()
	if host, _, err := net.SplitHostPort(peer); err == nil {
		tlsConf.ServerName = host
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: relayDialTimeout}, "tcp", peer, tlsConf)
	if err != nil {
		return nil, err
	}
	request := fmt.Sprintf("GET /relay HTTP/1.1\r\nHost: %s\r\n%s: %s\r\n%s: %s\r\n\r\n",
		domain,
		helper.RelayNonceHeader, nonce,
		helper.ProofHeader, helper.RelayProof(apiKey, nonce))
	conn.SetWriteDeadline(time.Now().Add(relayWriteWait))
	if _, err := conn.Write([]byte(request)); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetWriteDeadline(time.Time{})
	session, err := yamux.Client(conn, muxConfig())
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &relaySession{session: session, carrier: conn}, nil
}

// relayBothWays copies bytes both ways until both sides have finished.
func relayBothWays(visitor net.Conn, peer net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(peer, visitor)
		helper.CloseWrite(peer)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(visitor, peer)
		helper.CloseWrite(visitor)
		done <- struct{}{}
	}()
	<-done
	<-done
	visitor.Close()
	peer.Close()
}

// handleRelay opens a relay session from a peer. It checks the proof and the nonce
// once. The peer's streams then arrive as visitors, served by serveRelayStreams.
func (manager *Manager) handleRelay(conn net.Conn, req *http.Request) {
	domainName := strings.Split(req.Host, ":")[0]
	nonce := req.Header.Get(helper.RelayNonceHeader)
	now := time.Now()

	if _, ok := helper.MuxNonceTime(nonce, now); !ok {
		logger.Default.Error("Relay session with an expired or malformed nonce for domain:", domainName)
		conn.Close()
		return
	}
	record, err := manager.authManager.GetDomain(domainName)
	if err != nil || !helper.VerifyRelayProof(record.ApiKey, nonce, req.Header.Get(helper.ProofHeader)) {
		logger.Default.Error("Relay session without a valid proof for domain:", domainName)
		conn.Close()
		return
	}
	if !manager.nonces.take(nonce, now) {
		logger.Default.Error("Relay session with a replayed nonce for domain:", domainName)
		conn.Close()
		return
	}
	hub, ok := manager.GetHub(domainName)
	if !ok {
		conn.Close()
		return
	}
	session, err := yamux.Server(conn, muxConfig())
	if err != nil {
		conn.Close()
		return
	}
	go hub.serveRelayStreams(session, conn, domainName)
}

// serveRelayStreams takes the visitors a peer relays over one session.
func (hub *NetworkHub) serveRelayStreams(session *yamux.Session, carrier net.Conn, domain string) {
	defer session.Close()
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			return
		}
		go hub.serveRelayStream(stream, carrier, domain)
	}
}

// serveRelayStream gives one relayed visitor to a client session of this hub. The
// first line carries the port the visitor came in on. A visitor that no session
// can take gets 502 back through the relay.
func (hub *NetworkHub) serveRelayStream(stream *yamux.Stream, carrier net.Conn, domain string) {
	stream.SetReadDeadline(time.Now().Add(relayTimeout))
	reader := bufio.NewReader(stream)
	line, err := reader.ReadString('\n')
	if err != nil {
		stream.Close()
		return
	}
	stream.SetReadDeadline(time.Time{})
	port, err := parseRelayLine(line)
	if err != nil {
		logger.Default.Error("Bad relay line:", err)
		stream.Close()
		return
	}
	pending, _ := reader.Peek(reader.Buffered())
	wrapped := helper.NewConnWithBuffer(helper.NewStreamConn(stream, carrier), append([]byte(nil), pending...))
	visitor := &helper.RemoteConn{Conn: wrapped, Domain: domain}
	if !hub.takeMux(visitor, port) {
		rejectVisitor(visitor)
	}
}

// parseRelayLine reads "LIPSTICK-RELAY <port>". The port must be a valid number.
func parseRelayLine(line string) (int, error) {
	line = strings.TrimRight(line, "\r\n")
	rest, ok := strings.CutPrefix(line, "LIPSTICK-RELAY ")
	if !ok {
		return 0, fmt.Errorf("unexpected relay line %q", line)
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 || n > 65535 {
		return 0, fmt.Errorf("invalid relay port %q", rest)
	}
	return n, nil
}

// muxCount returns how many sessions the hub holds.
func (hub *NetworkHub) muxCount() int {
	hub.muxMu.Lock()
	defer hub.muxMu.Unlock()
	return len(hub.muxes)
}
