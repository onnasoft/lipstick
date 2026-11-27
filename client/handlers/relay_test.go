package handlers

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

// HandleTCP must let a service finish its reply after the tunnel side is done,
// and must then tell the tunnel side it is finished.
func TestHandleTCPCompletesReplyAfterEndOfFile(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	reply := []byte("reply after the request ended")
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		got, _ := io.ReadAll(c) // waits for end-of-file from the client side
		_ = got
		c.Write(reply)
		c.(*net.TCPConn).CloseWrite()
	}()

	tunnelSrv, tunnelCli := tcpPair(t)
	done := make(chan struct{})
	go func() {
		HandleTCP(tunnelSrv, ln.Addr().String(), "tcp")
		close(done)
	}()

	payload := []byte("request body")
	tunnelCli.Write(payload)
	tunnelCli.(*net.TCPConn).CloseWrite()
	got, err := io.ReadAll(tunnelCli)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, reply) {
		t.Fatalf("tunnel received %q, want %q", got, reply)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("HandleTCP did not finish")
	}
}

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
