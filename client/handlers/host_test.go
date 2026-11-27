package handlers

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The service receives the Host the visitor sent, including its port. Services
// that compare Origin with Host reject a different value.
func TestServiceSeesVisitorHost(t *testing.T) {
	seen := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Host
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	server, client := tcpPair(t)
	go HandleHTTP(server, strings.TrimPrefix(backend.URL, "http://"), "http", nil)
	client.Write([]byte("GET / HTTP/1.1\r\nHost: localhost:5050\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	client.Close()

	select {
	case host := <-seen:
		if host != "localhost:5050" {
			t.Fatalf("service saw Host %q, want the visitor's localhost:5050", host)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service was not reached")
	}
}
