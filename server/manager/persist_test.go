package manager

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// memStore is a RequestStore for tests. It can be made to fail.
type memStore struct {
	mu     sync.Mutex
	rows   []RequestRecord
	fail   bool
	saves  int
	pruned int
}

func (s *memStore) SaveBatch(records []RequestRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	if s.fail {
		return errors.New("database down")
	}
	s.rows = append(s.rows, records...)
	return nil
}

func (s *memStore) Query(q RequestQuery) ([]RequestRecord, error) { return nil, nil }

func (s *memStore) DeleteOlderThan(time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruned++
	return 0, nil
}

func (s *memStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

// Close writes everything still queued, even when the flush timer has not fired.
func TestPersisterFlushesQueueOnClose(t *testing.T) {
	store := &memStore{}
	p := newPersister(store, 0)
	go p.run()
	for i := 0; i < 7; i++ {
		p.enqueue(RequestRecord{Domain: "a", Path: "/p"})
	}
	p.Close()
	if n := store.count(); n != 7 {
		t.Fatalf("stored %d rows after close, want 7", n)
	}
}

// When the database is down, rows are kept and written once it is back.
func TestPersisterKeepsRowsWhileStoreIsDown(t *testing.T) {
	store := &memStore{fail: true}
	p := newPersister(store, 0)
	go p.run()
	p.enqueue(RequestRecord{Domain: "a", Path: "/1"})
	time.Sleep(persistFlushEvery + 500*time.Millisecond) // first attempt fails

	store.mu.Lock()
	store.fail = false
	store.mu.Unlock()
	p.Close() // final attempt must succeed and include the kept row

	if n := store.count(); n != 1 {
		t.Fatalf("row lost during outage: stored %d", n)
	}
}

// A full queue drops and counts, and never blocks the caller.
func TestPersisterDropsWhenQueueIsFull(t *testing.T) {
	p := newPersister(&memStore{}, 0) // run() not started, so nothing drains
	for i := 0; i < persistQueueSize+5; i++ {
		p.enqueue(RequestRecord{})
	}
	if d := p.Dropped(); d != 5 {
		t.Fatalf("dropped %d, want 5", d)
	}
}

// The request log hands each record to the persister, and only once.
func TestRequestLogHookReceivesRecords(t *testing.T) {
	l := NewRequestLog(10)
	var got []RequestRecord
	l.setHook(func(r RequestRecord) { got = append(got, r) })
	l.Add(RequestRecord{Domain: "a", Path: "/x"})
	if len(got) != 1 || got[0].Path != "/x" {
		t.Fatalf("hook got %+v", got)
	}
}
