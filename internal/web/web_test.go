package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHandler(t *testing.T) {
	built := fstest.MapFS{
		"200.html":            {Data: []byte("shell")},
		"_app/immutable/a.js": {Data: []byte("js")},
		"robots.txt":          {Data: []byte("robots")},
	}
	tests := []struct {
		name     string
		files    fstest.MapFS
		path     string
		wantCode int
		wantBody string
	}{
		{"an asset", built, "/_app/immutable/a.js", http.StatusOK, "js"},
		{"a static file", built, "/robots.txt", http.StatusOK, "robots"},
		{"the root gets the shell", built, "/", http.StatusOK, "shell"},
		{"a client route gets the shell", built, "/settings", http.StatusOK, "shell"},
		{"a directory gets the shell, not a listing", built, "/_app/", http.StatusOK, "shell"},
		{"an unknown API route is not the app", built, "/api/v1/nope", http.StatusNotFound, "404"},
		{"an empty build says so", fstest.MapFS{}, "/", http.StatusOK, "UI not built"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, handler(tt.files), tt.path)
			if rec.Code != tt.wantCode || !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("GET %s = %d %q, want %d containing %q", tt.path, rec.Code, rec.Body, tt.wantCode, tt.wantBody)
			}
		})
	}
}

func TestEmbeddedHandlerServes(t *testing.T) {
	if rec := get(t, Handler(), "/"); rec.Code != http.StatusOK {
		t.Errorf("GET / = %d", rec.Code)
	}
}
