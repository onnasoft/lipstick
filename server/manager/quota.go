package manager

import (
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/auth"
	"github.com/OnnaSoft/lipstick/server/db"
	"github.com/OnnaSoft/lipstick/server/limits"
	"gorm.io/gorm"
)

// usageCacheTime is how long a domain's monthly usage is reused. The database is asked
// at most this often per domain, however many connections arrive.
const usageCacheTime = 10 * time.Second

// quotaState reads and caches the monthly usage of each domain from the database, so
// every server of a cluster counts the same traffic.
type quotaState struct {
	mu    sync.Mutex
	db    *gorm.DB
	cache map[string]cachedUsage
	now   func() time.Time
}

type cachedUsage struct {
	used int64
	at   time.Time
}

// SetQuotaStore gives the manager the database it reads monthly usage from.
func (manager *Manager) SetQuotaStore(conn *gorm.DB) {
	manager.quota.mu.Lock()
	defer manager.quota.mu.Unlock()
	manager.quota.db = conn
}

// monthlyUsage returns the bytes domain has used this calendar month. When the database
// does not answer, the usage is taken as 0 and the failure is logged: the quota then
// fails open, and the log says so.
func (q *quotaState) monthlyUsage(domain string) int64 {
	q.mu.Lock()
	if q.cache == nil {
		q.cache = map[string]cachedUsage{}
	}
	now := time.Now()
	if hit, ok := q.cache[domain]; ok && now.Sub(hit.at) < usageCacheTime {
		q.mu.Unlock()
		return hit.used
	}
	conn := q.db
	q.mu.Unlock()
	if conn == nil {
		return 0
	}
	month := now.Format("2006-01")
	var used int64
	err := conn.Model(&db.DailyConsumption{}).
		Select("COALESCE(SUM(bytes_used), 0)").
		Where("domain = ? AND month = ?", domain, month).
		Row().Scan(&used)
	if err != nil {
		logger.Default.Error("quota: cannot read the month's usage of", domain, "; the quota is not enforced for now:", err)
		return 0
	}
	q.mu.Lock()
	q.cache[domain] = cachedUsage{used: used, at: now}
	q.mu.Unlock()
	return used
}

// quotaRefusal returns a refusal when domain has used its monthly quota. The retry time
// is the start of the next calendar month.
func (manager *Manager) quotaRefusal(domain string) *limits.Refusal {
	quota := manager.settings.DomainMonthlyQuota
	if rec, ok := manager.domainRecord(domain); ok && rec.MonthlyQuotaBytes > 0 {
		quota = rec.MonthlyQuotaBytes
	}
	if quota <= 0 {
		return nil
	}
	if manager.quota.monthlyUsage(domain) < quota {
		return nil
	}
	now := time.Now()
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	return &limits.Refusal{Reason: limits.ReasonQuota, RetryAfter: nextMonth.Sub(now)}
}

// bandwidthFor returns the bytes per second for domain: its own setting, else the server
// default. 0 is unlimited.
func (manager *Manager) bandwidthFor(domain string) int64 {
	if rec, ok := manager.domainRecord(domain); ok && rec.BandwidthBytesPerSecond > 0 {
		return rec.BandwidthBytesPerSecond
	}
	return manager.settings.DomainBandwidth
}

// domainRecord returns the domain's record. It reports false when there is no record or
// no store, in which case the server defaults apply.
func (manager *Manager) domainRecord(domain string) (*auth.Domain, bool) {
	if manager.authManager == nil {
		return nil, false
	}
	rec, err := manager.authManager.GetDomain(domain)
	return rec, err == nil && rec != nil
}
