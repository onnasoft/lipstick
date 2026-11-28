package admin

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/OnnaSoft/lipstick/server/auth"
)

type fakeConsole struct {
	users    map[string]string // username -> role
	disabled map[string]bool
	reset    map[string]string
}

func newFakeConsole() *fakeConsole {
	return &fakeConsole{users: map[string]string{}, disabled: map[string]bool{}, reset: map[string]string{}}
}

func (f *fakeConsole) CreateAdminUser(username, password, role string) error {
	if _, ok := f.users[username]; ok {
		return auth.ErrAdminUserExists
	}
	f.users[username] = role
	return nil
}
func (f *fakeConsole) ResetAdminPassword(username, password string) error {
	if _, ok := f.users[username]; !ok {
		return auth.ErrAdminUserNotFound
	}
	f.reset[username] = password
	return nil
}
func (f *fakeConsole) SetAdminDisabled(username string, disabled bool) error {
	if _, ok := f.users[username]; !ok {
		return auth.ErrAdminUserNotFound
	}
	f.disabled[username] = disabled
	return nil
}
func (f *fakeConsole) ListAdminUsers() ([]auth.AdminInfo, error) {
	var out []auth.AdminInfo
	for u, r := range f.users {
		out = append(out, auth.AdminInfo{Username: u, Role: r, Disabled: f.disabled[u]})
	}
	return out, nil
}

// typed returns a reader that answers the given passwords in order.
func typed(passwords ...string) func(string) (string, error) {
	return func(string) (string, error) {
		p := passwords[0]
		passwords = passwords[1:]
		return p, nil
	}
}

func runConsole(store consoleStore, readPassword func(string) (string, error), args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := RunConsole(store, args, readPassword, &out, &errOut)
	return code, out.String(), errOut.String()
}

const console12 = "correct-horse-battery"

func TestConsoleCreatesAnAdminWithTwoMatchingPasswords(t *testing.T) {
	f := newFakeConsole()
	code, out, _ := runConsole(f, typed(console12, console12), "create", "-username", "jtorres")
	if code != 0 || f.users["jtorres"] != auth.RoleSuperuser {
		t.Fatalf("create: code %d users %v", code, f.users)
	}
	if !strings.Contains(out, "created admin account") {
		t.Fatalf("output %q", out)
	}
}

func TestConsoleRefusesMismatchedPasswords(t *testing.T) {
	f := newFakeConsole()
	code, _, errOut := runConsole(f, typed(console12, "different-password-x"), "create", "-username", "jtorres")
	if code == 0 || len(f.users) != 0 || !strings.Contains(errOut, "do not match") {
		t.Fatalf("mismatch: code %d err %q users %v", code, errOut, f.users)
	}
}

func TestConsoleRefusesShortPassword(t *testing.T) {
	f := newFakeConsole()
	code, _, errOut := runConsole(f, typed("short", "short"), "create", "-username", "jtorres")
	if code == 0 || !strings.Contains(errOut, "at least 12") {
		t.Fatalf("short: code %d err %q", code, errOut)
	}
}

func TestConsoleNeedsUsernameAndKnownCommand(t *testing.T) {
	f := newFakeConsole()
	if code, _, _ := runConsole(f, typed(), "create"); code != 2 {
		t.Fatalf("missing username: %d", code)
	}
	if code, _, errOut := runConsole(f, typed(), "launch"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("unknown command: %d %q", code, errOut)
	}
	if code, _, _ := runConsole(f, typed()); code != 2 {
		t.Fatalf("no command: %d", code)
	}
}

func TestConsoleResetsPasswordAndTogglesAccess(t *testing.T) {
	f := newFakeConsole()
	f.users["jtorres"] = auth.RoleSuperuser
	if code, _, _ := runConsole(f, typed(console12, console12), "reset-password", "-username", "jtorres"); code != 0 || f.reset["jtorres"] != console12 {
		t.Fatalf("reset: %d %v", code, f.reset)
	}
	if code, out, _ := runConsole(f, typed(), "disable", "-username", "jtorres"); code != 0 || !f.disabled["jtorres"] || !strings.Contains(out, "disabled") {
		t.Fatalf("disable: %d %q", code, out)
	}
	if code, _, _ := runConsole(f, typed(), "enable", "-username", "jtorres"); code != 0 || f.disabled["jtorres"] {
		t.Fatalf("enable: %d", code)
	}
	if code, _, errOut := runConsole(f, typed(), "disable", "-username", "ghost"); code != 1 || !strings.Contains(errOut, "not found") {
		t.Fatalf("unknown account: %d %q", code, errOut)
	}
}

func TestConsoleListShowsAccountsWithoutSecrets(t *testing.T) {
	f := newFakeConsole()
	f.users["jtorres"] = auth.RoleOperator
	_, out, _ := runConsole(f, typed(), "list")
	if !strings.Contains(out, "jtorres") || !strings.Contains(out, "operator") || strings.Contains(out, console12) {
		t.Fatalf("list %q", out)
	}
}

func TestConsoleStopsWhenPasswordCannotBeRead(t *testing.T) {
	f := newFakeConsole()
	failing := func(string) (string, error) { return "", errors.New("no terminal") }
	if code, _, errOut := runConsole(f, failing, "create", "-username", "jtorres"); code == 0 || !strings.Contains(errOut, "no terminal") {
		t.Fatalf("no terminal: %d %q", code, errOut)
	}
	if len(f.users) != 0 {
		t.Fatal("account created without a password")
	}
}
