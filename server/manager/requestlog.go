package manager

import (
	"strings"
	"sync"
	"time"
)

// Limits on what a client may report. The client is authenticated, but a bad
// or buggy client should not be able to fill memory with long values.
const (
	maxMethodLen = 16
	maxPathLen   = 512
)

// RequestRecord is one finished request, as the admin panel shows it. It has no
// headers and no bodies, so it cannot leak cookies or credentials.
type RequestRecord struct {
	Time          time.Time `json:"time"`
	Domain        string    `json:"domain"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	Status        int       `json:"status"`
	DurationMS    int64     `json:"durationMs"`
	ResponseBytes int64     `json:"responseBytes"`
}

// RequestLog keeps the most recent requests of all domains in a fixed-size ring.
// Memory use is bounded by its size, whatever the traffic.
type RequestLog struct {
	mu      sync.Mutex
	records []RequestRecord
	next    int
	full    bool
}

// NewRequestLog returns a log that keeps the last size requests.
func NewRequestLog(size int) *RequestLog {
	return &RequestLog{records: make([]RequestRecord, size)}
}

// Add stores a record. Values are cleaned before they are kept.
func (l *RequestLog) Add(r RequestRecord) {
	r.Method = strings.ToUpper(r.Method)
	if len(r.Method) > maxMethodLen {
		r.Method = r.Method[:maxMethodLen]
	}
	if len(r.Path) > maxPathLen {
		r.Path = r.Path[:maxPathLen]
	}
	if r.Status < 0 || r.Status > 999 {
		r.Status = 0
	}
	if r.DurationMS < 0 {
		r.DurationMS = 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.records[l.next] = r
	l.next = (l.next + 1) % len(l.records)
	if l.next == 0 {
		l.full = true
	}
}

// Recent returns up to limit records, newest first. An empty domain matches all
// domains.
func (l *RequestLog) Recent(domain string, limit int) []RequestRecord {
	l.mu.Lock()
	defer l.mu.Unlock()

	n := l.next
	if l.full {
		n = len(l.records)
	}
	out := make([]RequestRecord, 0, limit)
	for i := 0; i < n && len(out) < limit; i++ {
		idx := (l.next - 1 - i + len(l.records)) % len(l.records)
		r := l.records[idx]
		if domain != "" && r.Domain != domain {
			continue
		}
		out = append(out, r)
	}
	return out
}

// RecentRequests is the manager's view of the log, used by the admin API.
func (m *Manager) RecentRequests(domain string, limit int) []RequestRecord {
	if m.reqlog == nil {
		return []RequestRecord{}
	}
	return m.reqlog.Recent(domain, limit)
}
