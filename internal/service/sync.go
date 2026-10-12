package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
)

// PulledEvent is one event from an integration's feed. Key is its identity in the feed, which
// stays the same when the event moves.
type PulledEvent struct {
	Key      string
	Title    string
	StartsAt time.Time
	EndsAt   *time.Time
}

// SyncEvents makes source's events that start in [from, to) match pulled, in one batch:
//   - a pulled event with no row is created;
//   - one with a live row is updated to the feed's title and times, over any hand edit;
//   - one whose row was deleted stays deleted, since that row is the tombstone of a removal.
//
// With prune, a live row the feed no longer has is deleted, and its key cleared so it's no
// tombstone. Pass prune false when part of the feed couldn't be fetched. It returns the live
// pulled events by key.
func (s *Service) SyncEvents(ctx context.Context, batch int64, source string, from, to time.Time, pulled []PulledEvent, prune bool) (map[string]domain.Event, error) {
	live := map[string]domain.Event{}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		for _, p := range pulled {
			f := EventFields{Title: p.Title, StartsAt: p.StartsAt, EndsAt: p.EndsAt}
			if err := f.normalize(); err != nil {
				return err
			}
			row, err := scanEvent(tx.QueryRowContext(ctx,
				`SELECT `+eventColumns+` FROM events WHERE source = ? AND source_key = ?`, source, p.Key))
			switch {
			case errors.Is(err, sql.ErrNoRows):
				e := domain.Event{Source: source, SourceKey: p.Key}
				f.applyTo(&e)
				if err := s.insertEvent(ctx, tx, batch, &e); err != nil {
					return err
				}
				live[p.Key] = e
			case err != nil:
				return err
			case row.DeletedAt != nil:
			case sameTimes(row, f):
				live[p.Key] = row
			default:
				e, err := s.writeChange(ctx, tx, batch, row, actionUpdate, f.applyTo)
				if err != nil {
					return err
				}
				live[p.Key] = e
			}
		}
		if !prune {
			return nil
		}
		gone, err := sourceEvents(ctx, tx, source, from, to)
		if err != nil {
			return err
		}
		for _, e := range gone {
			if _, ok := live[e.SourceKey]; ok {
				continue
			}
			_, err := s.writeChange(ctx, tx, batch, e, actionDelete, func(e *domain.Event) {
				deleted := s.now().UTC()
				e.DeletedAt, e.SourceKey = &deleted, ""
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return live, err
}

// sameTimes says whether e already has f's title and times.
func sameTimes(e domain.Event, f EventFields) bool {
	if e.Title != f.Title || !e.StartsAt.Equal(f.StartsAt) || (e.EndsAt == nil) != (f.EndsAt == nil) {
		return false
	}
	return e.EndsAt == nil || e.EndsAt.Equal(*f.EndsAt)
}

// sourceEvents returns source's live events that start in [from, to).
func sourceEvents(ctx context.Context, tx *sql.Tx, source string, from, to time.Time) ([]domain.Event, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM events
		 WHERE source = ? AND deleted_at IS NULL AND starts_at >= ? AND starts_at < ?`,
		source, formatTime(from), formatTime(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []domain.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
