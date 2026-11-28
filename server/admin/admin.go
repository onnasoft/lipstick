package admin

import (
	"crypto/tls"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"

	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/OnnaSoft/lipstick/server/limits"
	"github.com/OnnaSoft/lipstick/server/manager"
	"github.com/OnnaSoft/lipstick/server/metrics"
	"github.com/gin-gonic/gin"
)

type Admin struct {
	engine      *gin.Engine
	authManager auth.AuthManager
	addr        string
	store       adminStore   // admin accounts and sessions
	audit       auditSink    // who changed what, see audit.go
	addrLimit   *limits.Rate // requests per second per address, nil = no limit
	logins      loginLimiter
	tlsConfig   *tls.Config
	onChanged   func(domain string)
	stats       func() ([]manager.DomainStats, error)
	health      []healthCheck
	requests    func(q manager.RequestQuery) ([]manager.RequestRecord, error)
	metrics     func() []metrics.Family // read on each /metrics scrape
	readiness   func() []string         // names of the conditions not met, see SetReadiness
}

// SetRequestSource sets the function that returns finished requests.
func (a *Admin) SetRequestSource(fn func(q manager.RequestQuery) ([]manager.RequestRecord, error)) {
	a.requests = fn
}

// SetStatsSource sets the function that returns the state of every domain in the
// cluster. It reads the shared database, so every server gives the same answer.
func (a *Admin) SetStatsSource(fn func() ([]manager.DomainStats, error)) {
	a.stats = fn
}

// SetDomainChangedHook sets the function called when a domain is deleted or
// updated. The manager uses it to disconnect the domain's clients, so they
// reconnect under the current configuration.
func (a *Admin) SetDomainChangedHook(fn func(domain string)) {
	a.onChanged = fn
}

func (a *Admin) domainChanged(domain string) {
	if a.onChanged != nil {
		a.onChanged(domain)
	}
}

// SetupAdmin builds the admin API. Changes need a session token from /login, so
// the API is served over TLS.
func SetupAdmin(addr string, store adminStore, tlsConfig *tls.Config) *Admin {
	gin.SetMode(gin.ReleaseMode)

	admin := &Admin{
		authManager: auth.MakeAuthManager(),
		addr:        addr,
		store:       store,
		tlsConfig:   tlsConfig,
	}

	configureRouter(admin)

	return admin
}

func (a *Admin) Listen() {
	if a.tlsConfig == nil {
		log.Fatal("admin API has no TLS configuration; TLS is required")
	}
	logger.Default.Info("Listening admin (TLS) on ", a.addr)
	listener, err := tls.Listen("tcp", a.addr, a.tlsConfig)
	if err != nil {
		log.Fatalf("admin API cannot listen on %s: %v (check admin.address in the configuration)", a.addr, err)
	}

	if err := a.engine.RunListener(listener); err != nil {
		log.Fatalf("admin API stopped serving: %v", err)
	}
}

// healthCheck is one dependency /health looks at: the database, the cluster bus.
type healthCheck struct {
	name  string
	check func() error
}

// SetReadiness sets the server's own readiness conditions. The function returns the names
// of the conditions that are not met, such as "draining"; none means the server can take
// new traffic. The dependencies of /health are checked as well.
func (a *Admin) SetReadiness(fn func() []string) {
	a.readiness = fn
}

// SetMetricsSource sets what /metrics reports. Without one, /metrics answers 503.
func (a *Admin) SetMetricsSource(fn func() []metrics.Family) {
	a.metrics = fn
}

// SetHealthChecks replaces the dependencies /health checks. Each check should be quick.
func (a *Admin) SetHealthChecks(checks map[string]func() error) {
	a.health = nil
	for name, fn := range checks {
		a.health = append(a.health, healthCheck{name: name, check: fn})
	}
	sort.Slice(a.health, func(i, j int) bool { return a.health[i].name < a.health[j].name })
}

// SetRateLimit limits the requests each address may make to the admin API. A nil
// limiter means no limit.
func (a *Admin) SetRateLimit(limiter *limits.Rate) {
	a.addrLimit = limiter
}

// addressRateGate refuses requests from an address that exceeded its rate, with 429
// and Retry-After. It runs before the session check, so a flood of logins is cut here.
func (r *router) addressRateGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if ok, wait := r.admin.addrLimit.Allow(clientAddress(c.Request)); !ok {
			seconds := int(math.Ceil(wait.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			c.Header("Retry-After", strconv.Itoa(seconds))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, errorResponse{Error: "too many requests from this address; wait before trying again"})
			return
		}
		c.Next()
	}
}
