package manager

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
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
	// statusClass counts finished requests by status class: index 1 for 1xx ... 5 for 5xx,
	// 0 for anything else. It counts every request, not just the ones in the ring.
	statusClass [6]atomic.Int64
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
	l.statusClass[statusClassOf(r.Status)].Add(1)
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
	// The database is the source for every query, so every server answers the same.
	// Requests that finished less than a couple of seconds ago may not be in it yet.
	if m.persist == nil {
		return nil, ErrHistoryUnavailable
	}
	return m.persist.store.Query(q)
}

// ErrHistoryUnavailable means a time range was asked for but no database is
// configured to hold the history.
var ErrHistoryUnavailable = errors.New("request history is not stored: no database persistence")

// statusClassOf maps a status to its index in statusClass: 1 to 5 for 1xx to 5xx, else 0.
func statusClassOf(status int) int {
	if status < 100 || status > 599 {
		return 0
	}
	return status / 100
}

// StatusCounts returns the finished requests per status class, indexed like statusClass.
func (l *RequestLog) StatusCounts() [6]int64 {
	var out [6]int64
	for i := range l.statusClass {
		out[i] = l.statusClass[i].Load()
	}
	return out
}
