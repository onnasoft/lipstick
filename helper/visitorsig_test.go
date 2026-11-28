package helper

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testSecret = "s3cr3t-for-alice-only"

func signedFor(t *testing.T, method, target, host string, now time.Time) (VisitorSig, string) {
	t.Helper()
	req := &http.Request{Method: method, URL: mustURL(t, target), Host: host, Header: http.Header{}}
	if err := SignRequest(req, "alice", testSecret, now); err != nil {
		t.Fatal(err)
	}
	sig, ok := ParseVisitorAuth(req.Header.Get("Authorization"))
	if !ok {
		t.Fatalf("own header does not parse: %q", req.Header.Get("Authorization"))
	}
	return sig, req.Header.Get("Authorization")
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSignedRequestVerifies(t *testing.T) {
	now := time.Unix(1780000000, 0)
	sig, _ := signedFor(t, "GET", "/hello?x=1", "robot.example.com", now)
	if !VerifyVisitor(testSecret, sig, "GET", "/hello?x=1", "robot.example.com") {
		t.Fatal("a correct signature was refused")
	}
}

// Every field the signature covers must break it when changed.
func TestTamperingBreaksTheSignature(t *testing.T) {
	now := time.Unix(1780000000, 0)
	sig, _ := signedFor(t, "GET", "/hello", "robot.example.com", now)
	cases := map[string]func() bool{
		"method": func() bool { return VerifyVisitor(testSecret, sig, "POST", "/hello", "robot.example.com") },
		"path":   func() bool { return VerifyVisitor(testSecret, sig, "GET", "/admin", "robot.example.com") },
		"host":   func() bool { return VerifyVisitor(testSecret, sig, "GET", "/hello", "other.example.com") },
		"secret": func() bool { return VerifyVisitor("another-secret", sig, "GET", "/hello", "robot.example.com") },
		"user": func() bool {
			s := sig
			s.User = "bob"
			return VerifyVisitor(testSecret, s, "GET", "/hello", "robot.example.com")
		},
		"ts": func() bool {
			s := sig
			s.Timestamp = "1780000001"
			return VerifyVisitor(testSecret, s, "GET", "/hello", "robot.example.com")
		},
		"nonce": func() bool {
			s := sig
			s.Nonce = "AAAA"
			return VerifyVisitor(testSecret, s, "GET", "/hello", "robot.example.com")
		},
	}
	for name, verify := range cases {
		if verify() {
			t.Errorf("changing the %s still verifies", name)
		}
	}
}

func TestEmptySecretNeverVerifies(t *testing.T) {
	now := time.Unix(1780000000, 0)
	sig, _ := signedFor(t, "GET", "/", "h", now)
	if VerifyVisitor("", sig, "GET", "/", "h") {
		t.Fatal("empty secret verified")
	}
}

func TestParseRefusesMalformedHeaders(t *testing.T) {
	good := `LipstickHMAC user="alice", ts="1780000000", nonce="abcDEF-_12", sig="abcDEF-_12"`
	if _, ok := ParseVisitorAuth(good); !ok {
		t.Fatal("valid header refused")
	}
	bad := []string{
		"Basic YWxpY2U6eA==",
		`LipstickHMAC user="alice", ts="1780000000", nonce="abc"`,                             // no sig
		`LipstickHMAC user="alice", ts="1780000000", nonce="abc", sig="abc", extra="x"`,       // unknown field
		`LipstickHMAC user="alice", user="bob", ts="1", nonce="a", sig="a"`,                   // repeated field
		`LipstickHMAC user=alice, ts="1", nonce="a", sig="a"`,                                 // unquoted value
		`LipstickHMAC user="a b", ts="1", nonce="a", sig="a"`,                                 // bad username
		`LipstickHMAC user="alice", ts="soon", nonce="a", sig="a"`,                            // non-numeric time
		`LipstickHMAC user="alice", ts="1", nonce="a+b", sig="a"`,                             // not base64url
		`LipstickHMAC user="alice", ts="1", nonce="` + strings.Repeat("a", 65) + `", sig="a"`, // nonce too long
	}
	for _, h := range bad {
		if _, ok := ParseVisitorAuth(h); ok {
			t.Errorf("accepted malformed header %q", h)
		}
	}
}

func TestValidVisitorUsername(t *testing.T) {
	for _, ok := range []string{"alice", "bob.smith", "ops@team-1", "x_y"} {
		if !ValidVisitorUsername(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "a b", "a,b", `a"b`, "a:b", strings.Repeat("a", 65)} {
		if ValidVisitorUsername(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A vector computed by the Python example in the README, so the two implementations agree.
func TestMatchesPythonVector(t *testing.T) {
	sig := SignVisitor("test-secret", "GET", "/hello", "robot.example.com", "alice", "1780000000", "AAAAAAAAAAAAAAAAAAAAAA")
	if sig != "hhQLJqeYdQxniOhQeU_wz_bDK-ShksBRUcDYQFuZibI" {
		t.Fatalf("Go signature %q differs from the Python vector", sig)
	}
}
