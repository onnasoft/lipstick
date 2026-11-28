package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/OnnaSoft/lipstick/server/db"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const strongPassword = "correct-horse-battery"

func newTestAdminStore(t *testing.T) (*AdminStore, *gorm.DB) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "admin.db")), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&db.AdminUser{}, &db.AdminSession{}); err != nil {
		t.Fatal(err)
	}
	return NewAdminStore(conn), conn
}

func TestAdminLoginAndSession(t *testing.T) {
	s, _ := newTestAdminStore(t)
	if err := s.CreateAdminUser("jtorres", strongPassword, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	id, err := s.Login("jtorres", strongPassword)
	if err != nil {
		t.Fatalf("right password: %v", err)
	}
	token, expires, err := s.NewSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if until := time.Until(expires); until < AdminSessionTTL-time.Minute || until > AdminSessionTTL {
		t.Fatalf("session lifetime %v, want about %v", until, AdminSessionTTL)
	}
	if user, err := s.Authenticate(token); err != nil || user != "jtorres" {
		t.Fatalf("session did not authenticate: %q %v", user, err)
	}
	if _, err := s.Authenticate(token + "x"); err == nil {
		t.Fatal("altered token accepted")
	}
	if err := s.Logout(token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token); err == nil {
		t.Fatal("token still works after logout")
	}
}

func TestAdminLoginRefusesWrongPasswordAndUnknownUser(t *testing.T) {
	s, _ := newTestAdminStore(t)
	s.CreateAdminUser("jtorres", strongPassword, RoleAdmin)
	if _, err := s.Login("jtorres", "wrong-password-here"); err != ErrBadLogin {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := s.Login("nobody", strongPassword); err != ErrBadLogin {
		t.Fatalf("unknown user: %v", err)
	}
}

// A disabled account cannot log in, and its live sessions stop working at once.
func TestDisablingEndsSessionsAndBlocksLogin(t *testing.T) {
	s, _ := newTestAdminStore(t)
	s.CreateAdminUser("jtorres", strongPassword, RoleAdmin)
	id, _ := s.Login("jtorres", strongPassword)
	token, _, _ := s.NewSession(id)
	if err := s.SetAdminDisabled("jtorres", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token); err == nil {
		t.Fatal("session survived disabling")
	}
	if _, err := s.Login("jtorres", strongPassword); err != ErrBadLogin {
		t.Fatalf("disabled account logged in: %v", err)
	}
	s.SetAdminDisabled("jtorres", false)
	if _, err := s.Login("jtorres", strongPassword); err != nil {
		t.Fatalf("re-enabled account cannot log in: %v", err)
	}
}

// Resetting the password ends every session of that account.
func TestPasswordResetEndsSessions(t *testing.T) {
	s, _ := newTestAdminStore(t)
	s.CreateAdminUser("jtorres", strongPassword, RoleAdmin)
	id, _ := s.Login("jtorres", strongPassword)
	token, _, _ := s.NewSession(id)
	if err := s.ResetAdminPassword("jtorres", "a-brand-new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token); err == nil {
		t.Fatal("session survived a password reset")
	}
	if _, err := s.Login("jtorres", strongPassword); err != ErrBadLogin {
		t.Fatal("old password still works")
	}
	if _, err := s.Login("jtorres", "a-brand-new-password"); err != nil {
		t.Fatalf("new password refused: %v", err)
	}
}

func TestExpiredSessionIsRefused(t *testing.T) {
	s, conn := newTestAdminStore(t)
	s.CreateAdminUser("jtorres", strongPassword, RoleAdmin)
	id, _ := s.Login("jtorres", strongPassword)
	token, _, _ := s.NewSession(id)
	conn.Model(&db.AdminSession{}).Where("user_id = ?", id).Update("expires_at", time.Now().Add(-time.Second))
	if _, err := s.Authenticate(token); err == nil {
		t.Fatal("expired session accepted")
	}
}

// The database holds hashes only: neither the password nor the token is stored.
func TestSecretsAreNotStoredInClear(t *testing.T) {
	s, conn := newTestAdminStore(t)
	s.CreateAdminUser("jtorres", strongPassword, RoleAdmin)
	id, _ := s.Login("jtorres", strongPassword)
	token, _, _ := s.NewSession(id)
	var user db.AdminUser
	conn.First(&user)
	var sess db.AdminSession
	conn.First(&sess)
	if user.PasswordHash == strongPassword || sess.TokenHash == token {
		t.Fatal("a secret is stored in clear")
	}
}

func TestCreateRefusesBadInput(t *testing.T) {
	s, _ := newTestAdminStore(t)
	if err := s.CreateAdminUser("bad name", strongPassword, RoleAdmin); err != ErrInvalidUsername {
		t.Fatalf("username: %v", err)
	}
	if err := s.CreateAdminUser("jtorres", "short", RoleAdmin); err != ErrPasswordTooShort {
		t.Fatalf("short password: %v", err)
	}
	if err := s.CreateAdminUser("jtorres", strongPassword, "root"); err != ErrInvalidRole {
		t.Fatalf("role: %v", err)
	}
	s.CreateAdminUser("jtorres", strongPassword, RoleOperator)
	if err := s.CreateAdminUser("jtorres", strongPassword, RoleAdmin); err != ErrAdminUserExists {
		t.Fatalf("duplicate: %v", err)
	}
	users, _ := s.ListAdminUsers()
	if len(users) != 1 || users[0].Role != RoleOperator || users[0].Username != "jtorres" {
		t.Fatalf("list %+v", users)
	}
}
