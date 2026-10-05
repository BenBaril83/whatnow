package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
)

// planDay saves a plan for a day with every given task as a should.
func (f *fixture) planDay(t *testing.T, day domain.Day, tasks ...domain.Task) {
	t.Helper()
	p := domain.Plan{DayID: day.ID, Trigger: domain.TriggerMorning}
	for _, task := range tasks {
		p.Entries = append(p.Entries, domain.PlanEntry{TaskID: task.ID, Tier: domain.TierShould})
	}
	if _, err := f.svc.SavePlan(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) finish(t *testing.T, task domain.Task) {
	t.Helper()
	f.write(t, func(b int64) error {
		_, err := f.svc.SetTaskStatus(context.Background(), b, task.ID, domain.StatusDone)
		return err
	})
}

func suggestion(id int64, action, until string) map[string]any {
	return map[string]any{"task_id": id, "reason": "because", "action": action, "until": until}
}

func TestWrapReviewSuggestsForEachUnfinishedTask(t *testing.T) {
	f := newFixture(t)
	shipped := f.task(t, service.TaskFields{Title: "ship it"})
	rfc := f.task(t, service.TaskFields{Title: "write the RFC"})
	taxes := f.task(t, service.TaskFields{Title: "do taxes"})
	f.planDay(t, f.start(t, 3, ""), shipped, rfc, taxes)
	f.finish(t, shipped)
	f.model.replies = []any{map[string]any{"suggestions": []any{
		suggestion(rfc.ID, "roll_over", ""), suggestion(taxes.ID, "push", "2026-10-12")}}}

	r, err := f.planner.WrapReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Done) != 1 || r.Done[0].ID != shipped.ID || len(r.Unfinished) != 2 || r.Fallback {
		t.Errorf("review = %+v", r)
	}
	want := []planner.Suggestion{{TaskID: rfc.ID, Reason: "because", Action: planner.ActionRollOver},
		{TaskID: taxes.ID, Reason: "because", Action: planner.ActionPush, Until: "2026-10-12"}}
	if diff := cmp.Diff(want, r.Suggestions); diff != "" {
		t.Errorf("suggestions (-want +got):\n%s", diff)
	}
	req := f.model.sent[0]
	if req.Model != "claude-sonnet-5-5" || !req.Fallback ||
		req.User != `I'm wrapping up 2026-10-05. Done: #1 "ship it". Suggest what to do with each unfinished task: #2 #3` {
		t.Errorf("request: %+v", req)
	}
}

func TestBadSuggestionsAreRetriedAndGapsRollOver(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	b := f.task(t, service.TaskFields{Title: "b"})
	c := f.task(t, service.TaskFields{Title: "c"})
	f.planDay(t, f.start(t, 3, ""), a, b, c)
	f.model.replies = []any{
		map[string]any{"suggestions": []any{suggestion(a.ID, "push", ""), suggestion(b.ID, "push", "2026-10-05"),
			suggestion(c.ID, "drop", "2026-10-09"), suggestion(c.ID, "roll_over", ""), suggestion(42, "drop", "")}},
		map[string]any{"suggestions": []any{suggestion(b.ID, "drop", "")}},
	}

	r, err := f.planner.WrapReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	retry := f.model.sent[1].User
	for _, want := range []string{`#1 is pushed to "", which is not a date after 2026-10-05`,
		`#2 is pushed to "2026-10-05"`, "#3 has a date but isn't a push", "#3 appears more than once",
		"#42 is not an unfinished task"} {
		if !strings.Contains(retry, want) {
			t.Errorf("retry is missing %q:\n%s", want, retry)
		}
	}
	got := []planner.Action{}
	for _, s := range r.Suggestions {
		got = append(got, s.Action)
	}
	if diff := cmp.Diff([]planner.Action{planner.ActionRollOver, planner.ActionDrop, planner.ActionRollOver}, got); diff != "" {
		t.Errorf("actions (-want +got):\n%s", diff)
	}
}

func TestReviewWithoutAProviderRollsEverythingOver(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	a := f.task(t, service.TaskFields{Title: "a"})
	f.planDay(t, f.start(t, 3, ""), a)

	r, err := f.planner.WrapReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Fallback || len(r.Suggestions) != 1 || r.Suggestions[0] != (planner.Suggestion{TaskID: a.ID, Action: planner.ActionRollOver}) {
		t.Errorf("review = %+v", r)
	}
}

func TestReviewSkipsTasksThatAreNoLongerUnfinished(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	done := f.task(t, service.TaskFields{Title: "done"})
	pushed := f.task(t, service.TaskFields{Title: "pushed"})
	gone := f.task(t, service.TaskFields{Title: "deleted"})
	dropped := f.task(t, service.TaskFields{Title: "dropped"})
	f.planDay(t, f.start(t, 3, ""), done, pushed, gone, dropped)
	f.finish(t, done)
	f.write(t, func(b int64) error { _, err := f.svc.PushTask(ctx, b, pushed.ID, "2026-10-08"); return err })
	f.write(t, func(b int64) error { return f.svc.DeleteTask(ctx, b, gone.ID) })
	f.write(t, func(b int64) error {
		_, err := f.svc.SetTaskStatus(ctx, b, dropped.ID, domain.StatusDropped)
		return err
	})

	r, err := f.planner.WrapReview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Unfinished) != 0 || len(r.Suggestions) != 0 || len(f.model.sent) != 0 {
		t.Errorf("review = %+v after %d model calls", r, len(f.model.sent))
	}
}

func TestWrapReviewNeedsAStartedDay(t *testing.T) {
	f := newFixture(t)
	if _, err := f.planner.WrapReview(context.Background()); !errors.Is(err, planner.ErrNoDay) {
		t.Errorf("err = %v, want ErrNoDay", err)
	}
}

func TestMorningReviewReconcilesOnlyAnUnwrappedLastDay(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	if r, err := f.planner.MorningReview(ctx); r != nil || err != nil {
		t.Errorf("first ever morning: %+v, %v", r, err)
	}

	a := f.task(t, service.TaskFields{Title: "a"})
	friday, _ := f.svc.StartDay(ctx, "2026-10-02", 3, "")
	f.planDay(t, friday, a)
	r, err := f.planner.MorningReview(ctx)
	if err != nil || r == nil || r.Day.Date != "2026-10-02" || len(r.Unfinished) != 1 {
		t.Fatalf("unwrapped friday: %+v, %v", r, err)
	}

	if _, err := f.svc.WrapDay(ctx, friday.ID, ""); err != nil {
		t.Fatal(err)
	}
	if r, err := f.planner.MorningReview(ctx); r != nil || err != nil {
		t.Errorf("wrapped friday: %+v, %v", r, err)
	}
}
