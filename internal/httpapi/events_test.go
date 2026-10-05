package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
)

type eventMutation struct {
	BatchID int64        `json:"batch_id"`
	Data    domain.Event `json:"data"`
}

func TestEventsByLocalDate(t *testing.T) {
	api := newAPI(t)
	create := func(title string, start time.Time) domain.Event {
		t.Helper()
		rec := api.do(http.MethodPost, "/api/v1/events", map[string]any{"title": title, "starts_at": start})
		wantStatus(t, rec, http.StatusCreated)
		return decodeAs[eventMutation](t, rec).Data
	}
	// testLoc is UTC-5, so 23:00 local on the 5th is 04:00 UTC on the 6th.
	create("standup", time.Date(2026, 10, 5, 10, 0, 0, 0, testLoc))
	late := create("late call", time.Date(2026, 10, 5, 23, 0, 0, 0, testLoc))
	create("tomorrow", time.Date(2026, 10, 6, 9, 0, 0, 0, testLoc))

	got := decodeAs[[]domain.Event](t, api.do(http.MethodGet, "/api/v1/events?date=2026-10-05", nil))
	if len(got) != 2 || got[1].ID != late.ID {
		t.Fatalf("events for the 5th = %+v", got)
	}

	path := fmt.Sprintf("/api/v1/events/%d", late.ID)
	moved := api.do(http.MethodPut, path, map[string]any{"title": "late call", "starts_at": time.Date(2026, 10, 6, 8, 0, 0, 0, testLoc)})
	wantStatus(t, moved, http.StatusOK)
	wantStatus(t, api.do(http.MethodDelete, path, nil), http.StatusOK)
	if got := decodeAs[[]domain.Event](t, api.do(http.MethodGet, "/api/v1/events?date=2026-10-06", nil)); len(got) != 1 {
		t.Errorf("events for the 6th = %+v, want only tomorrow", got)
	}
}

func TestEventErrors(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodGet, "/api/v1/events", nil), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodGet, "/api/v1/events?date=today", nil), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/events", map[string]any{"title": "no start"}), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodDelete, "/api/v1/events/999", nil), http.StatusNotFound)
}
