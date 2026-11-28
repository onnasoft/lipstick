package manager

import (
	"runtime"
	"sync/atomic"

	"github.com/OnnaSoft/lipstick/server/metrics"
)

// Counters that have no per-manager home: a 504 is written by a relay, and a tunnel is
// opened by a handshake. They count since the process started.
// connectionLatency measures, per domain, the time from a visitor's request to the first
// byte of the reply, as the relay sees it. It is recorded on the request's first reply.
var connectionLatency = metrics.NewHistogramSet("domain", []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
})

// tunnelLatency is the round trip of each mux session, per domain, measured with yamux's
// ping. Buckets start at 0.5 ms, since a tunnel on a local network is fast.
var tunnelLatency = metrics.NewHistogramSet("domain", []float64{
	0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1,
})

var (
	backendTimeouts atomic.Int64 // visitor requests answered 504 because the backend was silent
	tunnelsOpened   atomic.Int64 // tunnel connections accepted from clients
)

// Metrics returns the server's families for /metrics. Each value is read when asked,
// so the numbers are current at scrape time.
func (manager *Manager) Metrics() []metrics.Family {
	var clients, sessions, pending, bytesSince []metrics.Sample
	var totalClients, totalSessions, totalPending float64
	for _, st := range manager.Snapshot() {
		labels := map[string]string{"domain": st.Domain}
		clients = append(clients, metrics.Sample{Labels: labels, Value: float64(st.Clients)})
		sessions = append(sessions, metrics.Sample{Labels: labels, Value: float64(st.ActiveSessions)})
		pending = append(pending, metrics.Sample{Labels: labels, Value: float64(st.PendingVisitors)})
		bytesSince = append(bytesSince, metrics.Sample{Labels: labels, Value: float64(st.BytesSinceStart)})
		totalClients += float64(st.Clients)
		totalSessions += float64(st.ActiveSessions)
		totalPending += float64(st.PendingVisitors)
	}

	var refusals []metrics.Sample
	for reason, n := range manager.refusals.Totals() {
		refusals = append(refusals, metrics.Sample{Labels: map[string]string{"reason": reason}, Value: float64(n)})
	}

	counts := manager.reqlog.StatusCounts()
	var responses []metrics.Sample
	for class := 1; class <= 5; class++ {
		responses = append(responses, metrics.Sample{
			Labels: map[string]string{"class": string(rune('0'+class)) + "xx"},
			Value:  float64(counts[class]),
		})
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return []metrics.Family{
		{Name: "lipstick_connected_clients", Help: "Tunnel clients connected, per domain.", Type: metrics.Gauge, Samples: clients},
		{Name: "lipstick_active_sessions", Help: "Open tunnel sessions, per domain.", Type: metrics.Gauge, Samples: sessions},
		{Name: "lipstick_pending_visitors", Help: "Visitors waiting for a tunnel, per domain.", Type: metrics.Gauge, Samples: pending},
		{Name: "lipstick_traffic_bytes_since_start", Help: "Bytes relayed for visitors since this server started, per domain.", Type: metrics.Counter, Samples: bytesSince},
		{Name: "lipstick_clients_connected", Help: "Tunnel clients connected, all domains.", Type: metrics.Gauge, Samples: []metrics.Sample{{Value: totalClients}}},
		{Name: "lipstick_sessions_active", Help: "Open tunnel sessions, all domains.", Type: metrics.Gauge, Samples: []metrics.Sample{{Value: totalSessions}}},
		{Name: "lipstick_visitors_pending", Help: "Visitors waiting for a tunnel, all domains.", Type: metrics.Gauge, Samples: []metrics.Sample{{Value: totalPending}}},
		{Name: "lipstick_visitor_connections_open", Help: "Visitor connections open now, all domains.", Type: metrics.Gauge, Samples: []metrics.Sample{{Value: float64(manager.visitors.OpenConnections())}}},
		{Name: "lipstick_visitor_refusals_total", Help: "Visitor connections refused, by reason.", Type: metrics.Counter, Samples: refusals},
		{Name: "lipstick_http_responses_total", Help: "HTTP responses to visitors, by status class.", Type: metrics.Counter, Samples: responses},
		{Name: "lipstick_backend_timeouts_total", Help: "Visitor requests answered 504 because the backend stayed silent.", Type: metrics.Counter, Samples: []metrics.Sample{{Value: float64(backendTimeouts.Load())}}},
		{Name: "lipstick_tunnels_opened_total", Help: "Tunnel connections accepted from clients.", Type: metrics.Counter, Samples: []metrics.Sample{{Value: float64(tunnelsOpened.Load())}}},
		{Name: "lipstick_connection_latency_seconds", Help: "Time from a visitor's request to the first byte of the reply, per domain.", Type: metrics.Histogram, Samples: connectionLatency.Samples()},
		{Name: "lipstick_tunnel_latency_seconds", Help: "Round trip of each tunnel session, per domain, measured with a ping every few seconds.", Type: metrics.Histogram, Samples: tunnelLatency.Samples()},
		{Name: "lipstick_go_goroutines", Help: "Goroutines in the server process.", Type: metrics.Gauge, Samples: []metrics.Sample{{Value: float64(runtime.NumGoroutine())}}},
		{Name: "lipstick_go_memory_alloc_bytes", Help: "Heap memory in use by the server process.", Type: metrics.Gauge, Samples: []metrics.Sample{{Value: float64(mem.HeapAlloc)}}},
	}
}
