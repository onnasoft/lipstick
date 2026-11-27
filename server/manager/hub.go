package manager

import (
	"net"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/subscriptions"
	"github.com/OnnaSoft/lipstick/server/traffic"
	"github.com/nats-io/nats.go"
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

	// natsQueueSize bounds the messages waiting for the hub loop.
	natsQueueSize = 64

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
	natsMessages                    chan string
	trafficManager                  *traffic.TrafficManager
	dataUsageAccumulator            int64
	threshold                       int64
	mu                              sync.Mutex
	totalDataTransferred            int64
	tickerManager                   *TickerManager
	subscription                    *nats.Subscription

	// done is closed to stop the hub. Every channel send from outside selects
	// on done, so no sender can block on a hub that has stopped.
	done     chan struct{}
	stopOnce sync.Once

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
		natsMessages:                    make(chan string, natsQueueSize),
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
	defer pipe.Close()
	defer destination.Close()

	var originToDest int64
	toTunnel := make(chan struct{})

	// visitor -> tunnel
	go func() {
		defer close(toTunnel)
		buffer := make([]byte, 4096)
		for {
			n, err := pipe.Read(buffer)
			if n > 0 {
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

	// tunnel -> visitor
	var destToOrigin int64
	buffer := make([]byte, 4096)
	for {
		n, err := destination.Read(buffer)
		if n > 0 {
			written, werr := pipe.Write(buffer[:n])
			destToOrigin += int64(written)
			hub.addDataUsage(int64(written))
			if werr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}

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
		case msg := <-hub.natsMessages:
			hub.handleNatsMessage(msg)
		case now := <-expire.C:
			hub.expirePendingVisitors(now)
		case <-hub.done:
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

	if hub.subscription == nil {
		mgr, err := subscriptions.GetSubscriptionManager()
		if err != nil {
			logger.Default.Error("Error getting SubscriptionManager:", err)
			conn.Close()
			return
		}

		// The callback runs on the NATS goroutine. It must not touch the hub's
		// maps, so it only queues the message for the hub loop.
		sub, err := mgr.Subscribe(hub.HubName, func(message *nats.Msg) {
			select {
			case hub.natsMessages <- string(message.Data):
			default:
				logger.Default.Error("NATS message dropped, hub queue is full:", hub.HubName)
			}
		})
		if err != nil {
			logger.Default.Error("Error subscribing to NATS:", err)
			conn.Close()
			return
		}
		hub.subscription = sub
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
	if hub.subscription != nil {
		hub.subscription.Unsubscribe()
		hub.subscription = nil
		logger.Default.Debug("Unsubscribed from NATS for hub:", hub.HubName)
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
	if len(hub.ProxyNotificationConns) == 0 {
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

	// The client only needs the ticket. The reverse connection goes to the
	// server URL it already has, so no address is sent.
	ws := hub.getProxyNotificationConn()
	if _, err := ws.Write([]byte(ticket + "\n")); err != nil {
		logger.Default.Error("Error writing ticket to ProxyNotificationConn:", err)
		rejectVisitor(visitor)
		return
	}

	hub.incomingClientConns[ticket] = &pendingVisitor{conn: visitor, since: time.Now()}
	logger.Default.Debug("Ticket sent to ProxyNotificationConn for hub:", hub.HubName)
}

// handleNatsMessage forwards a ticket that arrived over NATS to a tunnel client.
// If no client can take it, the visitor it belongs to gets 502.
func (hub *NetworkHub) handleNatsMessage(ticket string) {
	ws := hub.getProxyNotificationConn()
	if ws == nil {
		logger.Default.Error("No ProxyNotificationConns available for hub:", hub.HubName)
		hub.rejectPendingTicket(ticket)
		return
	}

	if _, err := ws.Write([]byte(ticket + "\n")); err != nil {
		logger.Default.Error("Error writing NATS ticket to ProxyNotificationConn:", err)
		hub.rejectPendingTicket(ticket)
	}
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
	for ws := range hub.ProxyNotificationConns {
		delete(hub.ProxyNotificationConns, ws)
		ws.Close()
	}
	for ticket, visitor := range hub.incomingClientConns {
		delete(hub.incomingClientConns, ticket)
		rejectVisitor(visitor.conn)
	}
	if hub.subscription != nil {
		hub.subscription.Unsubscribe()
		hub.subscription = nil
	}
	logger.Default.Info("Hub stopped:", hub.HubName)
}

// checkConnection reads from a tunnel client until it fails or stays silent for
// clientIdleTimeout. The client sends a ping every 30 seconds, so a silent
// client has died without closing its socket.
func (h *NetworkHub) checkConnection(connection *ProxyNotificationConn) {
	defer func() {
		h.unregisterClient(connection)
		logger.Default.Info("Connection closed for ProxyNotificationConn in hub:", h.HubName)
	}()
	buf := make([]byte, 16)
	for {
		connection.conn.SetReadDeadline(time.Now().Add(clientIdleTimeout))
		if _, err := connection.conn.Read(buf); err != nil {
			logger.Default.Debug("Tunnel client stopped responding or disconnected:", err)
			return
		}
	}
}
