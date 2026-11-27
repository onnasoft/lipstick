package manager

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestRequestLogKeepsNewestFirstAndBounded(t *testing.T) {
	l := NewRequestLog(3)
	for i := 1; i <= 5; i++ {
		l.Add(RequestRecord{Domain: "a", Path: "/" + string(rune('0'+i)), Status: 200})
	}
	got := l.Recent(RequestQuery{Limit: 10})
	if len(got) != 3 {
		t.Fatalf("ring of 3 kept %d", len(got))
	}
	if got[0].Path != "/5" || got[1].Path != "/4" || got[2].Path != "/3" {
		t.Fatalf("not newest first: %v %v %v", got[0].Path, got[1].Path, got[2].Path)
	}
}

func TestRequestLogFiltersByDomainAndLimit(t *testing.T) {
	l := NewRequestLog(10)
	l.Add(RequestRecord{Domain: "a.example.com", Path: "/a1"})
	l.Add(RequestRecord{Domain: "b.example.com", Path: "/b1"})
	l.Add(RequestRecord{Domain: "a.example.com", Path: "/a2"})
	a := l.Recent(RequestQuery{Domain: "a.example.com", Limit: 10})
	if len(a) != 2 || a[0].Path != "/a2" || a[1].Path != "/a1" {
		t.Fatalf("domain filter wrong: %+v", a)
	}
	if got := l.Recent(RequestQuery{Limit: 1}); len(got) != 1 {
		t.Fatalf("limit ignored: %d", len(got))
	}
}

// Values from a client are cleaned: the method is upper-cased, long paths are
// cut, and an impossible status is dropped.
func TestRequestLogCleansValues(t *testing.T) {
	l := NewRequestLog(2)
	l.Add(RequestRecord{Method: "get", Path: strings.Repeat("p", 2000), Status: 99999, DurationMS: -5})
	r := l.Recent(RequestQuery{Limit: 1})[0]
	if r.Method != "GET" || len(r.Path) != maxPathLen || r.Status != 0 || r.DurationMS != 0 {
		t.Fatalf("not cleaned: method=%q pathlen=%d status=%d dur=%d", r.Method, len(r.Path), r.Status, r.DurationMS)
	}
}

// A "log" line from a client is stored under the hub's domain, with the server's
// time. Pings and unknown lines are ignored, and so is broken JSON.
func TestClientLinesAreRecordedForTheHubDomain(t *testing.T) {
	hub := NewNetworkHub("robot-42.example.com", nil, 1<<40)
	hub.requests = NewRequestLog(10)

	hub.handleClientLine(`log {"method":"GET","path":"/status","status":200,"durationMs":12,"responseBytes":345}`)
	hub.handleClientLine("ping")
	hub.handleClientLine("something new from a newer client")
	hub.handleClientLine(`log {not json`)

	got := hub.requests.Recent(RequestQuery{Limit: 10})
	if len(got) != 1 {
		t.Fatalf("recorded %d lines, want 1", len(got))
	}
	r := got[0]
	if r.Domain != "robot-42.example.com" || r.Method != "GET" || r.Path != "/status" || r.Status != 200 || r.DurationMS != 12 || r.ResponseBytes != 345 {
		t.Fatalf("unexpected record: %+v", r)
	}
	if r.Time.IsZero() {
		t.Fatal("server did not stamp the time")
	}
}

// A client that sends a line longer than the limit is dropped, not buffered.
func TestOversizedClientLineDropsTheClient(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 1<<40)
	hub.requests = NewRequestLog(10)
	go hub.listen()
	defer hub.stop()

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	ws := &ProxyNotificationConn{conn: serverSide}
	hub.ProxyNotificationConns[ws] = true

	done := make(chan struct{})
	go func() { hub.checkConnection(ws); close(done) }()
	go io.Copy(io.Discard, clientSide)
	go clientSide.Write([]byte(strings.Repeat("x", maxClientLine*2)))

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("oversized line did not drop the client")
	}
}

// A port filter keeps only requests that came in on that port. The proxy and an
// intranet port are told apart by their port number.
func TestRequestLogFiltersByPort(t *testing.T) {
	l := NewRequestLog(10)
	l.Add(RequestRecord{Domain: "robot", Port: 5050, Path: "/proxy"})
	l.Add(RequestRecord{Domain: "robot", Port: 9100, Path: "/printer"})
	l.Add(RequestRecord{Domain: "robot", Port: 9101, Path: "/camera"})
	got := l.Recent(RequestQuery{Domain: "robot", Port: 9100, Limit: 10})
	if len(got) != 1 || got[0].Path != "/printer" {
		t.Fatalf("port 9100 filter: %+v", got)
	}
	if n := len(l.Recent(RequestQuery{Limit: 10})); n != 3 {
		t.Fatalf("no port filter should keep all 3, got %d", n)
	}
}

// A port outside 1..65535 is stored as unknown (0), not kept as given.
func TestRequestLogCleansPort(t *testing.T) {
	l := NewRequestLog(2)
	l.Add(RequestRecord{Port: 70000})
	if r := l.Recent(RequestQuery{Limit: 1})[0]; r.Port != 0 {
		t.Fatalf("port 70000 kept as %d", r.Port)
	}
}
