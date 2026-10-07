package cleanup_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/cleanup"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

// loc is UTC-5, so local times and UTC differ in every assertion that involves them.
var loc = time.FixedZone("UTC-5", -5*3600)

// replyFunc is a fake model: it sees each request and returns whatever the test decides.
type replyFunc func(req llm.Request) (string, error)

func (f replyFunc) Complete(_ context.Context, req llm.Request) (string, error) { return f(req) }

func (replyFunc) Chat(context.Context, llm.ChatRequest, func(string)) (llm.Reply, error) {
	panic("cleanup never chats")
}

type fixture struct {
	svc    *service.Service
	hub    *stream.Hub
	runner *cleanup.Runner
}

func newFixture(t *testing.T, model replyFunc) *fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := func() time.Time { return time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC) } // 09:00 local
	svc := service.New(db, now)
	hub := stream.NewHub()
	s := settings.Defaults()
	s.Provider = settings.Anthropic
	s.AboutMe = "Code review drains me."
	client := func(context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
		if model == nil {
			return nil, settings.ProviderConfig{}, settings.Settings{}, settings.ErrNotConfigured
		}
		return model, s.Config(), s, nil
	}
	return &fixture{svc: svc, hub: hub, runner: cleanup.New(svc, hub, client, loc, now)}
}

func (f *fixture) capture(t *testing.T, raw string) domain.Task {
	t.Helper()
	ctx := context.Background()
	batch, err := f.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.svc.CreateTask(ctx, batch, service.TaskFields{RawText: raw})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func reply(t *testing.T, v any) replyFunc {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return func(llm.Request) (string, error) { return string(b), nil }
}

func task(fields map[string]any) map[string]any {
	base := map[string]any{"title": "", "notes": "", "size": "", "energy": "", "feeling": "",
		"due_on": "", "not_before": "", "refs": []any{}}
	for k, v := range fields {
		base[k] = v
	}
	return base
}

const flexNote = "do FLEX1-666 today, it feels difficult and needs research on pubsub first. oh i have to review FLEX1-420 as well"

var flexReply = map[string]any{
	"original": task(map[string]any{"keep": true, "title": "Do FLEX1-666", "size": "L", "energy": "high",
		"feeling": "feels difficult", "due_on": "2026-10-05", "refs": []any{map[string]any{"label": "FLEX1-666", "url": ""}}}),
	"new_tasks": []any{
		task(map[string]any{"ref": "n1", "title": "Research pubsub", "size": "M", "energy": "med"}),
		task(map[string]any{"ref": "n2", "title": "Review FLEX1-420", "size": "S"}),
	},
	"events":   []any{},
	"blockers": []any{map[string]any{"task": "original", "blocked_by": "n1"}},
}

func TestSplitsANoteIntoTasksWithABlocker(t *testing.T) {
	var sent llm.Request
	f := newFixture(t, func(req llm.Request) (string, error) {
		sent = req
		return reply(t, flexReply)(req)
	})
	existing := f.capture(t, "water the plants")
	captured := f.capture(t, flexNote)
	msgs, unsub := f.hub.Subscribe("batch.applied")
	defer unsub()
	ctx := context.Background()

	batch, err := f.runner.Clean(ctx, captured)
	if err != nil {
		t.Fatal(err)
	}

	// The request: light model, the raw note, a schema, and context the model needs.
	if sent.Model != "claude-haiku-4-5" || sent.User != flexNote || sent.Schema == nil {
		t.Errorf("request: model %q, user %q, schema set %v", sent.Model, sent.User, sent.Schema != nil)
	}
	var system strings.Builder
	for _, b := range sent.System {
		system.WriteString(b.Text)
	}
	for _, want := range []string{"Code review drains me.", "Monday 2026-10-05 09:00", "#1 water the plants"} {
		if !strings.Contains(system.String(), want) {
			t.Errorf("system prompt is missing %q", want)
		}
	}
	if strings.Contains(system.String(), "#2 ") {
		t.Error("the captured task itself was listed as an existing task")
	}

	// The result: three tasks, one blocker, all in one agent batch.
	tasks, _ := f.svc.ListTasks(ctx)
	if len(tasks) != 4 {
		t.Fatalf("got %d tasks, want 4 (existing + 3)", len(tasks))
	}
	main, research, review := tasks[1], tasks[2], tasks[3]
	if main.ID != captured.ID || main.Title != "Do FLEX1-666" || main.Feeling != "feels difficult" ||
		main.DueOn == nil || *main.DueOn != "2026-10-05" || main.RawText != flexNote {
		t.Errorf("original task: %+v", main)
	}
	if research.Title != "Research pubsub" || research.RawText != flexNote || research.CreatedBy != domain.ActorAgent {
		t.Errorf("research task: %+v", research)
	}
	if review.Title != "Review FLEX1-420" || review.Size == nil || *review.Size != domain.SizeS {
		t.Errorf("review task: %+v", review)
	}
	blockers, _ := f.svc.ListBlockers(ctx)
	if len(blockers) != 1 || blockers[0] != (domain.Blocker{TaskID: main.ID, BlockedByID: research.ID}) {
		t.Errorf("blockers = %+v", blockers)
	}
	if existing.Title != "water the plants" {
		t.Error("an unrelated task was touched")
	}

	m := <-msgs
	if applied, ok := m.Data.(stream.BatchApplied); !ok || applied.BatchID != batch || applied.Source != stream.SourceCleanup || len(applied.Activity) != 4 {
		t.Errorf("published %+v", m)
	}

	// One undo puts the card back the way it was captured.
	if err := f.svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	tasks, _ = f.svc.ListTasks(ctx)
	if len(tasks) != 2 || tasks[1].Title != captured.Title {
		t.Errorf("after undo: %+v", tasks)
	}
}

func TestEventsOnlyNoteBecomesEventsInLocalTime(t *testing.T) {
	f := newFixture(t, reply(t, map[string]any{
		"original":  task(map[string]any{"keep": false}),
		"new_tasks": []any{},
		"events": []any{
			map[string]any{"title": "Standup", "starts_at": "2026-10-05T10:00", "ends_at": "2026-10-05T10:15"},
			map[string]any{"title": "1:1", "starts_at": "2026-10-05T14:00", "ends_at": ""},
		},
		"blockers": []any{},
	}))
	captured := f.capture(t, "standup at 10, 1:1 at 2")
	ctx := context.Background()

	if _, err := f.runner.Clean(ctx, captured); err != nil {
		t.Fatal(err)
	}

	if tasks, _ := f.svc.ListTasks(ctx); len(tasks) != 0 {
		t.Errorf("the events-only card should be gone: %+v", tasks)
	}
	day := time.Date(2026, 10, 5, 0, 0, 0, 0, loc)
	events, _ := f.svc.ListEvents(ctx, day, day.AddDate(0, 0, 1))
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if want := time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC); !events[0].StartsAt.Equal(want) {
		t.Errorf("standup starts %v, want %v (10:00 at UTC-5)", events[0].StartsAt, want)
	}
	if events[0].EndsAt == nil || events[1].EndsAt != nil {
		t.Errorf("ends: %v, %v", events[0].EndsAt, events[1].EndsAt)
	}
}

func TestABadReplyChangesNothing(t *testing.T) {
	cases := map[string]map[string]any{
		"unknown blocker ref": {
			"original":  task(map[string]any{"keep": true, "title": "Do it"}),
			"new_tasks": []any{task(map[string]any{"ref": "n1", "title": "Prep"})},
			"events":    []any{},
			"blockers":  []any{map[string]any{"task": "original", "blocked_by": "n9"}},
		},
		"invalid size": {
			"original":  task(map[string]any{"keep": true, "title": "Do it"}),
			"new_tasks": []any{task(map[string]any{"ref": "n1", "title": "Prep", "size": "XL"})},
			"events":    []any{},
			"blockers":  []any{},
		},
		"blocker on a deleted original": {
			"original":  task(map[string]any{"keep": false}),
			"new_tasks": []any{task(map[string]any{"ref": "n1", "title": "Prep"})},
			"events":    []any{},
			"blockers":  []any{map[string]any{"task": "original", "blocked_by": "n1"}},
		},
		"bad event time": {
			"original":  task(map[string]any{"keep": false}),
			"new_tasks": []any{},
			"events":    []any{map[string]any{"title": "Standup", "starts_at": "10am", "ends_at": ""}},
			"blockers":  []any{},
		},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, reply(t, bad))
			captured := f.capture(t, "do it, prep first")
			ctx := context.Background()

			if _, err := f.runner.Clean(ctx, captured); err == nil {
				t.Fatal("expected an error")
			}

			tasks, _ := f.svc.ListTasks(ctx)
			if len(tasks) != 1 || tasks[0].Title != captured.Title || !tasks[0].UpdatedAt.Equal(captured.UpdatedAt) {
				t.Errorf("tasks after a failed cleanup: %+v", tasks)
			}
			if blockers, _ := f.svc.ListBlockers(ctx); len(blockers) != 0 {
				t.Errorf("blockers after a failed cleanup: %+v", blockers)
			}
		})
	}
}

func TestLeavesACardTheUserEditedMeanwhile(t *testing.T) {
	var f *fixture
	var captured domain.Task
	f = newFixture(t, func(req llm.Request) (string, error) {
		// The user retitles the card while the model is thinking.
		ctx := context.Background()
		batch, _ := f.svc.NewBatch(ctx, domain.ActorUser)
		if _, err := f.svc.UpdateTask(ctx, batch, captured.ID, service.TaskFields{Title: "my own title"}); err != nil {
			t.Error(err)
		}
		return reply(t, flexReply)(req)
	})
	captured = f.capture(t, flexNote)

	_, err := f.runner.Clean(context.Background(), captured)
	if !errors.Is(err, cleanup.ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	tasks, _ := f.svc.ListTasks(context.Background())
	if len(tasks) != 1 || tasks[0].Title != "my own title" {
		t.Errorf("cleanup overwrote the user's edit: %+v", tasks)
	}
}

func TestRunReportsFailuresButNotMissingSetup(t *testing.T) {
	cases := []struct {
		name       string
		model      replyFunc
		wantFailed bool
	}{
		{"no provider set up", nil, false},
		{"provider error", func(llm.Request) (string, error) { return "", llm.ErrRefused }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, c.model)
			captured := f.capture(t, "do the thing")
			msgs, unsub := f.hub.Subscribe("cleanup.failed")
			defer unsub()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go f.runner.Run(ctx)

			f.runner.Enqueue(captured)

			select {
			case m := <-msgs:
				failed, ok := m.Data.(cleanup.Failed)
				if !c.wantFailed || m.Type != "cleanup.failed" || !ok || failed.TaskID != captured.ID {
					t.Errorf("unexpected message %+v", m)
				}
			case <-time.After(300 * time.Millisecond):
				if c.wantFailed {
					t.Error("no cleanup.failed published")
				}
			}
			tasks, _ := f.svc.ListTasks(context.Background())
			if len(tasks) != 1 || tasks[0].Title != "do the thing" {
				t.Errorf("the raw card should be untouched: %+v", tasks)
			}
		})
	}
}

func TestAnnouncesTheWorkAroundTheModelCall(t *testing.T) {
	cases := []struct {
		name  string
		model replyFunc
		want  []string
	}{
		{"cleaned", reply(t, flexReply), []string{"work.started", "work.finished", "batch.applied"}},
		{"provider error", func(llm.Request) (string, error) { return "", llm.ErrRefused }, []string{"work.started", "work.finished"}},
		{"no provider set up", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, c.model)
			captured := f.capture(t, flexNote)
			msgs, unsub := f.hub.Subscribe()
			defer unsub()

			_, _ = f.runner.Clean(context.Background(), captured)

			var got []string
			for len(msgs) > 0 {
				m := <-msgs
				got = append(got, m.Type)
				if strings.HasPrefix(m.Type, "work.") && m.Data != (stream.Work{Kind: stream.WorkCleanup}) {
					t.Errorf("%s data = %+v", m.Type, m.Data)
				}
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("published %v, want %v", got, c.want)
			}
		})
	}
}

func TestLeavesACardEditedWhileQueued(t *testing.T) {
	f := newFixture(t, reply(t, flexReply))
	captured := f.capture(t, flexNote)
	ctx := context.Background()
	// The user retitles the card before cleanup gets to it.
	batch, _ := f.svc.NewBatch(ctx, domain.ActorUser)
	if _, err := f.svc.UpdateTask(ctx, batch, captured.ID, service.TaskFields{Title: "my own title"}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.runner.Clean(ctx, captured); !errors.Is(err, cleanup.ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	tasks, _ := f.svc.ListTasks(ctx)
	if len(tasks) != 1 || tasks[0].Title != "my own title" {
		t.Errorf("cleanup overwrote an edit made while queued: %+v", tasks)
	}
}

func TestRefusesToDeleteANoteThatHasNoEvents(t *testing.T) {
	f := newFixture(t, reply(t, map[string]any{
		"original":  task(map[string]any{"keep": false}),
		"new_tasks": []any{},
		"events":    []any{},
		"blockers":  []any{},
	}))
	captured := f.capture(t, "water the plants")

	if _, err := f.runner.Clean(context.Background(), captured); err == nil {
		t.Fatal("expected an error")
	}
	if tasks, _ := f.svc.ListTasks(context.Background()); len(tasks) != 1 {
		t.Errorf("the note vanished: %+v", tasks)
	}
}
