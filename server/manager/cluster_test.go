package manager

import (
	"strings"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
)

func newTestCluster(self string) *cluster {
	return &cluster{advertise: self, peers: make(map[string]map[string]time.Time)}
}

// A server that reported sessions is a peer, while a server that reported none is not.
func TestClusterFindsPeerWithSessions(t *testing.T) {
	c := newTestCluster("a.internal:5051")
	now := time.Now()
	c.observe(presenceMsg{Server: "b.internal:5051", Domain: "robot.example.com", Sessions: 2}, now)
	if peer, ok := c.peerFor("robot.example.com", now); !ok || peer != "b.internal:5051" {
		t.Fatalf("peer %q, %v; want b.internal:5051", peer, ok)
	}
	c.observe(presenceMsg{Server: "b.internal:5051", Domain: "robot.example.com", Sessions: 0}, now)
	if _, ok := c.peerFor("robot.example.com", now); ok {
		t.Fatal("server without sessions still listed as peer")
	}
}

// A server's own presence is never a peer: relaying to itself would loop.
func TestClusterIgnoresOwnPresence(t *testing.T) {
	c := newTestCluster("a.internal:5051")
	c.observe(presenceMsg{Server: "a.internal:5051", Domain: "robot.example.com", Sessions: 1}, time.Now())
	if _, ok := c.peerFor("robot.example.com", time.Now()); ok {
		t.Fatal("own presence used as a peer")
	}
}

// Presence that was not renewed within the TTL is dropped.
func TestClusterForgetsStalePeers(t *testing.T) {
	c := newTestCluster("a.internal:5051")
	old := time.Now().Add(-presenceTTL - time.Second)
	c.observe(presenceMsg{Server: "b.internal:5051", Domain: "robot.example.com", Sessions: 1}, old)
	if _, ok := c.peerFor("robot.example.com", time.Now()); ok {
		t.Fatal("stale presence still used")
	}
}

// Among several peers, the most recently seen one is chosen.
func TestClusterPrefersFreshestPeer(t *testing.T) {
	c := newTestCluster("a.internal:5051")
	now := time.Now()
	c.observe(presenceMsg{Server: "b.internal:5051", Domain: "d", Sessions: 1}, now.Add(-2*time.Second))
	c.observe(presenceMsg{Server: "c.internal:5051", Domain: "d", Sessions: 1}, now)
	if peer, _ := c.peerFor("d", now); peer != "c.internal:5051" {
		t.Fatalf("peer %q, want the freshest c.internal:5051", peer)
	}
}

// Without a cluster, a visitor that this server cannot serve gets 502.
func TestRouteWithoutClusterRejectsVisitor(t *testing.T) {
	manager := &Manager{}
	visitor, peer := visitorPair(t)
	manager.routeVisitor("robot.example.com", &helper.RemoteConn{Conn: visitor, Domain: "robot.example.com"}, 5050)
	if got := readAll(t, peer); !strings.Contains(got, "502") {
		t.Fatalf("visitor got %q, want 502", got)
	}
}
