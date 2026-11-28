package manager

// End-to-end scenarios over the real stack: the server runs in-process and the
// client is the real binary. Each scenario starts its own server, so one failure
// cannot leave state behind for the next.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"github.com/pion/dtls/v3"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// echoHTTP answers with the body of the request, and its hash in a header.
func echoHTTP(t *testing.T) *http.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from the local service"))
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		w.Header().Set("X-Body-Sha256", hex.EncodeToString(sum[:]))
		w.Write(body)
	})
	return &http.Server{Handler: mux}
}

// localHTTP starts a plain HTTP service on 127.0.0.1 and returns its host:port.
func localHTTP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := echoHTTP(t)
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func getBody(t *testing.T, c *http.Client, path string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get("https://" + testDomain + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// Scenario: plain HTTP service, TLS ended by the server.
func TestIntegrationHTTPTerminated(t *testing.T) {
	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: localHTTP(t)})

	c := st.visitorClient(t, st.caFile)
	resp, body := getBody(t, c, "/hello")
	if resp.StatusCode != 200 || body != "hello from the local service" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
}

// Scenario: 20 requests on one connection (keep-alive) all answered.
func TestIntegrationKeepAlive(t *testing.T) {
	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: localHTTP(t)})

	c := st.visitorClient(t, st.caFile)
	for i := 0; i < 20; i++ {
		resp, _ := getBody(t, c, "/hello")
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: %d", i, resp.StatusCode)
		}
	}
}

// Scenario: a 1 MiB upload arrives intact, checked by its hash on the way back.
func TestIntegrationLargeUploadIntact(t *testing.T) {
	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: localHTTP(t)})

	payload := bytes.Repeat([]byte("0123456789abcdef"), 64*1024) // 1 MiB
	sum := sha256.Sum256(payload)
	c := st.visitorClient(t, st.caFile)
	resp, err := c.Post("https://"+testDomain+"/echo", "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if hex.EncodeToString(sum[:]) != resp.Header.Get("X-Body-Sha256") || !bytes.Equal(got, payload) {
		t.Fatalf("upload changed: %d bytes back, hash %s", len(got), resp.Header.Get("X-Body-Sha256"))
	}
}

// Scenario: the local service speaks TLS, and the client reaches it with https://.
func TestIntegrationTLSTargetTerminated(t *testing.T) {
	addr, servicePEM := tlsService(t, echoHTTP(t).Handler)
	st := startStack(t, stackOptions{extraTrust: []string{servicePEM}})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: "https://" + addr})

	c := st.visitorClient(t, st.caFile)
	resp, body := getBody(t, c, "/hello")
	if resp.StatusCode != 200 || body != "hello from the local service" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
}

// Scenario: passthrough. The visitor sees the service's certificate, not the server's.
func TestIntegrationPassthrough(t *testing.T) {
	addr, servicePEM := tlsService(t, echoHTTP(t).Handler)
	st := startStack(t, stackOptions{passthrough: true, extraTrust: []string{servicePEM}})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: addr, Passthrough: true})

	c := st.visitorClient(t, servicePEM)
	resp, body := getBody(t, c, "/hello")
	if resp.StatusCode != 200 || body != "hello from the local service" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	if cn := resp.TLS.PeerCertificates[0].Subject.CommonName; cn != "service" {
		t.Fatalf("visitor saw certificate %q, want the service's", cn)
	}
	// The server's certificate must not be what the visitor gets.
	if _, err := st.visitorClient(t, st.serverCA).Get("https://" + testDomain + "/hello"); err == nil {
		t.Fatal("visitor accepted the server's certificate on a passthrough domain")
	}
}

// Scenario: a TCP service through an intranet port: 1 MiB sent, the write side
// closed, and the echo comes back whole.
func TestIntegrationIntranetTCP(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { io.Copy(c, c); c.Close() }(c)
		}
	}()

	st := startStack(t, stackOptions{})
	defer st.close()
	port := st.intranet[0]
	st.runClient(clientConfig{ProxyPass: echo.Addr().String(), IntranetPort: port})

	payload := bytes.Repeat([]byte("tcp-intranet-"), 80*1024)
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(port)), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(20 * time.Second))
	go func() {
		c.Write(payload)
		c.(*net.TCPConn).CloseWrite()
	}()
	got, err := io.ReadAll(c)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("echo %d bytes of %d, err %v", len(got), len(payload), err)
	}
}

// Scenario: a UDP service through an intranet UDP port: 200 datagrams, each echoed.
func TestIntegrationIntranetUDP(t *testing.T) {
	echo := udpEcho(t)
	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: localHTTP(t), IntranetUDPPort: st.intranet[1], UDPTarget: echo})

	sock := dialUDP(t, "127.0.0.1", st.intranet[1])
	defer sock.Close()
	for i := 0; i < 200; i++ {
		msg := []byte("datagram-" + itoa(i))
		sock.Write(msg)
		buf := make([]byte, 2048)
		sock.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := sock.Read(buf)
		if err != nil || string(buf[:n]) != "echo:"+string(msg) {
			t.Fatalf("datagram %d: got %q, %v", i, buf[:n], err)
		}
	}
}

// Scenario: DTLS proxy. A DTLS visitor reaches a plain UDP service.
func TestIntegrationDTLSProxyToPlainUDP(t *testing.T) {
	echo := udpEcho(t)
	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: localHTTP(t), UDPTarget: echo})

	conn := st.dtlsDial(t, st.caFile)
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte("secure"))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "echo:secure" {
		t.Fatalf("got %q, %v", buf[:n], err)
	}
}

// Scenario: DTLS proxy to a DTLS service, verified by the client.
func TestIntegrationDTLSProxyToVerifiedDTLS(t *testing.T) {
	cert, servicePEM := selfSigned(t, "dtls-service")
	svc := dtlsEcho(t, cert)
	st := startStack(t, stackOptions{extraTrust: []string{servicePEM}})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: localHTTP(t), UDPTarget: svc, UDPTargetTLS: true})

	conn := st.dtlsDial(t, st.caFile)
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte("two-hops"))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "echo:two-hops" {
		t.Fatalf("got %q, %v", buf[:n], err)
	}
}

// Scenario: the DTLS service presents a certificate the client does not trust.
// Nothing must come back to the visitor. Two outcomes are correct: the visitor's
// handshake never completes (the server drops it when the local service refuses),
// or it completes and gets no answer.
func TestIntegrationDTLSRejectsUntrustedService(t *testing.T) {
	cert, _ := selfSigned(t, "untrusted")
	svc := dtlsEcho(t, cert)
	st := startStack(t, stackOptions{}) // the client trusts only the server's certificate
	defer st.close()
	st.runClient(clientConfig{ProxyPass: localHTTP(t), UDPTarget: svc, UDPTargetTLS: true})

	pool := x509.NewCertPool()
	pem, _ := os.ReadFile(st.caFile)
	pool.AppendCertsFromPEM(pem)
	raddr, err := net.ResolveUDPAddr("udp", st.dtlsAddr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dtls.DialWithOptions("udp", raddr, dtls.WithRootCAs(pool), dtls.WithServerName(testDomain))
	if err != nil {
		return // the visitor never got through: nothing came back
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		return
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	conn.Write([]byte("should-not-pass"))
	buf := make([]byte, 2048)
	if n, err := conn.Read(buf); err == nil {
		t.Fatalf("an untrusted service answered: %q", buf[:n])
	}
}

// Scenario: restricted domain. Without credentials 401; a user gets 200; a bad token 401.
func TestIntegrationRestrictedDomain(t *testing.T) {
	st := startStack(t, stackOptions{})
	defer st.close()
	st.auth.AddVisitorUser(testDomain, "alice", "correct-horse-battery")
	st.runClient(clientConfig{ProxyPass: localHTTP(t), VisitorAuth: true})

	c := st.visitorClient(t, st.caFile)
	if resp, _ := getBody(t, c, "/hello"); resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("anonymous visit: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", "https://"+testDomain+"/hello", nil)
	req.SetBasicAuth("alice", "correct-horse-battery")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("alice: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest("GET", "https://"+testDomain+"/hello", nil)
	req.Header.Set("Authorization", "Bearer not-a-token")
	if resp, err = c.Do(req); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("bad token: %d", resp.StatusCode)
	}
	if !st.isLatched() {
		t.Fatal("domain not latched after a client asked for authentication")
	}
}

// Scenario: a client that asks for visitor authentication on a passthrough domain
// is refused with the reason, and never connects.
func TestIntegrationRefusedOnPassthrough(t *testing.T) {
	st := startStack(t, stackOptions{passthrough: true})
	defer st.close()
	st.auth.AddVisitorUser(testDomain, "alice", "correct-horse-battery")

	bin := clientBinary(t)
	cfg := writeClientConfig(t, st, clientConfig{ProxyPass: localHTTP(t), VisitorAuth: true})
	cmd := exec.Command(bin, "-c", cfg)
	var out syncBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "passthrough") {
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "409") || !strings.Contains(out.String(), "passthrough") {
		t.Fatalf("no refusal reason in the client output:\n%s", out.String())
	}
	if hub, ok := st.manager.GetHub(testDomain); ok && hub.muxCount() > 0 {
		t.Fatal("a client was connected to a refused domain")
	}
}

// Scenario: a host with no tunnel gets 502, not a hang.
func TestIntegrationUnknownHostGets502(t *testing.T) {
	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: localHTTP(t)})

	c := st.visitorClient(t, st.caFile)
	req, _ := http.NewRequest("GET", "https://"+testDomain+"/hello", nil)
	req.Host = "nobody.example.com"
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("no answer: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("got %d, want 502", resp.StatusCode)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// Scenario: 50 visitors at once, 10 requests each, all answered through one client.
func TestIntegrationConcurrentVisitors(t *testing.T) {
	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: localHTTP(t)})

	c := st.visitorClient(t, st.caFile)
	c.Transport.(*http.Transport).MaxIdleConnsPerHost = 64
	errs := make(chan error, 50)
	for v := 0; v < 50; v++ {
		go func() {
			for i := 0; i < 10; i++ {
				resp, err := c.Get("https://" + testDomain + "/hello")
				if err != nil {
					errs <- err
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 {
					errs <- io.ErrUnexpectedEOF
					return
				}
			}
			errs <- nil
		}()
	}
	for v := 0; v < 50; v++ {
		if err := <-errs; err != nil {
			t.Fatalf("visitor failed: %v", err)
		}
	}
}

// Scenario: two clients for the same domain both serve it, and visitors still get answers.
func TestIntegrationTwoClientsSameDomain(t *testing.T) {
	st := startStack(t, stackOptions{})
	defer st.close()
	backend := localHTTP(t)
	st.runClient(clientConfig{ProxyPass: backend})
	st.runClient(clientConfig{ProxyPass: backend})

	c := st.visitorClient(t, st.caFile)
	for i := 0; i < 20; i++ {
		resp, _ := getBody(t, c, "/hello")
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: %d", i, resp.StatusCode)
		}
	}
	if hub, ok := st.manager.GetHub(testDomain); !ok || hub.muxCount() < 2 {
		t.Fatal("the second client did not register its sessions")
	}
}

// Scenario: a client that stops and a new one that starts: traffic resumes.
func TestIntegrationClientReconnects(t *testing.T) {
	st := startStack(t, stackOptions{})
	defer st.close()
	backend := localHTTP(t)
	first := st.runClient(clientConfig{ProxyPass: backend})
	c := st.visitorClient(t, st.caFile)
	getBody(t, c, "/hello")

	first.Process.Kill()
	first.Wait()
	st.waitForHubGone()

	st.runClient(clientConfig{ProxyPass: backend})
	resp, body := getBody(t, c, "/hello")
	if resp.StatusCode != 200 || body != "hello from the local service" {
		t.Fatalf("after reconnect: %d %q", resp.StatusCode, body)
	}
}

// Scenario: the request log records the port each visit came in on.
func TestIntegrationRequestLogRecordsPort(t *testing.T) {
	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: localHTTP(t)})

	c := st.visitorClient(t, st.caFile)
	getBody(t, c, "/hello")
	_, proxyPort, _ := net.SplitHostPort(st.proxyAddr)
	want := itoa(mustAtoi(t, proxyPort))
	// The client reports each request on its control connection, a moment after the answer.
	var recs []RequestRecord
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if recs = st.manager.reqlog.Recent(RequestQuery{Domain: testDomain, Limit: 5}); len(recs) > 0 {
			break
		}
	}
	if len(recs) == 0 {
		t.Fatal("no request recorded")
	}
	if got := itoa(recs[0].Port); got != want {
		t.Fatalf("recorded port %s, want the proxy's %s", got, want)
	}
}

// Scenario: several DTLS visitors at once, each from its own source, each answered.
func TestIntegrationManyDTLSVisitors(t *testing.T) {
	echo := udpEcho(t)
	st := startStack(t, stackOptions{})
	defer st.close()
	st.runClient(clientConfig{ProxyPass: localHTTP(t), UDPTarget: echo})

	errs := make(chan error, 8)
	for v := 0; v < 8; v++ {
		go func(v int) {
			conn := st.dtlsDialNoT(st.caFile)
			if conn == nil {
				errs <- io.ErrUnexpectedEOF
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(10 * time.Second))
			for i := 0; i < 5; i++ {
				msg := "v" + itoa(v) + "-" + itoa(i)
				conn.Write([]byte(msg))
				buf := make([]byte, 2048)
				n, err := conn.Read(buf)
				if err != nil || string(buf[:n]) != "echo:"+msg {
					errs <- io.ErrUnexpectedEOF
					return
				}
			}
			errs <- nil
		}(v)
	}
	for v := 0; v < 8; v++ {
		if err := <-errs; err != nil {
			t.Fatalf("DTLS visitor failed: %v", err)
		}
	}
}

// waitForHubGone waits until the domain has no hub, after its clients left.
func (st *stack) waitForHubGone() {
	st.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := st.manager.GetHub(testDomain); !ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	st.t.Fatal("hub still running after the client stopped")
}

// dtlsDialNoT is dtlsDial for goroutines, which must not call t.Fatal.
func (st *stack) dtlsDialNoT(caFile string) *dtls.Conn {
	pool := x509.NewCertPool()
	pem, _ := os.ReadFile(caFile)
	pool.AppendCertsFromPEM(pem)
	raddr, err := net.ResolveUDPAddr("udp", st.dtlsAddr)
	if err != nil {
		return nil
	}
	conn, err := dtls.DialWithOptions("udp", raddr, dtls.WithRootCAs(pool), dtls.WithServerName(testDomain))
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil
	}
	return conn
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Scenario: access rules. A visitor outside the domain's networks gets 403 before the
// tunnel; one inside gets 200. The same rule holds for passthrough and DTLS.
func TestIntegrationAccessRules(t *testing.T) {
	t.Run("outside the networks gets 403", func(t *testing.T) {
		st := startStack(t, stackOptions{allowed: "10.0.0.0/8"})
		defer st.close()
		st.runClient(clientConfig{ProxyPass: localHTTP(t)})
		resp, _ := getBody(t, st.visitorClient(t, st.caFile), "/hello")
		if resp.StatusCode != 403 {
			t.Fatalf("got %d, want 403", resp.StatusCode)
		}
	})
	t.Run("inside the networks gets 200", func(t *testing.T) {
		st := startStack(t, stackOptions{allowed: "127.0.0.0/8, 10.0.0.0/8"})
		defer st.close()
		st.runClient(clientConfig{ProxyPass: localHTTP(t)})
		resp, body := getBody(t, st.visitorClient(t, st.caFile), "/hello")
		if resp.StatusCode != 200 || body != "hello from the local service" {
			t.Fatalf("got %d %q", resp.StatusCode, body)
		}
	})
	t.Run("a single address rule", func(t *testing.T) {
		st := startStack(t, stackOptions{allowed: "127.0.0.1"})
		defer st.close()
		st.runClient(clientConfig{ProxyPass: localHTTP(t)})
		if resp, _ := getBody(t, st.visitorClient(t, st.caFile), "/hello"); resp.StatusCode != 200 {
			t.Fatalf("127.0.0.1 rule refused 127.0.0.1: %d", resp.StatusCode)
		}
	})
	t.Run("passthrough domain obeys the rule", func(t *testing.T) {
		addr, servicePEM := tlsService(t, echoHTTP(t).Handler)
		st := startStack(t, stackOptions{passthrough: true, allowed: "10.0.0.0/8", extraTrust: []string{servicePEM}})
		defer st.close()
		st.runClient(clientConfig{ProxyPass: addr, Passthrough: true})
		resp, _ := getBody(t, st.visitorClient(t, st.serverCA), "/hello")
		if resp.StatusCode != 403 {
			t.Fatalf("got %d, want 403", resp.StatusCode)
		}
	})
	t.Run("DTLS proxy obeys the rule", func(t *testing.T) {
		echo := udpEcho(t)
		st := startStack(t, stackOptions{allowed: "10.0.0.0/8"})
		defer st.close()
		st.runClient(clientConfig{ProxyPass: localHTTP(t), UDPTarget: echo})
		// A DTLS visitor from outside the rules never gets its handshake through.
		pool := x509.NewCertPool()
		pem, _ := os.ReadFile(st.caFile)
		pool.AppendCertsFromPEM(pem)
		raddr, _ := net.ResolveUDPAddr("udp", st.dtlsAddr)
		conn, err := dtls.DialWithOptions("udp", raddr, dtls.WithRootCAs(pool), dtls.WithServerName(testDomain))
		if err != nil {
			return
		}
		defer conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := conn.HandshakeContext(ctx); err == nil {
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			conn.Write([]byte("x"))
			buf := make([]byte, 64)
			if n, err := conn.Read(buf); err == nil {
				t.Fatalf("an outside DTLS visitor got an answer: %q", buf[:n])
			}
		}
	})
}
