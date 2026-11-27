package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The example shipped in the repository must load once its placeholder key is
// replaced, and must be refused while the placeholder is still there.
func TestShippedExampleLoads(t *testing.T) {
	raw, err := os.ReadFile("../config.yml.example")
	if err != nil {
		t.Fatal(err)
	}
	fixed := strings.Replace(string(raw), "change-me-run-openssl-rand-hex-24", "0123456789abcdef0123", 1)
	path := filepath.Join(t.TempDir(), "config.yml")
	os.WriteFile(path, []byte(fixed), 0o600)

	cfg, err := loadConfigFrom(path, AppConfig{})
	if err != nil {
		t.Fatalf("shipped example does not load: %v", err)
	}
	if cfg.Manager.Address != ":5051" || cfg.Admin.Address != "127.0.0.1:5052" {
		t.Fatalf("unexpected values from the example: %+v", cfg)
	}
	if !strings.HasSuffix(cfg.TLS.KeyPath, filepath.Join("certs", "server.key")) {
		t.Fatalf("TLS key path not resolved against the config directory: %q", cfg.TLS.KeyPath)
	}
}

func TestShippedExampleRefusesPlaceholderKey(t *testing.T) {
	raw, err := os.ReadFile("../config.yml.example")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	os.WriteFile(path, raw, 0o600)
	if _, err := loadConfigFrom(path, AppConfig{}); err == nil || !strings.Contains(err.Error(), "example value") {
		t.Fatalf("placeholder must be refused, got: %v", err)
	}
}
