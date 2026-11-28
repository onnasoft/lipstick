// Package metrics writes the server's numbers in the Prometheus text exposition format.
// It has no dependency and no global state: callers build families from what they know.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Type is a Prometheus metric type.
type Type string

const (
	Counter Type = "counter"
	Gauge   Type = "gauge"
)

// Sample is one value, with its labels.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Family is one metric name with its help text and samples.
type Family struct {
	Name    string
	Help    string
	Type    Type
	Samples []Sample
}

// Render writes the families in name order. Label values are escaped as the format requires.
func Render(w io.Writer, families []Family) error {
	sort.Slice(families, func(i, j int) bool { return families[i].Name < families[j].Name })
	for _, f := range families {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", f.Name, escapeHelp(f.Help), f.Name, f.Type); err != nil {
			return err
		}
		for _, s := range f.Samples {
			if _, err := fmt.Fprintf(w, "%s%s %s\n", f.Name, labels(s.Labels), formatValue(s.Value)); err != nil {
				return err
			}
		}
	}
	return nil
}

func labels(l map[string]string) string {
	if len(l) == 0 {
		return ""
	}
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", k, escapeLabel(l[k])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapeLabel(v string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(v)
}

func escapeHelp(v string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(v)
}

func formatValue(v float64) string {
	return fmt.Sprintf("%g", v)
}
