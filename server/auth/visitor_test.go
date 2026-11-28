package auth

import "testing"

func TestSecretsAreUniquePerUser(t *testing.T) {
	a, b := newSecretOrFail(t), newSecretOrFail(t)
	if a == b {
		t.Fatal("two secrets are equal")
	}
	if len(a) < 40 {
		t.Fatalf("secret too short: %d characters", len(a))
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

func newSecretOrFail(t *testing.T) string {
	t.Helper()
	s, err := newSecret()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
