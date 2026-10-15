# Review Queue Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Offer Review Queue's ranked pull requests as whatnow tickets, with a minimum score and re-offered re-reviews.

**Architecture:** Review Queue gains `GET /api/queue`, which ranks on the server with the saved settings file. whatnow gains a `reviewqueue` ticket integration that calls it, and the runner's dedupe learns that a done task doesn't hide a re-requested review. The web ticket picker is already generic; the TUI's offer stops naming Jira.

**Tech Stack:** Review Queue: Node 22+, Express 5, JavaScript with JSDoc, `node --test`, ESLint, Prettier. whatnow: Go, `go-cmp`, bubbletea; SvelteKit for one type.

**Spec:** `docs/superpowers/specs/2026-10-15-whatnow-review-queue-design.md`

## Global Constraints

- Two repos. Review Queue is `/Users/jnakama/code/flex1/review-queue` (remote `oreillymedia-ai-transformation/review-queue`); whatnow is this repo.
- Review Queue work goes on a new branch `ranked-queue` cut from `main`, not from the checked-out `graphql` branch (Chelsea's open PR #3). Match its style: JSDoc, single quotes, trailing commas, 100 columns (`npx prettier --write`). Its gate is `npm run check` (lint, format check, tests, build).
- Pushing the Review Queue branch and opening its PR are outward-facing: ask Jamey first.
- The user-facing name is "Review Queue"; the integration ID is `reviewqueue`.
- whatnow commits: conventional commits, lowercase after the prefix, no `Co-Authored-By`. Gate: `just test` and `just lint`.
- Errors never wrap the HTTP client's error.

## Review Focus

- A slow organization scan behind a running Review Queue hits whatnow's 60-second timeout: it must not say Review Queue is down. Pinned in Task 3 (`TestATimeoutBlamesGitHubNotReviewQueue`).
- A PR Review Queue gives no reasons or estimate: the notes must not read "Score 0 ·  · ". Pinned in Task 3 (the cap test checks `Notes == "Score 1"`).
- Settings that point at organization scope with no organization: the endpoint must say why, not 502 or crash. Pinned in Task 1 (`settings that cannot be used say why`).
- A hand-broken settings file: a 500 naming the file, never treated as "no settings". Pinned in Task 1.
- A re-review added once must not be added again by a second pull or a double Add while its task is open. Pinned in Task 2 (the second `Add` in the rereview test).

---

### Task 1: Review Queue `GET /api/queue`

**Files (Review Queue repo):**
- Create: `server/routes/queue.js`
- Modify: `server/app.js` (import and mount)
- Modify: `README.md` (new section before "Reuse the backend in another app")
- Test: `tests/queue-route.test.js`

**Interfaces:**
- Produces: `GET /api/queue` → `200 {pulls: [{id, url, title, repository, author, score, reasons: string[], estimate: string, rereview: boolean}], fetchedAt}`; `409`/`500`/`502` → `{error}`. Task 3 consumes this.

- [ ] **Step 1: Branch from main**

```bash
cd /Users/jnakama/code/flex1/review-queue
git status --short   # must be clean
git fetch origin && git switch -c ranked-queue origin/main
```

- [ ] **Step 2: Write the failing tests** in `tests/queue-route.test.js`:

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createApp } from '../server/app.js';
import { defaults, queueActionKey } from '../src/engine.js';

const repository = 'acme/search';

/** Settings for one followed repository, reviewed as `me`. */
function settings(overrides = {}) {
  return {
    ...structuredClone(defaults),
    source: 'github',
    username: 'me',
    included: ['requested', 'followed'],
    repositories: [repository],
    priorityRepositories: [],
    maintenanceRepositories: [],
    teamMembers: [],
    ...overrides,
  };
}

/**
 * Answers GitHub's REST calls for three open PRs: #1 requests a review from `me`
 * after `me` already reviewed it, #2 requests a first review, and #3 is only followed.
 */
async function github(url) {
  const { pathname, searchParams } = new URL(url);
  const ok = (body) => ({ ok: true, headers: new Headers(), json: async () => body });
  if (searchParams.get('state') === 'open') {
    return ok(
      [1, 2, 3].map((number) => ({
        number,
        user: { login: 'author' },
        title: `Change ${number}`,
        state: 'open',
        draft: false,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
        requested_reviewers: number < 3 ? [{ login: 'me' }] : [],
        assignees: [],
        labels: [],
        html_url: `https://github.com/${repository}/pull/${number}`,
      })),
    );
  }
  if (pathname.endsWith('/pulls/1/reviews')) {
    return ok([
      { user: { login: 'me' }, state: 'CHANGES_REQUESTED', submitted_at: '2026-10-01T00:00:00Z' },
    ]);
  }
  if (/\/(reviews|comments|commits)$/.test(pathname)) {
    return ok([]);
  }
  return ok({ additions: 10, deletions: 2, changed_files: 1 });
}

async function serve(t, saved, fetcher = github) {
  const settingsFile = join(await mkdtemp(join(tmpdir(), 'review-queue-')), 'settings.json');
  if (saved !== undefined) {
    await writeFile(settingsFile, typeof saved === 'string' ? saved : JSON.stringify(saved));
  }
  const server = createApp({ settingsFile, fetcher }).listen(0, '127.0.0.1');
  await new Promise((resolve) => server.once('listening', resolve));
  t.after(() => server.close());
  return fetch(`http://127.0.0.1:${server.address().port}/api/queue`);
}

test('the queue is ranked with the saved settings and flattened for other tools', async (t) => {
  const response = await serve(t, settings());
  assert.equal(response.status, 200);
  const { pulls, fetchedAt } = await response.json();
  assert(Number.isFinite(Date.parse(fetchedAt)));
  assert.deepEqual(
    pulls.map(({ id, score, rereview }) => [id, score, rereview]),
    [
      [`${repository}#1`, 170, true],
      [`${repository}#2`, 100, false],
      [`${repository}#3`, 0, false],
    ],
  );
  assert.deepEqual(pulls[1], {
    id: `${repository}#2`,
    url: `https://github.com/${repository}/pull/2`,
    title: 'Change 2',
    repository,
    author: 'author',
    score: 100,
    reasons: ['Review requested from you'],
    estimate: '5–10 min',
    rereview: false,
  });
});

test('dismissed PRs are left out and penalties lower the score', async (t) => {
  const prefs = settings();
  const key = (number) => queueActionKey({ id: `${repository}#${number}` }, prefs);
  prefs.queueActions = {
    dismissed: [key(1)],
    penalties: { [key(2)]: -50 },
    autoDismissRestored: [],
  };
  const { pulls } = await (await serve(t, prefs)).json();
  assert.deepEqual(
    pulls.map(({ id, score }) => [id, score]),
    [
      [`${repository}#2`, 50],
      [`${repository}#3`, 0],
    ],
  );
});

test('without saved GitHub settings the queue asks for them instead of showing demo data', async (t) => {
  for (const saved of [undefined, settings({ source: 'demo' })]) {
    const response = await serve(t, saved);
    assert.equal(response.status, 409);
    assert.match((await response.json()).error, /source to GitHub/);
  }
});

test('settings that cannot be used say why', async (t) => {
  const unusable = await serve(t, settings({ repositoryScope: 'organization', organization: '' }));
  assert.equal(unusable.status, 409);
  const unreadable = await serve(t, '{"source": ');
  assert.equal(unreadable.status, 500);
  assert.match((await unreadable.json()).error, /Could not read/);
});

test('a GitHub failure is a 502 with its reason', async (t) => {
  const failing = async () => ({
    ok: false,
    status: 404,
    headers: new Headers(),
    json: async () => ({ message: 'Not Found' }),
  });
  const response = await serve(t, settings(), failing);
  assert.equal(response.status, 502);
  assert.match((await response.json()).error, /Not Found/);
});
```

- [ ] **Step 3: Run them to see them fail**

Run: `node --test tests/queue-route.test.js`
Expected: FAIL, every test getting 404 HTML instead of JSON (the route doesn't exist).

- [ ] **Step 4: Write the route** in `server/routes/queue.js`:

```js
import { Router } from 'express';
import { readSettings } from '../settings.js';
import { loadQueue, validatePullRequest } from '../pulls.js';
import { repositoryRequest } from '../../src/repositories.js';
import { normalizePreferences } from '../../src/storage.js';
import { rankQueue } from '../../src/engine.js';

/**
 * Creates the ranked queue endpoint for other local tools. It ranks with the saved
 * settings, as the queue view does, so dismissals and penalties apply.
 * @param {{file: string, token?: string, fetcher?: typeof fetch}} options
 */
export function createQueueRouter({ file, token, fetcher = fetch }) {
  const router = Router();
  router.get('/queue', async (request, response) => {
    let saved;
    try {
      saved = await readSettings(file);
    } catch (error) {
      return response.status(500).json({ error: `Could not read ${file}: ${error.message}` });
    }
    const prefs = normalizePreferences(saved);
    if (!saved || prefs.source !== 'github') {
      return response.status(409).json({ error: "Set Review Queue's source to GitHub first." });
    }
    let scope;
    try {
      scope = validatePullRequest(repositoryRequest(prefs));
    } catch (error) {
      return response.status(409).json({ error: error.message });
    }
    try {
      const { pulls, fetchedAt } = await loadQueue(scope, { token, fetcher });
      response.json({ pulls: rankQueue(pulls, prefs).map(queueItem), fetchedAt });
    } catch (error) {
      response.status(502).json({
        error: error.name === 'TimeoutError' ? 'GitHub timed out. Try again.' : error.message,
      });
    }
  });
  return router;
}

/**
 * Flattens a ranked PR to what another tool needs to offer it.
 * @param {import('../../src/domain/types.js').RankedPullRequest} ranked
 */
function queueItem({ pr, score, reasons, estimate, signals }) {
  return {
    id: pr.id,
    url: pr.url,
    title: pr.title,
    repository: pr.repository,
    author: pr.author,
    score,
    reasons: reasons.map((reason) => reason.text),
    estimate: estimate.label,
    rereview: Boolean(signals.rereview),
  };
}
```

And mount it in `server/app.js`, after the preferences import and router:

```js
import { createQueueRouter } from './routes/queue.js';
```

```js
  app.use('/api', createPreferencesRouter({ file: settingsFile }));
  app.use('/api', createQueueRouter({ file: settingsFile, token: githubToken, fetcher }));
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `node --test tests/queue-route.test.js`
Expected: 5 pass.

- [ ] **Step 6: Document it** in `README.md`, as a new section just before `## Reuse the backend in another app`:

```markdown
## Ranked queue for other tools

`GET /api/queue` returns the queue as the Queue view ranks it, for local tools such as whatnow. It reads the saved settings, so dismissals, penalties, and age policies apply, and fetches from GitHub on every call.

Each PR is `{id, url, title, repository, author, score, reasons, estimate, rereview}`, highest score first. `reasons` are the ranking reasons' text, `estimate` the size label, and `rereview` whether your review was requested again after you reviewed. A `409` means the settings can't be used yet (no settings file, the demo source, or an invalid scope); a `502` is a GitHub failure. Requests without an `Origin` header, such as from a command-line tool, pass the cross-origin check.
```

- [ ] **Step 7: Run the full gate**

Run: `npx prettier --write server/routes/queue.js tests/queue-route.test.js README.md && npm run check`
Expected: lint clean, format check clean, all tests pass, build succeeds.

- [ ] **Step 8: Commit**

```bash
git add server/routes/queue.js server/app.js tests/queue-route.test.js README.md
git commit -m "feat: serve the ranked queue at GET /api/queue

Ranks on the server with the saved settings, so other local tools get
the queue the Queue view shows, dismissals and penalties included."
```

Don't push. Ask Jamey before pushing `ranked-queue` and opening the PR to `main`.

---

### Task 2: Re-reviews come back past a done task

**Files (whatnow):**
- Modify: `internal/integrations/integrations.go` (`Ticket`)
- Modify: `internal/integrations/runner.go` (`offer`, `haveTickets` → `onList`/`listed`, `Add`)
- Modify: `web/src/lib/api.ts` (`Ticket`)
- Test: `internal/integrations/runner_test.go`

**Interfaces:**
- Produces: `integrations.Ticket.Rereview bool` (JSON `rereview,omitempty`). Task 3 sets it.

- [ ] **Step 1: Write the failing test**, appended to `internal/integrations/runner_test.go`:

```go
func TestARereviewComesBackPastADoneTaskButNotAnOpenOne(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.setUpBoard(t)
	e.task(t, "reviewed once", true, "acme/search#1")
	e.task(t, "reviewing now", false, "acme/search#2")
	e.task(t, "reviewed, not asked again", true, "acme/search#3")
	again := func(key string) integrations.Ticket {
		tk := ticket(key)
		tk.Rereview = true
		return tk
	}
	e.board.tickets = []integrations.Ticket{again("acme/search#1"), again("acme/search#2"), ticket("acme/search#3")}

	if diff := cmp.Diff([]string{"acme/search#1"}, offered(e.pullFrom(t, "board"))); diff != "" {
		t.Errorf("offered (-want +got):\n%s", diff)
	}

	_, tasks, err := e.runner.Add(ctx, "board", e.board.tickets)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Refs[0].Label != "acme/search#1" {
		t.Errorf("added %+v", tasks)
	}
	if _, tasks, err = e.runner.Add(ctx, "board", e.board.tickets); err != nil || len(tasks) != 0 {
		t.Errorf("adding again: tasks %+v, err %v", tasks, err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/integrations/ -run Rereview`
Expected: build failure, `tk.Rereview undefined`.

- [ ] **Step 3: Add the field** to `Ticket` in `internal/integrations/integrations.go`, after `URL`:

```go
	// Rereview means the source is asking for this again, so a done task with its key doesn't
	// hide it, such as a pull request whose review was requested again.
	Rereview bool `json:"rereview,omitempty"`
```

- [ ] **Step 4: Split the dedupe into open and closed** in `internal/integrations/runner.go`. Replace `haveTickets` and its comment with:

```go
// onList is the ticket keys, upper-cased, already on the list: open holds those on an open task or
// skipped, and closed those only on a done or dropped task. A deleted task doesn't count, so its
// ticket can be offered again.
type onList struct {
	open, closed map[string]bool
}

// hides says whether a ticket is already on the list. A closed task doesn't hide a rereview.
func (l onList) hides(tk Ticket) bool {
	key := strings.ToUpper(strings.TrimSpace(tk.Key))
	return l.open[key] || (l.closed[key] && !tk.Rereview)
}

func (r *Runner) listed(ctx context.Context, s saved) (onList, error) {
	tasks, err := r.svc.ListTasks(ctx)
	if err != nil {
		return onList{}, err
	}
	l := onList{open: map[string]bool{}, closed: map[string]bool{}}
	for _, t := range tasks {
		for _, ref := range t.Refs {
			if t.Status == domain.StatusOpen {
				l.open[strings.ToUpper(ref.Label)] = true
			} else {
				l.closed[strings.ToUpper(ref.Label)] = true
			}
		}
	}
	for _, sk := range s.Skipped {
		l.open[strings.ToUpper(sk.Key)] = true
	}
	return l, nil
}
```

In `offer`: `have, err := r.listed(ctx, s)`, and the filter becomes `if !have.hides(tk) {`.

In `Add`: `have, err := r.listed(ctx, s)`, and the loop's check becomes:

```go
		key := strings.TrimSpace(tk.Key)
		if have.hides(tk) {
			continue
		}
		have.open[strings.ToUpper(key)] = true
```

- [ ] **Step 5: Declare it on the web** in `web/src/lib/api.ts`'s `Ticket`, after `url`. `picked` already sends pulled tickets back whole, so this is the type only:

```ts
	/** Asked for again, such as a re-requested review, so a done task with its key doesn't hide it. */
	rereview?: boolean;
```

- [ ] **Step 6: Run the tests to see them pass**

Run: `go test ./internal/integrations/... && (cd web && npm run check)`
Expected: all pass, including the existing ticket tests (`TestATicketPullOffersWhatIsntOnTheListAndWritesNothing` still hides `FLEX1-2`, a done task's ticket without `Rereview`). 0 svelte-check errors.

- [ ] **Step 7: Commit**

```bash
git add internal/integrations/integrations.go internal/integrations/runner.go internal/integrations/runner_test.go web/src/lib/api.ts
git commit -m "feat: let a re-requested ticket past a done task

A ticket marked rereview is offered again when only a done or dropped
task has its key; an open task still hides it."
```

---

### Task 3: The `reviewqueue` integration

**Files (whatnow):**
- Create: `internal/integrations/reviewqueue/reviewqueue.go`
- Modify: `internal/app/app.go` (import and register `reviewqueue.New(nil)` after `jira.New(nil)`)
- Test: `internal/integrations/reviewqueue/reviewqueue_test.go`

**Interfaces:**
- Consumes: Task 1's `GET /api/queue`; Task 2's `Ticket.Rereview`.
- Produces: integration ID `reviewqueue`, `Name()` "Review Queue", fields `url` and `min_score`. Task 4 relies on the name.

- [ ] **Step 1: Write the failing tests** in `internal/integrations/reviewqueue/reviewqueue_test.go`:

```go
package reviewqueue

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/integrations"
)

// server answers every request with status and body, and records the last request's path.
func server(t *testing.T, status int, body string) (*httptest.Server, *string) {
	t.Helper()
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &path
}

func cfg(url, minScore string) integrations.Config {
	return integrations.Config{Values: map[string]string{"url": url, "min_score": minScore}}
}

func pull(t *testing.T, c integrations.Config) integrations.Source {
	t.Helper()
	sources := New(nil).Pull(context.Background(), c, time.Time{}, time.Time{})
	if len(sources) != 1 || sources[0].Label != "Review Queue" {
		t.Fatalf("sources = %+v", sources)
	}
	return sources[0]
}

const queue = `{"fetchedAt": "2026-10-15T14:02:11.000Z", "pulls": [
	{"id": "acme/search#1", "url": "https://github.com/acme/search/pull/1", "title": "Cache facet counts",
	 "repository": "acme/search", "author": "alex", "score": 170,
	 "reasons": ["Review requested from you", "Re-review requested after your review"], "estimate": "5–10 min", "rereview": true},
	{"id": "acme/platform#7", "url": "https://github.com/acme/platform/pull/7", "title": "Bump lodash",
	 "repository": "acme/platform", "author": "renovate[bot]", "score": 70,
	 "reasons": ["Assigned maintenance repository", "Waiting for review"], "estimate": "5–10 min", "rereview": false}
]}`

func TestPullOffersTheRankedQueue(t *testing.T) {
	srv, path := server(t, http.StatusOK, queue)
	src := pull(t, cfg(srv.URL+"/", ""))

	want := []integrations.Ticket{
		{
			Key: "acme/search#1", Title: "Review acme/search#1: Cache facet counts",
			Notes: "Score 170 · Review requested from you, Re-review requested after your review · 5–10 min",
			URL:   "https://github.com/acme/search/pull/1", Rereview: true,
		},
		{
			Key: "acme/platform#7", Title: "Review acme/platform#7: Bump lodash",
			Notes: "Score 70 · Assigned maintenance repository, Waiting for review · 5–10 min",
			URL:   "https://github.com/acme/platform/pull/7",
		},
	}
	if diff := cmp.Diff(want, src.Tickets); diff != "" || src.Err != nil || src.More != 0 || *path != "/api/queue" {
		t.Errorf("err %v, more %d, path %q, tickets (-want +got):\n%s", src.Err, src.More, *path, diff)
	}
}

func TestMinScoreCutsTheLowerPulls(t *testing.T) {
	srv, _ := server(t, http.StatusOK, queue)
	for minScore, want := range map[string]int{" 100 ": 1, "70": 2, "-5": 2, "171": 0} {
		if src := pull(t, cfg(srv.URL, minScore)); src.Err != nil || len(src.Tickets) != want {
			t.Errorf("min %q: err %v, %d tickets, want %d", minScore, src.Err, len(src.Tickets), want)
		}
	}
	if src := pull(t, cfg(srv.URL, "high")); src.Err == nil || src.Err.Error() != "minimum score must be a whole number" {
		t.Errorf("min high: err %v", src.Err)
	}
}

func TestALongQueueIsCappedAndCountsTheRest(t *testing.T) {
	var pulls []string
	for i := range maxResults + 3 {
		pulls = append(pulls, fmt.Sprintf(`{"id": "acme/x#%d", "title": "t", "score": 1, "reasons": []}`, i))
	}
	srv, _ := server(t, http.StatusOK, `{"pulls": [`+strings.Join(pulls, ",")+`]}`)
	src := pull(t, cfg(srv.URL, ""))
	if len(src.Tickets) != maxResults || src.More != 3 || src.Tickets[0].Key != "acme/x#0" || src.Tickets[0].Notes != "Score 1" {
		t.Errorf("%d tickets, more %d, first %+v", len(src.Tickets), src.More, src.Tickets[0])
	}
}

func TestPullErrorsSayWhatToDo(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	cases := map[string]struct {
		url    string
		status int
		body   string
		want   string
	}{
		"not running":      {url: gone.URL, want: "Can't reach Review Queue — is it running?"},
		"not a url":        {url: "localhost:5173", want: "that address isn't a URL, such as http://localhost:5173"},
		"demo source":      {status: http.StatusConflict, body: `{"error": "Set Review Queue's source to GitHub first."}`, want: "Set Review Queue's source to GitHub first."},
		"github failed":    {status: http.StatusBadGateway, body: `{"error": "GitHub rate limit reached."}`, want: "GitHub rate limit reached."},
		"no error message": {status: http.StatusNotFound, body: "Cannot GET /api/queue", want: "Review Queue answered 404"},
		"not json":         {status: http.StatusOK, body: "<!doctype html>", want: "couldn't read Review Queue's answer — is the address right?"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if c.url == "" {
				srv, _ := server(t, c.status, c.body)
				c.url = srv.URL
			}
			src := pull(t, cfg(c.url, ""))
			if src.Err == nil || src.Err.Error() != c.want || src.Tickets != nil {
				t.Errorf("err %v, tickets %+v; want %q", src.Err, src.Tickets, c.want)
			}
		})
	}
}

func TestATimeoutBlamesGitHubNotReviewQueue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	sources := New(&http.Client{Timeout: 50 * time.Millisecond}).Pull(context.Background(), cfg(srv.URL, ""), time.Time{}, time.Time{})
	if err := sources[0].Err; err == nil || err.Error() != "Review Queue took over a minute — GitHub may be slow, try again" {
		t.Errorf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/integrations/reviewqueue/`
Expected: build failure, `undefined: New`.

- [ ] **Step 3: Write the integration** in `internal/integrations/reviewqueue/reviewqueue.go`:

```go
// Package reviewqueue offers the pull requests Review Queue ranks for review, from its local
// server.
package reviewqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/integrations"
)

// maxResults caps a pull. A pick list longer than this means the minimum score is too low.
const maxResults = 50

var (
	errUnreachable = errors.New("Can't reach Review Queue — is it running?")
	// errSlow is a timeout, which is GitHub being slow behind Review Queue, not Review Queue down.
	errSlow = errors.New("Review Queue took over a minute — GitHub may be slow, try again")
)

// ReviewQueue is the Review Queue integration.
type ReviewQueue struct {
	client *http.Client
}

// New returns the integration. A nil client means one that gives up after 60 seconds, since
// Review Queue may scan a whole organization.
func New(client *http.Client) *ReviewQueue {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &ReviewQueue{client: client}
}

func (*ReviewQueue) ID() string                { return "reviewqueue" }
func (*ReviewQueue) Name() string              { return "Review Queue" }
func (*ReviewQueue) Pulls() integrations.Pulls { return integrations.PullsTickets }

func (*ReviewQueue) Fields() []integrations.Field {
	return []integrations.Field{
		{Key: "url", Label: "Address", Kind: integrations.Text, Required: true, Help: "Where Review Queue runs, such as http://localhost:5173"},
		{Key: "min_score", Label: "Minimum score", Kind: integrations.Text, Help: "Hide PRs scoring below this, such as 100"},
	}
}

// Pull fetches the ranked queue. The window is ignored: a review isn't for a day.
func (q *ReviewQueue) Pull(ctx context.Context, cfg integrations.Config, _, _ time.Time) []integrations.Source {
	src := integrations.Source{Label: "Review Queue"}
	src.Tickets, src.More, src.Err = q.queue(ctx, cfg)
	return []integrations.Source{src}
}

type queueResult struct {
	Pulls []struct {
		ID       string   `json:"id"`
		URL      string   `json:"url"`
		Title    string   `json:"title"`
		Score    int      `json:"score"`
		Reasons  []string `json:"reasons"`
		Estimate string   `json:"estimate"`
		Rereview bool     `json:"rereview"`
	} `json:"pulls"`
}

// queue returns the pulls scoring at least the minimum, in Review Queue's order, and how many
// more there were past the cap. Its errors never wrap the client's.
func (q *ReviewQueue) queue(ctx context.Context, cfg integrations.Config) ([]integrations.Ticket, int, error) {
	minScore := 0
	if s := strings.TrimSpace(cfg.Values["min_score"]); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, 0, errors.New("minimum score must be a whole number")
		}
		minScore = n
	}
	base := strings.TrimRight(cfg.Values["url"], "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/queue", nil)
	if err != nil || req.URL.Scheme == "" || req.URL.Host == "" {
		return nil, 0, errors.New("that address isn't a URL, such as http://localhost:5173")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := q.client.Do(req)
	if err != nil {
		if errTimeout(err) {
			return nil, 0, errSlow
		}
		return nil, 0, errUnreachable
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if errTimeout(err) {
			return nil, 0, errSlow
		}
		return nil, 0, errUnreachable
	}
	if resp.StatusCode != http.StatusOK {
		var bad struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &bad) == nil && bad.Error != "" {
			return nil, 0, errors.New(bad.Error)
		}
		return nil, 0, fmt.Errorf("Review Queue answered %d", resp.StatusCode)
	}
	var res queueResult
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, 0, errors.New("couldn't read Review Queue's answer — is the address right?")
	}
	tickets := []integrations.Ticket{}
	more := 0
	for _, p := range res.Pulls {
		if p.Score < minScore {
			continue
		}
		if len(tickets) == maxResults {
			more++
			continue
		}
		tickets = append(tickets, integrations.Ticket{
			Key:      p.ID,
			Title:    "Review " + p.ID + ": " + p.Title,
			Notes:    notes(p.Score, p.Reasons, p.Estimate),
			URL:      p.URL,
			Rereview: p.Rereview,
		})
	}
	return tickets, more, nil
}

func errTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// notes reads as "Score 170 · Review requested from you, Small change · 5–10 min", leaving out
// a part Review Queue left empty.
func notes(score int, reasons []string, estimate string) string {
	parts := []string{fmt.Sprintf("Score %d", score)}
	for _, p := range []string{strings.Join(reasons, ", "), estimate} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}
```

- [ ] **Step 4: Register it** in `internal/app/app.go`. Add the import `"github.com/jameynakama/whatnow/internal/integrations/reviewqueue"` after the `jira` one, and:

```go
		Integrations: integrations.NewRunner(db, svc, hub, o.Loc, o.Now,
			gcal.New(nil), jira.New(nil), reviewqueue.New(nil),
		),
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go vet ./... && go test ./...`
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/integrations/reviewqueue internal/app/app.go
git commit -m "feat: offer Review Queue's ranked PRs as tickets

Pulls GET /api/queue from a running Review Queue, drops PRs below an
optional minimum score, and carries re-requested reviews through so
they come back past a done task."
```

---

### Task 4: The TUI offer names its integrations

**Files (whatnow):**
- Modify: `internal/tui/jira.go` (`jiraLines`, new `names`)
- Test: `internal/tui/jira_test.go`

**Interfaces:**
- Consumes: Task 3's registration and "Review Queue" name.

- [ ] **Step 1: Write the failing test**, in `internal/tui/jira_test.go` before `TestJiraView`, and update the copy `TestTheMorningPicksJiraTickets` waits for, from `"Pull your Jira tickets?"` to `"Pull from Jira?"`:

```go
func TestTheOfferNamesEachTicketIntegration(t *testing.T) {
	f := newFixture(t)
	f.jira(t, false)
	ctx := context.Background()
	if _, err := f.app.Integrations.Save(ctx, "reviewqueue", integrations.Update{Values: map[string]string{"url": "http://localhost:5173"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Integrations.SetEnabled(ctx, "reviewqueue", true); err != nil {
		t.Fatal(err)
	}
	m := f.morningAt(t)
	m = send(m, m.loadIntegrations()(), key("esc"))
	if lines, _ := m.jiraLines(80, 20); lines[0] != "Pull from Jira and Review Queue?" {
		t.Errorf("offer = %q", lines[0])
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/tui/ -run 'Offer|PicksJira'`
Expected: FAIL, `offer = "Pull your Jira tickets?"`, and the morning test timing out waiting for "Pull from Jira?".

- [ ] **Step 3: Name them** in `internal/tui/jira.go`. Add `"strings"` to the imports, make the offer:

```go
		return []string{"Pull from " + names(t.on) + "?"}, "enter pull · tab skip"
```

and add, before `ticketLine`:

```go
// names reads as "Jira", "Jira and Review Queue", or "A, B and C".
func names(views []integrations.View) string {
	var out []string
	for _, v := range views {
		out = append(out, v.Name)
	}
	if len(out) < 2 {
		return strings.Join(out, "")
	}
	return strings.Join(out[:len(out)-1], ", ") + " and " + out[len(out)-1]
}
```

- [ ] **Step 4: Run the gates**

Run: `just test && just lint`
Expected: all pass; the golden files are unchanged (they show the step after a pull).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/jira.go internal/tui/jira_test.go
git commit -m "feat: name each ticket integration in the TUI's offer"
```

---

### Task 5: Try it live

- [ ] **Step 1:** With Review Queue running (`npm run dev` on `ranked-queue`, source set to GitHub), `curl -s localhost:5173/api/queue | head -c 600` shows ranked pulls.
- [ ] **Step 2:** In whatnow's settings, set Review Queue's address to `http://localhost:5173`, turn it on, and **Pull**: the pick list shows "Review owner/repo#n: …" tickets in Review Queue's order. Set a minimum score and pull again: the low ones are gone.
- [ ] **Step 3:** In the TUI morning, the offer reads "Pull from Jira and Review Queue?" (or just Review Queue if Jira is off).
