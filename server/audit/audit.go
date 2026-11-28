// Package audit records who changed the server, when, and from where. Entries are
// written before a change ("pending") and completed after it, so a change never happens
// without a record. Only superusers can query them, and nothing in this package deletes
// or edits an entry once it is completed.
package audit

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/OnnaSoft/lipstick/server/db"
	"gorm.io/gorm"
)

const (
	OutcomePending   = "pending"
	OutcomeSucceeded = "succeeded"
	OutcomeFailed    = "failed"
)

// Entry is an attempt about to be made.
type Entry struct {
	Actor      string
	Action     string
	Target     string
	ClientAddr string
	UserAgent  string
}

// Result completes an entry. Actor is set when the entry was written before the user was
// known, as for a login.
type Result struct {
	Actor      string
	Target     string
	StatusCode int
	Outcome    string
	Details    map[string]any
}

// Event is one stored entry, as returned by a query.
type Event struct {
	ID         uint           `json:"id"`
	OccurredAt time.Time      `json:"occurredAt"`
	Actor      string         `json:"actor"`
	Action     string         `json:"action"`
	Target     string         `json:"target"`
	Outcome    string         `json:"outcome"`
	StatusCode int            `json:"statusCode"`
	ClientAddr string         `json:"clientAddr"`
	UserAgent  string         `json:"userAgent"`
	Details    map[string]any `json:"details"`
}

// Query filters the log. Zero values mean no filter. Limit defaults to 100 and is capped at 500.
type Query struct {
	Actor   string
	Action  string
	Target  string
	Outcome string
	From    time.Time
	To      time.Time
	Limit   int
}

// Store keeps the audit log in the database.
type Store struct {
	db *gorm.DB
}

// NewStore returns a store on the given connection.
func NewStore(conn *gorm.DB) *Store {
	return &Store{db: conn}
}

// ErrNotFound is returned when an entry to complete does not exist.
var ErrNotFound = errors.New("audit entry not found")

// Begin writes a pending entry and returns its id. An error means nothing may change.
func (s *Store) Begin(e Entry) (uint, error) {
	row := db.AuditEvent{
		OccurredAt: time.Now(),
		Actor:      e.Actor,
		Action:     e.Action,
		Target:     e.Target,
		Outcome:    OutcomePending,
		ClientAddr: e.ClientAddr,
		UserAgent:  e.UserAgent,
		Details:    "{}",
	}
	if err := s.db.Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

// Finish completes a pending entry with the outcome of the change.
func (s *Store) Finish(id uint, r Result) error {
	details := "{}"
	if r.Details != nil {
		raw, err := json.Marshal(r.Details)
		if err != nil {
			return err
		}
		details = string(raw)
	}
	updates := map[string]any{
		"outcome":     r.Outcome,
		"status_code": r.StatusCode,
		"details":     details,
	}
	if r.Target != "" {
		updates["target"] = r.Target
	}
	if r.Actor != "" {
		updates["actor"] = r.Actor
	}
	res := s.db.Model(&db.AuditEvent{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Search returns entries matching q, newest first.
func (s *Store) Search(q Query) ([]Event, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	tx := s.db.Model(&db.AuditEvent{})
	if q.Actor != "" {
		tx = tx.Where("actor = ?", q.Actor)
	}
	if q.Action != "" {
		tx = tx.Where("action = ?", q.Action)
	}
	if q.Target != "" {
		tx = tx.Where("target = ?", q.Target)
	}
	if q.Outcome != "" {
		tx = tx.Where("outcome = ?", q.Outcome)
	}
	if !q.From.IsZero() {
		tx = tx.Where("occurred_at >= ?", q.From)
	}
	if !q.To.IsZero() {
		tx = tx.Where("occurred_at < ?", q.To)
	}
	var rows []db.AuditEvent
	if err := tx.Order("occurred_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		details := map[string]any{}
		_ = json.Unmarshal([]byte(r.Details), &details)
		out = append(out, Event{
			ID: r.ID, OccurredAt: r.OccurredAt, Actor: r.Actor, Action: r.Action, Target: r.Target,
			Outcome: r.Outcome, StatusCode: r.StatusCode, ClientAddr: r.ClientAddr, UserAgent: r.UserAgent,
			Details: details,
		})
	}
	return out, nil
}
