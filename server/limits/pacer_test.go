package limits

import (
	"sync"
	"testing"
	"time"
)

type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *manualClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// At 1000 bytes per second, one second of data is free and more must wait.
func TestPacerAllowsOneSecondThenPaces(t *testing.T) {
	clk := &manualClock{t: time.Unix(1000, 0)}
	p := NewPacer(1000, clk.now)
	if d := p.Reserve(1000); d != 0 {
		t.Fatalf("the first second waited %v", d)
	}
	if d := p.Reserve(500); d < 480*time.Millisecond || d > 520*time.Millisecond {
		t.Fatalf("500 bytes over the rate waited %v, want about 500ms", d)
	}
}

// Over time the average stays at the rate: after a pause the bucket refills, to one
// second at most.
func TestPacerRefillsAndCapsTheBurst(t *testing.T) {
	clk := &manualClock{t: time.Unix(1000, 0)}
	p := NewPacer(1000, clk.now)
	p.Reserve(1000)
	clk.advance(10 * time.Second)
	if d := p.Reserve(1000); d != 0 {
		t.Fatalf("after a long pause, 1000 bytes waited %v", d)
	}
	if d := p.Reserve(1000); d < 990*time.Millisecond {
		t.Fatalf("burst exceeded one second: second 1000 bytes waited %v", d)
	}
}

func TestPacerDisabledAtZero(t *testing.T) {
	p := NewPacer(0, nil)
	if d := p.Reserve(1 << 30); d != 0 {
		t.Fatalf("disabled pacer waited %v", d)
	}
}
