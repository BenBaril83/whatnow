package planner_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm/fake"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
)

// These guard the end-to-end fake against prompt changes: it reads task numbers out of the real
// prompts, and a reply that fails the planner's checks would show up as a fallback.

func TestTheFakePlansInFactsOrder(t *testing.T) {
	f := newFixture(t)
	f.slow = fake.Client{}
	due := f.task(t, service.TaskFields{Title: "due today", DueOn: date("2026-10-05")})
	later := f.task(t, service.TaskFields{Title: "due later", DueOn: date("2026-10-09")})
	blocked := f.task(t, service.TaskFields{Title: "blocked"})
	f.block(t, blocked.ID, due.ID)
	f.start(t, 3, "")

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fallback || plan.Opening != fake.Opening {
		t.Fatalf("plan = %+v", plan)
	}
	want := []domain.PlanEntry{
		{TaskID: due.ID, Tier: domain.TierMust, Rationale: fake.Rationale},
		{TaskID: later.ID, Tier: domain.TierShould, Rationale: fake.Rationale},
		{TaskID: blocked.ID, Tier: domain.TierCould, Rationale: fake.Rationale},
	}
	if diff := cmp.Diff(want, plan.Entries); diff != "" {
		t.Errorf("entries (-want +got):\n%s", diff)
	}
}

func TestTheFakePicksTheFirstFreeTask(t *testing.T) {
	f := newFixture(t)
	f.slow = fake.Client{}
	blocker := f.task(t, service.TaskFields{Title: "blocker", DueOn: date("2026-10-05")})
	waiting := f.task(t, service.TaskFields{Title: "waiting", DueOn: date("2026-10-05")})
	free := f.task(t, service.TaskFields{Title: "free"})
	f.block(t, waiting.ID, blocker.ID)
	f.start(t, 3, "")

	got, err := f.planner.WhatNow(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != (planner.Pick{TaskID: blocker.ID, Reason: fake.Reason}) {
		t.Errorf("pick = %+v", got)
	}
	got, err = f.planner.WhatNow(context.Background(), blocker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != (planner.Pick{TaskID: free.ID, Reason: fake.Reason}) {
		t.Errorf("pick after passing on #%d = %+v", blocker.ID, got)
	}
}

func TestTheFakeRollsOverEveryUnfinishedTask(t *testing.T) {
	f := newFixture(t)
	f.slow = fake.Client{}
	done := f.task(t, service.TaskFields{Title: "done"})
	a := f.task(t, service.TaskFields{Title: "a"})
	b := f.task(t, service.TaskFields{Title: "b"})
	f.planDay(t, f.start(t, 3, ""), done, a, b)
	f.finish(t, done)

	r, err := f.planner.WrapReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []planner.Suggestion{
		{TaskID: a.ID, Reason: fake.Suggest, Action: planner.ActionRollOver},
		{TaskID: b.ID, Reason: fake.Suggest, Action: planner.ActionRollOver},
	}
	if diff := cmp.Diff(want, r.Suggestions); r.Fallback || diff != "" {
		t.Errorf("fallback %v, suggestions (-want +got):\n%s", r.Fallback, diff)
	}
}

func TestTheFakeReflectsOnTheSummary(t *testing.T) {
	f := newFixture(t)
	f.slow = fake.Client{}
	f.start(t, 3, "")
	_, wrapped, err := f.planner.WrapUp(context.Background(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if wrapped.Summary != fake.Sentence || wrapped.Reflection != fake.Reflection {
		t.Errorf("wrapped = %+v", wrapped)
	}
}
