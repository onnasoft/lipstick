package manager

import (
	"time"

	"github.com/OnnaSoft/lipstick/server/db"
	"gorm.io/gorm"
)

// gormRequestStore keeps requests in PostgreSQL.
type gormRequestStore struct {
	conn *gorm.DB
}

// NewGormRequestStore returns a RequestStore backed by the request_log_entries table.
func NewGormRequestStore(conn *gorm.DB) RequestStore {
	return &gormRequestStore{conn: conn}
}

func (s *gormRequestStore) SaveBatch(records []RequestRecord) error {
	if len(records) == 0 {
		return nil
	}
	rows := make([]db.RequestLogEntry, 0, len(records))
	for _, r := range records {
		rows = append(rows, db.RequestLogEntry{
			OccurredAt:    r.Time,
			Domain:        r.Domain,
			Port:          r.Port,
			Method:        r.Method,
			Path:          r.Path,
			Status:        r.Status,
			DurationMS:    r.DurationMS,
			ResponseBytes: r.ResponseBytes,
		})
	}
	return s.conn.Create(&rows).Error
}

func (s *gormRequestStore) Query(q RequestQuery) ([]RequestRecord, error) {
	var rows []db.RequestLogEntry
	tx := s.conn.Model(&db.RequestLogEntry{})
	if q.Domain != "" {
		tx = tx.Where("domain = ?", q.Domain)
	}
	if q.Port != 0 {
		tx = tx.Where("port = ?", q.Port)
	}
	if !q.From.IsZero() {
		tx = tx.Where("occurred_at >= ?", q.From)
	}
	if !q.To.IsZero() {
		tx = tx.Where("occurred_at < ?", q.To)
	}
	if err := tx.Order("occurred_at DESC, id DESC").Limit(q.Limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]RequestRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, RequestRecord{
			Time:          r.OccurredAt,
			Domain:        r.Domain,
			Port:          r.Port,
			Method:        r.Method,
			Path:          r.Path,
			Status:        r.Status,
			DurationMS:    r.DurationMS,
			ResponseBytes: r.ResponseBytes,
		})
	}
	return out, nil
}

func (s *gormRequestStore) DeleteOlderThan(t time.Time) (int64, error) {
	res := s.conn.Where("occurred_at < ?", t).Delete(&db.RequestLogEntry{})
	return res.RowsAffected, res.Error
}
