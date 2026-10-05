package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
)

type wrapped struct {
	BatchID int64      `json:"batch_id"`
	Data    domain.Day `json:"data"`
}

func TestWrapUpToday(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodGet, "/api/v1/today/wrap", nil), http.StatusConflict)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/wrap", map[string]any{}), http.StatusConflict)

	rfc := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "write the RFC"}))
	old := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "old idea"}))
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3}), http.StatusCreated)

	// No provider: suggestions roll everything over.
	review := decodeAs[planner.Review](t, api.do(http.MethodGet, "/api/v1/today/wrap", nil))
	if len(review.Unfinished) != 2 || len(review.Suggestions) != 2 || !review.Fallback {
		t.Errorf("review = %+v", review)
	}

	rec := api.do(http.MethodPost, "/api/v1/today/wrap", map[string]any{
		"triage": []any{map[string]any{"task_id": old.Data.ID, "action": "drop"},
			map[string]any{"task_id": rfc.Data.ID, "action": "push", "until": "2026-10-07"}},
		"note": "coffee first",
	})
	wantStatus(t, rec, http.StatusOK)
	got := decodeAs[wrapped](t, rec)
	if got.BatchID == 0 || got.Data.WrappedAt == nil || got.Data.TomorrowNote != "coffee first" {
		t.Errorf("wrap = %+v", got)
	}

	// One undo takes back the whole triage.
	wantStatus(t, api.do(http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/undo", got.BatchID), nil), http.StatusNoContent)
	for _, task := range decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil)) {
		if task.Status != domain.StatusOpen || task.NotBefore != nil {
			t.Errorf("after undo: %+v", task)
		}
	}

	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/wrap", map[string]any{}), http.StatusConflict)
}

func TestWrapUpRejectsBadTriage(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3}), http.StatusCreated)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/wrap", map[string]any{"triage": []any{
		map[string]any{"task_id": 1, "action": "later"}}}), http.StatusBadRequest)
	if today := decodeAs[planner.Today](t, api.do(http.MethodGet, "/api/v1/today", nil)); today.Day.WrappedAt != nil {
		t.Error("a rejected wrap-up wrapped the day")
	}
}

func TestMorningReconcilesAnUnwrappedDay(t *testing.T) {
	api := newAPI(t)
	ctx := context.Background()
	morning := func() *planner.Review {
		t.Helper()
		return decodeAs[struct {
			Review *planner.Review `json:"review"`
		}](t, api.do(http.MethodGet, "/api/v1/morning", nil)).Review
	}
	if r := morning(); r != nil {
		t.Errorf("nothing to reconcile, got %+v", r)
	}

	rfc := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "write the RFC"}))
	friday, err := api.svc.StartDay(ctx, "2026-10-02", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	api.svc.SavePlan(ctx, domain.Plan{DayID: friday.ID, Trigger: domain.TriggerMorning,
		Entries: []domain.PlanEntry{{TaskID: rfc.Data.ID, Tier: domain.TierMust}}})
	r := morning()
	if r == nil || r.Day.Date != "2026-10-02" || len(r.Unfinished) != 1 || r.Suggestions[0].Action != planner.ActionRollOver {
		t.Fatalf("review = %+v", r)
	}

	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3,
		"triage": []any{map[string]any{"task_id": rfc.Data.ID, "action": "drop"}}}), http.StatusCreated)
	if r := morning(); r != nil {
		t.Errorf("friday should be reconciled, got %+v", r)
	}
	if got, _ := api.svc.GetTask(ctx, rfc.Data.ID); got.Status != domain.StatusDropped {
		t.Errorf("rfc = %s", got.Status)
	}
}
