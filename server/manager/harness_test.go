package manager

// Integration harness. It runs a real server (manager, TCP and TLS proxy, DTLS
// proxy, intranet ports) in-process with an in-memory domain store, and the real
// client binary as a child process. No database or NATS is needed, so the suite
// runs anywhere `go test` runs.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/OnnaSoft/lipstick/server/db"
	"github.com/OnnaSoft/lipstick/server/traffic"
	"github.com/gin-gonic/gin"
	"github.com/pion/dtls/v3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const testDomain = "localhost"

// newTestDB opens a SQLite database for one test, with the production schema. It
// is a file in a temporary directory, so every test starts empty.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lipstick-test.db")
	conn, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&db.Domain{}, &db.DailyConsumption{}, &db.RequestLogEntry{}, &db.VisitorUser{}, &db.VisitorToken{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := conn.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return conn
}

// serverCert loads the repository's server certificate, the one clients trust.
func serverCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	dir := filepath.Join("..", "certs")
	crt := filepath.Join(dir, "server.crt")
	key := filepath.Join(dir, "server.key")
	if _, err := os.Stat(crt); err != nil {
		// The repository ships no certificate; generate one for the tests.
		return selfSigned(t, "lipstick-test")
	}
	cert, err := tls.LoadX509KeyPair(crt, key)
	if err != nil {
		t.Skipf("no usable server certificate in %s: %v", dir, err)
	}
	return cert, crt
}

// selfSigned makes a certificate for localhost and 127.0.0.1, and writes it to a
// temporary PEM file, returned as the second value.
func selfSigned(t *testing.T, cn string) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), cn+".crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return cert, path
}

// gin's mode is a global. It is set once, before any test runs, and no helper changes it.
func init() { gin.SetMode(gin.TestMode) }

var (
	clientOnce sync.Once
	clientBin  string
	clientErr  error
)

// clientBinary builds the real client once per test run.
func clientBinary(t *testing.T) string {
	t.Helper()
	clientOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lipstick-e2e")
		if err != nil {
			clientErr = err
			return
		}
		clientBin = filepath.Join(dir, "lipstick-client")
		cmd := exec.Command("go", "build", "-o", clientBin, "../../client")
		var out bytes.Buffer
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			clientErr = fmt.Errorf("building the client: %v\n%s", err, out.String())
		}
	})
	if clientErr != nil {
		t.Fatal(clientErr)
	}
	return clientBin
}

// stack is one running server with its listeners.
type stack struct {
	t           *testing.T
	manager     *Manager
	auth        auth.AuthManager
	db          *gorm.DB
	apiKey      string
	managerPort string
	proxyAddr   string
	dtlsAddr    string
	intranet    [2]int // reserved port range
	caFile      string // PEM the client trusts for the server (and any local service it reaches)
	serverCA    string // the server's own certificate, alone
	cleanups    []func()
}

type stackOptions struct {
	passthrough bool
	extraTrust  []string    // PEM files the client must trust in addition to the server certificate
	db          *gorm.DB    // shared database (cluster tests); nil means a new one
	bus         PresenceBus // presence bus (cluster tests); nil means a cluster is not started
	allowed     string      // visitor access rules for the domain (CIDRs, comma-separated)
	clusterName string      // advertise name for the cluster, the manager's address is appended
}

// startStack runs a server with one domain, the in-memory store and the real proxy wiring.
func startStack(t *testing.T, opts stackOptions) *stack {
	t.Helper()
	cert, serverCA := serverCert(t)
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	st := &stack{t: t, apiKey: "test-key-0123456789abcdef"}
	if opts.db == nil {
		st.db = newTestDB(t)
	} else {
		st.db = opts.db
	}
	st.auth = auth.NewAuthManagerWithDB(st.db)
	if _, err := st.auth.GetDomain(testDomain); err != nil {
		if err := st.auth.AddDomain(&auth.Domain{
			Name:                     testDomain,
			ApiKey:                   st.apiKey,
			AllowMultipleConnections: true,
			Passthrough:              opts.passthrough,
			AllowedCIDRs:             opts.allowed,
		}); err != nil {
			t.Fatal(err)
		}
	}

	m := &Manager{
		authManager:    st.auth,
		trafficManager: traffic.NewTrafficManager(1 << 40),
		tlsConfig:      tlsConfig,
		reqlog:         NewRequestLog(requestLogSize),
	}
	configureRouter(m)
	st.manager = m

	st.cleanups = append(st.cleanups, func() { m.trafficManager.Close() })

	// Manager port (clients connect here): TLS, served by the manager's own loop.
	mlis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	st.managerPort = fmt.Sprint(mlis.Addr().(*net.TCPAddr).Port)
	go m.ServeListener(tls.NewListener(mlis, tlsConfig))
	st.cleanups = append(st.cleanups, func() { mlis.Close() })

	// A cluster member advertises the manager address the other servers reach.
	if opts.bus != nil {
		if err := m.StartCluster(config.ClusterConfig{AdvertiseAddress: "localhost:" + st.managerPort}, tlsConfig, opts.bus); err != nil {
			t.Fatal(err)
		}
	}

	// Proxy: SNI listener, then the same handlers as main.go.
	raw, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	st.proxyAddr = raw.Addr().String()
	sni := helper.NewSNIListener(raw, tlsConfig)
	sni.SetRoute(m.RoutePassthrough)
	proxy := helper.NewListenerManager(sni)
	proxy.OnTCPConn(func(c net.Conn) { go m.HandleTCPConn(c) })
	proxy.OnHTTPConn(func(c net.Conn, req *http.Request) { go m.HandleHTTPConn(c, req) })
	go proxy.ListenAndServe()
	st.cleanups = append(st.cleanups, func() { raw.Close() })

	// DTLS proxy on its own UDP port.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	st.dtlsAddr = pc.LocalAddr().String()
	go m.ServeDTLSProxy(pc, tlsConfig, pc.LocalAddr().(*net.UDPAddr).Port)
	st.cleanups = append(st.cleanups, func() { pc.Close() })

	// Intranet ports: plain TCP and UDP on 127.0.0.1 in a small range.
	base := freePort(t)
	st.intranet = [2]int{base, base + 5}
	m.ConfigureIntranetPorts("127.0.0.1", st.intranet[0], st.intranet[1], map[string][]*net.IPNet{
		testDomain: {mustCIDR(t, "127.0.0.0/8")},
	})
	st.cleanups = append(st.cleanups, func() { m.releaseAllIntranetPorts() })

	st.caFile = serverCA
	st.serverCA = serverCA
	st.serverCA = serverCA
	if len(opts.extraTrust) > 0 {
		st.caFile = bundle(t, append([]string{serverCA}, opts.extraTrust...))
	}
	return st
}

// memBus is an in-memory PresenceBus standing in for NATS: every subscriber of a
// topic gets every message, asynchronously, as with a broadcast subscription.
type memBus struct {
	mu   sync.Mutex
	subs map[string][]func([]byte)
}

func newMemBus() *memBus { return &memBus{subs: map[string][]func([]byte){}} }

func (b *memBus) Publish(topic string, data []byte) error {
	b.mu.Lock()
	subs := append([]func([]byte){}, b.subs[topic]...)
	b.mu.Unlock()
	for _, cb := range subs {
		cb(append([]byte(nil), data...))
	}
	return nil
}

func (b *memBus) SubscribeBroadcast(topic string, cb func([]byte)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], cb)
	return nil
}

// isLatched reports whether the domain is restricted, as the database has it.
func (st *stack) isLatched() bool {
	d, err := st.auth.GetDomain(testDomain)
	return err == nil && d.AuthLatched
}

// close stops the stack.
func (st *stack) close() {
	for i := len(st.cleanups) - 1; i >= 0; i-- {
		st.cleanups[i]()
	}
}

// clientConfig is the YAML the client reads. Callers add fields as they need.
type clientConfig struct {
	ProxyPass       string
	IntranetPort    int
	IntranetUDPPort int
	UDPTarget       string
	UDPTargetTLS    bool
	Passthrough     bool
	VisitorAuth     bool
	CAFile          string
}

// writeClientConfig writes the YAML the client reads and returns its path.
func writeClientConfig(t *testing.T, st *stack, c clientConfig) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "api_secret: %q\n", st.apiKey)
	fmt.Fprintf(&b, "server_url: %q\n", "https://localhost:"+st.managerPort)
	fmt.Fprintf(&b, "proxy_pass:\n  - %q\n", c.ProxyPass)
	ca := c.CAFile
	if ca == "" {
		ca = st.caFile
	}
	fmt.Fprintf(&b, "ca_file: %q\n", ca)
	if c.IntranetPort > 0 {
		fmt.Fprintf(&b, "intranet_port: %d\n", c.IntranetPort)
	}
	if c.IntranetUDPPort > 0 {
		fmt.Fprintf(&b, "intranet_udp_port: %d\n", c.IntranetUDPPort)
	}
	if c.UDPTarget != "" {
		fmt.Fprintf(&b, "udp_target: %q\n", c.UDPTarget)
	}
	if c.UDPTargetTLS {
		b.WriteString("udp_target_tls: true\n")
	}
	if c.Passthrough {
		b.WriteString("passthrough: true\n")
	}
	if c.VisitorAuth {
		b.WriteString("visitor_auth: true\n")
	}
	cfgPath := filepath.Join(t.TempDir(), "client.yml")
	if err := os.WriteFile(cfgPath, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// runClient starts the real client against the stack and waits until it serves.
func (st *stack) runClient(c clientConfig) *exec.Cmd {
	t := st.t
	t.Helper()
	bin := clientBinary(t)
	cfgPath := writeClientConfig(t, st, c)
	cmd := exec.Command(bin, "-c", cfgPath)
	var out syncBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			t.Logf("client output:\n%s", out.String())
		}
	})
	st.waitForClient(&out)
	return cmd
}

// waitForClient waits until the domain has a mux session, so traffic can flow.
func (st *stack) waitForClient(out *syncBuffer) {
	st.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if hub, ok := st.manager.GetHub(testDomain); ok && hub.muxCount() > 0 {
			time.Sleep(100 * time.Millisecond)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	st.t.Fatalf("client did not connect within 20s; client output:\n%s", out.String())
}

// syncBuffer is a bytes.Buffer safe for the process copy and the test reading it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// proxyURL is the visitor-side address of the TCP proxy.
func (st *stack) proxyHostPort() string { return st.proxyAddr }

func bundle(t *testing.T, files []string) string {
	t.Helper()
	var all []byte
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, b...)
		all = append(all, '\n')
	}
	path := filepath.Join(t.TempDir(), "bundle.pem")
	if err := os.WriteFile(path, all, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// visitorClient returns an HTTP client that reaches the stack's proxy through
// TLS, presenting Host localhost, and trusting caFile.
func (st *stack) visitorClient(t *testing.T, caFile string) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	pem, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	pool.AppendCertsFromPEM(pem)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: testDomain, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, st.proxyAddr)
		},
		MaxIdleConnsPerHost: 8,
	}
	return &http.Client{Transport: tr, Timeout: 20 * time.Second}
}

// dtlsDial connects a DTLS client to the stack's DTLS proxy, for the domain.
func (st *stack) dtlsDial(t *testing.T, caFile string) *dtls.Conn {
	t.Helper()
	pool := x509.NewCertPool()
	pem, _ := os.ReadFile(caFile)
	pool.AppendCertsFromPEM(pem)
	raddr, err := net.ResolveUDPAddr("udp", st.dtlsAddr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dtls.DialWithOptions("udp", raddr, dtls.WithRootCAs(pool), dtls.WithServerName(testDomain))
	if err != nil {
		t.Fatalf("DTLS handshake: %v", err)
	}
	// Bound the handshake, so a stuck one fails the test instead of hanging it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		conn.Close()
		t.Fatalf("DTLS handshake did not complete: %v", err)
	}
	return conn
}

// tlsService starts a TLS HTTP server with its own certificate on 127.0.0.1, the
// local service behind a passthrough or an https:// target. It returns its address
// and the PEM file of its certificate.
func tlsService(t *testing.T, handler http.Handler) (string, string) {
	t.Helper()
	cert, certPath := selfSigned(t, "service")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String(), certPath
}

// udpEcho starts a UDP service that answers "echo:" + each datagram, and returns its address.
func udpEcho(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pc.WriteTo(append([]byte("echo:"), buf[:n]...), addr)
		}
	}()
	return pc.LocalAddr().String()
}

// dtlsEcho starts a DTLS service with cert that answers "echo:" + each datagram.
func dtlsEcho(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	ln, err := dtls.Listen("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")}, &dtls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 2048)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					c.Write(append([]byte("echo:"), buf[:n]...))
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

// dialUDP opens a connected UDP socket to host:port, for a visitor.
func dialUDP(t *testing.T, host string, port int) *net.UDPConn {
	t.Helper()
	raddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
