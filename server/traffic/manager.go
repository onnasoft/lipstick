package traffic

import (
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/logger"
)

type TrafficManager struct {
	mu          sync.Mutex
	saveMu      sync.Mutex // one save at a time, so no byte is written twice
	trafficData map[string]int64
	threshold   int64
	dbTicker    *time.Ticker
	stopChan    chan struct{}
	stopOnce    sync.Once
}

func NewTrafficManager(threshold int64) *TrafficManager {
	manager := &TrafficManager{
		trafficData: make(map[string]int64),
		threshold:   threshold,
		dbTicker:    time.NewTicker(5 * time.Second),
		stopChan:    make(chan struct{}),
	}
	go manager.run()
	return manager
}

func (tm *TrafficManager) AddTraffic(domain string, bytes int64) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.trafficData[domain] += bytes
}

func (tm *TrafficManager) run() {
	for {
		select {
		case <-tm.dbTicker.C:
			tm.saveAllToDatabase()
		case <-tm.stopChan:
			tm.dbTicker.Stop()
			return
		}
	}
}

// saveAllToDatabase writes the pending counters. A counter is reduced only by
// the amount that was written successfully. If the database is down, the bytes
// stay pending and the next save tries again, so nothing is lost.
func (tm *TrafficManager) saveAllToDatabase() {
	tm.saveMu.Lock()
	defer tm.saveMu.Unlock()

	tm.mu.Lock()
	pending := make(map[string]int64, len(tm.trafficData))
	for domain, bytes := range tm.trafficData {
		if bytes > 0 {
			pending[domain] = bytes
		}
	}
	tm.mu.Unlock()

	for domain, bytes := range pending {
		if err := tm.updateDatabase(domain, bytes); err != nil {
			logger.Default.Error("Traffic for domain kept for the next save:", domain, "bytes:", bytes, "error:", err)
			continue
		}

		tm.mu.Lock()
		tm.trafficData[domain] -= bytes
		if tm.trafficData[domain] <= 0 {
			delete(tm.trafficData, domain)
		}
		tm.mu.Unlock()
	}
}

// Close stops the periodic save and writes what is pending. It can be called
// more than once.
func (tm *TrafficManager) Close() {
	tm.stopOnce.Do(func() { close(tm.stopChan) })
	tm.saveAllToDatabase()
}
