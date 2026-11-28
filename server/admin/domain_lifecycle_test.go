package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/OnnaSoft/lipstick/server/auth"
)

// lifecycleAuth knows only the domains listed in existing, and remembers what was added.
type lifecycleAuth struct {
	fakeAuth
	existing map[string]bool
	added    *auth.Domain
}

func (f *lifecycleAuth) GetDomain(name string) (*auth.Domain, error) {
	if !f.existing[name] {
		return nil, auth.ErrDomainNotFound
	}
	return &auth.Domain{ID: 1, Name: name}, nil
}
func (f *lifecycleAuth) AddDomain(d *auth.Domain) error { f.added = d; return nil }
func (f *lifecycleAuth) SetDomainDisabled(name string, _ bool) error {
	if !f.existing[name] {
		return auth.ErrDomainNotFound
	}
	return nil
}

func lifecycleAdmin(existing ...string) (*Admin, *lifecycleAuth) {
	f := &lifecycleAuth{existing: map[string]bool{}}
	for _, name := range existing {
		f.existing[name] = true
	}
	a := &Admin{authManager: f, store: fakeSessions{valid: testKey}}
	a.SetAudit(newMemAudit())
	configureRouter(a)
	return a, f
}

func authed() map[string]string { return map[string]string{"Authorization": "Bearer " + testKey} }

func TestCreateRefusesBadNames(t *testing.T) {
	a, _ := lifecycleAdmin()
	for _, name := range []string{"", "Robot.example.com", "robot_one.example.com", "-bad.example.com", "a..b"} {
		body := `{"name":"` + name + `","apiKey":"` + strings.Repeat("k", 32) + `"}`
		if rec := request(a, http.MethodPost, "/domains", body, authed()); rec.Code != http.StatusBadRequest {
			t.Errorf("name %q: %d, want 400", name, rec.Code)
		}
	}
}

func TestCreateRefusesShortKeys(t *testing.T) {
	a, _ := lifecycleAdmin()
	rec := request(a, http.MethodPost, "/domains", `{"name":"robot.example.com","apiKey":"short"}`, authed())
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "at least 32") {
		t.Fatalf("short key: %d %s", rec.Code, rec.Body.String())
	}
}

// Without a key the server makes one and returns it once; the domain keeps the same key.
func TestCreateWithoutKeyReturnsAGeneratedKey(t *testing.T) {
	a, f := lifecycleAdmin()
	rec := request(a, http.MethodPost, "/domains", `{"name":"robot.example.com"}`, authed())
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Status, ApiKey string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.ApiKey) != 64 || f.added == nil || f.added.ApiKey != out.ApiKey {
		t.Fatalf("generated key %q not stored with the domain", out.ApiKey)
	}
}

// A given key is not echoed back.
func TestCreateWithKeyDoesNotEchoIt(t *testing.T) {
	a, _ := lifecycleAdmin()
	key := strings.Repeat("q", 40)
	rec := request(a, http.MethodPost, "/domains", `{"name":"robot.example.com","apiKey":"`+key+`"}`, authed())
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), key) || strings.Contains(rec.Body.String(), "apiKey") {
		t.Fatalf("given key answered: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCreateRefusesADuplicateName(t *testing.T) {
	a, _ := lifecycleAdmin("robot.example.com")
	rec := request(a, http.MethodPost, "/domains", `{"name":"robot.example.com"}`, authed())
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d, want 409", rec.Code)
	}
}

// Disabling and enabling are audited with their action names; an unknown domain is 404.
func TestDisableAndEnableAreAudited(t *testing.T) {
	a, _ := lifecycleAdmin("robot.example.com")
	if rec := request(a, http.MethodPost, "/domains/robot.example.com/disable", "", authed()); rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	if rec := request(a, http.MethodPost, "/domains/robot.example.com/enable", "", authed()); rec.Code != http.StatusOK {
		t.Fatalf("enable: %d", rec.Code)
	}
	if rec := request(a, http.MethodPost, "/domains/nope.example.com/disable", "", authed()); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown domain: %d, want 404", rec.Code)
	}
	actions := map[string]bool{}
	for _, e := range a.audit.(*memAudit).events {
		actions[e.Action] = true
	}
	if !actions["domain.disable"] || !actions["domain.enable"] {
		t.Fatalf("not audited: %v", actions)
	}
}
