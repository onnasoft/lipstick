package manager

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/server/auth"
)

// goodBearer lets only the bearer "good" through, for every request.
func goodBearer(req *http.Request, peer string) (int, string) {
	if req.Header.Get("Authorization") == "Bearer good" {
		return 0, ""
	}
	return 401, bearerChallenge
}

// readUntil reads from c until want appears, or fails after a few seconds.
func readUntil(t *testing.T, c net.Conn, want string) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var got []byte
	buf := make([]byte, 4096)
	for !strings.Contains(string(got), want) {
		n, err := c.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			t.Fatalf("waiting for %q: got %q, %v", want, got, err)
		}
	}
	return string(got)
}

// fakeService answers each request it receives with 200, and reports it on seen.
// It reads each body, the way a real HTTP service does.
func fakeService(c net.Conn, seen chan<- string) {
	br := bufio.NewReader(c)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		io.Copy(io.Discard, req.Body)
		seen <- req.Method + " " + req.URL.Path
		if _, err := c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")); err != nil {
			return
		}
	}
}

// A request that passes is forwarded and answered; one that fails gets 401 and is
// never forwarded.
func TestGuardForwardsOnlyAuthorizedRequests(t *testing.T) {
	visitorS, visitorC := net.Pipe()
	streamS, streamC := net.Pipe()
	defer visitorC.Close()
	defer streamC.Close()
	seen := make(chan string, 8)
	go fakeService(streamC, seen)
	go serveRestrictedConn(visitorS, streamS, goodBearer, func(int64) {})

	go visitorC.Write([]byte("GET /ok HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer good\r\n\r\n"))
	readUntil(t, visitorC, "200 OK")
	if got := <-seen; got != "GET /ok" {
		t.Fatalf("service got %q, want GET /ok", got)
	}

	go visitorC.Write([]byte("GET /no HTTP/1.1\r\nHost: x\r\n\r\n"))
	if got := readUntil(t, visitorC, "401"); !strings.Contains(got, "WWW-Authenticate: Bearer") {
		t.Fatalf("no challenge sent: %q", got)
	}
	select {
	case got := <-seen:
		t.Fatalf("unauthorized request reached the service: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// Two requests in one write, the second unauthorized: the first is answered, then
// the second is refused. The answers keep the order of the requests, and the
// refused request never reaches the service. This is the case the design exists for.
func TestGuardChecksEveryPipelinedRequest(t *testing.T) {
	visitorS, visitorC := net.Pipe()
	streamS, streamC := net.Pipe()
	defer visitorC.Close()
	defer streamC.Close()
	seen := make(chan string, 8)
	go fakeService(streamC, seen)
	go serveRestrictedConn(visitorS, streamS, goodBearer, func(int64) {})

	go visitorC.Write([]byte(
		"GET /first HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer good\r\n\r\n" +
			"GET /second HTTP/1.1\r\nHost: x\r\n\r\n"))

	got := readUntil(t, visitorC, "401")
	if i200, i401 := strings.Index(got, "200 OK"), strings.Index(got, "401"); i200 < 0 || i200 > i401 {
		t.Fatalf("answers out of order: %q", got)
	}
	if first := <-seen; first != "GET /first" {
		t.Fatalf("service got %q first, want GET /first", first)
	}
	select {
	case extra := <-seen:
		t.Fatalf("refused request reached the service: %q", extra)
	case <-time.After(300 * time.Millisecond):
	}
}

// A chunked body is forwarded whole, including its terminating chunk and trailers.
func TestGuardForwardsChunkedBody(t *testing.T) {
	visitorS, visitorC := net.Pipe()
	streamS, streamC := net.Pipe()
	defer visitorC.Close()
	defer streamC.Close()
	seen := make(chan string, 8)
	go fakeService(streamC, seen)
	go serveRestrictedConn(visitorS, streamS, goodBearer, func(int64) {})

	go visitorC.Write([]byte("POST /up HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer good\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"5\r\nhello\r\n6;ext=1\r\n world\r\n0\r\nX-Trailer: yes\r\n\r\n"))
	if got := <-seen; got != "POST /up" {
		t.Fatalf("service got %q", got)
	}
	readUntil(t, visitorC, "200 OK")
}

// A Content-Length body is forwarded by its length, and the next request is read after it.
func TestGuardForwardsBodyByLength(t *testing.T) {
	visitorS, visitorC := net.Pipe()
	streamS, streamC := net.Pipe()
	defer visitorC.Close()
	defer streamC.Close()
	seen := make(chan string, 8)
	go fakeService(streamC, seen)
	go serveRestrictedConn(visitorS, streamS, goodBearer, func(int64) {})
	go io.Copy(io.Discard, visitorC) // the answers must be read, as a visitor would

	go visitorC.Write([]byte("POST /a HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer good\r\nContent-Length: 4\r\n\r\nabcd" +
		"GET /b HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer good\r\n\r\n"))
	if got := <-seen; got != "POST /a" {
		t.Fatalf("first: %q", got)
	}
	if got := <-seen; got != "GET /b" {
		t.Fatalf("second: %q", got)
	}
}

// A request head that never ends is refused at the size limit, not buffered forever.
func TestReadRequestHeadHasASizeLimit(t *testing.T) {
	huge := strings.Repeat("X-Long: "+strings.Repeat("a", 1000)+"\r\n", 100)
	_, err := readRequestHead(bufio.NewReader(strings.NewReader("GET / HTTP/1.1\r\n" + huge)))
	if err == nil {
		t.Fatal("oversized request head accepted")
	}
}

func TestTokenScopes(t *testing.T) {
	cases := []struct {
		methods, prefixes, method, path string
		want                            bool
	}{
		{"GET", "/api", "GET", "/api/x", true},
		{"GET", "/api", "GET", "/api", true},
		{"GET", "/api", "GET", "/apix", false},
		{"GET", "/api", "POST", "/api/x", false},
		{"*", "/", "DELETE", "/anything", true},
		{"GET,POST", "/a,/b", "POST", "/b/c", true},
		{"GET", "/a", "GET", "/", false},
	}
	for _, c := range cases {
		rec := &auth.VisitorToken{Methods: c.methods, PathPrefixes: c.prefixes}
		if got := tokenAllows(rec, c.method, c.path); got != c.want {
			t.Errorf("methods %q prefixes %q, %s %s: got %v want %v", c.methods, c.prefixes, c.method, c.path, got, c.want)
		}
	}
}

func TestFailureLimiterLocksAfterFreeAttempts(t *testing.T) {
	var f failureLimiter
	for i := 0; i < freeFailures-1; i++ {
		f.fail("k")
	}
	if f.blocked("k") != 0 {
		t.Fatal("blocked before the free attempts were used")
	}
	f.fail("k")
	if f.blocked("k") <= 0 {
		t.Fatal("not blocked after the free attempts")
	}
	f.reset("k")
	if f.blocked("k") != 0 {
		t.Fatal("reset did not clear the lock")
	}
}

// signedRequest builds a request to the domain, signed by user with secret at now.
func signedRequest(t *testing.T, method, target, user, secret string, now time.Time) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, "https://robot.example.com"+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.SignRequest(req, user, secret, now); err != nil {
		t.Fatal(err)
	}
	return req
}

func signedManager(secrets map[string]string) *Manager {
	m := &Manager{authManager: &fakeAuth{secrets: secrets}}
	m.visitorNonces = signedNonceCache{}
	return m
}

const aliceSecret = "alice-secret-value-for-tests"

func TestSignedRequestPasses(t *testing.T) {
	m := signedManager(map[string]string{"robot.example.com|alice": aliceSecret})
	req := signedRequest(t, "GET", "/hello", "alice", aliceSecret, time.Now())
	if status, _ := m.visitorCheck("robot.example.com")(req, "10.0.0.9:1"); status != 0 {
		t.Fatalf("valid signature got %d", status)
	}
}

// A captured request must not work twice inside its window.
func TestSignedRequestCannotBeReplayed(t *testing.T) {
	m := signedManager(map[string]string{"robot.example.com|alice": aliceSecret})
	check := m.visitorCheck("robot.example.com")
	req := signedRequest(t, "POST", "/order", "alice", aliceSecret, time.Now())
	if status, _ := check(req, "10.0.0.9:1"); status != 0 {
		t.Fatalf("first use got %d", status)
	}
	replay := req.Clone(req.Context())
	replay.Header = req.Header.Clone()
	if status, _ := check(replay, "10.0.0.9:1"); status != 401 {
		t.Fatalf("replay got %d, want 401", status)
	}
}

func TestSignedRequestOutsideWindowIsRefused(t *testing.T) {
	m := signedManager(map[string]string{"robot.example.com|alice": aliceSecret})
	old := time.Now().Add(-10 * time.Minute)
	req := signedRequest(t, "GET", "/hello", "alice", aliceSecret, old)
	if status, _ := m.visitorCheck("robot.example.com")(req, "10.0.0.9:1"); status != 401 {
		t.Fatalf("stale request got %d, want 401", status)
	}
}

func TestSignedRequestWithWrongSecretIsRefused(t *testing.T) {
	m := signedManager(map[string]string{"robot.example.com|alice": aliceSecret})
	req := signedRequest(t, "GET", "/hello", "alice", "not-alices-secret", time.Now())
	if status, _ := m.visitorCheck("robot.example.com")(req, "10.0.0.9:1"); status != 401 {
		t.Fatalf("wrong secret got %d", status)
	}
}

// The signature covers the path: a request signed for one path cannot be sent to another.
func TestSignatureDoesNotMoveToAnotherPath(t *testing.T) {
	m := signedManager(map[string]string{"robot.example.com|alice": aliceSecret})
	req := signedRequest(t, "GET", "/public", "alice", aliceSecret, time.Now())
	req.URL.Path = "/admin"
	req.URL.RawPath = ""
	if status, _ := m.visitorCheck("robot.example.com")(req, "10.0.0.9:1"); status != 401 {
		t.Fatalf("moved signature got %d", status)
	}
}

func TestUnknownUserIsRefusedLikeAWrongSecret(t *testing.T) {
	m := signedManager(map[string]string{"robot.example.com|alice": aliceSecret})
	req := signedRequest(t, "GET", "/hello", "mallory", aliceSecret, time.Now())
	if status, _ := m.visitorCheck("robot.example.com")(req, "10.0.0.9:1"); status != 401 {
		t.Fatalf("unknown user got %d", status)
	}
}

// Repeated bad signatures from one user and peer get a 429 instead of more checks.
func TestBadSignaturesAreLimited(t *testing.T) {
	m := signedManager(map[string]string{"robot.example.com|alice": aliceSecret})
	check := m.visitorCheck("robot.example.com")
	limited := false
	for i := 0; i < 20 && !limited; i++ {
		req := signedRequest(t, "GET", "/hello", "alice", "wrong", time.Now())
		if status, _ := check(req, "10.0.0.9:1"); status == 429 {
			limited = true
		}
	}
	if !limited {
		t.Fatal("twenty bad signatures were never limited")
	}
}

func TestNonceCacheRefusesReuseAndExpires(t *testing.T) {
	var c signedNonceCache
	if !c.take("a", time.Now().Add(time.Minute)) {
		t.Fatal("new nonce refused")
	}
	if c.take("a", time.Now().Add(time.Minute)) {
		t.Fatal("reused nonce accepted")
	}
	if !c.take("b", time.Now().Add(-time.Second)) {
		t.Fatal("new nonce refused")
	}
	if !c.take("b", time.Now().Add(time.Minute)) {
		t.Fatal("expired nonce still refused")
	}
}
