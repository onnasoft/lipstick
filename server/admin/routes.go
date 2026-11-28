package admin

import (
	"encoding/json"
	"strings"

	"crypto/subtle"
	"errors"
	"fmt"
	"github.com/OnnaSoft/lipstick/helper"
	"net/http"
	"strconv"
	"time"

	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/admin/docs"
	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/OnnaSoft/lipstick/server/db"
	"github.com/OnnaSoft/lipstick/server/manager"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

type router struct {
	admin *Admin
}

func configureRouter(admin *Admin) {
	router := &router{admin: admin}
	r := gin.New()

	r.GET("/health", router.health)
	r.GET("/stats", router.getStats)
	r.GET("/", router.ui)
	r.GET("/ui", func(c *gin.Context) { c.Redirect(http.StatusFound, "/") })
	r.GET("/requests", router.getRequests)
	// The API reference and its "Authorize" button. Read-only, like the other GET routes.
	docs.SwaggerInfo.BasePath = "/"
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	r.GET("/domains", router.getDomains)
	const domainNamePath = "/domains/:domainName"

	r.GET(domainNamePath, router.getDomain)
	r.GET(domainNamePath+"/traffic", router.getTraffic)
	r.POST("/domains", router.addDomain)
	r.PATCH(domainNamePath, router.updateDomain)
	r.DELETE(domainNamePath, router.deleteDomain)

	// Visitor authentication: users and API tokens of a domain.
	r.POST(domainNamePath+"/visitor-users", router.addVisitorUser)
	r.GET(domainNamePath+"/visitor-users", router.listVisitorUsers)
	r.POST(domainNamePath+"/visitor-users/:username/rotate", router.rotateVisitorUser)
	r.DELETE(domainNamePath+"/visitor-users/:username", router.deleteVisitorUser)
	r.POST(domainNamePath+"/visitor-tokens", router.addVisitorToken)
	r.GET(domainNamePath+"/visitor-tokens", router.listVisitorTokens)
	r.DELETE(domainNamePath+"/visitor-tokens/:id", router.revokeVisitorToken)

	admin.engine = r
}

// domainView is what the admin API returns for a domain. It leaves out the
// tunnel API key, which is write-only: it can be set, but never read back.
type domainView struct {
	ID                       uint     `json:"id"`
	Name                     string   `json:"name"`
	AllowMultipleConnections bool     `json:"allowMultipleConnections"`
	Passthrough              bool     `json:"passthrough"`
	AuthLatched              bool     `json:"authLatched"`
	AllowedCIDRs             []string `json:"allowedCidrs"`
}

func toDomainView(d *auth.Domain) domainView {
	return domainView{
		ID:                       d.ID,
		Name:                     d.Name,
		AllowMultipleConnections: d.AllowMultipleConnections,
		Passthrough:              d.Passthrough,
		AuthLatched:              d.AuthLatched,
		AllowedCIDRs:             splitRules(d.AllowedCIDRs),
	}
}

// isAuthorized compares the Authorization header with the admin key in constant
// time, so the response time does not reveal how many characters matched. An
// empty key never authorizes anyone.
func isAuthorized(c *gin.Context, adminKey string) bool {
	if adminKey == "" {
		return false
	}
	provided := []byte(c.Request.Header.Get("Authorization"))
	return subtle.ConstantTimeCompare(provided, []byte(adminKey)) == 1
}

// authorized checks the admin key. It guards every endpoint that changes data
// (create, update, delete). Read-only endpoints do not use it: the admin port
// listens on 127.0.0.1 only and they expose no keys.
func (r *router) authorized(c *gin.Context) bool {
	if isAuthorized(c, r.admin.adminKey) {
		return true
	}
	c.JSON(http.StatusUnauthorized, errorResponse{Error: "Unauthorized"})
	return false
}

// writeLookupError answers a failed domain lookup: 404 when the domain does not
// exist, 500 for anything else.
func writeLookupError(c *gin.Context, err error) {
	if errors.Is(err, auth.ErrDomainNotFound) {
		c.JSON(http.StatusNotFound, errorResponse{Error: "Domain not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, errorResponse{Error: "Unable to get domain"})
}

// getDomains is read-only and needs no key; see the note on authorized.
// @Summary List domains
// @Description Every domain with its policies. Client keys are never returned. Needs no key.
// @Tags domains
// @Produce json
// @Success 200 {array} domainView
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains [get]
func (r *router) getDomains(c *gin.Context) {

	domains, err := r.admin.authManager.GetDomains()
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "Unable to get domains"})
		return
	}

	views := make([]domainView, 0, len(domains))
	for _, d := range domains {
		views = append(views, toDomainView(d))
	}
	c.JSON(http.StatusOK, views)
}

// getDomain is read-only and needs no key; see the note on authorized.
// @Summary Get one domain
// @Description One domain with its policies. The client key is never returned. Needs no key.
// @Tags domains
// @Produce json
// @Param domainName path string true "Domain name"
// @Success 200 {object} domainView
// @Failure 404 {object} errorResponse "Domain or user not found"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains/{domainName} [get]
func (r *router) getDomain(c *gin.Context) {

	domainName := c.Param("domainName")
	domain, err := r.admin.authManager.GetDomain(domainName)
	if err != nil {
		writeLookupError(c, err)
		return
	}

	c.JSON(http.StatusOK, toDomainView(domain))
}

// @Summary Create a domain
// @Description Registers a domain with the key its client will use. Multiple connections are allowed unless allowMultipleConnections is false. allowedCidrs is a list of networks or addresses; empty means no restriction.
// @Tags domains
// @Security AdminKey
// @Accept json
// @Produce json
// @Param domain body domainCreateBody true "Domain to create"
// @Success 200 {object} statusResponse
// @Failure 400 {object} errorResponse "Bad request: a field is missing, malformed or not allowed"
// @Failure 401 {object} errorResponse "Missing or wrong admin key"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains [post]
func (r *router) addDomain(c *gin.Context) {
	if !r.authorized(c) {
		return
	}

	// Multiple connections are allowed unless the request says otherwise. The
	// default is set before binding, so an omitted field keeps it.
	domain := &auth.Domain{AllowMultipleConnections: true}
	raw, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err := json.Unmarshal(raw, domain); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	var rules struct {
		AllowedCIDRs []string `json:"allowedCidrs"`
	}
	if err := json.Unmarshal(raw, &rules); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "allowedCidrs must be a list of strings"})
		return
	}
	if domain.AllowedCIDRs, err = helper.NormalizeAccessRules(rules.AllowedCIDRs); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	if err := r.admin.authManager.AddDomain(domain); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "Unable to add domain"})
		return
	}

	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// @Summary Change a domain
// @Description Changes any of: apiKey, allowMultipleConnections, passthrough (not while the domain requires visitor auth), allowedCidrs (an empty list removes the rules), authLatched (only false: clearing the restricted mode). Clients of the domain are disconnected and reconnect with the new policy.
// @Tags domains
// @Security AdminKey
// @Accept json
// @Produce json
// @Param domainName path string true "Domain name"
// @Param changes body domainChangeBody true "Fields to change; send only those"
// @Success 200 {object} statusResponse
// @Failure 400 {object} errorResponse "Bad request: a field is missing, malformed or not allowed"
// @Failure 401 {object} errorResponse "Missing or wrong admin key"
// @Failure 404 {object} errorResponse "Domain or user not found"
// @Failure 409 {object} errorResponse "Conflict with the domain's current state"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains/{domainName} [patch]
func (r *router) updateDomain(c *gin.Context) {
	if !r.authorized(c) {
		return
	}

	domain := map[string]interface{}{}
	if err := c.BindJSON(&domain); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	domainName := c.Param("domainName")
	record, err := r.admin.authManager.GetDomain(domainName)
	if err != nil {
		writeLookupError(c, err)
		return
	}

	// Checked assertions: a wrong JSON type is a 400, not a panic.
	if raw, ok := domain["apiKey"]; ok {
		key, isString := raw.(string)
		if !isString || key == "" {
			c.JSON(http.StatusBadRequest, errorResponse{Error: "apiKey must be a non-empty string"})
			return
		}
		record.ApiKey = key
	}
	if raw, ok := domain["allowMultipleConnections"]; ok {
		allow, isBool := raw.(bool)
		if !isBool {
			c.JSON(http.StatusBadRequest, errorResponse{Error: "allowMultipleConnections must be true or false"})
			return
		}
		record.AllowMultipleConnections = allow
	}
	if raw, ok := domain["passthrough"]; ok {
		pass, isBool := raw.(bool)
		if !isBool {
			c.JSON(http.StatusBadRequest, errorResponse{Error: "passthrough must be true or false"})
			return
		}
		if pass && record.AuthLatched {
			c.JSON(http.StatusConflict, errorResponse{Error: "passthrough cannot be turned on: the domain requires visitor authentication"})
			return
		}
		record.Passthrough = pass
	}
	if raw, ok := domain["allowedCidrs"]; ok {
		list, isList := raw.([]interface{})
		if !isList {
			c.JSON(http.StatusBadRequest, errorResponse{Error: "allowedCidrs must be a list of addresses or networks"})
			return
		}
		items := make([]string, 0, len(list))
		for _, item := range list {
			text, isText := item.(string)
			if !isText {
				c.JSON(http.StatusBadRequest, errorResponse{Error: "allowedCidrs must be a list of strings"})
				return
			}
			items = append(items, text)
		}
		normalized, err := helper.NormalizeAccessRules(items)
		if err != nil {
			c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		record.AllowedCIDRs = normalized
	}
	if raw, ok := domain["authLatched"]; ok {
		// Clients set the restricted mode by asking for it. An admin can only clear it.
		clear, isBool := raw.(bool)
		if !isBool || clear {
			c.JSON(http.StatusBadRequest, errorResponse{Error: "authLatched can only be set to false: clients set the restricted mode"})
			return
		}
		record.AuthLatched = false
		logger.Default.Info(fmt.Sprintf("restricted mode cleared by admin for domain %s", record.Name))
	}

	if err := r.admin.authManager.UpdateDomain(record); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "Unable to update domain"})
		return
	}
	// A new key or a new policy must apply to the clients already connected.
	r.admin.domainChanged(record.Name)

	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// @Summary Delete a domain
// @Description Removes the domain and disconnects its clients.
// @Tags domains
// @Security AdminKey
// @Produce json
// @Param domainName path string true "Domain name"
// @Success 200 {object} statusResponse
// @Failure 401 {object} errorResponse "Missing or wrong admin key"
// @Failure 404 {object} errorResponse "Domain or user not found"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains/{domainName} [delete]
func (r *router) deleteDomain(c *gin.Context) {
	if !r.authorized(c) {
		return
	}

	domainName := c.Param("domainName")
	record, err := r.admin.authManager.GetDomain(domainName)
	if err != nil {
		writeLookupError(c, err)
		return
	}

	if err := r.admin.authManager.DelDomain(record.ID); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "Unable to delete domain"})
		return
	}
	r.admin.domainChanged(record.Name)

	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// @Summary Health check
// @Description Reports that the admin API is up. Needs no key.
// @Tags system
// @Produce json
// @Success 200 {object} statusResponse
// @Router /health [get]
func (r *router) health(c *gin.Context) {
	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// getTraffic returns the daily usage of one domain between two dates, both in
// YYYY-MM-DD format. It is in the admin API because it reveals per-domain usage.
// getTraffic is read-only and needs no key; see the note on authorized.
// @Summary Daily traffic of a domain
// @Description Bytes transferred per day between two dates, inclusive. Needs no key.
// @Tags domains
// @Produce json
// @Param domainName path string true "Domain name"
// @Param from query string true "First day, YYYY-MM-DD"
// @Param to query string true "Last day, YYYY-MM-DD"
// @Success 200 {object} trafficResponse
// @Failure 400 {object} errorResponse "Bad request: a field is missing, malformed or not allowed"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains/{domainName}/traffic [get]
func (r *router) getTraffic(c *gin.Context) {
	domainName := c.Param("domainName")
	fromParam := c.Query("from")
	toParam := c.Query("to")

	from, err := time.Parse("2006-01-02", fromParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "Invalid 'from' parameter, use YYYY-MM-DD"})
		return
	}
	to, err := time.Parse("2006-01-02", toParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "Invalid 'to' parameter, use YYYY-MM-DD"})
		return
	}
	to = to.Add(23*time.Hour + 59*time.Minute + 59*time.Second)

	conf, err := config.GetConfig()
	if err != nil {
		logger.Default.Error("Error getting configuration:", err)
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "Unable to get config"})
		return
	}

	connection, err := db.GetConnection(conf.Database)
	if err != nil {
		logger.Default.Error("Error connecting to database:", err)
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "Unable to connect to database"})
		return
	}

	var consumptions []db.DailyConsumption
	if err := connection.Where("domain = ? AND date >= ? AND date <= ?", domainName, from, to).Find(&consumptions).Error; err != nil {
		logger.Default.Error("Error reading traffic for domain:", domainName, "Error:", err)
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "Unable to read traffic"})
		return
	}

	c.JSON(http.StatusOK, trafficResponse{
		Domain:       domainName,
		From:         fromParam,
		To:           toParam,
		Consumptions: toDailyConsumptions(consumptions),
	})
}

// getStats returns the live state of every domain: clients, sessions, pending
// visitors, bytes since start, and intranet ports.
// getStats is read-only and needs no key; see the note on authorized.
// @Summary Live state of every domain
// @Description Clients, active sessions, pending visitors, bytes since start, and intranet ports for each domain with a hub. Needs no key.
// @Tags domains
// @Produce json
// @Success 200 {array} manager.DomainStats
// @Router /stats [get]
func (r *router) getStats(c *gin.Context) {
	if r.admin.stats == nil {
		c.JSON(http.StatusOK, []manager.DomainStats{})
		return
	}
	c.JSON(http.StatusOK, r.admin.stats())
}

// ui serves the panel. The page holds no data: it asks for the admin key and
// then calls /stats with it, so nothing sensitive is in the HTML itself.
// @Summary Admin panel
// @Description HTML panel that reads /stats, /requests and the daily traffic. Needs no key.
// @Tags system
// @Produce html
// @Success 200 {string} string "HTML page"
// @Router / [get]
func (r *router) ui(c *gin.Context) {
	c.Header("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Frame-Options", "DENY")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(panelHTML))
}

// getRequests returns finished requests, newest first. Filters: domain, port
// (the proxy or an intranet port), and a
// time range with from and to (RFC 3339 or YYYY-MM-DD; a date in "to" includes
// that whole day). Without a time range the newest requests come from memory.
// limit defaults to 100 and is capped at 500.
// getRequests is read-only and needs no key; see the note on authorized.
// @Summary Recent HTTP requests
// @Description Finished HTTP requests, newest first: domain, port the visitor came in on, method, path without query, status, duration and response size. Headers and bodies are never stored. Without from or to, the newest requests come from memory; with a time range they come from the database (kept 7 days). Needs no key.
// @Tags requests
// @Produce json
// @Param domain query string false "Only this domain"
// @Param port query int false "Only this port: 5050 is the proxy, others are intranet ports"
// @Param limit query int false "How many, 1 to 500" default(100)
// @Param from query string false "Start of the range: RFC 3339 or YYYY-MM-DD"
// @Param to query string false "End of the range: RFC 3339 or YYYY-MM-DD (a date includes the whole day)"
// @Success 200 {array} manager.RequestRecord
// @Failure 400 {object} errorResponse "Bad request: a field is missing, malformed or not allowed"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /requests [get]
func (r *router) getRequests(c *gin.Context) {
	q := manager.RequestQuery{Domain: c.Query("domain"), Limit: 100}
	if raw := c.Query("port"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 65535 {
			c.JSON(http.StatusBadRequest, errorResponse{Error: "port must be between 1 and 65535"})
			return
		}
		q.Port = n
	}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			c.JSON(http.StatusBadRequest, errorResponse{Error: "limit must be a positive number"})
			return
		}
		q.Limit = min(n, 500)
	}
	from, err := parseTimeParam(c.Query("from"), false)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "from must be RFC 3339 or YYYY-MM-DD"})
		return
	}
	to, err := parseTimeParam(c.Query("to"), true)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "to must be RFC 3339 or YYYY-MM-DD"})
		return
	}
	q.From, q.To = from, to

	if r.admin.requests == nil {
		c.JSON(http.StatusOK, []manager.RequestRecord{})
		return
	}
	list, err := r.admin.requests(q)
	if errors.Is(err, manager.ErrHistoryUnavailable) {
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "could not read the request log"})
		return
	}
	c.JSON(http.StatusOK, list)
}

// parseTimeParam accepts RFC 3339 or a calendar date. A date in an end position
// (asEnd) becomes the start of the following day, so the whole day is included.
// An empty value is the zero time.
func parseTimeParam(raw string, asEnd bool) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, err
	}
	if asEnd {
		t = t.AddDate(0, 0, 1)
	}
	return t, nil
}

// domainNameParam returns the domain named in the path, after checking the admin key.
func (r *router) visitorDomain(c *gin.Context) (string, bool) {
	if !r.authorized(c) {
		return "", false
	}
	name := c.Param("domainName")
	if _, err := r.admin.authManager.GetDomain(name); err != nil {
		writeLookupError(c, err)
		return "", false
	}
	return name, true
}

type visitorUserBody struct {
	Username string `json:"username"`
	// Secret is optional. If it is omitted, the server generates one and returns it once.
	Secret string `json:"secret,omitempty"`
}

// @Summary Add a visitor user
// @Description Creates a user of a restricted domain with its own secret. Send the secret in the body (at least 32 characters) or omit it: then the server generates one and returns it in this response, the only time it is shown. The user signs each request with HMAC-SHA256 using that secret and sends it in the Authorization header as LipstickHMAC user, ts, nonce and sig. Usernames: 1 to 64 letters, digits, dots, underscores, at signs or hyphens.
// @Tags visitor-auth
// @Security AdminKey
// @Accept json
// @Produce json
// @Param domainName path string true "Domain name"
// @Param user body visitorUserBody true "Username and optional secret"
// @Success 200 {object} visitorUserResponse
// @Failure 400 {object} errorResponse "Bad request: a field is missing, malformed or not allowed"
// @Failure 401 {object} errorResponse "Missing or wrong admin key"
// @Failure 404 {object} errorResponse "Domain or user not found"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains/{domainName}/visitor-users [post]
func (r *router) addVisitorUser(c *gin.Context) {
	name, ok := r.visitorDomain(c)
	if !ok {
		return
	}
	var body visitorUserBody
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	secret, err := r.admin.authManager.AddVisitorUser(name, body.Username, body.Secret)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	out := visitorUserResponse{Status: "ok", Username: body.Username}
	if body.Secret == "" {
		// Generated here, so it is shown once, in this response only.
		out.Secret = secret
	}
	c.JSON(http.StatusOK, out)
}

// rotateVisitorUser replaces a user's secret. The old one stops working at once.
// @Summary Rotate a visitor user's secret
// @Description Replaces the secret of a user and returns the new one, shown once. The old secret stops working at once.
// @Tags visitor-auth
// @Security AdminKey
// @Produce json
// @Param domainName path string true "Domain name"
// @Param username path string true "Username"
// @Success 200 {object} visitorUserResponse
// @Failure 401 {object} errorResponse "Missing or wrong admin key"
// @Failure 404 {object} errorResponse "Domain or user not found"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains/{domainName}/visitor-users/{username}/rotate [post]
func (r *router) rotateVisitorUser(c *gin.Context) {
	name, ok := r.visitorDomain(c)
	if !ok {
		return
	}
	username := c.Param("username")
	secret, err := r.admin.authManager.RotateVisitorUser(name, username)
	if errors.Is(err, auth.ErrUserNotFound) {
		c.JSON(http.StatusNotFound, errorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "unable to rotate secret"})
		return
	}
	c.JSON(http.StatusOK, visitorUserResponse{Status: "ok", Username: username, Secret: secret})
}

// @Summary List visitor users
// @Description Usernames of a domain. Secrets are never returned.
// @Tags visitor-auth
// @Security AdminKey
// @Produce json
// @Param domainName path string true "Domain name"
// @Success 200 {array} string
// @Failure 401 {object} errorResponse "Missing or wrong admin key"
// @Failure 404 {object} errorResponse "Domain or user not found"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains/{domainName}/visitor-users [get]
func (r *router) listVisitorUsers(c *gin.Context) {
	name, ok := r.visitorDomain(c)
	if !ok {
		return
	}
	users, err := r.admin.authManager.ListVisitorUsers(name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "unable to list users"})
		return
	}
	c.JSON(http.StatusOK, users)
}

// @Summary Delete a visitor user
// @Description Removes the user; their access ends at once.
// @Tags visitor-auth
// @Security AdminKey
// @Produce json
// @Param domainName path string true "Domain name"
// @Param username path string true "Username"
// @Success 200 {object} statusResponse
// @Failure 401 {object} errorResponse "Missing or wrong admin key"
// @Failure 404 {object} errorResponse "Domain or user not found"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains/{domainName}/visitor-users/{username} [delete]
func (r *router) deleteVisitorUser(c *gin.Context) {
	name, ok := r.visitorDomain(c)
	if !ok {
		return
	}
	if err := r.admin.authManager.DeleteVisitorUser(name, c.Param("username")); err != nil {
		c.JSON(http.StatusNotFound, errorResponse{Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

type visitorTokenBody struct {
	Name         string   `json:"name"`
	Methods      []string `json:"methods"`
	PathPrefixes []string `json:"pathPrefixes"`
}

// addVisitorToken returns the token once. It is not stored and cannot be read again.
// @Summary Create an API token
// @Description Creates a Bearer token with permissions: methods (or "*") and path prefixes. The token value is returned once; only its SHA-256 hash is stored. The response has no id: list the tokens to get it.
// @Tags visitor-auth
// @Security AdminKey
// @Accept json
// @Produce json
// @Param domainName path string true "Domain name"
// @Param token body visitorTokenBody true "Name, methods and path prefixes"
// @Success 200 {object} visitorTokenCreated
// @Failure 400 {object} errorResponse "Bad request: a field is missing, malformed or not allowed"
// @Failure 401 {object} errorResponse "Missing or wrong admin key"
// @Failure 404 {object} errorResponse "Domain or user not found"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains/{domainName}/visitor-tokens [post]
func (r *router) addVisitorToken(c *gin.Context) {
	name, ok := r.visitorDomain(c)
	if !ok {
		return
	}
	var body visitorTokenBody
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	token, err := r.admin.authManager.AddVisitorToken(name, body.Name, body.Methods, body.PathPrefixes)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, visitorTokenCreated{Name: body.Name, Token: token, Note: "shown once; store it now"})
}

// @Summary List API tokens
// @Description Tokens of a domain without their values: id, name, methods, path prefixes, creation time, and whether they are revoked.
// @Tags visitor-auth
// @Security AdminKey
// @Produce json
// @Param domainName path string true "Domain name"
// @Success 200 {array} visitorTokenItem
// @Failure 401 {object} errorResponse "Missing or wrong admin key"
// @Failure 404 {object} errorResponse "Domain or user not found"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains/{domainName}/visitor-tokens [get]
func (r *router) listVisitorTokens(c *gin.Context) {
	name, ok := r.visitorDomain(c)
	if !ok {
		return
	}
	recs, err := r.admin.authManager.ListVisitorTokens(name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "unable to list tokens"})
		return
	}
	out := make([]visitorTokenItem, 0, len(recs))
	for _, t := range recs {
		out = append(out, visitorTokenItem{
			ID: t.ID, Name: t.Name, Methods: t.Methods, PathPrefixes: t.PathPrefixes,
			CreatedAt: t.CreatedAt, Revoked: t.RevokedAt != nil,
		})
	}
	c.JSON(http.StatusOK, out)
}

// @Summary Revoke an API token
// @Description The token stops working at once.
// @Tags visitor-auth
// @Security AdminKey
// @Produce json
// @Param domainName path string true "Domain name"
// @Param id path int true "Token id"
// @Success 200 {object} statusResponse
// @Failure 400 {object} errorResponse "Bad request: a field is missing, malformed or not allowed"
// @Failure 401 {object} errorResponse "Missing or wrong admin key"
// @Failure 404 {object} errorResponse "Domain or user not found"
// @Failure 500 {object} errorResponse "Internal error"
// @Router /domains/{domainName}/visitor-tokens/{id} [delete]
func (r *router) revokeVisitorToken(c *gin.Context) {
	name, ok := r.visitorDomain(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "id must be a number"})
		return
	}
	if err := r.admin.authManager.RevokeVisitorToken(name, uint(id)); err != nil {
		c.JSON(http.StatusNotFound, errorResponse{Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, statusResponse{Status: "ok"})
}

// splitRules turns the stored comma-separated rules into the list the API shows.
func splitRules(stored string) []string {
	if stored == "" {
		return []string{}
	}
	return strings.Split(stored, ",")
}

// toDailyConsumptions converts the database rows to the documented response type.
func toDailyConsumptions(rows []db.DailyConsumption) []dailyConsumption {
	out := make([]dailyConsumption, 0, len(rows))
	for _, r := range rows {
		out = append(out, dailyConsumption{ID: r.ID, Domain: r.Domain, Date: r.Date, Month: r.Month,
			BytesUsed: r.BytesUsed, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
	}
	return out
}
