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
		TLS:            TLSConfig{CertificatePath: "certs/server.crt", KeyPath: "certs/server.key"},
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	c := validConfig()
	c.Proxy.Address = "5050"
	c.Database.SSLMode = "bogus"
	c.Nats.URL = "http://localhost:4222"
	c.TLS.KeyPath = ""

	msg := c.Validate().Error()
	for _, want := range []string{
		"proxy.address",
		"database.ssl_mode",
		"nats.url",
		"tls.key_path is required",
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

func TestPrepareRequiresBothPaths(t *testing.T) {
	if _, err := (&TLSConfig{}).Prepare(); err == nil {
		t.Fatal("Prepare must fail when TLS paths are empty")
	}
}

func TestValidateRequiresTLS(t *testing.T) {
	c := validConfig()
	c.TLS = TLSConfig{}
	msg := c.Validate().Error()
	if !strings.Contains(msg, "tls.certificate_path is required") || !strings.Contains(msg, "tls.key_path is required") {
		t.Fatalf("TLS must be required, got:\n%s", msg)
	}
}

// The admin key is no longer needed: a configuration with or without it is valid.
func TestValidateDoesNotRequireAdminKey(t *testing.T) {
	c := validConfig()
	c.AdminSecretKey = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("no admin key refused: %v", err)
	}
	c.AdminSecretKey = "change-me-to-a-long-random-secret"
	if err := c.Validate(); err != nil {
		t.Fatalf("old admin key makes the file invalid: %v", err)
	}
}

func TestDefaultsIncludeTLSPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	os.WriteFile(path, []byte("admin_secret_key: 0123456789abcdef\nproxy:\n  address: \":5050\"\nmanager:\n  address: \":5051\"\nadmin:\n  address: \"127.0.0.1:5052\"\ndatabase:\n  host: localhost\n  port: 5432\n  user: u\n  database: d\n  ssl_mode: disable\nnats:\n  url: nats://localhost:4222\n"), 0o600)
	defaults := AppConfig{TLS: TLSConfig{CertificatePath: "certs/server.crt", KeyPath: "certs/server.key"}}
	cfg, err := loadConfigFrom(path, defaults)
	if err != nil {
		t.Fatalf("config without tls section should use defaults: %v", err)
	}
	if want := filepath.Join(dir, "certs", "server.crt"); cfg.TLS.CertificatePath != want {
		t.Fatalf("certificate path = %q, want %q", cfg.TLS.CertificatePath, want)
	}
}

func TestValidateRejectsDuplicateListeners(t *testing.T) {
	c := validConfig()
	c.Manager.Address = ":5050"
	if msg := c.Validate().Error(); !strings.Contains(msg, "both use") {
		t.Fatalf("expected duplicate listener error, got:\n%s", msg)
	}
}

func TestLimitsAreValidated(t *testing.T) {
	c := validConfig()
	c.Limits.VisitorIPRate = -1
	c.Limits.DomainRate = 5
	c.Limits.DomainBurst = 0
	msg := c.Validate().Error()
	if !strings.Contains(msg, "cannot be negative") || !strings.Contains(msg, "burst of at least 1") {
		t.Fatalf("limits not validated: %s", msg)
	}
}
