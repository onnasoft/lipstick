package manager

import (
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
	record, err := manager.authManager.GetDomain(domain)
	if err != nil {
		rejectVisitor(visitor)
		return
	}
	nonce, err := helper.NewMuxNonce(time.Now())
	if err != nil {
		rejectVisitor(visitor)
		return
	}

	tlsConf := c.tls.Clone()
	if host, _, err := net.SplitHostPort(peer); err == nil {
		tlsConf.ServerName = host
	}
	peerConn, err := tls.DialWithDialer(&net.Dialer{Timeout: relayDialTimeout}, "tcp", peer, tlsConf)
	if err != nil {
		logger.Default.Error("Cannot relay to peer", peer, "for domain", domain, ":", err)
		rejectVisitor(visitor)
		return
	}
	defer peerConn.Close()

	request := fmt.Sprintf("GET /relay HTTP/1.1\r\nHost: %s\r\n%s: %s\r\n%s: %s\r\n%s: %d\r\n\r\n",
		domain,
		helper.RelayNonceHeader, nonce,
		helper.ProofHeader, helper.RelayProof(record.ApiKey, nonce),
		helper.RelayPortHeader, port)
	peerConn.SetWriteDeadline(time.Now().Add(relayWriteWait))
	if _, err := peerConn.Write([]byte(request)); err != nil {
		rejectVisitor(visitor)
		return
	}
	peerConn.SetWriteDeadline(time.Time{})

	// The visitor's own request follows the headers. Bytes flow both ways until
	// both sides have finished.
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(peerConn, visitor)
		helper.CloseWrite(peerConn)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(visitor, peerConn)
		helper.CloseWrite(visitor)
		done <- struct{}{}
	}()
	<-done
	<-done
	visitor.Close()
}

// handleRelay receives a visitor relayed by another server. It checks the proof and
// the nonce, then gives the visitor to a session of this server. It never relays
// again: a visitor without a session here gets 502.
func (manager *Manager) handleRelay(conn net.Conn, req *http.Request) {
	domainName := strings.Split(req.Host, ":")[0]
	nonce := req.Header.Get(helper.RelayNonceHeader)
	now := time.Now()

	if _, ok := helper.MuxNonceTime(nonce, now); !ok {
		logger.Default.Error("Relay with an expired or malformed nonce for domain:", domainName)
		conn.Close()
		return
	}
	record, err := manager.authManager.GetDomain(domainName)
	if err != nil || !helper.VerifyRelayProof(record.ApiKey, nonce, req.Header.Get(helper.ProofHeader)) {
		logger.Default.Error("Relay without a valid proof for domain:", domainName)
		conn.Close()
		return
	}
	if !manager.nonces.take(nonce, now) {
		logger.Default.Error("Relay with a replayed nonce for domain:", domainName)
		conn.Close()
		return
	}

	port, err := strconv.Atoi(req.Header.Get(helper.RelayPortHeader))
	if err != nil || port < 0 || port > 65535 {
		port = 0
	}
	visitor := &helper.RemoteConn{Conn: conn, Domain: domainName}
	hub, ok := manager.GetHub(domainName)
	if !ok || !hub.takeMux(visitor, port) {
		rejectVisitor(visitor)
	}
}

// muxCount returns how many sessions the hub holds.
func (hub *NetworkHub) muxCount() int {
	hub.muxMu.Lock()
	defer hub.muxMu.Unlock()
	return len(hub.muxes)
}
