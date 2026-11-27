package main

import (
	"crypto/tls"
	"log"
	"net"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/OnnaSoft/lipstick/client/tlsconf"
	"github.com/OnnaSoft/lipstick/helper"
)

// passthroughOK is true when this client offers passthrough on its connections:
// the option is on and the local service presented a valid certificate for the
// domain at the last connection.
var passthroughOK atomic.Bool

// probePassthrough checks the local TLS service at target. The handshake verifies
// the certificate chain and the domain name, which is the host of serverURL. The
// check is done at each connection, so an expired certificate stops the offer.
func probePassthrough(proxyTarget, serverURL string) bool {
	u, err := url.Parse(serverURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	_, target := helper.ParseTargetEndpoint(proxyTarget)
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", target, &tls.Config{
		ServerName: u.Hostname(),
		RootCAs:    tlsconf.Roots(),
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		log.Printf("passthrough not offered: %s does not present a valid certificate for %s: %v", target, u.Hostname(), err)
		return false
	}
	conn.Close()
	return true
}
