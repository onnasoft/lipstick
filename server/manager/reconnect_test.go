package manager

import (
	"net"
	"testing"
	"time"
)

// A client that goes away and comes back: the domain loses its tunnel once, and the
// return is counted as one reconnection.
func TestReconnectionIsCounted(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			c, err := backend.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
			}()
		}
	}()

	st := startStack(t, stackOptions{})
	defer st.close()
	first := st.runClient(clientConfig{ProxyPass: backend.Addr().String()})

	losses := tunnelEvents.value(tunnelEvents.losses, testDomain)
	reconnects := tunnelEvents.value(tunnelEvents.reconnects, testDomain)

	first.Process.Kill()
	first.Wait()
	waitUntil(t, 15*time.Second, "the domain to lose its tunnel", func() bool {
		return tunnelEvents.value(tunnelEvents.losses, testDomain) > losses
	})

	st.runClient(clientConfig{ProxyPass: backend.Addr().String()})
	if got := tunnelEvents.value(tunnelEvents.reconnects, testDomain) - reconnects; got != 1 {
		t.Fatalf("reconnections counted %d, want 1", got)
	}
}

func waitUntil(t *testing.T, limit time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
