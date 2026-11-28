package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/server/auth"
)

const testKey = "0123456789abcdef-admin"
const clientSecret = "tunnel-client-secret-do-not-leak"

type fakeAuth struct{}

func (fakeAuth) GetDomains() ([]*auth.Domain, error) {
	return []*auth.Domain{{ID: 1, Name: "app.example.com", ApiKey: clientSecret, AllowMultipleConnections: true}}, nil
}
func (fakeAuth) GetDomain(name string) (*auth.Domain, error) {
	if name == "missing.example.com" {
		return nil, auth.ErrDomainNotFound
	}
	return &auth.Domain{ID: 1, Name: name, ApiKey: clientSecret}, nil
}
func (fakeAuth) AddDomain(*auth.Domain) error                               { return nil }
func (fakeAuth) UpdateDomain(*auth.Domain) error                            { return nil }
func (fakeAuth) DelDomain(uint) error                                       { return nil }
func (fakeAuth) AddVisitorUser(d, u, s string) (string, error)              { return "", nil }
func (fakeAuth) RotateVisitorUser(d, u string) (string, error)              { return "", nil }
func (fakeAuth) DeleteVisitorUser(d, u string) error                        { return nil }
func (fakeAuth) VisitorUserSecret(d, u string) (string, error)              { return "", nil }
func (fakeAuth) ListVisitorUsers(d string) ([]string, error)                { return nil, nil }
func (fakeAuth) AddVisitorToken(d, n string, m, p []string) (string, error) { return "", nil }
func (fakeAuth) RevokeVisitorToken(d string, id uint) error                 { return nil }
func (fakeAuth) FindVisitorToken(d, t string) (*auth.VisitorToken, error)   { return nil, nil }
func (fakeAuth) ListVisitorTokens(d string) ([]auth.VisitorToken, error)    { return nil, nil }
func (fakeAuth) CountVisitorCredentials(d string) (int, error)              { return 0, nil }
func (fakeAuth) SetAuthLatched(d string, l bool) error                      { return nil }

// fakeSessions accepts one session token, for every test. It stands in for the
// account store.
type fakeSessions struct{ valid string }

var errFakeLogin = errors.New("invalid username or password")

func (f fakeSessions) Login(username, password string) (uint, error) {
	if username == "tester" && password == "tester-password-long" {
		return 1, nil
	}
	return 0, errFakeLogin
}
func (f fakeSessions) NewSession(uint) (string, time.Time, error) {
	return f.valid, time.Now().Add(time.Hour), nil
}
func (f fakeSessions) Authenticate(token string) (string, string, error) {
	if token != "" && token == f.valid {
		return "tester", auth.RoleSuperuser, nil
	}
	return "", "", errFakeLogin
}
func (f fakeSessions) Logout(string) error                            { return nil }
func (f fakeSessions) ChangeOwnPassword(string, string, string) error { return nil }
func (f fakeSessions) CreateAdminUser(string, string, string) error   { return nil }
func (f fakeSessions) ResetAdminPassword(string, string) error        { return nil }
func (f fakeSessions) SetAdminRole(string, string) error              { return nil }
func (f fakeSessions) SetAdminDisabled(string, bool) error            { return nil }
func (f fakeSessions) ListAdminUsers() ([]auth.AdminInfo, error)      { return nil, nil }

// newTestAdmin builds the API. token is the only session token it accepts.
func newTestAdmin(token string) *Admin {
	a := &Admin{authManager: fakeAuth{}, store: fakeSessions{valid: token}}
	a.SetAudit(newMemAudit())
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

// Writes need a session token in a Bearer header. The unauthorized path returns
// before touching the database, so no database is needed.
func TestAdminRejectsMissingOrWrongSession(t *testing.T) {
	a := newTestAdmin(testKey)
	for _, h := range []string{"", "wrong", "Bearer wrong", "Bearer " + testKey + "x", testKey} {
		if rec := do(a, http.MethodDelete, "/domains/app.example.com", h); rec.Code != http.StatusUnauthorized {
			t.Errorf("header %q: got %d, want 401", h, rec.Code)
		}
	}
}

func TestAdminEmptySessionNeverAuthorizes(t *testing.T) {
	a := newTestAdmin("")
	for _, h := range []string{"", "Bearer ", "Bearer"} {
		if rec := do(a, http.MethodDelete, "/domains/app.example.com", h); rec.Code != http.StatusUnauthorized {
			t.Errorf("header %q authorized with no session: got %d", h, rec.Code)
		}
	}
}

func TestAdminListsDomainsWithoutApiKey(t *testing.T) {
	a := newTestAdmin(testKey)
	rec := do(a, http.MethodGet, "/domains", "Bearer "+testKey)
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
	rec := do(a, http.MethodGet, "/domains/app.example.com", "Bearer "+testKey)
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

func TestTrafficValidatesDatesBeforeDatabase(t *testing.T) {
	a := newTestAdmin(testKey)
	rec := do(a, http.MethodGet, "/domains/app.example.com/traffic?from=bad&to=2026-01-31", "Bearer "+testKey)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid from: got %d, want 400", rec.Code)
	}
}

func patch(a *Admin, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.engine.ServeHTTP(rec, req)
	return rec
}

func TestPatchUnknownDomainIs404(t *testing.T) {
	a := newTestAdmin(testKey)
	if rec := patch(a, "/domains/missing.example.com", `{"apiKey":"newkey0123456789"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown domain: got %d, want 404", rec.Code)
	}
}

// A wrong JSON type must be a 400. Before the fix it panicked.
func TestPatchWrongTypesAre400(t *testing.T) {
	a := newTestAdmin(testKey)
	for _, body := range []string{
		`{"apiKey":5}`,
		`{"apiKey":""}`,
		`{"allowMultipleConnections":"yes"}`,
	} {
		if rec := patch(a, "/domains/app.example.com", body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: got %d, want 400", body, rec.Code)
		}
	}
}

func TestPatchValidFieldsIsOK(t *testing.T) {
	a := newTestAdmin(testKey)
	if rec := patch(a, "/domains/app.example.com", `{"allowMultipleConnections":false}`); rec.Code != http.StatusOK {
		t.Fatalf("valid patch: got %d, want 200", rec.Code)
	}
}

// recordingAuth remembers the last domain added, so the create path can be checked.
type recordingAuth struct {
	fakeAuth
	added *auth.Domain
}

func (r *recordingAuth) AddDomain(d *auth.Domain) error {
	r.added = d
	return nil
}

func postDomain(a *Admin, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/domains", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.engine.ServeHTTP(rec, req)
	return rec
}

// An omitted allowMultipleConnections keeps the default (true). An explicit
// false must reach the store as false.
func TestCreateDomainMultipleConnectionsDefaults(t *testing.T) {
	rec := &recordingAuth{}
	a := &Admin{authManager: rec, store: fakeSessions{valid: testKey}}
	a.SetAudit(newMemAudit())
	configureRouter(a)

	if r := postDomain(a, `{"name":"a.example.com","apiKey":"0123456789abcdef"}`); r.Code != http.StatusOK {
		t.Fatalf("create without field: %d", r.Code)
	}
	if !rec.added.AllowMultipleConnections {
		t.Fatal("omitted field should default to true")
	}

	if r := postDomain(a, `{"name":"b.example.com","apiKey":"0123456789abcdef","allowMultipleConnections":false}`); r.Code != http.StatusOK {
		t.Fatalf("create with false: %d", r.Code)
	}
	if rec.added.AllowMultipleConnections {
		t.Fatal("explicit false was lost")
	}
}
