package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/integrations"
)

const (
	testKey   = "wn_k"
	testToken = "pat-secret"
)

// memVault is an in-memory secrets store.
type memVault map[string]string

func (m memVault) Get(_ context.Context, name string) (string, error) { return m[name], nil }
func (m memVault) Set(_ context.Context, name, value string) error    { m[name] = value; return nil }
func (m memVault) Delete(_ context.Context, name string) error        { delete(m, name); return nil }

// fakeJira answers every search with two issues of fourteen.
func fakeJira(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"total": 14, "issues": [
			{"key": "FLEX1-1", "fields": {"summary": "Mine", "description": null, "assignee": {"name": "me"}}},
			{"key": "FLEX1-2", "fields": {"summary": "Up for grabs", "description": null, "assignee": null}}
		]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type pushed struct {
	auth    string
	tickets []integrations.Ticket
	more    int
}

// fakeWhatnow answers pushes with status and body, and records what it was sent.
func fakeWhatnow(t *testing.T, status int, contentType, body string) (*httptest.Server, *[]pushed) {
	t.Helper()
	var got []pushed
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/integrations/jira/push" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		var b struct {
			Tickets []integrations.Ticket `json:"tickets"`
			More    int                   `json:"more"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		got = append(got, pushed{r.Header.Get("Authorization"), b.Tickets, b.More})
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestPushSends(t *testing.T) {
	jira := fakeJira(t)
	wn, got := fakeWhatnow(t, http.StatusNoContent, "", "")
	var out bytes.Buffer

	err := push(context.Background(), pushConfig{Whatnow: wn.URL, Jira: jira.URL, JQL: "project = FLEX1"}, testKey, testToken, nil, &out)

	if err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 || (*got)[0].auth != "Bearer "+testKey || len((*got)[0].tickets) != 2 || (*got)[0].more != 12 {
		t.Fatalf("pushed = %+v", *got)
	}
	host := strings.TrimPrefix(wn.URL, "http://")
	if want := "Sent 2 tickets to " + host + "\n12 more matched — narrow the search\n"; out.String() != want {
		t.Errorf("out = %q, want %q", out.String(), want)
	}
}

func TestPushErrors(t *testing.T) {
	jira := fakeJira(t)
	cases := map[string]struct {
		jira        string
		status      int
		contentType string
		body        string
		want        string
	}{
		"jira unreachable": {"http://127.0.0.1:1", http.StatusNoContent, "", "", "Can't reach Jira — on the VPN?"},
		"bad key":          {jira.URL, http.StatusUnauthorized, "application/json", `{"error":"unknown key"}`, "whatnow didn't accept the key — create a new one in Jira settings"},
		"direct mode":      {jira.URL, http.StatusConflict, "application/json", `{"error":"off"}`, "Jira on {host} is set to reach Jira directly — switch it to From my computer"},
		"html page":        {jira.URL, http.StatusOK, "text/html", "<html>hi</html>", "couldn't read whatnow's answer — is the address right?"},
		"html not found":   {jira.URL, http.StatusNotFound, "text/html", "<html>404</html>", "couldn't read whatnow's answer — is the address right?"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			wn, _ := fakeWhatnow(t, c.status, c.contentType, c.body)
			var out bytes.Buffer
			err := push(context.Background(), pushConfig{Whatnow: wn.URL, Jira: c.jira, JQL: "project = FLEX1"}, testKey, testToken, nil, &out)
			want := strings.ReplaceAll(c.want, "{host}", strings.TrimPrefix(wn.URL, "http://"))
			if err == nil || err.Error() != want {
				t.Errorf("err = %v, want %q", err, want)
			}
			for _, secret := range []string{testKey, testToken} {
				if strings.Contains(out.String(), secret) || (err != nil && strings.Contains(err.Error(), secret)) {
					t.Errorf("output or error contains %q", secret)
				}
			}
		})
	}
}

func TestSetupSavesAndPushes(t *testing.T) {
	jira := fakeJira(t)
	wn, got := fakeWhatnow(t, http.StatusNoContent, "", "")
	dir := t.TempDir()
	vault := memVault{}
	var out bytes.Buffer
	in := strings.Join([]string{wn.URL, testKey, jira.URL, testToken, "project = FLEX1"}, "\n") + "\n"

	if err := runJira(context.Background(), []string{"setup"}, strings.NewReader(in), &out, dir, vault, nil); err != nil {
		t.Fatal(err)
	}

	var cfg pushConfig
	b, err := os.ReadFile(filepath.Join(dir, "jira-push.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg != (pushConfig{Whatnow: wn.URL, Jira: jira.URL, JQL: "project = FLEX1"}) {
		t.Errorf("config = %+v", cfg)
	}
	if vault["jira-push/key"] != testKey || vault["jira-push/token"] != testToken {
		t.Errorf("vault = %v", vault)
	}
	if strings.Contains(string(b), testKey) || strings.Contains(string(b), testToken) {
		t.Error("the config file holds a secret")
	}
	if len(*got) != 1 {
		t.Fatalf("setup pushed %d times", len(*got))
	}

	out.Reset()
	if err := runJira(context.Background(), []string{"setup"}, strings.NewReader("\n\n\n\n\n"), &out, dir, vault, nil); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(filepath.Join(dir, "jira-push.json")); !bytes.Equal(b, b2) {
		t.Errorf("rerun changed the config: %s", b2)
	}
	if vault["jira-push/key"] != testKey || vault["jira-push/token"] != testToken {
		t.Errorf("rerun changed the vault: %v", vault)
	}
	if !strings.Contains(out.String(), "saved; enter to keep") {
		t.Errorf("rerun prompts = %q", out.String())
	}
	if len(*got) != 2 {
		t.Errorf("rerun pushed %d times in all", len(*got))
	}
}

func TestSetupDefaultsWhatnowAddress(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	// The push at the end fails, since nothing answers at whatnow.day here; the config is saved first.
	in := "\n" + testKey + "\nhttp://127.0.0.1:1\n" + testToken + "\nproject = FLEX1\n"
	runJira(context.Background(), []string{"setup"}, strings.NewReader(in), &out, dir, memVault{}, nil)

	b, err := os.ReadFile(filepath.Join(dir, "jira-push.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"whatnow":"https://whatnow.day"`) {
		t.Errorf("config = %s", b)
	}
}
