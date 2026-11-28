package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/gin-gonic/gin"
)

type fakeAuth struct {
	domains map[string]*auth.Domain
	secrets map[string]string // "domain|user" -> secret
}

func (f *fakeAuth) GetDomains() ([]*auth.Domain, error) { return nil, nil }
func (f *fakeAuth) GetDomain(name string) (*auth.Domain, error) {
	if d, ok := f.domains[name]; ok {
		return d, nil
	}
	return nil, http.ErrNoCookie
}
func (f *fakeAuth) AddDomain(*auth.Domain) error                  { return nil }
func (f *fakeAuth) UpdateDomain(*auth.Domain) error               { return nil }
func (f *fakeAuth) DelDomain(uint) error                          { return nil }
func (f *fakeAuth) AddVisitorUser(d, u, s string) (string, error) { return "", nil }
func (f *fakeAuth) RotateVisitorUser(d, u string) (string, error) { return "", nil }
func (f *fakeAuth) DeleteVisitorUser(d, u string) error           { return nil }
func (f *fakeAuth) VisitorUserSecret(d, u string) (string, error) {
	if s, ok := f.secrets[d+"|"+u]; ok {
		return s, nil
	}
	return "", auth.ErrUserNotFound
}
func (f *fakeAuth) ListVisitorUsers(d string) ([]string, error)                { return nil, nil }
func (f *fakeAuth) AddVisitorToken(d, n string, m, p []string) (string, error) { return "", nil }
func (f *fakeAuth) RevokeVisitorToken(d string, id uint) error                 { return nil }
func (f *fakeAuth) FindVisitorToken(d, t string) (*auth.VisitorToken, error)   { return nil, nil }
func (f *fakeAuth) ListVisitorTokens(d string) ([]auth.VisitorToken, error)    { return nil, nil }
func (f *fakeAuth) CountVisitorCredentials(d string) (int, error)              { return 0, nil }
func (f *fakeAuth) SetAuthLatched(d string, l bool) error                      { return nil }

func newTestRouter(domains map[string]*auth.Domain) *router {
	m := &Manager{authManager: &fakeAuth{domains: domains}}
	return &router{manager: m}
}

func serveUpgrade(t *testing.T, r *router, key string) int {
	t.Helper()
	engine := gin.New()
	engine.GET("/", r.upgrade)
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com:5051/", nil)
	if key != "" {
		req.Header.Set("Authorization", key)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec.Code
}

func TestUpgradeRejectsMissingOrWrongKey(t *testing.T) {
	r := newTestRouter(map[string]*auth.Domain{
		"app.example.com": {Name: "app.example.com", ApiKey: "s3cret"},
	})
	if code := serveUpgrade(t, r, ""); code != http.StatusUnauthorized {
		t.Errorf("missing key: got %d, want 401", code)
	}
	if code := serveUpgrade(t, r, "wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong key: got %d, want 401", code)
	}
}

func TestUpgradeRejectsEmptyConfiguredKey(t *testing.T) {
	r := newTestRouter(map[string]*auth.Domain{
		"app.example.com": {Name: "app.example.com", ApiKey: ""},
	})
	if code := serveUpgrade(t, r, ""); code != http.StatusUnauthorized {
		t.Errorf("empty key vs empty config: got %d, want 401", code)
	}
}

func TestUpgradeRejectsUnknownDomain(t *testing.T) {
	r := newTestRouter(map[string]*auth.Domain{})
	if code := serveUpgrade(t, r, "anything"); code != http.StatusNotFound {
		t.Errorf("unknown domain: got %d, want 404", code)
	}
}
