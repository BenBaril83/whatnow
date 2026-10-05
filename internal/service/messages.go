package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jameynakama/whatnow/internal/domain"
)

// AddMessage stores a chat message and returns it with its ID and time. Messages aren't in
// batches: undo takes back what a reply changed, never the conversation.
func (s *Service) AddMessage(ctx context.Context, m domain.Message) (domain.Message, error) {
	if m.Role != domain.RoleUser && m.Role != domain.RoleAssistant {
		return m, fmt.Errorf("%w: role %q", ErrInvalid, m.Role)
	}
	if m.Tools == nil {
		m.Tools = []domain.ToolUse{}
	}
	tools, err := json.Marshal(m.Tools)
	if err != nil {
		return m, err
	}
	m.CreatedAt = s.now().UTC()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (date, role, text, tools, batch_id, error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.Date, m.Role, m.Text, string(tools), m.BatchID, m.Error, formatTime(m.CreatedAt))
	if err != nil {
		return m, err
	}
	m.ID, err = res.LastInsertId()
	return m, err
}

// Messages returns a date's chat, oldest first.
func (s *Service) Messages(ctx context.Context, date domain.Date) ([]domain.Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, date, role, text, tools, batch_id, error, created_at FROM messages WHERE date = ? ORDER BY id`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs := []domain.Message{}
	for rows.Next() {
		var m domain.Message
		var tools, createdAt string
		if err := rows.Scan(&m.ID, &m.Date, &m.Role, &m.Text, &tools, &m.BatchID, &m.Error, &createdAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tools), &m.Tools); err != nil {
			return nil, err
		}
		if m.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// ChatFold returns a date's running chat summary, or an empty one if nothing is folded yet.
func (s *Service) ChatFold(ctx context.Context, date domain.Date) (domain.ChatFold, error) {
	var f domain.ChatFold
	err := s.db.QueryRowContext(ctx, `SELECT summary, through_id FROM chat_folds WHERE date = ?`, date).
		Scan(&f.Summary, &f.ThroughID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChatFold{}, nil
	}
	return f, err
}

func (s *Service) SetChatFold(ctx context.Context, date domain.Date, f domain.ChatFold) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_folds (date, summary, through_id) VALUES (?, ?, ?)
		 ON CONFLICT (date) DO UPDATE SET summary = excluded.summary, through_id = excluded.through_id`,
		date, f.Summary, f.ThroughID)
	return err
}
