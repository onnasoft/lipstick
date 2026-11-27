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

// A datagram from an allowed source reaches the client as a framed flow on a
// stream, and the client's reply goes back to that source.
func TestUDPFlowRoundTrip(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 1<<40)
	clientCarrier, serverCarrier := net.Pipe()
	defer clientCarrier.Close()
	serverSession, _ := yamux.Server(serverCarrier, muxConfig())
	clientSession, _ := yamux.Client(clientCarrier, muxConfig())
	defer clientSession.Close()
	hub.addMux(&muxSession{session: serverSession, carrier: serverCarrier})

	m := &Manager{}
	m.hubs.Store("app.example.com", hub)

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go m.serveIntranetUDP(pc, "app.example.com", []*net.IPNet{mustCIDR(t, "127.0.0.0/8")})

	sender, err := net.DialUDP("udp", nil, pc.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	sender.Write([]byte("ping"))

	stream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	stream.SetReadDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(stream)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "LIPSTICK-UDP ") {
		t.Fatalf("first line %q, %v", line, err)
	}
	got, err := helper.ReadFrame(reader)
	if err != nil || string(got) != "ping" {
		t.Fatalf("client got %q, %v; want ping", got, err)
	}
	if err := helper.WriteFrame(stream, []byte("pong")); err != nil {
		t.Fatal(err)
	}

	sender.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := sender.Read(buf)
	if err != nil || string(buf[:n]) != "pong" {
		t.Fatalf("source got %q, %v; want pong", buf[:n], err)
	}
}

// A datagram from outside the allowed networks opens no flow.
func TestUDPSourceOutsidePolicyOpensNoFlow(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 1<<40)
	clientCarrier, serverCarrier := net.Pipe()
	defer clientCarrier.Close()
	serverSession, _ := yamux.Server(serverCarrier, muxConfig())
	clientSession, _ := yamux.Client(clientCarrier, muxConfig())
	defer clientSession.Close()
	hub.addMux(&muxSession{session: serverSession, carrier: serverCarrier})
	m := &Manager{}
	m.hubs.Store("app.example.com", hub)

	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer pc.Close()
	go m.serveIntranetUDP(pc, "app.example.com", []*net.IPNet{mustCIDR(t, "10.0.0.0/8")})

	sender, _ := net.DialUDP("udp", nil, pc.LocalAddr().(*net.UDPAddr))
	defer sender.Close()
	sender.Write([]byte("ping"))

	accepted := make(chan struct{}, 1)
	go func() {
		if _, err := clientSession.AcceptStream(); err == nil {
			accepted <- struct{}{}
		}
	}()
	select {
	case <-accepted:
		t.Fatal("a flow was opened for a source outside the policy")
	case <-time.After(500 * time.Millisecond):
	}
}

// Addresses of both kinds are checked against the networks.
func TestSourceAllowedAcceptsUDPAddresses(t *testing.T) {
	allowed := []*net.IPNet{mustCIDR(t, "192.168.40.0/24")}
	if !sourceAllowed(&net.UDPAddr{IP: net.ParseIP("192.168.40.7"), Port: 1}, allowed) {
		t.Fatal("UDP address inside the network refused")
	}
	if sourceAllowed(&net.UDPAddr{IP: net.ParseIP("10.1.2.3"), Port: 1}, allowed) {
		t.Fatal("UDP address outside the network accepted")
	}
}

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
