package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestEveryToolIsOffered(t *testing.T) {
	f := newFixture(t, text("Hi."))
	f.send(t, "hi")
	var names []string
	for _, tl := range f.model.chats[0].Tools {
		names = append(names, tl.Name)
	}
	want := []string{"list_tasks", "create_task", "update_task", "complete_task", "drop_task", "push_task", "add_blocker",
		"remove_blocker", "create_event", "update_event", "delete_event", "replan", "what_now", "set_pace", "undo_last"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("tools (-want +got):\n%s", diff)
	}
}

// results runs one round of tool calls and returns what the model got back.
func results(t *testing.T, f *fixture, cs ...llm.ToolCall) []llm.ToolResult {
	t.Helper()
	f.model.replies = append(f.model.replies, calls(cs...), text("ok"))
	f.send(t, "go")
	msgs := f.model.chats[len(f.model.chats)-1].Messages
	return msgs[len(msgs)-1].ToolResults
}

func TestUpdateTaskChangesOnlyTheNamedFields(t *testing.T) {
	f := newFixture(t)
	size := domain.SizeM
	task := f.task(t, service.TaskFields{Title: "Review FLEX1-420", Notes: "the auth bits", Size: &size,
		DueOn: date("2026-10-07"), Refs: []domain.Ref{{Label: "FLEX1-420"}}})

	got := results(t, f, call("c1", "update_task", map[string]any{"task_id": task.ID, "changes": []map[string]string{
		{"field": "due_on", "value": ""}, {"field": "feeling", "value": "dreading it"}}}))
	if got[0].IsError {
		t.Fatal(got[0].Content)
	}
	after, _ := f.svc.GetTask(context.Background(), task.ID)
	if after.DueOn != nil || after.Feeling != "dreading it" || after.Notes != "the auth bits" || after.Size == nil ||
		len(after.Refs) != 1 {
		t.Errorf("after = %+v", after)
	}

	got = results(t, f,
		call("c2", "update_task", map[string]any{"task_id": task.ID, "changes": []map[string]string{{"field": "status", "value": "done"}}}),
		call("c3", "update_task", map[string]any{"task_id": task.ID, "changes": []map[string]string{{"field": "size", "value": "XL"}}}))
	if !got[0].IsError || !strings.Contains(got[0].Content, `no field "status"`) || !got[1].IsError {
		t.Errorf("results = %+v", got)
	}
}

func TestPushTaskCountsAndMustBeLater(t *testing.T) {
	f := newFixture(t)
	task := f.task(t, service.TaskFields{Title: "Clean the garage"})

	got := results(t, f,
		call("c1", "push_task", map[string]any{"task_id": task.ID, "until": string(today)}),
		call("c2", "push_task", map[string]any{"task_id": task.ID, "until": "2026-10-10"}))
	if !got[0].IsError || got[1].IsError {
		t.Errorf("results = %+v", got)
	}
	counts, _ := f.svc.PushCounts(context.Background())
	if counts[task.ID] != 1 {
		t.Errorf("pushes = %d, want 1", counts[task.ID])
	}
}

func TestBlockers(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "Ship it"})
	b := f.task(t, service.TaskFields{Title: "Research pubsub"})
	pair := map[string]any{"task_id": a.ID, "blocked_by_id": b.ID}
	reversed := map[string]any{"task_id": b.ID, "blocked_by_id": a.ID}

	got := results(t, f, call("c1", "add_blocker", pair), call("c2", "add_blocker", reversed))
	if got[0].IsError || !got[1].IsError {
		t.Errorf("add: %+v", got)
	}
	got = results(t, f, call("c3", "remove_blocker", pair), call("c4", "remove_blocker", pair))
	if got[0].IsError || !got[1].IsError {
		t.Errorf("remove: %+v", got)
	}
}

func TestEventsUseLocalTime(t *testing.T) {
	f := newFixture(t)
	got := results(t, f, call("c1", "create_event", map[string]any{"title": "Dentist", "starts_at": "2026-10-07T15:00", "ends_at": ""}))
	if got[0].IsError {
		t.Fatal(got[0].Content)
	}
	var created struct {
		ID       int64  `json:"id"`
		StartsAt string `json:"starts_at"`
	}
	json.Unmarshal([]byte(got[0].Content), &created)
	e, _ := f.svc.GetEvent(context.Background(), created.ID)
	if !e.StartsAt.Equal(time.Date(2026, 10, 7, 20, 0, 0, 0, time.UTC)) || created.StartsAt != "2026-10-07T15:00" {
		t.Errorf("starts at %v, reported %s", e.StartsAt, created.StartsAt)
	}

	got = results(t, f,
		call("c2", "update_event", map[string]any{"event_id": e.ID, "changes": []map[string]string{{"field": "ends_at", "value": "2026-10-07T16:00"}}}),
		call("c3", "update_event", map[string]any{"event_id": e.ID, "changes": []map[string]string{{"field": "starts_at", "value": "3pm"}}}),
		call("c4", "update_event", map[string]any{"event_id": e.ID, "changes": []map[string]string{{"field": "starts_at", "value": ""}}}))
	if got[0].IsError || !strings.Contains(got[1].Content, "not YYYY-MM-DDTHH:MM") || !got[2].IsError {
		t.Errorf("updates = %+v", got)
	}
	e, _ = f.svc.GetEvent(context.Background(), e.ID)
	if e.EndsAt == nil || !e.EndsAt.Equal(time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)) {
		t.Errorf("ends at %v", e.EndsAt)
	}

	got = results(t, f, call("c5", "delete_event", map[string]any{"event_id": e.ID}), call("c6", "delete_event", map[string]any{"event_id": e.ID}))
	if got[0].IsError || !got[1].IsError {
		t.Errorf("deletes = %+v", got)
	}
}

func TestPaceAndPlanningNeedAStartedDay(t *testing.T) {
	f := newFixture(t)
	f.task(t, service.TaskFields{Title: "Write the RFC", DueOn: date(string(today))})

	got := results(t, f,
		call("c1", "set_pace", map[string]any{"level": 2, "note": ""}),
		call("c2", "replan", map[string]any{"reason": "feeling rough"}))
	for _, r := range got {
		if !r.IsError || !strings.Contains(r.Content, "today hasn't started") {
			t.Errorf("result = %+v", r)
		}
	}

	if _, err := f.svc.StartDay(context.Background(), today, 3, ""); err != nil {
		t.Fatal(err)
	}
	f.model.completions = []any{map[string]any{"entries": []any{map[string]any{"task_id": 1, "rationale": "due", "tier": "must"}}, "opening": "Gently."}}
	got = results(t, f,
		call("c3", "set_pace", map[string]any{"level": 2, "note": "slept badly"}),
		call("c4", "replan", map[string]any{"reason": "feeling rough"}))
	if got[0].IsError || got[1].IsError {
		t.Fatalf("results = %+v", got)
	}
	day, _ := f.svc.GetDay(context.Background(), today)
	if day.Pace != 2 || day.MoodNote != "slept badly" {
		t.Errorf("day = %+v", day)
	}
	plan, _ := f.svc.LatestPlan(context.Background(), day.ID)
	if plan.Trigger != domain.TriggerAgent || plan.Reason != "feeling rough" || plan.Opening != "Gently." {
		t.Errorf("plan = %+v", plan)
	}
}

func TestWhatNow(t *testing.T) {
	f := newFixture(t)
	task := f.task(t, service.TaskFields{Title: "Water plants"})
	f.model.completions = []any{map[string]any{"task_id": task.ID, "reason": "quick win"}}

	got := results(t, f, call("c1", "what_now", map[string]any{}))
	if got[0].IsError || !strings.Contains(got[0].Content, "quick win") {
		t.Errorf("result = %+v", got[0])
	}
}

func TestUndoLastLeavesThisTurnAlone(t *testing.T) {
	f := newFixture(t)
	add := func(id, title string) llm.ToolCall {
		return call(id, "create_task", map[string]any{"title": title, "notes": "", "size": "", "energy": "", "feeling": "",
			"due_on": "", "not_before": ""})
	}
	results(t, f, add("c1", "From the last turn"))
	undone, unsub := f.hub.Subscribe("batch.undone")
	defer unsub()

	got := results(t, f, add("c2", "From this turn"), call("c3", "undo_last", map[string]any{}))
	if got[1].IsError {
		t.Fatal(got[1].Content)
	}

	var titles []string
	tasks, _ := f.svc.ListTasks(context.Background())
	for _, task := range tasks {
		titles = append(titles, task.Title)
	}
	if diff := cmp.Diff([]string{"From this turn"}, titles); diff != "" {
		t.Errorf("tasks (-want +got):\n%s", diff)
	}
	if len(drain(undone)) != 1 {
		t.Error("the undo wasn't published")
	}

	got = results(t, f, call("c4", "undo_last", map[string]any{}), call("c5", "undo_last", map[string]any{}))
	if got[0].IsError || !got[1].IsError {
		t.Errorf("undoing until nothing is left: %+v", got)
	}
}
