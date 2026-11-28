package manager

import (
	"bufio"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
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
// session token.
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

	// A client may ask for an intranet port in the upgrade request. The server
	// checks the request against its policy. A refusal is written on this
	// connection, so the client can read the status line and report it.
	if raw := c.GetHeader(intranetPortHeader); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil {
			err = ErrPortOutOfRange
		} else {
			err = r.manager.reserveIntranetPort(domain.Name, port)
		}
		if err != nil {
			logger.Default.Error("Intranet port request refused, domain:", domain.Name, "port:", raw, "reason:", err)
			writeRefusal(rw, conn, err)
			return
		}
	}

	// The same for a UDP port, which may be asked for alongside the TCP one.
	if raw := c.GetHeader(intranetUDPPortHeader); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil {
			err = ErrPortOutOfRange
		} else {
			err = r.manager.reserveIntranetUDPPort(domain.Name, port)
		}
		if err != nil {
			logger.Default.Error("Intranet UDP port request refused, domain:", domain.Name, "port:", raw, "reason:", err)
			writeRefusal(rw, conn, err)
			return
		}
	}

	// A client may ask for visitor authentication. Asking makes the domain restricted
	// for good: it stays restricted for every client until an admin clears it.
	if c.GetHeader(visitorAuthHeader) == "required" {
		if domain.Passthrough {
			writeReason(rw, conn, "409 Conflict: visitor authentication cannot be used with passthrough")
			return
		}
		n, err := r.manager.authManager.CountVisitorCredentials(domain.Name)
		if err != nil || n == 0 {
			writeReason(rw, conn, "409 Conflict: the domain has no visitor users or tokens; create one first")
			return
		}
		if !domain.AuthLatched {
			if err := r.manager.authManager.SetAuthLatched(domain.Name, true); err != nil {
				writeReason(rw, conn, "500 Internal Server Error: cannot store the restricted mode")
				return
			}
			logger.Default.Info(fmt.Sprintf("restricted mode latched for domain %s, requested by client at %s", domain.Name, conn.RemoteAddr()))
		}
	} else if domain.AuthLatched {
		logger.Default.Info(fmt.Sprintf("client connected without requested auth: domain %s remains restricted (client at %s)", domain.Name, conn.RemoteAddr()))
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

// intranetPortHeader carries the port a client asks to reserve.
const intranetPortHeader = "X-Lipstick-Intranet-Port"

// visitorAuthHeader is sent by a client that asks for visitor authentication.
const visitorAuthHeader = "X-Lipstick-Visitor-Auth"

// writeReason answers the upgrade with a status line that carries the reason, and closes.
func writeReason(rw *bufio.ReadWriter, conn net.Conn, status string) {
	rw.WriteString("HTTP/1.1 " + status + "\r\n")
	rw.WriteString("Content-Length: 0\r\n\r\n")
	rw.Flush()
	conn.Close()
}

// intranetUDPPortHeader carries the UDP port a client asks to reserve.
const intranetUDPPortHeader = "X-Lipstick-Intranet-UDP-Port"

// writeRefusal answers a request that the policy does not allow, with the
// status the client can read, and closes the connection.
func writeRefusal(rw *bufio.ReadWriter, conn net.Conn, err error) {
	status := "403 Forbidden"
	if errors.Is(err, ErrPortTaken) {
		status = "409 Conflict"
	}
	rw.WriteString("HTTP/1.1 " + status + "\r\n")
	rw.WriteString("Content-Type: text/plain\r\n")
	rw.WriteString("\r\n")
	rw.WriteString(err.Error() + "\n")
	rw.Flush()
	conn.Close()
}
