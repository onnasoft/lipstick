package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postLogin(a *Admin, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.engine.ServeHTTP(rec, req)
	return rec
}

func TestLoginReturnsSessionToken(t *testing.T) {
	a := newTestAdmin(testKey)
	rec := postLogin(a, `{"username":"tester","password":"tester-password-long"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token    string `json:"token"`
		Username string `json:"username"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Token != testKey || out.Username != "tester" {
		t.Fatalf("login body %+v", out)
	}
	// The token it returns is the one that opens the write endpoints.
	if rec := do(a, http.MethodPost, "/domains", "Bearer "+out.Token); rec.Code == http.StatusUnauthorized {
		t.Fatal("login token refused by a write endpoint")
	}
}

func TestLoginRefusesWrongPassword(t *testing.T) {
	a := newTestAdmin(testKey)
	rec := postLogin(a, `{"username":"tester","password":"nope"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "tester") {
		t.Fatal("the refusal says which part was wrong")
	}
}

// After five wrong passwords the account waits, even with the right password.
func TestLoginLocksAfterFailures(t *testing.T) {
	a := newTestAdmin(testKey)
	for i := 0; i < freeLoginFailures; i++ {
		postLogin(a, `{"username":"tester","password":"nope"}`)
	}
	if rec := postLogin(a, `{"username":"tester","password":"nope"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("attempt %d: %d, want 401", freeLoginFailures+1, rec.Code)
	}
	rec := postLogin(a, `{"username":"tester","password":"tester-password-long"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("right password while locked: %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After on the lock")
	}
}

func TestLoginNeedsJSON(t *testing.T) {
	a := newTestAdmin(testKey)
	if rec := postLogin(a, `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", rec.Code)
	}
}

func TestAuthorizedAnswersWithBearerChallenge(t *testing.T) {
	a := newTestAdmin(testKey)
	rec := do(a, http.MethodPost, "/domains", "")
	if rec.Code != http.StatusUnauthorized || !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("got %d, challenge %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}
