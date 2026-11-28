package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func readyOf(checks map[string]func() error, conditions func() []string) *Admin {
	a := healthOf(checks)
	a.SetReadiness(conditions)
	return a
}

// Ready when the server's own conditions hold and every dependency answers.
func TestReadyWhenNothingIsUnmet(t *testing.T) {
	a := readyOf(map[string]func() error{"database": func() error { return nil }}, func() []string { return nil })
	rec := request(a, http.MethodGet, "/ready", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready: %d %s", rec.Code, rec.Body.String())
	}
}

// Draining is a reason even when every dependency is fine: the server takes no new traffic.
func TestNotReadyWhenDraining(t *testing.T) {
	a := readyOf(map[string]func() error{"database": func() error { return nil }}, func() []string { return []string{"draining"} })
	rec := request(a, http.MethodGet, "/ready", "", nil)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"draining"`) {
		t.Fatalf("draining: %d %s", rec.Code, rec.Body.String())
	}
}

// A failing dependency makes /ready answer 503 and name it, without the error text.
func TestNotReadyNamesTheDownDependency(t *testing.T) {
	a := readyOf(map[string]func() error{
		"database": func() error { return errors.New("password authentication failed at 10.0.0.5") },
	}, func() []string { return nil })
	rec := request(a, http.MethodGet, "/ready", "", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(body, `"database"`) {
		t.Fatalf("database down: %d %s", rec.Code, body)
	}
	if strings.Contains(body, "password") || strings.Contains(body, "10.0.0.5") {
		t.Fatalf("ready reveals the error: %s", body)
	}
}

// /ready is public, like /health, so a load balancer needs no session.
func TestReadyIsPublic(t *testing.T) {
	a := readyOf(map[string]func() error{}, func() []string { return nil })
	if rec := do(a, http.MethodGet, "/ready", ""); rec.Code != http.StatusOK {
		t.Fatalf("ready without session: %d", rec.Code)
	}
}
