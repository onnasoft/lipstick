package auth

import (
	"path/filepath"
	"testing"

	"github.com/OnnaSoft/lipstick/server/db"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// newTestAuth opens a SQLite database with the production schema, and one domain.
func newTestAuth(t *testing.T) AuthManager {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&db.Domain{}, &db.VisitorUser{}, &db.VisitorToken{}); err != nil {
		t.Fatal(err)
	}
	a := NewAuthManagerWithDB(conn)
	if err := a.AddDomain(&Domain{Name: "robot.example.com", ApiKey: "key-0123456789abcdef", AllowMultipleConnections: true}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestVisitorUserLifecycle(t *testing.T) {
	a := newTestAuth(t)
	if err := a.AddVisitorUser("robot.example.com", "alice", "short"); err != ErrWeakPassword {
		t.Fatalf("short password: %v", err)
	}
	if err := a.AddVisitorUser("robot.example.com", "bad name", "correct-horse-battery"); err != ErrInvalidUsername {
		t.Fatalf("username with a space: %v", err)
	}
	if err := a.AddVisitorUser("robot.example.com", "alice", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddVisitorUser("robot.example.com", "alice", "another-password-here"); err != ErrUserExists {
		t.Fatalf("duplicate user: %v", err)
	}
	ok, err := a.VerifyVisitorUser("robot.example.com", "alice", "correct-horse-battery")
	if err != nil || !ok {
		t.Fatalf("right password: %v %v", ok, err)
	}
	if ok, _ := a.VerifyVisitorUser("robot.example.com", "alice", "wrong-password-here"); ok {
		t.Fatal("wrong password accepted")
	}
	if ok, _ := a.VerifyVisitorUser("robot.example.com", "nobody", "correct-horse-battery"); ok {
		t.Fatal("unknown user accepted")
	}
	names, _ := a.ListVisitorUsers("robot.example.com")
	if len(names) != 1 || names[0] != "alice" {
		t.Fatalf("users %v; passwords must not be listed", names)
	}
	if err := a.DeleteVisitorUser("robot.example.com", "alice"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.VerifyVisitorUser("robot.example.com", "alice", "correct-horse-battery"); ok {
		t.Fatal("deleted user still accepted")
	}
	if err := a.DeleteVisitorUser("robot.example.com", "alice"); err != ErrUserNotFound {
		t.Fatalf("second delete: %v", err)
	}
}

func TestVisitorTokenLifecycle(t *testing.T) {
	a := newTestAuth(t)
	if _, err := a.AddVisitorToken("robot.example.com", "reader", nil, nil); err == nil {
		t.Fatal("token without methods accepted")
	}
	token, err := a.AddVisitorToken("robot.example.com", "reader", []string{"get"}, []string{"/api"})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a.FindVisitorToken("robot.example.com", token)
	if err != nil {
		t.Fatalf("issued token not found: %v", err)
	}
	if rec.Methods != "GET" || rec.PathPrefixes != "/api" {
		t.Fatalf("scopes stored as %q %q", rec.Methods, rec.PathPrefixes)
	}
	if _, err := a.FindVisitorToken("robot.example.com", "not-the-token"); err != ErrTokenNotFound {
		t.Fatalf("wrong token: %v", err)
	}
	list, _ := a.ListVisitorTokens("robot.example.com")
	if len(list) != 1 || list[0].TokenHash == token {
		t.Fatal("the token itself must not be stored")
	}
	if err := a.RevokeVisitorToken("robot.example.com", list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.FindVisitorToken("robot.example.com", token); err != ErrTokenNotFound {
		t.Fatalf("revoked token still found: %v", err)
	}
}

func TestCredentialCountAndLatch(t *testing.T) {
	a := newTestAuth(t)
	if n, _ := a.CountVisitorCredentials("robot.example.com"); n != 0 {
		t.Fatalf("new domain has %d credentials", n)
	}
	a.AddVisitorUser("robot.example.com", "alice", "correct-horse-battery")
	a.AddVisitorToken("robot.example.com", "reader", []string{"GET"}, nil)
	if n, _ := a.CountVisitorCredentials("robot.example.com"); n != 2 {
		t.Fatalf("count %d, want 2", n)
	}
	if err := a.SetAuthLatched("robot.example.com", true); err != nil {
		t.Fatal(err)
	}
	d, _ := a.GetDomain("robot.example.com")
	if !d.AuthLatched {
		t.Fatal("latch not stored")
	}
	if err := a.SetAuthLatched("robot.example.com", false); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.GetDomain("robot.example.com"); d.AuthLatched {
		t.Fatal("latch not cleared")
	}
}

func TestPassthroughFlagIsStored(t *testing.T) {
	a := newTestAuth(t)
	d, _ := a.GetDomain("robot.example.com")
	d.Passthrough = true
	if err := a.UpdateDomain(d); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.GetDomain("robot.example.com"); !got.Passthrough {
		t.Fatal("passthrough not stored")
	}
}

func TestAllowedCIDRsAreStored(t *testing.T) {
	a := newTestAuth(t)
	d, _ := a.GetDomain("robot.example.com")
	d.AllowedCIDRs = "10.0.0.0/8,192.168.1.7/32"
	if err := a.UpdateDomain(d); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.GetDomain("robot.example.com"); got.AllowedCIDRs != "10.0.0.0/8,192.168.1.7/32" {
		t.Fatalf("rules stored as %q", got.AllowedCIDRs)
	}
}
