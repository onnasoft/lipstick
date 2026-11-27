package config

import (
	"strings"
	"testing"
)

func TestIntranetPortFlagIsRead(t *testing.T) {
	cfg, err := loadConfig([]string{"-k", "secret-key-value", "-intranet-port", "5105"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IntranetPort != 5105 {
		t.Fatalf("intranet port = %d, want 5105", cfg.IntranetPort)
	}
}

func TestIntranetPortDefaultsToNone(t *testing.T) {
	cfg, err := loadConfig([]string{"-k", "secret-key-value"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IntranetPort != 0 {
		t.Fatalf("intranet port = %d, want 0", cfg.IntranetPort)
	}
}

func TestIntranetPortOutOfRangeIsRejected(t *testing.T) {
	_, err := loadConfig([]string{"-k", "secret-key-value", "-intranet-port", "70000"})
	if err == nil || !strings.Contains(err.Error(), "not a port number") {
		t.Fatalf("port 70000 accepted: %v", err)
	}
}
