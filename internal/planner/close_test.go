package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

func until(d string) *domain.Date { v := domain.Date(d); return &v }

func TestCloseAppliesTriageWrapsAndSummarizes(t *testing.T) {
	f := newFixture(t, "A steady day: shipped it, and the RFC rolls on.")
	ctx := context.Background()
	shipped := f.task(t, service.TaskFields{Title: "ship it"})
	rfc := f.task(t, service.TaskFields{Title: "write the RFC"})
	taxes := f.task(t, service.TaskFields{Title: "do taxes"})
	gym := f.task(t, service.TaskFields{Title: "gym"})
	old := f.task(t, service.TaskFields{Title: "old idea"})
	day := f.start(t, 3, "a bit tired")
	f.planDay(t, day, shipped, rfc, taxes, gym, old)
	f.finish(t, shipped)
	msgs, unsub := f.hub.Subscribe()
	defer unsub()

	batch, wrapped, err := f.planner.Close(ctx, day, []planner.Triage{
		{TaskID: rfc.ID, Action: planner.ActionRollOver},
		{TaskID: taxes.ID, Action: planner.ActionPush, Until: until("2026-10-12")},
		{TaskID: gym.ID, Action: planner.ActionPush},
		{TaskID: old.ID, Action: planner.ActionDrop},
	}, "start with the RFC")
	if err != nil {
		t.Fatal(err)
	}

	if wrapped.WrappedAt == nil || wrapped.TomorrowNote != "start with the RFC" ||
		wrapped.Summary != "A steady day: shipped it, and the RFC rolls on." {
		t.Errorf("wrapped = %+v", wrapped)
	}
	if stored, _ := f.svc.GetDay(ctx, today); stored.Summary != wrapped.Summary {
		t.Errorf("stored summary = %q", stored.Summary)
	}
	check := func(task domain.Task, status domain.Status, notBefore string) {
		t.Helper()
		got, _ := f.svc.GetTask(ctx, task.ID)
		nb := ""
		if got.NotBefore != nil {
			nb = string(*got.NotBefore)
		}
		if got.Status != status || nb != notBefore {
			t.Errorf("%s: status %s, not_before %q", task.Title, got.Status, nb)
		}
	}
	check(rfc, domain.StatusOpen, "")
	check(taxes, domain.StatusOpen, "2026-10-12")
	check(gym, domain.StatusOpen, "2026-10-06")
	check(old, domain.StatusDropped, "")

	m := <-msgs
	if applied, ok := m.Data.(stream.BatchApplied); !ok || applied.BatchID != batch || len(applied.Activity) != 3 {
		t.Errorf("published %+v", m)
	}

	req := f.model.sent[0]
	if req.Model != "claude-haiku-4-5" || req.Schema != nil || req.Fallback {
		t.Errorf("summary request: %+v", req)
	}
	for _, want := range []string{"Day: Monday 2026-10-05.", `Pace: "steady"`, `On their mind that morning: "a bit tired".`,
		"Done: ship it.", "write the RFC (rolled over); do taxes (pushed to 2026-10-12); gym (pushed to 2026-10-06); old idea (dropped).",
		`Their note for next time: "start with the RFC".`} {
		if !strings.Contains(req.User, want) {
			t.Errorf("summary input is missing %q:\n%s", want, req.User)
		}
	}
}

func TestBadTriageChangesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.task(t, service.TaskFields{Title: "a"})
	day := f.start(t, 3, "")
	f.planDay(t, day, a)

	cases := map[string][]planner.Triage{
		"unknown action": {{TaskID: a.ID, Action: "postpone"}},
		"twice":          {{TaskID: a.ID, Action: planner.ActionDrop}, {TaskID: a.ID, Action: planner.ActionPush}},
		"bad date":       {{TaskID: a.ID, Action: planner.ActionPush, Until: until("next week")}},
	}
	for name, triage := range cases {
		if _, _, err := f.planner.Close(ctx, day, triage, ""); !errors.Is(err, service.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	// A task that isn't unfinished rejects the whole triage before anything is written.
	_, _, err := f.planner.Close(ctx, day, []planner.Triage{{TaskID: a.ID, Action: planner.ActionDrop},
		{TaskID: 99, Action: planner.ActionDrop}}, "")
	if !errors.Is(err, service.ErrConflict) {
		t.Errorf("missing task: err = %v, want ErrConflict", err)
	}
	if got, _ := f.svc.GetTask(ctx, a.ID); got.Status != domain.StatusOpen {
		t.Errorf("task left %s", got.Status)
	}
	if got, _ := f.svc.GetDay(ctx, today); got.WrappedAt != nil {
		t.Error("day was wrapped despite the failed triage")
	}
}

func TestCloseTwiceConflicts(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	day := f.start(t, 3, "")
	_, wrapped, err := f.planner.Close(context.Background(), day, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.planner.Close(context.Background(), wrapped, nil, ""); !errors.Is(err, service.ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestAFailedSummaryStillWrapsTheDay(t *testing.T) {
	for name, f := range map[string]*fixture{
		"provider error": newFixture(t, errors.New("overloaded")),
		"no provider":    func() *fixture { f := newFixture(t); f.configured = false; return f }(),
	} {
		day := f.start(t, 3, "")
		batch, wrapped, err := f.planner.Close(context.Background(), day, nil, "rest")
		if err != nil || batch != 0 || wrapped.WrappedAt == nil || wrapped.Summary != "" {
			t.Errorf("%s: batch %d, day %+v, err %v", name, batch, wrapped, err)
		}
	}
}

func TestStartDayReconcilesTheUnwrappedLastDay(t *testing.T) {
	f := newFixture(t, "Friday slipped; the RFC carried over.", map[string]any{"entries": []any{}, "opening": "Morning."})
	ctx := context.Background()
	rfc := f.task(t, service.TaskFields{Title: "write the RFC"})
	friday, _ := f.svc.StartDay(ctx, "2026-10-02", 2, "")
	f.planDay(t, friday, rfc)

	_, plan, err := f.planner.StartDay(ctx, 3, "", []planner.Triage{{TaskID: rfc.ID, Action: planner.ActionPush}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.GetDay(ctx, "2026-10-02")
	if got.WrappedAt == nil || got.Summary != "Friday slipped; the RFC carried over." {
		t.Errorf("friday = %+v", got)
	}
	if task, _ := f.svc.GetTask(ctx, rfc.ID); task.NotBefore == nil || *task.NotBefore != "2026-10-06" {
		t.Errorf("rfc not_before = %v", task.NotBefore)
	}
	// The plan sees what the summary says about Friday.
	if plan.Opening != "Morning." || !strings.Contains(system(f.model.sent[1]),
		"Last day they used this, 2026-10-02: Friday slipped; the RFC carried over.") {
		t.Errorf("plan %+v; system:\n%s", plan, system(f.model.sent[1]))
	}
}

func TestStartDayIgnoresTriageWithNothingToReconcile(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	a := f.task(t, service.TaskFields{Title: "a"})
	if _, _, err := f.planner.StartDay(context.Background(), 3, "", []planner.Triage{{TaskID: a.ID, Action: planner.ActionDrop}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.GetTask(context.Background(), a.ID); got.Status != domain.StatusOpen {
		t.Errorf("task = %s", got.Status)
	}
}

func TestThePlanSeesTheNoteLeftForNextTime(t *testing.T) {
	f := newFixture(t, map[string]any{"entries": []any{}, "opening": "hi"})
	ctx := context.Background()
	friday, _ := f.svc.StartDay(ctx, "2026-10-02", 3, "")
	f.svc.WrapDay(ctx, friday.ID, "call the vet first")
	f.start(t, 3, "")

	if _, err := f.planner.Replan(ctx, domain.TriggerMorning, ""); err != nil {
		t.Fatal(err)
	}
	if sys := system(f.model.sent[0]); !strings.Contains(sys, `The note they left for next time: "call the vet first"`) {
		t.Errorf("system prompt:\n%s", sys)
	}
}

func TestNoReplanAfterTheDayIsWrapped(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	must := f.task(t, service.TaskFields{Title: "must", DueOn: date("2026-10-05")})
	day := f.start(t, 3, "")
	if _, err := f.planner.Replan(ctx, domain.TriggerMorning, ""); err != nil {
		t.Fatal(err)
	}
	plans := f.run(t)

	// Dropping a must would replan a live day; at wrap-up it must not.
	if _, _, err := f.planner.Close(ctx, day, []planner.Triage{{TaskID: must.ID, Action: planner.ActionDrop}}, ""); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case m := <-plans:
			if p, ok := m.Data.(domain.Plan); ok {
				t.Fatalf("unexpected replan: %+v", p)
			}
		case <-timeout:
			return
		}
	}
}

func TestStaleTriageIsRejected(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	finished := f.task(t, service.TaskFields{Title: "finished on the phone"})
	unplanned := f.task(t, service.TaskFields{Title: "never planned"})
	day := f.start(t, 3, "")
	f.planDay(t, day, finished)
	f.finish(t, finished)

	for name, triage := range map[string][]planner.Triage{
		"drop a task done since the review": {{TaskID: finished.ID, Action: planner.ActionDrop}},
		"push a task that wasn't planned":   {{TaskID: unplanned.ID, Action: planner.ActionPush}},
	} {
		if _, _, err := f.planner.Close(ctx, day, triage, ""); !errors.Is(err, service.ErrConflict) {
			t.Errorf("%s: err = %v, want ErrConflict", name, err)
		}
	}
	if got, _ := f.svc.GetTask(ctx, finished.ID); got.Status != domain.StatusDone {
		t.Errorf("finished task is now %s", got.Status)
	}
	if got, _ := f.svc.GetTask(ctx, unplanned.ID); got.NotBefore != nil {
		t.Errorf("unplanned task pushed to %s", *got.NotBefore)
	}
	if got, _ := f.svc.GetDay(ctx, today); got.WrappedAt != nil {
		t.Error("day was wrapped despite the stale triage")
	}
}

// leaving is a model that sees the user leave mid-call: it cancels the request's context, then
// answers only if its own context outlived that.
type leaving struct{ cancel context.CancelFunc }

func (l leaving) Complete(ctx context.Context, _ llm.Request) (string, error) {
	l.cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "Wrapped up early and went for a walk.", nil
}

func TestTheSummarySurvivesTheClientLeaving(t *testing.T) {
	f := newFixture(t)
	day := f.start(t, 3, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.slow = leaving{cancel: cancel}

	if _, _, err := f.planner.Close(ctx, day, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.GetDay(context.Background(), today); got.Summary != "Wrapped up early and went for a walk." {
		t.Errorf("summary = %q", got.Summary)
	}
}
