// Package logger writes the server's log lines. Two formats are supported: text, for a
// terminal, with colored levels; and json, one object per line, for a log collector.
// Events can carry fields: logger.Default.With("domain", name).Error("...").
package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Format names a log format.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

type Logger struct {
	mu     sync.Mutex
	debug  bool
	format Format
	out    io.Writer
}

var Default *Logger

const (
	reset  = "\033[0m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[34m"
)

func init() {
	Default = &Logger{
		debug:  os.Getenv("DEBUG") == "true",
		format: FormatText,
		out:    os.Stdout,
	}
}

// SetFormat chooses "text" or "json". An empty name means text.
func (l *Logger) SetFormat(name string) error {
	switch Format(strings.ToLower(name)) {
	case "", FormatText:
		l.mu.Lock()
		l.format = FormatText
		l.mu.Unlock()
	case FormatJSON:
		l.mu.Lock()
		l.format = FormatJSON
		l.mu.Unlock()
	default:
		return fmt.Errorf("logging.format must be text or json, not %q", name)
	}
	return nil
}

// SetOutput appends the log to a file instead of standard output.
func (l *Logger) SetOutput(outputFile string) error {
	file, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		return fmt.Errorf("failed to set logger output: %v", err)
	}
	l.mu.Lock()
	l.out = file
	l.mu.Unlock()
	return nil
}

// Entry is a log event with fields attached. Fields are written in both formats.
type Entry struct {
	l      *Logger
	fields map[string]any
}

// With starts an entry with fields given as key, value pairs. A key without a value gets
// "(missing)" so the log still shows it.
func (l *Logger) With(kv ...any) *Entry {
	e := &Entry{l: l, fields: map[string]any{}}
	for i := 0; i < len(kv); i += 2 {
		key := fmt.Sprint(kv[i])
		if i+1 >= len(kv) {
			e.fields[key] = "(missing)"
			break
		}
		e.fields[key] = kv[i+1]
	}
	return e
}

// With adds fields to an entry and returns it, so calls can be chained.
func (e *Entry) With(kv ...any) *Entry {
	for k, v := range e.l.With(kv...).fields {
		e.fields[k] = v
	}
	return e
}

func (e *Entry) Info(msg string)    { e.l.write("info", green, msg, e.fields) }
func (e *Entry) Warning(msg string) { e.l.write("warning", yellow, msg, e.fields) }
func (e *Entry) Error(msg string)   { e.l.write("error", red, msg, e.fields) }
func (e *Entry) Debug(msg string) {
	if e.l.debug {
		e.l.write("debug", blue, msg, e.fields)
	}
}

func (l *Logger) Info(v ...interface{})    { l.write("info", green, fmt.Sprint(v...), nil) }
func (l *Logger) Warning(v ...interface{}) { l.write("warning", yellow, fmt.Sprint(v...), nil) }
func (l *Logger) Error(v ...interface{})   { l.write("error", red, fmt.Sprint(v...), nil) }
func (l *Logger) Debug(v ...interface{}) {
	if l.debug {
		l.write("debug", blue, fmt.Sprint(v...), nil)
	}
}

// write emits one line. The json format writes {"time","level","msg"} and the fields
// under "fields", so a field name can never overwrite the core keys.
func (l *Logger) write(level, color, msg string, fields map[string]any) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	var line string
	if l.format == FormatJSON {
		rec := map[string]any{"time": now.Format(time.RFC3339Nano), "level": level, "msg": msg}
		if len(fields) > 0 {
			rec["fields"] = fields
		}
		b, err := json.Marshal(rec)
		if err != nil {
			// A field that cannot be encoded must not drop the event.
			b, _ = json.Marshal(map[string]any{"time": now.Format(time.RFC3339Nano), "level": level, "msg": msg, "fields_error": err.Error()})
		}
		line = string(b) + "\n"
	} else {
		var sb strings.Builder
		sb.WriteString(now.Format("2006/01/02 15:04:05") + " ")
		sb.WriteString(color + "[" + strings.ToUpper(level) + "] " + reset)
		sb.WriteString(msg)
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, " %s=%v", k, fields[k])
		}
		sb.WriteString("\n")
		line = sb.String()
	}
	io.WriteString(l.out, line)
}
