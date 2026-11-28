package audit

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/server/db"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit.db")), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&db.AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	return NewStore(conn)
}

func TestEntryIsPendingThenCompleted(t *testing.T) {
	s := newTestStore(t)
	id, err := s.Begin(Entry{Actor: "jtorres", Action: "domain.create", Target: "", ClientAddr: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := s.Search(Query{})
	if len(pending) != 1 || pending[0].Outcome != OutcomePending {
		t.Fatalf("before the change: %+v", pending)
	}
	if err := s.Finish(id, Result{Target: "app.example.com", StatusCode: 200, Outcome: OutcomeSucceeded, Details: map[string]any{"fields": []string{"name"}}}); err != nil {
		t.Fatal(err)
	}
	done, _ := s.Search(Query{Action: "domain.create"})
	if done[0].Outcome != OutcomeSucceeded || done[0].Target != "app.example.com" || done[0].StatusCode != 200 {
		t.Fatalf("after the change: %+v", done[0])
	}
	if done[0].Details["fields"] == nil {
		t.Fatalf("details lost: %+v", done[0].Details)
	}
}

func TestFinishUnknownEntryFails(t *testing.T) {
	s := newTestStore(t)
	if err := s.Finish(999, Result{Outcome: OutcomeFailed}); err != ErrNotFound {
		t.Fatalf("unknown entry: %v", err)
	}
}

func TestSearchFiltersNewestFirstAndCapsTheLimit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		id, _ := s.Begin(Entry{Actor: "a", Action: "x.one"})
		s.Finish(id, Result{Outcome: OutcomeSucceeded, StatusCode: 200})
	}
	id, _ := s.Begin(Entry{Actor: "b", Action: "x.two"})
	s.Finish(id, Result{Outcome: OutcomeFailed, StatusCode: 403})

	byActor, _ := s.Search(Query{Actor: "a"})
	if len(byActor) != 3 {
		t.Fatalf("actor filter: %d", len(byActor))
	}
	failed, _ := s.Search(Query{Outcome: OutcomeFailed})
	if len(failed) != 1 || failed[0].Action != "x.two" {
		t.Fatalf("outcome filter: %+v", failed)
	}
	newest, _ := s.Search(Query{})
	if newest[0].Action != "x.two" {
		t.Fatalf("not newest first: %+v", newest[0])
	}
	future, _ := s.Search(Query{From: time.Now().Add(time.Hour)})
	if len(future) != 0 {
		t.Fatalf("time filter: %d", len(future))
	}
	many, _ := s.Search(Query{Limit: 100000})
	if len(many) != 4 {
		t.Fatalf("limit cap: %d", len(many))
	}
}
