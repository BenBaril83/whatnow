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

func TestStartDayOncePerDate(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	day, err := svc.StartDay(ctx, "2026-10-05", 2, "slept badly")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetDay(ctx, "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(day, got); diff != "" {
		t.Errorf("stored day differs (-started +got):\n%s", diff)
	}

	if _, err := svc.StartDay(ctx, "2026-10-05", 3, ""); !errors.Is(err, service.ErrConflict) {
		t.Errorf("second start: err = %v, want ErrConflict", err)
	}
	if _, err := svc.GetDay(ctx, "2026-10-06"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unstarted day: err = %v, want ErrNotFound", err)
	}
}

func TestStartDayRejectsBadInput(t *testing.T) {
	svc, _ := newService(t)
	for name, start := range map[string]func() error{
		"pace 0":   func() error { _, err := svc.StartDay(context.Background(), "2026-10-05", 0, ""); return err },
		"pace 6":   func() error { _, err := svc.StartDay(context.Background(), "2026-10-05", 6, ""); return err },
		"bad date": func() error { _, err := svc.StartDay(context.Background(), "10/05", 3, ""); return err },
	} {
		if err := start(); !errors.Is(err, service.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestSetPaceKeepsMoodUnlessGivenAndUndoes(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.StartDay(ctx, "2026-10-05", 3, "anxious about the demo"); err != nil {
		t.Fatal(err)
	}

	batch := newBatch(t, svc, domain.ActorUser)
	day, err := svc.SetPace(ctx, batch, "2026-10-05", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if day.Pace != 1 || day.MoodNote != "anxious about the demo" {
		t.Errorf("after pace change: %+v", day)
	}
	day, err = svc.SetPace(ctx, newBatch(t, svc, domain.ActorAgent), "2026-10-05", 2, "a bit better")
	if err != nil {
		t.Fatal(err)
	}
	if day.MoodNote != "a bit better" {
		t.Errorf("mood note = %q", day.MoodNote)
	}

	// The first change can't be undone under the second, but the second can.
	if err := svc.Undo(ctx, batch); !errors.Is(err, service.ErrConflict) {
		t.Errorf("undo under a newer change: err = %v, want ErrConflict", err)
	}
	last, err := svc.LastUndoableBatch(ctx, domain.ActorAgent, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, last); err != nil {
		t.Fatal(err)
	}
	if day, _ := svc.GetDay(ctx, "2026-10-05"); day.Pace != 1 || day.MoodNote != "anxious about the demo" {
		t.Errorf("after undo: %+v", day)
	}
}

func TestSetPaceErrors(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SetPace(ctx, batch, "2026-10-05", 3, ""); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unstarted day: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.SetPace(ctx, batch, "2026-10-05", 9, ""); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("pace 9: err = %v, want ErrInvalid", err)
	}
}

func TestPlansAreVersionedPerDay(t *testing.T) {
	svc, clock := newService(t)
	ctx := context.Background()
	day, _ := svc.StartDay(ctx, "2026-10-05", 3, "")
	if _, err := svc.LatestPlan(ctx, day.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("no plan yet: err = %v, want ErrNotFound", err)
	}

	first, err := svc.SavePlan(ctx, domain.Plan{DayID: day.ID, Trigger: domain.TriggerMorning, Opening: "Morning!",
		Entries: []domain.PlanEntry{{TaskID: 1, Tier: domain.TierMust, Rationale: "due today"}}})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(30 * time.Minute)
	second, err := svc.SavePlan(ctx, domain.Plan{DayID: day.ID, Trigger: domain.TriggerPace, Reason: "pace changed", Fallback: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || second.Version != 2 {
		t.Errorf("versions = %d, %d", first.Version, second.Version)
	}

	latest, err := svc.LatestPlan(ctx, day.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(second, latest); diff != "" {
		t.Errorf("latest plan (-saved +loaded):\n%s", diff)
	}
	if latest.Entries == nil {
		t.Error("an empty plan should load with empty entries, not null")
	}
}

func TestSavePlanRejectsBadTierOrTrigger(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	day, _ := svc.StartDay(ctx, "2026-10-05", 3, "")
	bad := []domain.Plan{
		{DayID: day.ID, Trigger: "whim"},
		{DayID: day.ID, Trigger: domain.TriggerEdit, Entries: []domain.PlanEntry{{TaskID: 1, Tier: "maybe"}}},
	}
	for _, p := range bad {
		if _, err := svc.SavePlan(ctx, p); !errors.Is(err, service.ErrInvalid) {
			t.Errorf("%+v: err = %v, want ErrInvalid", p, err)
		}
	}
}
