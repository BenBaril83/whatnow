package service_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/store"
)

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time          { return c.t }
func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newService(t *testing.T) (*service.Service, *testClock) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := &testClock{t: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)}
	return service.New(db, clock.now), clock
}

func newBatch(t *testing.T, svc *service.Service, actor domain.Actor) int64 {
	t.Helper()
	batch, err := svc.NewBatch(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func createTask(t *testing.T, svc *service.Service, title string) domain.Task {
	t.Helper()
	task, err := svc.CreateTask(context.Background(), newBatch(t, svc, domain.ActorUser), service.TaskFields{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func ptr[T any](v T) *T { return &v }
