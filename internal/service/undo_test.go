package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestUndoRevertsAWholeBatch(t *testing.T) {
	svc, clock := newService(t)
	ctx := context.Background()
	existing := createTask(t, svc, "do FLEX1-666")
	gone := createTask(t, svc, "duplicate")
	start := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	event, err := svc.CreateEvent(ctx, newBatch(t, svc, domain.ActorUser), service.EventFields{Title: "1:1", StartsAt: start})
	if err != nil {
		t.Fatal(err)
	}
	tasksBefore, _ := svc.ListTasks(ctx)
	clock.advance(time.Minute)

	// The kind of batch cleanup produces: rename, add, block, delete, and an event change.
	batch := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.UpdateTask(ctx, batch, existing.ID, service.TaskFields{Title: "Do FLEX1-666", Size: ptr(domain.SizeL)}); err != nil {
		t.Fatal(err)
	}
	research, err := svc.CreateTask(ctx, batch, service.TaskFields{Title: "research pubsub"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddBlocker(ctx, batch, domain.Blocker{TaskID: existing.ID, BlockedByID: research.ID}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteTask(ctx, batch, gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteEvent(ctx, batch, event.ID); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}

	tasksAfter, err := svc.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(tasksBefore, tasksAfter); diff != "" {
		t.Errorf("tasks not restored exactly (-before +after undo):\n%s", diff)
	}
	blockers, _ := svc.ListBlockers(ctx)
	if len(blockers) != 0 {
		t.Errorf("blocker survived undo: %+v", blockers)
	}
	events, _ := svc.ListEvents(ctx, start.Add(-time.Hour), start.Add(time.Hour))
	if len(events) != 1 {
		t.Errorf("deleted event not restored: %+v", events)
	}
}

func TestUndoRestoresARemovedBlocker(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	a, b := createTask(t, svc, "a"), createTask(t, svc, "b")
	blocker := domain.Blocker{TaskID: a.ID, BlockedByID: b.ID}
	if err := svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorUser), blocker); err != nil {
		t.Fatal(err)
	}
	batch := newBatch(t, svc, domain.ActorUser)
	if err := svc.RemoveBlocker(ctx, batch, blocker); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.ListBlockers(ctx)
	if diff := cmp.Diff([]domain.Blocker{blocker}, got); diff != "" {
		t.Errorf("blockers (-want +got):\n%s", diff)
	}
}

func TestUndoRefusesWhenALaterBatchTouchedTheSameTask(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "original")
	agentBatch := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.UpdateTask(ctx, agentBatch, task.ID, service.TaskFields{Title: "agent edit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateTask(ctx, newBatch(t, svc, domain.ActorUser), task.ID, service.TaskFields{Title: "user edit"}); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, agentBatch); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	got, err := svc.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "user edit" {
		t.Errorf("refused undo still changed the task: title %q", got.Title)
	}
}

func TestUndoAllowedOnceTheLaterBatchIsUndone(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "original")
	first := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.UpdateTask(ctx, first, task.ID, service.TaskFields{Title: "first"}); err != nil {
		t.Fatal(err)
	}
	second := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.UpdateTask(ctx, second, task.ID, service.TaskFields{Title: "second"}); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, first); err != nil {
		t.Fatalf("undo first after second: %v", err)
	}
	got, _ := svc.GetTask(ctx, task.ID)
	if got.Title != "original" {
		t.Errorf("title = %q, want original", got.Title)
	}
}

func TestUndoneBatchIsClosed(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.CreateTask(ctx, batch, service.TaskFields{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, batch); !errors.Is(err, service.ErrConflict) {
		t.Errorf("second undo: err = %v, want ErrConflict", err)
	}
	if _, err := svc.CreateTask(ctx, batch, service.TaskFields{Title: "y"}); !errors.Is(err, service.ErrConflict) {
		t.Errorf("write to undone batch: err = %v, want ErrConflict", err)
	}
	if err := svc.Undo(ctx, 999); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown batch: err = %v, want ErrNotFound", err)
	}
}

func TestLastUndoableBatch(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.LastUndoableBatch(ctx, domain.ActorAgent); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("no batches: err = %v, want ErrNotFound", err)
	}

	older := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.CreateTask(ctx, older, service.TaskFields{Title: "a"}); err != nil {
		t.Fatal(err)
	}
	undone := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.CreateTask(ctx, undone, service.TaskFields{Title: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, undone); err != nil {
		t.Fatal(err)
	}
	newBatch(t, svc, domain.ActorAgent) // empty: nothing to undo
	createTask(t, svc, "user's own change")

	got, err := svc.LastUndoableBatch(ctx, domain.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	if got != older {
		t.Errorf("got batch %d, want %d", got, older)
	}
}
