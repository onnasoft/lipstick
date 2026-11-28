package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/server/manager"
)

func healthOf(checks map[string]func() error) *Admin {
	a := newTestAdmin(testKey)
	a.SetHealthChecks(checks)
	return a
}

func TestHealthIsOkWhenEveryDependencyAnswers(t *testing.T) {
	a := healthOf(map[string]func() error{"database": func() error { return nil }, "nats": func() error { return nil }})
	rec := request(a, http.MethodGet, "/health", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) || !strings.Contains(rec.Body.String(), `"database":"ok"`) {
		t.Fatalf("healthy: %d %s", rec.Code, rec.Body.String())
	}
}

// A failing dependency makes /health answer 503, and the answer names it without the error text.
func TestHealthIsDegradedWhenADependencyFails(t *testing.T) {
	a := healthOf(map[string]func() error{
		"database": func() error { return errors.New("password authentication failed for user lipstick at 10.0.0.5") },
		"nats":     func() error { return nil },
	})
	rec := request(a, http.MethodGet, "/health", "", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("degraded: %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"database":"down"`) || !strings.Contains(body, `"status":"degraded"`) {
		t.Fatalf("degraded body: %s", body)
	}
	if strings.Contains(body, "password") || strings.Contains(body, "10.0.0.5") {
		t.Fatalf("the health answer reveals the error: %s", body)
	}
}

// A check that hangs cannot hang /health: it is given up after its deadline.
func TestHungDependencyDoesNotHangHealth(t *testing.T) {
	a := healthOf(map[string]func() error{"database": func() error { time.Sleep(10 * time.Second); return nil }})
	start := time.Now()
	rec := request(a, http.MethodGet, "/health", "", nil)
	if rec.Code != http.StatusServiceUnavailable || time.Since(start) > 5*time.Second {
		t.Fatalf("hung check: %d after %v", rec.Code, time.Since(start))
	}
}

// The state of the cluster and the request history answer 503 when the database does not.
func TestClusterStateAnswers503WhenDatabaseIsDown(t *testing.T) {
	a := newTestAdmin(testKey)
	a.SetStatsSource(func() ([]manager.DomainStats, error) { return nil, errors.New("db down") })
	a.SetRequestSource(func(manager.RequestQuery) ([]manager.RequestRecord, error) { return nil, errors.New("db down") })
	if rec := request(a, http.MethodGet, "/stats", "", cookieFor(testKey)); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("stats: %d, want 503", rec.Code)
	}
	if rec := request(a, http.MethodGet, "/requests", "", cookieFor(testKey)); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("requests: %d, want 503", rec.Code)
	}
}
