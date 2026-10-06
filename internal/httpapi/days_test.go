package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/stream"
)

type started struct {
	Day  domain.Day  `json:"day"`
	Plan domain.Plan `json:"plan"`
}

func TestStartTheDayThenReadToday(t *testing.T) {
	api := newAPI(t)
	due := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "ship it", "due_on": "2026-10-05"}))
	research := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "research"}))
	api.do(http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/blockers", due.Data.ID), map[string]int64{"blocked_by_id": research.Data.ID})

	before := decodeAs[planner.Today](t, api.do(http.MethodGet, "/api/v1/today", nil))
	if before.Date != "2026-10-05" || before.Day != nil || before.Plan != nil || len(before.Unplanned) != 2 {
		t.Errorf("before starting: %+v", before)
	}

	// No provider is set up, so the first plan is the fallback.
	rec := api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 2, "mood_note": "tired"})
	wantStatus(t, rec, http.StatusCreated)
	got := decodeAs[started](t, rec)
	if got.Day.Pace != 2 || got.Day.MoodNote != "tired" || !got.Plan.Fallback || len(got.Plan.Entries) != 2 {
		t.Errorf("started: %+v", got)
	}

	today := decodeAs[planner.Today](t, api.do(http.MethodGet, "/api/v1/today", nil))
	if today.Plan == nil || today.Plan.ID != got.Plan.ID || len(today.Unplanned) != 0 ||
		len(today.Blocked) != 1 || today.Blocked[0] != due.Data.ID {
		t.Errorf("today: %+v", today)
	}

	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3}), http.StatusConflict)
}

func TestTodayListsTasksAddedAfterThePlanAsUnplanned(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3}), http.StatusCreated)
	added := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "new thing"}))

	today := decodeAs[planner.Today](t, api.do(http.MethodGet, "/api/v1/today", nil))
	if len(today.Unplanned) != 1 || today.Unplanned[0] != added.Data.ID {
		t.Errorf("unplanned = %v", today.Unplanned)
	}
}

func TestSetPace(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/today/pace", map[string]any{"pace": 3}), http.StatusNotFound)
	api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3, "mood_note": "ok"})
	msgs, unsub := api.hub.Subscribe()
	defer unsub()

	rec := api.do(http.MethodPut, "/api/v1/today/pace", map[string]any{"pace": 5})
	wantStatus(t, rec, http.StatusOK)
	got := decodeAs[struct {
		BatchID int64      `json:"batch_id"`
		Data    domain.Day `json:"data"`
	}](t, rec)
	if got.BatchID == 0 || got.Data.Pace != 5 || got.Data.MoodNote != "ok" {
		t.Errorf("pace change: %+v", got)
	}
	if m := <-msgs; m.Type != "batch.applied" {
		t.Errorf("published %q", m.Type)
	}
	wantStatus(t, api.do(http.MethodPut, "/api/v1/today/pace", map[string]any{"pace": 0}), http.StatusBadRequest)
}

func TestWhatNow(t *testing.T) {
	api := newAPI(t)
	first := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "first"}))
	second := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "second"}))
	api.model = modelFunc(func(req llm.Request) (string, error) {
		return fmt.Sprintf(`{"reason":"quick win","task_id":%d}`, first.Data.ID), nil
	})

	rec := api.do(http.MethodPost, "/api/v1/whatnow", map[string]any{})
	wantStatus(t, rec, http.StatusOK)
	if got := decodeAs[planner.Pick](t, rec); got != (planner.Pick{TaskID: first.Data.ID, Reason: "quick win"}) {
		t.Errorf("pick = %+v", got)
	}

	// "Not this one": the reason lands on the task's feeling, and the model's repeat pick is refused.
	msgs, unsub := api.hub.Subscribe()
	defer unsub()
	rec = api.do(http.MethodPost, "/api/v1/whatnow", map[string]any{"exclude_task_id": first.Data.ID, "reason": "too fried"})
	wantStatus(t, rec, http.StatusOK)
	if got := decodeAs[planner.Pick](t, rec); got.TaskID != second.Data.ID || !got.Fallback {
		t.Errorf("pick = %+v", got)
	}
	if m := <-msgs; m.Type != "batch.applied" || len(m.Data.(stream.BatchApplied).Activity) != 1 {
		t.Errorf("published %+v", m)
	}
	list := decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil))
	if list[0].Feeling != "too fried" {
		t.Errorf("feeling = %q", list[0].Feeling)
	}
}

func TestWhatNowWithNothingToDo(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/whatnow", map[string]any{}), http.StatusNotFound)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/whatnow", map[string]any{"exclude_task_id": 99, "reason": "no"}), http.StatusNotFound)
}

func TestPushTask(t *testing.T) {
	api := newAPI(t)
	task := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "rfc"}))
	path := fmt.Sprintf("/api/v1/tasks/%d/push", task.Data.ID)

	got := decodeAs[taskMutation](t, api.do(http.MethodPost, path, map[string]string{"until": "2026-10-06"}))
	if got.Data.NotBefore == nil || *got.Data.NotBefore != "2026-10-06" {
		t.Errorf("pushed: %+v", got.Data)
	}
	wantStatus(t, api.do(http.MethodPost, path, map[string]string{"until": "monday"}), http.StatusBadRequest)
}

func TestReplanSlotsInTheTray(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/replan", nil), http.StatusConflict)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3}), http.StatusCreated)
	added := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "new thing"}))

	rec := api.do(http.MethodPost, "/api/v1/today/replan", nil)
	wantStatus(t, rec, http.StatusOK)
	plan := decodeAs[domain.Plan](t, rec)
	if plan.Trigger != domain.TriggerUser {
		t.Errorf("trigger = %q, want user", plan.Trigger)
	}
	today := decodeAs[planner.Today](t, api.do(http.MethodGet, "/api/v1/today", nil))
	if len(today.Unplanned) != 0 || today.Plan == nil || today.Plan.ID != plan.ID {
		t.Errorf("after replan, unplanned = %v, plan = %+v; want task %d planned", today.Unplanned, today.Plan, added.Data.ID)
	}
}

func TestReplanRefusesAWrappedDay(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3}), http.StatusCreated)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/wrap", map[string]any{"triage": []any{}, "note": ""}), http.StatusOK)

	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/replan", nil), http.StatusConflict)
}
