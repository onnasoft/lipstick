package manager

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// A backend that accepts and never answers. The visitor gets 504 once the backend has
// been silent past backendResponseTimeout, and the tunnel is freed.
func TestSilentBackendGets504(t *testing.T) {
	old := backendResponseTimeout
	backendResponseTimeout = 2 * time.Second
	defer func() { backendResponseTimeout = old }()

	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			go func() { time.Sleep(time.Minute); c.Close() }()
		}
	}()

	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: silent.Addr().String()})

	c := st.visitorClient(t, st.caFile)
	c.Timeout = 15 * time.Second
	start := time.Now()
	resp, err := c.Get("https://" + testDomain + "/hang")
	if err != nil {
		t.Fatalf("visitor got no answer at all: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status %d, want 504", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("504 after %v, want about the 2 second timeout", elapsed)
	}
}
