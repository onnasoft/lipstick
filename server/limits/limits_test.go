package limits

import (
	"sync"
	"testing"
	"time"
)

// fakeClock moves only when a test says so.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestRateAllowsTheBurstThenRefills(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	r := NewRate(2, 3, 100, clk.now) // 2 per second, bursts of 3
	for i := 0; i < 3; i++ {
		if ok, _ := r.Allow("a"); !ok {
			t.Fatalf("burst request %d refused", i)
		}
	}
	ok, wait := r.Allow("a")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("over the burst: ok=%v wait=%v", ok, wait)
	}
	clk.advance(500 * time.Millisecond) // one token back at 2 per second
	if ok, _ := r.Allow("a"); !ok {
		t.Fatal("token not refilled after 500 ms")
	}
	if ok, _ := r.Allow("a"); ok {
		t.Fatal("second token before its time")
	}
}

func TestRateKeysAreIndependent(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	r := NewRate(1, 1, 100, clk.now)
	if ok, _ := r.Allow("10.0.0.1"); !ok {
		t.Fatal("first address refused")
	}
	if ok, _ := r.Allow("10.0.0.2"); !ok {
		t.Fatal("another address was affected by the first")
	}
}

func TestRateDisabledWhenZero(t *testing.T) {
	r := NewRate(0, 0, 100, nil)
	for i := 0; i < 1000; i++ {
		if ok, _ := r.Allow("x"); !ok {
			t.Fatal("disabled limit refused a request")
		}
	}
}

// The table of keys is bounded: idle keys are dropped to make room, and a key that does
// not fit is refused rather than given no limit.
func TestRateTableIsBounded(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	r := NewRate(1, 1, 2, clk.now)
	r.Allow("a")
	r.Allow("b")
	if ok, _ := r.Allow("c"); !ok {
		// "a" and "b" are not full yet (their one token is spent), so nothing is pruned.
		// The new key does not fit and is refused.
	} else {
		t.Fatal("a new key was admitted into a full table")
	}
	clk.advance(10 * time.Second) // "a" and "b" refill to full and can be dropped
	if ok, _ := r.Allow("c"); !ok {
		t.Fatal("a new key was refused after idle keys could be dropped")
	}
	if len(r.buckets) > 2 {
		t.Fatalf("table grew past its bound: %d", len(r.buckets))
	}
}

func TestConcurrencyLimitsOpenConnectionsPerKey(t *testing.T) {
	c := NewConcurrency(2)
	r1, ok1 := c.Acquire("d")
	r2, ok2 := c.Acquire("d")
	if !ok1 || !ok2 {
		t.Fatal("two connections within the limit refused")
	}
	if _, ok := c.Acquire("d"); ok {
		t.Fatal("third connection admitted over the limit")
	}
	if _, ok := c.Acquire("other"); !ok {
		t.Fatal("another key was affected")
	}
	r1()
	r1() // releasing twice must not free two slots
	r3, ok := c.Acquire("d")
	if !ok {
		t.Fatal("slot not freed after release")
	}
	if _, ok := c.Acquire("d"); ok {
		t.Fatal("a double release freed a second slot")
	}
	r2()
	if c.Open("d") != 1 {
		t.Fatalf("open count %d, want 1 (the re-acquired slot)", c.Open("d"))
	}
	r3()
	if c.Open("d") != 0 {
		t.Fatalf("open count %d, want 0", c.Open("d"))
	}
}

func TestConcurrencyDisabledWhenZero(t *testing.T) {
	c := NewConcurrency(0)
	for i := 0; i < 100; i++ {
		if _, ok := c.Acquire("d"); !ok {
			t.Fatal("disabled limit refused a connection")
		}
	}
}

func TestVisitorsRefuseByReasonAndReleaseTogether(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	v := NewVisitors(Settings{VisitorIPRate: 1000, VisitorIPBurst: 1000, DomainRate: 1000, DomainBurst: 1000, ConnsPerIP: 1, ConnsPerDomain: 5}, clk.now)
	rel, refused := v.Admit("10.0.0.1", "app.example.com")
	if refused != nil {
		t.Fatalf("first connection refused: %+v", refused)
	}
	if _, refused := v.Admit("10.0.0.1", "app.example.com"); refused == nil || refused.Reason != ReasonIPConns {
		t.Fatalf("second connection from the same address: %+v", refused)
	}
	// The refused connection must not have taken a domain slot.
	if v.domainConns.Open("app.example.com") != 1 {
		t.Fatalf("domain slots held: %d, want 1", v.domainConns.Open("app.example.com"))
	}
	rel()
	if _, refused := v.Admit("10.0.0.1", "app.example.com"); refused != nil {
		t.Fatalf("after release: %+v", refused)
	}
}

func TestVisitorsDomainLimitHoldsAcrossAddresses(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	v := NewVisitors(Settings{DomainRate: 1, DomainBurst: 2}, clk.now)
	v.Admit("10.0.0.1", "app.example.com")
	v.Admit("10.0.0.2", "app.example.com")
	if _, refused := v.Admit("10.0.0.3", "app.example.com"); refused == nil || refused.Reason != ReasonDomainRate {
		t.Fatalf("domain rate not shared by addresses: %+v", refused)
	}
	if _, refused := v.Admit("10.0.0.3", "other.example.com"); refused != nil {
		t.Fatal("another domain affected")
	}
}
