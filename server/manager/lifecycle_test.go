package manager

import (
	"bufio"
	"net"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
)

// The hub stops when its last client leaves, and tells the manager to forget it.
func TestHubStopsWhenLastClientLeaves(t *testing.T) {
	hub := NewNetworkHub("localhost", nil, 0)
	idle := make(chan struct{})
	hub.onIdle = func() { close(idle) }
	go hub.listen()

	clientSide, clientPeer := net.Pipe()
	defer clientPeer.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			if _, err := clientPeer.Read(buf); err != nil {
				return
			}
		}
	}()
	ws := &ProxyNotificationConn{conn: clientSide, ReadWriter: newRW(clientSide)}
	hub.ProxyNotificationConns[ws] = true

	hub.unregisterClient(ws)

	select {
	case <-idle:
	case <-time.After(2 * time.Second):
		t.Fatal("manager was not told the hub is idle")
	}
	select {
	case <-hub.done:
	case <-time.After(2 * time.Second):
		t.Fatal("idle hub did not stop")
	}
}

// Dropping a domain stops its hub. A later visitor is refused instead of blocking.
func TestDropHubRefusesLaterSenders(t *testing.T) {
	m := &Manager{}
	hub := m.getOrCreateHub("localhost")
	m.DropHub("localhost")

	server, client := net.Pipe()
	defer client.Close()
	if hub.submitVisitor(&helper.RemoteConn{Conn: server}) {
		t.Fatal("stopped hub accepted a visitor")
	}
	if _, ok := m.GetHub("localhost"); ok {
		t.Fatal("dropped hub is still registered")
	}
}

// getOrCreateHub must give concurrent callers the same hub.
func TestGetOrCreateHubIsSingleton(t *testing.T) {
	m := &Manager{}
	first := m.getOrCreateHub("localhost")
	second := m.getOrCreateHub("localhost")
	if first != second {
		t.Fatal("two hubs created for one domain")
	}
	m.DropHub("localhost")
}

// A client that stops sending pings is removed after the idle timeout, instead
// of staying registered until a write fails.
func TestSilentClientIsDetected(t *testing.T) {
	old := clientIdleTimeout
	clientIdleTimeout = 200 * time.Millisecond
	defer func() { clientIdleTimeout = old }()

	hub := NewNetworkHub("localhost", nil, 0)
	go hub.listen()
	defer hub.stop()

	clientSide, clientPeer := net.Pipe()
	defer clientPeer.Close()
	ws := &ProxyNotificationConn{conn: clientSide, ReadWriter: newRW(clientSide)}
	hub.ProxyNotificationConns[ws] = true

	finished := make(chan struct{})
	go func() {
		hub.checkConnection(ws)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("silent client was never detected")
	}
}

func newRW(c net.Conn) *bufio.ReadWriter {
	return bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))
}
