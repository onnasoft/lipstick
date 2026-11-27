package auth

import (
	"errors"
	"sync"
)

// ErrDomainNotFound is returned when no domain has the requested name.
var ErrDomainNotFound = errors.New("domain not found")

type Domain struct {
	ID                       uint   `json:"id"`
	Name                     string `json:"name"`
	ApiKey                   string `json:"apiKey"`
	AllowMultipleConnections bool   `json:"allowMultipleConnections"`
	Passthrough              bool   `json:"passthrough"`
}

type AuthManager interface {
	GetDomains() ([]*Domain, error)
	GetDomain(domain string) (*Domain, error)
	AddDomain(domain *Domain) error
	UpdateDomain(domain *Domain) error
	DelDomain(id uint) error
}

var (
	sharedOnce    sync.Once
	sharedManager AuthManager
)

// MakeAuthManager returns the one auth manager of the process. The admin API and
// the proxy must share it: each keeps a cache of domains, and an update made
// through one would otherwise leave the other serving the old value until the
// cache expired. This is the bug that kept passthrough on after it was turned off.
func MakeAuthManager() AuthManager {
	sharedOnce.Do(func() {
		sharedManager = NewPostgresAuthManager()
	})
	return sharedManager
}
