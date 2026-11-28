package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The example shipped in the repository must load as it is.
func TestShippedExampleLoads(t *testing.T) {
	raw, err := os.ReadFile("../config.yml.example")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	os.WriteFile(path, raw, 0o600)

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
