package admin

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/gin-gonic/gin"
)

// adminStore is what the API needs from the account store. The implementation is
// auth.AdminStore; tests use a fake.
type adminStore interface {
	Login(username, password string) (uint, error)
	NewSession(userID uint) (string, time.Time, error)
	Authenticate(token string) (username, role string, err error)
	Logout(token string) error
	ChangeOwnPassword(username, current, next string) error
	CreateAdminUser(username, password, role string) error
	ResetAdminPassword(username, password string) error
	SetAdminRole(username, role string) error
	SetAdminDisabled(username string, disabled bool) error
	ListAdminUsers() ([]auth.AdminInfo, error)
}

// Five wrong passwords lock an account for 15 minutes. A successful login clears the
// count.
const (
	maxLoginFailures = 5
	loginLockout     = 15 * time.Minute
)

type loginState struct {
	fails int
	until time.Time
}

// loginLimiter slows password guessing. Its zero value is ready to use.
type loginLimiter struct {
	mu     sync.Mutex
	states map[string]*loginState
}

// blocked returns how long the account must still wait, or 0.
func (l *loginLimiter) blocked(username string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.states[username]
	if !ok {
		return 0
	}
	if wait := time.Until(st.until); wait > 0 {
		return wait
	}
	return 0
}

// fail counts a wrong password and starts a lock once the free attempts are used.
func (l *loginLimiter) fail(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.states == nil {
		l.states = make(map[string]*loginState)
	}
	st, ok := l.states[username]
	if !ok {
		st = &loginState{}
		l.states[username] = st
	}
	st.fails++
	if st.fails >= maxLoginFailures {
		st.until = time.Now().Add(loginLockout)
	}
}

func (l *loginLimiter) reset(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.states, username)
}

type loginBody struct {
	Username string `json:"username" example:"jtorres"`
	Password string `json:"password" example:"a-long-password"`
}

type loginResponse struct {
	Username  string    `json:"username" example:"jtorres"`
	Token     string    `json:"token" example:"q8Vx2nqT1sK9wEZl0cHjR3pYb7LmUo4aGdNfXiTr5Ys"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// sessionFromRequest returns the session token of the request. A Bearer header
// wins (API clients); otherwise the session cookie (the web panel). fromCookie tells
// which one it was, because cookie requests need the CSRF header for changes.
func sessionFromRequest(req *http.Request) (token string, fromCookie bool) {
	if raw, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer "); ok {
		if t := strings.TrimSpace(raw); t != "" {
			return t, false
		}
	}
	if cookie, err := req.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		return cookie.Value, true
	}
	return "", false
}

// publicPaths need no session: the health check, the two login endpoints, and the
// files the login page loads. Everything else is behind the session gate.
var publicPaths = map[string]bool{
	"/health":    true,
	"/login":     true,
	"/web/login": true,
}

// publicPrefixes are the files of the panel and its login page. They hold no data.
var publicPrefixes = []string{"/admin/js/", "/admin/css/", "/admin/img/"}

func isPublicPath(p string) bool {
	if publicPaths[p] {
		return true
	}
	for _, prefix := range publicPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func unsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// sessionGate checks the session of every request except the public paths. Without
// a valid session, "/" shows the login form and every other path answers 401.
// Changes made with the cookie must carry the CSRF header.
func (r *router) sessionGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isPublicPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		token, fromCookie := sessionFromRequest(c.Request)
		user, role := "", ""
		if token != "" && r.admin.store != nil {
			if name, userRole, err := r.admin.store.Authenticate(token); err == nil {
				user, role = name, userRole
			}
		}
		if user == "" {
			if c.Request.URL.Path == "/" && c.Request.Method == http.MethodGet {
				c.Header("Content-Security-Policy", pageCSP)
				c.Header("Cache-Control", "no-store")
				c.Data(http.StatusOK, "text/html; charset=utf-8", pageFile("login.html"))
				c.Abort()
				return
			}
			c.Header("WWW-Authenticate", `Bearer realm="lipstick-admin"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse{Error: "Unauthorized"})
			return
		}
		if fromCookie && unsafeMethod(c.Request.Method) && c.GetHeader(csrfHeader) != "1" {
			c.AbortWithStatusJSON(http.StatusForbidden, errorResponse{Error: "changes from the panel need the " + csrfHeader + " header"})
			return
		}
		c.Set("adminUser", user)
		c.Set("adminRole", role)
		c.Next()
	}
}

// authorized reports whether the gate accepted the request. Handlers call it as a
// second check; it never answers by itself on the normal path.
func (r *router) authorized(c *gin.Context) bool {
	if c.GetString("adminUser") != "" {
		return true
	}
	c.Header("WWW-Authenticate", `Bearer realm="lipstick-admin"`)
	c.JSON(http.StatusUnauthorized, errorResponse{Error: "Unauthorized"})
	return false
}

// issueSession checks the credentials, applies the lock, and creates a session.
// wait is set when the account is locked.
func (r *router) issueSession(username, password string) (token string, expires time.Time, wait time.Duration, status int, message string) {
	if wait := r.admin.logins.blocked(username); wait > 0 {
		return "", time.Time{}, wait, http.StatusTooManyRequests, "too many failed logins; wait before trying again"
	}
	userID, err := r.admin.store.Login(username, password)
	if err != nil {
		r.admin.logins.fail(username)
		return "", time.Time{}, 0, http.StatusUnauthorized, "invalid username or password"
	}
	token, expires, err = r.admin.store.NewSession(userID)
	if err != nil {
		return "", time.Time{}, 0, http.StatusInternalServerError, "unable to create a session"
	}
	r.admin.logins.reset(username)
	return token, expires, 0, http.StatusOK, ""
}

// @Summary Log in
// @Description Checks an admin account and returns a session token. Send the token on every change as "Authorization: Bearer <token>". The token expires after 12 hours. Five wrong passwords lock the account for 15 minutes. Accounts are created on the server machine with: lipstick-server admin create -username NAME. The web panel uses /web/login instead, which keeps the token in a cookie.
// @Tags session
// @Accept json
// @Produce json
// @Param credentials body loginBody true "Account name and password"
// @Success 200 {object} loginResponse
// @Failure 400 {object} errorResponse "Body is not JSON with username and password"
// @Failure 401 {object} errorResponse "Invalid username or password. The message does not say which of the two was wrong"
// @Failure 429 {object} errorResponse "Too many failed logins; the Retry-After header says how long to wait"
// @Failure 500 {object} errorResponse "The session could not be created"
// @Failure 503 {object} errorResponse "The change was not made: the audit log is not available"
// @Router /login [post]
func (r *router) login(c *gin.Context) {
	var body loginBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "body must be JSON with username and password"})
		return
	}
	c.Set(auditActorKey, body.Username)
	setAudit(c, body.Username, map[string]any{"via": "api"})
	token, expires, wait, status, message := r.issueSession(body.Username, body.Password)
	if status == http.StatusTooManyRequests {
		c.Header("Retry-After", wait.Round(time.Second).String())
	}
	if status != http.StatusOK {
		c.JSON(status, errorResponse{Error: message})
		return
	}
	c.JSON(http.StatusOK, loginResponse{Username: body.Username, Token: token, ExpiresAt: expires})
}

// @Summary Log in to the web panel
// @Description The same check as /login, for the web panel. The token is not in the answer: it is stored in the HttpOnly, Secure, SameSite=Strict cookie "lipstick_session", which scripts cannot read. The request must carry the header X-Lipstick-CSRF: 1.
// @Tags session
// @Accept json
// @Produce json
// @Param credentials body loginBody true "Account name and password"
// @Success 200 {object} webLoginResponse "The session cookie is set"
// @Failure 400 {object} errorResponse "Body is not JSON with username and password"
// @Failure 401 {object} errorResponse "Invalid username or password"
// @Failure 403 {object} errorResponse "The X-Lipstick-CSRF header is missing"
// @Failure 429 {object} errorResponse "Too many failed logins; the Retry-After header says how long to wait"
// @Failure 500 {object} errorResponse "The session could not be created"
// @Failure 503 {object} errorResponse "The change was not made: the audit log is not available"
// @Router /web/login [post]
func (r *router) webLogin(c *gin.Context) {
	if c.GetHeader(csrfHeader) != "1" {
		c.JSON(http.StatusForbidden, errorResponse{Error: "the " + csrfHeader + " header is required"})
		return
	}
	var body loginBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "body must be JSON with username and password"})
		return
	}
	c.Set(auditActorKey, body.Username)
	setAudit(c, body.Username, map[string]any{"via": "panel"})
	token, expires, wait, status, message := r.issueSession(body.Username, body.Password)
	if status == http.StatusTooManyRequests {
		c.Header("Retry-After", wait.Round(time.Second).String())
	}
	if status != http.StatusOK {
		c.JSON(status, errorResponse{Error: message})
		return
	}
	setSessionCookie(c, token, expires)
	c.JSON(http.StatusOK, webLoginResponse{Username: body.Username, ExpiresAt: expires})
}

// @Summary Log out
// @Description Ends the session of the token or cookie of the request. The token stops working at once, and the session cookie is cleared. Changes with the cookie need the X-Lipstick-CSRF: 1 header.
// @Tags session
// @Security BearerAuth
// @Produce json
// @Success 200 {object} statusResponse
// @Failure 401 {object} errorResponse "Missing, expired or invalid session. Error: 'Unauthorized'"
// @Failure 403 {object} errorResponse "The X-Lipstick-CSRF header is missing on a cookie logout"
// @Failure 500 {object} errorResponse "The session could not be ended"
// @Failure 503 {object} errorResponse "The change was not made: the audit log is not available"
// @Router /logout [post]
func (r *router) logout(c *gin.Context) {
	if !r.authorized(c) {
		return
	}
	token, _ := sessionFromRequest(c.Request)
	if err := r.admin.store.Logout(token); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "unable to end the session"})
		return
	}
	clearSessionCookie(c)
	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}
