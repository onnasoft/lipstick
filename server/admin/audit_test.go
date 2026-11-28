package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/OnnaSoft/lipstick/server/audit"
)

func findEvent(t *testing.T, mem *memAudit, action string) audit.Event {
	t.Helper()
	events, _ := mem.Search(audit.Query{Action: action})
	if len(events) == 0 {
		t.Fatalf("no %s event recorded", action)
	}
	return events[0]
}

// A change is recorded with who and what, and its outcome. The record names the
// fields that were set, never their values.
func TestDomainChangeIsRecordedWithFieldNamesOnly(t *testing.T) {
	a := newTestAdmin(testKey)
	mem := a.audit.(*memAudit)
	const secret = "secret-key-value-that-must-not-appear"
	rec := send(a, http.MethodPost, "/domains", testKey, `{"name":"app.example.com","apiKey":"`+secret+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	e := findEvent(t, mem, "domain.create")
	if e.Outcome != audit.OutcomeSucceeded || e.Target != "app.example.com" || e.Actor != "tester" || e.StatusCode != 200 {
		t.Fatalf("event: %+v", e)
	}
	fields, _ := e.Details["fields"].([]string)
	if len(fields) == 0 {
		t.Fatalf("no field names recorded: %+v", e.Details)
	}
	raw, _ := json.Marshal(mem.events)
	if strings.Contains(string(raw), secret) {
		t.Fatal("a key value reached the audit log")
	}
}

func TestFailedChangeIsRecorded(t *testing.T) {
	a := newTestAdmin(testKey)
	mem := a.audit.(*memAudit)
	if rec := send(a, http.MethodPatch, "/domains/missing.example.com", testKey, `{"apiKey":"0123456789abcdef"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("patch unknown domain: %d", rec.Code)
	}
	e := findEvent(t, mem, "domain.update")
	if e.Outcome != audit.OutcomeFailed || e.StatusCode != http.StatusNotFound || e.Target != "missing.example.com" {
		t.Fatalf("event: %+v", e)
	}
}

// Nothing may change without a record: when the entry cannot be written, the change
// is refused.
func TestChangeIsRefusedWhenItCannotBeRecorded(t *testing.T) {
	a := newTestAdmin(testKey)
	mem := a.audit.(*memAudit)
	mem.failBegin = true
	rec := send(a, http.MethodPost, "/domains", testKey, `{"name":"app.example.com","apiKey":"0123456789abcdef"}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "not made") {
		t.Fatalf("change without a record: %d %s", rec.Code, rec.Body.String())
	}
	if len(mem.events) != 0 {
		t.Fatalf("events written while the log was down: %+v", mem.events)
	}
}

// Every admin route that changes data is wrapped. Without an audit log, none of them
// may run.
func TestNoAuditLogMeansNoChanges(t *testing.T) {
	a := newTestAdmin(testKey)
	a.audit = nil
	if rec := send(a, http.MethodPost, "/domains", testKey, `{"name":"app.example.com","apiKey":"0123456789abcdef"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("change with no audit log: %d", rec.Code)
	}
}

// Logins are recorded, the wrong ones too, and never with the password.
func TestLoginAttemptsAreRecorded(t *testing.T) {
	a := newTestAdmin(testKey)
	mem := a.audit.(*memAudit)
	const wrong = "a-wrong-password-value"
	postLogin(a, `{"username":"tester","password":"`+wrong+`"}`)
	postLogin(a, `{"username":"tester","password":"tester-password-long"}`)
	events, _ := mem.Search(audit.Query{Action: "session.login"})
	if len(events) != 2 {
		t.Fatalf("logins recorded: %d, want 2", len(events))
	}
	if events[0].Outcome != audit.OutcomeSucceeded || events[1].Outcome != audit.OutcomeFailed {
		t.Fatalf("outcomes: %+v", events)
	}
	if events[0].Actor != "tester" || events[1].Actor != "tester" || events[0].Details["via"] != "api" {
		t.Fatalf("who logged in: %+v", events)
	}
	raw, _ := json.Marshal(mem.events)
	if strings.Contains(string(raw), wrong) || strings.Contains(string(raw), "tester-password-long") {
		t.Fatal("a password reached the audit log")
	}
}

// The log is for superusers. An operator is refused, and the refusal is recorded too.
func TestAccountChangesAreAuditedAndTheLogIsSuperuserOnly(t *testing.T) {
	a, sup, op := realAdmin(t)
	mem := a.audit.(*memAudit)
	if rec := send(a, http.MethodPost, "/admin-users", sup, `{"username":"nuevo","password":"`+accountPassword+`","role":"operator"}`); rec.Code != http.StatusOK {
		t.Fatalf("create: %d", rec.Code)
	}
	if rec := send(a, http.MethodPost, "/admin-users", op, `{"username":"x","password":"`+accountPassword+`","role":"operator"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("operator create: %d", rec.Code)
	}
	succeeded, _ := mem.Search(audit.Query{Action: "admin_account.create", Outcome: audit.OutcomeSucceeded})
	if len(succeeded) != 1 {
		t.Fatalf("successful creates recorded: %d", len(succeeded))
	}
	created := succeeded[0]
	if created.Actor != "jtorres" || created.Target != "nuevo" || created.Outcome != audit.OutcomeSucceeded || created.Details["role"] != "operator" {
		t.Fatalf("create event: %+v", created)
	}
	refused, _ := mem.Search(audit.Query{Actor: "operador", Outcome: audit.OutcomeFailed})
	if len(refused) == 0 || refused[0].StatusCode != http.StatusForbidden {
		t.Fatalf("operator refusal not recorded: %+v", refused)
	}

	if rec := send(a, http.MethodGet, "/audit", op, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("operator reads the log: %d", rec.Code)
	}
	rec := send(a, http.MethodGet, "/audit?action=admin_account.create", sup, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"target":"nuevo"`) || strings.Contains(rec.Body.String(), "session.login") {
		t.Fatalf("superuser query: %d %s", rec.Code, rec.Body.String())
	}
	if rec := send(a, http.MethodGet, "/audit?limit=0", sup, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("limit 0: %d, want 400", rec.Code)
	}
}
