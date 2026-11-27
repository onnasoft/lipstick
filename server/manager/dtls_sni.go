package manager

import "github.com/OnnaSoft/lipstick/helper"

// dtlsServerName reads the SNI from the first datagram of a DTLS handshake. See
// helper.DTLSClientHelloServerName.
func dtlsServerName(d []byte) (string, bool) {
	return helper.DTLSClientHelloServerName(d)
}
