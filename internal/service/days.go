package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jameynakama/whatnow/internal/domain"
)

// StartDay begins a day with the user's pace. Starting a day isn't in a batch: undoing it would
// throw away the day's plans, and nothing about it needs taking back.
func (s *Service) StartDay(ctx context.Context, date domain.Date, pace domain.Pace, moodNote string) (domain.Day, error) {
	if !date.Valid() {
		return domain.Day{}, fmt.Errorf("%w: date %q", ErrInvalid, date)
	}
	if !pace.Valid() {
		return domain.Day{}, fmt.Errorf("%w: pace %d", ErrInvalid, pace)
	}
	d := domain.Day{Date: date, Pace: pace, MoodNote: moodNote, StartedAt: s.now().UTC()}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := dayByDate(ctx, tx, date); err == nil {
			return fmt.Errorf("%w: %s has already started", ErrConflict, date)
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO days (date, pace, mood_note, started_at) VALUES (?, ?, ?, ?)`,
			d.Date, d.Pace, d.MoodNote, formatTime(d.StartedAt))
		if err != nil {
			return err
		}
		d.ID, err = res.LastInsertId()
		return err
	})
	return d, err
}

func (s *Service) GetDay(ctx context.Context, date domain.Date) (domain.Day, error) {
	return dayByDate(ctx, s.db, date)
}

// SetPace changes a started day's pace. An empty note leaves the mood note as it was.
func (s *Service) SetPace(ctx context.Context, batch int64, date domain.Date, pace domain.Pace, note string) (domain.Day, error) {
	if !pace.Valid() {
		return domain.Day{}, fmt.Errorf("%w: pace %d", ErrInvalid, pace)
	}
	var d domain.Day
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		before, err := dayByDate(ctx, tx, date)
		if err != nil {
			return err
		}
		d = before
		d.Pace = pace
		if note != "" {
			d.MoodNote = note
		}
		if err := writeDay(ctx, tx, d); err != nil {
			return err
		}
		return record(ctx, tx, batch, actionUpdate, entityDay, d.ID, before, d)
	})
	return d, err
}

const dayColumns = `id, date, pace, mood_note, started_at, wrapped_at`

func dayByDate(ctx context.Context, q querier, date domain.Date) (domain.Day, error) {
	d, err := scanDay(q.QueryRowContext(ctx, `SELECT `+dayColumns+` FROM days WHERE date = ?`, date))
	if errors.Is(err, sql.ErrNoRows) {
		return d, fmt.Errorf("%w: day %s", ErrNotFound, date)
	}
	return d, err
}

func scanDay(row scanner) (domain.Day, error) {
	var d domain.Day
	var startedAt string
	var wrappedAt sql.NullString
	if err := row.Scan(&d.ID, &d.Date, &d.Pace, &d.MoodNote, &startedAt, &wrappedAt); err != nil {
		return d, err
	}
	var err error
	if d.StartedAt, err = parseTime(startedAt); err != nil {
		return d, err
	}
	d.WrappedAt, err = parseNullTime(wrappedAt)
	return d, err
}

// writeDay overwrites a day's editable columns. Undo uses it to restore a snapshot.
func writeDay(ctx context.Context, tx *sql.Tx, d domain.Day) error {
	_, err := tx.ExecContext(ctx, `UPDATE days SET pace = ?, mood_note = ?, wrapped_at = ? WHERE id = ?`,
		d.Pace, d.MoodNote, formatNullTime(d.WrappedAt), d.ID)
	return err
}

// SavePlan stores a new version of a day's plan and returns it with its ID and version.
func (s *Service) SavePlan(ctx context.Context, p domain.Plan) (domain.Plan, error) {
	if !p.Trigger.Valid() {
		return p, fmt.Errorf("%w: trigger %q", ErrInvalid, p.Trigger)
	}
	if p.Entries == nil {
		p.Entries = []domain.PlanEntry{}
	}
	for _, e := range p.Entries {
		if !e.Tier.Valid() {
			return p, fmt.Errorf("%w: tier %q", ErrInvalid, e.Tier)
		}
	}
	entries, err := json.Marshal(p.Entries)
	if err != nil {
		return p, err
	}
	p.CreatedAt = s.now().UTC()
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM plans WHERE day_id = ?`, p.DayID).Scan(&p.Version); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO plans (day_id, version, triggered_by, reason, opening, entries, fallback, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			p.DayID, p.Version, p.Trigger, p.Reason, p.Opening, string(entries), p.Fallback, formatTime(p.CreatedAt))
		if err != nil {
			return err
		}
		p.ID, err = res.LastInsertId()
		return err
	})
	return p, err
}

// LatestPlan returns the newest version of a day's plan.
func (s *Service) LatestPlan(ctx context.Context, dayID int64) (domain.Plan, error) {
	var p domain.Plan
	var entries, createdAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, day_id, version, triggered_by, reason, opening, entries, fallback, created_at
		 FROM plans WHERE day_id = ? ORDER BY version DESC LIMIT 1`, dayID).
		Scan(&p.ID, &p.DayID, &p.Version, &p.Trigger, &p.Reason, &p.Opening, &entries, &p.Fallback, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("%w: no plan for day %d", ErrNotFound, dayID)
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(entries), &p.Entries); err != nil {
		return p, err
	}
	p.CreatedAt, err = parseTime(createdAt)
	return p, err
}
