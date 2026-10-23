package host

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/integrations"
)

type keyView struct {
	Key *PushKey `json:"key"`
}

// pushRequest is what `whatnow jira push` sends: no cookie and no Origin, just the key.
func pushRequest(t *testing.T, key string, tickets ...string) *http.Request {
	t.Helper()
	body := map[string]any{"tickets": []integrations.Ticket{}, "more": 0}
	var tks []integrations.Ticket
	for _, k := range tickets {
		tks = append(tks, integrations.Ticket{Key: k, Title: k + " pushed", URL: "https://jira.test/browse/" + k})
	}
	if tks != nil {
		body["tickets"] = tks
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/jira/push", bytes.NewReader(b))
	req.Host = "whatnow.test"
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req
}

// setUpComputerJira turns the user's Jira on, reached from their computer, and returns a key.
func (f *frontTest) setUpComputerJira(c *http.Cookie) string {
	f.t.Helper()
	f.want(f.do(http.MethodPut, "/api/v1/integrations/jira/via", c, map[string]string{"via": "computer"}), http.StatusOK)
	f.want(f.do(http.MethodPut, "/api/v1/integrations/jira/enabled", c, map[string]bool{"enabled": true}), http.StatusOK)
	return f.createKey(c)
}

func (f *frontTest) createKey(c *http.Cookie) string {
	f.t.Helper()
	w := f.do(http.MethodPost, "/api/v1/integrations/jira/key", c, nil)
	f.want(w, http.StatusOK)
	key := decodeBody[struct {
		Key string `json:"key"`
	}](f.t, w).Key
	if !strings.HasPrefix(key, "wn_") {
		f.t.Fatalf("key = %q", key)
	}
	return key
}

func offeredKeys(t *testing.T, w *httptest.ResponseRecorder) ([]string, *time.Time) {
	t.Helper()
	res := decodeBody[integrations.PullResult](t, w)
	var keys []string
	for _, tk := range res.Sources[0].Tickets {
		keys = append(keys, tk.Key)
	}
	return keys, res.Sources[0].SentAt
}

func TestPushKeyLifecycle(t *testing.T) {
	f := newFrontTest(t)
	_, c := f.signIn("a")
	w := f.do(http.MethodGet, "/api/v1/integrations/jira/key", c, nil)
	f.want(w, http.StatusOK)
	if v := decodeBody[keyView](t, w); v.Key != nil {
		t.Fatalf("before = %+v", v.Key)
	}

	first := f.setUpComputerJira(c)
	v := decodeBody[keyView](t, f.do(http.MethodGet, "/api/v1/integrations/jira/key", c, nil))
	if v.Key == nil || v.Key.CreatedAt.IsZero() || v.Key.LastUsedAt != nil {
		t.Fatalf("after create = %+v", v.Key)
	}

	second := f.createKey(c)
	if second == first {
		t.Fatal("a new key is the old one")
	}
	f.want(f.serve(pushRequest(t, first, "FLEX1-1")), http.StatusUnauthorized)
	f.want(f.serve(pushRequest(t, second, "FLEX1-1")), http.StatusNoContent)

	f.want(f.do(http.MethodDelete, "/api/v1/integrations/jira/key", c, nil), http.StatusNoContent)
	f.want(f.serve(pushRequest(t, second, "FLEX1-1")), http.StatusUnauthorized)
}

func TestPushFlow(t *testing.T) {
	f := newFrontTest(t)
	_, c := f.signIn("a")
	key := f.setUpComputerJira(c)

	f.want(f.serve(pushRequest(t, key, "FLEX1-1", "FLEX1-2")), http.StatusNoContent)
	w := f.do(http.MethodPost, "/api/v1/integrations/jira/pull", c, nil)
	f.want(w, http.StatusOK)
	keys, sent := offeredKeys(t, w)
	if fmt.Sprint(keys) != "[FLEX1-1 FLEX1-2]" || sent == nil {
		t.Fatalf("offered %v, sent %v", keys, sent)
	}

	add := []integrations.Ticket{{Key: "FLEX1-1", Title: "FLEX1-1 pushed"}}
	f.want(f.do(http.MethodPost, "/api/v1/integrations/jira/add", c, map[string]any{"tickets": add}), http.StatusOK)
	f.want(f.do(http.MethodPost, "/api/v1/integrations/jira/skipped", c, map[string]string{"key": "FLEX1-2", "title": "FLEX1-2 pushed"}), http.StatusOK)

	f.want(f.serve(pushRequest(t, key, "FLEX1-1", "FLEX1-2")), http.StatusNoContent)
	if keys, _ := offeredKeys(t, f.do(http.MethodPost, "/api/v1/integrations/jira/pull", c, nil)); len(keys) != 0 {
		t.Errorf("offered after add and skip: %v", keys)
	}
	v := decodeBody[keyView](t, f.do(http.MethodGet, "/api/v1/integrations/jira/key", c, nil))
	if v.Key == nil || v.Key.LastUsedAt == nil {
		t.Errorf("key = %+v, want it used", v.Key)
	}
}

func TestPushRefused(t *testing.T) {
	f := newFrontTest(t)
	_, c := f.signIn("a")
	key := f.setUpComputerJira(c)

	f.want(f.serve(pushRequest(t, "", "FLEX1-1")), http.StatusUnauthorized)
	f.want(f.serve(pushRequest(t, "wn_nope", "FLEX1-1")), http.StatusUnauthorized)

	cookieOnly := pushRequest(t, "", "FLEX1-1")
	cookieOnly.AddCookie(c)
	cookieOnly.Header.Set("Origin", publicURL)
	f.want(f.serve(cookieOnly), http.StatusUnauthorized)

	push := map[string]any{"tickets": []integrations.Ticket{{Key: "FLEX1-1", Title: "t"}}, "more": 0}
	f.want(f.do(http.MethodPost, "/api/v1/integrations/jira/received", c, push), http.StatusNotFound)

	wrongHost := pushRequest(t, key, "FLEX1-1")
	wrongHost.Host = "evil.test"
	f.want(f.serve(wrongHost), http.StatusForbidden)

	notJSON := pushRequest(t, key, "FLEX1-1")
	notJSON.Header.Set("Content-Type", "text/plain")
	f.want(f.serve(notJSON), http.StatusForbidden)

	tasks := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	tasks.Host = "whatnow.test"
	tasks.Header.Set("Authorization", "Bearer "+key)
	f.want(f.serve(tasks), http.StatusUnauthorized)

	f.want(f.do(http.MethodPut, "/api/v1/integrations/jira/via", c, map[string]string{"via": "direct"}), http.StatusOK)
	f.want(f.serve(pushRequest(t, key, "FLEX1-1")), http.StatusConflict)
}

func TestPushRateLimited(t *testing.T) {
	f := newFrontTest(t)
	_, c := f.signIn("a")
	key := f.setUpComputerJira(c)
	// Setting up spent none of the burst: none of its requests is metered.
	for i := range 30 {
		if w := f.serve(pushRequest(t, key, "FLEX1-1")); w.Code != http.StatusNoContent {
			t.Fatalf("push %d: status %d", i+1, w.Code)
		}
	}
	f.want(f.serve(pushRequest(t, key, "FLEX1-1")), http.StatusTooManyRequests)
}

func TestDeleteAccountDeletesPushKey(t *testing.T) {
	f := newFrontTest(t)
	_, c := f.signIn("a")
	key := f.setUpComputerJira(c)
	f.want(f.do(http.MethodDelete, "/api/v1/data", c, nil), http.StatusNoContent)
	f.want(f.serve(pushRequest(t, key, "FLEX1-1")), http.StatusUnauthorized)
}
