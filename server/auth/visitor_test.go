package auth

import "testing"

func TestPasswordHashVerifies(t *testing.T) {
	h, err := hashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(h, "correct horse battery") {
		t.Fatal("right password rejected")
	}
	if verifyPassword(h, "correct horse batterY") {
		t.Fatal("wrong password accepted")
	}
	// The stored value is a hash, never the password.
	if containsSubstring(h, "correct horse") {
		t.Fatal("password visible in the stored hash")
	}
}

func TestVerifyPasswordRefusesMalformedHash(t *testing.T) {
	for _, h := range []string{"", "plain", "argon2id$!!$!!", "bcrypt$x$y"} {
		if verifyPassword(h, "anything") {
			t.Errorf("malformed hash %q accepted", h)
		}
	}
}

func TestTokenHashIsStableAndNotTheToken(t *testing.T) {
	if HashToken("abc") != HashToken("abc") {
		t.Fatal("hash not stable")
	}
	if HashToken("abc") == "abc" || len(HashToken("abc")) != 64 {
		t.Fatal("token stored in the clear or wrong hash length")
	}
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
