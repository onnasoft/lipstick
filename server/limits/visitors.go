package limits

import "time"

// Settings are the limits. A value of 0 disables that limit.
type Settings struct {
	// VisitorIPRate is new visitor connections per second from one IP address.
	VisitorIPRate  float64
	VisitorIPBurst int
	// DomainRate is new visitor connections per second for one domain.
	DomainRate  float64
	DomainBurst int
	// ConnsPerIP and ConnsPerDomain are open visitor connections at the same time.
	ConnsPerIP     int
	ConnsPerDomain int
	// AdminIPRate is admin API requests per second from one IP address.
	AdminIPRate  float64
	AdminIPBurst int
	// DomainBandwidth is bytes per second for a domain's visitor data, used when the domain
	// sets none. DomainMonthlyQuota is bytes per calendar month, likewise. 0 is unlimited.
	DomainBandwidth    int64
	DomainMonthlyQuota int64
}

// Defaults are generous: a busy office behind one address, or a page that opens many
// connections, stays far below them. Lower them in config.yml to tighten.
func Defaults() Settings {
	return Settings{
		VisitorIPRate:  50,
		VisitorIPBurst: 100,
		DomainRate:     500,
		DomainBurst:    1000,
		ConnsPerIP:     512,
		ConnsPerDomain: 4096,
		AdminIPRate:    20,
		AdminIPBurst:   60,
	}
}

// Why a visitor connection was refused.
const (
	ReasonIPRate      = "ip_rate"
	ReasonDomainRate  = "domain_rate"
	ReasonIPConns     = "ip_connections"
	ReasonDomainConns = "domain_connections"
	ReasonQuota       = "monthly_quota"
)

// Refusal says why a connection was not admitted, and when to try again.
type Refusal struct {
	Reason     string
	RetryAfter time.Duration
}

// maxTrackedKeys bounds the IP and domain tables.
const maxTrackedKeys = 100000

// Visitors decides admission of visitor connections.
type Visitors struct {
	ip          *Rate
	domain      *Rate
	ipConns     *Concurrency
	domainConns *Concurrency
}

// NewVisitors builds the visitor limits from s.
func NewVisitors(s Settings, now Clock) *Visitors {
	return &Visitors{
		ip:          NewRate(s.VisitorIPRate, s.VisitorIPBurst, maxTrackedKeys, now),
		domain:      NewRate(s.DomainRate, s.DomainBurst, maxTrackedKeys, now),
		ipConns:     NewConcurrency(s.ConnsPerIP),
		domainConns: NewConcurrency(s.ConnsPerDomain),
	}
}

// Admit checks the limits for a new connection from ip to domain, in order: the rate
// for the address, the rate for the domain, the open connections of the address, and
// the open connections of the domain. When all pass, it reserves one open connection
// for each and returns the release, which must be called when the connection closes.
// A nil Visitors imposes no limits; the server always builds one, so this only serves
// code that builds a manager without limits.
func (v *Visitors) Admit(ip, domain string) (release func(), refused *Refusal) {
	if v == nil {
		return func() {}, nil
	}
	if ok, wait := v.ip.Allow(ip); !ok {
		return nil, &Refusal{Reason: ReasonIPRate, RetryAfter: wait}
	}
	if ok, wait := v.domain.Allow(domain); !ok {
		return nil, &Refusal{Reason: ReasonDomainRate, RetryAfter: wait}
	}
	releaseIP, ok := v.ipConns.Acquire(ip)
	if !ok {
		return nil, &Refusal{Reason: ReasonIPConns, RetryAfter: time.Second}
	}
	releaseDomain, ok := v.domainConns.Acquire(domain)
	if !ok {
		releaseIP()
		return nil, &Refusal{Reason: ReasonDomainConns, RetryAfter: time.Second}
	}
	return func() {
		releaseDomain()
		releaseIP()
	}, nil
}

// AdminLimiter returns the per-address limiter for the admin API.
func AdminLimiter(s Settings, now Clock) *Rate {
	return NewRate(s.AdminIPRate, s.AdminIPBurst, maxTrackedKeys, now)
}

// OpenConnections returns the visitor connections open now, across all addresses.
func (v *Visitors) OpenConnections() int {
	if v == nil {
		return 0
	}
	return v.ipConns.Total()
}
