package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestMorningPlanFromTheModel(t *testing.T) {
	f := newFixture(t)
	flex := f.task(t, service.TaskFields{Title: "Do FLEX1-666", DueOn: date("2026-10-05"), Feeling: "dreading it"})
	plants := f.task(t, service.TaskFields{Title: "water plants"})
	f.model.replies = []any{map[string]any{
		"entries": []any{entry(flex.ID, domain.TierMust), entry(plants.ID, domain.TierCould)},
		"opening": "Rough night. One must today.",
	}}
	f.start(t, 2, "slept badly")
	msgs, unsub := f.hub.Subscribe()
	defer unsub()

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}

	want := []domain.PlanEntry{{TaskID: flex.ID, Tier: domain.TierMust, Rationale: "because"},
		{TaskID: plants.ID, Tier: domain.TierCould, Rationale: "because"}}
	if diff := cmp.Diff(want, plan.Entries); diff != "" {
		t.Errorf("entries (-want +got):\n%s", diff)
	}
	if plan.Opening != "Rough night. One must today." || plan.Fallback || plan.Version != 1 || plan.Trigger != domain.TriggerMorning {
		t.Errorf("plan = %+v", plan)
	}

	req := f.model.sent[0]
	if req.Model != "claude-sonnet-5-5" || !req.Fallback || req.Schema == nil || req.User != "Plan my day." {
		t.Errorf("request: model %q, fallback %v, user %q", req.Model, req.Fallback, req.User)
	}
	sys := system(req)
	for _, want := range []string{"dry humor", `Pace today: 2 of 5, "gentle".`, `On their mind: "slept badly"`,
		"Monday 2026-10-05 09:00", `#1 Do FLEX1-666 feeling: "dreading it"`, "  - due today"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt is missing %q", want)
		}
	}

	m := <-msgs
	if got, ok := m.Data.(domain.Plan); m.Type != "plan.updated" || !ok || got.ID != plan.ID {
		t.Errorf("published %+v", m)
	}
}

func TestReplanShowsTheCurrentPlanAndUnplannedTasks(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	b := f.task(t, service.TaskFields{Title: "b"})
	f.model.replies = []any{
		map[string]any{"entries": []any{entry(a.ID, domain.TierShould)}, "opening": "hi"},
		map[string]any{"entries": []any{entry(a.ID, domain.TierShould), entry(b.ID, domain.TierCould)}, "opening": "ok"},
	}
	f.start(t, 3, "")
	ctx := context.Background()
	if _, err := f.planner.Replan(ctx, domain.TriggerMorning, ""); err != nil {
		t.Fatal(err)
	}

	plan, err := f.planner.Replan(ctx, domain.TriggerPace, "pace changed to full steam")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != 2 || plan.Reason != "pace changed to full steam" {
		t.Errorf("plan = %+v", plan)
	}
	req := f.model.sent[1]
	if req.User != "Something changed: pace changed to full steam. Update the plan." {
		t.Errorf("user = %q", req.User)
	}
	sys := system(req)
	for _, want := range []string{"must: (none)\nshould: #1\ncould: (none)\n", "unplanned: #2\n"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt is missing %q", want)
		}
	}
}

func TestAnInvalidPlanIsRetriedWithItsProblems(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	later := f.task(t, service.TaskFields{Title: "later", NotBefore: date("2026-10-09")})
	f.model.replies = []any{
		map[string]any{"entries": []any{entry(a.ID, domain.TierMust), entry(a.ID, domain.TierCould),
			entry(later.ID, domain.TierCould), entry(99, domain.TierShould), entry(a.ID, "someday")}, "opening": "x"},
		map[string]any{"entries": []any{entry(a.ID, domain.TierMust)}, "opening": "fixed"},
	}
	f.start(t, 3, "")

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fallback || plan.Opening != "fixed" {
		t.Errorf("plan = %+v", plan)
	}
	retry := f.model.sent[1].User
	for _, want := range []string{"Your previous reply was", "#1 appears more than once", "#2 is not an eligible task",
		"#99 is not an eligible task", `#1 has tier "someday"`} {
		if !strings.Contains(retry, want) {
			t.Errorf("retry message is missing %q:\n%s", want, retry)
		}
	}
}

func TestFallbackPlanAfterTwoBadReplies(t *testing.T) {
	f := newFixture(t, "not json", map[string]any{"entries": []any{entry(99, domain.TierMust)}, "opening": "x"})
	undated := f.task(t, service.TaskFields{Title: "undated"})
	later := f.task(t, service.TaskFields{Title: "due friday", DueOn: date("2026-10-09")})
	overdue := f.task(t, service.TaskFields{Title: "overdue", DueOn: date("2026-10-01")})
	dueToday := f.task(t, service.TaskFields{Title: "due today", DueOn: date("2026-10-05")})
	blocker := f.task(t, service.TaskFields{Title: "research"})
	f.block(t, dueToday.ID, blocker.ID)
	f.start(t, 3, "")

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fallback || len(f.model.sent) != 2 {
		t.Fatalf("fallback %v after %d calls", plan.Fallback, len(f.model.sent))
	}
	var got []string
	for _, e := range plan.Entries {
		got = append(got, string(e.Tier)+" "+map[int64]string{undated.ID: "undated", later.ID: "later",
			overdue.ID: "overdue", dueToday.ID: "today", blocker.ID: "research"}[e.TaskID])
	}
	// The blocked must stays a must, and its blocker is planned like any other task.
	want := []string{"must overdue", "must today", "should later", "could undated", "could research"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("fallback plan (-want +got):\n%s", diff)
	}
}

func TestProviderErrorFallsBackWithoutARetry(t *testing.T) {
	f := newFixture(t, errors.New("anthropic: 529 overloaded"))
	f.task(t, service.TaskFields{Title: "a"})
	f.start(t, 3, "")

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fallback || len(f.model.sent) != 1 || !strings.Contains(plan.Opening, "couldn't") {
		t.Errorf("plan = %+v after %d calls", plan, len(f.model.sent))
	}
}

func TestNoProviderGivesAFallbackPlan(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	f.task(t, service.TaskFields{Title: "a"})
	f.start(t, 3, "")

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fallback || len(plan.Entries) != 1 || !strings.Contains(plan.Opening, "No AI provider") {
		t.Errorf("plan = %+v", plan)
	}
}

func TestReplanNeedsAStartedDay(t *testing.T) {
	f := newFixture(t)
	if _, err := f.planner.Replan(context.Background(), domain.TriggerEdit, "x"); !errors.Is(err, planner.ErrNoDay) {
		t.Errorf("err = %v, want ErrNoDay", err)
	}
}

func TestAnEmptyDayStillGetsAPlan(t *testing.T) {
	f := newFixture(t, map[string]any{"entries": []any{}, "opening": "Nothing on the list. Enjoy it."})
	f.start(t, 3, "")
	msgs, unsub := f.hub.Subscribe()
	defer unsub()

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fallback || len(plan.Entries) != 0 {
		t.Errorf("plan = %+v", plan)
	}
	if m := <-msgs; m.Type != "plan.updated" {
		t.Errorf("published %+v", m)
	}
}

// hang is a model that never answers until its context ends.
type hang struct{}

func (hang) Complete(ctx context.Context, _ llm.Request) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func TestAHungModelTimesOutIntoAFallbackPlan(t *testing.T) {
	defer planner.SetModelTimeout(50 * time.Millisecond)()
	f := newFixture(t)
	f.slow = hang{}
	f.task(t, service.TaskFields{Title: "a"})
	f.start(t, 3, "")

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fallback || len(plan.Entries) != 1 {
		t.Errorf("plan = %+v", plan)
	}
}

func TestStartingAgainMakesTheMissingFirstPlan(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	// The day started, but its first plan was never saved (say the request was cancelled).
	f.start(t, 2, "tired")

	day, plan, err := f.planner.StartDay(ctx, 4, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if day.Pace != 2 || plan.Version != 1 || plan.Trigger != domain.TriggerMorning {
		t.Errorf("day %+v, plan %+v", day, plan)
	}
	if _, _, err := f.planner.StartDay(ctx, 3, ""); !errors.Is(err, service.ErrConflict) {
		t.Errorf("starting a planned day: err = %v, want ErrConflict", err)
	}
}
