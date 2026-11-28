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
	if _, err := a.AddVisitorUser("robot.example.com", "bad name", ""); err != ErrInvalidUsername {
		t.Fatalf("username with a space: %v", err)
	}
	secret, err := a.AddVisitorUser("robot.example.com", "alice", "")
	if err != nil || secret == "" {
		t.Fatalf("create: %v", err)
	}
	if _, err := a.AddVisitorUser("robot.example.com", "alice", ""); err != ErrUserExists {
		t.Fatalf("duplicate user: %v", err)
	}
	got, err := a.VisitorUserSecret("robot.example.com", "alice")
	if err != nil || got != secret {
		t.Fatalf("stored secret does not match the one shown: %v", err)
	}
	if _, err := a.VisitorUserSecret("robot.example.com", "nobody"); err != ErrUserNotFound {
		t.Fatalf("unknown user: %v", err)
	}
	names, _ := a.ListVisitorUsers("robot.example.com")
	if len(names) != 1 || names[0] != "alice" {
		t.Fatalf("users %v; secrets must not be listed", names)
	}

	rotated, err := a.RotateVisitorUser("robot.example.com", "alice")
	if err != nil || rotated == secret || rotated == "" {
		t.Fatalf("rotate: %v", err)
	}
	if now, _ := a.VisitorUserSecret("robot.example.com", "alice"); now != rotated {
		t.Fatal("rotation not stored")
	}
	if _, err := a.RotateVisitorUser("robot.example.com", "nobody"); err != ErrUserNotFound {
		t.Fatalf("rotate unknown user: %v", err)
	}

	if err := a.DeleteVisitorUser("robot.example.com", "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.VisitorUserSecret("robot.example.com", "alice"); err != ErrUserNotFound {
		t.Fatalf("deleted user still has a secret: %v", err)
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
	a.AddVisitorUser("robot.example.com", "alice", "")
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

func TestSuppliedSecretIsStoredAsGiven(t *testing.T) {
	a := newTestAuth(t)
	mine := "a-secret-the-client-chose-for-jtorres-01"
	got, err := a.AddVisitorUser("robot.example.com", "jtorres", mine)
	if err != nil || got != mine {
		t.Fatalf("supplied secret: %q %v", got, err)
	}
	if stored, _ := a.VisitorUserSecret("robot.example.com", "jtorres"); stored != mine {
		t.Fatal("supplied secret not stored")
	}
	if _, err := a.AddVisitorUser("robot.example.com", "short", "too-short"); err != ErrWeakSecret {
		t.Fatalf("short secret: %v", err)
	}
}
