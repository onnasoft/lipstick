package manager

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/auth"
)

// Restricted domains. The server reads every request of a visitor's connection,
// not only the first: a keep-alive connection can carry many, and an unchecked
// one would let later requests through. Each request is checked against the
// domain's users and tokens. A request that passes is forwarded as it was
// received. Responses are copied back without being read. A WebSocket upgrade is
// checked once, and then relayed raw.
const (
	maxRequestHead    = 64 << 10
	guardCopyWait     = 30 * time.Second
	guardResponseWait = 5 * time.Minute
	basicRealm        = `Basic realm="lipstick"`
	bearerChallenge   = `Bearer realm="lipstick"`
)

// checkFunc decides a request. It returns 0 to let it pass, or the status to answer
// with and the WWW-Authenticate value to send with it.
type checkFunc func(req *http.Request, peer string) (status int, challenge string)

var errHeadTooLarge = errors.New("request head too large")

// serveRestricted gives a visitor of a restricted domain to a client session,
// through the checking relay. Without a session the visitor gets 502: a restricted
// domain never falls back to an unchecked path.
func (manager *Manager) serveRestricted(domain string, conn net.Conn) {
	hub, ok := manager.GetHub(domain)
	if !ok {
		rejectVisitor(conn)
		return
	}
	stream, ok := hub.openStream("LIPSTICK-PORT", localPort(conn), false)
	if !ok {
		rejectVisitor(conn)
		return
	}
	serveRestrictedConn(conn, stream, manager.visitorCheck(domain), hub.addDataUsage)
}

// responseEnd reports that a final response is complete. closes says the connection
// cannot carry another request after it.
type responseEnd struct {
	status int
	closes bool
}

// serveRestrictedConn relays one visitor's connection through check. count receives
// the bytes that move in each direction. The next request is read only after the
// response to the previous one is complete, so responses keep the order of their
// requests and a refused request cannot overtake an answer still on its way.
func serveRestrictedConn(visitor, stream net.Conn, check checkFunc, count func(int64)) {
	peer := peerHost(visitor.RemoteAddr())
	vr := bufio.NewReader(visitor)

	methods := make(chan string, 1)
	ends := make(chan responseEnd, 1)
	interim := make(chan struct{}, 8)
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		relayResponses(visitor, stream, methods, ends, interim, count)
		visitor.Close()
	}()
	defer func() {
		close(methods)
		stream.Close()
		select {
		case <-relayDone:
		case <-time.After(guardCopyWait):
		}
		visitor.Close()
	}()

	for {
		head, err := readRequestHead(vr)
		if err != nil {
			if errors.Is(err, errHeadTooLarge) {
				writeStatus(visitor, 431, "")
			}
			return
		}
		req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(head)))
		if err != nil {
			writeStatus(visitor, 400, "")
			return
		}
		if status, challenge := check(req, peer); status != 0 {
			writeStatus(visitor, status, challenge)
			return
		}
		if _, err := stream.Write(head); err != nil {
			return
		}
		count(int64(len(head)))
		methods <- req.Method

		if isUpgrade(req) {
			// After a 101 the connection is no longer HTTP. The request was checked.
			n, _ := io.Copy(stream, vr)
			count(n)
			return
		}

		// Expect: 100-continue: the visitor sends its body only after an interim 100.
		if strings.EqualFold(req.Header.Get("Expect"), "100-continue") {
			select {
			case <-interim:
			case end := <-ends:
				_ = end
				return
			case <-time.After(guardResponseWait):
				return
			}
		}

		n, err := forwardBody(stream, vr, req)
		count(n)
		if err != nil {
			return
		}

		select {
		case end := <-ends:
			if end.closes {
				return
			}
		case <-time.After(guardResponseWait):
			return
		}
	}
}

// relayResponses copies the service's responses to the visitor, one for each
// request, and reports when each final response is complete. Interim responses
// (100 and other 1xx) are copied and reported on interim.
func relayResponses(visitor io.Writer, stream io.Reader, methods <-chan string, ends chan<- responseEnd, interim chan<- struct{}, count func(int64)) {
	br := bufio.NewReader(stream)
	// sendEnd never blocks: when the guard has gone, nobody reads the end.
	sendEnd := func(e responseEnd) {
		select {
		case ends <- e:
		default:
		}
	}
	for method := range methods {
		for {
			head, err := readRequestHead(br)
			if err != nil {
				sendEnd(responseEnd{closes: true})
				return
			}
			n, _ := visitor.Write(head)
			count(int64(n))
			resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(head)), &http.Request{Method: method})
			if err != nil {
				sendEnd(responseEnd{closes: true})
				return
			}
			if resp.StatusCode == 101 {
				m, _ := io.Copy(visitor, br)
				count(m)
				sendEnd(responseEnd{status: 101, closes: true})
				return
			}
			if resp.StatusCode >= 100 && resp.StatusCode < 200 {
				select {
				case interim <- struct{}{}:
				default:
				}
				continue
			}
			m, closes, err := copyResponseBody(visitor, br, resp, method)
			count(m)
			sendEnd(responseEnd{status: resp.StatusCode, closes: closes || err != nil})
			if closes || err != nil {
				return
			}
			break
		}
	}
}

// copyResponseBody copies the body of a final response. It reports whether the
// connection must close afterwards, which is the case when the body has no length.
func copyResponseBody(dst io.Writer, src *bufio.Reader, resp *http.Response, method string) (int64, bool, error) {
	if method == http.MethodHead || resp.StatusCode == 204 || resp.StatusCode == 304 {
		return 0, resp.Close, nil
	}
	if isChunkedResponse(resp) {
		n, err := copyChunked(dst, src)
		return n, resp.Close, err
	}
	if resp.ContentLength >= 0 {
		n, err := io.CopyN(dst, src, resp.ContentLength)
		return n, resp.Close, err
	}
	n, err := io.Copy(dst, src)
	return n, true, err
}

func isChunkedResponse(resp *http.Response) bool {
	for _, te := range resp.TransferEncoding {
		if strings.EqualFold(te, "chunked") {
			return true
		}
	}
	return false
}

// readRequestHead reads the request line and the header block, up to the empty line.
func readRequestHead(r *bufio.Reader) ([]byte, error) {
	var head []byte
	lines := 0
	for {
		line, err := r.ReadSlice('\n')
		head = append(head, line...)
		if len(head) > maxRequestHead {
			return nil, errHeadTooLarge
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		lines++
		if lines > 1 && (string(line) == "\r\n" || string(line) == "\n") {
			return head, nil
		}
	}
}

// forwardBody copies the body of the request, by Content-Length or chunked framing.
func forwardBody(dst io.Writer, src *bufio.Reader, req *http.Request) (int64, error) {
	if req.ContentLength > 0 {
		return io.CopyN(dst, src, req.ContentLength)
	}
	if isChunked(req) {
		return copyChunked(dst, src)
	}
	return 0, nil
}

func isChunked(req *http.Request) bool {
	for _, te := range req.TransferEncoding {
		if strings.EqualFold(te, "chunked") {
			return true
		}
	}
	return false
}

// copyChunked copies a chunked body, including its trailers, to the end.
func copyChunked(dst io.Writer, src *bufio.Reader) (int64, error) {
	var total int64
	for {
		line, err := src.ReadSlice('\n')
		if err != nil {
			return total, err
		}
		if _, err := dst.Write(line); err != nil {
			return total, err
		}
		total += int64(len(line))
		size, err := strconv.ParseInt(strings.TrimSpace(strings.SplitN(string(line), ";", 2)[0]), 16, 64)
		if err != nil || size < 0 {
			return total, errors.New("bad chunk size")
		}
		if size == 0 {
			for { // trailers, up to the empty line
				t, err := src.ReadSlice('\n')
				if err != nil {
					return total, err
				}
				dst.Write(t)
				total += int64(len(t))
				if string(t) == "\r\n" || string(t) == "\n" {
					return total, nil
				}
			}
		}
		n, err := io.CopyN(dst, src, size+2) // data and its CRLF
		total += n
		if err != nil {
			return total, err
		}
	}
}

func isUpgrade(req *http.Request) bool {
	return strings.Contains(strings.ToLower(req.Header.Get("Connection")), "upgrade") &&
		req.Header.Get("Upgrade") != ""
}

// writeStatus answers with a status line and no body, and closes the connection.
func writeStatus(conn net.Conn, status int, challenge string) {
	conn.SetWriteDeadline(time.Now().Add(rejectWriteTimeout))
	var b strings.Builder
	b.WriteString("HTTP/1.1 " + strconv.Itoa(status) + " " + http.StatusText(status) + "\r\n")
	if challenge != "" {
		b.WriteString("WWW-Authenticate: " + challenge + "\r\n")
	}
	b.WriteString("Content-Length: 0\r\nConnection: close\r\n\r\n")
	conn.Write([]byte(b.String()))
}

// visitorCheck returns the check for a domain: a Basic user or a Bearer token.
func (manager *Manager) visitorCheck(domain string) checkFunc {
	return func(req *http.Request, peer string) (int, string) {
		challenge := basicRealm + ", " + bearerChallenge
		auth := req.Header.Get("Authorization")
		switch {
		case strings.HasPrefix(auth, "Bearer "):
			rec, err := manager.authManager.FindVisitorToken(domain, strings.TrimSpace(auth[len("Bearer "):]))
			if err != nil {
				return 401, bearerChallenge
			}
			if !tokenAllows(rec, req.Method, req.URL.Path) {
				return 403, ""
			}
			return 0, ""

		case strings.HasPrefix(auth, "Basic "):
			user, pass, ok := parseBasic(auth[len("Basic "):])
			if !ok {
				return 401, basicRealm
			}
			key := domain + "|" + peer + "|" + user
			if wait := manager.failures.blocked(key); wait > 0 {
				return 429, ""
			}
			valid, err := manager.authManager.VerifyVisitorUser(domain, user, pass)
			if err != nil || !valid {
				manager.failures.fail(key)
				return 401, basicRealm
			}
			manager.failures.reset(key)
			return 0, ""
		}
		return 401, challenge
	}
}

// tokenAllows reports whether a token may make this request: its method list and
// its path prefixes both have to allow it.
func tokenAllows(rec *auth.VisitorToken, method, path string) bool {
	methodOK := false
	for _, m := range strings.Split(rec.Methods, ",") {
		if m == "*" || strings.EqualFold(m, method) {
			methodOK = true
			break
		}
	}
	if !methodOK {
		return false
	}
	for _, p := range strings.Split(rec.PathPrefixes, ",") {
		if p == "/" || path == p || strings.HasPrefix(path, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}

func parseBasic(encoded string) (string, string, bool) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", false
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	return user, pass, ok
}

// failureLimiter slows down guessing: after a few failed passwords for the same
// domain, peer and user, further attempts wait, doubling each time up to 15 minutes.
type failureLimiter struct {
	mu sync.Mutex
	m  map[string]*failState
}

type failState struct {
	fails int
	until time.Time
}

const (
	freeFailures   = 5
	maxLockout     = 15 * time.Minute
	maxFailEntries = 10000
)

// blocked returns how long the key must still wait, or zero.
func (f *failureLimiter) blocked(key string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.m[key]
	if !ok {
		return 0
	}
	if wait := time.Until(st.until); wait > 0 {
		return wait
	}
	return 0
}

func (f *failureLimiter) fail(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = make(map[string]*failState)
	}
	if len(f.m) >= maxFailEntries {
		for k, st := range f.m {
			if time.Now().After(st.until) {
				delete(f.m, k)
			}
		}
	}
	st := f.m[key]
	if st == nil {
		st = &failState{}
		f.m[key] = st
	}
	st.fails++
	if st.fails >= freeFailures {
		lock := time.Second << min(st.fails-freeFailures, 20)
		if lock > maxLockout {
			lock = maxLockout
		}
		st.until = time.Now().Add(lock)
	}
	if st.fails >= freeFailures {
		logger.Default.Error(fmt.Sprintf("visitor authentication failures: %d for %s", st.fails, key))
	}
}

func (f *failureLimiter) reset(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, key)
}

func peerHost(a net.Addr) string {
	if a == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return host
}

type countingReader struct {
	r     io.Reader
	count func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.count(int64(n))
	}
	return n, err
}
