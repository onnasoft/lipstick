package admin

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

// minAPIKeyLength is the shortest key a domain may have. A client's key is its password.
const minAPIKeyLength = 32

// domainLabel is one dot-separated part of a host name: letters, digits and inner hyphens.
var domainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// validDomainName accepts a lowercase host name, such as robot.example.com or localhost.
// Uppercase is refused rather than folded: a visitor's Host header must match the name exactly.
func validDomainName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if len(name) > 253 {
		return errors.New("name is longer than 253 characters")
	}
	for _, label := range strings.Split(name, ".") {
		if !domainLabel.MatchString(label) {
			return errors.New("name must be a host name in lowercase letters, digits and hyphens, such as robot.example.com")
		}
	}
	return nil
}

// newAPIKey makes a random key of 32 bytes, as hex (64 characters).
func newAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
