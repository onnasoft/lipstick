package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderFollowsTheExpositionFormat(t *testing.T) {
	var b bytes.Buffer
	err := Render(&b, []Family{
		{Name: "lipstick_b", Help: "second", Type: Gauge, Samples: []Sample{{Labels: map[string]string{"domain": `a"b\c`}, Value: 2}}},
		{Name: "lipstick_a_total", Help: "first\nline", Type: Counter, Samples: []Sample{{Value: 7}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	want := "# HELP lipstick_a_total first\\nline\n# TYPE lipstick_a_total counter\nlipstick_a_total 7\n" +
		"# HELP lipstick_b second\n# TYPE lipstick_b gauge\nlipstick_b{domain=\"a\\\"b\\\\c\"} 2\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
	if strings.Index(out, "lipstick_a_total") > strings.Index(out, "lipstick_b") {
		t.Fatal("families not in name order")
	}
}
