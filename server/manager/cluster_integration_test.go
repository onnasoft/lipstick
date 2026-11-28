package manager

import (
	"testing"
	"time"
)

// Scenario: two servers share one database and one presence bus. The client is on
// the first; a visitor enters through the second. The second relays the visit to the
// first over the cluster, and the answer comes back through the same path.
func TestIntegrationClusterRelaysBetweenServers(t *testing.T) {
	saved := presenceEvery
	presenceEvery = 100 * time.Millisecond
	defer func() { presenceEvery = saved }()

	database := newTestDB(t)
	bus := newMemBus()
	a := startStack(t, stackOptions{db: database, bus: bus})
	defer a.close()
	b := startStack(t, stackOptions{db: database, bus: bus})
	defer b.close()

	a.runClient(clientConfig{ProxyPass: localHTTP(t)})

	// Wait until the second server has learned that the first one serves the domain.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := b.manager.cluster.peerFor(testDomain, time.Now()); ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, ok := b.manager.cluster.peerFor(testDomain, time.Now()); !ok {
		t.Fatal("the second server never learned about the first one's sessions")
	}

	c := b.visitorClient(t, b.caFile)
	resp, body := getBody(t, c, "/hello")
	if resp.StatusCode != 200 || body != "hello from the local service" {
		t.Fatalf("visit through the second server: %d %q", resp.StatusCode, body)
	}
}
