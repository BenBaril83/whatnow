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

// Undo reverts every change in a batch, newest first. It refuses with ErrConflict when another
// batch that hasn't been undone touched the same entity afterward, so an undo never silently
// discards a newer edit. "Afterward" is by write order, not batch ID: overlapping requests can
// write in the opposite order to the one their batches were created in.
func (s *Service) Undo(ctx context.Context, batch int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		acts, err := batchActivity(ctx, tx, batch)
		if err != nil {
			return err
		}
		for _, a := range acts {
			var later int
			err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM activity a JOIN batches b ON b.id = a.batch_id
				 WHERE a.entity = ? AND a.entity_id = ? AND a.id > ? AND a.batch_id != ? AND b.undone_at IS NULL`,
				a.Entity, a.EntityID, a.ID, batch).Scan(&later)
			if err != nil {
				return err
			}
			if later > 0 {
				return fmt.Errorf("%w: %s %d changed after batch %d", ErrConflict, a.Entity, a.EntityID, batch)
			}
		}
		now := s.now().UTC()
		for i := len(acts) - 1; i >= 0; i-- {
			if err := revert(ctx, tx, acts[i], now); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE batches SET undone_at = ? WHERE id = ?`, formatTime(now), batch)
		return err
	})
}

func revert(ctx context.Context, tx *sql.Tx, a domain.Activity, now time.Time) error {
	switch a.Entity {
	case entityTask:
		if a.Before == nil {
			_, err := tx.ExecContext(ctx, `UPDATE tasks SET deleted_at = ? WHERE id = ?`, formatTime(now), a.EntityID)
			return err
		}
		var t domain.Task
		if err := json.Unmarshal(a.Before, &t); err != nil {
			return err
		}
		return writeTask(ctx, tx, t)
	case entityEvent:
		if a.Before == nil {
			_, err := tx.ExecContext(ctx, `UPDATE events SET deleted_at = ? WHERE id = ?`, formatTime(now), a.EntityID)
			return err
		}
		var e domain.Event
		if err := json.Unmarshal(a.Before, &e); err != nil {
			return err
		}
		return writeEvent(ctx, tx, e)
	case entityDay:
		var d domain.Day
		if err := json.Unmarshal(a.Before, &d); err != nil {
			return err
		}
		return writeDay(ctx, tx, d)
	case entityBlocker:
		if a.Before == nil {
			var b domain.Blocker
			if err := json.Unmarshal(a.After, &b); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM blockers WHERE task_id = ? AND blocked_by_id = ?`, b.TaskID, b.BlockedByID)
			return err
		}
		var b domain.Blocker
		if err := json.Unmarshal(a.Before, &b); err != nil {
			return err
		}
		cycle, err := waitsOn(ctx, tx, b.BlockedByID, b.TaskID)
		if err != nil {
			return err
		}
		if cycle {
			return fmt.Errorf("%w: restoring this blocker would make task %d and task %d wait on each other",
				ErrConflict, b.TaskID, b.BlockedByID)
		}
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO blockers (task_id, blocked_by_id) VALUES (?, ?)`, b.TaskID, b.BlockedByID)
		return err
	}
	return fmt.Errorf("revert: unknown entity %q", a.Entity)
}

// LastUndoableBatch returns the newest batch by actor that changed something and isn't undone,
// other than except. Pass 0 to leave none out.
func (s *Service) LastUndoableBatch(ctx context.Context, actor domain.Actor, except int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM batches
		 WHERE actor = ? AND id != ? AND undone_at IS NULL
		   AND EXISTS (SELECT 1 FROM activity WHERE activity.batch_id = batches.id)
		 ORDER BY id DESC LIMIT 1`, actor, except).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: no undoable %s batch", ErrNotFound, actor)
	}
	return id, err
}
