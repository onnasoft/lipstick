package manager

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/hashicorp/yamux"
)

// A visitor takes a stream of a live session: the client gets the port line, then
// the visitor's bytes, and the answer goes back the same way.
func TestTakeMuxRelaysVisitorOverStream(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 1<<40)

	serverCarrier, clientCarrier := net.Pipe()
	defer clientCarrier.Close()
	serverSession, err := yamux.Server(serverCarrier, muxConfig())
	if err != nil {
		t.Fatal(err)
	}
	clientSession, err := yamux.Client(clientCarrier, muxConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	if !hub.addMux(&muxSession{session: serverSession, carrier: serverCarrier}) {
		t.Fatal("session not added")
	}

	visitorServer, visitorClient := net.Pipe()
	defer visitorClient.Close()
	visitor := &helper.RemoteConn{Conn: visitorServer, Domain: "app.example.com"}

	taken := make(chan bool, 1)
	go func() { taken <- hub.takeMux(visitor, 5105) }()

	stream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	stream.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(stream).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "LIPSTICK-PORT ") {
		t.Fatalf("first line %q, %v", line, err)
	}
	if !<-taken {
		t.Fatal("visitor not taken by the session")
	}

	go visitorClient.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	buf := make([]byte, 64)
	n, err := stream.Read(buf)
	if err != nil || string(buf[:n]) != "GET / HTTP/1.1\r\n\r\n" {
		t.Fatalf("visitor bytes %q, %v", buf[:n], err)
	}
}

// A session that is closed is removed, so no visitor is sent to it.
func TestClosedSessionIsNotUsed(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 1<<40)
	serverCarrier, clientCarrier := net.Pipe()
	defer clientCarrier.Close()
	serverSession, _ := yamux.Server(serverCarrier, muxConfig())
	ms := &muxSession{session: serverSession, carrier: serverCarrier}
	hub.addMux(ms)
	serverSession.Close()
	hub.removeMux(ms)

	visitorServer, visitorClient := net.Pipe()
	defer visitorClient.Close()
	visitor := &helper.RemoteConn{Conn: visitorServer, Domain: "app.example.com"}
	if hub.takeMux(visitor, 5105) {
		t.Fatal("visitor sent to a closed session")
	}
}

// Once the hub has stopped, no session is added.
func TestAddMuxRefusedAfterHubStops(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 1<<40)
	close(hub.done)
	serverCarrier, clientCarrier := net.Pipe()
	defer clientCarrier.Close()
	serverSession, _ := yamux.Server(serverCarrier, muxConfig())
	defer serverSession.Close()
	if hub.addMux(&muxSession{session: serverSession, carrier: serverCarrier}) {
		t.Fatal("session added to a stopped hub")
	}
}

// A nonce is accepted once. Using it again is a replay and is refused.
func TestNonceCacheRefusesReplay(t *testing.T) {
	var c nonceCache // zero value must work
	now := time.Now()
	if !c.take("aaa", now) {
		t.Fatal("first use refused")
	}
	if c.take("aaa", now) {
		t.Fatal("replayed nonce accepted")
	}
	if !c.take("bbb", now) {
		t.Fatal("different nonce refused")
	}
}
