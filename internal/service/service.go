// Package service is the only write path for tasks, blockers, events, and days. Every write belongs to a
// batch and records an activity row with before and after snapshots, which is what undo replays.
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
)

var (
	ErrInvalid  = errors.New("invalid")
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

const (
	entityTask    = "task"
	entityBlocker = "blocker"
	entityEvent   = "event"
	entityDay     = "day"

	actionCreate = "create"
	actionUpdate = "update"
	actionStatus = "status"
	actionDelete = "delete"
	actionAdd    = "add"
	actionRemove = "remove"
	actionPush   = "push"
)

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB, now func() time.Time) *Service {
	return &Service{db: db, now: now}
}

// NewBatch starts a batch. Every write names one, and undo reverts a whole batch at once.
func (s *Service) NewBatch(ctx context.Context, actor domain.Actor) (int64, error) {
	if !actor.Valid() {
		return 0, fmt.Errorf("%w: actor %q", ErrInvalid, actor)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO batches (actor, created_at) VALUES (?, ?)`, actor, formatTime(s.now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// BatchActivity returns a batch's changes in the order they happened.
func (s *Service) BatchActivity(ctx context.Context, batch int64) ([]domain.Activity, error) {
	return batchActivity(ctx, s.db, batch)
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type scanner interface {
	Scan(dest ...any) error
}

func (s *Service) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// batchActor returns the actor of a batch that can still take writes.
func batchActor(ctx context.Context, tx *sql.Tx, batch int64) (domain.Actor, error) {
	var actor domain.Actor
	var undone sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT actor, undone_at FROM batches WHERE id = ?`, batch).Scan(&actor, &undone)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: batch %d", ErrNotFound, batch)
	}
	if err != nil {
		return "", err
	}
	if undone.Valid {
		return "", fmt.Errorf("%w: batch %d was undone", ErrConflict, batch)
	}
	return actor, nil
}

// record writes one activity row. Pass a nil before for a creation and a nil after for a removal.
func record(ctx context.Context, tx *sql.Tx, batch int64, action, entity string, entityID int64, before, after any) error {
	b, err := snapshot(before)
	if err != nil {
		return err
	}
	a, err := snapshot(after)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO activity (batch_id, action, entity, entity_id, before_json, after_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, (SELECT created_at FROM batches WHERE id = ?))`,
		batch, action, entity, entityID, b, a, batch)
	return err
}

func snapshot(v any) (sql.NullString, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func batchActivity(ctx context.Context, q querier, batch int64) ([]domain.Activity, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, batch_id, action, entity, entity_id, before_json, after_json, created_at
		 FROM activity WHERE batch_id = ? ORDER BY id`, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acts := []domain.Activity{}
	for rows.Next() {
		var a domain.Activity
		var before, after sql.NullString
		var createdAt string
		if err := rows.Scan(&a.ID, &a.BatchID, &a.Action, &a.Entity, &a.EntityID, &before, &after, &createdAt); err != nil {
			return nil, err
		}
		if before.Valid {
			a.Before = json.RawMessage(before.String)
		}
		if after.Valid {
			a.After = json.RawMessage(after.String)
		}
		if a.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		acts = append(acts, a)
	}
	return acts, rows.Err()
}

// timeLayout is fixed width, so stored timestamps sort correctly as strings.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }

func formatNullTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*t), Valid: true}
}

func parseNullTime(n sql.NullString) (*time.Time, error) {
	if !n.Valid {
		return nil, nil
	}
	t, err := parseTime(n.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// nullString stores an empty string as NULL.
func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func toNull[T ~string](p *T) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*p), Valid: true}
}

func fromNull[T ~string](n sql.NullString) *T {
	if !n.Valid {
		return nil
	}
	v := T(n.String)
	return &v
}
