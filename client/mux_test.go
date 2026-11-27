package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

func TestParseAssignment(t *testing.T) {
	port, err := parseAssignment("LIPSTICK-PORT 5105\n")
	if err != nil || port != 5105 {
		t.Fatalf("got port %d, %v", port, err)
	}
	for _, line := range []string{"HTTP/1.1 401 Unauthorized\n", "LIPSTICK-PORT\n", "LIPSTICK-PORT 70000\n", "LIPSTICK-PORT -1\n", "LIPSTICK-PORT x\n", ""} {
		if _, err := parseAssignment(line); err == nil {
			t.Errorf("line %q accepted", line)
		}
	}
}

// A visitor stream is served end to end: the server opens a stream, writes the
// port line and the request, and the client forwards the request to the local
// service and returns the response on the same stream.
func TestMuxStreamServesVisitor(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("from backend " + r.URL.Path))
	}))
	defer backend.Close()

	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	serverSession, err := yamux.Server(serverSide, muxClientConfig())
	if err != nil {
		t.Fatal(err)
	}
	clientSession, err := yamux.Client(clientSide, muxClientConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	go func() {
		stream, err := clientSession.AcceptStream()
		if err != nil {
			return
		}
		serveMuxStream(stream, clientSide, strings.TrimPrefix(backend.URL, "http://"))
	}()

	stream, err := serverSession.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	stream.Write([]byte("LIPSTICK-PORT 5105\nGET /hello HTTP/1.1\r\nHost: a\r\nConnection: close\r\n\r\n"))
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(stream), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "from backend /hello" {
		t.Fatalf("body %q", body)
	}
}
