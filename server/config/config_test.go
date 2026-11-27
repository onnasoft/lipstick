package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() AppConfig {
	return AppConfig{
		AdminSecretKey: "0123456789abcdef",
		Proxy:          ProxyConfig{Address: ":5050"},
		Manager:        ManagerConfig{Address: ":5051"},
		Admin:          AdminConfig{Address: ":5052"},
		Database:       DatabaseConfig{Host: "localhost", Port: 5432, User: "u", Database: "d", SSLMode: "disable"},
		Nats:           NatsConfig{URL: "nats://localhost:4222"},
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	c := validConfig()
	c.AdminSecretKey = "short"
	c.Proxy.Address = "5050"
	c.Database.SSLMode = "bogus"
	c.Nats.URL = "http://localhost:4222"
	c.TLS.CertificatePath = "x.crt"

	msg := c.Validate().Error()
	for _, want := range []string{
		"admin_secret_key",
		"proxy.address",
		"database.ssl_mode",
		"nats.url",
		"must be set together",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got:\n%s", want, msg)
		}
	}
}

func TestValidateRejectsBadPorts(t *testing.T) {
	c := validConfig()
	c.Admin.Address = ":70000"
	c.Database.Port = 0
	msg := c.Validate().Error()
	if !strings.Contains(msg, "admin.address") || !strings.Contains(msg, "database.port") {
		t.Errorf("expected port errors, got:\n%s", msg)
	}
}

func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	os.WriteFile(path, []byte("admin_secret_key: 0123456789abcdef\nkeyword: x\n"), 0o600)
	_, err := loadConfigFrom(path, AppConfig{})
	if err == nil || !strings.Contains(err.Error(), "keyword") {
		t.Fatalf("expected unknown key error, got: %v", err)
	}
}

func TestEnsureCertificateGeneratesPair(t *testing.T) {
	dir := t.TempDir()
	cfg := TLSConfig{
		CertificatePath: filepath.Join(dir, "certs", "server.crt"),
		KeyPath:         filepath.Join(dir, "certs", "server.key"),
	}
	tc, err := cfg.Prepare()
	if err != nil || tc == nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	info, err := os.Stat(cfg.KeyPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file missing or not 0600: %v %v", err, info)
	}
	// Second call must reuse the existing pair, not fail.
	if _, err := cfg.Prepare(); err != nil {
		t.Fatalf("second Prepare failed: %v", err)
	}
}

func TestEnsureCertificateRejectsHalfPair(t *testing.T) {
	dir := t.TempDir()
	crt := filepath.Join(dir, "server.crt")
	os.WriteFile(crt, []byte("x"), 0o644)
	cfg := TLSConfig{CertificatePath: crt, KeyPath: filepath.Join(dir, "server.key")}
	if _, err := cfg.Prepare(); err == nil || !strings.Contains(err.Error(), "only one") {
		t.Fatalf("expected half-pair error, got: %v", err)
	}
}

func TestTLSDisabledReturnsNil(t *testing.T) {
	cfg := TLSConfig{}
	tc, err := cfg.Prepare()
	if tc != nil || err != nil {
		t.Fatalf("expected nil,nil when TLS disabled, got %v %v", tc, err)
	}
}

func TestValidateRejectsDuplicateListeners(t *testing.T) {
	c := validConfig()
	c.Manager.Address = ":5050"
	if msg := c.Validate().Error(); !strings.Contains(msg, "both use") {
		t.Fatalf("expected duplicate listener error, got:\n%s", msg)
	}
}
