package manager

import (
	"testing"
	"time"
)

// A hub with no client is idle, with the counters at zero, and answers from its loop.
func TestSnapshotReportsIdleHub(t *testing.T) {
	m := &Manager{}
	m.getOrCreateHub("app.example.com")
	defer m.DropHub("app.example.com")

	snap := m.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("got %d domains, want 1", len(snap))
	}
	d := snap[0]
	if d.Domain != "app.example.com" || d.Status != "idle" || d.Clients != 0 || d.ActiveSessions != 0 {
		t.Fatalf("unexpected stats: %+v", d)
	}
}

// Sessions are counted while they run, and go back to zero when they end.
func TestActiveSessionsAreCounted(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 1<<40)
	go hub.listen()
	defer hub.stop()

	visitorSrv, visitorCli := tcpPair(t)
	tunnelSrv, tunnelCli := tcpPair(t)
	done := make(chan struct{})
	go func() { hub.syncConnections(visitorSrv, tunnelSrv); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		st, ok := hub.Stats(time.Second)
		if !ok {
			t.Fatal("hub did not answer")
		}
		if st.ActiveSessions == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("active sessions = %d, want 1", st.ActiveSessions)
		}
		time.Sleep(10 * time.Millisecond)
	}

	visitorCli.Close()
	tunnelCli.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end")
	}
	st, _ := hub.Stats(time.Second)
	if st.ActiveSessions != 0 {
		t.Fatalf("active sessions after close = %d, want 0", st.ActiveSessions)
	}
}

// A stopped hub must not hang the snapshot.
func TestStatsOfStoppedHubDoNotHang(t *testing.T) {
	hub := NewNetworkHub("app.example.com", nil, 1<<40)
	hub.stop()
	start := time.Now()
	if _, ok := hub.Stats(time.Second); ok {
		t.Fatal("stopped hub answered")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("stats waited too long on a stopped hub")
	}
}

// With no domains, the snapshot is an empty slice, not nil: nil encodes as JSON
// null, which the admin panel cannot read.
func TestSnapshotWithoutDomainsIsEmptyNotNil(t *testing.T) {
	m := &Manager{}
	if got := m.Snapshot(); got == nil || len(got) != 0 {
		t.Fatalf("Snapshot() = %#v, want empty non-nil slice", got)
	}
}
