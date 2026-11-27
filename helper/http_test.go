package helper

import (
	"io"
	"net"
	"strings"
	"testing"
)

func feed(t *testing.T, data string) net.Conn {
	t.Helper()
	server, client := net.Pipe()
	go func() {
		client.Write([]byte(data))
		client.Close()
	}()
	t.Cleanup(func() { server.Close() })
	return server
}

func TestReadHeadersAcceptsBothLineEndings(t *testing.T) {
	for _, in := range []string{"GET / HTTP/1.1\r\nHost: a\r\n\r\n", "GET / HTTP/1.1\nHost: a\n\n", "GET / HTTP/1.1\r\nHost: a\n\r\n"} {
		conn := feed(t, in)
		got, err := ReadUntilHeadersEnd(conn)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if string(got) != in {
			t.Fatalf("%q: got %q", in, got)
		}
	}
}

// The body must stay on the connection. A buffered reader used to read past the
// headers and lose the start of the body.
func TestBodyIsNotConsumed(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	go func() {
		client.Write([]byte("POST / HTTP/1.1\r\nContent-Length: 5\r\n\r\nhello"))
		client.Close()
	}()
	if _, err := ReadUntilHeadersEnd(server); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(server)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "hello" {
		t.Fatalf("body after headers = %q, want %q", rest, "hello")
	}
}

func TestHeadersLimitIsEnforced(t *testing.T) {
	conn := feed(t, "GET / HTTP/1.1\r\n"+strings.Repeat("X-Pad: aaaaaaaaaaaaaaaaaaaaaaaa\r\n", 4000))
	if _, err := ReadUntilHeadersEnd(conn); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("oversized headers accepted: %v", err)
	}
}

func TestTruncatedHeadersAreAnError(t *testing.T) {
	conn := feed(t, "GET / HTTP/1.1\r\nHost: a\r\n")
	if _, err := ReadUntilHeadersEnd(conn); err == nil {
		t.Fatal("headers without an end accepted")
	}
}
