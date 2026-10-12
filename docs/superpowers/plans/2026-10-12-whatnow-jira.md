# Jira Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pull tickets from a user's JQL search on Jira Data Center and let them pick which become tasks, from the web morning, web settings, and the TUI morning.

**Architecture:** The integration contract learns that an integration pulls events or tickets. A ticket pull is returned to the screen unsaved, minus tickets already on the list or skipped; a separate `Runner.Add` turns picked tickets into tasks in one batch. `internal/integrations/jira` does one REST search per pull with a Personal Access Token.

**Tech Stack:** Go (net/http, `encoding/json`), SvelteKit 5 + Vitest + Playwright, Bubble Tea.

**Spec:** `docs/superpowers/specs/2026-10-12-whatnow-jira-design.md` (contract in `docs/superpowers/specs/2026-10-12-whatnow-integrations-design.md`)

## Global Constraints

- Auth: `Authorization: Bearer <token>`; Jira Data Center/Server 8.14+. No email, no Cloud.
- One request per pull: `GET {url}/rest/api/2/search`, `fields=summary,description,assignee`, `maxResults=50`, 30-second timeout.
- Task title is `KEY summary`, key first. Ref is `{label: KEY, url: {url}/browse/KEY}`. Notes are the raw description; size and energy blank; no cleanup run.
- Nothing syncs back either way after a pick.
- Errors: "Can't reach Jira — on the VPN?" (no response or timeout), "Jira rejected the token" (401), Jira's `errorMessages` joined (400), "Jira answered <status>" (else). No error contains the token.
- Nothing is prefilled in the fields; nothing starts ticked in a pick list.
- A ticket-only pull writes nothing and opens no batch. Add is one user batch.
- Comments and copy: no dates, names, or history in source. Match surrounding style; new struct fields go after existing blocks so gofmt doesn't realign them.

## Review Focus

1. **A ref label typed in a brain dump in a different case** ("flex1-666") should still hide FLEX1-666 from a pull — compare keys case-insensitively. Test in Task 1.
2. **Add sent tickets that are already on the list or repeated in the request** must add each once. Test in Task 1.
3. **Skipping a ticket has no event**, so `Skip` with event ID 0 must not open a batch or fail with not-found. Test in Task 1.
4. **A Jira URL saved with a trailing slash** must still build `/rest/api/2/search` and `/browse/KEY` without `//`. Test in Task 2.
5. **Jira answering 200 with an HTML login page** (SSO in front of the API) must read as an error, not an empty list. Test in Task 2.

---

### Task 1: Contract — tickets, `Pulls`, `Runner.Add`, ticket skips

**Files:**
- Modify: `internal/integrations/integrations.go`
- Modify: `internal/integrations/runner.go`
- Modify: `internal/integrations/gcal/gcal.go` (add `Pulls`)
- Test: `internal/integrations/runner_test.go`

**Interfaces:**
- Produces:
  ```go
  type Pulls string
  const (PullsEvents Pulls = "events"; PullsTickets Pulls = "tickets")
  // Integration gains: Pulls() Pulls
  type Ticket struct { Key, Title, Notes, URL string } // json: key, title, notes, url
  // Source gains: Tickets []Ticket; More int
  // View gains: Pulls Pulls `json:"pulls"`
  // SourceResult gains: Tickets []Ticket `json:"tickets"`; More int `json:"more"`
  func (r *Runner) Add(ctx context.Context, id string, tickets []Ticket) (int64, []domain.Task, error)
  // Skip(ctx, id, sk, 0) skips without removing an event and returns batch 0.
  ```

- [ ] **Step 1: Write the failing tests**

Add a second fake to `runner_test.go`, register it in `newEnv` beside `fake`, and add `Pulls` to `fake`:

```go
func (*fake) Pulls() integrations.Pulls { return integrations.PullsEvents }

// board is a ticket integration with one required text field. Its pull returns tickets, or err.
type board struct {
	tickets []integrations.Ticket
	more    int
	err     error
}

func (*board) ID() string                { return "board" }
func (*board) Name() string              { return "Board" }
func (*board) Pulls() integrations.Pulls { return integrations.PullsTickets }
func (*board) Fields() []integrations.Field {
	return []integrations.Field{{Key: "jql", Label: "Search", Kind: integrations.Text, Required: true}}
}
func (b *board) Pull(context.Context, integrations.Config, time.Time, time.Time) []integrations.Source {
	return []integrations.Source{{Label: "Board", Tickets: b.tickets, More: b.more, Err: b.err}}
}

func ticket(key string) integrations.Ticket {
	return integrations.Ticket{Key: key, Title: key + " do it", Notes: "notes for " + key, URL: "https://jira.test/browse/" + key}
}
```

`env` gets a `board *board` field; `newEnv` builds `b := &board{}` and passes `f, b` to `NewRunner`. Add a helper:

```go
// setUpBoard saves the board's search and turns it on.
func (e env) setUpBoard(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.runner.Save(ctx, "board", integrations.Update{Values: map[string]string{"jql": "project = X"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.runner.SetEnabled(ctx, "board", true); err != nil {
		t.Fatal(err)
	}
}

// offered lists the keys of a ticket pull.
func offered(res integrations.PullResult) []string {
	var keys []string
	for _, s := range res.Sources {
		for _, tk := range s.Tickets {
			keys = append(keys, tk.Key)
		}
	}
	return keys
}

// task makes a task with the given ref labels, done or not.
func (e env) task(t *testing.T, title string, done bool, refs ...string) domain.Task {
	t.Helper()
	ctx := context.Background()
	batch, _ := e.svc.NewBatch(ctx, domain.ActorUser)
	var rs []domain.Ref
	for _, r := range refs {
		rs = append(rs, domain.Ref{Label: r})
	}
	tk, err := e.svc.CreateTask(ctx, batch, service.TaskFields{Title: title, Refs: rs})
	if err != nil {
		t.Fatal(err)
	}
	if done {
		if tk, err = e.svc.SetTaskStatus(ctx, batch, tk.ID, domain.StatusDone); err != nil {
			t.Fatal(err)
		}
	}
	return tk
}
```

Tests:

```go
func TestViewsSayWhatEachIntegrationPulls(t *testing.T) {
	e := newEnv(t)
	views, err := e.runner.Views(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]integrations.Pulls{}
	for _, v := range views {
		got[v.ID] = v.Pulls
	}
	if diff := cmp.Diff(map[string]integrations.Pulls{"fake": "events", "board": "tickets"}, got); diff != "" {
		t.Errorf("pulls (-want +got):\n%s", diff)
	}
}

func TestATicketPullOffersWhatIsntOnTheListAndWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.setUpBoard(t)
	e.task(t, "typed in a dump", false, "flex1-1")
	e.task(t, "finished", true, "FLEX1-2")
	gone := e.task(t, "deleted", false, "FLEX1-3")
	batch, _ := e.svc.NewBatch(context.Background(), domain.ActorUser)
	if err := e.svc.DeleteTask(context.Background(), batch, gone.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.runner.Skip(context.Background(), "board", integrations.Skip{Key: "FLEX1-4", Title: "nope"}, 0); err != nil {
		t.Fatal(err)
	}
	e.board.tickets = []integrations.Ticket{ticket("FLEX1-1"), ticket("FLEX1-2"), ticket("FLEX1-3"), ticket("FLEX1-4"), ticket("FLEX1-5")}
	e.board.more = 12
	before := e.lastBatch(t)

	res := e.pullFrom(t, "board")

	if diff := cmp.Diff([]string{"FLEX1-3", "FLEX1-5"}, offered(res)); diff != "" {
		t.Errorf("offered (-want +got):\n%s", diff)
	}
	if res.BatchID != 0 || res.Sources[0].More != 12 || res.Sources[0].Events == nil {
		t.Errorf("result = %+v", res)
	}
	if e.lastBatch(t) != before {
		t.Error("a ticket pull opened a batch")
	}
}

func TestATicketPullCarriesItsError(t *testing.T) {
	e := newEnv(t)
	e.setUpBoard(t)
	e.board.err = errors.New("Jira rejected the token")
	res := e.pullFrom(t, "board")
	if res.Sources[0].Error != "Jira rejected the token" || res.Sources[0].Tickets == nil {
		t.Errorf("result = %+v", res)
	}
}

func TestAddMakesTasksInOneBatchThatOneUndoRemoves(t *testing.T) {
	e := newEnv(t)
	e.setUpBoard(t)
	ctx := context.Background()
	e.task(t, "already", false, "FLEX1-1")

	batch, tasks, err := e.runner.Add(ctx, "board", []integrations.Ticket{ticket("FLEX1-1"), ticket("FLEX1-2"), ticket("FLEX1-2"), ticket("FLEX1-3")})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || batch == 0 {
		t.Fatalf("batch %d, tasks %+v", batch, tasks)
	}
	got := tasks[0]
	want := domain.Ref{Label: "FLEX1-2", URL: "https://jira.test/browse/FLEX1-2"}
	if got.Title != "FLEX1-2 do it" || got.Notes != "notes for FLEX1-2" || len(got.Refs) != 1 || got.Refs[0] != want ||
		got.Size != nil || got.Energy != nil || got.CreatedBy != domain.ActorUser {
		t.Errorf("task = %+v", got)
	}

	if _, tasks, err = e.runner.Add(ctx, "board", []integrations.Ticket{ticket("FLEX1-2")}); err != nil || len(tasks) != 0 {
		t.Errorf("adding again: tasks %+v, err %v", tasks, err)
	}

	if _, err := e.svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	all, _ := e.svc.ListTasks(ctx)
	if len(all) != 1 {
		t.Errorf("after undo, tasks = %+v", all)
	}
}

func TestAddNeedsTheIntegrationOnAndAKeyAndTitle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, _, err := e.runner.Add(ctx, "board", []integrations.Ticket{ticket("FLEX1-1")}); !errors.Is(err, service.ErrConflict) {
		t.Errorf("off: err = %v", err)
	}
	e.setUpBoard(t)
	if _, _, err := e.runner.Add(ctx, "board", []integrations.Ticket{{Key: " ", Title: "x"}}); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("no key: err = %v", err)
	}
	if _, _, err := e.runner.Add(ctx, "board", []integrations.Ticket{{Key: "FLEX1-1", Title: ""}}); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("no title: err = %v", err)
	}
}

func TestSkippingATicketRemovesNoEvent(t *testing.T) {
	e := newEnv(t)
	before := e.lastBatch(t)
	batch, err := e.runner.Skip(context.Background(), "board", integrations.Skip{Key: "FLEX1-9", Title: "FLEX1-9 never"}, 0)
	if err != nil || batch != 0 || e.lastBatch(t) != before {
		t.Errorf("batch %d, err %v", batch, err)
	}
	v, _ := e.runner.View(context.Background(), "board")
	if diff := cmp.Diff([]integrations.Skip{{Key: "FLEX1-9", Title: "FLEX1-9 never"}}, v.Skipped); diff != "" {
		t.Errorf("skipped (-want +got):\n%s", diff)
	}
}
```

`pullFrom(t, id)` is `pull` generalized to an ID (make `pull` call `pullFrom(t, "fake")`). `lastBatch` reads `SELECT COALESCE(MAX(id), 0) FROM batches` — add `db *sql.DB` to `env` if the test needs it; check the batches table name in `internal/store/migrations` first. Check `e.svc.Undo`'s real name and signature in `internal/service/undo.go` and use it.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/integrations/ 2>&1 | head -20`
Expected: build failure — `Pulls`, `Ticket`, `Runner.Add` undefined.

- [ ] **Step 3: Implement the contract**

In `integrations.go`, add `Pulls() Pulls` to the interface after `Fields()`, with the comment `// Pulls says whether Pull brings events, which it writes, or tickets, which the user picks from.`, and:

```go
// Pulls is what an integration brings in.
type Pulls string

const (
	// PullsEvents writes today's events as they're pulled.
	PullsEvents Pulls = "events"
	// PullsTickets offers tickets, and only the ones the user picks become tasks, through Add.
	PullsTickets Pulls = "tickets"
)

// Ticket is one pulled ticket. Key is its identity, kept as the ref of the task it becomes.
type Ticket struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Notes string `json:"notes"`
	URL   string `json:"url"`
}
```

`Source` gets, after `Err`:

```go
	// Tickets is what a ticket integration pulled, and More how many matched beyond them.
	Tickets []Ticket
	More    int
```

In `gcal.go`: `func (*Calendar) Pulls() integrations.Pulls { return integrations.PullsEvents }` after `Name`.

- [ ] **Step 4: Implement the runner**

`View` gets `Pulls Pulls `json:"pulls"`` after `Skipped`, set in `view()` from `in.Pulls()`. `SourceResult` gets, after `Events`:

```go
	Tickets []Ticket `json:"tickets"`
	More    int      `json:"more"`
```

In `Pull`, after `sources := in.Pull(ctx, cfg, from, to)`, branch before the event code:

```go
	if in.Pulls() == PullsTickets {
		return r.offer(ctx, s, sources)
	}
```

and fill `Tickets: []Ticket{}` in the event path's `SourceResult` literal so JSON never has `null`. Add:

```go
// offer returns a ticket pull without saving it, leaving out tickets already on the list or
// skipped.
func (r *Runner) offer(ctx context.Context, s saved, sources []Source) (PullResult, error) {
	have, err := r.haveTickets(ctx, s)
	if err != nil {
		return PullResult{}, err
	}
	res := PullResult{Sources: make([]SourceResult, len(sources))}
	for i, src := range sources {
		sr := SourceResult{Label: src.Label, Events: []PulledEvent{}, Tickets: []Ticket{}, More: src.More}
		if src.Err != nil {
			sr.Error = src.Err.Error()
		}
		for _, tk := range src.Tickets {
			if !have[strings.ToUpper(tk.Key)] {
				sr.Tickets = append(sr.Tickets, tk)
			}
		}
		res.Sources[i] = sr
	}
	return res, nil
}

// haveTickets is every key, upper-cased, that a live task has as a ref or the user skipped.
// A deleted task doesn't count, so its ticket can be offered again.
func (r *Runner) haveTickets(ctx context.Context, s saved) (map[string]bool, error) {
	tasks, err := r.svc.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, t := range tasks {
		for _, ref := range t.Refs {
			have[strings.ToUpper(ref.Label)] = true
		}
	}
	for _, sk := range s.Skipped {
		have[strings.ToUpper(sk.Key)] = true
	}
	return have, nil
}

// Add makes a task of each ticket, in one user batch it announces and returns. Tickets already
// on the list, skipped, or repeated are left out, so adding twice adds once; when none are left,
// no batch is opened.
func (r *Runner) Add(ctx context.Context, id string, tickets []Ticket) (int64, []domain.Task, error) {
	in, err := r.find(id)
	if err != nil {
		return 0, nil, err
	}
	v, err := r.view(ctx, in)
	if err != nil {
		return 0, nil, err
	}
	if !v.On() {
		return 0, nil, fmt.Errorf("%w: %s is off or not set up", service.ErrConflict, in.Name())
	}
	for _, tk := range tickets {
		if strings.TrimSpace(tk.Key) == "" || strings.TrimSpace(tk.Title) == "" {
			return 0, nil, fmt.Errorf("%w: a ticket needs a key and a title", service.ErrInvalid)
		}
	}
	s, err := load(ctx, r.db, id)
	if err != nil {
		return 0, nil, err
	}
	have, err := r.haveTickets(ctx, s)
	if err != nil {
		return 0, nil, err
	}
	tasks := []domain.Task{}
	var batch int64
	for _, tk := range tickets {
		key := strings.TrimSpace(tk.Key)
		if have[strings.ToUpper(key)] {
			continue
		}
		have[strings.ToUpper(key)] = true
		if batch == 0 {
			if batch, err = r.svc.NewBatch(ctx, domain.ActorUser); err != nil {
				return 0, nil, err
			}
		}
		t, err := r.svc.CreateTask(ctx, batch, service.TaskFields{
			Title: tk.Title, Notes: tk.Notes, Refs: []domain.Ref{{Label: key, URL: tk.URL}},
		})
		if err != nil {
			return 0, nil, err
		}
		tasks = append(tasks, t)
	}
	if batch == 0 {
		return 0, tasks, nil
	}
	return batch, tasks, r.publish(ctx, batch)
}
```

In `Skip`, wrap the batch and `ForgetEvent` so a ticket skip opens none, and update its doc comment to say "eventID 0, as for a ticket, removes nothing and returns batch 0":

```go
	var batch int64
	if eventID != 0 {
		if batch, err = r.svc.NewBatch(ctx, domain.ActorUser); err != nil {
			return 0, err
		}
		if err := r.svc.ForgetEvent(ctx, batch, eventID); err != nil {
			return 0, err
		}
	}
	// … load, append, store unchanged …
	if batch == 0 {
		return 0, nil
	}
	return batch, r.publish(ctx, batch)
```

- [ ] **Step 5: Run to verify they pass**

Run: `go test ./internal/integrations/... && go vet ./...`
Expected: PASS. (`go vet` flags other packages' fakes missing `Pulls` — `internal/tui`, `internal/httpapi` use the real `gcal`, so expect none; fix any that appear.)

- [ ] **Step 6: Commit**

```bash
git add internal/integrations
git commit -m "Let an integration offer tickets, and add the picked ones as tasks"
```

---

### Task 2: The `jira` package

**Files:**
- Create: `internal/integrations/jira/jira.go`
- Test: `internal/integrations/jira/jira_test.go`

**Interfaces:**
- Consumes: `integrations.Integration`, `Ticket`, `Source`, `PullsTickets` from Task 1.
- Produces: `jira.New(client *http.Client) *Jira`, ID `"jira"`, fields `url`, `token`, `jql`.

- [ ] **Step 1: Write the failing tests**

```go
package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/integrations"
)

const token = "secret-pat"

// server answers searches like Jira, checking the token, and records the last request.
func server(t *testing.T, status int, body string) (*httptest.Server, *http.Request) {
	t.Helper()
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r.Clone(context.Background())
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func cfg(url string) integrations.Config {
	return integrations.Config{Values: map[string]string{"url": url, "token": token, "jql": "project = FLEX1"}}
}

func pull(t *testing.T, c integrations.Config) integrations.Source {
	t.Helper()
	sources := New(nil).Pull(context.Background(), c, time.Time{}, time.Time{})
	if len(sources) != 1 || sources[0].Label != "Jira" {
		t.Fatalf("sources = %+v", sources)
	}
	return sources[0]
}

const search = `{"total": 63, "issues": [
	{"key": "FLEX1-2", "fields": {"summary": "Unassigned one", "description": null, "assignee": null}},
	{"key": "FLEX1-1", "fields": {"summary": "[quiz] Subscribe to PubSub", "description": "See https://x.test/doc", "assignee": {"name": "me"}}}
]}`

func TestPullOffersTicketsMineFirst(t *testing.T) {
	srv, got := server(t, http.StatusOK, search)
	src := pull(t, cfg(srv.URL+"/jira/"))

	want := []integrations.Ticket{
		{Key: "FLEX1-1", Title: "FLEX1-1 [quiz] Subscribe to PubSub", Notes: "See https://x.test/doc", URL: srv.URL + "/jira/browse/FLEX1-1"},
		{Key: "FLEX1-2", Title: "FLEX1-2 Unassigned one", Notes: "", URL: srv.URL + "/jira/browse/FLEX1-2"},
	}
	if diff := cmp.Diff(want, src.Tickets); diff != "" || src.Err != nil || src.More != 61 {
		t.Errorf("err %v, more %d, tickets (-want +got):\n%s", src.Err, src.More, diff)
	}
	if got.URL.Path != "/jira/rest/api/2/search" || got.Header.Get("Authorization") != "Bearer "+token {
		t.Errorf("request = %s %v", got.URL.Path, got.Header)
	}
	q := got.URL.Query()
	if q.Get("jql") != "project = FLEX1" || q.Get("fields") != "summary,description,assignee" || q.Get("maxResults") != "50" {
		t.Errorf("query = %v", q)
	}
}

func TestPullExplainsWhatWentWrongWithoutTheToken(t *testing.T) {
	bad, _ := json.Marshal(map[string]any{"errorMessages": []string{"Field 'sprnt' does not exist.", "Try again."}})
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"bad token":   {http.StatusUnauthorized, "", "Jira rejected the token"},
		"bad search":  {http.StatusBadRequest, string(bad), "Field 'sprnt' does not exist. Try again."},
		"server down": {http.StatusInternalServerError, "", "Jira answered 500"},
		"login page":  {http.StatusOK, "<html>Sign in</html>", "couldn't read Jira's answer — is the address right?"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := server(t, c.status, c.body)
			src := pull(t, cfg(srv.URL))
			if src.Err == nil || src.Err.Error() != c.want {
				t.Errorf("err = %v, want %q", src.Err, c.want)
			}
		})
	}
}

func TestPullBlamesTheNetworkWhenJiraIsOutOfReach(t *testing.T) {
	src := pull(t, cfg("http://127.0.0.1:1"))
	if src.Err == nil || src.Err.Error() != "Can't reach Jira — on the VPN?" || strings.Contains(src.Err.Error(), token) {
		t.Errorf("err = %v", src.Err)
	}
}

func TestPullGivesUpOnASlowJira(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	sources := New(&http.Client{Timeout: 50 * time.Millisecond}).Pull(context.Background(), cfg(srv.URL), time.Time{}, time.Time{})
	if err := sources[0].Err; err == nil || err.Error() != "Can't reach Jira — on the VPN?" {
		t.Errorf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/integrations/jira/`
Expected: build failure — `New` undefined.

- [ ] **Step 3: Implement**

```go
// Package jira offers tickets from a Jira Data Center search, through a Personal Access Token.
package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/integrations"
)

// maxResults caps a pull. A pick list longer than this means the search is too broad.
const maxResults = 50

// Jira is the Jira integration.
type Jira struct {
	client *http.Client
}

// New returns the integration. A nil client means one that gives up after 30 seconds.
func New(client *http.Client) *Jira {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Jira{client: client}
}

func (*Jira) ID() string                { return "jira" }
func (*Jira) Name() string              { return "Jira" }
func (*Jira) Pulls() integrations.Pulls { return integrations.PullsTickets }

func (*Jira) Fields() []integrations.Field {
	return []integrations.Field{
		{Key: "url", Label: "Address", Kind: integrations.Text, Required: true, Help: "Your Jira's address, such as https://jira.example.com"},
		{Key: "token", Label: "Personal access token", Kind: integrations.Secret, Required: true, Help: "In Jira: your profile → Personal Access Tokens"},
		{Key: "jql", Label: "Search (JQL)", Kind: integrations.Text, Required: true, Help: "Such as: sprint in openSprints() AND statusCategory != Done AND assignee = currentUser()"},
	}
}

// Pull runs the search. The window is ignored: a ticket isn't for a day.
func (j *Jira) Pull(ctx context.Context, cfg integrations.Config, _, _ time.Time) []integrations.Source {
	src := integrations.Source{Label: "Jira"}
	src.Tickets, src.More, src.Err = j.search(ctx, cfg)
	return []integrations.Source{src}
}

type searchResult struct {
	Total  int `json:"total"`
	Issues []struct {
		Key    string `json:"key"`
		Fields struct {
			Summary     string    `json:"summary"`
			Description *string   `json:"description"`
			Assignee    *struct{} `json:"assignee"`
		} `json:"fields"`
	} `json:"issues"`
}

// search returns the matching tickets, assigned ones first, and how many more matched. Its errors
// never wrap the client's, and never hold the token.
func (j *Jira) search(ctx context.Context, cfg integrations.Config) ([]integrations.Ticket, int, error) {
	base := strings.TrimRight(cfg.Values["url"], "/")
	q := url.Values{"jql": {cfg.Values["jql"]}, "fields": {"summary,description,assignee"}, "maxResults": {fmt.Sprint(maxResults)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/rest/api/2/search?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, errors.New("that address isn't a URL")
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Values["token"])
	req.Header.Set("Accept", "application/json")
	resp, err := j.client.Do(req)
	if err != nil {
		return nil, 0, errors.New("Can't reach Jira — on the VPN?")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, errors.New("Can't reach Jira — on the VPN?")
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, 0, errors.New("Jira rejected the token")
	case http.StatusBadRequest:
		var bad struct {
			ErrorMessages []string `json:"errorMessages"`
		}
		if json.Unmarshal(body, &bad) == nil && len(bad.ErrorMessages) > 0 {
			return nil, 0, errors.New(strings.Join(bad.ErrorMessages, " "))
		}
		return nil, 0, errors.New("Jira didn't accept the search")
	default:
		return nil, 0, fmt.Errorf("Jira answered %d", resp.StatusCode)
	}
	var res searchResult
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, 0, errors.New("couldn't read Jira's answer — is the address right?")
	}
	tickets := make([]integrations.Ticket, 0, len(res.Issues))
	assigned := map[string]bool{}
	for _, is := range res.Issues {
		t := integrations.Ticket{Key: is.Key, Title: is.Key + " " + is.Fields.Summary, URL: base + "/browse/" + is.Key}
		if is.Fields.Description != nil {
			t.Notes = *is.Fields.Description
		}
		assigned[is.Key] = is.Fields.Assignee != nil
		tickets = append(tickets, t)
	}
	sort.SliceStable(tickets, func(a, b int) bool { return assigned[tickets[a].Key] && !assigned[tickets[b].Key] })
	return tickets, max(res.Total-len(res.Issues), 0), nil
}
```

The Global Constraints' error list gains two words-for-the-user cases the spec didn't name: an unparseable URL and an unreadable 200 (Review Focus 5). Ledger that as a ruling.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/integrations/jira/ -v 2>&1 | tail -15`
Expected: PASS, 4 tests (+4 subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/jira
git commit -m "Add the Jira integration: one search per pull, through a personal access token"
```

---

### Task 3: Wire Jira in, and the add endpoint

**Files:**
- Modify: `internal/app/app.go:92-94`
- Modify: `internal/httpapi/server.go` (route), `internal/httpapi/integrations.go` (handler)
- Modify: `internal/httpapi/helpers_test.go` (register Jira, serve a fake search)
- Test: `internal/httpapi/integrations_test.go`

**Interfaces:**
- Consumes: `jira.New`, `Runner.Add`, `Runner.Skip(…, 0)`.
- Produces: `POST /api/v1/integrations/{id}/add` taking `{tickets: Ticket[]}`, returning `Mutation{BatchID, Data: []domain.Task}`.

- [ ] **Step 1: Write the failing tests**

In `helpers_test.go`, add `jira.New(nil)` after `gcal.New(nil)` in `NewRunner`, and a fake Jira beside `feeds`:

```go
	jiraSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, api.search) }))
	t.Cleanup(jiraSrv.Close)
	api.jiraURL = jiraSrv.URL
```

with `search, jiraURL string` fields on `testAPI`. Update `TestIntegrationsListGoogleCalendarWithItsFields` to expect two views (`gcal` then `jira`) and check `views[0].Pulls == integrations.PullsEvents`, `views[1].Pulls == integrations.PullsTickets`.

```go
const testSearch = `{"total": 2, "issues": [
	{"key": "FLEX1-1", "fields": {"summary": "Mine", "description": "d", "assignee": {"name": "me"}}},
	{"key": "FLEX1-2", "fields": {"summary": "Up for grabs", "description": null, "assignee": null}}
]}`

func setUpJira(t *testing.T, api *testAPI) {
	t.Helper()
	rec := api.do(http.MethodPut, "/api/v1/integrations/jira", integrations.Update{Values: map[string]string{
		"url": api.jiraURL, "token": "pat-token", "jql": "project = FLEX1",
	}})
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "pat-token") {
		t.Fatal("the saved view contains the token")
	}
	wantStatus(t, api.do(http.MethodPut, "/api/v1/integrations/jira/enabled", map[string]bool{"enabled": true}), http.StatusOK)
}

func TestPickingJiraTickets(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	api.search = testSearch
	tk := integrations.Ticket{Key: "FLEX1-1", Title: "FLEX1-1 Mine", URL: api.jiraURL + "/browse/FLEX1-1"}
	wantStatus(t, api.do(http.MethodPost, "/api/v1/integrations/jira/add", map[string]any{"tickets": []integrations.Ticket{tk}}), http.StatusConflict)
	setUpJira(t, api)

	res := decodeAs[integrations.PullResult](t, api.do(http.MethodPost, "/api/v1/integrations/jira/pull", nil))
	if len(res.Sources) != 1 || len(res.Sources[0].Tickets) != 2 || res.BatchID != 0 {
		t.Fatalf("pull = %+v", res)
	}

	rec := api.do(http.MethodPost, "/api/v1/integrations/jira/add", map[string]any{"tickets": []integrations.Ticket{res.Sources[0].Tickets[0]}})
	wantStatus(t, rec, http.StatusOK)
	added := decodeAs[struct {
		BatchID int64         `json:"batch_id"`
		Data    []domain.Task `json:"data"`
	}](t, rec)
	if added.BatchID == 0 || len(added.Data) != 1 || added.Data[0].Title != "FLEX1-1 Mine" {
		t.Fatalf("added = %+v", added)
	}

	rec = api.do(http.MethodPost, "/api/v1/integrations/jira/skipped", map[string]string{"key": "FLEX1-2", "title": "FLEX1-2 Up for grabs"})
	wantStatus(t, rec, http.StatusOK)

	res = decodeAs[integrations.PullResult](t, api.do(http.MethodPost, "/api/v1/integrations/jira/pull", nil))
	if len(res.Sources[0].Tickets) != 0 {
		t.Errorf("pull after add and skip = %+v", res.Sources[0].Tickets)
	}
}
```

(Add `domain` to the test imports.)

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/httpapi/ -run 'Integrations|Jira' 2>&1 | tail -15`
Expected: FAIL — two views expected, one got; `/add` 404/405.

- [ ] **Step 3: Implement**

`app.go`: `gcal.New(nil), jira.New(nil),` with the import. `server.go`: `mux.HandleFunc("POST /api/v1/integrations/{id}/add", s.addTickets)` after the pull route. `integrations.go`:

```go
// addTickets makes tasks of the tickets the user picked from a pull. The client sends back what
// the pull returned, so Jira isn't asked twice.
func (s *Server) addTickets(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tickets []integrations.Ticket `json:"tickets"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	batch, tasks, err := s.Integrations.Add(r.Context(), r.PathValue("id"), body.Tickets)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Mutation{BatchID: batch, Data: tasks})
}
```

Update `pullIntegration`'s comment: "brings in today's events, or offers tickets".

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./... 2>&1 | tail -20`
Expected: all PASS. The TUI fixture builds its app through `app.Open`, so it now has Jira too; nothing there should change since Jira is off.

- [ ] **Step 5: Commit**

```bash
git add internal/app internal/httpapi
git commit -m "Serve Jira, and add picked tickets through the API"
```

---

### Task 4: Web — the pick list

**Files:**
- Modify: `web/src/lib/api.ts` (types, `addTickets`, `skip`'s `event_id` optional)
- Modify: `web/src/lib/integrations.ts` (`withoutTicket`, `picked`)
- Create: `web/src/lib/components/TicketPicker.svelte`
- Modify: `web/src/lib/components/MorningIntegrations.svelte`, `web/src/lib/components/IntegrationCard.svelte`
- Test: `web/src/lib/integrations.test.ts`

**Interfaces:**
- Consumes: `POST /integrations/{id}/add`, `pulls` on the view, `tickets`/`more` on a source.
- Produces: `api.addTickets(id: string, tickets: Ticket[]): Promise<Mutation>`; `picked(result, ticked: Set<string>): Ticket[]`; `withoutTicket(result, key): PullResult`.

- [ ] **Step 1: Write the failing tests**

Add `pulls: 'events'` to the `gcal` fixture, then:

```ts
describe('tickets', () => {
	const tk = (key: string) => ({ key, title: `${key} t`, notes: '', url: `https://j/browse/${key}` });
	const result: PullResult = {
		batch_id: 0,
		sources: [{ label: 'Jira', events: [], tickets: [tk('A-1'), tk('A-2'), tk('A-3')], more: 0 }]
	};

	it('Should pick the ticked tickets in the order shown', () => {
		expect(picked(result, new Set(['A-3', 'A-1']))).toEqual([tk('A-1'), tk('A-3')]);
	});

	it('Should drop a skipped ticket from the result', () => {
		expect(withoutTicket(result, 'A-2').sources[0].tickets).toEqual([tk('A-1'), tk('A-3')]);
	});
});
```

The `withoutEvent` test's sources gain `tickets: [], more: 0`.

- [ ] **Step 2: Run to verify they fail**

Run: `cd web && npx vitest run src/lib/integrations.test.ts`
Expected: FAIL — `picked`/`withoutTicket` not exported.

- [ ] **Step 3: Implement**

`api.ts`: `Integration` gains `/** Whether a pull writes events or offers tickets to pick. */ pulls: 'events' | 'tickets';`. Add

```ts
/** A pulled ticket, which becomes a task only when picked. */
export interface Ticket {
	key: string;
	title: string;
	notes: string;
	url: string;
}
```

`PullSource` gains `tickets: Ticket[];` and `/** How many more matched than were returned. */ more: number;`. `skip`'s `event_id` becomes optional (`event_id?: number`). Add `addTickets: (id: string, tickets: Ticket[]) => request<Mutation>('POST', `/integrations/${id}/add`, { tickets }),` after `pull`.

`integrations.ts`:

```ts
/** The ticked tickets, in the order the pull listed them. */
export function picked(result: PullResult, ticked: Set<string>): Ticket[] {
	return result.sources.flatMap((s) => s.tickets.filter((t) => ticked.has(t.key)));
}

/** The result without one ticket, after it was skipped. */
export function withoutTicket(result: PullResult, key: string): PullResult {
	return {
		...result,
		sources: result.sources.map((s) => ({ ...s, tickets: s.tickets.filter((t) => t.key !== key) }))
	};
}
```

`TicketPicker.svelte` (same props as `PullResults`; reuse its `.link`, `.remove`, `.bad`, `ul`/`li` styles):

```svelte
<script lang="ts">
	import { SvelteSet } from 'svelte/reactivity';
	import { api, type PullResult, type Ticket } from '#lib/api.ts';
	import { picked, withoutTicket } from '#lib/integrations.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	interface Props {
		/** The integration the tickets came from. */
		id: string;
		result: PullResult;
		/** Pulls again, for a pull that failed. */
		retry: () => void;
	}
	let { id, result = $bindable(), retry }: Props = $props();

	const ticked = new SvelteSet<string>();
	let added = $state(0);
	let adding = $state(false);

	function toggle(key: string) {
		if (ticked.has(key)) ticked.delete(key);
		else ticked.add(key);
	}

	async function add() {
		const tickets = picked(result, ticked);
		adding = true;
		const n = tickets.length;
		if (await toasts.track(`Added ${n} ${n === 1 ? 'task' : 'tasks'}`, () => api.addTickets(id, tickets))) added = n;
		adding = false;
	}

	async function skip(t: Ticket) {
		if (await toasts.track(`Skipping ${t.key} from now on`, () => api.skip(id, t.key, t.title))) {
			ticked.delete(t.key);
			result = withoutTicket(result, t.key);
		}
	}
</script>

{#if added}
	<p class="muted">Added {added}.</p>
{:else}
	{#each result.sources as source, i (i)}
		<section class="tickets">
			{#if source.error}
				<p class="bad">{source.error} <button class="link" onclick={retry}>Retry</button></p>
			{:else if source.tickets.length === 0}
				<p class="muted">Nothing new in {source.label}.</p>
			{:else}
				<h3 class="label">{`${source.label} · ${source.tickets.length}`}</h3>
				<ul>
					{#each source.tickets as t (t.key)}
						<li>
							<input type="checkbox" aria-label="Pick {t.key}" checked={ticked.has(t.key)} onchange={() => toggle(t.key)} />
							<a class="title" href={t.url} target="_blank" rel="noopener noreferrer">{t.title}</a>
							<button class="remove" aria-label="Skip {t.key}" onclick={() => skip(t)}>×</button>
						</li>
					{/each}
				</ul>
				{#if source.more}<p class="muted">{source.more} more matched — narrow the search</p>{/if}
				<button class="btn primary" disabled={ticked.size === 0 || adding} onclick={add}>Add {ticked.size}</button>
			{/if}
		</section>
	{/each}
{/if}
```

Check `svelte/reactivity`'s `SvelteSet` is available in the installed Svelte (`grep version web/node_modules/svelte/package.json`, 5.x has it); `picked` takes `Set<string>`, which `SvelteSet` is.

`MorningIntegrations.svelte`: import `TicketPicker`, and render `{#if i.pulls === 'tickets'}<TicketPicker id={i.id} bind:result={results[i.id]} retry={() => pull(i.id)} />{:else}<PullResults … />{/if}`. Keep `.integrations :global(.tickets) { width: 100%; }` beside `.sources`.

`IntegrationCard.svelte`: the same switch for `result`, and the button reads `{pulling ? 'Pulling…' : integration.pulls === 'tickets' ? 'Pull' : 'Pull today'}`.

`TaskCard`/`text` inputs need nothing: Jira's fields are `text` and `secret`, which the card already renders.

- [ ] **Step 4: Run to verify**

Run: `cd web && npx vitest run && npm run check`
Expected: all Vitest PASS; svelte-check 0 errors.

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "Pick Jira tickets from the morning and settings"
```

---

### Task 5: TUI — the Jira step

**Files:**
- Create: `internal/tui/jira.go`
- Modify: `internal/tui/calendar.go` (narrow to event integrations; exits go through `afterCalendar`)
- Modify: `internal/tui/morning.go` (`stepJira`, `tickets` on `morning`, key and view dispatch)
- Modify: `internal/tui/model.go:177` (route the new messages)
- Test: `internal/tui/jira_test.go`, golden `internal/tui/testdata/morning-jira-80.golden`

**Interfaces:**
- Consumes: `View.Pulls`, `PullResult` tickets, `Runner.Add`, `Runner.Skip(…, 0)`.
- Produces: `stepJira` between `stepCalendar` and `stepPace`; `func (m Model) afterCalendar() (tea.Model, tea.Cmd)`.

- [ ] **Step 1: Write the failing tests**

```go
package tui

// jiraSearch has one ticket of the user's and one up for grabs.
const jiraSearch = `{"total": 2, "issues": [
	{"key": "FLEX1-1", "fields": {"summary": "Mine", "description": "d", "assignee": {"name": "me"}}},
	{"key": "FLEX1-2", "fields": {"summary": "Up for grabs", "description": null, "assignee": null}}
]}`

// jira sets up Jira against a fake search and turns it on. With fail, the search answers 401.
func (f *fixture) jira(t *testing.T, fail bool) {
	t.Helper()
	keyring.MockInit()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, jiraSearch)
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	if _, err := f.app.Integrations.Save(ctx, "jira", integrations.Update{Values: map[string]string{"url": srv.URL, "token": "t", "jql": "x"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Integrations.SetEnabled(ctx, "jira", true); err != nil {
		t.Fatal(err)
	}
}

// jiraAt is a morning on the Jira step, pulled.
func (f *fixture) jiraAt(t *testing.T) Model {
	t.Helper()
	m := f.morningAt(t)
	m = send(m, m.loadIntegrations()(), key("esc"))
	if m.morning.step != stepJira {
		t.Fatalf("step = %d, want Jira", m.morning.step)
	}
	return send(m, m.pullTickets()())
}

func (f *fixture) taskTitles(t *testing.T) []string {
	t.Helper()
	tasks, err := f.app.Service.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, tk := range tasks {
		titles = append(titles, tk.Title)
	}
	return titles
}

func TestTheMorningPicksJiraTickets(t *testing.T) {
	f := newFixture(t)
	f.jira(t, false)
	tm := f.run(t)
	waitFor(t, tm, "to start your day")
	tm.Send(key("enter"))
	waitFor(t, tm, "What's on your plate?")
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	waitFor(t, tm, "Pull your Jira tickets?")
	tm.Send(key("enter"))
	waitFor(t, tm, "Jira · 2")
	tm.Send(key(" "))
	tm.Send(key("enter"))
	waitFor(t, tm, "How hard")
	quit(t, tm)
	if diff := cmp.Diff([]string{"FLEX1-1 Mine"}, f.taskTitles(t)); diff != "" {
		t.Errorf("tasks (-want +got):\n%s", diff)
	}
}

func TestJiraView(t *testing.T) {
	f := newFixture(t)
	f.jira(t, false)
	m := send(f.jiraAt(t), key(" "), key("down"))
	golden(t, "morning-jira-80", m.View())
}

func TestXSkipsATicketForGood(t *testing.T) {
	f := newFixture(t)
	f.jira(t, false)
	m := send(f.jiraAt(t), key("down"))
	_, cmd := m.Update(key("x"))
	m = send(m, cmd())
	if got := m.morning.tickets.list(); len(got) != 1 || got[0].ticket.Key != "FLEX1-1" {
		t.Errorf("listed = %+v", got)
	}
	v, _ := f.app.Integrations.View(context.Background(), "jira")
	if len(v.Skipped) != 1 || v.Skipped[0].Key != "FLEX1-2" {
		t.Errorf("skipped = %+v", v.Skipped)
	}
}

func TestTabLeavesJiraWithoutAdding(t *testing.T) {
	f := newFixture(t)
	f.jira(t, false)
	m := send(f.jiraAt(t), key(" "), tea.KeyMsg{Type: tea.KeyTab})
	if m.morning.step != stepPace || len(f.taskTitles(t)) != 0 {
		t.Errorf("step %d, tasks %v", m.morning.step, f.taskTitles(t))
	}
}

func TestAFailedJiraPullShowsWhyAndRRetries(t *testing.T) {
	f := newFixture(t)
	f.jira(t, true)
	m := f.jiraAt(t)
	if !strings.Contains(m.View(), "Jira rejected the token") {
		t.Errorf("view:\n%s", m.View())
	}
	next, cmd := m.Update(key("r"))
	if !next.(Model).morning.tickets.pulling || cmd == nil {
		t.Error("r didn't pull again")
	}
}

func TestTheCalendarStepIsOnlyForCalendars(t *testing.T) {
	f := newFixture(t)
	f.jira(t, false)
	m := f.morningAt(t)
	m = send(m, m.loadIntegrations()(), key("esc"))
	if m.morning.step != stepJira {
		t.Errorf("step = %d, want Jira, skipping the calendar", m.morning.step)
	}
}

func TestTheCalendarStepGoesOnToJira(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	f.jira(t, false)
	m := send(f.calendarAt(t), key("enter"))
	if m.morning.step != stepJira {
		t.Errorf("step = %d, want Jira", m.morning.step)
	}
}
```

Imports as in `calendar_test.go`. `fixture.run`/`morningAt`/`waitFor`/`quit` exist; check `key(" ")` produces a `space` string — if `key` builds `KeyRunes`, `k.String()` is `" "`, so the step matches `" "` (use `case " ", "space":` to be safe).

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/tui/ -run 'Jira|Ticket|Calendar' 2>&1 | tail -15`
Expected: build failure — `stepJira`, `pullTickets`, `tickets` undefined.

- [ ] **Step 3: Implement**

`morning.go`: `stepJira` after `stepCalendar` in the const block; `tickets tickets` after `calendar calendar` in `morning`; in `onMorningKey`, `case stepJira: return m.onJiraKey(k)`; in `morningView`, a `case stepJira:` like the calendar's, calling `m.jiraLines(inner, m.height-len(lines)-7)`.

`calendar.go`:
- `loadIntegrations` returns all views that are on (unchanged); in `onCalendarMsg`'s `integrationsMsg` case split them:
  ```go
	case integrationsMsg:
		c.on, m.morning.tickets.on = nil, nil
		for _, v := range msg.on {
			if v.Pulls == integrations.PullsTickets {
				m.morning.tickets.on = append(m.morning.tickets.on, v)
			} else {
				c.on = append(c.on, v)
			}
		}
  ```
  Update the `calendar` type comment to "pulls today's events from the event integrations that are on".
- `afterDump`: when `len(m.morning.calendar.on) == 0`, `return m.afterCalendar()`.
- Every `return m.toPace()` in `onCalendarKey` becomes `return m.afterCalendar()`.
- Add:
  ```go
// afterCalendar goes to the Jira step when a ticket integration can pull, or on to pace.
func (m Model) afterCalendar() (tea.Model, tea.Cmd) {
	if len(m.morning.tickets.on) == 0 {
		return m.toPace()
	}
	m.morning.dump.Blur()
	m.morning.step = stepJira
	return m, nil
}
  ```

`jira.go`:

```go
package tui

import (
	"context"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jameynakama/whatnow/internal/integrations"
)

// tickets is the morning's step that offers tickets from the ticket integrations that are on, to
// pick from.
type tickets struct {
	on      []integrations.View
	pulling bool
	pulled  []pulledFrom    // nil until the first pull
	at      int             // the ticket under the cursor, counted across every source
	ticked  map[string]bool // by ticket key
}

// pickTicket is a pulled ticket and the integration it came from.
type pickTicket struct {
	integration string
	ticket      integrations.Ticket
}

type (
	ticketsPulledMsg struct{ pulled []pulledFrom }
	ticketsFailedMsg struct{ err error }
	ticketSkippedMsg struct{ key string }
	ticketsAddedMsg  struct{ n int }
)

// list is every offered ticket in the order shown.
func (t tickets) list() []pickTicket {
	var out []pickTicket
	for _, p := range t.pulled {
		for _, s := range p.result.Sources {
			for _, tk := range s.Tickets {
				out = append(out, pickTicket{p.integration, tk})
			}
		}
	}
	return out
}

func (m Model) pullTickets() tea.Cmd {
	on := m.morning.tickets.on
	return func() tea.Msg {
		var pulled []pulledFrom
		for _, v := range on {
			res, err := m.app.Integrations.Pull(m.ctx, v.ID)
			if err != nil {
				return ticketsFailedMsg{err}
			}
			pulled = append(pulled, pulledFrom{v.ID, res})
		}
		return ticketsPulledMsg{pulled}
	}
}

// addTicked makes tasks of the ticked tickets, one batch per integration.
func (m Model) addTicked() tea.Cmd {
	byIntegration := map[string][]integrations.Ticket{}
	var order []string
	for _, p := range m.morning.tickets.list() {
		if !m.morning.tickets.ticked[p.ticket.Key] {
			continue
		}
		if _, ok := byIntegration[p.integration]; !ok {
			order = append(order, p.integration)
		}
		byIntegration[p.integration] = append(byIntegration[p.integration], p.ticket)
	}
	return func() tea.Msg {
		n := 0
		for _, id := range order {
			_, tasks, err := m.app.Integrations.Add(m.ctx, id, byIntegration[id])
			if err != nil {
				return failedMsg{err}
			}
			n += len(tasks)
		}
		return ticketsAddedMsg{n}
	}
}

func (m Model) skipTicket(p pickTicket) tea.Cmd {
	return func() tea.Msg {
		if _, err := m.app.Integrations.Skip(m.ctx, p.integration, integrations.Skip{Key: p.ticket.Key, Title: p.ticket.Title}, 0); err != nil {
			return failedMsg{err}
		}
		return ticketSkippedMsg{p.ticket.Key}
	}
}

func (m Model) onJiraMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	t := &m.morning.tickets
	switch msg := msg.(type) {
	case ticketsPulledMsg:
		t.pulling, t.pulled, t.at, t.ticked = false, msg.pulled, 0, map[string]bool{}
	case ticketsFailedMsg:
		t.pulling = false
		return m.show(friendly(msg.err), true)
	case ticketSkippedMsg:
		for i := range t.pulled {
			for j := range t.pulled[i].result.Sources {
				s := &t.pulled[i].result.Sources[j]
				s.Tickets = slices.DeleteFunc(s.Tickets, func(tk integrations.Ticket) bool { return tk.Key == msg.key })
			}
		}
		delete(t.ticked, msg.key)
		t.at = max(min(t.at, len(t.list())-1), 0)
		return m.show("Skipping "+msg.key+" from now on", false)
	case ticketsAddedMsg:
		next, cmd := m.toPace()
		shown, toast := next.(Model).show("Added "+count(msg.n, "task", "tasks"), false)
		return shown, tea.Batch(cmd, toast)
	}
	return m, nil
}

func (m Model) onJiraKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	t := &m.morning.tickets
	if t.pulling {
		return m, nil
	}
	if t.pulled == nil {
		switch k.String() {
		case "enter":
			t.pulling = true
			return m, tea.Batch(m.pullTickets(), m.spin.Tick)
		case "tab":
			return m.toPace()
		}
		return m, nil
	}
	list := t.list()
	switch k.String() {
	case "up", "k":
		t.at = max(t.at-1, 0)
	case "down", "j":
		t.at = max(min(t.at+1, len(list)-1), 0)
	case " ", "space":
		if len(list) > 0 {
			key := list[t.at].ticket.Key
			t.ticked[key] = !t.ticked[key]
		}
	case "x":
		if len(list) > 0 {
			return m, m.skipTicket(list[t.at])
		}
	case "r":
		t.pulling = true
		return m, tea.Batch(m.pullTickets(), m.spin.Tick)
	case "enter":
		if slices.ContainsFunc(list, func(p pickTicket) bool { return t.ticked[p.ticket.Key] }) {
			return m, m.addTicked()
		}
		return m.toPace()
	case "tab":
		return m.toPace()
	}
	return m, nil
}

// jiraLines is the Jira step in at most room lines: the offer to pull, then each source's tickets
// or error, scrolled so the cursor's ticket is in view.
func (m Model) jiraLines(w, room int) (lines []string, hint string) {
	t := m.morning.tickets
	if t.pulling {
		return []string{m.spin.View() + softStyle.Render(" Pulling your tickets…")}, ""
	}
	if t.pulled == nil {
		return []string{"Pull your Jira tickets?"}, "enter pull · tab skip"
	}
	var body []string
	i, cursor := 0, 0
	for _, p := range t.pulled {
		for _, s := range p.result.Sources {
			if s.Error != "" {
				body = append(body, nowStyle.Render(s.Label+": "+s.Error))
				continue
			}
			if len(s.Tickets) == 0 {
				body = append(body, softStyle.Render("Nothing new in "+s.Label+"."))
				continue
			}
			body = append(body, lipgloss.NewStyle().Bold(true).Render(s.Label+" · "+count(len(s.Tickets), "ticket", "tickets")))
			for _, tk := range s.Tickets {
				if i == t.at {
					cursor = len(body)
				}
				body = append(body, ticketLine(tk, t.ticked[tk.Key], i == t.at, w))
				i++
			}
			if s.More > 0 {
				body = append(body, softStyle.Render(count(s.More, "more matched", "more matched")+" — narrow the search"))
			}
		}
	}
	lines = append([]string{"Pick today's tickets", ""}, scrolled(body, cursor, room-2)...)
	return lines, "↑↓ move · space pick · x skip · r pull again · enter add · tab skip all"
}

func ticketLine(tk integrations.Ticket, ticked, current bool, w int) string {
	mark, box, title := "  ", "[ ] ", tk.Title
	if ticked {
		box = "[x] "
	}
	if current {
		mark, title = cursorStyle.Render("▸ "), lipgloss.NewStyle().Bold(true).Render(title)
	}
	return mark + box + lipgloss.NewStyle().MaxWidth(max(w-6, 1)).Render(title)
}
```

Check `count`'s output: `count(12, "more matched", …)` must read "12 more matched"; if `count` formats differently, write `fmt.Sprintf("%d more matched — narrow the search", s.More)` instead. Drop the `context` import if unused.

`model.go:177`: add `case ticketsPulledMsg, ticketsFailedMsg, ticketSkippedMsg, ticketsAddedMsg: return m.onJiraMsg(msg)`.

- [ ] **Step 4: Run to verify they pass, and write the golden**

Run: `go test ./internal/tui/ -run TestJiraView -update` (check `view_test.go`'s `golden` for the real flag name), read `internal/tui/testdata/morning-jira-80.golden`, confirm it shows a ticked FLEX1-1, the cursor on FLEX1-2, and the hint. Then `go test ./internal/tui/`.
Expected: PASS, golden reviewed.

- [ ] **Step 5: Commit**

```bash
git add internal/tui
git commit -m "Pick Jira tickets in the TUI morning, after the calendar"
```

---

### Task 6: End to end, and the records

**Files:**
- Create: `web/e2e/jira.spec.ts`
- Modify: `docs/superpowers/specs/2026-10-12-whatnow-integrations-design.md` only if anything here contradicts it

- [ ] **Step 1: Write the test**

```ts
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { expect, setUp, test } from './fixtures';

const search = JSON.stringify({
	total: 2,
	issues: [
		{ key: 'FLEX1-1', fields: { summary: 'Mine', description: 'See https://example.com', assignee: { name: 'me' } } },
		{ key: 'FLEX1-2', fields: { summary: 'Up for grabs', description: null, assignee: null } }
	]
});

test('Jira tickets are picked in the morning, and neither a pick nor a skip comes back', async ({ page, request }) => {
	const server = createServer((_, res) => {
		res.setHeader('Content-Type', 'application/json');
		res.end(search);
	});
	await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
	const url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
	try {
		await setUp(request);

		await page.goto('/settings/integrations');
		await page.getByRole('button', { name: /Jira/ }).click();
		await page.getByLabel('Address').fill(url);
		await page.getByLabel('Personal access token').fill('pat');
		await page.getByLabel('Search (JQL)').fill('project = FLEX1');
		await page.getByRole('button', { name: 'Save' }).click();
		await page.getByRole('switch', { name: /Jira/ }).check();
		await expect(page.getByRole('button', { name: 'Pull', exact: true })).toBeEnabled();

		await page.goto('/morning');
		const chip = page.getByRole('button', { name: '↻ Jira' });
		await chip.click();
		await expect(page.getByText('Jira · 2')).toBeVisible();
		await page.getByRole('button', { name: 'Skip FLEX1-2' }).click();
		await page.getByRole('checkbox', { name: 'Pick FLEX1-1' }).check();
		await page.getByRole('button', { name: 'Add 1' }).click();
		await expect(page.getByText('Added 1.')).toBeVisible();

		await chip.click();
		await expect(page.getByText('Nothing new in Jira.')).toBeVisible();
	} finally {
		server.close();
	}
});
```

Check the settings page's switch and card-header names against `IntegrationCard.svelte` and the calendar e2e (it uses `getByRole('switch')` with one card; with two cards the switch needs a name — use whatever accessible name the card's toggle has, e.g. `page.locator('section', { hasText: 'Jira' }).getByRole('switch')`).

- [ ] **Step 2: Run**

Run: `just e2e`
Expected: all pass, including the calendar flow (its `getByRole('switch')` may now match two switches; scope it to the Google Calendar card the same way if so).

- [ ] **Step 3: Full check and commit**

Run: `go test ./... && cd web && npx vitest run && npm run check`
Expected: all PASS.

```bash
git add web/e2e
git commit -m "Pick Jira tickets end to end"
```
