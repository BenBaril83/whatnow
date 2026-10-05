package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestCreateTaskFromRawText(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	raw := "  do FLEX1-666 today\nit feels difficult, needs pubsub research first"

	got, err := svc.CreateTask(ctx, newBatch(t, svc, domain.ActorAgent), service.TaskFields{RawText: raw})
	if err != nil {
		t.Fatal(err)
	}

	if got.Title != "do FLEX1-666 today" {
		t.Errorf("title = %q, want the raw text's first line", got.Title)
	}
	if got.RawText != raw {
		t.Errorf("raw text not kept verbatim: %q", got.RawText)
	}
	if got.Status != domain.StatusOpen || got.CreatedBy != domain.ActorAgent {
		t.Errorf("status %q, created_by %q; want open, agent", got.Status, got.CreatedBy)
	}

	listed, err := svc.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]domain.Task{got}, listed); diff != "" {
		t.Errorf("stored task differs from returned task (-returned +stored):\n%s", diff)
	}
}

func TestTitleFromLongMultibyteRawText(t *testing.T) {
	svc, _ := newService(t)
	raw := strings.Repeat("🔥", 200)

	got, err := svc.CreateTask(context.Background(), newBatch(t, svc, domain.ActorUser), service.TaskFields{RawText: raw})
	if err != nil {
		t.Fatal(err)
	}

	if !utf8.ValidString(got.Title) {
		t.Fatalf("title is not valid UTF-8: %q", got.Title)
	}
	if n := utf8.RuneCountInString(got.Title); n != 120 {
		t.Errorf("title is %d runes, want 120", n)
	}
	if !strings.HasSuffix(got.Title, "…") {
		t.Errorf("truncated title should end with an ellipsis: %q", got.Title)
	}
}

func TestCreateTaskRejectsInvalidFields(t *testing.T) {
	cases := map[string]service.TaskFields{
		"no title or raw text": {Title: "   "},
		"bad size":             {Title: "x", Size: ptr(domain.Size("XL"))},
		"bad energy":           {Title: "x", Energy: ptr(domain.Energy("frantic"))},
		"bad due_on":           {Title: "x", DueOn: ptr(domain.Date("tomorrow"))},
		"bad not_before":       {Title: "x", NotBefore: ptr(domain.Date("2026-13-01"))},
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _ := newService(t)
			_, err := svc.CreateTask(context.Background(), newBatch(t, svc, domain.ActorUser), fields)
			if !errors.Is(err, service.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCreateTaskUnknownBatch(t *testing.T) {
	svc, _ := newService(t)
	_, err := svc.CreateTask(context.Background(), 999, service.TaskFields{Title: "x"})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateTaskKeepsRawTextAndRecordsActivity(t *testing.T) {
	svc, clock := newService(t)
	ctx := context.Background()
	created, err := svc.CreateTask(ctx, newBatch(t, svc, domain.ActorUser), service.TaskFields{RawText: "review 420"})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Hour)

	batch := newBatch(t, svc, domain.ActorAgent)
	updated, err := svc.UpdateTask(ctx, batch, created.ID, service.TaskFields{
		Title:   "Review FLEX1-420",
		Size:    ptr(domain.SizeS),
		DueOn:   ptr(domain.Date("2026-10-05")),
		Refs:    []domain.Ref{{Label: "FLEX1-420"}},
		RawText: "this should be ignored",
	})
	if err != nil {
		t.Fatal(err)
	}

	if updated.RawText != "review 420" {
		t.Errorf("raw text changed on update: %q", updated.RawText)
	}
	if !updated.UpdatedAt.Equal(clock.now()) {
		t.Errorf("updated_at = %v, want %v", updated.UpdatedAt, clock.now())
	}

	acts, err := svc.BatchActivity(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 {
		t.Fatalf("got %d activity rows, want 1", len(acts))
	}
	var before, after domain.Task
	if err := json.Unmarshal(acts[0].Before, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(acts[0].After, &after); err != nil {
		t.Fatal(err)
	}
	if before.Title != "review 420" || after.Title != "Review FLEX1-420" {
		t.Errorf("snapshots: before %q, after %q", before.Title, after.Title)
	}
}

func TestStatusChangesAndRestore(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "write the doc")

	dropped, err := svc.SetTaskStatus(ctx, newBatch(t, svc, domain.ActorUser), task.ID, domain.StatusDropped)
	if err != nil {
		t.Fatal(err)
	}
	if dropped.Status != domain.StatusDropped {
		t.Fatalf("status = %q, want dropped", dropped.Status)
	}
	restored, err := svc.SetTaskStatus(ctx, newBatch(t, svc, domain.ActorUser), task.ID, domain.StatusOpen)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != domain.StatusOpen {
		t.Errorf("status = %q, want open", restored.Status)
	}

	_, err = svc.SetTaskStatus(ctx, newBatch(t, svc, domain.ActorUser), task.ID, domain.Status("paused"))
	if !errors.Is(err, service.ErrInvalid) {
		t.Errorf("bad status: err = %v, want ErrInvalid", err)
	}
}

func TestDeletedTaskIsGone(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "temp")

	if err := svc.DeleteTask(ctx, newBatch(t, svc, domain.ActorUser), task.ID); err != nil {
		t.Fatal(err)
	}

	tasks, err := svc.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("deleted task still listed: %+v", tasks)
	}
	if _, err := svc.GetTask(ctx, task.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("GetTask err = %v, want ErrNotFound", err)
	}
	_, err = svc.UpdateTask(ctx, newBatch(t, svc, domain.ActorUser), task.ID, service.TaskFields{Title: "back?"})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("UpdateTask err = %v, want ErrNotFound", err)
	}
}

func TestListTasksEmptyIsNotNil(t *testing.T) {
	svc, _ := newService(t)
	tasks, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tasks == nil {
		t.Error("ListTasks returned nil; the API would encode it as null")
	}
}
