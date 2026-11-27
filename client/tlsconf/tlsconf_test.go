package tlsconf

import (
	"crypto/tls"
	"encoding/pem"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func serverCertPEM(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	path := filepath.Join(t.TempDir(), "server.crt")
	if err := os.WriteFile(path, block, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func dial(t *testing.T, addr string) error {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, Config(""))
	if err == nil {
		conn.Close()
	}
	return err
}

func TestDefaultRejectsSelfSignedServer(t *testing.T) {
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")

	if err := Configure(""); err != nil {
		t.Fatal(err)
	}
	err := dial(t, addr)
	if err == nil {
		t.Fatal("self-signed server accepted with default trust")
	}
	var verr *tls.CertificateVerificationError
	if !errors.As(err, &verr) {
		t.Fatalf("want certificate verification error, got %T: %v", err, err)
	}
	if !strings.Contains(Explain(err).Error(), "-ca") {
		t.Fatalf("Explain should point to -ca, got: %v", Explain(err))
	}
}

func TestCAFileTrustsServerCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")

	if err := Configure(serverCertPEM(t, srv)); err != nil {
		t.Fatal(err)
	}
	defer Configure("")
	if err := dial(t, addr); err != nil {
		t.Fatalf("trusted certificate rejected: %v", err)
	}
}

func TestConfigAlwaysRequiresTLS12AndVerification(t *testing.T) {
	c := Config("example.com")
	if c.InsecureSkipVerify {
		t.Fatal("client TLS config must never skip verification")
	}
	if c.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %x, want TLS 1.2", c.MinVersion)
	}
}

func TestConfigureRejectsBadCAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pem")
	os.WriteFile(path, []byte("not a certificate"), 0o644)
	if err := Configure(path); err == nil {
		t.Fatal("expected error for a CA file without certificates")
	}
	if err := Configure(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("expected error for a missing CA file")
	}
}
