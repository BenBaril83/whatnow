package service

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jameynakama/whatnow/internal/domain"
)

// AddBlocker records that b.TaskID waits on b.BlockedByID. It rejects any blocker that would form
// a cycle, since every task in a cycle would stay blocked forever.
func (s *Service) AddBlocker(ctx context.Context, batch int64, b domain.Blocker) error {
	if b.TaskID == b.BlockedByID {
		return fmt.Errorf("%w: a task cannot block itself", ErrInvalid)
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		for _, id := range []int64{b.TaskID, b.BlockedByID} {
			if _, err := liveTask(ctx, tx, id); err != nil {
				return err
			}
		}
		cycle, err := waitsOn(ctx, tx, b.BlockedByID, b.TaskID)
		if err != nil {
			return err
		}
		if cycle {
			return fmt.Errorf("%w: task %d already waits on task %d", ErrInvalid, b.BlockedByID, b.TaskID)
		}
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO blockers (task_id, blocked_by_id) VALUES (?, ?)`,
			b.TaskID, b.BlockedByID)
		if err != nil {
			return err
		}
		added, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if added == 0 {
			return nil
		}
		return record(ctx, tx, batch, actionAdd, entityBlocker, b.TaskID, nil, b)
	})
}

func (s *Service) RemoveBlocker(ctx context.Context, batch int64, b domain.Blocker) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM blockers WHERE task_id = ? AND blocked_by_id = ?`,
			b.TaskID, b.BlockedByID)
		if err != nil {
			return err
		}
		removed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if removed == 0 {
			return fmt.Errorf("%w: task %d is not blocked by task %d", ErrNotFound, b.TaskID, b.BlockedByID)
		}
		return record(ctx, tx, batch, actionRemove, entityBlocker, b.TaskID, b, nil)
	})
}

func (s *Service) ListBlockers(ctx context.Context) ([]domain.Blocker, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT b.task_id, b.blocked_by_id FROM blockers b
		 JOIN tasks t ON t.id = b.task_id AND t.deleted_at IS NULL
		 JOIN tasks blocking ON blocking.id = b.blocked_by_id AND blocking.deleted_at IS NULL
		 ORDER BY b.task_id, b.blocked_by_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blockers := []domain.Blocker{}
	for rows.Next() {
		var b domain.Blocker
		if err := rows.Scan(&b.TaskID, &b.BlockedByID); err != nil {
			return nil, err
		}
		blockers = append(blockers, b)
	}
	return blockers, rows.Err()
}

// waitsOn reports whether task from already waits on task to, directly or through other tasks.
func waitsOn(ctx context.Context, tx *sql.Tx, from, to int64) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT task_id, blocked_by_id FROM blockers`)
	if err != nil {
		return false, err
	}
	edges := map[int64][]int64{}
	for rows.Next() {
		var task, by int64
		if err := rows.Scan(&task, &by); err != nil {
			rows.Close()
			return false, err
		}
		edges[task] = append(edges[task], by)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	seen := map[int64]bool{}
	stack := []int64{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == to {
			return true, nil
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		stack = append(stack, edges[n]...)
	}
	return false, nil
}
