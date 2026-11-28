package admin

import (
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// adminStore is what the API needs from the account store. The implementation is
// auth.AdminStore; tests use a fake.
type adminStore interface {
	Login(username, password string) (uint, error)
	NewSession(userID uint) (string, time.Time, error)
	Authenticate(token string) (string, error)
	Logout(token string) error
}

// Failed logins for one account lock it for a while. Five free attempts, then the
// lock lasts 15 minutes; a successful login clears the count.
const (
	freeLoginFailures = 5
	loginLockout      = 15 * time.Minute
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
	if st.fails > freeLoginFailures {
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

// bearerToken returns the token of an "Authorization: Bearer <token>" header.
func bearerToken(header string) (string, bool) {
	token, ok := strings.CutPrefix(header, "Bearer ")
	token = strings.TrimSpace(token)
	return token, ok && token != ""
}

// authorized checks the session token of the request. It guards every endpoint that
// changes data. Read-only endpoints do not use it: the admin port listens on
// 127.0.0.1 only and they expose no keys.
func (r *router) authorized(c *gin.Context) bool {
	if token, ok := bearerToken(c.Request.Header.Get("Authorization")); ok && r.admin.store != nil {
		if user, err := r.admin.store.Authenticate(token); err == nil {
			c.Set("adminUser", user)
			return true
		}
	}
	c.Header("WWW-Authenticate", `Bearer realm="lipstick-admin"`)
	c.JSON(401, errorResponse{Error: "Unauthorized"})
	return false
}

// @Summary Log in
// @Description Checks an admin account and returns a session token. Send the token on every change as "Authorization: Bearer <token>". The token expires after 12 hours. Five wrong passwords lock the account for 15 minutes. Accounts are created on the server machine with: lipstick-server admin create -username NAME.
// @Tags session
// @Accept json
// @Produce json
// @Param credentials body loginBody true "Account name and password"
// @Success 200 {object} loginResponse
// @Failure 400 {object} errorResponse "Body is not JSON with username and password"
// @Failure 401 {object} errorResponse "Invalid username or password. The message does not say which of the two was wrong"
// @Failure 429 {object} errorResponse "Too many failed logins; the Retry-After header says how long to wait"
// @Failure 500 {object} errorResponse "The session could not be created"
// @Router /login [post]
func (r *router) login(c *gin.Context) {
	var body loginBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, errorResponse{Error: "body must be JSON with username and password"})
		return
	}
	if wait := r.admin.logins.blocked(body.Username); wait > 0 {
		c.Header("Retry-After", wait.Round(time.Second).String())
		c.JSON(429, errorResponse{Error: "too many failed logins; wait before trying again"})
		return
	}
	userID, err := r.admin.store.Login(body.Username, body.Password)
	if err != nil {
		r.admin.logins.fail(body.Username)
		c.JSON(401, errorResponse{Error: "invalid username or password"})
		return
	}
	token, expires, err := r.admin.store.NewSession(userID)
	if err != nil {
		c.JSON(500, errorResponse{Error: "unable to create a session"})
		return
	}
	r.admin.logins.reset(body.Username)
	c.JSON(200, loginResponse{Username: body.Username, Token: token, ExpiresAt: expires})
}

// @Summary Log out
// @Description Ends the session of the token sent in the Authorization header. The token stops working at once.
// @Tags session
// @Security BearerAuth
// @Produce json
// @Success 200 {object} statusResponse
// @Failure 401 {object} errorResponse "Missing, expired or invalid session token. Error: 'Unauthorized'"
// @Failure 500 {object} errorResponse "The session could not be ended"
// @Router /logout [post]
func (r *router) logout(c *gin.Context) {
	if !r.authorized(c) {
		return
	}
	token, _ := bearerToken(c.Request.Header.Get("Authorization"))
	if err := r.admin.store.Logout(token); err != nil {
		c.JSON(500, errorResponse{Error: "unable to end the session"})
		return
	}
	c.JSON(200, statusResponse{Status: "ok"})
}
