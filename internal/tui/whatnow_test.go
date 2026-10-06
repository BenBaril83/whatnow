package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm/fake"
	"github.com/jameynakama/whatnow/internal/planner"
)

func TestWMovesTheCursorToThePickAndShowsWhy(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.task(t, "call mom")
	f.start(t)
	want, err := f.app.Planner.WhatNow(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("w"))
	waitFor(t, tm, fake.Reason)
	if m := quit(t, tm); m.cursor != want.TaskID || m.pick == nil {
		t.Errorf("cursor = %d, want the pick %d", m.cursor, want.TaskID)
	}
}

func TestNPassesOnThePickWithAReason(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.task(t, "call mom")
	f.start(t)
	ctx := context.Background()
	first, err := f.app.Planner.WhatNow(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.app.Planner.WhatNow(ctx, first.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("w"))
	waitFor(t, tm, fake.Reason)
	tm.Send(key("n"))
	tm.Type("too tired")
	tm.Send(key("enter"))
	waitFor(t, tm, "▸ ☐ "+f.status(t, second.TaskID).Title)
	quit(t, tm)
	if got := f.status(t, first.TaskID).Feeling; !strings.Contains(got, "too tired") {
		t.Errorf("feeling = %q", got)
	}
}

func TestNDoesNothingWithoutAPick(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	if m := send(f.model(t, 80, 24), key("n")); m.mode != modeList {
		t.Errorf("mode = %d", m.mode)
	}
}

func TestThePickClearsWhenItsTaskLeaves(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.task(t, "call mom")
	f.start(t)
	m := send(f.model(t, 80, 24), pickedMsg{pick: planner.Pick{TaskID: milk.ID, Reason: "go"}})
	s := f.load(t)
	task := s.tasks[milk.ID]
	task.Status = domain.StatusDone
	s.tasks[milk.ID] = task
	if send(m, snapshotMsg{s}).pick != nil {
		t.Error("the pick outlived its task")
	}
}
