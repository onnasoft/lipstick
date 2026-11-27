// Package tlsconf holds the trust policy for every TLS connection the client
// makes: to the lipstick server, and to a local service that is exposed over
// TLS. Certificates are always verified against the system roots, or against
// the CA file given with -ca. There is no option to skip verification.
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
	mu    sync.RWMutex
	roots *x509.CertPool // nil means the system roots
)

// Configure sets the trust policy. caFile is a PEM file with the CA or server
// certificate to trust; empty means the system roots. Call it once, before any
// connection is made.
func Configure(caFile string) error {
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
	return nil
}

// Config returns the TLS configuration for one connection. When serverName is
// empty, tls.Dial uses the host it dialed, which is the usual case.
func Config(serverName string) *tls.Config {
	mu.RLock()
	defer mu.RUnlock()
	return &tls.Config{
		ServerName: serverName,
		RootCAs:    roots,
		MinVersion: tls.VersionTLS12,
	}
}

// ErrVerificationFailed explains how to fix a certificate that cannot be
// verified.
var ErrVerificationFailed = errors.New("certificate could not be verified: pass -ca with the server certificate (server/certs/server.crt)")

// Explain adds the fix to certificate verification errors and leaves other
// errors unchanged.
func Explain(err error) error {
	var verr *tls.CertificateVerificationError
	if errors.As(err, &verr) {
		return fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}
	return err
}
