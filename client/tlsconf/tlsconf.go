// Package tlsconf holds the trust policy the client uses when it connects to
// the lipstick server. By default the server certificate is verified against
// the system roots, or against a CA file given with -ca. Skipping verification
// is only possible with -insecure, and it exposes the API key to anyone who
// can intercept the connection.
package tlsconf

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync"
)

var (
	mu       sync.RWMutex
	roots    *x509.CertPool // nil means the system roots
	insecure bool
)

// Configure sets the trust policy. caFile is a PEM file with the CA or server
// certificate to trust; empty means the system roots. skipVerify disables
// certificate verification.
func Configure(caFile string, skipVerify bool) error {
	var pool *x509.CertPool
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return fmt.Errorf("cannot read CA file %q: %w", caFile, err)
		}
		pool = x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return fmt.Errorf("CA file %q contains no PEM certificates", caFile)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	roots = pool
	insecure = skipVerify
	return nil
}

// Insecure reports whether certificate verification is disabled.
func Insecure() bool {
	mu.RLock()
	defer mu.RUnlock()
	return insecure
}

// Config returns the TLS configuration for one connection to the server. When
// serverName is empty, tls.Dial uses the host it dialed, which is the usual
// case.
func Config(serverName string) *tls.Config {
	mu.RLock()
	defer mu.RUnlock()
	return &tls.Config{
		ServerName:         serverName,
		RootCAs:            roots,
		InsecureSkipVerify: insecure,
		MinVersion:         tls.VersionTLS12,
	}
}

// ErrVerificationFailed explains how to fix a certificate that cannot be
// verified.
var ErrVerificationFailed = errors.New("server certificate could not be verified: pass -ca with the server certificate (server/certs/server.crt), or -insecure for development only")

// Explain adds the fix to certificate verification errors and leaves other
// errors unchanged.
func Explain(err error) error {
	var verr *tls.CertificateVerificationError
	if errors.As(err, &verr) {
		return fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}
	return err
}
