package manager

import (
	"errors"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/server/auth"
)

const (
	appDomain   = "app.example.com"
	otherDomain = "other.example.com"
	appKey      = "app-key-0123456789abcdef0123"
	otherKey    = "other-key-0123456789abcdef012"
)

// twoDomains is a manager with two domains, each with its own key and its own hub.
func twoDomains(t *testing.T) *Manager {
	t.Helper()
	m := &Manager{authManager: &fakeAuth{domains: map[string]*auth.Domain{
		appDomain:   {Name: appDomain, ApiKey: appKey, AllowMultipleConnections: true},
		otherDomain: {Name: otherDomain, ApiKey: otherKey, AllowMultipleConnections: true},
	}}}
	m.getOrCreateHub(appDomain)
	m.getOrCreateHub(otherDomain)
	return m
}

// refusedWithin reports whether the server closed its side of the connection, without
// sending anything, within d. A connection left open reads as a timeout instead.
func refusedWithin(peer net.Conn, d time.Duration) bool {
	peer.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 64)
	n, err := peer.Read(buf)
	return n == 0 && err != nil && !errors.Is(err, os.ErrDeadlineExceeded) && !isTimeout(err)
}

// A reverse connection proves the key of the domain in its Host header. A key of another
// domain must not open this domain's tunnel, and the right key must.
func TestTunnelProofBelongsToItsDomain(t *testing.T) {
	m := twoDomains(t)
	ticket := "ticket-for-the-other-domain"

	server, peer := net.Pipe()
	defer peer.Close()
	req := &http.Request{Host: otherDomain, Header: http.Header{}}
	req.Header.Set(helper.ProofHeader, helper.TunnelProof(appKey, ticket)) // the app's key, sent for the other domain
	go m.handleTunnel(server, req, ticket)
	if !refusedWithin(peer, 2*time.Second) {
		t.Fatal("a key of another domain opened this domain's tunnel")
	}

	server2, peer2 := net.Pipe()
	defer peer2.Close()
	req2 := &http.Request{Host: otherDomain, Header: http.Header{}}
	req2.Header.Set(helper.ProofHeader, helper.TunnelProof(otherKey, ticket))
	go m.handleTunnel(server2, req2, ticket)
	if refusedWithin(peer2, 300*time.Millisecond) {
		t.Fatal("the right key was refused")
	}
	server2.Close()
}

// The same rule for the multiplexed connection, which is the one clients use for traffic.
func TestMuxProofBelongsToItsDomain(t *testing.T) {
	m := twoDomains(t)
	nonce, err := helper.NewMuxNonce(time.Now())
	if err != nil {
		t.Fatal(err)
	}

	server, peer := net.Pipe()
	defer peer.Close()
	req := &http.Request{Host: otherDomain, Header: http.Header{}}
	req.Header.Set(helper.MuxNonceHeader, nonce)
	req.Header.Set(helper.ProofHeader, helper.MuxProof(appKey, nonce))
	go m.handleMux(server, req)
	if !refusedWithin(peer, 2*time.Second) {
		t.Fatal("a key of another domain opened this domain's mux session")
	}

	server2, peer2 := net.Pipe()
	defer peer2.Close()
	req2 := &http.Request{Host: otherDomain, Header: http.Header{}}
	req2.Header.Set(helper.MuxNonceHeader, nonce)
	req2.Header.Set(helper.ProofHeader, helper.MuxProof(otherKey, nonce))
	go m.handleMux(server2, req2)
	if refusedWithin(peer2, 300*time.Millisecond) {
		t.Fatal("the right key was refused")
	}
	server2.Close()
}
