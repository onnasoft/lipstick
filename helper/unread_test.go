package helper

import (
	"io"
	"net"
	"testing"
)

// Headers and body arriving in one read must reach the tunnel whole. Before the
// fix, the body was dropped and the visitor's request was corrupted.
func TestUnreadKeepsBodyThatArrivedWithHeaders(t *testing.T) {
	request := "POST /echo HTTP/1.1\r\nHost: localhost\r\nContent-Length: 3\r\n\r\nabc"
	raw, peer := net.Pipe()
	defer peer.Close()
	go func() {
		peer.Write([]byte(request))
		peer.Close()
	}()

	buf := make([]byte, 1024)
	n, _ := raw.Read(buf) // one read: headers and body together
	conn := NewConnWithBuffer(raw, buf[:n])

	head, err := ReadUntilHeadersEnd(conn)
	if err != nil {
		t.Fatal(err)
	}
	conn.Unread(head)

	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != request {
		t.Fatalf("stream changed:\n got %q\nwant %q", got, request)
	}
}
