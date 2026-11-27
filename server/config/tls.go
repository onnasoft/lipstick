package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const selfSignedValidity = 365 * 24 * time.Hour

// Prepare returns the TLS configuration for every listener. TLS is always
// required. If the certificate and key files do not exist yet, a self-signed
// certificate is generated at those paths. The configuration must already be
// validated, so both paths are set.
func (t *TLSConfig) Prepare() (*tls.Config, error) {
	if t.CertificatePath == "" || t.KeyPath == "" {
		return nil, errors.New("tls.certificate_path and tls.key_path are required")
	}

	if err := ensureCertificate(t.CertificatePath, t.KeyPath); err != nil {
		return nil, err
	}

	cert, err := tls.LoadX509KeyPair(t.CertificatePath, t.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("cannot load TLS certificate %q and key %q: %w", t.CertificatePath, t.KeyPath, err)
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}, nil
}

func ensureCertificate(certPath, keyPath string) error {
	certExists, err := fileExists(certPath)
	if err != nil {
		return err
	}
	keyExists, err := fileExists(keyPath)
	if err != nil {
		return err
	}

	switch {
	case certExists && keyExists:
		return nil
	case certExists != keyExists:
		return fmt.Errorf("only one of the TLS files exists (certificate %q, key %q); provide both or remove the existing one so a new pair can be generated", certPath, keyPath)
	}

	log.Printf("TLS certificate not found, generating a self-signed certificate at %s", certPath)
	if err := generateSelfSigned(certPath, keyPath); err != nil {
		return fmt.Errorf("cannot generate self-signed TLS certificate: %w", err)
	}
	return nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("cannot access %q: %w", path, err)
}

func generateSelfSigned(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "lipstick-server", Organization: []string{"Lipstick"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(selfSignedValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return os.WriteFile(keyPath, keyPEM, 0o600)
}
