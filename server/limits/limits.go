// Package limits holds the counters behind the server's rate and connection limits.
// Rates are token buckets per key (an IP address or a domain). Concurrency is a count
// of open connections per key. Memory is bounded: when the table of keys is full, idle
// keys are dropped, and a new key that still does not fit is refused.
package limits

import (
	"sync"
	"time"
)

// Clock returns the current time. Tests replace it.
type Clock func() time.Time

// Rate is a token bucket per key. A rate of 0 disables it.
type Rate struct {
	mu      sync.Mutex
	rate    float64 // tokens added per second
	burst   float64 // most tokens a key can save up
	maxKeys int
	now     Clock
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRate allows perSecond events per key on average, with bursts up to burst.
func NewRate(perSecond float64, burst int, maxKeys int, now Clock) *Rate {
	if now == nil {
		now = time.Now
	}
	return &Rate{rate: perSecond, burst: float64(burst), maxKeys: maxKeys, now: now, buckets: map[string]*bucket{}}
}

// Allow takes one token for key. When it refuses, retryAfter says how long to wait.
func (r *Rate) Allow(key string) (ok bool, retryAfter time.Duration) {
	if r == nil || r.rate <= 0 {
		return true, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	b, known := r.buckets[key]
	if !known {
		if len(r.buckets) >= r.maxKeys {
			r.prune(now)
		}
		if len(r.buckets) >= r.maxKeys {
			// Full of keys in use: refuse the new one rather than forget a limit.
			return false, time.Second
		}
		b = &bucket{tokens: r.burst, last: now}
		r.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * r.rate
	if b.tokens > r.burst {
		b.tokens = r.burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / r.rate * float64(time.Second))
	return false, wait
}

// prune drops keys whose bucket is full: they carry no information. Called with mu held.
func (r *Rate) prune(now time.Time) {
	for k, b := range r.buckets {
		refilled := b.tokens + now.Sub(b.last).Seconds()*r.rate
		if refilled >= r.burst {
			delete(r.buckets, k)
		}
	}
}

// Concurrency counts open connections per key. A max of 0 disables it.
type Concurrency struct {
	mu     sync.Mutex
	max    int
	counts map[string]int
}

// NewConcurrency allows up to max open connections per key.
func NewConcurrency(max int) *Concurrency {
	return &Concurrency{max: max, counts: map[string]int{}}
}

// Acquire reserves one slot for key. The returned release must be called once when the
// connection closes; calling it again does nothing.
func (c *Concurrency) Acquire(key string) (release func(), ok bool) {
	if c == nil || c.max <= 0 {
		return func() {}, true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts[key] >= c.max {
		return nil, false
	}
	c.counts[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.counts[key]--
			if c.counts[key] <= 0 {
				delete(c.counts, key)
			}
		})
	}, true
}

// Open returns how many connections are open for key.
func (c *Concurrency) Open(key string) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[key]
}
