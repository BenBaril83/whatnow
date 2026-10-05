package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestPushCountsLeaveOutUndonePushesAndOtherEdits(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "write the RFC")
	other := createTask(t, svc, "water plants")

	for _, until := range []domain.Date{"2026-10-06", "2026-10-07"} {
		if _, err := svc.PushTask(ctx, newBatch(t, svc, domain.ActorUser), task.ID, until); err != nil {
			t.Fatal(err)
		}
	}
	undone := newBatch(t, svc, domain.ActorUser)
	pushed, err := svc.PushTask(ctx, undone, task.ID, "2026-10-08")
	if err != nil {
		t.Fatal(err)
	}
	if pushed.NotBefore == nil || *pushed.NotBefore != "2026-10-08" {
		t.Errorf("not_before = %v", pushed.NotBefore)
	}
	if err := svc.Undo(ctx, undone); err != nil {
		t.Fatal(err)
	}
	// An ordinary edit that happens to set not_before is not a push.
	if _, err := svc.UpdateTask(ctx, newBatch(t, svc, domain.ActorUser), other.ID,
		service.TaskFields{Title: "water plants", NotBefore: ptr(domain.Date("2026-10-09"))}); err != nil {
		t.Fatal(err)
	}

	counts, err := svc.PushCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 || counts[task.ID] != 2 {
		t.Errorf("counts = %v, want {%d: 2}", counts, task.ID)
	}
	if got, _ := svc.GetTask(ctx, task.ID); *got.NotBefore != "2026-10-07" {
		t.Errorf("undo left not_before at %v", *got.NotBefore)
	}
}

func TestPushRejectsABadDate(t *testing.T) {
	svc, _ := newService(t)
	task := createTask(t, svc, "x")
	if _, err := svc.PushTask(context.Background(), newBatch(t, svc, domain.ActorUser), task.ID, "tomorrow"); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestAppendFeeling(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "review FLEX1-420")

	got, err := svc.AppendFeeling(ctx, newBatch(t, svc, domain.ActorUser), task.ID, " not before coffee ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Feeling != "not before coffee" {
		t.Errorf("feeling = %q", got.Feeling)
	}
	got, _ = svc.AppendFeeling(ctx, newBatch(t, svc, domain.ActorUser), task.ID, "too fried")
	if got.Feeling != "not before coffee; too fried" {
		t.Errorf("feeling = %q", got.Feeling)
	}
	if _, err := svc.AppendFeeling(ctx, newBatch(t, svc, domain.ActorUser), task.ID, "  "); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("empty note: err = %v, want ErrInvalid", err)
	}
}
