package admin

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/OnnaSoft/lipstick/server/db"
	"github.com/OnnaSoft/lipstick/server/manager"
	"github.com/gin-gonic/gin"
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

	r.GET("/domains", router.getDomains)
	const domainNamePath = "/domains/:domainName"

	r.GET(domainNamePath, router.getDomain)
	r.GET(domainNamePath+"/traffic", router.getTraffic)
	r.POST("/domains", router.addDomain)
	r.PATCH(domainNamePath, router.updateDomain)
	r.DELETE(domainNamePath, router.deleteDomain)

	admin.engine = r
}

// domainView is what the admin API returns for a domain. It leaves out the
// tunnel API key, which is write-only: it can be set, but never read back.
type domainView struct {
	ID                       uint   `json:"id"`
	Name                     string `json:"name"`
	AllowMultipleConnections bool   `json:"allowMultipleConnections"`
}

func toDomainView(d *auth.Domain) domainView {
	return domainView{
		ID:                       d.ID,
		Name:                     d.Name,
		AllowMultipleConnections: d.AllowMultipleConnections,
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
	c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
	return false
}

// writeLookupError answers a failed domain lookup: 404 when the domain does not
// exist, 500 for anything else.
func writeLookupError(c *gin.Context, err error) {
	if errors.Is(err, auth.ErrDomainNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Domain not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to get domain"})
}

// getDomains is read-only and needs no key; see the note on authorized.
func (r *router) getDomains(c *gin.Context) {

	domains, err := r.admin.authManager.GetDomains()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to get domains"})
		return
	}

	views := make([]domainView, 0, len(domains))
	for _, d := range domains {
		views = append(views, toDomainView(d))
	}
	c.JSON(http.StatusOK, views)
}

// getDomain is read-only and needs no key; see the note on authorized.
func (r *router) getDomain(c *gin.Context) {

	domainName := c.Param("domainName")
	domain, err := r.admin.authManager.GetDomain(domainName)
	if err != nil {
		writeLookupError(c, err)
		return
	}

	c.JSON(http.StatusOK, toDomainView(domain))
}

func (r *router) addDomain(c *gin.Context) {
	if !r.authorized(c) {
		return
	}

	// Multiple connections are allowed unless the request says otherwise. The
	// default is set before binding, so an omitted field keeps it.
	domain := &auth.Domain{AllowMultipleConnections: true}
	if err := c.BindJSON(domain); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := r.admin.authManager.AddDomain(domain); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to add domain"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (r *router) updateDomain(c *gin.Context) {
	if !r.authorized(c) {
		return
	}

	domain := map[string]interface{}{}
	if err := c.BindJSON(&domain); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "apiKey must be a non-empty string"})
			return
		}
		record.ApiKey = key
	}
	if raw, ok := domain["allowMultipleConnections"]; ok {
		allow, isBool := raw.(bool)
		if !isBool {
			c.JSON(http.StatusBadRequest, gin.H{"error": "allowMultipleConnections must be true or false"})
			return
		}
		record.AllowMultipleConnections = allow
	}

	if err := r.admin.authManager.UpdateDomain(record); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to update domain"})
		return
	}
	// A new key or a new policy must apply to the clients already connected.
	r.admin.domainChanged(record.Name)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to delete domain"})
		return
	}
	r.admin.domainChanged(record.Name)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (r *router) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// getTraffic returns the daily usage of one domain between two dates, both in
// YYYY-MM-DD format. It is in the admin API because it reveals per-domain usage.
// getTraffic is read-only and needs no key; see the note on authorized.
func (r *router) getTraffic(c *gin.Context) {
	domainName := c.Param("domainName")
	fromParam := c.Query("from")
	toParam := c.Query("to")

	from, err := time.Parse("2006-01-02", fromParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid 'from' parameter, use YYYY-MM-DD"})
		return
	}
	to, err := time.Parse("2006-01-02", toParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid 'to' parameter, use YYYY-MM-DD"})
		return
	}
	to = to.Add(23*time.Hour + 59*time.Minute + 59*time.Second)

	conf, err := config.GetConfig()
	if err != nil {
		logger.Default.Error("Error getting configuration:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to get config"})
		return
	}

	connection, err := db.GetConnection(conf.Database)
	if err != nil {
		logger.Default.Error("Error connecting to database:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to connect to database"})
		return
	}

	var consumptions []db.DailyConsumption
	if err := connection.Where("domain = ? AND date >= ? AND date <= ?", domainName, from, to).Find(&consumptions).Error; err != nil {
		logger.Default.Error("Error reading traffic for domain:", domainName, "Error:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to read traffic"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"domain":       domainName,
		"from":         fromParam,
		"to":           toParam,
		"consumptions": consumptions,
	})
}

// getStats returns the live state of every domain: clients, sessions, pending
// visitors, bytes since start, and intranet ports.
// getStats is read-only and needs no key; see the note on authorized.
func (r *router) getStats(c *gin.Context) {
	if r.admin.stats == nil {
		c.JSON(http.StatusOK, []manager.DomainStats{})
		return
	}
	c.JSON(http.StatusOK, r.admin.stats())
}

// ui serves the panel. The page holds no data: it asks for the admin key and
// then calls /stats with it, so nothing sensitive is in the HTML itself.
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
func (r *router) getRequests(c *gin.Context) {
	q := manager.RequestQuery{Domain: c.Query("domain"), Limit: 100}
	if raw := c.Query("port"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 65535 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "port must be between 1 and 65535"})
			return
		}
		q.Port = n
	}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive number"})
			return
		}
		q.Limit = min(n, 500)
	}
	from, err := parseTimeParam(c.Query("from"), false)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from must be RFC 3339 or YYYY-MM-DD"})
		return
	}
	to, err := parseTimeParam(c.Query("to"), true)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to must be RFC 3339 or YYYY-MM-DD"})
		return
	}
	q.From, q.To = from, to

	if r.admin.requests == nil {
		c.JSON(http.StatusOK, []manager.RequestRecord{})
		return
	}
	list, err := r.admin.requests(q)
	if errors.Is(err, manager.ErrHistoryUnavailable) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read the request log"})
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
