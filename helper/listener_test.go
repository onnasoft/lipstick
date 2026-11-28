package helper

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A request whose Content-Length is not a number gets 400, not silence.
func TestMalformedContentLengthGets400(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	l := &ListenerManager{onHTTPConn: func(net.Conn, *http.Request) { t.Error("a malformed request reached the handler") }}
	done := make(chan error, 1)
	go func() {
		err := l.handleHTTPConn(NewConnWithBuffer(server, nil))
		server.Close() // the caller closes a connection it could not read, as handleConnection does
		done <- err
	}()

	go client.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\nContent-Length: abc\r\n\r\n"))
	got := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(client)
		got <- string(b)
	}()
	select {
	case answer := <-got:
		if !strings.HasPrefix(answer, "HTTP/1.1 400 Bad Request") || !strings.Contains(answer, "Connection: close") {
			t.Fatalf("answer: %q", answer)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no answer to a malformed request")
	}
	if err := <-done; err == nil {
		t.Fatal("malformed request reported as read")
	}
}
