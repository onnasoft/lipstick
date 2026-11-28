package manager

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/OnnaSoft/lipstick/server/traffic"
	"github.com/gin-gonic/gin"
)

type request struct {
	conn   net.Conn
	ticket string
}

type ProxyNotificationConn struct {
	Domain                   string
	AllowMultipleConnections bool
	*bufio.ReadWriter
	conn net.Conn
}

// notificationWriteTimeout bounds a write to a tunnel client. A client that
// stopped reading must not block the hub loop, which is the only writer.
const notificationWriteTimeout = 5 * time.Second

func (p *ProxyNotificationConn) Write(b []byte) (int, error) {
	p.conn.SetWriteDeadline(time.Now().Add(notificationWriteTimeout))
	n, err := p.ReadWriter.Write(b)
	if err != nil {
		logger.Default.Error("Error writing to ReadWriter:", err)
		return n, err
	}
	err = p.Flush()
	if err != nil {
		logger.Default.Error("Error flushing ReadWriter:", err)
		return n, err
	}
	return n, nil
}

func (p *ProxyNotificationConn) Close() error {
	p.conn.SetWriteDeadline(time.Now().Add(notificationWriteTimeout))
	_, err := p.conn.Write([]byte("close"))
	if err != nil {
		logger.Default.Debug("Error writing 'close' to connection:", err)
	}
	closeErr := p.conn.Close()
	if closeErr != nil {
		logger.Default.Error("Error closing connection:", closeErr)
	}
	if err != nil {
		return err
	}
	return closeErr
}

type Manager struct {
	engine         *gin.Engine
	hubs           sync.Map
	trafficManager *traffic.TrafficManager
	authManager    auth.AuthManager
	tlsConfig      *tls.Config
	intranet       *intranetPorts
	reqlog         *RequestLog
	persist        *persister
	nonces         nonceCache
	cluster        *cluster       // nil on a single server
	failures       failureLimiter // failed visitor logins, see guard.go
}

// requestLogSize is how many finished requests the admin panel keeps in memory.
const requestLogSize = 1000

func SetupManager(tlsConfig *tls.Config) *Manager {
	gin.SetMode(gin.ReleaseMode)

	manager := &Manager{
		hubs:           sync.Map{},
		authManager:    auth.MakeAuthManager(),
		trafficManager: traffic.NewTrafficManager(64 * 1024),
		tlsConfig:      tlsConfig,
		reqlog:         NewRequestLog(requestLogSize),
	}

	configureRouter(manager)

	logger.Default.Info("Manager setup completed")

	return manager
}

// getOrCreateHub returns the hub of a domain, starting its loop when it does not
// exist yet. Two clients connecting at the same time get the same hub.
func (m *Manager) getOrCreateHub(domain string) *NetworkHub {
	if value, ok := m.hubs.Load(domain); ok {
		return value.(*NetworkHub)
	}

	hub := NewNetworkHub(domain, m.trafficManager, 64*1024)
	hub.route = m.routeVisitor
	hub.requests = m.reqlog
	// When the hub stops for lack of clients, forget it. CompareAndDelete leaves
	// a newer hub for the same domain alone.
	hub.onIdle = func() {
		m.hubs.CompareAndDelete(domain, hub)
		m.releaseIntranetPorts(domain)
	}

	actual, loaded := m.hubs.LoadOrStore(domain, hub)
	if loaded {
		return actual.(*NetworkHub)
	}
	go hub.listen()
	logger.Default.Info("New hub created for domain:", domain)
	return hub
}

func (m *Manager) GetHub(domain string) (*NetworkHub, bool) {
	value, ok := m.hubs.Load(domain)
	if !ok {
		logger.Default.Debug("hub not found for domain:", domain)
		return nil, false
	}
	return value.(*NetworkHub), true
}

// DropHub stops the hub of a domain and disconnects its clients. Called when
// the domain is deleted or its key changes. The clients must reconnect, and
// they are then checked against the current configuration.
func (m *Manager) DropHub(domain string) {
	if value, ok := m.hubs.LoadAndDelete(domain); ok {
		logger.Default.Info("Dropping hub for domain:", domain)
		m.releaseIntranetPorts(domain)
		value.(*NetworkHub).stop()
	}
}

// Close stops every hub and flushes the pending traffic counters. Called on
// shutdown.
func (m *Manager) Close() {
	m.releaseAllIntranetPorts()
	m.hubs.Range(func(key, value any) bool {
		m.hubs.Delete(key)
		value.(*NetworkHub).stop()
		return true
	})
	if m.persist != nil {
		m.persist.Close()
	}
	m.trafficManager.Close()
}

func (manager *Manager) handleTunnel(conn net.Conn, req *http.Request, ticket string) {
	host := req.Host
	domainName := strings.Split(host, ":")[0]

	// The ticket alone does not open a tunnel. The client must also prove it
	// knows the domain's key, for every reverse connection it opens.
	domainRecord, err := manager.authManager.GetDomain(domainName)
	if err != nil || !helper.VerifyTunnelProof(domainRecord.ApiKey, ticket, req.Header.Get(helper.ProofHeader)) {
		// Close without a reply: any bytes sent here would reach the client as
		// visitor data, and it would forward them to the local service.
		logger.Default.Error("Reverse connection without a valid proof for domain:", domainName)
		conn.Close()
		return
	}

	domain, ok := manager.GetHub(domainName)
	if !ok {
		logger.Default.Error("hub not found for domain:", domainName)
		conn.Close()
		return
	}

	logger.Default.Debug("Handling tunnel for domain:", domainName)
	if !domain.submitTunnel(&request{ticket: ticket, conn: conn}) {
		logger.Default.Error("hub stopped before the tunnel connection arrived:", domainName)
		conn.Close()
	}
}

func (manager *Manager) Listen() {
	var err error
	config, configErr := config.GetConfig()
	if configErr != nil {
		logger.Default.Error("Error getting config:", configErr)
		return
	}

	logger.Default.Info("Listening manager on ", config.Manager.Address)

	if manager.tlsConfig == nil {
		log.Fatal("manager has no TLS configuration; TLS is required")
	}
	listener, err := tls.Listen("tcp", config.Manager.Address, manager.tlsConfig)
	if err != nil {
		log.Fatalf("manager cannot listen on %s: %v (check manager.address in the configuration)", config.Manager.Address, err)
	}

	if engineErr := manager.ServeListener(listener); engineErr != nil {
		log.Fatalf("manager stopped serving: %v", engineErr)
	}
}

// ServeListener serves the manager API and tunnel connections on an already open
// listener. Listen uses it, and the integration tests use it too.
func (manager *Manager) ServeListener(listener net.Listener) error {
	return manager.engine.RunListener(NewCustomListener(listener, manager))
}

func (manager *Manager) HandleHTTPConn(conn net.Conn, req *http.Request) {
	host := req.Host
	domain := strings.Split(host, ":")[0]

	// A restricted domain checks every request on the connection, so it has its own path.
	if rec, err := manager.authManager.GetDomain(domain); err == nil && rec.AuthLatched {
		manager.serveRestricted(domain, conn)
		return
	}

	hub, ok := manager.GetHub(domain)
	if !ok && manager.cluster != nil {
		// This server has no tunnel for the domain. Another server of the cluster may.
		manager.routeVisitor(domain, &helper.RemoteConn{Conn: conn, Domain: domain}, localPort(conn))
		return
	}
	if !ok {
		logger.Default.Error("Hub not found for domain:", domain)
		_, err := fmt.Fprint(conn, helper.BadGatewayResponse)
		if err != nil {
			logger.Default.Error("Error writing BadGatewayResponse to HTTP connection:", err)
		}
		conn.Close()
		return
	}

	logger.Default.Debug("Handling HTTP connection for domain:", domain)
	if remoteConn, ok := conn.(*helper.RemoteConn); ok {
		if !hub.submitVisitor(remoteConn) {
			rejectVisitor(remoteConn)
		}
		return
	}

	remoteConn := &helper.RemoteConn{Conn: conn, Domain: domain}
	if !hub.submitVisitor(remoteConn) {
		rejectVisitor(remoteConn)
	}
}

func (manager *Manager) HandleTCPConn(conn net.Conn) {
	domain, err := helper.GetDomainName(conn)
	if err != nil {
		logger.Default.Error("Error getting domain name from TCP connection:", err)
		_, writeErr := fmt.Fprint(conn, helper.BadGatewayResponse)
		if writeErr != nil {
			logger.Default.Error("Error writing BadGatewayResponse to TCP connection:", writeErr)
		}
		conn.Close()
		return
	}

	if rec, err := manager.authManager.GetDomain(domain); err == nil && rec.AuthLatched {
		// Only HTTP can be checked. Other traffic to a restricted domain is refused.
		writeStatus(conn, 403, "")
		return
	}

	hub, ok := manager.GetHub(domain)
	if !ok && manager.cluster != nil {
		// This server has no tunnel for the domain. Another server of the cluster may.
		manager.routeVisitor(domain, &helper.RemoteConn{Conn: conn, Domain: domain}, localPort(conn))
		return
	}
	if !ok {
		logger.Default.Error("Hub not found for domain:", domain)
		_, err := fmt.Fprint(conn, helper.BadGatewayResponse)
		if err != nil {
			logger.Default.Error("Error writing BadGatewayResponse to TCP connection:", err)
		}
		conn.Close()
		return
	}

	logger.Default.Debug("Handling TCP connection for domain:", domain)
	if remoteConn, ok := conn.(*helper.RemoteConn); ok {
		if !hub.submitVisitor(remoteConn) {
			rejectVisitor(remoteConn)
		}
		return
	}

	remoteConn := &helper.RemoteConn{Conn: conn, Domain: domain}
	if !hub.submitVisitor(remoteConn) {
		rejectVisitor(remoteConn)
	}
}

// RoutePassthrough takes a TLS connection of a domain with passthrough on, and
// forwards it to a client whose service ends TLS itself. The server does not read
// the TLS. It returns false when the domain or such a client is not available, and
// the connection is then ended here as usual. prefix holds the bytes already read.
func (manager *Manager) RoutePassthrough(domain string, conn net.Conn, prefix []byte) bool {
	record, err := manager.authManager.GetDomain(domain)
	if err != nil || !record.Passthrough || record.AuthLatched {
		return false
	}
	hub, ok := manager.GetHub(domain)
	if !ok {
		return false
	}
	visitor := &helper.RemoteConn{Conn: helper.NewConnWithBuffer(conn, prefix), Domain: domain}
	return hub.takePassthrough(visitor, localPort(conn))
}
