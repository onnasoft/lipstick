package manager

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
)

// readAll reads everything the peer sends until it closes, or fails the test.
func readAll(t *testing.T, c net.Conn) string {
	t.Helper()
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(c)
		done <- string(b)
	}()
	select {
	case s := <-done:
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("visitor was not answered and closed in time")
		return ""
	}
}

func visitorPair(t *testing.T) (*helper.RemoteConn, net.Conn) {
	t.Helper()
	server, client := net.Pipe()
	return &helper.RemoteConn{Conn: server, Domain: "localhost"}, client
}

// A visitor that arrives with no tunnel client gets 502 at once. The old code
// published to NATS and left the visitor waiting.
func TestVisitorWithoutClientGets502(t *testing.T) {
	hub := NewNetworkHub("localhost", nil, 0)
	visitor, peer := visitorPair(t)
	go hub.handleIncomingClientConn(visitor)

	if got := readAll(t, peer); !strings.Contains(got, "502") {
		t.Fatalf("visitor got %q, want 502", got)
	}
	if len(hub.incomingClientConns) != 0 {
		t.Fatal("rejected visitor left in the pending map")
	}
}

// A pending visitor whose client never answers gets 502 after the timeout.
func TestPendingVisitorExpires(t *testing.T) {
	hub := NewNetworkHub("localhost", nil, 0)
	visitor, peer := visitorPair(t)
	hub.incomingClientConns["stale"] = &pendingVisitor{
		conn:  visitor,
		since: time.Now().Add(-pendingVisitorTimeout - time.Second),
	}
	hub.expirePendingVisitors(time.Now())

	if got := readAll(t, peer); !strings.Contains(got, "502") {
		t.Fatalf("expired visitor got %q, want 502", got)
	}
	if _, ok := hub.incomingClientConns["stale"]; ok {
		t.Fatal("expired visitor still pending")
	}
}

// A visitor that is still within the timeout must keep waiting.
func TestFreshPendingVisitorIsKept(t *testing.T) {
	hub := NewNetworkHub("localhost", nil, 0)
	visitor, _ := visitorPair(t)
	hub.incomingClientConns["fresh"] = &pendingVisitor{conn: visitor, since: time.Now()}
	hub.expirePendingVisitors(time.Now())
	if _, ok := hub.incomingClientConns["fresh"]; !ok {
		t.Fatal("fresh visitor was expired early")
	}
}

// The pending map is capped: a flood gets 502 instead of growing without limit.
func TestPendingVisitorsAreCapped(t *testing.T) {
	hub := NewNetworkHub("localhost", nil, 0)

	// A connected tunnel client, so the cap is what rejects the visitor.
	clientSide, clientPeer := net.Pipe()
	defer clientPeer.Close()
	go io.Copy(io.Discard, clientPeer)
	hub.ProxyNotificationConns[&ProxyNotificationConn{
		conn:       clientSide,
		ReadWriter: bufio.NewReadWriter(bufio.NewReader(clientSide), bufio.NewWriter(clientSide)),
	}] = true

	for i := 0; i < maxPendingVisitors; i++ {
		hub.incomingClientConns[string(rune('a'+i%26))+string(rune(i))] = &pendingVisitor{since: time.Now()}
	}
	visitor, peer := visitorPair(t)
	go hub.handleIncomingClientConn(visitor)

	if got := readAll(t, peer); !strings.Contains(got, "502") {
		t.Fatalf("visitor beyond the cap got %q, want 502", got)
	}
}
