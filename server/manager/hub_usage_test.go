package manager

import (
	"testing"

	"github.com/OnnaSoft/lipstick/server/traffic"
)

// Bytes below the threshold must still leave memory on the flush, or a day of small
// requests never reaches the daily total.
func TestSmallTrafficIsFlushedBelowTheThreshold(t *testing.T) {
	hub := NewNetworkHub("app.example.com", traffic.NewTrafficManager(64*1024), 64*1024)
	hub.addDataUsage(261)
	hub.addDataUsage(261)
	if hub.dataUsageAccumulator != 522 {
		t.Fatalf("waiting bytes: %d", hub.dataUsageAccumulator)
	}
	hub.flushUsage()
	if hub.dataUsageAccumulator != 0 {
		t.Fatalf("bytes still waiting after the flush: %d", hub.dataUsageAccumulator)
	}
	if hub.totalDataTransferred != 522 {
		t.Fatalf("total since start: %d, want 522", hub.totalDataTransferred)
	}
	hub.flushUsage() // nothing waiting: a flush must not write anything
}
