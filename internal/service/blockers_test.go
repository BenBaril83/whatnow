package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestAddAndRemoveBlocker(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	work := createTask(t, svc, "do FLEX1-666")
	research := createTask(t, svc, "research pubsub")
	b := domain.Blocker{TaskID: work.ID, BlockedByID: research.ID}

	if err := svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorAgent), b); err != nil {
		t.Fatal(err)
	}
	got, err := svc.ListBlockers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]domain.Blocker{b}, got); diff != "" {
		t.Errorf("blockers (-want +got):\n%s", diff)
	}

	if err := svc.RemoveBlocker(ctx, newBatch(t, svc, domain.ActorUser), b); err != nil {
		t.Fatal(err)
	}
	got, err = svc.ListBlockers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("blocker still listed after removal: %+v", got)
	}
}

func TestAddBlockerTwiceRecordsOnce(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	a, b := createTask(t, svc, "a"), createTask(t, svc, "b")
	blocker := domain.Blocker{TaskID: a.ID, BlockedByID: b.ID}
	if err := svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorUser), blocker); err != nil {
		t.Fatal(err)
	}

	batch := newBatch(t, svc, domain.ActorUser)
	if err := svc.AddBlocker(ctx, batch, blocker); err != nil {
		t.Fatalf("second add: %v", err)
	}
	acts, err := svc.BatchActivity(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 0 {
		t.Errorf("a duplicate add recorded %d activity rows, want 0", len(acts))
	}
}

func TestAddBlockerRejectsCycles(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	a, b, c := createTask(t, svc, "a"), createTask(t, svc, "b"), createTask(t, svc, "c")
	add := func(task, by int64) error {
		return svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorUser), domain.Blocker{TaskID: task, BlockedByID: by})
	}
	if err := add(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := add(b.ID, c.ID); err != nil {
		t.Fatal(err)
	}

	cases := map[string][2]int64{
		"self":       {a.ID, a.ID},
		"direct":     {b.ID, a.ID},
		"transitive": {c.ID, a.ID},
	}
	for name, pair := range cases {
		t.Run(name, func(t *testing.T) {
			if err := add(pair[0], pair[1]); !errors.Is(err, service.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestBlockerErrors(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	a := createTask(t, svc, "a")

	err := svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorUser), domain.Blocker{TaskID: a.ID, BlockedByID: 999})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown task: err = %v, want ErrNotFound", err)
	}
	err = svc.RemoveBlocker(ctx, newBatch(t, svc, domain.ActorUser), domain.Blocker{TaskID: a.ID, BlockedByID: 999})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("missing blocker: err = %v, want ErrNotFound", err)
	}
}

func TestListBlockersHidesDeletedTasks(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	a, b := createTask(t, svc, "a"), createTask(t, svc, "b")
	if err := svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorUser), domain.Blocker{TaskID: a.ID, BlockedByID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteTask(ctx, newBatch(t, svc, domain.ActorUser), b.ID); err != nil {
		t.Fatal(err)
	}

	got, err := svc.ListBlockers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("blocker on a deleted task still listed: %+v", got)
	}
}
