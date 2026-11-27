package helper

import (
	"strings"
	"testing"
	"time"
)

const testTicket = "3f9a0c5e1b2d4f60a7c8e9d0b1a2c3d4"

func TestTunnelProofVerifies(t *testing.T) {
	if !VerifyTunnelProof("domain-key-123456", testTicket, TunnelProof("domain-key-123456", testTicket)) {
		t.Fatal("correct proof rejected")
	}
}

// A proof is bound to its key and its ticket: changing either one fails.
func TestTunnelProofIsBoundToKeyAndTicket(t *testing.T) {
	proof := TunnelProof("domain-key-123456", testTicket)
	if VerifyTunnelProof("other-key-654321", testTicket, proof) {
		t.Fatal("proof accepted with another key")
	}
	if VerifyTunnelProof("domain-key-123456", "00000000000000000000000000000000", proof) {
		t.Fatal("proof accepted for another ticket")
	}
}

func TestTunnelProofRejectsMissingOrEmpty(t *testing.T) {
	if VerifyTunnelProof("domain-key-123456", testTicket, "") {
		t.Fatal("empty proof accepted")
	}
	if VerifyTunnelProof("", testTicket, TunnelProof("", testTicket)) {
		t.Fatal("domain with empty key accepted a proof")
	}
	if VerifyTunnelProof("domain-key-123456", testTicket, "zz") {
		t.Fatal("garbage proof accepted")
	}
}

// The raw key must not work as a proof, so a leaked key header is not a shortcut.
func TestTunnelProofIsNotTheKey(t *testing.T) {
	if VerifyTunnelProof("domain-key-123456", testTicket, "domain-key-123456") {
		t.Fatal("the key itself accepted as proof")
	}
}

// A tunnel proof for a value is not a valid mux proof for the same value.
func TestProofLabelsAreSeparate(t *testing.T) {
	value := "3f9a0c5e1b2d4f60a7c8e9d0b1a2c3d4"
	if VerifyMuxProof("domain-key-123456", value, TunnelProof("domain-key-123456", value)) {
		t.Fatal("tunnel proof accepted as mux proof")
	}
}

func TestMuxProofVerifies(t *testing.T) {
	nonce, err := NewMuxNonce(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyMuxProof("domain-key-123456", nonce, MuxProof("domain-key-123456", nonce)) {
		t.Fatal("correct mux proof rejected")
	}
	if VerifyMuxProof("other-key-654321", nonce, MuxProof("domain-key-123456", nonce)) {
		t.Fatal("mux proof accepted with another key")
	}
}

func TestNewMuxNonceHasTimeAndRandomness(t *testing.T) {
	now := time.Now()
	a, _ := NewMuxNonce(now)
	b, _ := NewMuxNonce(now)
	if len(a) != nonceLength || a == b {
		t.Fatalf("nonces %q and %q: want length %d and different values", a, b, nonceLength)
	}
	if _, ok := MuxNonceTime(a, now); !ok {
		t.Fatal("fresh nonce not accepted")
	}
}

// Nonces outside the window, malformed nonces, and nonces from the future are refused.
func TestMuxNonceTimeWindow(t *testing.T) {
	now := time.Now()
	old, _ := NewMuxNonce(now.Add(-MuxNonceWindow - time.Second))
	future, _ := NewMuxNonce(now.Add(MuxNonceWindow + time.Second))
	for name, n := range map[string]string{"old": old, "future": future,
		"short": "abc", "not hex": strings.Repeat("z", nonceLength)} {
		if _, ok := MuxNonceTime(n, now); ok {
			t.Errorf("%s nonce accepted", name)
		}
	}
}

// A relay proof is not valid as a mux or ticket proof for the same value.
func TestRelayProofLabelIsSeparate(t *testing.T) {
	nonce, _ := NewMuxNonce(time.Now())
	key := "domain-key-123456"
	if !VerifyRelayProof(key, nonce, RelayProof(key, nonce)) {
		t.Fatal("correct relay proof rejected")
	}
	if VerifyMuxProof(key, nonce, RelayProof(key, nonce)) || VerifyTunnelProof(key, nonce, RelayProof(key, nonce)) {
		t.Fatal("relay proof accepted for another use")
	}
	if VerifyRelayProof("other-key-654321", nonce, RelayProof(key, nonce)) {
		t.Fatal("relay proof accepted with another key")
	}
}
