package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OnnaSoft/lipstick/server/db"
	"golang.org/x/crypto/argon2"
)

// Visitor authentication: users with passwords and API tokens with permissions,
// per domain. Passwords are hashed with argon2id. Tokens are 256 random bits,
// shown once, and stored only as a SHA-256 hash.

// VisitorToken is the stored token record. The token itself is never kept.
type VisitorToken = db.VisitorToken

var (
	ErrInvalidUsername = errors.New("username must be 1 to 64 characters with no spaces or colons")
	ErrWeakPassword    = errors.New("password must be at least 12 characters")
	ErrUserExists      = errors.New("a visitor user with that name already exists")
	ErrUserNotFound    = errors.New("visitor user not found")
	ErrTokenNotFound   = errors.New("visitor token not found")
)

// Argon2id parameters follow the OWASP minimum for argon2id.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// HashToken returns the value stored for a token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (p *PostgresAuthManager) domainID(domain string) (uint, error) {
	var d db.Domain
	if err := p.db.Where("name = ?", domain).First(&d).Error; err != nil {
		return 0, ErrDomainNotFound
	}
	return d.ID, nil
}

// AddVisitorUser creates a user for a domain, with a hashed password.
func (p *PostgresAuthManager) AddVisitorUser(domain, username, password string) error {
	if username == "" || len(username) > 64 || strings.ContainsAny(username, " :") {
		return ErrInvalidUsername
	}
	if len(password) < 12 {
		return ErrWeakPassword
	}
	id, err := p.domainID(domain)
	if err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	u := db.VisitorUser{DomainID: id, Username: username, PasswordHash: hash, CreatedAt: time.Now()}
	if err := p.db.Create(&u).Error; err != nil {
		return ErrUserExists
	}
	return nil
}

// DeleteVisitorUser removes a user. Its access ends at once.
func (p *PostgresAuthManager) DeleteVisitorUser(domain, username string) error {
	id, err := p.domainID(domain)
	if err != nil {
		return err
	}
	res := p.db.Where("domain_id = ? AND username = ?", id, username).Delete(&db.VisitorUser{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// VerifyVisitorUser reports whether the username and password match a user of the domain.
func (p *PostgresAuthManager) VerifyVisitorUser(domain, username, password string) (bool, error) {
	id, err := p.domainID(domain)
	if err != nil {
		return false, err
	}
	var u db.VisitorUser
	if err := p.db.Where("domain_id = ? AND username = ?", id, username).First(&u).Error; err != nil {
		return false, nil
	}
	return verifyPassword(u.PasswordHash, password), nil
}

// ListVisitorUsers returns the usernames of a domain. Passwords are never returned.
func (p *PostgresAuthManager) ListVisitorUsers(domain string) ([]string, error) {
	id, err := p.domainID(domain)
	if err != nil {
		return nil, err
	}
	var users []db.VisitorUser
	if err := p.db.Where("domain_id = ?", id).Order("username").Find(&users).Error; err != nil {
		return nil, err
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, u.Username)
	}
	return names, nil
}

// AddVisitorToken creates a token and returns it. The caller must show it to the
// user now: only its hash is kept. methods may contain "*"; prefixes default to "/".
func (p *PostgresAuthManager) AddVisitorToken(domain, name string, methods, prefixes []string) (string, error) {
	if name == "" || len(name) > 64 {
		return "", errors.New("token name must be 1 to 64 characters")
	}
	if len(methods) == 0 {
		return "", errors.New("a token needs at least one method, or \"*\"")
	}
	if len(prefixes) == 0 {
		prefixes = []string{"/"}
	}
	id, err := p.domainID(domain)
	if err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	rec := db.VisitorToken{
		DomainID:     id,
		Name:         name,
		TokenHash:    HashToken(token),
		Methods:      strings.ToUpper(strings.Join(methods, ",")),
		PathPrefixes: strings.Join(prefixes, ","),
		CreatedAt:    time.Now(),
	}
	if err := p.db.Create(&rec).Error; err != nil {
		return "", err
	}
	return token, nil
}

// RevokeVisitorToken ends a token. It stops working at once.
func (p *PostgresAuthManager) RevokeVisitorToken(domain string, tokenID uint) error {
	id, err := p.domainID(domain)
	if err != nil {
		return err
	}
	now := time.Now()
	res := p.db.Model(&db.VisitorToken{}).Where("id = ? AND domain_id = ? AND revoked_at IS NULL", tokenID, id).Update("revoked_at", &now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrTokenNotFound
	}
	return nil
}

// FindVisitorToken returns the active token that matches, for the domain.
func (p *PostgresAuthManager) FindVisitorToken(domain, token string) (*VisitorToken, error) {
	id, err := p.domainID(domain)
	if err != nil {
		return nil, err
	}
	var rec db.VisitorToken
	err = p.db.Where("domain_id = ? AND token_hash = ? AND revoked_at IS NULL", id, HashToken(token)).First(&rec).Error
	if err != nil {
		return nil, ErrTokenNotFound
	}
	return &rec, nil
}

// ListVisitorTokens returns the tokens of a domain, without their values.
func (p *PostgresAuthManager) ListVisitorTokens(domain string) ([]VisitorToken, error) {
	id, err := p.domainID(domain)
	if err != nil {
		return nil, err
	}
	var recs []VisitorToken
	if err := p.db.Where("domain_id = ?", id).Order("id").Find(&recs).Error; err != nil {
		return nil, err
	}
	return recs, nil
}

// CountVisitorCredentials counts the active users and tokens of a domain. A domain
// cannot be restricted without at least one.
func (p *PostgresAuthManager) CountVisitorCredentials(domain string) (int, error) {
	id, err := p.domainID(domain)
	if err != nil {
		return 0, err
	}
	var users, tokens int64
	p.db.Model(&db.VisitorUser{}).Where("domain_id = ?", id).Count(&users)
	p.db.Model(&db.VisitorToken{}).Where("domain_id = ? AND revoked_at IS NULL", id).Count(&tokens)
	return int(users + tokens), nil
}

// SetAuthLatched sets or clears the restricted state of a domain.
func (p *PostgresAuthManager) SetAuthLatched(domain string, latched bool) error {
	res := p.db.Model(&db.Domain{}).Where("name = ?", domain).Update("auth_latched", latched)
	if res.Error != nil {
		return res.Error
	}
	p.cache.Delete("domain_" + domain)
	p.cache.Delete("all_domains")
	return nil
}
