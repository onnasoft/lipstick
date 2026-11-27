package db

import "time"

type Domain struct {
	ID                       uint   `gorm:"primary_key"`
	Name                     string `gorm:"unique;not null"`
	ApiKey                   string `gorm:"not null"`
	AllowMultipleConnections bool   `gorm:"not null"`
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
