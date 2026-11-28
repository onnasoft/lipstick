package db

import "time"

type Domain struct {
	ID                       uint   `gorm:"primary_key"`
	Name                     string `gorm:"unique;not null"`
	ApiKey                   string `gorm:"not null"`
	AllowMultipleConnections bool   `gorm:"not null"`
	// Passthrough makes the proxy forward this domain's TLS without ending it. The
	// service behind the client must hold a certificate for the domain.
	Passthrough bool `gorm:"not null;default:false"`
	// AuthLatched is set when a client asks for visitor authentication. Once set,
	// the domain stays restricted until an admin clears it.
	AuthLatched bool `gorm:"not null;default:false"`
	// AllowedCIDRs lists the networks visitors may come from, comma-separated. Empty
	// means no restriction.
	AllowedCIDRs string `gorm:"column:allowed_cidrs;not null;default:''"`
	// BandwidthBytesPerSecond paces the data of this domain's visitors. 0 uses the server default.
	BandwidthBytesPerSecond int64 `gorm:"not null;default:0"`
	// MonthlyQuotaBytes caps the traffic of the domain in a calendar month. 0 uses the server default.
	MonthlyQuotaBytes int64 `gorm:"not null;default:0"`
	// Disabled turns the domain off: its client cannot connect and its visitors get 503.
	// Its settings and its history stay; enabling it again brings it back.
	Disabled bool `gorm:"not null;default:false"`
}

// VisitorUser is a user of a restricted domain. Each user has a secret of its own,
// which the server needs to verify the user's signed requests, so it is stored.
// The secret is shown once, when the user is created or rotated.
type VisitorUser struct {
	ID        uint   `gorm:"primary_key"`
	DomainID  uint   `gorm:"not null;index:idx_visitor_user,unique,priority:1"`
	Username  string `gorm:"not null;index:idx_visitor_user,unique,priority:2"`
	Secret    string `gorm:"not null;default:''"`
	CreatedAt time.Time
}

// VisitorToken is an API token for a restricted domain. Only a hash of the token
// is stored. Methods and PathPrefixes are comma-separated lists; "*" in Methods
// allows every method.
type VisitorToken struct {
	ID           uint   `gorm:"primary_key"`
	DomainID     uint   `gorm:"not null;index"`
	Name         string `gorm:"not null"`
	TokenHash    string `gorm:"not null;unique"`
	Methods      string `gorm:"not null"`
	PathPrefixes string `gorm:"not null"`
	CreatedAt    time.Time
	RevokedAt    *time.Time
}

type DailyConsumption struct {
	ID        uint      `gorm:"primary_key"`
	Domain    string    `gorm:"not null;index"`
	Date      time.Time `gorm:"type:date;not null"`
	Month     string    `gorm:"type:varchar(7);not null;index"`
	BytesUsed int64     `gorm:"not null;default:0"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RequestLogEntry is one finished HTTP request, kept for the admin panel. It has
// no headers and no bodies.
type RequestLogEntry struct {
	ID            uint      `gorm:"primary_key"`
	OccurredAt    time.Time `gorm:"column:occurred_at;not null;index;index:idx_request_domain_time,priority:2"`
	Domain        string    `gorm:"not null;index:idx_request_domain_time,priority:1"`
	Port          int       `gorm:"not null;default:0;index"` // server port the visitor came in on; 0 when unknown
	Method        string    `gorm:"type:varchar(16);not null"`
	Path          string    `gorm:"type:varchar(512);not null"`
	Status        int       `gorm:"not null"`
	DurationMS    int64     `gorm:"not null"`
	ResponseBytes int64     `gorm:"not null"`
}

// AdminUser is an account that can change the server through the admin API. Accounts
// are created and reset from the console on the server machine. Passwords are stored
// as argon2id hashes.
type AdminUser struct {
	ID           uint   `gorm:"primary_key"`
	Username     string `gorm:"not null;uniqueIndex"`
	PasswordHash string `gorm:"not null"`
	Role         string `gorm:"not null;default:'superuser'"`
	Disabled     bool   `gorm:"not null;default:false"`
	CreatedAt    time.Time
}

// AdminSession is a login token of an admin user. Only its SHA-256 hash is stored.
type AdminSession struct {
	ID        uint      `gorm:"primary_key"`
	UserID    uint      `gorm:"not null;index"`
	TokenHash string    `gorm:"not null;uniqueIndex"`
	ExpiresAt time.Time `gorm:"not null;index"`
	RevokedAt *time.Time
	CreatedAt time.Time
}

// NodeStatus is the state one server reports for one domain, every few seconds. The
// rows of all servers in a cluster together give the state of the cluster, so any
// server's admin API answers the same. A row older than the freshness window belongs
// to a server that stopped reporting and is ignored.
type NodeStatus struct {
	NodeID          string    `gorm:"primaryKey;size:191"`
	Domain          string    `gorm:"primaryKey;size:255"`
	Status          string    `gorm:"not null;size:16"`
	Clients         int       `gorm:"not null;default:0"`
	ActiveSessions  int64     `gorm:"not null;default:0"`
	PendingVisitors int       `gorm:"not null;default:0"`
	BytesSinceStart int64     `gorm:"not null;default:0"`
	IntranetPorts   string    `gorm:"not null;default:''"`
	UpdatedAt       time.Time `gorm:"not null;index"`
}

// AuditEvent records one attempt to change the server, or to log in. A row is written
// as "pending" before the change and updated to "succeeded" or "failed" after it, so an
// attempt without an outcome shows that the process stopped in the middle. Details hold
// the names of the fields changed, never their values.
type AuditEvent struct {
	ID         uint      `gorm:"primaryKey"`
	OccurredAt time.Time `gorm:"not null;index"`
	Actor      string    `gorm:"not null;size:64;index"`
	Action     string    `gorm:"not null;size:64;index"`
	Target     string    `gorm:"not null;size:255;index"`
	Outcome    string    `gorm:"not null;size:16;index"`
	StatusCode int       `gorm:"not null;default:0"`
	ClientAddr string    `gorm:"not null;size:64"`
	UserAgent  string    `gorm:"not null;size:255"`
	Details    string    `gorm:"not null;default:'{}'"`
}
