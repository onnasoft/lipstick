package admin

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/OnnaSoft/lipstick/server/audit"
)

// memAudit keeps the audit log in memory. failBegin makes every new entry fail, to
// prove that a change is refused when it cannot be recorded.
type memAudit struct {
	mu        sync.Mutex
	events    []audit.Event
	nextID    uint
	failBegin bool
}

var errAuditDown = errors.New("audit store down")

func newMemAudit() *memAudit { return &memAudit{} }

func (m *memAudit) Begin(e audit.Entry) (uint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failBegin {
		return 0, errAuditDown
	}
	m.nextID++
	m.events = append(m.events, audit.Event{
		ID: m.nextID, Actor: e.Actor, Action: e.Action, Target: e.Target,
		Outcome: audit.OutcomePending, ClientAddr: e.ClientAddr, UserAgent: e.UserAgent,
		Details: map[string]any{},
	})
	return m.nextID, nil
}

func (m *memAudit) Finish(id uint, r audit.Result) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.events {
		if m.events[i].ID == id {
			m.events[i].Outcome = r.Outcome
			m.events[i].StatusCode = r.StatusCode
			if r.Target != "" {
				m.events[i].Target = r.Target
			}
			if r.Actor != "" {
				m.events[i].Actor = r.Actor
			}
			if r.Details != nil {
				m.events[i].Details = r.Details
			}
			return nil
		}
	}
	return audit.ErrNotFound
}

func (m *memAudit) Search(q audit.Query) ([]audit.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []audit.Event
	for i := len(m.events) - 1; i >= 0; i-- {
		e := m.events[i]
		if q.Action != "" && e.Action != q.Action {
			continue
		}
		if q.Actor != "" && e.Actor != q.Actor {
			continue
		}
		if q.Outcome != "" && e.Outcome != q.Outcome {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// all returns the events as JSON-safe text, for checks that no secret appears in them.
func (m *memAudit) text() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var parts []string
	for _, e := range m.events {
		parts = append(parts, e.Actor, e.Action, e.Target, e.Outcome)
		keys := make([]string, 0, len(e.Details))
		for k := range e.Details {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts = append(parts, strings.Join(keys, ","))
	}
	return strings.Join(parts, "|")
}
