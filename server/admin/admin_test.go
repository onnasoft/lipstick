package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OnnaSoft/lipstick/server/auth"
)

const testKey = "0123456789abcdef-admin"
const clientSecret = "tunnel-client-secret-do-not-leak"

type fakeAuth struct{}

func (fakeAuth) GetDomains() ([]*auth.Domain, error) {
	return []*auth.Domain{{ID: 1, Name: "app.example.com", ApiKey: clientSecret, AllowMultipleConnections: true}}, nil
}
func (fakeAuth) GetDomain(name string) (*auth.Domain, error) {
	return &auth.Domain{ID: 1, Name: name, ApiKey: clientSecret}, nil
}
func (fakeAuth) AddDomain(*auth.Domain) error    { return nil }
func (fakeAuth) UpdateDomain(*auth.Domain) error { return nil }
func (fakeAuth) DelDomain(uint) error            { return nil }

func newTestAdmin(key string) *Admin {
	a := &Admin{authManager: fakeAuth{}, adminKey: key}
	configureRouter(a)
	return a
}

func do(a *Admin, method, path, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	a.engine.ServeHTTP(rec, req)
	return rec
}

func TestAdminRejectsMissingOrWrongKey(t *testing.T) {
	a := newTestAdmin(testKey)
	for _, h := range []string{"", "wrong", testKey + "x", "Bearer " + testKey} {
		if rec := do(a, http.MethodGet, "/domains", h); rec.Code != http.StatusUnauthorized {
			t.Errorf("header %q: got %d, want 401", h, rec.Code)
		}
	}
}

func TestAdminEmptyKeyNeverAuthorizes(t *testing.T) {
	a := newTestAdmin("")
	if rec := do(a, http.MethodGet, "/domains", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("empty configured key authorized an empty header: got %d", rec.Code)
	}
}

func TestAdminListsDomainsWithoutApiKey(t *testing.T) {
	a := newTestAdmin(testKey)
	rec := do(a, http.MethodGet, "/domains", testKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct key: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, clientSecret) || strings.Contains(body, "apiKey") {
		t.Fatalf("list leaks the tunnel key: %s", body)
	}
	if !strings.Contains(body, "app.example.com") {
		t.Fatalf("list missing the domain name: %s", body)
	}
}

func TestAdminGetDomainWithoutApiKey(t *testing.T) {
	a := newTestAdmin(testKey)
	rec := do(a, http.MethodGet, "/domains/app.example.com", testKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, clientSecret) || strings.Contains(body, "apiKey") {
		t.Fatalf("get leaks the tunnel key: %s", body)
	}
}

func TestAdminHealthIsPublic(t *testing.T) {
	a := newTestAdmin(testKey)
	if rec := do(a, http.MethodGet, "/health", ""); rec.Code != http.StatusOK {
		t.Fatalf("health got %d, want 200", rec.Code)
	}
}

func TestTrafficRequiresAdminKey(t *testing.T) {
	a := newTestAdmin(testKey)
	if rec := do(a, http.MethodGet, "/domains/app.example.com/traffic?from=2026-01-01&to=2026-01-31", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("traffic without key: got %d, want 401", rec.Code)
	}
}

func TestTrafficValidatesDatesBeforeDatabase(t *testing.T) {
	a := newTestAdmin(testKey)
	rec := do(a, http.MethodGet, "/domains/app.example.com/traffic?from=bad&to=2026-01-31", testKey)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid from: got %d, want 400", rec.Code)
	}
}
