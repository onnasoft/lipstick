package manager

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/server/db"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// sharedDB opens one SQLite file, as two servers of a cluster share one PostgreSQL.
// Each call returns a new connection to the same file.
func sharedDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&db.NodeStatus{}, &db.RequestLogEntry{}); err != nil {
		t.Fatal(err)
	}
	return conn
}

// Two servers report, and a third connection (another admin) reads the same totals.
func TestClusterStateIsTheSameFromAnyServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster.db")
	nodeA := sharedDB(t, path)
	nodeB := sharedDB(t, path)
	adminOnB := sharedDB(t, path)
	now := time.Now()

	if err := PublishNodeStatus(nodeA, "a/:5051", []DomainStats{
		{Domain: "app.example.com", Status: "active", Clients: 1, ActiveSessions: 2, BytesSinceStart: 100, IntranetPorts: []string{"5105/tcp"}},
		{Domain: "api.example.com", Status: "idle", Clients: 0, BytesSinceStart: 7},
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := PublishNodeStatus(nodeB, "b/:5051", []DomainStats{
		{Domain: "app.example.com", Status: "idle", Clients: 1, ActiveSessions: 1, BytesSinceStart: 50, IntranetPorts: []string{"5106/tcp", "5105/tcp"}},
	}, now); err != nil {
		t.Fatal(err)
	}

	fromA, err := ReadClusterStatus(nodeA, now)
	if err != nil {
		t.Fatal(err)
	}
	fromAdminOnB, err := ReadClusterStatus(adminOnB, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromA, fromAdminOnB) {
		t.Fatalf("two servers answer differently:\n%+v\n%+v", fromA, fromAdminOnB)
	}
	if len(fromA) != 2 || fromA[0].Domain != "api.example.com" {
		t.Fatalf("expected both domains sorted by name: %+v", fromA)
	}
	app := fromA[1]
	if app.Domain != "app.example.com" || app.Status != "active" || app.Clients != 2 || app.ActiveSessions != 3 || app.BytesSinceStart != 150 {
		t.Fatalf("totals for app: %+v", app)
	}
	if !reflect.DeepEqual(app.IntranetPorts, []string{"5105/tcp", "5106/tcp"}) {
		t.Fatalf("ports not joined and sorted: %v", app.IntranetPorts)
	}
}

// A server that stops reporting drops out of the totals after the freshness window.
func TestSilentServerDropsOut(t *testing.T) {
	conn := sharedDB(t, filepath.Join(t.TempDir(), "silent.db"))
	then := time.Now().Add(-NodeStatusFreshness - time.Second)
	if err := PublishNodeStatus(conn, "gone/:5051", []DomainStats{{Domain: "x.example.com", Status: "active", Clients: 3}}, then); err != nil {
		t.Fatal(err)
	}
	got, err := ReadClusterStatus(conn, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a silent server still counts: %+v", got)
	}
}

// A server drops the domains it no longer serves, and removes all of them when it stops.
func TestServerRemovesWhatItStopsServing(t *testing.T) {
	conn := sharedDB(t, filepath.Join(t.TempDir(), "remove.db"))
	now := time.Now()
	PublishNodeStatus(conn, "a/:5051", []DomainStats{{Domain: "x.example.com", Status: "idle"}, {Domain: "y.example.com", Status: "idle"}}, now)
	PublishNodeStatus(conn, "a/:5051", []DomainStats{{Domain: "x.example.com", Status: "idle"}}, now)
	got, _ := ReadClusterStatus(conn, now)
	if len(got) != 1 || got[0].Domain != "x.example.com" {
		t.Fatalf("after dropping y: %+v", got)
	}
	PublishNodeStatus(conn, "a/:5051", nil, now)
	got, _ = ReadClusterStatus(conn, now)
	if len(got) != 0 {
		t.Fatalf("after stopping: %+v", got)
	}
}

// Recent requests are read from the database, newest first, so they match on every server.
func TestRecentRequestsComeFromTheDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	writer := sharedDB(t, path)
	reader := sharedDB(t, path)
	base := time.Now().Add(-time.Minute)
	store := NewGormRequestStore(writer)
	if err := store.SaveBatch([]RequestRecord{
		{Time: base, Domain: "app.example.com", Port: 5050, Method: "GET", Path: "/old", Status: 200},
		{Time: base.Add(10 * time.Second), Domain: "app.example.com", Port: 5050, Method: "POST", Path: "/new", Status: 201},
		{Time: base.Add(5 * time.Second), Domain: "api.example.com", Port: 5050, Method: "GET", Path: "/other", Status: 404},
	}); err != nil {
		t.Fatal(err)
	}

	m := &Manager{persist: &persister{store: NewGormRequestStore(reader)}}
	got, err := m.RecentRequests(RequestQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Path != "/new" || got[2].Path != "/old" {
		t.Fatalf("not newest first: %+v", got)
	}
	only, _ := m.RecentRequests(RequestQuery{Domain: "api.example.com", Limit: 10})
	if len(only) != 1 || only[0].Path != "/other" {
		t.Fatalf("domain filter: %+v", only)
	}

	noDB := &Manager{}
	if _, err := noDB.RecentRequests(RequestQuery{Limit: 10}); err != ErrHistoryUnavailable {
		t.Fatalf("without a store: %v", err)
	}
}
