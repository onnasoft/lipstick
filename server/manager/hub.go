package manager

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/OnnaSoft/lipstick/server/limits"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/traffic"
)

const (
	// pendingVisitorTimeout is how long a visitor waits for its tunnel client to
	// open the reverse connection. After that the visitor gets 502.
	pendingVisitorTimeout = 30 * time.Second

	// maxPendingVisitors caps the visitors waiting on one hub, so a flood of
	// requests cannot exhaust memory or file descriptors.
	maxPendingVisitors = 1024

	// expireInterval is how often the hub checks for timed-out visitors.
	expireInterval = 5 * time.Second
	// usageFlushInterval bounds how long bytes wait in memory before they reach the
	// daily total. Without it, a day of small requests never reaches the threshold.
	usageFlushInterval = 5 * time.Second

	// rejectWriteTimeout bounds how long a 502 may block on a dead socket.
	rejectWriteTimeout = 5 * time.Second
)

var rng = helper.NewXORShift(uint32(time.Now().UnixNano()))

// clientIdleTimeout is how long a tunnel client may stay silent. Clients ping
// every 30 seconds, so a longer silence means the client is gone. It is a
// variable so tests can shorten it.
var clientIdleTimeout = 90 * time.Second

// pendingVisitor is a visitor connection waiting for its tunnel client.
type pendingVisitor struct {
	conn  net.Conn
	since time.Time
	port  int // local port the visitor came in on: the proxy or an intranet port
}

// localPort returns the port a connection arrived on, or 0 when unknown.
func localPort(c net.Conn) int {
	if a, ok := c.LocalAddr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// NetworkHub owns the state of one domain. Its maps are read and written only
// by the goroutine running listen(). Other goroutines send it messages over
// channels, so no locks are needed around the maps.
type NetworkHub struct {
	HubName                         string
	incomingClientConns             map[string]*pendingVisitor
	ProxyNotificationConns          map[*ProxyNotificationConn]bool
	registerProxyNotificationConn   chan *ProxyNotificationConn
	unregisterProxyNotificationConn chan *ProxyNotificationConn
	incomingClientConn              chan *helper.RemoteConn
	serverRequests                  chan *request
	trafficManager                  *traffic.TrafficManager
	dataUsageAccumulator            int64
	pacer                           *limits.Pacer // bandwidth of the domain's visitor data, see limits.Pacer
	threshold                       int64
	mu                              sync.Mutex
	totalDataTransferred            int64
	tickerManager                   *TickerManager

	// done is closed to stop the hub. Every channel send from outside selects
	// on done, so no sender can block on a hub that has stopped.
	done     chan struct{}
	stopOnce sync.Once

	// statsReq asks the loop for a snapshot. The loop owns the maps, so the
	// snapshot is taken there, never from another goroutine.
	statsReq chan chan HubStats

	// activeSessions counts relays in progress (visitor and tunnel joined).
	activeSessions atomic.Int64

	// route hands a visitor to another server of the cluster when this server has
	// no tunnel client for the domain. It is nil on a single server. See cluster.go.
	route func(domain string, visitor *helper.RemoteConn, port int)

	// muxes are the authenticated sessions of tunnel clients. See mux.go.
	muxMu   sync.Mutex
	muxes   []*muxSession
	muxNext int

	// requests receives the requests that clients report.
	requests *RequestLog

	// onIdle runs in the hub loop when the last client leaves. The manager uses
	// it to forget the hub.
	onIdle func()
}

func NewNetworkHub(name string, trafficManager *traffic.TrafficManager, threshold int64) *NetworkHub {
	return &NetworkHub{
		HubName:                         name,
		incomingClientConns:             make(map[string]*pendingVisitor),
		ProxyNotificationConns:          make(map[*ProxyNotificationConn]bool),
		registerProxyNotificationConn:   make(chan *ProxyNotificationConn),
		unregisterProxyNotificationConn: make(chan *ProxyNotificationConn),
		incomingClientConn:              make(chan *helper.RemoteConn),
		serverRequests:                  make(chan *request),
		statsReq:                        make(chan chan HubStats),
		trafficManager:                  trafficManager,
		dataUsageAccumulator:            0,
		threshold:                       threshold,
		tickerManager:                   &TickerManager{},
		done:                            make(chan struct{}),
	}
}

// stop asks the hub loop to close its clients and exit. It is safe to call more
// than once and never blocks.
func (hub *NetworkHub) stop() {
	hub.stopOnce.Do(func() { close(hub.done) })
}

// Senders outside the hub loop use these helpers. Each returns false when the
// hub has stopped, so the caller can close its connection instead of blocking.

func (hub *NetworkHub) registerClient(ws *ProxyNotificationConn) bool {
	select {
	case hub.registerProxyNotificationConn <- ws:
		return true
	case <-hub.done:
		return false
	}
}

func (hub *NetworkHub) unregisterClient(ws *ProxyNotificationConn) {
	select {
	case hub.unregisterProxyNotificationConn <- ws:
	case <-hub.done:
	}
}

func (hub *NetworkHub) submitVisitor(visitor *helper.RemoteConn) bool {
	select {
	case hub.incomingClientConn <- visitor:
		return true
	case <-hub.done:
		return false
	}
}

func (hub *NetworkHub) submitTunnel(req *request) bool {
	select {
	case hub.serverRequests <- req:
		return true
	case <-hub.done:
		return false
	}
}

// relayDrainTimeout bounds how long a relay waits for the visitor after the
// tunnel side has finished, so an idle visitor cannot keep the session forever.
const relayDrainTimeout = 10 * time.Second

// syncConnections relays bytes between a visitor (pipe) and the tunnel
// connection (destination) until both directions have finished. A direction
// that ends is passed on as end-of-file, so the other side can still finish its
// reply. Without this, a visitor that closes would leave the session open
// forever, and a client that waits for end-of-file would never finish.
func (hub *NetworkHub) syncConnections(pipe net.Conn, destination net.Conn) {
	hub.activeSessions.Add(1)
	defer hub.activeSessions.Add(-1)
	defer pipe.Close()
	defer destination.Close()

	var originToDest int64
	toTunnel := make(chan struct{})

	// requestAt is when the visitor last sent data; answered is set once the backend has
	// sent a reply to it. A request that gets no reply within backendResponseTimeout is
	// answered with 504 by the reply loop below.
	var requestAt atomic.Int64
	var answered atomic.Bool

	// visitor -> tunnel
	go func() {
		defer close(toTunnel)
		buffer := make([]byte, 4096)
		for {
			n, err := pipe.Read(buffer)
			if n > 0 {
				requestAt.Store(time.Now().UnixNano())
				answered.Store(false)
				hub.pace(n)
				written, werr := destination.Write(buffer[:n])
				originToDest += int64(written)
				hub.addDataUsage(int64(written))
				if werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		// The visitor will send no more: tell the client.
		helper.CloseWrite(destination)
	}()

	// tunnel -> visitor. The read wakes every backendPollInterval to check whether the
	// backend has answered the visitor's last request in time.
	var destToOrigin int64
	buffer := make([]byte, 4096)
	for {
		destination.SetReadDeadline(time.Now().Add(backendPollInterval))
		n, err := destination.Read(buffer)
		if n > 0 {
			answered.Store(true)
			hub.pace(n)
			written, werr := pipe.Write(buffer[:n])
			destToOrigin += int64(written)
			hub.addDataUsage(int64(written))
			if werr != nil {
				break
			}
		}
		if err != nil {
			if isTimeout(err) && !answered.Load() && time.Since(time.Unix(0, requestAt.Load())) > backendResponseTimeout {
				logger.Default.Info("backend did not answer within", backendResponseTimeout, "for hub:", hub.HubName)
				backendTimeouts.Add(1)
				fmt.Fprint(pipe, helper.GatewayTimeoutResponse)
				break
			}
			if isTimeout(err) {
				continue
			}
			break
		}
	}
	destination.SetReadDeadline(time.Time{})

	// The client finished its reply: tell the visitor, and give it a bounded
	// time to finish sending before the session is closed.
	helper.CloseWrite(pipe)
	pipe.SetReadDeadline(time.Now().Add(relayDrainTimeout))
	<-toTunnel

	logger.Default.Debug("Connection data usage:",
		"origin → destination:", originToDest, "bytes,",
		"destination → origin:", destToOrigin, "bytes",
		"Hub:", hub.HubName,
	)
}

// flushUsage hands the bytes waiting in memory to the daily total.
func (hub *NetworkHub) flushUsage() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.dataUsageAccumulator > 0 {
		hub.trafficManager.AddTraffic(hub.HubName, hub.dataUsageAccumulator)
		hub.dataUsageAccumulator = 0
	}
}

// pace makes a write of n bytes wait as long as the domain's bandwidth requires.
func (hub *NetworkHub) pace(n int) {
	if d := hub.pacer.Reserve(n); d > 0 {
		time.Sleep(d)
	}
}

func (hub *NetworkHub) addDataUsage(bytes int64) {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	hub.dataUsageAccumulator += bytes
	hub.totalDataTransferred += bytes

	if hub.dataUsageAccumulator >= hub.threshold {
		hub.trafficManager.AddTraffic(hub.HubName, hub.dataUsageAccumulator)
		hub.dataUsageAccumulator = 0
	}
	logger.Default.Debug("Data usage updated for hub:", hub.HubName, "Total transferred:", hub.totalDataTransferred)
}

func (hub *NetworkHub) listen() {
	expire := time.NewTicker(expireInterval)
	defer expire.Stop()
	flush := time.NewTicker(usageFlushInterval)
	defer flush.Stop()

	for {
		select {
		case conn := <-hub.registerProxyNotificationConn:
			hub.handleRegisterProxyNotificationConn(conn)
		case ws := <-hub.unregisterProxyNotificationConn:
			hub.handleUnregisterProxyNotificationConn(ws)
		case request := <-hub.serverRequests:
			hub.handleServerRequest(request)
		case remoteConn := <-hub.incomingClientConn:
			hub.handleIncomingClientConn(remoteConn)
		case reply := <-hub.statsReq:
			reply <- hub.snapshot()
		case now := <-expire.C:
			hub.expirePendingVisitors(now)
		case <-flush.C:
			hub.flushUsage()
		case <-hub.done:
			hub.flushUsage()
			hub.handleShutdown()
			return
		}
	}
}

func (hub *NetworkHub) handleRegisterProxyNotificationConn(conn *ProxyNotificationConn) {
	if !conn.AllowMultipleConnections && len(hub.ProxyNotificationConns) > 0 {
		logger.Default.Error("Connection rejected: multiple connections not allowed for hub:", hub.HubName)
		conn.Close()
		return
	}

	hub.ProxyNotificationConns[conn] = true
	logger.Default.Debug("ProxyNotificationConn registered for hub:", hub.HubName)
	go hub.checkConnection(conn)
}

func (hub *NetworkHub) handleUnregisterProxyNotificationConn(ws *ProxyNotificationConn) {
	if _, exists := hub.ProxyNotificationConns[ws]; exists {
		delete(hub.ProxyNotificationConns, ws)
		logger.Default.Debug("ProxyNotificationConn unregistered for hub:", hub.HubName)
	}
	ws.Close()

	if len(hub.ProxyNotificationConns) > 0 {
		return
	}

	// Visitors waiting for a ticket can only be served by a client of this hub.
	// When the last client is gone, nothing will serve them, so they get 502 now.
	for ticket, visitor := range hub.incomingClientConns {
		delete(hub.incomingClientConns, ticket)
		rejectVisitor(visitor.conn)
	}

	// No clients left: the hub has nothing to do, so it stops and the manager
	// forgets it. A later client creates a new hub.
	logger.Default.Info("No tunnel clients left, stopping hub:", hub.HubName)
	if hub.onIdle != nil {
		hub.onIdle()
	}
	hub.stop()
}

func (hub *NetworkHub) handleServerRequest(request *request) {
	pending, exists := hub.incomingClientConns[request.ticket]
	if !exists {
		logger.Default.Error("Invalid ticket for hub:", hub.HubName)
		rejectVisitor(request.conn)
		return
	}
	delete(hub.incomingClientConns, request.ticket)
	logger.Default.Debug("Server request handled for hub:", hub.HubName)
	go hub.syncConnections(pending.conn, request.conn)
}

func (hub *NetworkHub) handleIncomingClientConn(visitor *helper.RemoteConn) {
	port := localPort(visitor)
	// A mux session is the fast path: a stream, no ticket and no new connection.
	if hub.takeMux(visitor, port) {
		return
	}

	if len(hub.ProxyNotificationConns) == 0 {
		// No tunnel client here. In a cluster, another server may hold one.
		if hub.route != nil {
			hub.route(hub.HubName, visitor, port)
			return
		}
		logger.Default.Error("No tunnel client connected for hub:", hub.HubName)
		rejectVisitor(visitor)
		return
	}

	if len(hub.incomingClientConns) >= maxPendingVisitors {
		logger.Default.Error("Too many pending visitors for hub:", hub.HubName)
		rejectVisitor(visitor)
		return
	}

	ticket, err := hub.tickerManager.generate()
	if err != nil {
		logger.Default.Error("Error generating ticket for hub:", hub.HubName, err)
		rejectVisitor(visitor)
		return
	}

	// The line is "ticket port". The client returns the port with each request it
	// reports, so the request log can tell the proxy apart from intranet ports.
	ws := hub.getProxyNotificationConn()
	if _, err := ws.Write([]byte(ticket + " " + strconv.Itoa(port) + "\n")); err != nil {
		logger.Default.Error("Error writing ticket to ProxyNotificationConn:", err)
		rejectVisitor(visitor)
		return
	}

	hub.incomingClientConns[ticket] = &pendingVisitor{conn: visitor, since: time.Now(), port: port}
	logger.Default.Debug("Ticket sent to ProxyNotificationConn for hub:", hub.HubName)
}

// rejectPendingTicket answers the visitor waiting on ticket with 502, if there
// is one.
func (hub *NetworkHub) rejectPendingTicket(ticket string) {
	visitor, ok := hub.incomingClientConns[ticket]
	if !ok {
		return
	}
	delete(hub.incomingClientConns, ticket)
	rejectVisitor(visitor.conn)
}

// expirePendingVisitors answers with 502 every visitor that has waited longer
// than pendingVisitorTimeout.
func (hub *NetworkHub) expirePendingVisitors(now time.Time) {
	for ticket, visitor := range hub.incomingClientConns {
		if now.Sub(visitor.since) < pendingVisitorTimeout {
			continue
		}
		delete(hub.incomingClientConns, ticket)
		logger.Default.Error("Visitor timed out waiting for a tunnel client, hub:", hub.HubName)
		rejectVisitor(visitor.conn)
	}
}

// getProxyNotificationConn picks a client at random. It is called only from the
// hub loop.
func (hub *NetworkHub) getProxyNotificationConn() *ProxyNotificationConn {
	conns := make([]*ProxyNotificationConn, 0, len(hub.ProxyNotificationConns))
	for key := range hub.ProxyNotificationConns {
		conns = append(conns, key)
	}

	if len(conns) == 0 {
		return nil
	}

	if len(conns) == 1 {
		return conns[0]
	}

	return conns[int(rng.Next()%uint32(len(conns)))]
}

// rejectVisitor answers a visitor with 502 and closes its connection. It runs in
// its own goroutine, so a slow or dead visitor cannot block the hub loop. The
// connection is already removed from the hub's maps when this is called.
func rejectVisitor(conn net.Conn) {
	go func() {
		conn.SetWriteDeadline(time.Now().Add(rejectWriteTimeout))
		_, _ = conn.Write([]byte(helper.BadGatewayResponse))
		conn.Close()
	}()
}

// handleShutdown runs in the hub loop when the hub is stopped. It disconnects
// every client and answers every waiting visitor.
func (hub *NetworkHub) handleShutdown() {
	hub.closeMuxes()
	for ws := range hub.ProxyNotificationConns {
		delete(hub.ProxyNotificationConns, ws)
		ws.Close()
	}
	for ticket, visitor := range hub.incomingClientConns {
		delete(hub.incomingClientConns, ticket)
		rejectVisitor(visitor.conn)
	}
	logger.Default.Info("Hub stopped:", hub.HubName)
}

// maxClientLine bounds one line from a client. A longer line closes the
// connection instead of growing memory.
const maxClientLine = 8192

// checkConnection reads the lines a tunnel client sends: "ping", and "log {json}"
// for each finished request. A client that stays silent for clientIdleTimeout is
// dropped, and so is a client that sends a line that is too long.
func (h *NetworkHub) checkConnection(connection *ProxyNotificationConn) {
	defer func() {
		h.unregisterClient(connection)
		logger.Default.Info("Connection closed for ProxyNotificationConn in hub:", h.HubName)
	}()
	reader := bufio.NewReaderSize(connection.conn, maxClientLine)
	for {
		connection.conn.SetReadDeadline(time.Now().Add(clientIdleTimeout))
		line, err := reader.ReadSlice('\n')
		if err != nil {
			if err == bufio.ErrBufferFull {
				logger.Default.Error("Client line too long, dropping client of hub:", h.HubName)
			} else {
				logger.Default.Debug("Tunnel client stopped responding or disconnected:", err)
			}
			return
		}
		h.handleClientLine(strings.TrimSpace(string(line)))
	}
}

// handleClientLine applies one line from a tunnel client. Unknown lines are
// ignored, so newer clients can add messages without breaking older servers.
func (h *NetworkHub) handleClientLine(line string) {
	const prefix = "log "
	if !strings.HasPrefix(line, prefix) || h.requests == nil {
		return
	}
	var rec RequestRecord
	if err := json.Unmarshal([]byte(line[len(prefix):]), &rec); err != nil {
		logger.Default.Error("Invalid request report from client of hub:", h.HubName)
		return
	}
	rec.Time = time.Now()
	rec.Domain = h.HubName
	h.requests.Add(rec)
}

// HubStats is a point-in-time view of one domain's hub.
type HubStats struct {
	Domain          string
	Clients         int
	PendingVisitors int
	ActiveSessions  int64
	BytesSinceStart int64
}

// snapshot runs in the hub loop, the only goroutine that reads the hub's maps.
func (hub *NetworkHub) snapshot() HubStats {
	hub.mu.Lock()
	total := hub.totalDataTransferred
	hub.mu.Unlock()
	return HubStats{
		Domain:          hub.HubName,
		Clients:         len(hub.ProxyNotificationConns),
		PendingVisitors: len(hub.incomingClientConns),
		ActiveSessions:  hub.activeSessions.Load(),
		BytesSinceStart: total,
	}
}

// Stats asks the hub loop for a snapshot. It gives up after timeout, so a hub
// that is stuck cannot hang the admin panel.
func (hub *NetworkHub) Stats(timeout time.Duration) (HubStats, bool) {
	reply := make(chan HubStats, 1)
	select {
	case hub.statsReq <- reply:
	case <-hub.done:
		return HubStats{}, false
	case <-time.After(timeout):
		return HubStats{}, false
	}
	select {
	case st := <-reply:
		return st, true
	case <-time.After(timeout):
		return HubStats{}, false
	}
}

// Backend response timing. A visitor request that gets no reply within backendResponseTimeout
// is answered with 504. Once a reply has started, nothing is cut: streams and long downloads
// run as long as they send. The poll interval bounds how late the 504 can be.
var (
	backendResponseTimeout = 60 * time.Second
	backendPollInterval    = time.Second
)

// isTimeout reports whether err is a read deadline passing, not a broken connection.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
