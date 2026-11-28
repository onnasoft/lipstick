package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// request sends one request with optional headers and cookie, and returns the answer.
func request(a *Admin, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	a.engine.ServeHTTP(rec, req)
	return rec
}

func cookieFor(token string) map[string]string {
	return map[string]string{"Cookie": sessionCookie + "=" + token}
}

func csrfCookieFor(token string) map[string]string {
	return map[string]string{"Cookie": sessionCookie + "=" + token, csrfHeader: "1"}
}

// The session cookie opens the reads, like a Bearer token does.
func TestSessionCookieOpensReads(t *testing.T) {
	a := statsAdmin()
	if rec := request(a, http.MethodGet, "/stats", "", cookieFor(testKey)); rec.Code != http.StatusOK {
		t.Fatalf("stats with the cookie: %d", rec.Code)
	}
	if rec := request(a, http.MethodGet, "/stats", "", cookieFor("not-a-session")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stats with a wrong cookie: %d", rec.Code)
	}
}

// A change made with the cookie must carry the CSRF header; a page on another site
// cannot add it, so its forged form post is refused.
func TestCookieChangeNeedsCSRFHeader(t *testing.T) {
	a := newTestAdmin(testKey)
	if rec := request(a, http.MethodPost, "/domains", "{}", cookieFor(testKey)); rec.Code != http.StatusForbidden {
		t.Fatalf("cookie change without the CSRF header: %d, want 403", rec.Code)
	}
	if rec := request(a, http.MethodPost, "/domains", "{}", csrfCookieFor(testKey)); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("cookie change with the CSRF header refused: %d", rec.Code)
	}
}

// Bearer tokens are not affected by the CSRF rule: they are not sent by browsers
// on their own.
func TestBearerChangeNeedsNoCSRFHeader(t *testing.T) {
	a := newTestAdmin(testKey)
	if rec := request(a, http.MethodPost, "/domains", "{}", map[string]string{"Authorization": "Bearer " + testKey}); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("bearer change refused: %d", rec.Code)
	}
}

func TestWebLoginSetsHttpOnlyCookieAndNoToken(t *testing.T) {
	a := newTestAdmin(testKey)
	rec := request(a, http.MethodPost, "/web/login", `{"username":"tester","password":"tester-password-long"}`, map[string]string{csrfHeader: "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("web login: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), testKey) || strings.Contains(rec.Body.String(), "token") {
		t.Fatalf("the token is in the body: %s", rec.Body.String())
	}
	setCookie := rec.Header().Get("Set-Cookie")
	for _, want := range []string{sessionCookie + "=" + testKey, "HttpOnly", "Secure", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(setCookie, want) {
			t.Errorf("cookie lacks %q: %s", want, setCookie)
		}
	}
}

func TestWebLoginNeedsCSRFHeaderAndRightPassword(t *testing.T) {
	a := newTestAdmin(testKey)
	if rec := request(a, http.MethodPost, "/web/login", `{"username":"tester","password":"tester-password-long"}`, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("login without the CSRF header: %d, want 403", rec.Code)
	}
	rec := request(a, http.MethodPost, "/web/login", `{"username":"tester","password":"wrong-password-here"}`, map[string]string{csrfHeader: "1"})
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("wrong password: %d, cookie %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}
}

// The web login and the API login share the lock: wrong passwords count together.
func TestWebLoginSharesTheLock(t *testing.T) {
	a := newTestAdmin(testKey)
	for i := 0; i < maxLoginFailures; i++ {
		request(a, http.MethodPost, "/web/login", `{"username":"tester","password":"nope"}`, map[string]string{csrfHeader: "1"})
	}
	rec := request(a, http.MethodPost, "/login", `{"username":"tester","password":"tester-password-long"}`, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("API login while locked by the web login: %d", rec.Code)
	}
}

func TestLogoutClearsTheCookie(t *testing.T) {
	a := newTestAdmin(testKey)
	if rec := request(a, http.MethodPost, "/logout", "", cookieFor(testKey)); rec.Code != http.StatusForbidden {
		t.Fatalf("cookie logout without the CSRF header: %d", rec.Code)
	}
	rec := request(a, http.MethodPost, "/logout", "", csrfCookieFor(testKey))
	if rec.Code != http.StatusOK {
		t.Fatalf("logout: %d", rec.Code)
	}
	if sc := rec.Header().Get("Set-Cookie"); !strings.Contains(sc, sessionCookie+"=;") || !strings.Contains(sc, "Max-Age=0") && !strings.Contains(sc, "Max-Age=-1") {
		t.Fatalf("cookie not cleared: %q", sc)
	}
}

// Without a session the panel shows only the login form; its files are public.
func TestLoginPageAssetsArePublicAndStrict(t *testing.T) {
	a := newTestAdmin(testKey)
	rec := request(a, http.MethodGet, "/admin/panel.js", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("panel.js: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := request(a, http.MethodGet, "/admin/login.js", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("login.js: %d", rec.Code)
	}
	if rec := request(a, http.MethodGet, "/health", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("health without a session: %d", rec.Code)
	}
	csp := request(a, http.MethodGet, "/", "", nil).Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "unsafe") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("page policy: %q", csp)
	}
}

// Pages and files never contain inline script or style, so the policy can forbid them.
func TestPanelHasNoInlineScriptOrStyle(t *testing.T) {
	for _, bad := range []string{"<script>", "<style>", " style=", " onclick=", " onchange="} {
		if strings.Contains(panelHTML, bad) || strings.Contains(loginHTML, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
}
