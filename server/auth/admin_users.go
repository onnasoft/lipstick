package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/helper"
	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/OnnaSoft/lipstick/server/db"
	"golang.org/x/crypto/argon2"
	"gorm.io/gorm"
)

// Admin accounts. They are created and reset from the console on the server
// machine, never through the API. A login returns a session token; only its hash is
// stored, and it expires after AdminSessionTTL.

const (
	AdminSessionTTL = 12 * time.Hour
	MinPasswordLen  = 12
	// A superuser manages admin accounts too. An operator manages domains and visitor
	// users, and changes only its own password.
	RoleSuperuser = "superuser"
	RoleOperator  = "operator"
)

var (
	ErrAdminUserExists   = errors.New("an admin user with that name already exists")
	ErrAdminUserNotFound = errors.New("admin user not found")
	ErrBadLogin          = errors.New("invalid username or password")
	ErrSessionInvalid    = errors.New("session is not valid")
	ErrPasswordTooShort  = errors.New("password must be at least 12 characters")
	ErrInvalidRole       = errors.New("role must be superuser or operator")
	ErrWrongPassword     = errors.New("the current password is wrong")
	ErrLastSuperuser     = errors.New("at least one active superuser must remain")
)

// AdminInfo describes an admin user without its password.
type AdminInfo struct {
	Username  string
	Role      string
	Disabled  bool
	CreatedAt time.Time
}

// AdminStore manages admin accounts and their sessions.
type AdminStore struct {
	db *gorm.DB
}

// NewAdminStore returns a store on the given connection.
func NewAdminStore(conn *gorm.DB) *AdminStore {
	return &AdminStore{db: conn}
}

var (
	adminOnce   sync.Once
	adminShared *AdminStore
)

// MakeAdminStore returns the shared store, opening the database on first use.
func MakeAdminStore() *AdminStore {
	adminOnce.Do(func() {
		conf, err := config.GetConfig()
		if err != nil {
			panic(err)
		}
		conn, err := db.GetConnection(conf.Database)
		if err != nil {
			panic(err)
		}
		adminShared = NewAdminStore(conn)
	})
	return adminShared
}

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

func checkNewPassword(password string) error {
	if len(password) < MinPasswordLen {
		return ErrPasswordTooShort
	}
	return nil
}

// CreateAdminUser adds an account with the given role.
func (s *AdminStore) CreateAdminUser(username, password, role string) error {
	if !helper.ValidVisitorUsername(username) {
		return ErrInvalidUsername
	}
	if !validRole(role) {
		return ErrInvalidRole
	}
	if err := checkNewPassword(password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	u := db.AdminUser{Username: username, PasswordHash: hash, Role: role, CreatedAt: time.Now()}
	if err := s.db.Create(&u).Error; err != nil {
		return ErrAdminUserExists
	}
	return nil
}

// ResetAdminPassword sets a new password and ends every session of the user.
func (s *AdminStore) ResetAdminPassword(username, password string) error {
	if err := checkNewPassword(password); err != nil {
		return err
	}
	u, err := s.findUser(username)
	if err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	if err := s.db.Model(&db.AdminUser{}).Where("id = ?", u.ID).Update("password_hash", hash).Error; err != nil {
		return err
	}
	return s.revokeSessions(u.ID)
}

func validRole(role string) bool {
	return role == RoleSuperuser || role == RoleOperator
}

// ChangeOwnPassword lets an account change its password after proving the current
// one. Every session of the account ends, so the new password is needed everywhere.
func (s *AdminStore) ChangeOwnPassword(username, current, next string) error {
	if err := checkNewPassword(next); err != nil {
		return err
	}
	u, err := s.findUser(username)
	if err != nil {
		return err
	}
	if !verifyPassword(u.PasswordHash, current) {
		return ErrWrongPassword
	}
	return s.ResetAdminPassword(username, next)
}

// SetAdminRole changes the role of an account. The last active superuser cannot be
// demoted.
func (s *AdminStore) SetAdminRole(username, role string) error {
	if !validRole(role) {
		return ErrInvalidRole
	}
	u, err := s.findUser(username)
	if err != nil {
		return err
	}
	if u.Role == RoleSuperuser && role != RoleSuperuser && !u.Disabled {
		if err := s.guardLastSuperuser(); err != nil {
			return err
		}
	}
	return s.db.Model(&db.AdminUser{}).Where("id = ?", u.ID).Update("role", role).Error
}

// guardLastSuperuser refuses a change that would leave no active superuser.
func (s *AdminStore) guardLastSuperuser() error {
	var active int64
	s.db.Model(&db.AdminUser{}).Where("role = ? AND disabled = ?", RoleSuperuser, false).Count(&active)
	if active <= 1 {
		return ErrLastSuperuser
	}
	return nil
}

// SetAdminDisabled enables or disables an account. Disabling ends its sessions. The
// last active superuser cannot be disabled.
func (s *AdminStore) SetAdminDisabled(username string, disabled bool) error {
	u, err := s.findUser(username)
	if err != nil {
		return err
	}
	if disabled && !u.Disabled && u.Role == RoleSuperuser {
		if err := s.guardLastSuperuser(); err != nil {
			return err
		}
	}
	if err := s.db.Model(&db.AdminUser{}).Where("id = ?", u.ID).Update("disabled", disabled).Error; err != nil {
		return err
	}
	if disabled {
		return s.revokeSessions(u.ID)
	}
	return nil
}

// ListAdminUsers returns every account, without password hashes.
func (s *AdminStore) ListAdminUsers() ([]AdminInfo, error) {
	var users []db.AdminUser
	if err := s.db.Order("username").Find(&users).Error; err != nil {
		return nil, err
	}
	out := make([]AdminInfo, 0, len(users))
	for _, u := range users {
		out = append(out, AdminInfo{Username: u.Username, Role: u.Role, Disabled: u.Disabled, CreatedAt: u.CreatedAt})
	}
	return out, nil
}

// Login checks a username and password and returns the account id. Unknown users,
// wrong passwords and disabled accounts all give the same error.
func (s *AdminStore) Login(username, password string) (uint, error) {
	u, err := s.findUser(username)
	if err != nil || u.Disabled || !verifyPassword(u.PasswordHash, password) {
		return 0, ErrBadLogin
	}
	return u.ID, nil
}

// NewSession creates a login token for an account. It is returned once.
func (s *AdminStore) NewSession(userID uint) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(AdminSessionTTL)
	rec := db.AdminSession{UserID: userID, TokenHash: HashToken(token), ExpiresAt: expires, CreatedAt: time.Now()}
	if err := s.db.Create(&rec).Error; err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// Authenticate returns the account and role of a live session: not expired, not
// revoked, and the account not disabled.
func (s *AdminStore) Authenticate(token string) (string, string, error) {
	if token == "" {
		return "", "", ErrSessionInvalid
	}
	var rec db.AdminSession
	err := s.db.Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", HashToken(token), time.Now()).First(&rec).Error
	if err != nil {
		return "", "", ErrSessionInvalid
	}
	var u db.AdminUser
	if err := s.db.First(&u, rec.UserID).Error; err != nil || u.Disabled {
		return "", "", ErrSessionInvalid
	}
	return u.Username, u.Role, nil
}

// Logout ends a session. It is not an error if the session is already gone.
func (s *AdminStore) Logout(token string) error {
	now := time.Now()
	return s.db.Model(&db.AdminSession{}).
		Where("token_hash = ? AND revoked_at IS NULL", HashToken(token)).
		Update("revoked_at", &now).Error
}

func (s *AdminStore) findUser(username string) (db.AdminUser, error) {
	var u db.AdminUser
	if err := s.db.Where("username = ?", username).First(&u).Error; err != nil {
		return db.AdminUser{}, ErrAdminUserNotFound
	}
	return u, nil
}

func (s *AdminStore) revokeSessions(userID uint) error {
	now := time.Now()
	return s.db.Model(&db.AdminSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", &now).Error
}
