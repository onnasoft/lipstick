package manager

import (
	"net"
	"testing"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/hashicorp/yamux"
)

// A passthrough visitor goes only to a session whose client ends TLS itself. A
// session without the capability never receives one.
func TestPassthroughUsesOnlyCapableSessions(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 1<<40)

	plainCarrier, plainServer := net.Pipe()
	defer plainCarrier.Close()
	plainSession, _ := yamux.Server(plainServer, muxConfig())
	hub.addMux(&muxSession{session: plainSession, carrier: plainServer, passthrough: false})

	visitorServer, visitorClient := net.Pipe()
	defer visitorClient.Close()
	visitor := &helper.RemoteConn{Conn: visitorServer, Domain: "app.example.com"}
	if hub.takePassthrough(visitor, 5050) {
		t.Fatal("passthrough visitor given to a session that ends TLS on the server side")
	}

	capCarrier, capServer := net.Pipe()
	defer capCarrier.Close()
	capSession, _ := yamux.Server(capServer, muxConfig())
	hub.addMux(&muxSession{session: capSession, carrier: capServer, passthrough: true})

	// The capable session is the only one that can take it now.
	clientSide, _ := yamux.Client(capCarrier, muxConfig())
	defer clientSide.Close()
	visitorServer2, visitorClient2 := net.Pipe()
	defer visitorClient2.Close()
	visitor2 := &helper.RemoteConn{Conn: visitorServer2, Domain: "app.example.com"}
	if !hub.takePassthrough(visitor2, 5050) {
		t.Fatal("capable session not used for passthrough")
	}
}
