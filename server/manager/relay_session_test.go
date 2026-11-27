package manager

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

func TestParseRelayLine(t *testing.T) {
	if port, err := parseRelayLine("LIPSTICK-RELAY 5050\n"); err != nil || port != 5050 {
		t.Fatalf("got %d, %v", port, err)
	}
	for _, line := range []string{"LIPSTICK-RELAY\n", "LIPSTICK-RELAY 70000\n", "GET / HTTP/1.1\n", ""} {
		if _, err := parseRelayLine(line); err == nil {
			t.Errorf("line %q accepted", line)
		}
	}
}

// relayPair connects a peer's relay session to a hub, over in-memory pipes. It
// returns the peer's side, which opens streams for relayed visitors.
func relayPair(t *testing.T, hub *NetworkHub) *yamux.Session {
	t.Helper()
	peerSide, hubSide := net.Pipe()
	peerSession, err := yamux.Client(peerSide, muxConfig())
	if err != nil {
		t.Fatal(err)
	}
	hubSession, err := yamux.Server(hubSide, muxConfig())
	if err != nil {
		t.Fatal(err)
	}
	go hub.serveRelayStreams(hubSession, hubSide, "app.example.com")
	return peerSession
}

// A visitor relayed over a session reaches a client session of the hub, with the
// port it came in on, and the answer comes back the same way.
func TestRelayedVisitorReachesClientSession(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 1<<40)

	clientCarrier, serverCarrier := net.Pipe()
	defer clientCarrier.Close()
	serverSession, _ := yamux.Server(serverCarrier, muxConfig())
	clientSession, _ := yamux.Client(clientCarrier, muxConfig())
	defer clientSession.Close()
	hub.addMux(&muxSession{session: serverSession, carrier: serverCarrier})

	peer := relayPair(t, hub)
	stream, err := peer.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	stream.Write([]byte("LIPSTICK-RELAY 5050\nGET /x HTTP/1.1\r\nHost: a\r\n\r\n"))

	clientStream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	clientStream.SetReadDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(clientStream)
	line, err := reader.ReadString('\n')
	if err != nil || line != "LIPSTICK-PORT 5050\n" {
		t.Fatalf("client got %q, %v; want the original port", line, err)
	}
	head, _ := reader.ReadString('\n')
	if head != "GET /x HTTP/1.1\r\n" {
		t.Fatalf("visitor bytes lost: %q", head)
	}
}

// A relayed visitor that no session here can take gets 502 back over the relay.
func TestRelayedVisitorWithoutSessionGetsBadGateway(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 1<<40)
	peer := relayPair(t, hub)
	stream, err := peer.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	stream.Write([]byte("LIPSTICK-RELAY 5050\n"))
	stream.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, err := io.ReadAll(stream)
	if err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "502") {
		t.Fatalf("got %q, want 502 back over the relay", got)
	}
}
