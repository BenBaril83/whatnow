package httpapi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/httpapi"
)

type taskMutation struct {
	BatchID int64       `json:"batch_id"`
	Data    domain.Task `json:"data"`
}

func TestCaptureCreatesTaskAndPublishes(t *testing.T) {
	api := newAPI(t)
	msgs, unsub := api.hub.Subscribe()
	defer unsub()

	rec := api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"raw_text": "review FLEX1-420"})
	wantStatus(t, rec, http.StatusCreated)
	got := decodeAs[taskMutation](t, rec)
	if got.BatchID == 0 || got.Data.Title != "review FLEX1-420" || got.Data.CreatedBy != domain.ActorUser {
		t.Errorf("unexpected mutation: %+v", got)
	}

	select {
	case m := <-msgs:
		applied, ok := m.Data.(httpapi.BatchApplied)
		if m.Type != "batch.applied" || !ok || applied.BatchID != got.BatchID || len(applied.Activity) != 1 {
			t.Errorf("unexpected message: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no batch.applied published")
	}

	list := decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil))
	if len(list) != 1 {
		t.Errorf("listed %d tasks, want 1", len(list))
	}
}

func TestTaskErrors(t *testing.T) {
	api := newAPI(t)
	cases := []struct {
		name, method, path string
		body               any
		status             int
	}{
		{"empty capture", http.MethodPost, "/api/v1/tasks", map[string]string{"raw_text": "  "}, http.StatusBadRequest},
		{"unknown field", http.MethodPost, "/api/v1/tasks", map[string]string{"titel": "typo"}, http.StatusBadRequest},
		{"non-numeric id", http.MethodPut, "/api/v1/tasks/abc", map[string]string{"title": "x"}, http.StatusBadRequest},
		{"missing task", http.MethodPut, "/api/v1/tasks/999", map[string]string{"title": "x"}, http.StatusNotFound},
		{"bad status", http.MethodPost, "/api/v1/tasks/999/status", map[string]string{"status": "paused"}, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := api.do(c.method, c.path, c.body)
			wantStatus(t, rec, c.status)
			if body := decodeAs[map[string]string](t, rec); body["error"] == "" {
				t.Errorf("error body missing message: %s", rec.Body.String())
			}
		})
	}
}

func TestEditStatusDeleteAndUndo(t *testing.T) {
	api := newAPI(t)
	created := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "draft"}))
	path := fmt.Sprintf("/api/v1/tasks/%d", created.Data.ID)

	edit := decodeAs[taskMutation](t, api.do(http.MethodPut, path, map[string]any{"title": "Draft the RFC", "size": "M"}))
	if edit.Data.Title != "Draft the RFC" {
		t.Fatalf("edit not applied: %+v", edit.Data)
	}
	done := decodeAs[taskMutation](t, api.do(http.MethodPost, path+"/status", map[string]string{"status": "done"}))
	if done.Data.Status != domain.StatusDone {
		t.Fatalf("status = %q", done.Data.Status)
	}

	msgs, unsub := api.hub.Subscribe()
	defer unsub()
	rec := api.do(http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/undo", done.BatchID), nil)
	wantStatus(t, rec, http.StatusNoContent)
	if m := <-msgs; m.Type != "batch.undone" {
		t.Errorf("published %q, want batch.undone", m.Type)
	}

	// The later status batch is undone, so nothing blocks undoing the edit now.
	wantStatus(t, api.do(http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/undo", edit.BatchID), nil), http.StatusNoContent)
	list := decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil))
	if len(list) != 1 || list[0].Title != "draft" || list[0].Status != domain.StatusOpen {
		t.Errorf("after undos: %+v", list)
	}

	wantStatus(t, api.do(http.MethodDelete, path, nil), http.StatusOK)
	if list := decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil)); len(list) != 0 {
		t.Errorf("deleted task still listed")
	}
}

func TestUndoConflictIs409(t *testing.T) {
	api := newAPI(t)
	created := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "a"}))
	path := fmt.Sprintf("/api/v1/tasks/%d", created.Data.ID)
	api.do(http.MethodPut, path, map[string]string{"title": "b"})

	rec := api.do(http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/undo", created.BatchID), nil)
	wantStatus(t, rec, http.StatusConflict)
}

func TestBlockerRoutes(t *testing.T) {
	api := newAPI(t)
	a := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "a"})).Data
	b := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "b"})).Data

	wantStatus(t, api.do(http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/blockers", a.ID), map[string]int64{"blocked_by_id": b.ID}), http.StatusOK)
	got := decodeAs[[]domain.Blocker](t, api.do(http.MethodGet, "/api/v1/blockers", nil))
	if len(got) != 1 || got[0] != (domain.Blocker{TaskID: a.ID, BlockedByID: b.ID}) {
		t.Fatalf("blockers = %+v", got)
	}

	cycle := api.do(http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/blockers", b.ID), map[string]int64{"blocked_by_id": a.ID})
	wantStatus(t, cycle, http.StatusBadRequest)

	wantStatus(t, api.do(http.MethodDelete, fmt.Sprintf("/api/v1/tasks/%d/blockers/%d", a.ID, b.ID), nil), http.StatusOK)
	if got := decodeAs[[]domain.Blocker](t, api.do(http.MethodGet, "/api/v1/blockers", nil)); len(got) != 0 {
		t.Errorf("blocker still listed: %+v", got)
	}
}

func TestRejectsRequestsFromOtherSites(t *testing.T) {
	api := newAPI(t)
	send := func(host, contentType string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"title":"pwned"}`))
		req.Host = host
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		api.handler.ServeHTTP(rec, req)
		return rec.Code
	}

	cases := []struct {
		name, host, contentType string
		status                  int
	}{
		{"rebound DNS name", "evil.example:7777", "application/json", http.StatusForbidden},
		{"cross-site form post", "127.0.0.1:7777", "text/plain", http.StatusUnsupportedMediaType},
		{"no content type", "127.0.0.1:7777", "", http.StatusUnsupportedMediaType},
		{"localhost", "localhost:7777", "application/json", http.StatusCreated},
		{"ipv6 loopback", "[::1]:7777", "application/json; charset=utf-8", http.StatusCreated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := send(c.host, c.contentType); got != c.status {
				t.Errorf("status = %d, want %d", got, c.status)
			}
		})
	}
	if list := decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil)); len(list) != 2 {
		t.Errorf("tasks = %d, want only the 2 legitimate ones", len(list))
	}
}
