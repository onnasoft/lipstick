package logger

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func newTestLogger(format Format) (*Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return &Logger{debug: true, format: format, out: &buf}, &buf
}

// In json format each event is one object with its level, message and fields.
func TestJSONLineCarriesLevelMessageAndFields(t *testing.T) {
	l, buf := newTestLogger(FormatJSON)
	l.With("domain", "example.test", "reason", "unauthorized").Error("tunnel setup refused")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not one JSON object: %v in %q", err, buf.String())
	}
	if rec["level"] != "error" || rec["msg"] != "tunnel setup refused" {
		t.Fatalf("core keys wrong: %v", rec)
	}
	fields, _ := rec["fields"].(map[string]any)
	if fields["domain"] != "example.test" || fields["reason"] != "unauthorized" {
		t.Fatalf("fields wrong: %v", fields)
	}
	if _, ok := rec["time"].(string); !ok {
		t.Fatal("no time")
	}
}

// A field cannot overwrite the core keys: it stays under "fields".
func TestFieldNamesCannotOverwriteCoreKeys(t *testing.T) {
	l, buf := newTestLogger(FormatJSON)
	l.With("msg", "spoofed", "level", "info").Warning("real")
	var rec map[string]any
	json.Unmarshal(buf.Bytes(), &rec)
	if rec["msg"] != "real" || rec["level"] != "warning" {
		t.Fatalf("core keys overwritten: %v", rec)
	}
}

// In text format the same fields appear after the message, sorted, with no JSON.
func TestTextFormatShowsFieldsAfterMessage(t *testing.T) {
	l, buf := newTestLogger(FormatText)
	l.With("domain", "example.test", "client", "10.0.0.1:5000").Info("tunnel connected")
	out := buf.String()
	if !strings.Contains(out, "tunnel connected client=10.0.0.1:5000 domain=example.test") {
		t.Fatalf("text line: %q", out)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatal("text format wrote JSON")
	}
}

func TestSetFormatRejectsUnknownNames(t *testing.T) {
	l, _ := newTestLogger(FormatText)
	if err := l.SetFormat("yaml"); err == nil {
		t.Fatal("yaml accepted")
	}
	if err := l.SetFormat("json"); err != nil || l.format != FormatJSON {
		t.Fatalf("json not set: %v", err)
	}
}

func TestMissingValueIsMarked(t *testing.T) {
	l, buf := newTestLogger(FormatText)
	l.With("domain").Info("x")
	if !strings.Contains(buf.String(), "domain=(missing)") {
		t.Fatalf("missing value not marked: %q", buf.String())
	}
}
