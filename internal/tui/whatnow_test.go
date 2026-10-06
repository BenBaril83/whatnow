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

func TestWWithNothingFreeSaysSo(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.start(t)
	ctx := context.Background()
	batch, _ := f.app.Service.NewBatch(ctx, domain.ActorUser)
	if _, err := f.app.Service.SetTaskStatus(ctx, batch, milk.ID, domain.StatusDone); err != nil {
		t.Fatal(err)
	}
	m := f.model(t, 80, 24)
	if v := send(m, m.whatNow(0, "")()).View(); !strings.Contains(v, "Nothing is free to start right now.") {
		t.Errorf("view:\n%s", v)
	}
}

func TestWWhileAskingDoesNotAskTwice(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	m := f.model(t, 80, 24)
	m.asking = true
	if _, cmd := m.Update(key("w")); cmd != nil {
		t.Error("w asked again while an answer was coming")
	}
}

func TestPassingOnATaskThatIsGoneShowsWhy(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	if _, ok := f.model(t, 80, 24).whatNow(9999, "too tired")().(failedMsg); !ok {
		t.Error("passing on a missing task didn't fail")
	}
}
