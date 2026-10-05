package planner_test

import (
	"context"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

// run starts the trigger watcher and returns a subscription that sees the plans it publishes.
func (f *fixture) run(t *testing.T) <-chan stream.Message {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	watched, unwatch := f.hub.Subscribe()
	plans, unsub := f.hub.Subscribe()
	t.Cleanup(func() { cancel(); unwatch(); unsub() })
	go f.planner.Run(ctx, watched)
	return plans
}

// write applies a change in a new batch and announces it, as the API does.
func (f *fixture) write(t *testing.T, change func(batch int64) error) int64 {
	t.Helper()
	ctx := context.Background()
	batch, err := f.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := change(batch); err != nil {
		t.Fatal(err)
	}
	acts, err := f.svc.BatchActivity(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	f.hub.PublishBatch(batch, acts)
	return batch
}

func nextPlan(t *testing.T, msgs <-chan stream.Message) domain.Plan {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case m := <-msgs:
			if p, ok := m.Data.(domain.Plan); ok {
				return p
			}
		case <-timeout:
			t.Fatal("no plan.updated")
		}
	}
}

func TestEditsThatMatterTriggerAReplan(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	plants := f.task(t, service.TaskFields{Title: "water plants"})
	rfc := f.task(t, service.TaskFields{Title: "write the RFC"})
	f.start(t, 3, "")
	if _, err := f.planner.Replan(ctx, domain.TriggerMorning, ""); err != nil {
		t.Fatal(err)
	}
	plans := f.run(t)

	// An edit that changes nothing about the plan is ignored; the next plan is for the due date.
	f.write(t, func(b int64) error {
		_, err := f.svc.UpdateTask(ctx, b, plants.ID, service.TaskFields{Title: "water the plants"})
		return err
	})
	f.write(t, func(b int64) error {
		_, err := f.svc.UpdateTask(ctx, b, rfc.ID, service.TaskFields{Title: "write the RFC", DueOn: date("2026-10-05")})
		return err
	})
	plan := nextPlan(t, plans)
	if plan.Version != 2 || plan.Trigger != domain.TriggerEdit || plan.Reason != `#2 "write the RFC" is due now` {
		t.Errorf("plan = %+v", plan)
	}
}

func TestPaceChangesAndTheirUndoTriggerAReplan(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	f.start(t, 3, "")
	if _, err := f.planner.Replan(ctx, domain.TriggerMorning, ""); err != nil {
		t.Fatal(err)
	}
	plans := f.run(t)

	batch := f.write(t, func(b int64) error {
		_, err := f.svc.SetPace(ctx, b, today, 1, "")
		return err
	})
	if plan := nextPlan(t, plans); plan.Trigger != domain.TriggerPace || plan.Reason != `pace changed to "running on fumes"` {
		t.Errorf("after the change: %+v", plan)
	}

	if err := f.svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	f.hub.PublishUndone(batch)
	if plan := nextPlan(t, plans); plan.Trigger != domain.TriggerPace || plan.Reason != `pace changed to "steady"` || plan.Version != 3 {
		t.Errorf("after the undo: %+v", plan)
	}
}

func TestNoReplanBeforeTheMorningPlan(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	rfc := f.task(t, service.TaskFields{Title: "write the RFC"})
	f.start(t, 3, "")
	plans := f.run(t)

	// The day has started but has no plan yet: the morning makes the first one, not an edit.
	f.write(t, func(b int64) error {
		_, err := f.svc.UpdateTask(ctx, b, rfc.ID, service.TaskFields{Title: "write the RFC", DueOn: date("2026-10-05")})
		return err
	})
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case m := <-plans:
			if p, ok := m.Data.(domain.Plan); ok {
				t.Fatalf("unexpected plan: %+v", p)
			}
		case <-timeout:
			return
		}
	}
}
