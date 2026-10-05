package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

var testLoc = time.FixedZone("UTC-5", -5*3600)

type testAPI struct {
	t       *testing.T
	handler http.Handler
	hub     *stream.Hub
	// svc sets up state the API can't, such as days before today.
	svc      *service.Service
	captured []int64
	// model answers the connection test and the planner. Nil means no provider is set up.
	model llm.Client
}

func newAPI(t *testing.T) *testAPI {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := func() time.Time { return time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC) } // 09:00 local
	svc := service.New(db, now)
	api := &testAPI{t: t, hub: stream.NewHub(), svc: svc}
	active := func(context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
		if api.model == nil {
			return nil, settings.ProviderConfig{}, settings.Defaults(), settings.ErrNotConfigured
		}
		return api.model, settings.ProviderConfig{MainModel: "main"}, settings.Defaults(), nil
	}
	api.handler = httpapi.New(httpapi.Deps{
		Service:   svc,
		Hub:       api.hub,
		DB:        db,
		Loc:       testLoc,
		OnCapture: func(task domain.Task) { api.captured = append(api.captured, task.ID) },
		NewClient: func(settings.Settings) (llm.Client, settings.ProviderConfig, error) {
			if api.model == nil {
				return nil, settings.ProviderConfig{}, settings.ErrNotConfigured
			}
			return api.model, settings.ProviderConfig{MainModel: "main"}, nil
		},
		Planner: planner.New(svc, api.hub, active, testLoc, now),
	})
	return api
}

// do sends a request the way the web client will: from a loopback host, with a JSON body type on
// every write.
func (a *testAPI) do(method, path string, body any) *httptest.ResponseRecorder {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = "127.0.0.1:7777" // httptest defaults to example.com, which the guard rejects
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func decodeAs[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
}
