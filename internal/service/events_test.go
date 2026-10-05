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

func TestListEventsForALocalDay(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	loc := time.FixedZone("UTC-5", -5*3600)
	at := func(day, hour, min int) time.Time { return time.Date(2026, 10, day, hour, min, 0, 0, loc) }
	create := func(title string, start time.Time) {
		t.Helper()
		_, err := svc.CreateEvent(ctx, newBatch(t, svc, domain.ActorUser), service.EventFields{Title: title, StartsAt: start})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Created out of order, and straddling midnight in UTC: 23:30 local is 04:30 UTC the next day.
	create("late call", at(5, 23, 30))
	create("standup", at(5, 10, 0))
	create("yesterday", at(4, 23, 59))
	create("tomorrow", at(6, 0, 0))

	from := at(5, 0, 0)
	got, err := svc.ListEvents(ctx, from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}

	var titles []string
	for _, e := range got {
		titles = append(titles, e.Title)
	}
	if len(titles) != 2 || titles[0] != "standup" || titles[1] != "late call" {
		t.Errorf("titles = %v, want [standup late call]", titles)
	}
}

func TestEventValidation(t *testing.T) {
	start := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	cases := map[string]service.EventFields{
		"no title":          {StartsAt: start},
		"no start":          {Title: "1:1"},
		"ends before start": {Title: "1:1", StartsAt: start, EndsAt: ptr(start.Add(-time.Minute))},
		"ends at start":     {Title: "1:1", StartsAt: start, EndsAt: ptr(start)},
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _ := newService(t)
			_, err := svc.CreateEvent(context.Background(), newBatch(t, svc, domain.ActorUser), fields)
			if !errors.Is(err, service.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestUpdateAndDeleteEvent(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	e, err := svc.CreateEvent(ctx, newBatch(t, svc, domain.ActorUser),
		service.EventFields{Title: "1:1", StartsAt: start, RawText: "1:1 at 2"})
	if err != nil {
		t.Fatal(err)
	}

	end := start.Add(30 * time.Minute)
	updated, err := svc.UpdateEvent(ctx, newBatch(t, svc, domain.ActorUser), e.ID,
		service.EventFields{Title: "1:1 with Sam", StartsAt: start, EndsAt: &end})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "1:1 with Sam" || updated.EndsAt == nil || !updated.EndsAt.Equal(end) {
		t.Errorf("update not applied: %+v", updated)
	}
	if updated.RawText != "1:1 at 2" {
		t.Errorf("raw text changed on update: %q", updated.RawText)
	}
	if got, err := svc.GetEvent(ctx, e.ID); err != nil || !cmp.Equal(got, updated) {
		t.Errorf("get = %+v, %v; want %+v", got, err, updated)
	}

	if err := svc.DeleteEvent(ctx, newBatch(t, svc, domain.ActorUser), e.ID); err != nil {
		t.Fatal(err)
	}
	got, err := svc.ListEvents(ctx, start.Add(-time.Hour), start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("deleted event still listed: %+v", got)
	}
	_, err = svc.UpdateEvent(ctx, newBatch(t, svc, domain.ActorUser), e.ID, service.EventFields{Title: "x", StartsAt: start})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("update deleted event: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.GetEvent(ctx, e.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("get deleted event: err = %v, want ErrNotFound", err)
	}
}
