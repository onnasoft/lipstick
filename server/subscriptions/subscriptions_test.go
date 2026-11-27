package subscriptions

import "testing"

// A failed NATS connection must be an error on every call. Before the fix the
// shared manager could come back as (nil, nil) and callers panicked.
func TestSharedManagerReportsErrorAndRetries(t *testing.T) {
	const unreachable = "nats://127.0.0.1:1"
	for i := 0; i < 2; i++ {
		m, err := sharedManager(unreachable)
		if err == nil {
			t.Fatalf("attempt %d: expected an error, got manager %v", i, m)
		}
		if m != nil {
			t.Fatalf("attempt %d: got a manager together with an error", i)
		}
	}
}
