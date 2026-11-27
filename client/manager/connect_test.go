package manager

import (
	"bufio"
	"crypto/tls"
	"encoding/pem"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/client/tlsconf"
)

// rawServer answers each connection with a fixed response: the status line and
// headers, then optionally more bytes that play the role of tunnel tickets.
func rawServer(t *testing.T, response string, after string) (addr string, cert *tls.Certificate) {
	t.Helper()
	certPair, err := tlsSelfSigned()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certPair}})
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
				defer c.Close()
				http.ReadRequest(bufio.NewReader(c))
				c.Write([]byte(response))
				if after != "" {
					c.Write([]byte(after))
					time.Sleep(200 * time.Millisecond)
				}
			}(c)
		}
	}()
	return ln.Addr().String(), &certPair
}

// trustCert makes the client trust the test certificate for this test only.
func trustCert(t *testing.T, cert *tls.Certificate) {
	t.Helper()
	var out []byte
	for _, der := range cert.Certificate {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tlsconf.Configure(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tlsconf.Configure("") })
}

// A 401 must be an error that names the status, not a connection that looks fine.
func TestConnectRejectsRefusedConnection(t *testing.T) {
	addr, cert := rawServer(t, "HTTP/1.1 401 Unauthorized\r\nContent-Type: application/json\r\n\r\n", "")
	trustCert(t, cert)

	_, err := NewHTTPManager().Connect("https://"+addr+"/", http.Header{})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("refused connection accepted, err=%v", err)
	}
}

// A 200 is a connection. Tickets sent right after the headers must still be
// readable from the returned connection.
func TestConnectAcceptsOKAndKeepsTickets(t *testing.T) {
	addr, cert := rawServer(t, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n", "3f9a\n")
	trustCert(t, cert)

	conn, err := NewHTTPManager().Connect("https://"+addr+"/", http.Header{})
	if err != nil {
		t.Fatalf("200 rejected: %v", err)
	}
	defer conn.Close()

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != "3f9a\n" {
		t.Fatalf("ticket after headers: %q, %v", line, err)
	}
}

// tlsSelfSigned builds a certificate for 127.0.0.1 for the test server.
func tlsSelfSigned() (tls.Certificate, error) {
	return selfSignedForTest()
}
