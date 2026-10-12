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

var syncDay = time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)

func pulled(key, title string, hour int) service.PulledEvent {
	end := syncDay.Add(time.Duration(hour)*time.Hour + 30*time.Minute)
	return service.PulledEvent{Key: key, Title: title, StartsAt: syncDay.Add(time.Duration(hour) * time.Hour), EndsAt: &end}
}

// sync pulls events into today as gcal, returning the live events by key.
func sync(t *testing.T, svc *service.Service, prune bool, events ...service.PulledEvent) map[string]domain.Event {
	t.Helper()
	live, err := svc.SyncEvents(context.Background(), newBatch(t, svc, domain.ActorUser), "gcal",
		syncDay, syncDay.AddDate(0, 0, 1), events, prune)
	if err != nil {
		t.Fatal(err)
	}
	return live
}

// titles lists today's live events as "title@hour".
func titles(t *testing.T, svc *service.Service) []string {
	t.Helper()
	events, err := svc.ListEvents(context.Background(), syncDay, syncDay.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Title+"@"+e.StartsAt.Format("15"))
	}
	return out
}

func TestSyncEventsCreatesThenUpdatesInPlace(t *testing.T) {
	svc, _ := newService(t)
	first := sync(t, svc, true, pulled("a", "Standup", 9), pulled("b", "1:1", 13))
	if first["a"].Source != "gcal" || first["a"].SourceKey != "a" {
		t.Errorf("created event = %+v", first["a"])
	}

	// The 1:1 moved and was renamed: same key, so the same row.
	second := sync(t, svc, true, pulled("a", "Standup", 9), pulled("b", "1:1 with Sam", 15))
	if second["b"].ID != first["b"].ID {
		t.Errorf("moved event got a new row: %d, was %d", second["b"].ID, first["b"].ID)
	}
	if diff := cmp.Diff([]string{"Standup@09", "1:1 with Sam@15"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestSyncEventsWritesNothingWhenNothingChanged(t *testing.T) {
	svc, _ := newService(t)
	sync(t, svc, true, pulled("a", "Standup", 9))
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SyncEvents(context.Background(), batch, "gcal", syncDay, syncDay.AddDate(0, 0, 1),
		[]service.PulledEvent{pulled("a", "Standup", 9)}, true); err != nil {
		t.Fatal(err)
	}
	if acts, _ := svc.BatchActivity(context.Background(), batch); len(acts) != 0 {
		t.Errorf("activity = %+v, want none", acts)
	}
}

func TestSyncEventsKeepsARemovalAsATombstone(t *testing.T) {
	svc, _ := newService(t)
	live := sync(t, svc, true, pulled("a", "Blockout", 15), pulled("b", "Standup", 9))
	if err := svc.DeleteEvent(context.Background(), newBatch(t, svc, domain.ActorUser), live["a"].ID); err != nil {
		t.Fatal(err)
	}

	again := sync(t, svc, true, pulled("a", "Blockout", 15), pulled("b", "Standup", 9))
	if _, ok := again["a"]; ok {
		t.Error("a removed event came back as live")
	}
	if diff := cmp.Diff([]string{"Standup@09"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestSyncEventsPrunesWhatTheFeedDropped(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	sync(t, svc, true, pulled("a", "Standup", 9), pulled("b", "Canceled", 13))
	mine, err := svc.CreateEvent(ctx, newBatch(t, svc, domain.ActorUser), service.EventFields{Title: "Mine", StartsAt: syncDay.Add(11 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	sync(t, svc, true, pulled("a", "Standup", 9))
	if diff := cmp.Diff([]string{"Standup@09", "Mine@11"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}

	// The pruned row isn't a tombstone: if the meeting comes back, so does the event.
	sync(t, svc, true, pulled("a", "Standup", 9), pulled("b", "Canceled", 13))
	if diff := cmp.Diff([]string{"Standup@09", "Mine@11", "Canceled@13"}, titles(t, svc)); diff != "" {
		t.Errorf("after it came back (-want +got):\n%s", diff)
	}
	if got, err := svc.GetEvent(ctx, mine.ID); err != nil || got.Source != "" {
		t.Errorf("own event = %+v, %v", got, err)
	}
}

func TestSyncEventsWithoutPruneDeletesNothing(t *testing.T) {
	svc, _ := newService(t)
	sync(t, svc, true, pulled("a", "Standup", 9), pulled("b", "1:1", 13))
	sync(t, svc, false, pulled("a", "Standup", 9))
	if diff := cmp.Diff([]string{"Standup@09", "1:1@13"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestSyncEventsOnlyPrunesItsSourceAndWindow(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	tomorrow := pulled("t", "Tomorrow", 9)
	tomorrow.StartsAt, tomorrow.EndsAt = tomorrow.StartsAt.AddDate(0, 0, 1), nil
	if _, err := svc.SyncEvents(ctx, newBatch(t, svc, domain.ActorUser), "gcal", syncDay.AddDate(0, 0, 1), syncDay.AddDate(0, 0, 2),
		[]service.PulledEvent{tomorrow}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SyncEvents(ctx, newBatch(t, svc, domain.ActorUser), "other", syncDay, syncDay.AddDate(0, 0, 1),
		[]service.PulledEvent{pulled("o", "Other", 10)}, true); err != nil {
		t.Fatal(err)
	}

	sync(t, svc, true)

	if diff := cmp.Diff([]string{"Other@10"}, titles(t, svc)); diff != "" {
		t.Errorf("today (-want +got):\n%s", diff)
	}
	next, err := svc.ListEvents(ctx, syncDay.AddDate(0, 0, 1), syncDay.AddDate(0, 0, 2))
	if err != nil || len(next) != 1 {
		t.Errorf("tomorrow = %+v, %v", next, err)
	}
}

func TestUndoingAPullLetsTheNextPullBringItBack(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SyncEvents(ctx, batch, "gcal", syncDay, syncDay.AddDate(0, 0, 1),
		[]service.PulledEvent{pulled("a", "Standup", 9)}, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, svc); len(got) != 0 {
		t.Fatalf("after undo = %v", got)
	}

	sync(t, svc, true, pulled("a", "Standup", 9))
	if diff := cmp.Diff([]string{"Standup@09"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestUndoingAPruneRestoresTheEventAndItsKey(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	sync(t, svc, true, pulled("a", "Standup", 9))
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SyncEvents(ctx, batch, "gcal", syncDay, syncDay.AddDate(0, 0, 1), nil, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}

	live := sync(t, svc, true, pulled("a", "Standup", 9))
	if diff := cmp.Diff([]string{"Standup@09"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if live["a"].SourceKey != "a" {
		t.Errorf("restored event = %+v", live["a"])
	}
}

func TestSyncEventsRejectsAnInvalidEvent(t *testing.T) {
	svc, _ := newService(t)
	bad := pulled("a", "  ", 9)
	if _, err := svc.SyncEvents(context.Background(), newBatch(t, svc, domain.ActorUser), "gcal", syncDay, syncDay.AddDate(0, 0, 1),
		[]service.PulledEvent{bad}, true); err == nil {
		t.Error("a blank title was accepted")
	}
}

func TestUndoingAPruneRefusesOnceTheEventWasPulledAgain(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	sync(t, svc, true, pulled("a", "Standup", 9))
	prune := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SyncEvents(ctx, prune, "gcal", syncDay, syncDay.AddDate(0, 0, 1), nil, true); err != nil {
		t.Fatal(err)
	}
	sync(t, svc, true, pulled("a", "Standup", 9))

	if err := svc.Undo(ctx, prune); !errors.Is(err, service.ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
	if diff := cmp.Diff([]string{"Standup@09"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}
