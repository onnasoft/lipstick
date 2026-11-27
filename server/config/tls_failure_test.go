package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A broken certificate must stop startup with an error. It must never return
// a nil TLS configuration, because nil would mean a plain-text listener.
func TestPrepareFailsOnBrokenCertificate(t *testing.T) {
	dir := t.TempDir()
	crt := filepath.Join(dir, "server.crt")
	key := filepath.Join(dir, "server.key")
	os.WriteFile(crt, []byte("not a certificate"), 0o644)
	os.WriteFile(key, []byte("not a key"), 0o600)

	tc, err := (&TLSConfig{CertificatePath: crt, KeyPath: key}).Prepare()
	if err == nil {
		t.Fatal("broken certificate accepted")
	}
	if tc != nil {
		t.Fatal("Prepare returned a config together with an error")
	}
}

func TestPrepareNeverReturnsNilWithoutError(t *testing.T) {
	dir := t.TempDir()
	tc, err := (&TLSConfig{
		CertificatePath: filepath.Join(dir, "c.crt"),
		KeyPath:         filepath.Join(dir, "c.key"),
	}).Prepare()
	if err != nil || tc == nil {
		t.Fatalf("missing files should be generated, got config=%v err=%v", tc, err)
	}
}
