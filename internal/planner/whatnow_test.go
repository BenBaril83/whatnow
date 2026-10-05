package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
)

func pick(id int64, reason string) map[string]any {
	return map[string]any{"reason": reason, "task_id": id}
}

func TestWhatNowPicksFromTheModel(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	f.model.replies = []any{pick(a.ID, "Ten minutes before standup: perfect for this.")}
	f.start(t, 2, "")

	got, err := f.planner.WhatNow(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != (planner.Pick{TaskID: a.ID, Reason: "Ten minutes before standup: perfect for this."}) {
		t.Errorf("pick = %+v", got)
	}
	req := f.model.sent[0]
	if req.User != "What should I do right now?" || !req.Fallback || req.Model != "claude-sonnet-5-5" {
		t.Errorf("request: %+v", req)
	}
}

func TestWhatNowWorksBeforeTheDayStarts(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	f.model.replies = []any{pick(a.ID, "go")}

	if got, err := f.planner.WhatNow(context.Background(), 0); err != nil || got.TaskID != a.ID {
		t.Errorf("pick %+v, err %v", got, err)
	}
	if strings.Contains(system(f.model.sent[0]), "Pace today") {
		t.Error("no day, so no pace should be claimed")
	}
}

func TestWhatNowRetriesABlockedOrExcludedPick(t *testing.T) {
	f := newFixture(t)
	blocked := f.task(t, service.TaskFields{Title: "blocked"})
	first := f.task(t, service.TaskFields{Title: "first"})
	passed := f.task(t, service.TaskFields{Title: "passed on"})
	f.block(t, blocked.ID, first.ID)

	cases := []struct {
		bad     int64
		problem string
	}{{blocked.ID, "#1 is blocked"}, {passed.ID, "#3 is the task the user just passed on"}, {42, "#42 is not an eligible task"}}
	for _, c := range cases {
		f.model.sent = nil
		f.model.replies = []any{pick(c.bad, "x"), pick(first.ID, "fine")}
		got, err := f.planner.WhatNow(context.Background(), passed.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.TaskID != first.ID || got.Fallback {
			t.Errorf("pick = %+v", got)
		}
		if !strings.Contains(f.model.sent[0].User, "Not #3") || !strings.Contains(f.model.sent[1].User, c.problem) {
			t.Errorf("requests: %q then %q", f.model.sent[0].User, f.model.sent[1].User)
		}
	}
}

func TestWhatNowFallsBackToTheFirstFreeTaskInFallbackOrder(t *testing.T) {
	f := newFixture(t, errors.New("rate limited"))
	f.task(t, service.TaskFields{Title: "undated"})
	dueToday := f.task(t, service.TaskFields{Title: "due today", DueOn: date("2026-10-05")})
	overdue := f.task(t, service.TaskFields{Title: "overdue", DueOn: date("2026-10-01")})
	research := f.task(t, service.TaskFields{Title: "research", DueOn: date("2026-10-07")})
	f.block(t, overdue.ID, research.ID)

	// Overdue is first but blocked; due today is the user's pass; research is next by due date.
	got, err := f.planner.WhatNow(context.Background(), dueToday.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskID != research.ID || !got.Fallback {
		t.Errorf("pick = %+v", got)
	}
}

func TestWhatNowWithNothingFree(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	f.task(t, service.TaskFields{Title: "later", NotBefore: date("2026-10-09")})
	if _, err := f.planner.WhatNow(context.Background(), a.ID); !errors.Is(err, planner.ErrNothingToDo) {
		t.Errorf("err = %v, want ErrNothingToDo", err)
	}
	if len(f.model.sent) != 0 {
		t.Error("the model was asked with nothing to pick")
	}
}
