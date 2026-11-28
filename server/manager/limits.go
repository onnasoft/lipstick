package manager

import (
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/limits"
)

// ConfigureLimits replaces the visitor limits. It is called once at startup.
func (manager *Manager) ConfigureLimits(s limits.Settings) {
	manager.settings = s
	manager.visitors = limits.NewVisitors(s, nil)
}

// admitVisitor applies the visitor limits to a new connection to domain. It returns the
// connection to serve from now on, which releases its reserved slot when it closes. A
// refused connection is answered (HTTP, with 429) or closed, and ok is false.
//
// A connection that is already a *helper.RemoteConn was admitted by the route that
// handed it over, so it is not counted twice.
func (manager *Manager) admitVisitor(conn net.Conn, domain string, answerHTTP bool) (*helper.RemoteConn, bool) {
	if rc, ok := conn.(*helper.RemoteConn); ok {
		return rc, true
	}
	// A disabled domain serves nobody: its visitors get 503 and nothing is counted against
	// its limits.
	if rec, ok := manager.domainRecord(domain); ok && rec.Disabled {
		manager.refusals.note("domain_disabled", domain, conn.RemoteAddr())
		if answerHTTP {
			fmt.Fprint(conn, "HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		}
		conn.Close()
		return nil, false
	}
	refused := manager.quotaRefusal(domain)
	var release func()
	if refused == nil {
		release, refused = manager.visitors.Admit(hostOnly(conn.RemoteAddr()), domain)
	}
	if refused == nil {
		return &helper.RemoteConn{Conn: conn, Domain: domain, OnClose: release}, true
	}
	manager.refusals.note(refused.Reason, domain, conn.RemoteAddr())
	if answerHTTP {
		seconds := int(math.Ceil(refused.RetryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		fmt.Fprintf(conn, "HTTP/1.1 429 Too Many Requests\r\nRetry-After: %d\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", seconds)
	}
	conn.Close()
	return nil, false
}

// hostOnly returns the IP of an address, without its port.
func hostOnly(addr net.Addr) string {
	if ip := helper.RemoteIP(addr); ip != nil {
		return ip.String()
	}
	return ""
}

// refusalLog writes at most one line per second about refused connections, with how
// many were refused since the last line. A flood must not fill the log.
type refusalLog struct {
	mu      sync.Mutex
	last    time.Time
	count   int
	reasons map[string]int
	totals  map[string]int64 // since start, per reason, for /metrics
}

func (r *refusalLog) note(reason, domain string, from net.Addr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reasons == nil {
		r.reasons = map[string]int{}
	}
	r.count++
	r.reasons[reason]++
	if r.totals == nil {
		r.totals = map[string]int64{}
	}
	r.totals[reason]++
	if time.Since(r.last) < time.Second {
		return
	}
	logger.Default.Info(fmt.Sprintf("visitor limits: %d connections refused in the last second (%v), last one to %s from %v",
		r.count, r.reasons, domain, from))
	r.last = time.Now()
	r.count = 0
	r.reasons = map[string]int{}
}

// Totals returns the refused connections since start, per reason.
func (r *refusalLog) Totals() map[string]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int64, len(r.totals))
	for k, v := range r.totals {
		out[k] = v
	}
	return out
}
