package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/gin-gonic/gin"
)

func managerEngine(domains map[string]*auth.Domain) *gin.Engine {
	m := &Manager{authManager: &fakeAuth{domains: domains}}
	configureRouter(m)
	return m.engine
}

func get(e *gin.Engine, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// Health must not reveal whether a domain exists or how much it transfers.
func TestHealthIsIdenticalForAnyDomain(t *testing.T) {
	e := managerEngine(map[string]*auth.Domain{
		"app.example.com": {Name: "app.example.com", ApiKey: "k"},
	})
	known := get(e, "app.example.com:5051", "/health")
	unknown := get(e, "nope.example.com:5051", "/health")

	if known.Code != http.StatusOK || unknown.Code != http.StatusOK {
		t.Fatalf("health codes differ: known=%d unknown=%d", known.Code, unknown.Code)
	}
	if known.Body.String() != unknown.Body.String() {
		t.Fatalf("health bodies differ: %q vs %q", known.Body.String(), unknown.Body.String())
	}
	if strings.Contains(known.Body.String(), "domain") || strings.Contains(known.Body.String(), "data_usage") {
		t.Fatalf("health leaks domain details: %s", known.Body.String())
	}
}

// Traffic statistics moved to the admin API, which requires the admin key.
func TestManagerNoLongerServesTraffic(t *testing.T) {
	e := managerEngine(map[string]*auth.Domain{})
	if rec := get(e, "app.example.com:5051", "/traffic?from=2026-01-01&to=2026-01-31"); rec.Code == http.StatusOK {
		t.Fatalf("/traffic on the manager port must not answer 200, got %d", rec.Code)
	}
}
