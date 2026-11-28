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
