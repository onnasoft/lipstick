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

// answerTo feeds raw to the request reader and returns what the client gets back.
func answerTo(t *testing.T, raw []byte) string {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	l := &ListenerManager{onHTTPConn: func(net.Conn, *http.Request) { t.Error("an invalid request reached the handler") }}
	done := make(chan error, 1)
	go func() {
		err := l.handleHTTPConn(NewConnWithBuffer(server, nil))
		server.Close()
		done <- err
	}()
	go func() { client.Write(raw) }()
	got := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(client)
		got <- string(b)
	}()
	select {
	case answer := <-got:
		<-done
		return answer
	case <-time.After(3 * time.Second):
		t.Fatal("no answer")
		return ""
	}
}

func TestInvalidRequestsGetTheirAnswer(t *testing.T) {
	cases := map[string]struct {
		raw  []byte
		want string
	}{
		"no Host":             {[]byte("GET /x HTTP/1.1\r\n\r\n"), "HTTP/1.1 400 Bad Request"},
		"Content-Length + TE": {[]byte("POST /x HTTP/1.1\r\nHost: a\r\nContent-Length: 5\r\nTransfer-Encoding: chunked\r\n\r\n"), "HTTP/1.1 400 Bad Request"},
		"headers over 64 KiB": {append([]byte("GET /x HTTP/1.1\r\nHost: a\r\nX-Big: "), append(bytesOf('a', 70000), []byte("\r\n\r\n")...)...), "HTTP/1.1 431 Request Header Fields Too Large"},
	}
	for name, c := range cases {
		if got := answerTo(t, c.raw); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: %q, want prefix %q", name, got, c.want)
		}
	}
}

func bytesOf(c byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return b
}
