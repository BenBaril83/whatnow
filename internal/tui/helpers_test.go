package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/llm/fake"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type fixture struct {
	app   *app.App
	clock *clock
}

// newFixture opens a temp database at 10:00 on a fixed day, with the fake model.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, func(settings.Settings) (llm.Client, settings.ProviderConfig, error) {
		return fake.Client{}, settings.ProviderConfig{}, nil
	})
}

// newFixtureWith is newFixture with its own model client.
func newFixtureWith(t *testing.T, newClient func(settings.Settings) (llm.Client, settings.ProviderConfig, error)) *fixture {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	a, err := app.Open(app.Options{
		DBPath:    filepath.Join(t.TempDir(), "test.db"),
		NewClient: newClient,
		Now:       c.now,
		Loc:       time.UTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return &fixture{app: a, clock: c}
}

func (f *fixture) task(t *testing.T, title string) domain.Task {
	t.Helper()
	ctx := context.Background()
	batch, err := f.app.Service.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.app.Service.CreateTask(ctx, batch, service.TaskFields{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// start starts today. The fake plans eligible tasks in order: a must, a should, then coulds.
func (f *fixture) start(t *testing.T) {
	t.Helper()
	if _, _, err := f.app.Planner.StartDay(context.Background(), 3, "", nil); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) load(t *testing.T) snapshot {
	t.Helper()
	s, err := load(context.Background(), f.app)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
