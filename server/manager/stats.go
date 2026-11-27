package manager

import (
	"sort"
	"time"
)

// statsTimeout bounds how long the snapshot waits for each hub. A hub that does
// not answer is reported as unresponsive instead of blocking the panel.
const statsTimeout = 2 * time.Second

// DomainStats is what the admin panel shows for one domain.
type DomainStats struct {
	Domain          string `json:"domain"`
	Status          string `json:"status"` // "active", "idle" or "unresponsive"
	Clients         int    `json:"clients"`
	ActiveSessions  int64  `json:"activeSessions"`
	PendingVisitors int    `json:"pendingVisitors"`
	BytesSinceStart int64  `json:"bytesSinceStart"`
	IntranetPorts   []int  `json:"intranetPorts"`
}

// Snapshot returns the live state of every hub, sorted by domain. It is safe to
// call while traffic flows: each hub answers from its own loop.
func (m *Manager) Snapshot() []DomainStats {
	out := []DomainStats{} // never nil: JSON must be [] when there are no domains
	m.hubs.Range(func(key, value any) bool {
		hub := value.(*NetworkHub)
		st, ok := hub.Stats(statsTimeout)
		ds := DomainStats{Domain: key.(string), IntranetPorts: m.intranetPortsOf(key.(string))}
		if !ok {
			ds.Status = "unresponsive"
		} else {
			ds.Clients = st.Clients
			ds.ActiveSessions = st.ActiveSessions
			ds.PendingVisitors = st.PendingVisitors
			ds.BytesSinceStart = st.BytesSinceStart
			ds.Status = "idle"
			if st.Clients > 0 {
				ds.Status = "active"
			}
		}
		out = append(out, ds)
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out
}

// intranetPortsOf lists the intranet ports a domain holds, sorted.
func (m *Manager) intranetPortsOf(domain string) []int {
	p := m.intranet
	if p == nil {
		return []int{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ports := []int{}
	for port, r := range p.reserved {
		if r.domain == domain {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports
}
