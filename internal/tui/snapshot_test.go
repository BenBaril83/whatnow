package tui

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
)

func TestSnapshotGroupsThePlanByTierThenWhatItLeftOut(t *testing.T) {
	f := newFixture(t)
	a, b, c, d := f.task(t, "a"), f.task(t, "b"), f.task(t, "c"), f.task(t, "d")
	f.start(t)
	late := f.task(t, "late")

	got := f.load(t).groups()
	want := []group{
		{tier: domain.TierMust, ids: []int64{a.ID}},
		{tier: domain.TierShould, ids: []int64{b.ID}},
		{tier: domain.TierCould, ids: []int64{c.ID, d.ID}},
		{tier: "", ids: []int64{late.ID}},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(group{})); diff != "" {
		t.Errorf("groups (-want +got):\n%s", diff)
	}
}

func TestSnapshotLeavesOutDoneTasksAndCountsThem(t *testing.T) {
	f := newFixture(t)
	a, b := f.task(t, "a"), f.task(t, "b")
	f.start(t)
	ctx := context.Background()
	batch, _ := f.app.Service.NewBatch(ctx, domain.ActorUser)
	if _, err := f.app.Service.SetTaskStatus(ctx, batch, a.ID, domain.StatusDone); err != nil {
		t.Fatal(err)
	}

	s := f.load(t)
	if diff := cmp.Diff([]int64{b.ID}, s.order()); diff != "" {
		t.Errorf("order (-want +got):\n%s", diff)
	}
	if s.doneToday != 1 {
		t.Errorf("doneToday = %d, want 1", s.doneToday)
	}
}

func TestSnapshotCountsOnlyOpenBlockers(t *testing.T) {
	f := newFixture(t)
	first, waiting := f.task(t, "buy milk"), f.task(t, "pour cereal")
	ctx := context.Background()
	batch, _ := f.app.Service.NewBatch(ctx, domain.ActorUser)
	if err := f.app.Service.AddBlocker(ctx, batch, domain.Blocker{TaskID: waiting.ID, BlockedByID: first.ID}); err != nil {
		t.Fatal(err)
	}
	f.start(t)
	if n := f.load(t).waits[waiting.ID]; n != 1 {
		t.Fatalf("waits = %d, want 1", n)
	}

	batch, _ = f.app.Service.NewBatch(ctx, domain.ActorUser)
	if _, err := f.app.Service.SetTaskStatus(ctx, batch, first.ID, domain.StatusDone); err != nil {
		t.Fatal(err)
	}
	if n := f.load(t).waits[waiting.ID]; n != 0 {
		t.Errorf("waits after the blocker is done = %d, want 0", n)
	}
}

func TestSnapshotReadsTheEndOfDayOnToday(t *testing.T) {
	f := newFixture(t)
	want := time.Date(2026, 10, 6, 17, 30, 0, 0, time.UTC) // the default end of day
	if got := f.load(t).endOfDay; !got.Equal(want) {
		t.Errorf("endOfDay = %v, want %v", got, want)
	}
}

func TestSnapshotHasNoPlanBeforeTheDayStarts(t *testing.T) {
	f := newFixture(t)
	f.task(t, "a")
	s := f.load(t)
	if s.today.Plan != nil {
		t.Error("a plan before the day started")
	}
	if len(s.groups()) != 0 {
		t.Errorf("groups before the day started: %v", s.groups())
	}
}
