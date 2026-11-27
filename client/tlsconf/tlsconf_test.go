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

	if err := Configure("", false); err != nil {
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

	if err := Configure(serverCertPEM(t, srv), false); err != nil {
		t.Fatal(err)
	}
	if err := dial(t, addr); err != nil {
		t.Fatalf("trusted certificate rejected: %v", err)
	}
}

func TestInsecureSkipsVerification(t *testing.T) {
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")

	if err := Configure("", true); err != nil {
		t.Fatal(err)
	}
	if !Insecure() {
		t.Fatal("Insecure() should report true")
	}
	if err := dial(t, addr); err != nil {
		t.Fatalf("insecure mode rejected the server: %v", err)
	}
	Configure("", false)
}

func TestConfigureRejectsBadCAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pem")
	os.WriteFile(path, []byte("not a certificate"), 0o644)
	if err := Configure(path, false); err == nil {
		t.Fatal("expected error for a CA file without certificates")
	}
	if err := Configure(filepath.Join(t.TempDir(), "missing.pem"), false); err == nil {
		t.Fatal("expected error for a missing CA file")
	}
}
