package manager

import (
	"net"
	"testing"
	"time"
)

// A connected client's mux session is probed; the round trips land in the domain's
// tunnel latency histogram.
func TestTunnelLatencyIsRecordedPerSession(t *testing.T) {
	old := tunnelProbeEvery
	tunnelProbeEvery = 50 * time.Millisecond
	defer func() { tunnelProbeEvery = old }()

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
	st.runClient(clientConfig{ProxyPass: backend.Addr().String()})

	before := tunnelCount(t, testDomain)
	deadline := time.Now().Add(10 * time.Second)
	for tunnelCount(t, testDomain)-before < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("only %v round trips recorded in 10 s", tunnelCount(t, testDomain)-before)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func tunnelCount(t *testing.T, domain string) float64 {
	t.Helper()
	for _, s := range tunnelLatency.Samples() {
		if s.Suffix == "_count" && s.Labels["domain"] == domain {
			return s.Value
		}
	}
	return 0
}
