package admin

import (
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strconv"

	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/audit"
	"github.com/gin-gonic/gin"
)

// auditSink is what the admin API needs from the audit log. The implementation is
// audit.Store; tests use a memory fake.
type auditSink interface {
	Begin(e audit.Entry) (uint, error)
	Finish(id uint, r audit.Result) error
	Search(q audit.Query) ([]audit.Event, error)
}

// SetAudit sets the audit log. Without one, every change is refused: nothing may
// change without a record.
func (a *Admin) SetAudit(sink auditSink) {
	a.audit = sink
}

// clientAddress is the address the connection came from. Forwarded headers are not
// read: any client could set them, and the record would lie.
func clientAddress(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

// The handler sets these keys on the context to describe what it did. The wrapper
// reads them after the handler has answered.
const (
	auditActorKey   = "auditActor"
	auditTargetKey  = "auditTarget"
	auditDetailsKey = "auditDetails"
)

// setAudit lets a handler name the object it changed and the fields it touched.
// Values of secrets, keys and passwords never go here: names only.
func setAudit(c *gin.Context, target string, details map[string]any) {
	if target != "" {
		c.Set(auditTargetKey, target)
	}
	if details != nil {
		c.Set(auditDetailsKey, details)
	}
}

// audited wraps a change so it is recorded. The attempt is written first, as pending.
// If that write fails, the change is refused with 503 and nothing happens. After the
// handler answers, the entry is completed as succeeded or failed.
func (r *router) audited(action string, target func(*gin.Context) string, handle gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if r.admin.audit == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, errorResponse{Error: "the change was not made: no audit log is configured"})
			return
		}
		first := ""
		if target != nil {
			first = target(c)
		}
		id, err := r.admin.audit.Begin(audit.Entry{
			Actor:      c.GetString("adminUser"),
			Action:     action,
			Target:     first,
			ClientAddr: clientAddress(c.Request),
			UserAgent:  c.Request.UserAgent(),
		})
		if err != nil {
			logger.Default.Error("audit: could not record", action, "; the change was not made:", err)
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, errorResponse{Error: "the change was not made: the audit log is not available"})
			return
		}

		handle(c)

		status := c.Writer.Status()
		outcome := audit.OutcomeSucceeded
		if status >= 400 {
			outcome = audit.OutcomeFailed
		}
		result := audit.Result{
			Actor:      c.GetString(auditActorKey),
			Target:     c.GetString(auditTargetKey),
			StatusCode: status,
			Outcome:    outcome,
		}
		if d, ok := c.Get(auditDetailsKey); ok {
			result.Details, _ = d.(map[string]any)
		}
		if err := r.admin.audit.Finish(id, result); err != nil {
			// The change has already happened. The entry stays pending, which shows
			// that the outcome was not recorded; the log says why.
			logger.Default.Error("audit: could not complete entry", id, action, ":", err)
		}
	}
}

// targetOf returns a route parameter as the audit target.
func targetOf(param string) func(*gin.Context) string {
	return func(c *gin.Context) string { return c.Param(param) }
}

// @Summary Search the audit log
// @Description Every change and login attempt, newest first: who (actor), what (action), which object (target), the outcome, the status code, the client address, and the names of the fields changed. Values of keys, secrets and passwords are never recorded. Superusers only.
// @Tags audit
// @Security BearerAuth
// @Produce json
// @Param actor query string false "Only this account"
// @Param action query string false "Only this action, for example domain.update"
// @Param target query string false "Only this object"
// @Param outcome query string false "pending, succeeded or failed"
// @Param from query string false "Start: RFC 3339 or YYYY-MM-DD"
// @Param to query string false "End: RFC 3339 or YYYY-MM-DD (a date includes the whole day)"
// @Param limit query int false "How many, 1 to 500" default(100)
// @Success 200 {array} audit.Event
// @Failure 400 {object} errorResponse "A parameter is not valid"
// @Failure 401 {object} errorResponse "Missing, expired or invalid session. Error: 'Unauthorized'"
// @Failure 403 {object} errorResponse "Only a superuser can do this"
// @Failure 503 {object} errorResponse "The audit log does not answer"
// @Router /audit [get]
func (r *router) searchAudit(c *gin.Context) {
	if !requireSuperuser(c) {
		return
	}
	if r.admin.audit == nil {
		c.JSON(http.StatusServiceUnavailable, errorResponse{Error: "the audit log is not configured"})
		return
	}
	q := audit.Query{
		Actor:   c.Query("actor"),
		Action:  c.Query("action"),
		Target:  c.Query("target"),
		Outcome: c.Query("outcome"),
		Limit:   100,
	}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			c.JSON(http.StatusBadRequest, errorResponse{Error: "limit must be between 1 and 500"})
			return
		}
		q.Limit = n
	}
	var err error
	if raw := c.Query("from"); raw != "" {
		if q.From, err = parseTimeParam(raw, false); err != nil {
			c.JSON(http.StatusBadRequest, errorResponse{Error: "from must be RFC 3339 or YYYY-MM-DD"})
			return
		}
	}
	if raw := c.Query("to"); raw != "" {
		if q.To, err = parseTimeParam(raw, true); err != nil {
			c.JSON(http.StatusBadRequest, errorResponse{Error: "to must be RFC 3339 or YYYY-MM-DD"})
			return
		}
	}
	events, err := r.admin.audit.Search(q)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, errorResponse{Error: "the audit log is not available right now"})
		return
	}
	c.JSON(http.StatusOK, events)
}

// targetOf2 joins two route parameters into one target, as "domain/name".
func targetOf2(first, second string) func(*gin.Context) string {
	return func(c *gin.Context) string { return c.Param(first) + "/" + c.Param(second) }
}

// currentAccount is the target for a change to the caller's own account.
func currentAccount(c *gin.Context) string { return c.GetString("adminUser") }

// bodyFields returns the names of the fields in a JSON object, sorted. Values are not returned.
func bodyFields(raw []byte) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return mapKeys(m)
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
