package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
)

// EventFields are an event's editable fields. RawText is read only on create.
type EventFields struct {
	Title    string     `json:"title"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`
	RawText  string     `json:"raw_text"`
}

func (f *EventFields) normalize() error {
	f.Title = strings.TrimSpace(f.Title)
	if f.Title == "" {
		return fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if f.StartsAt.IsZero() {
		return fmt.Errorf("%w: starts_at is required", ErrInvalid)
	}
	if f.EndsAt != nil && !f.EndsAt.After(f.StartsAt) {
		return fmt.Errorf("%w: ends_at must be after starts_at", ErrInvalid)
	}
	return nil
}

func (f EventFields) applyTo(e *domain.Event) {
	e.Title = f.Title
	e.StartsAt = f.StartsAt.UTC()
	e.EndsAt = nil
	if f.EndsAt != nil {
		end := f.EndsAt.UTC()
		e.EndsAt = &end
	}
}

func (s *Service) CreateEvent(ctx context.Context, batch int64, f EventFields) (domain.Event, error) {
	if err := f.normalize(); err != nil {
		return domain.Event{}, err
	}
	var e domain.Event
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		now := s.now().UTC()
		e = domain.Event{RawText: f.RawText, CreatedAt: now, UpdatedAt: now}
		f.applyTo(&e)
		res, err := tx.ExecContext(ctx,
			`INSERT INTO events (title, starts_at, ends_at, raw_text, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			e.Title, formatTime(e.StartsAt), formatNullTime(e.EndsAt), e.RawText, formatTime(e.CreatedAt), formatTime(e.UpdatedAt))
		if err != nil {
			return err
		}
		if e.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return record(ctx, tx, batch, actionCreate, entityEvent, e.ID, nil, e)
	})
	return e, err
}

func (s *Service) UpdateEvent(ctx context.Context, batch, id int64, f EventFields) (domain.Event, error) {
	f.RawText = ""
	if err := f.normalize(); err != nil {
		return domain.Event{}, err
	}
	return s.changeEvent(ctx, batch, id, actionUpdate, f.applyTo)
}

func (s *Service) DeleteEvent(ctx context.Context, batch, id int64) error {
	_, err := s.changeEvent(ctx, batch, id, actionDelete, func(e *domain.Event) {
		deleted := s.now().UTC()
		e.DeletedAt = &deleted
	})
	return err
}

func (s *Service) changeEvent(ctx context.Context, batch, id int64, action string, change func(*domain.Event)) (domain.Event, error) {
	var e domain.Event
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		before, err := liveEvent(ctx, tx, id)
		if err != nil {
			return err
		}
		e = before
		change(&e)
		e.UpdatedAt = s.now().UTC()
		if err := writeEvent(ctx, tx, e); err != nil {
			return err
		}
		return record(ctx, tx, batch, action, entityEvent, id, before, e)
	})
	return e, err
}

func (s *Service) GetEvent(ctx context.Context, id int64) (domain.Event, error) {
	return liveEvent(ctx, s.db, id)
}

// ListEvents returns events that start in [from, to), earliest first.
func (s *Service) ListEvents(ctx context.Context, from, to time.Time) ([]domain.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM events
		 WHERE deleted_at IS NULL AND starts_at >= ? AND starts_at < ? ORDER BY starts_at`,
		formatTime(from), formatTime(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []domain.Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func liveEvent(ctx context.Context, q querier, id int64) (domain.Event, error) {
	e, err := scanEvent(q.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE id = ? AND deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, fmt.Errorf("%w: event %d", ErrNotFound, id)
	}
	return e, err
}

const eventColumns = `id, title, starts_at, ends_at, raw_text, created_at, updated_at, deleted_at`

func scanEvent(row scanner) (domain.Event, error) {
	var e domain.Event
	var startsAt, createdAt, updatedAt string
	var endsAt, deletedAt sql.NullString
	if err := row.Scan(&e.ID, &e.Title, &startsAt, &endsAt, &e.RawText, &createdAt, &updatedAt, &deletedAt); err != nil {
		return e, err
	}
	var err error
	if e.StartsAt, err = parseTime(startsAt); err != nil {
		return e, err
	}
	if e.EndsAt, err = parseNullTime(endsAt); err != nil {
		return e, err
	}
	if e.CreatedAt, err = parseTime(createdAt); err != nil {
		return e, err
	}
	if e.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return e, err
	}
	e.DeletedAt, err = parseNullTime(deletedAt)
	return e, err
}

// writeEvent overwrites every column of an existing event. Undo uses it to restore a snapshot.
func writeEvent(ctx context.Context, tx *sql.Tx, e domain.Event) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE events SET title = ?, starts_at = ?, ends_at = ?, raw_text = ?, created_at = ?, updated_at = ?,
		 deleted_at = ? WHERE id = ?`,
		e.Title, formatTime(e.StartsAt), formatNullTime(e.EndsAt), e.RawText, formatTime(e.CreatedAt),
		formatTime(e.UpdatedAt), formatNullTime(e.DeletedAt), e.ID)
	return err
}
