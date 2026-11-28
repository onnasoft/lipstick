package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/OnnaSoft/lipstick/server/db"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const accountPassword = "a-long-enough-password"

// realAdmin builds the API on the real account store, over SQLite. It has one
// superuser, "jtorres", and one operator, "operador". It returns a token for each.
func realAdmin(t *testing.T) (a *Admin, superToken, operatorToken string) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "accounts.db")), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&db.AdminUser{}, &db.AdminSession{}); err != nil {
		t.Fatal(err)
	}
	store := auth.NewAdminStore(conn)
	if err := store.CreateAdminUser("jtorres", accountPassword, auth.RoleSuperuser); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAdminUser("operador", accountPassword, auth.RoleOperator); err != nil {
		t.Fatal(err)
	}
	superToken = sessionFor(t, store, "jtorres")
	operatorToken = sessionFor(t, store, "operador")
	a = &Admin{authManager: fakeAuth{}, store: store}
	configureRouter(a)
	return a, superToken, operatorToken
}

func sessionFor(t *testing.T, store *auth.AdminStore, user string) string {
	t.Helper()
	id, err := store.Login(user, accountPassword)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.NewSession(id)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func send(a *Admin, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	a.engine.ServeHTTP(rec, req)
	return rec
}

func TestWhoAmIReportsTheRole(t *testing.T) {
	a, sup, op := realAdmin(t)
	var got accountInfo
	json.Unmarshal(send(a, http.MethodGet, "/account", sup, "").Body.Bytes(), &got)
	if got.Username != "jtorres" || got.Role != auth.RoleSuperuser {
		t.Fatalf("superuser: %+v", got)
	}
	json.Unmarshal(send(a, http.MethodGet, "/account", op, "").Body.Bytes(), &got)
	if got.Role != auth.RoleOperator {
		t.Fatalf("operator: %+v", got)
	}
}

// Only a superuser manages accounts. An operator is refused, not hidden.
func TestOperatorCannotManageAccounts(t *testing.T) {
	a, _, op := realAdmin(t)
	calls := []struct{ method, path, body string }{
		{http.MethodGet, "/admin-users", ""},
		{http.MethodPost, "/admin-users", `{"username":"x","password":"` + accountPassword + `","role":"operator"}`},
		{http.MethodPost, "/admin-users/jtorres/password", `{"password":"` + accountPassword + `"}`},
		{http.MethodPost, "/admin-users/jtorres/role", `{"role":"operator"}`},
		{http.MethodPost, "/admin-users/jtorres/disable", ""},
	}
	for _, c := range calls {
		if rec := send(a, c.method, c.path, op, c.body); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as operator: %d, want 403", c.method, c.path, rec.Code)
		}
	}
}

func TestSuperuserCreatesAccountsAndSeesErrors(t *testing.T) {
	a, sup, _ := realAdmin(t)
	if rec := send(a, http.MethodPost, "/admin-users", sup, `{"username":"nuevo","password":"`+accountPassword+`","role":"operator"}`); rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := send(a, http.MethodPost, "/admin-users", sup, `{"username":"nuevo","password":"`+accountPassword+`","role":"operator"}`); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d, want 409", rec.Code)
	}
	if rec := send(a, http.MethodPost, "/admin-users", sup, `{"username":"corto","password":"short","role":"operator"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("short password: %d, want 400", rec.Code)
	}
	if rec := send(a, http.MethodPost, "/admin-users", sup, `{"username":"raro","password":"`+accountPassword+`","role":"root"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("role root: %d, want 400", rec.Code)
	}
	rec := send(a, http.MethodGet, "/admin-users", sup, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "password") || !strings.Contains(rec.Body.String(), "nuevo") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSuperuserCannotDisableItselfAndDisablesOthers(t *testing.T) {
	a, sup, op := realAdmin(t)
	if rec := send(a, http.MethodPost, "/admin-users/jtorres/disable", sup, ""); rec.Code != http.StatusConflict {
		t.Fatalf("self-disable: %d, want 409", rec.Code)
	}
	if rec := send(a, http.MethodPost, "/admin-users/operador/disable", sup, ""); rec.Code != http.StatusOK {
		t.Fatalf("disable operator: %d", rec.Code)
	}
	if rec := send(a, http.MethodGet, "/account", op, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled operator's session still works: %d", rec.Code)
	}
	if rec := send(a, http.MethodPost, "/admin-users/operador/enable", sup, ""); rec.Code != http.StatusOK {
		t.Fatalf("enable: %d", rec.Code)
	}
	if rec := send(a, http.MethodPost, "/admin-users/ghost/disable", sup, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown account: %d, want 404", rec.Code)
	}
}

// A superuser resets another account's password, which ends that account's sessions.
func TestResetEndsTheOtherAccountsSessions(t *testing.T) {
	a, sup, op := realAdmin(t)
	if rec := send(a, http.MethodPost, "/admin-users/operador/password", sup, `{"password":"another-long-password"}`); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d", rec.Code)
	}
	if rec := send(a, http.MethodGet, "/account", op, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session survived the reset: %d", rec.Code)
	}
}

// Anyone can change their own password, but only with the current one.
func TestOwnPasswordChange(t *testing.T) {
	a, _, op := realAdmin(t)
	if rec := send(a, http.MethodPost, "/account/password", op, `{"currentPassword":"not-my-password","newPassword":"another-long-password"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong current password: %d, want 403", rec.Code)
	}
	if rec := send(a, http.MethodGet, "/account", op, ""); rec.Code != http.StatusOK {
		t.Fatalf("a refused change ended the session: %d", rec.Code)
	}
	rec := send(a, http.MethodPost, "/account/password", op, `{"currentPassword":"`+accountPassword+`","newPassword":"another-long-password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("own change: %d %s", rec.Code, rec.Body.String())
	}
	if rec := send(a, http.MethodGet, "/account", op, ""); rec.Code != http.StatusUnauthorized {
		t.Fatal("session survived its own password change")
	}
}

func TestLastSuperuserCannotBeDemoted(t *testing.T) {
	a, sup, _ := realAdmin(t)
	if rec := send(a, http.MethodPost, "/admin-users/jtorres/role", sup, `{"role":"operator"}`); rec.Code != http.StatusConflict {
		t.Fatalf("demoting the last superuser: %d, want 409", rec.Code)
	}
}

// A cookie change on an account route still needs the CSRF header.
func TestAccountChangeWithCookieNeedsCSRF(t *testing.T) {
	a, sup, _ := realAdmin(t)
	req := httptest.NewRequest(http.MethodPost, "/admin-users", strings.NewReader(`{"username":"x","password":"`+accountPassword+`","role":"operator"}`))
	req.Header.Set("Cookie", sessionCookie+"="+sup)
	rec := httptest.NewRecorder()
	a.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cookie account change without CSRF: %d", rec.Code)
	}
}
