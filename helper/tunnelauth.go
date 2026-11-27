package helper

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"time"
)

// ProofHeader carries the proof that a reverse connection comes from a client
// that knows the domain's key. The key itself is never sent on that connection.
const ProofHeader = "X-Lipstick-Proof"

// PassthroughHeader is sent on a mux connection by a client whose service ends TLS
// itself, with a certificate for the domain. Its value is "1".
const PassthroughHeader = "X-Lipstick-Passthrough"

// MuxNonceHeader carries the nonce of a mux connection. It starts with the
// client's time, so the server can refuse old nonces.
const MuxNonceHeader = "X-Lipstick-Mux-Nonce"

// MuxNonceWindow is how far a pool nonce may be from the server's clock. A
// nonce that old is refused, and the server forgets nonces older than this.
const MuxNonceWindow = 2 * time.Minute

// Labels separate the uses of the key, so a proof for one use is never valid
// for another.
const (
	ticketLabel = "lipstick-tunnel-v1:"
	muxLabel    = "lipstick-mux-v1:"
)

// nonceLength is 12 hex digits of time in milliseconds plus 32 hex digits of randomness.
const nonceLength = 44

func proof(label, apiKey, value string) string {
	mac := hmac.New(sha256.New, []byte(apiKey))
	mac.Write([]byte(label + value))
	return hex.EncodeToString(mac.Sum(nil))
}

func verify(label, apiKey, value, got string) bool {
	if apiKey == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(proof(label, apiKey, value)), []byte(got)) == 1
}

// TunnelProof returns HMAC-SHA256(apiKey, label || ticket) in hex, for the
// reverse connection of a ticket.
func TunnelProof(apiKey, ticket string) string { return proof(ticketLabel, apiKey, ticket) }

// VerifyTunnelProof reports whether got is the correct proof for ticket under
// apiKey. A missing key never verifies. The comparison takes constant time.
func VerifyTunnelProof(apiKey, ticket, got string) bool {
	return verify(ticketLabel, apiKey, ticket, got)
}

// MuxProof returns the proof for a mux connection with the given nonce.
func MuxProof(apiKey, nonce string) string { return proof(muxLabel, apiKey, nonce) }

// VerifyMuxProof reports whether got is the correct proof for nonce under apiKey.
func VerifyMuxProof(apiKey, nonce, got string) bool {
	return verify(muxLabel, apiKey, nonce, got)
}

// NewMuxNonce returns a fresh nonce: the current time in milliseconds, then 128
// random bits, both in hex.
func NewMuxNonce(now time.Time) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("%012x", now.UnixMilli()) + hex.EncodeToString(random), nil
}

// MuxNonceTime returns the time a nonce was made, or false if it is malformed
// or outside the window around now.
func MuxNonceTime(nonce string, now time.Time) (time.Time, bool) {
	if len(nonce) != nonceLength {
		return time.Time{}, false
	}
	var ms int64
	for _, c := range nonce[:12] {
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = byte(c - '0')
		case c >= 'a' && c <= 'f':
			v = byte(c - 'a' + 10)
		default:
			return time.Time{}, false
		}
		ms = ms<<4 | int64(v)
	}
	for _, c := range nonce[12:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return time.Time{}, false
		}
	}
	t := time.UnixMilli(ms)
	age := now.Sub(t)
	if age > MuxNonceWindow || age < -MuxNonceWindow {
		return time.Time{}, false
	}
	return t, true
}

// relayLabel separates relay proofs from the other uses of the key.
const relayLabel = "lipstick-relay-v1:"

// RelayNonceHeader and RelayPortHeader carry a relayed visitor between servers.
const (
	RelayNonceHeader = "X-Lipstick-Relay-Nonce"
	RelayPortHeader  = "X-Lipstick-Relay-Port"
)

// RelayProof returns the proof a server sends to a peer when it relays a visitor.
// It is made with the domain key, so only a server that can read that key can
// hand visitors over.
func RelayProof(apiKey, nonce string) string { return proof(relayLabel, apiKey, nonce) }

// VerifyRelayProof reports whether got is the correct relay proof for nonce under apiKey.
func VerifyRelayProof(apiKey, nonce, got string) bool { return verify(relayLabel, apiKey, nonce, got) }
