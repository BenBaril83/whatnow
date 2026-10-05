package httpapi_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamDeliversWrites(t *testing.T) {
	api := newAPI(t)
	srv := httptest.NewServer(api.handler) // listens on 127.0.0.1, so the guard lets it through
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	lines := bufio.NewReader(resp.Body)
	if first, _ := lines.ReadString('\n'); first != ": connected\n" {
		t.Fatalf("first line = %q", first)
	}

	post, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tasks", strings.NewReader(`{"raw_text":"buy oat milk"}`))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("Content-Type", "application/json")
	postResp, err := http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()

	var event, data string
	for event == "" || data == "" {
		line, err := lines.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before the event arrived: %v", err)
		}
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			data = line
		}
	}
	if event != "batch.applied" || !strings.Contains(data, "buy oat milk") {
		t.Errorf("event %q, data %q", event, data)
	}
}
