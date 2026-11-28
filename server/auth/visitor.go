package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/server/db"
)

// Visitor authentication, per domain:
//   - users, each with a secret of its own. A signed request (see helper/visitorsig.go)
//     proves the user knows the secret without sending it. The secret is kept because
//     the server needs it to verify the signature.
//   - API tokens with permissions. Only their SHA-256 hash is kept.

// VisitorToken is the stored token record. The token itself is never kept.
type VisitorToken = db.VisitorToken

var (
	ErrInvalidUsername = errors.New("username must be 1 to 64 letters, digits, dots, underscores, at signs or hyphens")
	ErrWeakSecret      = errors.New("secret must be at least 32 characters")
	ErrUserExists      = errors.New("a visitor user with that name already exists")
	ErrUserNotFound    = errors.New("visitor user not found")
	ErrTokenNotFound   = errors.New("visitor token not found")
)

// MinSecretLen is the shortest secret accepted for a visitor user.
const MinSecretLen = 32

// newSecret returns a fresh 256-bit secret, base64url encoded.
func newSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
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

// AddVisitorUser creates a user for a domain with its own secret. If secret is
// empty, a new one is generated. It returns the secret stored, so the caller knows
// the value in both cases. The secret must be at least MinSecretLen characters.
func (p *PostgresAuthManager) AddVisitorUser(domain, username, secret string) (string, error) {
	if !helper.ValidVisitorUsername(username) {
		return "", ErrInvalidUsername
	}
	if secret == "" {
		generated, err := newSecret()
		if err != nil {
			return "", err
		}
		secret = generated
	} else if len(secret) < MinSecretLen {
		return "", ErrWeakSecret
	}
	id, err := p.domainID(domain)
	if err != nil {
		return "", err
	}
	u := db.VisitorUser{DomainID: id, Username: username, Secret: secret, CreatedAt: time.Now()}
	if err := p.db.Create(&u).Error; err != nil {
		return "", ErrUserExists
	}
	return secret, nil
}

// RotateVisitorUser replaces a user's secret and returns the new one. The old
// secret stops working at once.
func (p *PostgresAuthManager) RotateVisitorUser(domain, username string) (string, error) {
	id, err := p.domainID(domain)
	if err != nil {
		return "", err
	}
	secret, err := newSecret()
	if err != nil {
		return "", err
	}
	res := p.db.Model(&db.VisitorUser{}).Where("domain_id = ? AND username = ?", id, username).Update("secret", secret)
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "", ErrUserNotFound
	}
	return secret, nil
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

// VisitorUserSecret returns the secret of a user of the domain. It is used to
// verify signatures and is never sent to a visitor or returned by the API.
func (p *PostgresAuthManager) VisitorUserSecret(domain, username string) (string, error) {
	id, err := p.domainID(domain)
	if err != nil {
		return "", err
	}
	var u db.VisitorUser
	if err := p.db.Where("domain_id = ? AND username = ?", id, username).First(&u).Error; err != nil {
		return "", ErrUserNotFound
	}
	return u.Secret, nil
}

// ListVisitorUsers returns the usernames of a domain. Secrets are never returned.
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
