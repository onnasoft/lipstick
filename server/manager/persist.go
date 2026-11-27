package manager

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/OnnaSoft/lipstick/logger"
)

// Persistence tuning. Requests are written in batches, so a busy server does
// not issue one insert per request.
const (
	persistBatchSize   = 500
	persistFlushEvery  = 2 * time.Second
	persistQueueSize   = 10000
	persistMaxRetained = 50000 // rows kept in memory while the database is down
	persistPruneEvery  = time.Hour
	RequestRetention   = 7 * 24 * time.Hour // how long stored requests are kept
)

// RequestStore keeps finished requests durably.
type RequestStore interface {
	SaveBatch(records []RequestRecord) error
	Query(q RequestQuery) ([]RequestRecord, error)
	DeleteOlderThan(t time.Time) (int64, error)
}

// RequestQuery selects requests. A zero From or To leaves that side open.
// Limit must be positive.
type RequestQuery struct {
	Domain string
	Port   int // 0 matches every port
	From   time.Time
	To     time.Time
	Limit  int
}

// persister writes requests to the store in the background. It never blocks the
// request path: when its queue is full, the request is counted as dropped.
type persister struct {
	store     RequestStore
	in        chan RequestRecord
	dropped   atomic.Int64
	retention time.Duration
	stop      chan struct{}
	done      chan struct{}
	stopOnce  sync.Once
}

func newPersister(store RequestStore, retention time.Duration) *persister {
	return &persister{
		store:     store,
		in:        make(chan RequestRecord, persistQueueSize),
		retention: retention,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// enqueue is the hook the request log calls. It never waits.
func (p *persister) enqueue(r RequestRecord) {
	select {
	case p.in <- r:
	default:
		p.dropped.Add(1)
	}
}

// Dropped reports how many requests were not stored because the queue was full.
func (p *persister) Dropped() int64 { return p.dropped.Load() }

func (p *persister) run() {
	defer close(p.done)
	flush := time.NewTicker(persistFlushEvery)
	defer flush.Stop()
	prune := time.NewTicker(persistPruneEvery)
	defer prune.Stop()

	var batch []RequestRecord
	var retryAt time.Time // after a failed write, wait before trying again

	save := func() {
		if len(batch) == 0 || time.Now().Before(retryAt) {
			return
		}
		if err := p.store.SaveBatch(batch); err != nil {
			logger.Default.Error("Could not store request log, will retry:", err)
			retryAt = time.Now().Add(persistFlushEvery)
			if len(batch) > persistMaxRetained {
				batch = batch[len(batch)-persistMaxRetained:]
			}
			return
		}
		retryAt = time.Time{}
		batch = batch[:0]
	}
	pruneOld := func() {
		if p.retention <= 0 {
			return
		}
		if n, err := p.store.DeleteOlderThan(time.Now().Add(-p.retention)); err != nil {
			logger.Default.Error("Could not prune request log:", err)
		} else if n > 0 {
			logger.Default.Info("Pruned request log rows:", n)
		}
	}

	pruneOld()
	for {
		select {
		case r := <-p.in:
			batch = append(batch, r)
			if len(batch) >= persistBatchSize {
				save()
			}
		case <-flush.C:
			save()
		case <-prune.C:
			pruneOld()
		case <-p.stop:
			batch = p.drain(batch)
			retryAt = time.Time{} // last attempt, ignore the backoff
			save()
			return
		}
	}
}

// drain takes everything still queued, without waiting for more.
func (p *persister) drain(batch []RequestRecord) []RequestRecord {
	for {
		select {
		case r := <-p.in:
			batch = append(batch, r)
		default:
			return batch
		}
	}
}

// Close stops the persister after writing what is queued. It returns once the
// last write has finished or failed.
func (p *persister) Close() {
	p.stopOnce.Do(func() { close(p.stop) })
	<-p.done
}

// StartRequestPersistence stores finished requests in store and deletes those
// older than RequestRetention. Close the manager to flush them on shutdown.
func (m *Manager) StartRequestPersistence(store RequestStore) {
	p := newPersister(store, RequestRetention)
	m.persist = p
	m.reqlog.setHook(p.enqueue)
	go p.run()
}
