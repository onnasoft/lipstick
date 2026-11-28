package helper

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Signed visitor requests. Each user of a restricted domain has a secret of their
// own. A request carries the user name, a timestamp, a random nonce and an
// HMAC-SHA256 signature over the request line, the Host header and those values.
//
//	Authorization: LipstickHMAC user="alice", ts="1780000000", nonce="...", sig="..."
//
// The body is not signed: it is protected by TLS only.
const (
	VisitorSigScheme  = "LipstickHMAC"
	visitorSigVersion = "LIPSTICK-HMAC-V1"
	// VisitorSigWindow is how far a request timestamp may differ from the server's clock.
	// A nonce is refused again for this long, so a replay inside the window fails.
	VisitorSigWindow = 2 * time.Minute
	maxNonceLen      = 64
	maxUsernameLen   = 64
)

// VisitorSig is the parsed Authorization header of a signed visitor request.
type VisitorSig struct {
	User      string
	Timestamp string
	Nonce     string
	Signature string
}

// ValidVisitorUsername reports whether a name can be used in the header: 1 to 64
// letters, digits, dots, underscores, at signs or hyphens.
func ValidVisitorUsername(name string) bool {
	if name == "" || len(name) > maxUsernameLen {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == '@', c == '-':
		default:
			return false
		}
	}
	return true
}

func visitorCanonical(method, target, host, user, ts, nonce string) string {
	return strings.Join([]string{
		visitorSigVersion,
		strings.ToUpper(method),
		target,
		strings.ToLower(host),
		user,
		ts,
		nonce,
	}, "\n")
}

// SignVisitor computes the signature for a request with the given fields.
func SignVisitor(secret, method, target, host, user, ts, nonce string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(visitorCanonical(method, target, host, user, ts, nonce)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyVisitor reports whether sig matches the request. The comparison is constant
// time. An empty secret never verifies.
func VerifyVisitor(secret string, sig VisitorSig, method, target, host string) bool {
	if secret == "" {
		return false
	}
	want := SignVisitor(secret, method, target, host, sig.User, sig.Timestamp, sig.Nonce)
	return hmac.Equal([]byte(want), []byte(sig.Signature))
}

// ParseVisitorAuth reads the Authorization header. It accepts only the four
// parameters, each once, with values in the expected characters.
func ParseVisitorAuth(header string) (VisitorSig, bool) {
	rest, ok := strings.CutPrefix(header, VisitorSigScheme+" ")
	if !ok {
		return VisitorSig{}, false
	}
	var s VisitorSig
	seen := map[string]bool{}
	for _, part := range strings.Split(rest, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			return VisitorSig{}, false
		}
		value = value[1 : len(value)-1]
		if seen[key] {
			return VisitorSig{}, false
		}
		seen[key] = true
		switch key {
		case "user":
			s.User = value
		case "ts":
			s.Timestamp = value
		case "nonce":
			s.Nonce = value
		case "sig":
			s.Signature = value
		default:
			return VisitorSig{}, false
		}
	}
	if !ValidVisitorUsername(s.User) || len(s.Nonce) == 0 || len(s.Nonce) > maxNonceLen ||
		!isDigits(s.Timestamp) || !isBase64URL(s.Nonce) || !isBase64URL(s.Signature) {
		return VisitorSig{}, false
	}
	return s, true
}

// SignRequest adds a signed Authorization header to req, stamped with now.
func SignRequest(req *http.Request, user, secret string, now time.Time) error {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := SignVisitor(secret, req.Method, req.URL.RequestURI(), host, user, ts, nonce)
	req.Header.Set("Authorization", fmt.Sprintf(`%s user="%s", ts="%s", nonce="%s", sig="%s"`,
		VisitorSigScheme, user, ts, nonce, sig))
	return nil
}

func isDigits(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isBase64URL(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
