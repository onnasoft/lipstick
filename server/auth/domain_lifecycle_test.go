package auth

import (
	"testing"

	"github.com/OnnaSoft/lipstick/server/db"
)

// Deleting a domain removes its visitor users and tokens, and leaves those of other domains.
func TestDeletingADomainRemovesItsVisitorSecrets(t *testing.T) {
	a := newTestAuth(t)
	other := "other.example.com"
	if err := a.AddDomain(&Domain{Name: other, ApiKey: "key-0123456789abcdef-other", AllowMultipleConnections: true}); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"robot.example.com", other} {
		if _, err := a.AddVisitorUser(d, "alice", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := a.AddVisitorToken(d, "ci", []string{"GET"}, []string{"/"}); err != nil {
			t.Fatal(err)
		}
	}
	robot, err := a.GetDomain("robot.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.DelDomain(robot.ID); err != nil {
		t.Fatal(err)
	}

	conn := a.(*PostgresAuthManager).db
	var users, tokens int64
	conn.Model(&db.VisitorUser{}).Where("domain_id = ?", robot.ID).Count(&users)
	conn.Model(&db.VisitorToken{}).Where("domain_id = ?", robot.ID).Count(&tokens)
	if users != 0 || tokens != 0 {
		t.Fatalf("secrets left behind: %d users, %d tokens", users, tokens)
	}
	kept, _ := a.GetDomain(other)
	conn.Model(&db.VisitorUser{}).Where("domain_id = ?", kept.ID).Count(&users)
	if users != 1 {
		t.Fatalf("another domain's user was removed")
	}
}

func TestSetDomainDisabledTurnsADomainOffAndOn(t *testing.T) {
	a := newTestAuth(t)
	if err := a.SetDomainDisabled("nope.example.com", true); err != ErrDomainNotFound {
		t.Fatalf("unknown domain: %v", err)
	}
	if err := a.SetDomainDisabled("robot.example.com", true); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.GetDomain("robot.example.com"); !d.Disabled {
		t.Fatal("not disabled")
	}
	if err := a.SetDomainDisabled("robot.example.com", false); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.GetDomain("robot.example.com"); d.Disabled {
		t.Fatal("still disabled after enable")
	}
}
