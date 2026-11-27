package manager

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/server/auth"
)

// Malformed or empty connections must not stop the manager from accepting
// clients. Before the fix, the first bad request ended http.Server.Serve for good.
func TestMalformedConnectionsDoNotStopManager(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	m := &Manager{authManager: &fakeAuth{domains: map[string]*auth.Domain{}}}
	configureRouter(m)
	cl := NewCustomListener(ln, m)
	srv := &http.Server{Handler: m.engine, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(cl)

	addr := ln.Addr().String()
	for i := 0; i < 5; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("\x00\x01 not http at all\r\n"))
		c.Close()
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/health", addr))
	if err != nil {
		t.Fatalf("manager stopped accepting after malformed connections: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health got %d, want 200", resp.StatusCode)
	}
}
