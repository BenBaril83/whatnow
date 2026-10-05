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

// TaskFields are a task's editable fields. RawText is read only on create: it is the user's
// original words, and edits never change it. An empty Title on create defaults to the raw text's
// first line.
type TaskFields struct {
	Title     string         `json:"title"`
	Notes     string         `json:"notes"`
	RawText   string         `json:"raw_text"`
	Size      *domain.Size   `json:"size"`
	Energy    *domain.Energy `json:"energy"`
	Feeling   string         `json:"feeling"`
	DueOn     *domain.Date   `json:"due_on"`
	NotBefore *domain.Date   `json:"not_before"`
	Refs      []domain.Ref   `json:"refs"`
}

const maxTitleRunes = 120

func (f *TaskFields) normalize() error {
	f.Title = strings.TrimSpace(f.Title)
	if f.Title == "" {
		f.Title = titleFromRaw(f.RawText)
	}
	if f.Title == "" {
		return fmt.Errorf("%w: title or raw_text is required", ErrInvalid)
	}
	if f.Size != nil && !f.Size.Valid() {
		return fmt.Errorf("%w: size %q", ErrInvalid, *f.Size)
	}
	if f.Energy != nil && !f.Energy.Valid() {
		return fmt.Errorf("%w: energy %q", ErrInvalid, *f.Energy)
	}
	if f.DueOn != nil && !f.DueOn.Valid() {
		return fmt.Errorf("%w: due_on %q", ErrInvalid, *f.DueOn)
	}
	if f.NotBefore != nil && !f.NotBefore.Valid() {
		return fmt.Errorf("%w: not_before %q", ErrInvalid, *f.NotBefore)
	}
	if f.Refs == nil {
		f.Refs = []domain.Ref{}
	}
	return nil
}

func (f TaskFields) applyTo(t *domain.Task) {
	t.Title = f.Title
	t.Notes = f.Notes
	t.Size = f.Size
	t.Energy = f.Energy
	t.Feeling = f.Feeling
	t.DueOn = f.DueOn
	t.NotBefore = f.NotBefore
	t.Refs = f.Refs
}

// titleFromRaw returns the raw text's first line, cut on a rune boundary.
func titleFromRaw(raw string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(raw), "\n")
	runes := []rune(strings.TrimSpace(line))
	if len(runes) > maxTitleRunes {
		return string(runes[:maxTitleRunes-1]) + "…"
	}
	return string(runes)
}

func (s *Service) CreateTask(ctx context.Context, batch int64, f TaskFields) (domain.Task, error) {
	if err := f.normalize(); err != nil {
		return domain.Task{}, err
	}
	var t domain.Task
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		actor, err := batchActor(ctx, tx, batch)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		t = domain.Task{RawText: f.RawText, Status: domain.StatusOpen, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
		f.applyTo(&t)
		if err := insertTask(ctx, tx, &t); err != nil {
			return err
		}
		return record(ctx, tx, batch, actionCreate, entityTask, t.ID, nil, t)
	})
	return t, err
}

func (s *Service) UpdateTask(ctx context.Context, batch, id int64, f TaskFields) (domain.Task, error) {
	f.RawText = ""
	if err := f.normalize(); err != nil {
		return domain.Task{}, err
	}
	return s.changeTask(ctx, batch, id, actionUpdate, f.applyTo)
}

func (s *Service) SetTaskStatus(ctx context.Context, batch, id int64, status domain.Status) (domain.Task, error) {
	if !status.Valid() {
		return domain.Task{}, fmt.Errorf("%w: status %q", ErrInvalid, status)
	}
	return s.changeTask(ctx, batch, id, actionStatus, func(t *domain.Task) { t.Status = status })
}

// PushTask puts a task off until a date by setting its not_before. Pushes are counted apart from
// other edits, because a task pushed again and again says something about it.
func (s *Service) PushTask(ctx context.Context, batch, id int64, until domain.Date) (domain.Task, error) {
	if !until.Valid() {
		return domain.Task{}, fmt.Errorf("%w: until %q", ErrInvalid, until)
	}
	return s.changeTask(ctx, batch, id, actionPush, func(t *domain.Task) { t.NotBefore = &until })
}

// PushCounts returns how many times each task has been pushed, leaving out undone pushes.
func (s *Service) PushCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.entity_id, COUNT(*) FROM activity a JOIN batches b ON b.id = a.batch_id
		 WHERE a.entity = ? AND a.action = ? AND b.undone_at IS NULL GROUP BY a.entity_id`, entityTask, actionPush)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		counts[id] = n
	}
	return counts, rows.Err()
}

// AppendFeeling adds a note to the end of a task's feeling.
func (s *Service) AppendFeeling(ctx context.Context, batch, id int64, note string) (domain.Task, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return domain.Task{}, fmt.Errorf("%w: note is empty", ErrInvalid)
	}
	return s.changeTask(ctx, batch, id, actionUpdate, func(t *domain.Task) {
		if t.Feeling == "" {
			t.Feeling = note
		} else {
			t.Feeling += "; " + note
		}
	})
}

// DeleteTask soft-deletes a task. Dropping (a status) means "not doing it"; deleting means
// "this shouldn't exist", and deleted tasks are hidden everywhere.
func (s *Service) DeleteTask(ctx context.Context, batch, id int64) error {
	_, err := s.changeTask(ctx, batch, id, actionDelete, func(t *domain.Task) {
		deleted := s.now().UTC()
		t.DeletedAt = &deleted
	})
	return err
}

func (s *Service) changeTask(ctx context.Context, batch, id int64, action string, change func(*domain.Task)) (domain.Task, error) {
	var t domain.Task
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		before, err := liveTask(ctx, tx, id)
		if err != nil {
			return err
		}
		t = before
		change(&t)
		t.UpdatedAt = s.now().UTC()
		if err := writeTask(ctx, tx, t); err != nil {
			return err
		}
		return record(ctx, tx, batch, action, entityTask, id, before, t)
	})
	return t, err
}

func (s *Service) GetTask(ctx context.Context, id int64) (domain.Task, error) {
	return liveTask(ctx, s.db, id)
}

// ListTasks returns every task that isn't deleted, oldest first.
func (s *Service) ListTasks(ctx context.Context) ([]domain.Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []domain.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

const taskColumns = `id, title, notes, raw_text, size, energy, feeling, due_on, not_before, status, refs,
	created_by, created_at, updated_at, deleted_at`

func liveTask(ctx context.Context, q querier, id int64) (domain.Task, error) {
	t, err := scanTask(q.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ? AND deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("%w: task %d", ErrNotFound, id)
	}
	return t, err
}

func scanTask(row scanner) (domain.Task, error) {
	var t domain.Task
	var size, energy, dueOn, notBefore, deletedAt sql.NullString
	var refs, createdAt, updatedAt string
	err := row.Scan(&t.ID, &t.Title, &t.Notes, &t.RawText, &size, &energy, &t.Feeling, &dueOn, &notBefore,
		&t.Status, &refs, &t.CreatedBy, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return t, err
	}
	t.Size = fromNull[domain.Size](size)
	t.Energy = fromNull[domain.Energy](energy)
	t.DueOn = fromNull[domain.Date](dueOn)
	t.NotBefore = fromNull[domain.Date](notBefore)
	if err := json.Unmarshal([]byte(refs), &t.Refs); err != nil {
		return t, err
	}
	if t.CreatedAt, err = parseTime(createdAt); err != nil {
		return t, err
	}
	if t.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return t, err
	}
	t.DeletedAt, err = parseNullTime(deletedAt)
	return t, err
}

func insertTask(ctx context.Context, tx *sql.Tx, t *domain.Task) error {
	refs, err := json.Marshal(t.Refs)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO tasks (title, notes, raw_text, size, energy, feeling, due_on, not_before, status, refs,
		 created_by, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Title, t.Notes, t.RawText, toNull(t.Size), toNull(t.Energy), t.Feeling, toNull(t.DueOn), toNull(t.NotBefore),
		string(t.Status), string(refs), string(t.CreatedBy), formatTime(t.CreatedAt), formatTime(t.UpdatedAt),
		formatNullTime(t.DeletedAt))
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// writeTask overwrites every column of an existing task. Undo uses it to restore a snapshot.
func writeTask(ctx context.Context, tx *sql.Tx, t domain.Task) error {
	refs, err := json.Marshal(t.Refs)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE tasks SET title = ?, notes = ?, raw_text = ?, size = ?, energy = ?, feeling = ?, due_on = ?,
		 not_before = ?, status = ?, refs = ?, created_by = ?, created_at = ?, updated_at = ?, deleted_at = ?
		 WHERE id = ?`,
		t.Title, t.Notes, t.RawText, toNull(t.Size), toNull(t.Energy), t.Feeling, toNull(t.DueOn), toNull(t.NotBefore),
		string(t.Status), string(refs), string(t.CreatedBy), formatTime(t.CreatedAt), formatTime(t.UpdatedAt),
		formatNullTime(t.DeletedAt), t.ID)
	return err
}
