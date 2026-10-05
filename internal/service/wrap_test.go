package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestWrapDayOnceWithANote(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	day, _ := svc.StartDay(ctx, "2026-10-05", 3, "")

	wrapped, err := svc.WrapDay(ctx, day.ID, "  start with the RFC  ")
	if err != nil {
		t.Fatal(err)
	}
	if wrapped.WrappedAt == nil || wrapped.TomorrowNote != "start with the RFC" {
		t.Errorf("wrapped = %+v", wrapped)
	}
	if got, _ := svc.GetDay(ctx, "2026-10-05"); got.WrappedAt == nil || got.TomorrowNote != "start with the RFC" {
		t.Errorf("stored = %+v", got)
	}
	if _, err := svc.WrapDay(ctx, day.ID, ""); !errors.Is(err, service.ErrConflict) {
		t.Errorf("second wrap: err = %v, want ErrConflict", err)
	}
	if _, err := svc.WrapDay(ctx, 99, ""); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("missing day: err = %v, want ErrNotFound", err)
	}
}

func TestUndoingAPaceChangeKeepsTheDayWrapped(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	day, _ := svc.StartDay(ctx, "2026-10-05", 3, "")
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SetPace(ctx, batch, "2026-10-05", 1, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WrapDay(ctx, day.ID, "rest"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDaySummary(ctx, day.ID, "A slow day."); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetDay(ctx, "2026-10-05")
	if got.Pace != 3 || got.WrappedAt == nil || got.TomorrowNote != "rest" || got.Summary != "A slow day." {
		t.Errorf("after undo: %+v", got)
	}
}

func TestSetDaySummaryOnAMissingDay(t *testing.T) {
	svc, _ := newService(t)
	if err := svc.SetDaySummary(context.Background(), 99, "x"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLastDayBefore(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.LastDayBefore(ctx, "2026-10-05"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("no days: err = %v, want ErrNotFound", err)
	}
	svc.StartDay(ctx, "2026-09-28", 3, "")
	svc.StartDay(ctx, "2026-10-01", 3, "")
	svc.StartDay(ctx, "2026-10-05", 3, "")

	got, err := svc.LastDayBefore(ctx, "2026-10-05")
	if err != nil || got.Date != "2026-10-01" {
		t.Errorf("got %+v, err %v; want 2026-10-01", got, err)
	}
}

func TestDoneBetweenCountsOnlyLiveCompletionsInTheWindow(t *testing.T) {
	svc, clock := newService(t) // 09:00 UTC
	ctx := context.Background()
	done := func(task domain.Task) int64 {
		batch := newBatch(t, svc, domain.ActorUser)
		if _, err := svc.SetTaskStatus(ctx, batch, task.ID, domain.StatusDone); err != nil {
			t.Fatal(err)
		}
		return batch
	}
	before := createTask(t, svc, "done yesterday")
	done(before)
	clock.advance(24 * time.Hour)
	inside := createTask(t, svc, "done today")
	done(inside)
	undone := createTask(t, svc, "done then undone")
	if err := svc.Undo(ctx, done(undone)); err != nil {
		t.Fatal(err)
	}
	reopened := createTask(t, svc, "done then reopened")
	done(reopened)
	svc.SetTaskStatus(ctx, newBatch(t, svc, domain.ActorUser), reopened.ID, domain.StatusOpen)
	createTask(t, svc, "still open")

	from := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	got, err := svc.DoneBetween(ctx, from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != inside.ID {
		t.Errorf("done = %+v, want only %q", got, inside.Title)
	}
}
