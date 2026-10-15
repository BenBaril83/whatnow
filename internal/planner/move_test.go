package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
)

// planned starts today with a in should and b in could, leaving c unplanned.
func planned(t *testing.T) (f *fixture, a, b, c domain.Task) {
	t.Helper()
	f = newFixture(t)
	a = f.task(t, service.TaskFields{Title: "a"})
	b = f.task(t, service.TaskFields{Title: "b"})
	c = f.task(t, service.TaskFields{Title: "c"})
	f.model.replies = []any{map[string]any{
		"entries": []any{entry(a.ID, domain.TierShould), entry(b.ID, domain.TierCould)}, "opening": "hi"}}
	f.start(t, 3, "")
	if _, err := f.planner.Replan(context.Background(), domain.TriggerMorning, ""); err != nil {
		t.Fatal(err)
	}
	return f, a, b, c
}

func TestMoveSavesAPlanWithTheTaskInItsNewTier(t *testing.T) {
	f, a, b, c := planned(t)
	ctx := context.Background()

	plan, err := f.planner.Move(ctx, b.ID, domain.TierMust)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != 2 || plan.Trigger != domain.TriggerMove || plan.Reason != `#2 "b" from could to must` || plan.Opening != "hi" {
		t.Errorf("plan = %+v", plan)
	}
	want := []domain.PlanEntry{
		{TaskID: a.ID, Tier: domain.TierShould, Rationale: "because"},
		{TaskID: b.ID, Tier: domain.TierMust, Rationale: "You moved this here."},
	}
	if diff := cmp.Diff(want, plan.Entries); diff != "" {
		t.Errorf("entries (-want +got):\n%s", diff)
	}

	if plan, err = f.planner.Move(ctx, a.ID, ""); err != nil {
		t.Fatal(err)
	}
	if plan.Reason != `#1 "a" from should to unplanned` || len(plan.Entries) != 1 || plan.Entries[0].TaskID != b.ID {
		t.Errorf("moved to unplanned: %+v", plan)
	}
	if plan, err = f.planner.Move(ctx, c.ID, domain.TierCould); err != nil {
		t.Fatal(err)
	}
	if plan.Reason != `#3 "c" from unplanned to could` || len(plan.Entries) != 2 || plan.Entries[1].TaskID != c.ID {
		t.Errorf("moved into the plan: %+v", plan)
	}
}

func TestMoveAnnouncesThePlan(t *testing.T) {
	f, _, b, _ := planned(t)
	sub, cancel := f.hub.Subscribe("plan.updated")
	defer cancel()

	plan, err := f.planner.Move(context.Background(), b.ID, domain.TierMust)
	if err != nil {
		t.Fatal(err)
	}
	if m := <-sub; m.Type != "plan.updated" || m.Data.(domain.Plan).ID != plan.ID {
		t.Errorf("message = %+v", m)
	}
}

func TestReplanSeesWhatWasMovedByHand(t *testing.T) {
	f, a, b, _ := planned(t)
	ctx := context.Background()
	if _, err := f.planner.Move(ctx, b.ID, domain.TierMust); err != nil {
		t.Fatal(err)
	}
	if _, err := f.planner.Move(ctx, a.ID, ""); err != nil {
		t.Fatal(err)
	}
	f.model.replies = []any{map[string]any{"entries": []any{entry(b.ID, domain.TierMust)}, "opening": "ok"}}

	if _, err := f.planner.Replan(ctx, domain.TriggerEdit, "x"); err != nil {
		t.Fatal(err)
	}
	want := "Moved by hand today, oldest first:\n- #2 \"b\" from could to must\n- #1 \"a\" from should to unplanned\n"
	if sys := system(f.model.sent[1]); !strings.Contains(sys, want) {
		t.Errorf("system prompt is missing %q", want)
	}
}

func TestMoveRejects(t *testing.T) {
	ctx := context.Background()
	if _, err := newFixture(t).planner.Move(ctx, 1, domain.TierMust); !errors.Is(err, planner.ErrNoDay) {
		t.Errorf("no day: err = %v, want ErrNoDay", err)
	}

	f, a, _, _ := planned(t)
	if _, err := f.planner.Move(ctx, a.ID, "later"); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("bad tier: err = %v, want ErrInvalid", err)
	}
	if _, err := f.planner.Move(ctx, 99, domain.TierMust); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("unknown task: err = %v, want ErrInvalid", err)
	}
	day, _ := f.svc.GetDay(ctx, today)
	if _, err := f.svc.WrapDay(ctx, day.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.planner.Move(ctx, a.ID, domain.TierMust); !errors.Is(err, service.ErrConflict) {
		t.Errorf("wrapped: err = %v, want ErrConflict", err)
	}
}
