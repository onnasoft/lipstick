package manager

import (
	"errors"
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
	Port          int       `json:"port"` // proxy port or an intranet port; 0 when unknown
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
	hook    func(RequestRecord) // called after each Add, outside the lock
}

// setHook sets the function called for every added record, such as the
// persister's queue.
func (l *RequestLog) setHook(fn func(RequestRecord)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hook = fn
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
	if r.Port < 0 || r.Port > 65535 {
		r.Port = 0
	}

	l.mu.Lock()
	l.records[l.next] = r
	l.next = (l.next + 1) % len(l.records)
	if l.next == 0 {
		l.full = true
	}
	hook := l.hook
	l.mu.Unlock()

	if hook != nil {
		hook(r)
	}
}

// Recent returns up to q.Limit records, newest first, that match q. An empty
// domain matches all domains; a zero port matches all ports.
func (l *RequestLog) Recent(q RequestQuery) []RequestRecord {
	l.mu.Lock()
	defer l.mu.Unlock()

	n := l.next
	if l.full {
		n = len(l.records)
	}
	out := make([]RequestRecord, 0, q.Limit)
	for i := 0; i < n && len(out) < q.Limit; i++ {
		idx := (l.next - 1 - i + len(l.records)) % len(l.records)
		r := l.records[idx]
		if q.Domain != "" && r.Domain != q.Domain {
			continue
		}
		if q.Port != 0 && r.Port != q.Port {
			continue
		}
		out = append(out, r)
	}
	return out
}

// RecentRequests answers the admin API. With a time range it reads the database,
// which keeps history across restarts. Without one it reads the in-memory ring,
// which is always current, including requests not yet written.
func (m *Manager) RecentRequests(q RequestQuery) ([]RequestRecord, error) {
	if !q.From.IsZero() || !q.To.IsZero() {
		if m.persist == nil {
			return nil, ErrHistoryUnavailable
		}
		return m.persist.store.Query(q)
	}
	if m.reqlog == nil {
		return []RequestRecord{}, nil
	}
	return m.reqlog.Recent(q), nil
}

// ErrHistoryUnavailable means a time range was asked for but no database is
// configured to hold the history.
var ErrHistoryUnavailable = errors.New("request history is not stored: no database persistence")
