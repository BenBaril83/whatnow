package planner_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

// loc is UTC-5. The clock reads 09:00 local on Monday 2026-10-05.
var loc = time.FixedZone("UTC-5", -5*3600)

const today = domain.Date("2026-10-05")

// script is a fake model. It answers each call with the next reply in order and records the
// requests. A reply that is an error is returned as one.
type script struct {
	t       *testing.T
	replies []any
	sent    []llm.Request
}

func (s *script) Complete(_ context.Context, req llm.Request) (string, error) {
	s.sent = append(s.sent, req)
	if len(s.replies) == 0 {
		s.t.Fatalf("unexpected model call %d", len(s.sent))
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	if err, ok := r.(error); ok {
		return "", err
	}
	if str, ok := r.(string); ok {
		return str, nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		s.t.Fatal(err)
	}
	return string(b), nil
}

type fixture struct {
	svc     *service.Service
	hub     *stream.Hub
	planner *planner.Planner
	model   *script
	// configured false means no provider is set up.
	configured bool
}

func newFixture(t *testing.T, replies ...any) *fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := func() time.Time { return time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC) }
	f := &fixture{svc: service.New(db, now), hub: stream.NewHub(), model: &script{t: t, replies: replies}, configured: true}
	s := settings.Defaults()
	s.Provider = settings.Anthropic
	s.Style = "dry humor"
	client := func(context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
		if !f.configured {
			return nil, settings.ProviderConfig{}, settings.Defaults(), settings.ErrNotConfigured
		}
		return f.model, s.Config(), s, nil
	}
	f.planner = planner.New(f.svc, f.hub, client, loc, now)
	return f
}

func (f *fixture) task(t *testing.T, fields service.TaskFields) domain.Task {
	t.Helper()
	ctx := context.Background()
	batch, err := f.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.svc.CreateTask(ctx, batch, fields)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (f *fixture) block(t *testing.T, waiting, first int64) {
	t.Helper()
	ctx := context.Background()
	batch, _ := f.svc.NewBatch(ctx, domain.ActorUser)
	if err := f.svc.AddBlocker(ctx, batch, domain.Blocker{TaskID: waiting, BlockedByID: first}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) start(t *testing.T, pace domain.Pace, mood string) domain.Day {
	t.Helper()
	day, err := f.svc.StartDay(context.Background(), today, pace, mood)
	if err != nil {
		t.Fatal(err)
	}
	return day
}

func system(req llm.Request) string {
	var b strings.Builder
	for _, blk := range req.System {
		b.WriteString(blk.Text)
	}
	return b.String()
}

func date(d string) *domain.Date { v := domain.Date(d); return &v }

func entry(id int64, tier domain.Tier) map[string]any {
	return map[string]any{"task_id": id, "rationale": "because", "tier": tier}
}
