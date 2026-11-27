package helper

import (
	"net/http"
	"strings"
	"testing"
)

// The Host header is written even though Go keeps it outside request.Header.
// A WebSocket handshake without it is rejected by Jupyter with 400.
func TestHTTPRequestToStringWritesHost(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://127.0.0.1:8888/api/kernels/x/channels", nil)
	req.Host = "127.0.0.1:8888"
	req.Header.Set("Upgrade", "websocket")
	got := HTTPRequestToString(req)
	if !strings.Contains(got, "\r\nHost: 127.0.0.1:8888\r\n") {
		t.Fatalf("Host header missing:\n%s", got)
	}
}
