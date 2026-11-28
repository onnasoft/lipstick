package metrics

import (
	"sort"
	"strconv"
	"sync"
)

// HistogramSet holds one histogram per label value, such as per domain. Observations are
// counted into fixed buckets; the buckets are cumulative when written, as the format asks.
type HistogramSet struct {
	label  string
	bounds []float64 // upper bounds in seconds, ascending

	mu     sync.Mutex
	series map[string]*series
}

type series struct {
	counts []uint64 // one per bound; an observation above all bounds only shows in count
	sum    float64
	count  uint64
}

// NewHistogramSet makes a set whose series are keyed by the given label name.
func NewHistogramSet(label string, bounds []float64) *HistogramSet {
	b := append([]float64(nil), bounds...)
	sort.Float64s(b)
	return &HistogramSet{label: label, bounds: b, series: map[string]*series{}}
}

// Observe records one value for the series with this label value.
func (h *HistogramSet) Observe(value string, seconds float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.series[value]
	if !ok {
		s = &series{counts: make([]uint64, len(h.bounds))}
		h.series[value] = s
	}
	s.sum += seconds
	s.count++
	for i, bound := range h.bounds {
		if seconds <= bound {
			s.counts[i]++
		}
	}
}

// Samples returns the bucket, sum and count samples of every series, ready for a Family
// of type Histogram.
func (h *HistogramSet) Samples() []Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := make([]string, 0, len(h.series))
	for name := range h.series {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []Sample
	for _, name := range names {
		s := h.series[name]
		base := map[string]string{h.label: name}
		var cumulative uint64
		for i, bound := range h.bounds {
			cumulative = s.counts[i]
			out = append(out, Sample{Suffix: "_bucket", Labels: withLE(base, strconv.FormatFloat(bound, 'g', -1, 64)), Value: float64(cumulative)})
		}
		out = append(out,
			Sample{Suffix: "_bucket", Labels: withLE(base, "+Inf"), Value: float64(s.count)},
			Sample{Suffix: "_sum", Labels: base, Value: s.sum},
			Sample{Suffix: "_count", Labels: base, Value: float64(s.count)},
		)
	}
	return out
}

func withLE(base map[string]string, le string) map[string]string {
	m := make(map[string]string, len(base)+1)
	for k, v := range base {
		m[k] = v
	}
	m["le"] = le
	return m
}
