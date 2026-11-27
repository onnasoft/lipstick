package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testDefaults mirrors the program's defaults, so the file can be minimal.
func testDefaults() AppConfig {
	return AppConfig{
		Proxy:    ProxyConfig{Address: ":5050"},
		Manager:  ManagerConfig{Address: ":5051"},
		Admin:    AdminConfig{Address: "127.0.0.1:5052"},
		Database: DatabaseConfig{Host: "localhost", Port: 5432, User: "postgres", Database: "app_db", SSLMode: "disable"},
		Nats:     NatsConfig{URL: "nats://localhost:4222"},
		TLS:      TLSConfig{CertificatePath: "certs/server.crt", KeyPath: "certs/server.key"},
	}
}

const minimalFile = "admin_secret_key: 0123456789abcdef0123\nnats:\n  url: nats://localhost:4222\n"

func fileWith(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Command-line values replace the file, and they are validated like the file.
func TestOverridesApplyAfterFile(t *testing.T) {
	path := fileWith(t, minimalFile)
	cfg, err := loadConfigWith(path, testDefaults(), func(c *AppConfig) {
		c.Admin.Address = "127.0.0.1:6000"
		c.Redis.Database = 0 // zero is a real value when it was written
		c.Database.Host = "db.internal"
	})
	if err != nil {
		t.Fatalf("valid overrides rejected: %v", err)
	}
	if cfg.Admin.Address != "127.0.0.1:6000" || cfg.Database.Host != "db.internal" {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
}

// A bad override must fail validation, not be ignored.
func TestBadOverrideIsRejected(t *testing.T) {
	path := fileWith(t, minimalFile)
	_, err := loadConfigWith(path, testDefaults(), func(c *AppConfig) {
		c.Proxy.Address = "not-an-address"
	})
	if err == nil || !strings.Contains(err.Error(), "proxy.address") {
		t.Fatalf("bad override must be reported, got: %v", err)
	}
}

// Relative paths from the file are relative to the file. Paths from flags are
// used as written.
func TestFlagPathsAreNotRebasedToTheConfigDirectory(t *testing.T) {
	path := fileWith(t, minimalFile)
	cfg, err := loadConfigWith(path, testDefaults(), func(c *AppConfig) {
		c.TLS.CertificatePath = "/abs/from-flag.crt"
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS.CertificatePath != "/abs/from-flag.crt" {
		t.Fatalf("flag path rebased: %q", cfg.TLS.CertificatePath)
	}
}
