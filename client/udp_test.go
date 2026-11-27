package main

import (
	"net"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
)

// A flow reaches a local UDP service and carries the service's reply back, framed.
func TestServeUDPFlowRelaysToLocalService(t *testing.T) {
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := echo.ReadFrom(buf)
			if err != nil {
				return
			}
			echo.WriteTo(append([]byte("echo:"), buf[:n]...), addr)
		}
	}()

	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	go serveUDPFlow(clientSide, echo.LocalAddr().String(), false)

	if err := helper.WriteFrame(serverSide, []byte("dns?")); err != nil {
		t.Fatal(err)
	}
	serverSide.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, err := helper.ReadFrame(serverSide)
	if err != nil || string(got) != "echo:dns?" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// A flow with no UDP target does nothing and closes its stream.
func TestServeUDPFlowWithoutTargetCloses(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	done := make(chan struct{})
	go func() { serveUDPFlow(clientSide, "", false); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("flow without target did not return")
	}
}
