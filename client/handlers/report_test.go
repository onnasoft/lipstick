package handlers

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A finished request is reported once, with method, path (no query), status and
// size. Headers and the query string are not part of the report.
func TestHandleHTTPReportsEachRequest(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "5")
		w.Write([]byte("hello"))
	}))
	defer backend.Close()
	host := strings.TrimPrefix(backend.URL, "http://")

	reports := make(chan RequestReport, 4)
	server, client := tcpPair(t)
	go HandleHTTP(server, host, "http", func(r RequestReport) { reports <- r })

	client.Write([]byte("GET /status?token=secret HTTP/1.1\r\nHost: a\r\nCookie: session=abc\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	client.Close()

	select {
	case r := <-reports:
		if r.Method != "GET" || r.Path != "/status" || r.Status != 200 || r.ResponseBytes != 5 {
			t.Fatalf("unexpected report: %+v", r)
		}
		if strings.Contains(r.Path, "token") {
			t.Fatal("query string leaked into the report")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no report for the request")
	}
}

// A service that is down is reported as 502, so the panel shows the failure.
func TestHandleHTTPReports502WhenServiceIsDown(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	down := ln.Addr().String()
	ln.Close()

	reports := make(chan RequestReport, 1)
	server, client := tcpPair(t)
	go func() {
		HandleHTTP(server, down, "http", func(r RequestReport) { reports <- r })
		server.Close() // establishConnection closes the tunnel side the same way
	}()

	client.Write([]byte("GET /x HTTP/1.1\r\nHost: a\r\n\r\n"))
	io.ReadAll(client)
	client.Close()

	select {
	case r := <-reports:
		if r.Status != http.StatusBadGateway {
			t.Fatalf("status %d, want 502", r.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no report for the failed request")
	}
}
