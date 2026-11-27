package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.client.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFlagsAloneAreEnoughWithDefaults(t *testing.T) {
	cfg, err := loadConfig([]string{"-c", filepath.Join(t.TempDir(), "missing.yml"), "-k", "secret-key-value"})
	if err == nil {
		// A missing file is fine only when it was not passed explicitly.
		t.Fatalf("explicit -c must fail when the file is missing, got %+v", cfg)
	}

	cfg, err = loadConfig([]string{"-k", "secret-key-value"})
	if err != nil {
		t.Fatalf("defaults with -k should be valid: %v", err)
	}
	if cfg.ServerURL != defaultServerURL || cfg.ProxyPass[0] != defaultProxyPass {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestRejectsPlainHTTPServerURL(t *testing.T) {
	_, err := loadConfig([]string{"-s", "http://localhost:5051", "-k", "secret-key-value"})
	if err == nil || !strings.Contains(err.Error(), "https://") {
		t.Fatalf("http:// server_url must be rejected, got: %v", err)
	}
}

func TestRejectsMissingSecret(t *testing.T) {
	_, err := loadConfig([]string{})
	if err == nil || !strings.Contains(err.Error(), "api_secret is required") {
		t.Fatalf("missing api_secret must be rejected, got: %v", err)
	}
}

func TestReportsAllProblemsTogether(t *testing.T) {
	_, err := loadConfig([]string{"-s", "ws://x", "-p", "not-an-address"})
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	for _, want := range []string{"api_secret is required", "server_url", "proxy_pass"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
}

func TestYAMLValuesAndFlagOverride(t *testing.T) {
	path := writeYAML(t, "api_secret: from-file-key\nserver_url: https://file.example.com:5051\nproxy_pass:\n  - 127.0.0.1:9000\n  - 127.0.0.1:9001\n")
	cfg, err := loadConfig([]string{"-c", path})
	if err != nil {
		t.Fatalf("valid file rejected: %v", err)
	}
	if cfg.APISecret != "from-file-key" || cfg.ServerURL != "https://file.example.com:5051" || len(cfg.ProxyPass) != 2 {
		t.Fatalf("file values not loaded: %+v", cfg)
	}

	cfg, err = loadConfig([]string{"-c", path, "-k", "flag-key"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APISecret != "flag-key" {
		t.Fatalf("flag must override the file, got %q", cfg.APISecret)
	}
}

func TestUnknownYAMLKeyIsAnError(t *testing.T) {
	path := writeYAML(t, "api_secret: k\nproxyPass:\n  - 127.0.0.1:8082\n")
	_, err := loadConfig([]string{"-c", path})
	if err == nil || !strings.Contains(err.Error(), "proxyPass") {
		t.Fatalf("old key name must be reported, got: %v", err)
	}
}
