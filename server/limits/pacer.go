package limits

import (
	"sync"
	"time"
)

// Pacer paces bytes to a rate. It is a token bucket that may go into debt: Reserve
// takes the bytes at once and returns how long the caller should wait before it writes
// them, so the average rate stays at bytesPerSecond with a burst of one second. A rate
// of 0 means no pacing.
type Pacer struct {
	mu     sync.Mutex
	rate   float64 // bytes per second
	tokens float64 // may be negative: debt
	last   time.Time
	now    Clock
}

// NewPacer returns a pacer at bytesPerSecond.
func NewPacer(bytesPerSecond int64, now Clock) *Pacer {
	if now == nil {
		now = time.Now
	}
	return &Pacer{rate: float64(bytesPerSecond), tokens: float64(bytesPerSecond), last: now(), now: now}
}

// Reserve takes n bytes and returns the wait before writing them.
func (p *Pacer) Reserve(n int) time.Duration {
	if p == nil || p.rate <= 0 || n <= 0 {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	p.tokens += now.Sub(p.last).Seconds() * p.rate
	if p.tokens > p.rate {
		p.tokens = p.rate // at most one second of burst
	}
	p.last = now
	p.tokens -= float64(n)
	if p.tokens >= 0 {
		return 0
	}
	return time.Duration(-p.tokens / p.rate * float64(time.Second))
}
