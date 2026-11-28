package manager

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// A backend that answers after 300 ms. The latency recorded for the domain must reflect
// that delay: the first reply byte comes about 300 ms after the visitor's request.
func TestSlowBackendShowsInConnectionLatency(t *testing.T) {
	slow, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	go func() {
		for {
			c, err := slow.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 4096)
				c.Read(buf)
				time.Sleep(300 * time.Millisecond)
				c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
			}()
		}
	}()

	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: slow.Addr().String()})

	before := latencySum(t, testDomain)
	c := st.visitorClient(t, st.caFile)
	resp, err := c.Get("https://" + testDomain + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := latencySum(t, testDomain) - before; got < 0.25 {
		t.Fatalf("recorded latency %.3fs, want about 0.3s", got)
	}
}

// latencySum reads the recorded sum for a domain from the exported samples.
func latencySum(t *testing.T, domain string) float64 {
	t.Helper()
	for _, s := range connectionLatency.Samples() {
		if s.Suffix == "_sum" && s.Labels["domain"] == domain {
			return s.Value
		}
	}
	return 0
}
