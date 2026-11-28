package admin

import (
	"errors"
	"net/http"
	"time"

	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/gin-gonic/gin"
)

// Admin accounts, managed from the panel or the API. Only a superuser manages other
// accounts. Every session can change its own password. The first superuser is
// created from the console, so the panel can never be left without one.

type accountView struct {
	Username  string    `json:"username" example:"operador"`
	Role      string    `json:"role" example:"operator"`
	Disabled  bool      `json:"disabled" example:"false"`
	CreatedAt time.Time `json:"createdAt"`
}

type accountInfo struct {
	Username string `json:"username" example:"jtorres"`
	Role     string `json:"role" example:"superuser"`
}

type createAccountBody struct {
	Username string `json:"username" example:"operador"`
	Password string `json:"password" example:"a-password-of-12-or-more"`
	Role     string `json:"role" example:"operator"`
}

type passwordBody struct {
	Password string `json:"password" example:"a-password-of-12-or-more"`
}

type ownPasswordBody struct {
	CurrentPassword string `json:"currentPassword" example:"the-password-you-have-now"`
	NewPassword     string `json:"newPassword" example:"a-password-of-12-or-more"`
}

type roleBody struct {
	Role string `json:"role" example:"operator"`
}

// requireSuperuser answers 403 unless the session belongs to a superuser.
func requireSuperuser(c *gin.Context) bool {
	if c.GetString("adminRole") == auth.RoleSuperuser {
		return true
	}
	c.JSON(http.StatusForbidden, errorResponse{Error: "only a superuser can do this"})
	return false
}

// writeAccountError maps the account store's errors to HTTP answers.
func writeAccountError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrAdminUserNotFound):
		c.JSON(http.StatusNotFound, errorResponse{Error: err.Error()})
	case errors.Is(err, auth.ErrAdminUserExists), errors.Is(err, auth.ErrLastSuperuser):
		c.JSON(http.StatusConflict, errorResponse{Error: err.Error()})
	case errors.Is(err, auth.ErrWrongPassword):
		c.JSON(http.StatusForbidden, errorResponse{Error: err.Error()})
	case errors.Is(err, auth.ErrInvalidUsername), errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrInvalidRole):
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "unable to change the account"})
	}
}

// @Summary Who am I
// @Description The account and role of the session making the request.
// @Tags accounts
// @Security BearerAuth
// @Produce json
// @Success 200 {object} accountInfo
// @Failure 401 {object} errorResponse "Missing, expired or invalid session. Error: 'Unauthorized'"
// @Router /account [get]
func (r *router) whoAmI(c *gin.Context) {
	c.JSON(http.StatusOK, accountInfo{Username: c.GetString("adminUser"), Role: c.GetString("adminRole")})
}

// @Summary Change my password
// @Description Changes the password of the session's own account after checking the current one. Every session of the account ends, so the user logs in again with the new password.
// @Tags accounts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param change body ownPasswordBody true "Current and new password"
// @Success 200 {object} statusResponse
// @Failure 400 {object} errorResponse "The new password is shorter than 12 characters, or the body is not JSON"
// @Failure 401 {object} errorResponse "Missing, expired or invalid session. Error: 'Unauthorized'"
// @Failure 403 {object} errorResponse "The current password is wrong. Error: 'the current password is wrong'"
// @Failure 500 {object} errorResponse "The password could not be changed"
// @Router /account/password [post]
func (r *router) changeOwnPassword(c *gin.Context) {
	var body ownPasswordBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "body must be JSON with currentPassword and newPassword"})
		return
	}
	if err := r.admin.store.ChangeOwnPassword(c.GetString("adminUser"), body.CurrentPassword, body.NewPassword); err != nil {
		writeAccountError(c, err)
		return
	}
	clearSessionCookie(c)
	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// @Summary List admin accounts
// @Description Every account with its role and status. Superusers only.
// @Tags accounts
// @Security BearerAuth
// @Produce json
// @Success 200 {array} accountView
// @Failure 401 {object} errorResponse "Missing, expired or invalid session. Error: 'Unauthorized'"
// @Failure 403 {object} errorResponse "Only a superuser can do this"
// @Failure 500 {object} errorResponse "The accounts could not be listed"
// @Router /admin-users [get]
func (r *router) listAccounts(c *gin.Context) {
	if !requireSuperuser(c) {
		return
	}
	infos, err := r.admin.store.ListAdminUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "unable to list accounts"})
		return
	}
	out := make([]accountView, 0, len(infos))
	for _, a := range infos {
		out = append(out, accountView{Username: a.Username, Role: a.Role, Disabled: a.Disabled, CreatedAt: a.CreatedAt})
	}
	c.JSON(http.StatusOK, out)
}

// @Summary Create an admin account
// @Description Creates an account with a role: superuser (manages accounts) or operator (manages domains and visitor users). Superusers only. The password is at least 12 characters.
// @Tags accounts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param account body createAccountBody true "Account to create"
// @Success 200 {object} statusResponse
// @Failure 400 {object} errorResponse "Bad username, short password, role not superuser or operator, or malformed JSON"
// @Failure 401 {object} errorResponse "Missing, expired or invalid session. Error: 'Unauthorized'"
// @Failure 403 {object} errorResponse "Only a superuser can do this"
// @Failure 409 {object} errorResponse "An account with that name already exists"
// @Failure 500 {object} errorResponse "The account could not be created"
// @Router /admin-users [post]
func (r *router) createAccount(c *gin.Context) {
	if !requireSuperuser(c) {
		return
	}
	var body createAccountBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "body must be JSON with username, password and role"})
		return
	}
	if err := r.admin.store.CreateAdminUser(body.Username, body.Password, body.Role); err != nil {
		writeAccountError(c, err)
		return
	}
	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// @Summary Reset an account's password
// @Description Sets a new password for an account and ends all its sessions. Superusers only.
// @Tags accounts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param username path string true "Account name"
// @Param password body passwordBody true "New password"
// @Success 200 {object} statusResponse
// @Failure 400 {object} errorResponse "Short password or malformed JSON"
// @Failure 401 {object} errorResponse "Missing, expired or invalid session. Error: 'Unauthorized'"
// @Failure 403 {object} errorResponse "Only a superuser can do this"
// @Failure 404 {object} errorResponse "The account does not exist. Error: 'admin user not found'"
// @Failure 500 {object} errorResponse "The password could not be changed"
// @Router /admin-users/{username}/password [post]
func (r *router) resetAccountPassword(c *gin.Context) {
	if !requireSuperuser(c) {
		return
	}
	var body passwordBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "body must be JSON with password"})
		return
	}
	if err := r.admin.store.ResetAdminPassword(c.Param("username"), body.Password); err != nil {
		writeAccountError(c, err)
		return
	}
	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// @Summary Change an account's role
// @Description Sets superuser or operator. The last active superuser cannot be demoted. Superusers only.
// @Tags accounts
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param username path string true "Account name"
// @Param role body roleBody true "New role"
// @Success 200 {object} statusResponse
// @Failure 400 {object} errorResponse "Role is not superuser or operator, or malformed JSON"
// @Failure 401 {object} errorResponse "Missing, expired or invalid session. Error: 'Unauthorized'"
// @Failure 403 {object} errorResponse "Only a superuser can do this"
// @Failure 404 {object} errorResponse "The account does not exist. Error: 'admin user not found'"
// @Failure 409 {object} errorResponse "At least one active superuser must remain"
// @Failure 500 {object} errorResponse "The role could not be changed"
// @Router /admin-users/{username}/role [post]
func (r *router) changeAccountRole(c *gin.Context) {
	if !requireSuperuser(c) {
		return
	}
	var body roleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "body must be JSON with role"})
		return
	}
	if err := r.admin.store.SetAdminRole(c.Param("username"), body.Role); err != nil {
		writeAccountError(c, err)
		return
	}
	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// setAccountDisabled blocks or allows an account. An account cannot disable itself,
// so a superuser cannot lock the panel out by mistake.
func (r *router) setAccountDisabled(c *gin.Context, disabled bool) {
	if !requireSuperuser(c) {
		return
	}
	name := c.Param("username")
	if disabled && name == c.GetString("adminUser") {
		c.JSON(http.StatusConflict, errorResponse{Error: "you cannot disable your own account"})
		return
	}
	if err := r.admin.store.SetAdminDisabled(name, disabled); err != nil {
		writeAccountError(c, err)
		return
	}
	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// @Summary Disable an account
// @Description Blocks the account and ends its sessions. You cannot disable your own account, and the last active superuser cannot be disabled. Superusers only.
// @Tags accounts
// @Security BearerAuth
// @Produce json
// @Param username path string true "Account name"
// @Success 200 {object} statusResponse
// @Failure 401 {object} errorResponse "Missing, expired or invalid session. Error: 'Unauthorized'"
// @Failure 403 {object} errorResponse "Only a superuser can do this"
// @Failure 404 {object} errorResponse "The account does not exist. Error: 'admin user not found'"
// @Failure 409 {object} errorResponse "Your own account, or the last active superuser"
// @Failure 500 {object} errorResponse "The account could not be changed"
// @Router /admin-users/{username}/disable [post]
func (r *router) disableAccount(c *gin.Context) { r.setAccountDisabled(c, true) }

// @Summary Enable an account
// @Description Allows a disabled account to log in again. Superusers only.
// @Tags accounts
// @Security BearerAuth
// @Produce json
// @Param username path string true "Account name"
// @Success 200 {object} statusResponse
// @Failure 401 {object} errorResponse "Missing, expired or invalid session. Error: 'Unauthorized'"
// @Failure 403 {object} errorResponse "Only a superuser can do this"
// @Failure 404 {object} errorResponse "The account does not exist. Error: 'admin user not found'"
// @Failure 500 {object} errorResponse "The account could not be changed"
// @Router /admin-users/{username}/enable [post]
func (r *router) enableAccount(c *gin.Context) { r.setAccountDisabled(c, false) }
