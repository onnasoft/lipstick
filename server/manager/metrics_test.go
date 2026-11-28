package manager

import (
	"bytes"
	"strings"
	"testing"

	"github.com/OnnaSoft/lipstick/server/metrics"
)

func TestStatusClassOf(t *testing.T) {
	cases := map[int]int{0: 0, 99: 0, 100: 1, 200: 2, 404: 4, 504: 5, 599: 5, 600: 0, 999: 0}
	for status, want := range cases {
		if got := statusClassOf(status); got != want {
			t.Errorf("statusClassOf(%d) = %d, want %d", status, got, want)
		}
	}
}

func TestMetricsCountResponsesAndRefusals(t *testing.T) {
	m := &Manager{reqlog: NewRequestLog(10)}
	m.reqlog.Add(RequestRecord{Method: "GET", Path: "/a", Status: 200})
	m.reqlog.Add(RequestRecord{Method: "GET", Path: "/b", Status: 504})
	m.refusals.note("ip_rate", "d", nil)
	m.refusals.note("ip_rate", "d", nil)

	var b bytes.Buffer
	if err := metrics.Render(&b, m.Metrics()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		`lipstick_http_responses_total{class="2xx"} 1`,
		`lipstick_http_responses_total{class="5xx"} 1`,
		`lipstick_visitor_refusals_total{reason="ip_rate"} 2`,
		"# TYPE lipstick_connected_clients gauge",
		"lipstick_go_goroutines ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestConnectionLatencyIsAHistogram(t *testing.T) {
	connectionLatency.Observe("latency.test", 0.03)
	connectionLatency.Observe("latency.test", 0.3)
	var b bytes.Buffer
	if err := metrics.Render(&b, (&Manager{reqlog: NewRequestLog(10)}).Metrics()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"# TYPE lipstick_connection_latency_seconds histogram",
		`lipstick_connection_latency_seconds_bucket{domain="latency.test",le="0.05"} 1`,
		`lipstick_connection_latency_seconds_bucket{domain="latency.test",le="+Inf"} 2`,
		`lipstick_connection_latency_seconds_count{domain="latency.test"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestCPUTimeGrowsWithWork(t *testing.T) {
	before := processCPUSeconds()
	x := 0.0
	for i := 0; i < 30_000_000; i++ {
		x += float64(i) * 1.0000001
	}
	_ = x
	if after := processCPUSeconds(); after <= before {
		t.Fatalf("CPU time did not grow: %v then %v", before, after)
	}
}
