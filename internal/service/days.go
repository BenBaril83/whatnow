package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

const dayColumns = `id, date, pace, mood_note, started_at, wrapped_at, tomorrow_note, summary`

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
	if err := row.Scan(&d.ID, &d.Date, &d.Pace, &d.MoodNote, &startedAt, &wrappedAt, &d.TomorrowNote, &d.Summary); err != nil {
		return d, err
	}
	var err error
	if d.StartedAt, err = parseTime(startedAt); err != nil {
		return d, err
	}
	d.WrappedAt, err = parseNullTime(wrappedAt)
	return d, err
}

// writeDay writes the columns a batch can change: pace and mood. Undo uses it to restore a
// snapshot, so it must leave wrapping and the summary alone, which aren't in batches.
func writeDay(ctx context.Context, tx *sql.Tx, d domain.Day) error {
	_, err := tx.ExecContext(ctx, `UPDATE days SET pace = ?, mood_note = ? WHERE id = ?`, d.Pace, d.MoodNote, d.ID)
	return err
}

// WrapDay closes a day, with an optional note for the next one. Like starting, it isn't in a
// batch; what happens to the day's tasks is.
func (s *Service) WrapDay(ctx context.Context, dayID int64, note string) (domain.Day, error) {
	var d domain.Day
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if d, err = dayByID(ctx, tx, dayID); err != nil {
			return err
		}
		if d.WrappedAt != nil {
			return fmt.Errorf("%w: %s is already wrapped", ErrConflict, d.Date)
		}
		wrapped := s.now().UTC()
		d.WrappedAt, d.TomorrowNote = &wrapped, strings.TrimSpace(note)
		_, err = tx.ExecContext(ctx, `UPDATE days SET wrapped_at = ?, tomorrow_note = ? WHERE id = ?`,
			formatTime(wrapped), d.TomorrowNote, d.ID)
		return err
	})
	return d, err
}

func (s *Service) SetDaySummary(ctx context.Context, dayID int64, summary string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE days SET summary = ? WHERE id = ?`, summary, dayID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, fmt.Errorf("%w: day %d", ErrNotFound, dayID))
	}
	return nil
}

// LastDayBefore returns the most recent day started before date.
func (s *Service) LastDayBefore(ctx context.Context, date domain.Date) (domain.Day, error) {
	d, err := scanDay(s.db.QueryRowContext(ctx, `SELECT `+dayColumns+` FROM days WHERE date < ? ORDER BY date DESC LIMIT 1`, date))
	if errors.Is(err, sql.ErrNoRows) {
		return d, fmt.Errorf("%w: no day before %s", ErrNotFound, date)
	}
	return d, err
}

func dayByID(ctx context.Context, q querier, id int64) (domain.Day, error) {
	d, err := scanDay(q.QueryRowContext(ctx, `SELECT `+dayColumns+` FROM days WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, fmt.Errorf("%w: day %d", ErrNotFound, id)
	}
	return d, err
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

// DaysBetween returns the days started on dates in [from, to), oldest first.
func (s *Service) DaysBetween(ctx context.Context, from, to domain.Date) ([]domain.Day, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+dayColumns+` FROM days WHERE date >= ? AND date < ? ORDER BY date`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := []domain.Day{}
	for rows.Next() {
		d, err := scanDay(rows)
		if err != nil {
			return nil, err
		}
		days = append(days, d)
	}
	return days, rows.Err()
}
