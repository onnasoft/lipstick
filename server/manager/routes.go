package manager

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/OnnaSoft/lipstick/logger"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type router struct {
	manager *Manager
}

// The manager port is reachable by tunnel clients and must not reveal anything
// about domains. Traffic statistics live in the admin API, which requires the
// admin key.
func configureRouter(manager *Manager) {
	router := &router{manager: manager}
	r := gin.New()

	r.GET("/health", router.health)
	r.GET("/", router.upgrade)

	manager.engine = r
}

// health answers the same way for every host and every domain, so it cannot be
// used to find out which domains exist or how much they transfer.
func (r *router) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (r *router) upgrade(c *gin.Context) {
	host := c.Request.Host
	domainName := strings.Split(host, ":")[0]
	domain, err := r.manager.authManager.GetDomain(domainName)
	if err != nil {
		logger.Default.Error("Unable to get domain:", domainName, "Error:", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Domain not found"})
		return
	}

	// Authenticate the tunnel client before hijacking the connection.
	// An empty ApiKey must never match, so a misconfigured domain stays closed.
	providedKey := c.GetHeader("Authorization")
	if domain.ApiKey == "" || subtle.ConstantTimeCompare([]byte(providedKey), []byte(domain.ApiKey)) != 1 {
		logger.Default.Error("Unauthorized tunnel client for domain:", domainName)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	w := c.Writer
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		logger.Default.Error("Hijacking not supported on writer")
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	conn, rw, err := hijacker.Hijack()
	if err != nil {
		logger.Default.Error("Failed to hijack connection:", err)
		http.Error(w, "Failed to hijack connection", http.StatusInternalServerError)
		return
	}

	hub := r.manager.getOrCreateHub(domain.Name)

	rw.WriteString("HTTP/1.1 200 OK\r\n")
	rw.WriteString("Content-Type: text/plain\r\n")
	rw.WriteString("\r\n")
	if err := rw.Flush(); err != nil {
		logger.Default.Error("Error flushing headers for upgrade:", err)
		return
	}

	logger.Default.Info("Connection upgraded for domain:", domain.Name)
	notification := &ProxyNotificationConn{
		Domain:                   domain.Name,
		conn:                     conn,
		ReadWriter:               rw,
		AllowMultipleConnections: domain.AllowMultipleConnections,
	}

	if !hub.registerClient(notification) {
		logger.Default.Error("hub stopped before the client could register:", domain.Name)
		conn.Close()
	}
}
