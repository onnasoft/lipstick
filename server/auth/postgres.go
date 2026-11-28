package auth

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/OnnaSoft/lipstick/server/db"
	"gorm.io/gorm"
)

type PostgresAuthManager struct {
	db         *gorm.DB
	cache      sync.Map
	cacheTTL   time.Duration
	cacheMutex sync.Mutex
}

type cacheEntry struct {
	data      interface{}
	timestamp time.Time
}

func NewPostgresAuthManager() AuthManager {
	conf, err := config.GetConfig()
	if err != nil {
		log.Fatal(err)
	}
	conn, err := db.GetConnection(conf.Database)
	if err != nil {
		log.Fatal(err)
	}

	return NewAuthManagerWithDB(conn)
}

// NewAuthManagerWithDB returns an auth manager on an open connection. The schema
// must already be migrated. Production uses PostgreSQL; the tests use SQLite.
func NewAuthManagerWithDB(conn *gorm.DB) AuthManager {
	return &PostgresAuthManager{
		db: conn,
		// Short on purpose: a change made on one server of a cluster reaches the others
		// within this time. Visitor requests read the database at most this often.
		cacheTTL: 5 * time.Second,
	}
}

func (p *PostgresAuthManager) getCached(key string, fallback func() (interface{}, error)) (interface{}, error) {
	if entry, found := p.cache.Load(key); found {
		cached := entry.(cacheEntry)
		if time.Since(cached.timestamp) < p.cacheTTL {
			return cached.data, nil
		}
		p.cache.Delete(key)
	}

	p.cacheMutex.Lock()
	defer p.cacheMutex.Unlock()

	if entry, found := p.cache.Load(key); found {
		cached := entry.(cacheEntry)
		if time.Since(cached.timestamp) < p.cacheTTL {
			return cached.data, nil
		}
		p.cache.Delete(key)
	}

	data, err := fallback()
	if err == nil {
		p.cache.Store(key, cacheEntry{
			data:      data,
			timestamp: time.Now(),
		})
	}
	return data, err
}

func (p *PostgresAuthManager) GetDomains() ([]*Domain, error) {
	key := "all_domains"
	data, err := p.getCached(key, func() (interface{}, error) {
		domains := []*db.Domain{}
		if tx := p.db.Find(&domains); tx.Error != nil {
			return nil, tx.Error
		}

		result := make([]*Domain, len(domains))
		for i, domain := range domains {
			result[i] = &Domain{
				ID:                       domain.ID,
				Name:                     domain.Name,
				ApiKey:                   domain.ApiKey,
				AllowMultipleConnections: domain.AllowMultipleConnections,
				Passthrough:              domain.Passthrough,
				AuthLatched:              domain.AuthLatched,
				AllowedCIDRs:             domain.AllowedCIDRs,
				BandwidthBytesPerSecond:  domain.BandwidthBytesPerSecond,
				MonthlyQuotaBytes:        domain.MonthlyQuotaBytes,
				Disabled:                 domain.Disabled,
			}
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	return data.([]*Domain), nil
}

func (p *PostgresAuthManager) GetDomain(domain string) (*Domain, error) {
	key := "domain_" + domain
	data, err := p.getCached(key, func() (interface{}, error) {
		result := &db.Domain{}
		tx := p.db.Where("name = ?", domain).First(result)
		if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
			return nil, ErrDomainNotFound
		}
		if tx.Error != nil {
			return nil, tx.Error
		}
		return &Domain{
			ID:                       result.ID,
			Name:                     result.Name,
			ApiKey:                   result.ApiKey,
			AllowMultipleConnections: result.AllowMultipleConnections,
			Passthrough:              result.Passthrough,
			AuthLatched:              result.AuthLatched,
			AllowedCIDRs:             result.AllowedCIDRs,
			BandwidthBytesPerSecond:  result.BandwidthBytesPerSecond,
			MonthlyQuotaBytes:        result.MonthlyQuotaBytes,
			Disabled:                 result.Disabled,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return data.(*Domain), nil
}

func (p *PostgresAuthManager) AddDomain(domain *Domain) error {
	tx := p.db.Create(&db.Domain{
		Name:                     domain.Name,
		ApiKey:                   domain.ApiKey,
		AllowMultipleConnections: domain.AllowMultipleConnections,
		Passthrough:              domain.Passthrough,
		AllowedCIDRs:             domain.AllowedCIDRs,
		BandwidthBytesPerSecond:  domain.BandwidthBytesPerSecond,
		MonthlyQuotaBytes:        domain.MonthlyQuotaBytes,
	})
	if tx.Error != nil {
		return tx.Error
	}

	p.cache.Delete("all_domains")
	return nil
}

func (p *PostgresAuthManager) UpdateDomain(domain *Domain) error {
	tx := p.db.Model(&db.Domain{}).Where("id = ?", domain.ID).Updates(map[string]interface{}{
		"name":                       domain.Name,
		"api_key":                    domain.ApiKey,
		"allow_multiple_connections": domain.AllowMultipleConnections,
		"passthrough":                domain.Passthrough,
		"auth_latched":               domain.AuthLatched,
		"allowed_cidrs":              domain.AllowedCIDRs,
		"bandwidth_bytes_per_second": domain.BandwidthBytesPerSecond,
		"monthly_quota_bytes":        domain.MonthlyQuotaBytes,
	})
	if tx.Error != nil {
		return tx.Error
	}

	p.cache.Delete("domain_" + domain.Name)
	p.cache.Delete("all_domains")
	return nil
}

// DelDomain deletes a domain with the visitor users and tokens that belong to it. Their
// secrets must not outlive the domain. Its traffic history is kept, as the record of what
// it served.
func (p *PostgresAuthManager) DelDomain(id uint) error {
	result := &db.Domain{}
	tx := p.db.First(result, id)
	if tx.Error != nil {
		return tx.Error
	}

	err := p.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("domain_id = ?", id).Delete(&db.VisitorUser{}).Error; err != nil {
			return err
		}
		if err := tx.Where("domain_id = ?", id).Delete(&db.VisitorToken{}).Error; err != nil {
			return err
		}
		return tx.Delete(&db.Domain{}, id).Error
	})
	if err != nil {
		return err
	}

	p.cache.Delete("domain_" + result.Name)
	p.cache.Delete("all_domains")
	return nil
}

// SetDomainDisabled turns a domain off or on. The cache is cleared at once, so this server
// sees the change without waiting for the cache to expire.
func (p *PostgresAuthManager) SetDomainDisabled(name string, disabled bool) error {
	tx := p.db.Model(&db.Domain{}).Where("name = ?", name).Update("disabled", disabled)
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return ErrDomainNotFound
	}
	p.cache.Delete("domain_" + name)
	p.cache.Delete("all_domains")
	return nil
}
