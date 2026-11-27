package manager

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/server/traffic"
)

// tcpPair returns two connected TCP sockets.
func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		done <- c
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-done
	t.Cleanup(func() { client.Close(); server.Close() })
	return server, client
}

func testHub() *NetworkHub {
	// The threshold is never reached in these tests, so no traffic manager is
	// needed.
	return NewNetworkHub("localhost", (*traffic.TrafficManager)(nil), 1<<40)
}

// The visitor half-closes after sending. The client must see end-of-file, reply,
// and the visitor must receive the whole reply before the session ends.
func TestRelayPassesEndOfFileBothWays(t *testing.T) {
	visitorSrv, visitorCli := tcpPair(t)
	tunnelSrv, tunnelCli := tcpPair(t)
	payload := bytes.Repeat([]byte("abc"), 5000)
	reply := []byte("the whole reply")

	finished := make(chan struct{})
	go func() {
		testHub().syncConnections(visitorSrv, tunnelSrv)
		close(finished)
	}()

	// The simulated client reads until end-of-file, then replies and half-closes.
	clientDone := make(chan []byte, 1)
	go func() {
		got, _ := io.ReadAll(tunnelCli)
		tunnelCli.Write(reply)
		tunnelCli.(*net.TCPConn).CloseWrite()
		clientDone <- got
	}()

	visitorCli.Write(payload)
	visitorCli.(*net.TCPConn).CloseWrite()
	gotReply, err := io.ReadAll(visitorCli)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-clientDone:
		if !bytes.Equal(got, payload) {
			t.Fatalf("client received %d bytes, want %d", len(got), len(payload))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client never saw end-of-file")
	}
	if !bytes.Equal(gotReply, reply) {
		t.Fatalf("visitor received %q, want %q", gotReply, reply)
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end")
	}
}

// The visitor closes completely. The session must end, not stay open waiting
// for the client, which was the leak.
func TestRelayEndsWhenVisitorCloses(t *testing.T) {
	visitorSrv, visitorCli := tcpPair(t)
	tunnelSrv, tunnelCli := tcpPair(t)

	finished := make(chan struct{})
	go func() {
		testHub().syncConnections(visitorSrv, tunnelSrv)
		close(finished)
	}()

	clientSawEOF := make(chan struct{})
	go func() {
		io.ReadAll(tunnelCli)
		tunnelCli.Close()
		close(clientSawEOF)
	}()

	visitorCli.Write([]byte("hello"))
	visitorCli.Close()

	select {
	case <-clientSawEOF:
	case <-time.After(5 * time.Second):
		t.Fatal("client never saw the visitor close")
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("session stayed open after the visitor closed (leak)")
	}
}
