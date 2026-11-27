package admin

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/OnnaSoft/lipstick/server/db"
	"github.com/gin-gonic/gin"
)

type router struct {
	admin *Admin
}

func configureRouter(admin *Admin) {
	router := &router{admin: admin}
	r := gin.New()

	r.GET("/health", router.health)

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

func (r *router) authorized(c *gin.Context) bool {
	if isAuthorized(c, r.admin.adminKey) {
		return true
	}
	c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
	return false
}

func (r *router) getDomains(c *gin.Context) {
	if !r.authorized(c) {
		return
	}

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

func (r *router) getDomain(c *gin.Context) {
	if !r.authorized(c) {
		return
	}

	domainName := c.Param("domainName")
	domain, err := r.admin.authManager.GetDomain(domainName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to get domain"})
		return
	}

	c.JSON(http.StatusOK, toDomainView(domain))
}

func (r *router) addDomain(c *gin.Context) {
	if !r.authorized(c) {
		return
	}

	domain := &auth.Domain{}
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to get domain"})
		return
	}

	if _, ok := domain["apiKey"]; ok {
		record.ApiKey = domain["apiKey"].(string)
	}
	if _, ok := domain["allowMultipleConnections"]; ok {
		record.AllowMultipleConnections = domain["allowMultipleConnections"].(bool)
	}

	if err := r.admin.authManager.UpdateDomain(record); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to update domain"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (r *router) deleteDomain(c *gin.Context) {
	if !r.authorized(c) {
		return
	}

	domainName := c.Param("domainName")
	record, err := r.admin.authManager.GetDomain(domainName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to get domain"})
		return
	}

	if err := r.admin.authManager.DelDomain(record.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to delete domain"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (r *router) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// getTraffic returns the daily usage of one domain between two dates, both in
// YYYY-MM-DD format. It is in the admin API because it reveals per-domain usage.
func (r *router) getTraffic(c *gin.Context) {
	if !r.authorized(c) {
		return
	}

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
