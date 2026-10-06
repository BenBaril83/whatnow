# whatnow web UI, part 1 (Today) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A usable daily Today page embedded in the `whatnow` binary: plan tiers, capture, What now, a collapsible chat dock, undo toasts, and a bare settings form.

**Architecture:** A SvelteKit 3 SPA (adapter-static, `200.html` fallback) in `web/`. `just web` builds it and copies it into `internal/web/dist`, which the Go binary embeds and serves for every GET outside `/api`. The client refetches `/today`, `/events`, and `/tasks` whenever the SSE stream reports a change, and it builds chat replies from `chat.*` events. The server stays the single source of truth.

**Tech Stack:** Go 1.x stdlib `net/http` and `embed`; SvelteKit 3 and Svelte 5 runes; TypeScript strict; Vitest 4; plain CSS custom properties.

**Spec:** `docs/superpowers/specs/2026-10-05-whatnow-web-ui-today-design.md`. The parent spec is `docs/superpowers/specs/2026-10-05-whatnow-design.md`.

## Global Constraints

- Node 24, pinned in `web/.nvmrc`. npm is the package manager.
- `web/.npmrc` sets `registry=https://registry.npmjs.org/`. The user-level npm config points at a private registry that rejects these packages.
- SvelteKit 3 uses `#lib/...` (Node subpath imports), not `$lib`. Importing a `.ts` module through `#lib` needs its extension: `'#lib/api.ts'`, `'#lib/toasts.svelte.ts'`. Relative imports inside `src/lib` stay extensionless.
- Svelte 5 runes only. Shared state is a class with `$state` fields in a `.svelte.ts` module.
- No Tailwind and no component library. Components use the tokens in `tokens.css`, never raw colors.
- Every non-GET request sends `Content-Type: application/json` and a JSON body, even `{}`. The server rejects any other write.
- UI copy is American English, with no em dashes.
- `go build` and `go test ./...` must pass on a clean clone with no Node installed.
- Never save an API key during manual testing. A saved key goes to the real OS keychain.

## Review Focus

These are the inputs most likely to hurt someone that no automated test fully covers. The components have no unit tests by design (Playwright arrives in 5c), so the ones that live in components are pinned by the manual smoke test in Task 8.

1. **A write fails** (server down, 400): the checkbox unticks, capture puts the text back, and a red toast says why. `Toasts.track` returning `false` is tested in Task 5. The component behavior is in Task 8's smoke steps.
2. **The stream drops mid-turn:** after it reconnects, the page refetches Today and the chat, and the header shows "reconnecting…" while it's down. Tested in Task 5 (`Stream`).
3. **A plan task changes elsewhere** (another tab, chat, or a push): it leaves its tier before the next replan. Tested in Task 4 (`tiers`).
4. **What now finds nothing eligible** (404): a gentle note, not an error toast. Task 8 smoke step.
5. **Two tabs chat at once** (409): a "One sec, still answering." notice, and the input keeps the text. Tested in Task 5 (`Chat.send`).

---

### Task 1: Name each batch's source on `batch.applied`

The UI decides where a batch's undo goes from the event alone. A stored batch's actor can't tell chat from cleanup, since both are `agent`, so the event gets its own `source`. The schema doesn't change.

**Files:**
- Modify: `internal/stream/hub.go`, `internal/stream/hub_test.go`
- Modify: `internal/httpapi/server.go` (`publishBatch`), `internal/httpapi/tasks_test.go`
- Modify: `internal/agent/agent.go`, `internal/agent/agent_test.go`
- Modify: `internal/cleanup/cleanup.go`, `internal/cleanup/cleanup_test.go`
- Modify: `internal/planner/close.go`, `internal/planner/close_test.go`, `internal/planner/triggers_test.go`

**Interfaces:**
- Produces: `stream.Source` (`SourceUser`, `SourceChat`, `SourceCleanup`, `SourceTriage`), `BatchApplied.Source` (JSON `source`), and `Hub.PublishBatch(batch int64, source Source, activity []domain.Activity)`. The web client reads `{batch_id, source}`.

- [ ] **Step 1: Write the failing tests.** Each publisher's existing test also asserts the source.

`internal/stream/hub_test.go`, in `TestPublishBatchSendsTypedPayload`:
```go
	hub.PublishBatch(7, stream.SourceCleanup, []domain.Activity{{ID: 1, BatchID: 7}})

	m := <-ch
	applied, ok := m.Data.(stream.BatchApplied)
	if m.Type != "batch.applied" || !ok || applied.BatchID != 7 || applied.Source != stream.SourceCleanup || len(applied.Activity) != 1 {
```

`internal/httpapi/tasks_test.go`, in `TestCaptureCreatesTaskAndPublishes`:
```go
		if m.Type != "batch.applied" || !ok || applied.BatchID != got.BatchID || applied.Source != stream.SourceUser || len(applied.Activity) != 1 {
```

`internal/planner/close_test.go`, where the test reads the published batch:
```go
	if applied, ok := m.Data.(stream.BatchApplied); !ok || applied.BatchID != batch || applied.Source != stream.SourceTriage || len(applied.Activity) != 3 {
```

`internal/cleanup/cleanup_test.go`, where the test reads the published batch:
```go
	if applied, ok := m.Data.(stream.BatchApplied); !ok || applied.BatchID != batch || applied.Source != stream.SourceCleanup || len(applied.Activity) != 4 {
```

`internal/agent/agent_test.go`, in the test that subscribes to `batch.applied` and expects `reply.Error != ""` with one tool. Also add the `internal/stream` import:
```go
	if got := drain(msgs); len(got) != 1 || got[0].Data.(stream.BatchApplied).Source != stream.SourceChat {
		t.Errorf("published %+v, want the one chat batch with the write", got)
	}
```

`internal/planner/triggers_test.go`, the helper that publishes a batch:
```go
	f.hub.PublishBatch(batch, stream.SourceUser, acts)
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./...`
Expected: FAIL: `undefined: stream.SourceCleanup`, plus too many arguments to `PublishBatch`.

- [ ] **Step 3: Implement.** In `internal/stream/hub.go`, replace `BatchApplied` and `PublishBatch` with:
```go
// Source is what made a batch. The web client uses it to pick where the batch's undo appears.
type Source string

const (
	SourceUser    Source = "user"
	SourceChat    Source = "chat"
	SourceCleanup Source = "cleanup"
	SourceTriage  Source = "triage"
)

// BatchApplied is the batch.applied payload, sent after every write.
type BatchApplied struct {
	BatchID  int64             `json:"batch_id"`
	Source   Source            `json:"source"`
	Activity []domain.Activity `json:"activity"`
}

// PublishBatch announces a batch's changes to every client.
func (h *Hub) PublishBatch(batch int64, source Source, activity []domain.Activity) {
	h.Publish(Message{Type: "batch.applied", Data: BatchApplied{BatchID: batch, Source: source, Activity: activity}})
}
```

Then pass each caller's source:
- `internal/httpapi/server.go` `publishBatch`: `s.Hub.PublishBatch(batch, stream.SourceUser, acts)`
- `internal/agent/agent.go` `Send`: `a.hub.PublishBatch(t.batchID, stream.SourceChat, acts)`
- `internal/cleanup/cleanup.go`: `r.hub.PublishBatch(batch, stream.SourceCleanup, acts)`
- `internal/planner/close.go`: `p.hub.PublishBatch(batch, stream.SourceTriage, acts)`, and add `"github.com/jameynakama/whatnow/internal/stream"` to its imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: no gofmt output, and every package `ok`.

- [ ] **Step 5: Commit**
```bash
git add internal
git commit -m "Name each batch's source on batch.applied"
```

---

### Task 2: Serve the embedded web client

**Files:**
- Create: `internal/web/web.go`, `internal/web/web_test.go`, `internal/web/dist/.gitkeep` (empty)
- Create: `internal/httpapi/static_test.go`
- Modify: `internal/httpapi/server.go`, `internal/httpapi/helpers_test.go`, `cmd/whatnow/main.go`, `.gitignore`

**Interfaces:**
- Produces: `web.Handler() http.Handler`, and `httpapi.Deps.Static http.Handler`, which serves every GET outside `/api`. Task 3's `just web` fills `internal/web/dist`.

- [ ] **Step 1: Write the failing tests**

`internal/web/web_test.go`:
```go
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
```

`internal/httpapi/static_test.go`:
```go
package httpapi_test

import (
	"net/http"
	"testing"
)

func TestStaticServesEveryGETOutsideTheAPI(t *testing.T) {
	api := newAPI(t)

	rec := api.do(http.MethodGet, "/settings", nil)
	wantStatus(t, rec, http.StatusOK)
	if rec.Body.String() != "static" {
		t.Errorf("GET /settings = %q, want the web client", rec.Body)
	}
	// A wrong method on an API route is still the API's answer, not the client.
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today", map[string]string{}), http.StatusMethodNotAllowed)
}
```

In `internal/httpapi/helpers_test.go`, add a stub to the `httpapi.Deps` literal in `newAPI`. `io` is already imported.
```go
		Static:  http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "static") }),
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/web ./internal/httpapi`
Expected: FAIL: `undefined: handler`, `unknown field Static`.

- [ ] **Step 3: Implement**

Create the empty `internal/web/dist/.gitkeep`, then `internal/web/web.go`:
```go
// Package web serves the web client, built into dist by `just web` and embedded in the binary.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// shell is the page SvelteKit writes for every route it didn't prerender. The client router takes
// over from it.
const shell = "200.html"

const notBuilt = `<!doctype html><title>whatnow</title><p>UI not built, run <code>just build</code>.</p>`

// Handler serves the embedded client.
func Handler() http.Handler {
	files, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time, so it is always there
	}
	return handler(files)
}

// handler serves the file a path names, and the app shell for any other path, so a client route
// such as /settings loads the app. A path under /api/ is never the app: an unknown API route is a
// 404.
func handler(files fs.FS) http.Handler {
	static := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if info, err := fs.Stat(files, name); err == nil && !info.IsDir() {
			static.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page, err := fs.ReadFile(files, shell)
		if err != nil {
			page = []byte(notBuilt)
		}
		w.Write(page)
	})
}
```

In `internal/httpapi/server.go`:
- Change the package comment to `// Package httpapi serves the REST API under /api/v1/, the SSE stream, and the web client.`
- Add this field to `Deps`, after `Agent`:
```go
	// Static serves the web client for every GET outside the API.
	Static http.Handler
```
- Register it last in `New`, just before `return localOnly(mux)`. A `GET` pattern keeps a wrong method on an API route a 405 instead of the app:
```go
	mux.Handle("GET /", d.Static)
```

In `cmd/whatnow/main.go`, import `"github.com/jameynakama/whatnow/internal/web"` and add `Static: web.Handler(),` to the `httpapi.Deps` literal.

Append to `.gitignore`:
```
internal/web/dist/*
!internal/web/dist/.gitkeep
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: every package `ok`, including `internal/web`.

- [ ] **Step 5: Commit**
```bash
git add .gitignore cmd internal
git commit -m "Serve the embedded web client for every GET outside the API"
```

---

### Task 3: Scaffold the SvelteKit app and the build recipes

**Files:**
- Create: `web/` (scaffolded), then replace or create the files below
- Modify: `Justfile`

**Interfaces:**
- Produces: `npm test`, `npm run check`, and `npm run build` in `web/`; `just web`, `just build`, and `just dev`; the `#lib/tokens.css` classes `.sticker`, `.btn`, `.btn.primary`, `.chip`, `.label`, `.muted`, and `.field`; and a layout that mounts `#lib/components/Toasts.svelte` (created in Task 6).

- [ ] **Step 1: Scaffold.** From the repo root, overriding the private registry for this one command:
```bash
npm_config_registry=https://registry.npmjs.org/ npx --yes sv@1.1.1 create web --template minimal --types ts --add "vitest=usages:unit" "sveltekit-adapter=adapter:static" --install npm
cd web && rm -rf src/lib/vitest-examples src/lib/index.ts README.md .vscode
```

- [ ] **Step 2: Configure.** Set `"name": "whatnow-web"` in `web/package.json`, and replace its `"imports"` with `{ "#lib/*": "./src/lib/*" }`, since there is no `src/lib/index.ts`.

`web/.npmrc`:
```
engine-strict=true
registry=https://registry.npmjs.org/
```

`web/.nvmrc`:
```
24
```

`web/vite.config.ts`:
```ts
import { defineConfig } from 'vitest/config';
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			// Every route falls back to one shell page; the Go server serves it for any unknown path.
			adapter: adapter({ fallback: '200.html' })
		})
	],
	server: {
		// `just serve` runs the API here. The SSE stream goes through the same proxy.
		proxy: { '/api': 'http://127.0.0.1:7777' }
	},
	test: {
		expect: { requireAssertions: true },
		include: ['src/**/*.test.ts'],
		environment: 'node'
	}
});
```

`web/src/routes/+layout.ts`:
```ts
// The Go server is the backend, so the app renders only in the browser.
export const ssr = false;
```

`web/src/lib/tokens.css`:
```css
/* The sticker look: chunky ink outlines, flat offset shadows, bright tier fills. Components use
   these tokens, never raw colors. */
:root {
	--paper: #fff8e7;
	--paper-deep: #f1eadb;
	--card: #ffffff;
	--ink: #1f1a3d;
	--ink-soft: #6b6590;
	--must: #ffd9d2;
	--should: #d8ecff;
	--could: #ffffff;
	--chip: #ffd84d;
	--primary: #7b5cff;
	--on-primary: #ffffff;
	--now: #ff4d6d;
	--event: #e3ffd8;
	--danger: #ff4d6d;

	--line: 2px solid var(--ink);
	--shadow: 3px 3px 0 var(--ink);
	--shadow-sm: 2px 2px 0 var(--ink);
	--radius: 12px;
	--radius-sm: 8px;
	--pill: 999px;

	--font: 'Avenir Next', 'Nunito', system-ui, sans-serif;
	--heavy: 800;
}

*,
*::before,
*::after {
	box-sizing: border-box;
}

body {
	margin: 0;
	background: var(--paper);
	color: var(--ink);
	font-family: var(--font);
	font-size: 15px;
}

button,
input,
select,
textarea {
	font: inherit;
	color: inherit;
}

button {
	cursor: pointer;
}

a {
	color: var(--primary);
	font-weight: var(--heavy);
}

.sticker {
	background: var(--card);
	border: var(--line);
	border-radius: var(--radius);
	box-shadow: var(--shadow);
}

.btn {
	background: var(--card);
	border: var(--line);
	border-radius: var(--radius-sm);
	box-shadow: var(--shadow-sm);
	padding: 6px 12px;
	font-weight: var(--heavy);
}

.btn:active {
	box-shadow: none;
	transform: translate(2px, 2px);
}

.btn.primary {
	background: var(--primary);
	color: var(--on-primary);
}

.chip {
	background: var(--chip);
	border: var(--line);
	border-radius: var(--pill);
	padding: 1px 10px;
	font-size: 13px;
	font-weight: var(--heavy);
}

.label {
	font-size: 12px;
	font-weight: var(--heavy);
	letter-spacing: 0.06em;
	text-transform: uppercase;
}

.muted {
	color: var(--ink-soft);
}

input.field,
textarea.field,
select.field {
	background: var(--card);
	border: var(--line);
	border-radius: var(--radius-sm);
	padding: 8px 10px;
	width: 100%;
}
```

`web/src/lib/assets/favicon.svg`:
```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect x="4" y="4" width="25" height="25" rx="7" fill="#1f1a3d"/><rect x="2" y="2" width="25" height="25" rx="7" fill="#7b5cff" stroke="#1f1a3d" stroke-width="2"/><text x="14.5" y="21" font-family="system-ui,sans-serif" font-size="16" font-weight="800" fill="#fff" text-anchor="middle">?</text></svg>
```

`web/src/routes/+layout.svelte`. It mounts `Toasts`, which arrives in Task 6. Until then, leave the `Toasts` import and tag out:
```svelte
<script lang="ts">
	import '#lib/tokens.css';
	import favicon from '#lib/assets/favicon.svg';
	import Toasts from '#lib/components/Toasts.svelte';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<title>whatnow</title>
</svelte:head>

{@render children()}
<Toasts />
```

Replace `web/src/routes/+page.svelte` with a placeholder until Task 7:
```svelte
<p>whatnow</p>
```

- [ ] **Step 3: Add the recipes.** Replace `Justfile` with:
```just
default:
    @just --list

test:
    go test ./...
    cd web && npm test

cover:
    go test -coverpkg=./... -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out | tail -1

lint:
    go vet ./...
    cd web && npm run check

# Builds the web client into the directory the Go binary embeds.
web:
    cd web && npm ci && npm run build
    find internal/web/dist -mindepth 1 ! -name .gitkeep -delete
    cp -R web/build/. internal/web/dist/

build: web
    go build -o whatnow ./cmd/whatnow

# Positional arguments keep quoted paths with spaces intact.
[positional-arguments]
serve *args:
    go run ./cmd/whatnow serve "$@"

# Runs the API and the Vite dev server, which proxies /api to it. Open http://localhost:5173.
[positional-arguments]
dev *args:
    #!/usr/bin/env bash
    trap 'kill 0' EXIT
    go run ./cmd/whatnow serve "$@" &
    cd web && npm run dev
```

- [ ] **Step 4: Verify the build end to end**

Run: `just build && cd web && npm run check`
Expected: `Wrote site to "build"`, `internal/web/dist/200.html` exists, the `whatnow` binary builds, and svelte-check reports `0 ERRORS`. `git status` shows nothing under `internal/web/dist` except `.gitkeep`.

- [ ] **Step 5: Commit**
```bash
git add Justfile web
git commit -m "Scaffold the SvelteKit web client and its build recipes"
```

---

### Task 4: API client and pure Today logic

**Files:**
- Create: `web/src/lib/api.ts`, `web/src/lib/api.test.ts`
- Create: `web/src/lib/time.ts`, `web/src/lib/time.test.ts`
- Create: `web/src/lib/plan.ts`, `web/src/lib/plan.test.ts`

**Interfaces:**
- Produces, from `api.ts`: the types `Task`, `CalendarEvent`, `Day`, `Plan`, `PlanEntry`, `Today`, `Pick`, `ToolUse`, `Message`, `Mutation`, `BatchApplied`, `Source`, `Settings`, `SettingsView`, `Provider`, `KeySource`, `ConnectionResult`, and `Tier`; `ApiError {status, message}`; `paceLabels: string[]` (index = pace − 1); `configured(view): boolean`; and `api.{today, events(date), tasks, chat, capture(raw), setStatus(id, status), push(id, until), undo(batch), setPace(pace), start(pace, mood), whatNow(exclude?, reason?), send(text), settings, saveSettings(s), saveKey(provider, key), testConnection}`.
- Produces, from `time.ts`: `eventEnd`, `localDate`, `addDays(date, n)`, `countdown(events, now): string | null`, `strip(events, now, endOfDay): {items: {event, left, width}[], now}`, and `survived(events, now): number`.
- Produces, from `plan.ts`: `Card {task, rationale, blocked}`, `tiers(today, tasks): Record<Tier, Card[]>`, `unplanned(today, tasks): Card[]`, `doneOn(date, tasks): Task[]`, and `winLine(done, meetings): string`.

- [ ] **Step 1: Write the failing tests**

`web/src/lib/api.test.ts`:
```ts
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api, configured, type SettingsView } from './api';

afterEach(() => vi.unstubAllGlobals());

function stubFetch(status: number, body: unknown) {
	const fetch = vi.fn(async () => new Response(status === 204 ? null : JSON.stringify(body), { status }));
	vi.stubGlobal('fetch', fetch);
	return fetch;
}

describe('api', () => {
	it('Should send every write as JSON, even with no body to send', async () => {
		const fetch = stubFetch(204, null);
		await api.undo(4);
		expect(fetch).toHaveBeenCalledWith('/api/v1/batches/4/undo', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: '{}'
		});
	});

	it("Should throw the server's message with its status", async () => {
		stubFetch(409, { error: 'a reply is still being written' });
		await expect(api.send('hi')).rejects.toEqual(new ApiError(409, 'a reply is still being written'));
	});
});

describe('configured', () => {
	const view = (provider: '' | 'anthropic', key: '' | 'env'): SettingsView => ({
		settings: {
			provider,
			anthropic: { main_model: '', light_model: '', base_url: '' },
			openai: { main_model: '', light_model: '', base_url: '' },
			style: '',
			about_me: '',
			end_of_day: '17:30'
		},
		keys: { anthropic: key, openai: '' }
	});

	it.each([
		['no provider', false, view('', 'env')],
		['a provider with no key', false, view('anthropic', '')],
		['a provider with a key', true, view('anthropic', 'env')]
	])('Should count %s as set up: %s', (_, want, v) => {
		expect(configured(v)).toBe(want);
	});
});
```

`web/src/lib/time.test.ts`:
```ts
import { describe, expect, it } from 'vitest';
import type { CalendarEvent } from './api';
import { addDays, countdown, strip, survived } from './time';

const at = (hhmm: string) => new Date(`2026-10-05T${hhmm}:00`);
const ev = (title: string, start: string, end: string | null = null): CalendarEvent => ({
	id: 0,
	title,
	starts_at: at(start).toISOString(),
	ends_at: end ? at(end).toISOString() : null
});

describe('countdown', () => {
	const events = [ev('Standup', '09:30', '09:45'), ev('1:1', '13:00', '13:30')];

	it.each([
		['before the first event', '09:06', 'Standup in 24m'],
		['during an event', '09:40', 'Standup now'],
		['between events, over an hour out', '11:55', '1:1 in 1h 5m'],
		['an exact hour out', '12:00', '1:1 in 1h'],
		['after the last event', '14:00', null]
	])('Should say the right thing %s', (_, now, want) => {
		expect(countdown(events, at(now))).toBe(want);
	});

	it('Should treat an event with no end as 30 minutes long', () => {
		const open = [ev('Focus', '10:00')];
		expect(countdown(open, at('10:29'))).toBe('Focus now');
		expect(countdown(open, at('10:30'))).toBeNull();
	});

	it('Should find the next event when events arrive out of order', () => {
		expect(countdown([ev('Late', '15:00'), ev('Soon', '10:00')], at('09:00'))).toBe('Soon in 1h');
	});

	it('Should say nothing with no events', () => {
		expect(countdown([], at('09:00'))).toBeNull();
	});
});

describe('strip', () => {
	const eod = at('17:30');

	it('Should run from now to the end of day with no events', () => {
		const s = strip([], at('09:30'), eod);
		expect(s.items).toEqual([]);
		expect(s.now).toBe(0);
	});

	it('Should start at the first event when it is earlier than now', () => {
		const s = strip([ev('Standup', '09:00', '09:30')], at('13:00'), at('17:00'));
		expect(s.items[0].left).toBe(0);
		expect(s.items[0].width).toBeCloseTo(6.25); // 30 of 480 minutes
		expect(s.now).toBe(50);
	});

	it('Should draw an event with no end as 30 minutes', () => {
		const s = strip([ev('Focus', '13:00')], at('09:00'), at('17:00'));
		expect(s.items[0].width).toBeCloseTo(6.25);
	});

	it('Should stretch past the end of day to keep now and late events on it', () => {
		const late = strip([ev('Late call', '18:00', '19:00')], at('09:00'), at('17:00'));
		expect(late.items[0].left + late.items[0].width).toBeCloseTo(100);
		const after = strip([], at('20:00'), at('17:00'));
		expect(after.now).toBe(0);
		expect(Number.isFinite(after.now)).toBe(true);
	});
});

describe('survived', () => {
	it('Should count only events that have ended', () => {
		const events = [ev('A', '09:00', '09:30'), ev('B', '09:45'), ev('C', '11:00', '12:00')];
		expect(survived(events, at('10:15'))).toBe(2);
	});
});

describe('addDays', () => {
	it.each([
		['2026-10-05', '2026-10-06'],
		['2026-10-31', '2026-11-01'],
		['2026-12-31', '2027-01-01'],
		['2026-03-07', '2026-03-08'] // the night before a US DST change
	])('Should give the day after %s', (date, want) => {
		expect(addDays(date, 1)).toBe(want);
	});
});
```

`web/src/lib/plan.test.ts`:
```ts
import { describe, expect, it } from 'vitest';
import type { Status, Task, Today } from './api';
import { doneOn, tiers, unplanned, winLine } from './plan';

const task = (id: number, status: Status = 'open', updated_at = '2026-10-05T15:00:00Z'): Task => ({
	id,
	title: `task ${id}`,
	raw_text: '',
	size: null,
	energy: null,
	feeling: '',
	not_before: null,
	status,
	updated_at
});

const today = (over: Partial<Today> = {}): Today => ({
	date: '2026-10-05',
	day: { date: '2026-10-05', pace: 3 },
	plan: {
		id: 1,
		opening: '',
		fallback: false,
		entries: [
			{ task_id: 1, tier: 'must', rationale: 'due today' },
			{ task_id: 2, tier: 'should', rationale: 'r2' },
			{ task_id: 3, tier: 'must', rationale: 'r3' },
			{ task_id: 4, tier: 'could', rationale: 'gone' }
		]
	},
	unplanned: [],
	blocked: [],
	...over
});

describe('tiers', () => {
	it('Should group open plan tasks by tier in plan order, with blocked marked', () => {
		const got = tiers(today({ blocked: [3] }), [task(1), task(2), task(3)]);
		expect(got.must.map((c) => [c.task.id, c.blocked])).toEqual([
			[1, false],
			[3, true]
		]);
		expect(got.should[0].rationale).toBe('r2');
		expect(got.could).toEqual([]); // task 4 was deleted, so it isn't listed
	});

	it('Should leave out plan tasks finished or dropped since', () => {
		const got = tiers(today(), [task(1, 'done'), task(2, 'dropped'), task(3)]);
		expect(got.must.map((c) => c.task.id)).toEqual([3]);
		expect(got.should).toEqual([]);
	});

	it('Should leave out a plan task pushed past today, but keep one due back today', () => {
		const got = tiers(today(), [
			{ ...task(1), not_before: '2026-10-06' },
			{ ...task(3), not_before: '2026-10-05' }
		]);
		expect(got.must.map((c) => c.task.id)).toEqual([3]);
	});

	it('Should be empty when there is no plan yet', () => {
		expect(tiers(today({ plan: null }), [task(1)])).toEqual({ must: [], should: [], could: [] });
	});
});

describe('unplanned', () => {
	it('Should keep the server order and skip tasks it no longer has', () => {
		const got = unplanned(today({ unplanned: [9, 7, 8], blocked: [7] }), [task(7), task(9)]);
		expect(got.map((c) => [c.task.id, c.blocked])).toEqual([
			[9, false],
			[7, true]
		]);
	});
});

describe('doneOn', () => {
	it('Should list tasks done on that local date only', () => {
		const noonToday = new Date('2026-10-05T12:00:00').toISOString();
		const noonYesterday = new Date('2026-10-04T12:00:00').toISOString();
		const got = doneOn('2026-10-05', [
			task(1, 'done', noonToday),
			task(2, 'done', noonYesterday),
			task(3, 'open', noonToday)
		]);
		expect(got.map((t) => t.id)).toEqual([1]);
	});
});

describe('winLine', () => {
	it.each([
		[0, 0, 'Nothing checked off yet'],
		[1, 0, '✓ 1 thing done'],
		[6, 3, '✓ 6 things done, 3 meetings survived'],
		[0, 1, '✓ 1 meeting survived']
	])('Should read well for %i done and %i meetings', (done, meetings, want) => {
		expect(winLine(done, meetings)).toBe(want);
	});
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npm test`
Expected: FAIL: cannot resolve `./api`, `./time`, `./plan`.

- [ ] **Step 3: Implement**

`web/src/lib/api.ts`:
```ts
// Types mirror the Go JSON. Only the fields the client reads are listed.

export type Size = 'S' | 'M' | 'L';
export type Energy = 'low' | 'med' | 'high';
export type Status = 'open' | 'done' | 'dropped';
export type Tier = 'must' | 'should' | 'could';
export type Source = 'user' | 'chat' | 'cleanup' | 'triage';
export type Provider = 'anthropic' | 'openai';
/** Where a provider's key comes from. Empty means there is none. */
export type KeySource = '' | 'keychain' | 'env';

export interface Task {
	id: number;
	title: string;
	raw_text: string;
	size: Size | null;
	energy: Energy | null;
	feeling: string;
	/** A push sets this. The task is put off until then. */
	not_before: string | null;
	status: Status;
	updated_at: string;
}

export interface CalendarEvent {
	id: number;
	title: string;
	starts_at: string;
	ends_at: string | null;
}

export interface Day {
	date: string;
	pace: number;
}

/** The five pace levels, 1 to 5, as the server labels them. */
export const paceLabels = ['running on fumes', 'gentle', 'steady', 'full steam', 'go hard'];

export interface PlanEntry {
	task_id: number;
	tier: Tier;
	rationale: string;
}

export interface Plan {
	id: number;
	opening: string;
	entries: PlanEntry[];
	fallback: boolean;
}

export interface Today {
	date: string;
	day: Day | null;
	plan: Plan | null;
	unplanned: number[];
	blocked: number[];
}

export interface Pick {
	task_id: number;
	reason: string;
}

export interface ToolUse {
	tool: string;
	label: string;
	error: string;
}

export interface Message {
	id: number;
	role: 'user' | 'assistant';
	text: string;
	tools: ToolUse[] | null;
	/** The reply's changes, or 0 when it changed nothing. */
	batch_id: number;
	/** Set on a reply the provider couldn't give. */
	error: string;
}

/** The response to every write. */
export interface Mutation {
	batch_id: number;
}

export interface BatchApplied {
	batch_id: number;
	source: Source;
}

export interface ProviderConfig {
	main_model: string;
	light_model: string;
	base_url: string;
}

export interface Settings {
	provider: Provider | '';
	anthropic: ProviderConfig;
	openai: ProviderConfig;
	style: string;
	about_me: string;
	end_of_day: string;
}

export interface SettingsView {
	settings: Settings;
	keys: Record<Provider, KeySource>;
}

export interface ConnectionResult {
	ok: boolean;
	error?: string;
}

export class ApiError extends Error {
	constructor(
		readonly status: number,
		message: string
	) {
		super(message);
	}
}

// Every write sends a JSON body, even an empty one: the server rejects any other write as a
// possible cross-site form post, and decodes a body on most writes.
async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	const init: RequestInit = { method };
	if (method !== 'GET') {
		init.headers = { 'Content-Type': 'application/json' };
		init.body = JSON.stringify(body ?? {});
	}
	const res = await fetch(`/api/v1${path}`, init);
	if (res.status === 204) return undefined as T;
	const data = await res.json().catch(() => ({}));
	if (!res.ok) throw new ApiError(res.status, data.error ?? res.statusText);
	return data as T;
}

export const api = {
	today: () => request<Today>('GET', '/today'),
	events: (date: string) => request<CalendarEvent[]>('GET', `/events?date=${date}`),
	tasks: () => request<Task[]>('GET', '/tasks'),
	chat: () => request<Message[]>('GET', '/chat'),
	capture: (raw_text: string) => request<Mutation>('POST', '/tasks', { raw_text }),
	setStatus: (id: number, status: Status) =>
		request<Mutation>('POST', `/tasks/${id}/status`, { status }),
	push: (id: number, until: string) => request<Mutation>('POST', `/tasks/${id}/push`, { until }),
	undo: (batch: number) => request<void>('POST', `/batches/${batch}/undo`),
	setPace: (pace: number) => request<Mutation>('PUT', '/today/pace', { pace, note: '' }),
	start: (pace: number, mood_note: string) =>
		request<unknown>('POST', '/today/start', { pace, mood_note, triage: [] }),
	whatNow: (exclude_task_id = 0, reason = '') =>
		request<Pick>('POST', '/whatnow', { exclude_task_id, reason }),
	send: (text: string) => request<Message>('POST', '/chat', { text }),
	settings: () => request<SettingsView>('GET', '/settings'),
	saveSettings: (s: Settings) => request<SettingsView>('PUT', '/settings', s),
	saveKey: (provider: Provider, key: string) =>
		request<void>('PUT', `/settings/keys/${provider}`, { key }),
	testConnection: () => request<ConnectionResult>('POST', '/settings/test')
};

/** Whether a model is set up. Without one, capture still works and plans come from code. */
export function configured(view: SettingsView): boolean {
	const p = view.settings.provider;
	return p !== '' && view.keys[p] !== '';
}
```

`web/src/lib/time.ts`:
```ts
import type { CalendarEvent } from './api';

const minute = 60_000;

/** An event with no end counts as 30 minutes, the same as the server's facts. */
export function eventEnd(e: CalendarEvent): number {
	return e.ends_at ? Date.parse(e.ends_at) : Date.parse(e.starts_at) + 30 * minute;
}

/** The local YYYY-MM-DD of a moment. */
export function localDate(d: Date): string {
	const pad = (n: number) => String(n).padStart(2, '0');
	return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

export function addDays(date: string, days: number): string {
	const d = new Date(`${date}T12:00:00`); // noon keeps a DST shift from changing the date
	d.setDate(d.getDate() + days);
	return localDate(d);
}

function span(ms: number): string {
	const mins = Math.ceil(ms / minute);
	if (mins < 60) return `${mins}m`;
	const h = Math.floor(mins / 60);
	const m = mins % 60;
	return m ? `${h}h ${m}m` : `${h}h`;
}

/** "Standup now" while an event runs, else "Standup in 24m" for the next one, else null. */
export function countdown(events: CalendarEvent[], now: Date): string | null {
	const t = now.getTime();
	const current = events.find((e) => Date.parse(e.starts_at) <= t && t < eventEnd(e));
	if (current) return `${current.title} now`;
	const next = events
		.filter((e) => Date.parse(e.starts_at) > t)
		.sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at))[0];
	return next ? `${next.title} in ${span(Date.parse(next.starts_at) - t)}` : null;
}

export interface StripItem {
	event: CalendarEvent;
	/** Percent of the strip's width. */
	left: number;
	width: number;
}

export interface Strip {
	items: StripItem[];
	now: number;
}

/**
 * Places events on a strip from the earlier of now and the first event, to the end of day. The
 * strip stretches past the end of day to keep now and every event on it, and is at least an hour.
 */
export function strip(events: CalendarEvent[], now: Date, endOfDay: Date): Strip {
	const t = now.getTime();
	const start = Math.min(t, ...events.map((e) => Date.parse(e.starts_at)));
	const end = Math.max(endOfDay.getTime(), t, start + 60 * minute, ...events.map(eventEnd));
	const pct = (ms: number) => ((ms - start) / (end - start)) * 100;
	return {
		items: events.map((event) => ({
			event,
			left: pct(Date.parse(event.starts_at)),
			width: pct(eventEnd(event)) - pct(Date.parse(event.starts_at))
		})),
		now: pct(t)
	};
}

/** Today's events that have ended. */
export function survived(events: CalendarEvent[], now: Date): number {
	return events.filter((e) => eventEnd(e) <= now.getTime()).length;
}
```

`web/src/lib/plan.ts`:
```ts
import type { Task, Tier, Today } from './api';
import { localDate } from './time';

export interface Card {
	task: Task;
	rationale: string;
	blocked: boolean;
}

export type Tiers = Record<Tier, Card[]>;

/**
 * The plan's open tasks by tier, in plan order. A task the plan names but that is finished,
 * dropped, deleted, or pushed past today since is left out: it isn't something to do today any
 * more, and the plan only catches up at the next replan.
 */
export function tiers(today: Today, tasks: Task[]): Tiers {
	const byId = new Map(tasks.map((t) => [t.id, t]));
	const blocked = new Set(today.blocked);
	const out: Tiers = { must: [], should: [], could: [] };
	for (const e of today.plan?.entries ?? []) {
		const task = byId.get(e.task_id);
		if (task?.status !== 'open' || (task.not_before ?? '') > today.date) continue;
		out[e.tier].push({ task, rationale: e.rationale, blocked: blocked.has(task.id) });
	}
	return out;
}

/** Eligible tasks the plan leaves out, in the server's order. */
export function unplanned(today: Today, tasks: Task[]): Card[] {
	const byId = new Map(tasks.map((t) => [t.id, t]));
	const blocked = new Set(today.blocked);
	return today.unplanned.flatMap((id) => {
		const task = byId.get(id);
		return task ? [{ task, rationale: '', blocked: blocked.has(id) }] : [];
	});
}

/** Tasks marked done on the given local date. */
export function doneOn(date: string, tasks: Task[]): Task[] {
	return tasks.filter((t) => t.status === 'done' && localDate(new Date(t.updated_at)) === date);
}

function count(n: number, one: string, many: string): string {
	return `${n} ${n === 1 ? one : many}`;
}

/** The Done today header: the day's win. */
export function winLine(done: number, meetings: number): string {
	if (done === 0 && meetings === 0) return 'Nothing checked off yet';
	const parts: string[] = [];
	if (done > 0) parts.push(count(done, 'thing done', 'things done'));
	if (meetings > 0) parts.push(count(meetings, 'meeting survived', 'meetings survived'));
	return `✓ ${parts.join(', ')}`;
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && npm test && npm run check`
Expected: all tests pass, and `0 ERRORS`.

- [ ] **Step 5: Commit**
```bash
git add web/src/lib
git commit -m "Add the web API client and Today's time and plan logic"
```

---

### Task 5: Client state: toasts, Today, chat, and the stream

**Files:**
- Create: `web/src/lib/toasts.svelte.ts`, `web/src/lib/toasts.test.ts`
- Create: `web/src/lib/today.svelte.ts`, `web/src/lib/today.test.ts`
- Create: `web/src/lib/chat.svelte.ts`, `web/src/lib/chat.test.ts`
- Create: `web/src/lib/stream.svelte.ts`, `web/src/lib/stream.test.ts`

**Interfaces:**
- Consumes: `api`, `ApiError`, and the types from Task 4.
- Produces: `toasts: Toasts` with `items`, `show(text, batch)`, `error(err)`, `dismiss(id)`, `track(text, write): Promise<boolean>`, `applied(b)`, `undone(batch)`, and `undo(toast)`.
- Produces: `today: TodayState` with `today`, `events`, `tasks`, `refresh()`, and `changed()` (debounced 100ms).
- Produces: `chat: Chat` with `messages`, `draft`, `busy`, `notice`, `undone`, `open`, `unread`, `load()`, `delta(text)`, `tool(use)`, `message(m)`, `markUndone(batch)`, `toggle()`, and `send(text): Promise<boolean>`.
- Produces: `new Stream(handlers, onReconnect, connect?)` with `connected`, `open()`, and `close()`.

- [ ] **Step 1: Write the failing tests**

`web/src/lib/toasts.test.ts`:
```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, type Source } from './api';
import { Toasts } from './toasts.svelte';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('Toasts', () => {
	it.each([
		['cleanup', 1],
		['user', 0],
		['chat', 0],
		['triage', 0]
	] as [Source, number][])('Should toast a %s batch from the stream %i times', (source, want) => {
		const t = new Toasts();
		t.applied({ batch_id: 5, source });
		expect(t.items.length).toBe(want);
	});

	it('Should offer undo for a write that worked', async () => {
		const t = new Toasts();
		expect(await t.track('Done', async () => ({ batch_id: 9 }))).toBe(true);
		expect(t.items).toMatchObject([{ text: 'Done', batch: 9 }]);
	});

	it('Should show the error, with no undo, for a write that failed', async () => {
		const t = new Toasts();
		const worked = await t.track('Done', async () => {
			throw new ApiError(400, 'title is required');
		});
		expect(worked).toBe(false);
		expect(t.items).toMatchObject([{ text: 'title is required', error: true }]);
		expect(t.items[0].batch).toBeUndefined();
	});

	it('Should drop the toast when its batch is undone, from any tab', () => {
		const t = new Toasts();
		t.show('Pushed', 3);
		t.show('Dropped', 4);
		t.undone(3);
		expect(t.items.map((x) => x.batch)).toEqual([4]);
	});

	it('Should dismiss itself after about 8 seconds', () => {
		const t = new Toasts();
		t.show('Pushed', 3);
		vi.advanceTimersByTime(7999);
		expect(t.items.length).toBe(1);
		vi.advanceTimersByTime(1);
		expect(t.items.length).toBe(0);
	});
});
```

`web/src/lib/today.test.ts`:
```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, type Today } from './api';
import { TodayState } from './today.svelte';

const day: Today = { date: '2026-10-05', day: null, plan: null, unplanned: [], blocked: [] };

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
	vi.useRealTimers();
	vi.restoreAllMocks();
});

describe('TodayState', () => {
	it("Should fetch today's events for the server's date", async () => {
		vi.spyOn(api, 'today').mockResolvedValue(day);
		const events = vi.spyOn(api, 'events').mockResolvedValue([]);
		vi.spyOn(api, 'tasks').mockResolvedValue([]);
		await new TodayState().refresh();
		expect(events).toHaveBeenCalledWith('2026-10-05');
	});

	it('Should refetch once for a burst of changes', async () => {
		const fetched = vi.spyOn(api, 'today').mockResolvedValue(day);
		vi.spyOn(api, 'events').mockResolvedValue([]);
		vi.spyOn(api, 'tasks').mockResolvedValue([]);
		const t = new TodayState();
		t.changed();
		t.changed();
		t.changed();
		await vi.advanceTimersByTimeAsync(100);
		expect(fetched).toHaveBeenCalledTimes(1);
	});
});
```

`web/src/lib/chat.test.ts`:
```ts
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api, type Message } from './api';
import { Chat } from './chat.svelte';

afterEach(() => vi.restoreAllMocks());

const reply = (id: number, text: string, over: Partial<Message> = {}): Message => ({
	id,
	role: 'assistant',
	text,
	tools: null,
	batch_id: 0,
	error: '',
	...over
});

describe('Chat', () => {
	it('Should build a draft from deltas and chips, then swap in the stored reply', () => {
		const c = new Chat();
		c.tool({ tool: 'complete_task', label: 'Done: taxes', error: '' });
		c.delta('Nice, ');
		c.delta('that one is off your plate.');
		expect(c.draft).toEqual({
			text: 'Nice, that one is off your plate.',
			tools: [{ tool: 'complete_task', label: 'Done: taxes', error: '' }]
		});

		c.message(reply(2, 'Nice, that one is off your plate!', { batch_id: 7 }));
		expect(c.draft).toBeNull();
		expect(c.messages.map((m) => m.text)).toEqual(['Nice, that one is off your plate!']);
	});

	it('Should take a stored reply that arrives with no deltas before it', () => {
		const c = new Chat();
		c.message(reply(2, 'Short and sweet.'));
		expect(c.messages).toHaveLength(1);
		expect(c.draft).toBeNull();
	});

	it('Should keep a user message from ending the draft, and not list a message twice', () => {
		const c = new Chat();
		c.delta('Thinking');
		c.message({ ...reply(1, 'hi'), role: 'user' });
		expect(c.draft?.text).toBe('Thinking');
		c.message(reply(2, 'Hello'));
		c.message(reply(2, 'Hello')); // the stream and the response both carry the reply
		expect(c.messages.map((m) => m.id)).toEqual([1, 2]);
	});

	it('Should mark a reply unread only while the dock is collapsed', () => {
		const c = new Chat();
		c.message(reply(1, 'open dock'));
		expect(c.unread).toBe(false);
		c.toggle();
		c.message(reply(2, 'collapsed dock'));
		expect(c.unread).toBe(true);
		c.toggle();
		expect(c.unread).toBe(false);
	});

	it('Should keep the text and say so when another tab is mid-reply', async () => {
		vi.spyOn(api, 'send').mockRejectedValue(new ApiError(409, 'a reply is still being written'));
		const c = new Chat();
		expect(await c.send('hello')).toBe(false);
		expect(c.notice).toBe('One sec, still answering.');
		expect(c.busy).toBe(false);
	});

	it('Should add the reply from the response, in case the stream missed it', async () => {
		vi.spyOn(api, 'send').mockResolvedValue(reply(3, 'Done.'));
		const c = new Chat();
		expect(await c.send('finish taxes')).toBe(true);
		expect(c.messages.map((m) => m.text)).toEqual(['Done.']);
	});
});
```

`web/src/lib/stream.test.ts`:
```ts
import { describe, expect, it, vi } from 'vitest';
import { Stream, type Handlers, type Source } from './stream.svelte';

class FakeSource implements Source {
	listeners = new Map<string, (e: MessageEvent) => void>();
	addEventListener(type: string, listener: (e: MessageEvent) => void): void {
		this.listeners.set(type, listener);
	}
	emit(type: string, data: unknown = null): void {
		this.listeners.get(type)?.(new MessageEvent(type, { data: JSON.stringify(data) }));
	}
	close(): void {}
}

function handlers(): Handlers {
	return {
		'batch.applied': vi.fn(),
		'batch.undone': vi.fn(),
		'plan.updated': vi.fn(),
		'chat.delta': vi.fn(),
		'chat.tool': vi.fn(),
		'chat.message': vi.fn()
	};
}

describe('Stream', () => {
	it('Should route each event to its handler by name, parsed', () => {
		const fake = new FakeSource();
		const h = handlers();
		new Stream(h, vi.fn(), () => fake).open();

		fake.emit('chat.delta', { text: 'hi' });
		fake.emit('batch.applied', { batch_id: 4, source: 'cleanup' });

		expect(h['chat.delta']).toHaveBeenCalledWith({ text: 'hi' });
		expect(h['batch.applied']).toHaveBeenCalledWith({ batch_id: 4, source: 'cleanup' });
		expect(h['chat.message']).not.toHaveBeenCalled();
	});

	it('Should catch up after a reconnect, but not on the first connect', () => {
		const fake = new FakeSource();
		const caughtUp = vi.fn();
		const s = new Stream(handlers(), caughtUp, () => fake);
		s.open();

		fake.emit('open');
		expect(caughtUp).not.toHaveBeenCalled();
		expect(s.connected).toBe(true);

		fake.emit('error');
		expect(s.connected).toBe(false);
		fake.emit('open');
		expect(caughtUp).toHaveBeenCalledTimes(1);
	});
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npm test`
Expected: FAIL: cannot resolve the four `.svelte` modules.

- [ ] **Step 3: Implement**

`web/src/lib/toasts.svelte.ts`:
```ts
import { api, type BatchApplied, type Mutation } from './api';

export interface Toast {
	id: number;
	text: string;
	/** The batch Undo reverts. Absent on an error. */
	batch?: number;
	error?: boolean;
}

const lifetime = 8000;

export class Toasts {
	items = $state<Toast[]>([]);
	#next = 1;

	#add(toast: Omit<Toast, 'id'>): void {
		const id = this.#next++;
		this.items.push({ id, ...toast });
		setTimeout(() => this.dismiss(id), lifetime);
	}

	show(text: string, batch: number): void {
		this.#add({ text, batch });
	}

	error(err: unknown): void {
		this.#add({ text: err instanceof Error ? err.message : String(err), error: true });
	}

	dismiss(id: number): void {
		this.items = this.items.filter((t) => t.id !== id);
	}

	/** Runs a write and offers its undo, or shows why it failed. Returns whether it worked. */
	async track(text: string, write: () => Promise<Mutation>): Promise<boolean> {
		try {
			this.show(text, (await write()).batch_id);
			return true;
		} catch (err) {
			this.error(err);
			return false;
		}
	}

	/**
	 * Only cleanup's batches get a toast from the stream. A user write gets its toast from its own
	 * response, a chat batch's undo is on its reply, and triage belongs to the morning and wrap-up
	 * flows.
	 */
	applied(b: BatchApplied): void {
		if (b.source === 'cleanup') this.show("Here's what I did", b.batch_id);
	}

	undone(batch: number): void {
		this.items = this.items.filter((t) => t.batch !== batch);
	}

	async undo(toast: Toast): Promise<void> {
		if (toast.batch === undefined) return;
		try {
			await api.undo(toast.batch);
		} catch (err) {
			this.error(err);
		}
	}
}

export const toasts = new Toasts();
```

`web/src/lib/today.svelte.ts`:
```ts
import { api, type CalendarEvent, type Task, type Today } from './api';
import { toasts } from './toasts.svelte';

const debounce = 100;

/** Today's state, refetched whole whenever the server says something changed. */
export class TodayState {
	today = $state<Today | null>(null);
	events = $state<CalendarEvent[]>([]);
	tasks = $state<Task[]>([]);
	#timer: ReturnType<typeof setTimeout> | undefined;

	async refresh(): Promise<void> {
		const today = await api.today();
		const [events, tasks] = await Promise.all([api.events(today.date), api.tasks()]);
		this.today = today;
		this.events = events;
		this.tasks = tasks;
	}

	/** Refreshes once a burst of changes settles. */
	changed(): void {
		clearTimeout(this.#timer);
		this.#timer = setTimeout(() => this.refresh().catch((err) => toasts.error(err)), debounce);
	}
}

export const today = new TodayState();
```

`web/src/lib/chat.svelte.ts`:
```ts
import { api, ApiError, type Message, type ToolUse } from './api';

/** The reply being written, built from deltas and tool chips until its stored message lands. */
export interface Draft {
	text: string;
	tools: ToolUse[];
}

export class Chat {
	messages = $state<Message[]>([]);
	draft = $state<Draft | null>(null);
	busy = $state(false);
	/** A gentle line under the input, such as when another tab is mid-reply. */
	notice = $state('');
	/** Reply batches undone, so their Undo buttons read "undone". */
	undone = $state<number[]>([]);
	open = $state(true);
	/** A reply landed while the dock was collapsed. */
	unread = $state(false);

	async load(): Promise<void> {
		this.messages = await api.chat();
	}

	delta(text: string): void {
		this.draft ??= { text: '', tools: [] };
		this.draft.text += text;
	}

	tool(use: ToolUse): void {
		this.draft ??= { text: '', tools: [] };
		this.draft.tools.push(use);
	}

	/** A stored message is authoritative: it replaces the draft it finishes. */
	message(m: Message): void {
		const i = this.messages.findIndex((x) => x.id === m.id);
		if (i >= 0) this.messages[i] = m;
		else this.messages.push(m);
		if (m.role === 'assistant') {
			this.draft = null;
			if (!this.open) this.unread = true;
		}
	}

	markUndone(batch: number): void {
		this.undone.push(batch);
	}

	toggle(): void {
		this.open = !this.open;
		if (this.open) this.unread = false;
	}

	/** Sends a message. Returns false when it wasn't taken, so the input keeps the text. */
	async send(text: string): Promise<boolean> {
		this.busy = true;
		this.notice = '';
		try {
			this.message(await api.send(text));
			return true;
		} catch (err) {
			if (err instanceof ApiError && err.status === 409) {
				this.notice = 'One sec, still answering.';
			} else {
				this.notice = err instanceof Error ? err.message : String(err);
			}
			return false;
		} finally {
			this.busy = false;
		}
	}
}

export const chat = new Chat();
```

`web/src/lib/stream.svelte.ts`:
```ts
import type { BatchApplied, Message, ToolUse } from './api';

export interface Handlers {
	'batch.applied': (d: BatchApplied) => void;
	'batch.undone': (d: { batch_id: number }) => void;
	'plan.updated': () => void;
	'chat.delta': (d: { text: string }) => void;
	'chat.tool': (d: ToolUse) => void;
	'chat.message': (d: Message) => void;
}

/** The parts of EventSource the stream uses, so tests can drive it. */
export interface Source {
	addEventListener(type: string, listener: (e: MessageEvent) => void): void;
	close(): void;
}

/**
 * One connection to the server's event stream. EventSource reconnects on its own; when it does,
 * onReconnect runs, since anything sent while it was down is lost.
 */
export class Stream {
	connected = $state(false);
	#source: Source | null = null;

	constructor(
		private handlers: Handlers,
		private onReconnect: () => void,
		private connect: () => Source = () => new EventSource('/api/v1/stream')
	) {}

	open(): void {
		const source = this.connect();
		let opened = false;
		source.addEventListener('open', () => {
			if (opened) this.onReconnect();
			opened = true;
			this.connected = true;
		});
		source.addEventListener('error', () => {
			this.connected = false;
		});
		for (const [name, handle] of Object.entries(this.handlers)) {
			source.addEventListener(name, (e) => handle(JSON.parse(e.data)));
		}
		this.#source = source;
	}

	close(): void {
		this.#source?.close();
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && npm test && npm run check`
Expected: 50 tests pass, and `0 ERRORS`.

- [ ] **Step 5: Commit**
```bash
git add web/src/lib
git commit -m "Add the web client's toast, Today, chat, and stream state"
```

---

### Task 6: Today components

Components carry no unit tests (see the spec's Testing section). `npm run check` and Task 8's smoke test cover them.

**Files:**
- Create: `web/src/lib/components/Toasts.svelte`, `Header.svelte`, `DayStrip.svelte`, `Capture.svelte`, `TaskCard.svelte`, `WhatNow.svelte`, `StartDay.svelte`, `DoneToday.svelte`, `ChatDock.svelte`
- Modify: `web/src/routes/+layout.svelte` (restore the `Toasts` import and tag from Task 3)

**Interfaces:**
- Consumes: everything from Tasks 4 and 5.
- Produces these props:
  - `Header {today, events, now, connected, configured, asking, onwhatnow}`
  - `DayStrip {events, now, endOfDay}`
  - `Capture {}`
  - `TaskCard {card, tier?, date}`
  - `WhatNow {pick, task, asking, onnotthis(reason), onclose}`
  - `StartDay {}`
  - `DoneToday {done, meetings}`
  - `ChatDock {opening}`

- [ ] **Step 1: Write the components**

`web/src/lib/components/Toasts.svelte`:
```svelte
<script lang="ts">
	import { toasts } from '#lib/toasts.svelte.ts';
</script>

<div class="stack" aria-live="polite">
	{#each toasts.items as toast (toast.id)}
		<div class="sticker toast" class:error={toast.error}>
			<span>{toast.text}</span>
			{#if toast.batch !== undefined}
				<button class="btn" onclick={() => toasts.undo(toast)}>Undo</button>
			{/if}
			<button class="close" aria-label="Dismiss" onclick={() => toasts.dismiss(toast.id)}>✕</button>
		</div>
	{/each}
</div>

<style>
	.stack {
		position: fixed;
		left: 16px;
		bottom: 16px;
		display: flex;
		flex-direction: column;
		gap: 8px;
		z-index: 30;
		max-width: min(420px, calc(100vw - 32px));
	}
	.toast {
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 10px 12px;
		font-weight: var(--heavy);
	}
	.toast span {
		flex: 1;
	}
	.error {
		background: var(--must);
	}
	.close {
		background: none;
		border: none;
		padding: 0 4px;
	}
</style>
```

`web/src/lib/components/Header.svelte`:
```svelte
<script lang="ts">
	import { api, paceLabels, type CalendarEvent, type Today } from '#lib/api.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { countdown } from '#lib/time.ts';

	interface Props {
		today: Today;
		events: CalendarEvent[];
		now: Date;
		connected: boolean;
		/** False when no model is set up. */
		configured: boolean;
		asking: boolean;
		onwhatnow: () => void;
	}
	let { today, events, now, connected, configured, asking, onwhatnow }: Props = $props();

	let paceOpen = $state(false);
	const next = $derived(countdown(events, now));
	const date = $derived(
		new Date(`${today.date}T12:00:00`).toLocaleDateString(undefined, {
			weekday: 'long',
			month: 'short',
			day: 'numeric'
		})
	);

	function setPace(pace: number) {
		paceOpen = false;
		toasts.track(`Pace: ${paceLabels[pace - 1]}`, () => api.setPace(pace));
	}
</script>

<header class="sticker">
	<div class="left">
		<h1>{date}</h1>
		{#if today.day}
			<details class="pace" bind:open={paceOpen}>
				<summary class="chip">pace: {paceLabels[today.day.pace - 1]}</summary>
				<div class="sticker menu">
					{#each paceLabels as label, i (label)}
						<button class="item" class:current={i + 1 === today.day.pace} onclick={() => setPace(i + 1)}>
							{label}
						</button>
					{/each}
				</div>
			</details>
		{/if}
		{#if !connected}
			<span class="chip offline">reconnecting…</span>
		{/if}
	</div>
	<div class="right">
		{#if next}
			<span class="next">{next}</span>
		{/if}
		<a class="muted" href="/settings">Settings</a>
		<button class="btn primary" disabled={asking || !today.day} onclick={onwhatnow}>
			{asking ? 'Thinking…' : 'What now?'}
		</button>
	</div>
	{#if !configured}
		<p class="notice">
			No model set up yet. Capture works and plans come out rough. <a href="/settings">Set one up</a>
		</p>
	{/if}
</header>

<style>
	header {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		padding: 12px 16px;
	}
	.left,
	.right {
		display: flex;
		align-items: center;
		gap: 12px;
	}
	h1 {
		margin: 0;
		font-size: 22px;
		font-weight: var(--heavy);
	}
	.next {
		font-weight: var(--heavy);
	}
	.pace {
		position: relative;
	}
	.pace summary {
		list-style: none;
		cursor: pointer;
	}
	.menu {
		position: absolute;
		top: 32px;
		left: 0;
		z-index: 10;
		display: flex;
		flex-direction: column;
		padding: 6px;
		min-width: 170px;
	}
	.item {
		background: none;
		border: none;
		text-align: left;
		padding: 6px 8px;
		border-radius: var(--radius-sm);
	}
	.item:hover,
	.item.current {
		background: var(--chip);
	}
	.offline {
		background: var(--must);
	}
	.notice {
		flex-basis: 100%;
		margin: 0;
		font-size: 14px;
	}
</style>
```

`web/src/lib/components/DayStrip.svelte`:
```svelte
<script lang="ts">
	import type { CalendarEvent } from '#lib/api.ts';
	import { strip } from '#lib/time.ts';

	interface Props {
		events: CalendarEvent[];
		now: Date;
		endOfDay: Date;
	}
	let { events, now, endOfDay }: Props = $props();

	const layout = $derived(strip(events, now, endOfDay));
	const time = (iso: string) =>
		new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
</script>

<div class="sticker strip" aria-label="Today's events">
	{#each layout.items as item (item.event.id)}
		<div
			class="event"
			style:left="{item.left}%"
			style:width="{item.width}%"
			title="{time(item.event.starts_at)} {item.event.title}"
		>
			{item.event.title}
		</div>
	{/each}
	<div class="now" style:left="{layout.now}%"></div>
	{#if events.length === 0}
		<span class="empty muted">No events today. Wide open.</span>
	{/if}
</div>

<style>
	.strip {
		position: relative;
		height: 48px;
		overflow: hidden;
	}
	.event {
		position: absolute;
		top: 8px;
		bottom: 8px;
		background: var(--event);
		border: var(--line);
		border-radius: var(--radius-sm);
		padding: 4px 6px;
		/* A short event grows to fit its title rather than cutting it off. Hover gives the time. */
		min-width: max-content;
		font-size: 12px;
		font-weight: var(--heavy);
		white-space: nowrap;
	}
	.event:hover {
		z-index: 2;
	}
	.now {
		position: absolute;
		top: 0;
		bottom: 0;
		width: 3px;
		background: var(--now);
		z-index: 1;
	}
	.empty {
		position: absolute;
		left: 16px;
		top: 13px;
		font-size: 14px;
	}
</style>
```

`web/src/lib/components/Capture.svelte`:
```svelte
<script lang="ts">
	import { api } from '#lib/api.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	let text = $state('');

	// The box clears at once: the raw task is saved before cleanup ever runs, so capture never
	// waits on a model. A failed save puts the text back rather than losing it.
	async function capture(e: SubmitEvent) {
		e.preventDefault();
		const raw = text.trim();
		if (!raw) return;
		text = '';
		if (!(await toasts.track('Captured', () => api.capture(raw))) && !text) text = raw;
	}
</script>

<form class="capture" onsubmit={capture}>
	<input class="field" bind:value={text} placeholder="Capture anything… press Enter" aria-label="Capture" />
</form>

<style>
	.capture input {
		box-shadow: var(--shadow-sm);
		padding: 12px 14px;
	}
</style>
```

`web/src/lib/components/TaskCard.svelte`:
```svelte
<script lang="ts">
	import { api, type Tier } from '#lib/api.ts';
	import type { Card } from '#lib/plan.ts';
	import { addDays } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	interface Props {
		card: Card;
		/** Absent for an unplanned card. */
		tier?: Tier;
		/** Today's date, for push to tomorrow. */
		date: string;
	}
	let { card, tier, date }: Props = $props();

	let menuOpen = $state(false);
	let why = $state(false);
	const task = $derived(card.task);

	// The card leaves once the refetch lands. If the write failed, untick it again.
	async function complete(e: Event & { currentTarget: HTMLInputElement }) {
		const box = e.currentTarget;
		if (!(await toasts.track(`Done: ${task.title}`, () => api.setStatus(task.id, 'done')))) {
			box.checked = false;
		}
	}
	function push() {
		menuOpen = false;
		toasts.track(`Pushed to tomorrow: ${task.title}`, () => api.push(task.id, addDays(date, 1)));
	}
	function drop() {
		menuOpen = false;
		toasts.track(`Dropped: ${task.title}`, () => api.setStatus(task.id, 'dropped'));
	}
</script>

<article class="sticker card {tier ?? 'unplanned'}">
	<input type="checkbox" aria-label="Mark done" onchange={complete} />
	<button class="body" onclick={() => (why = !why)} disabled={!card.rationale}>
		<span class="title">
			{#if card.blocked}<span title="Blocked by another task">⛓</span>{/if}
			{task.title}
		</span>
		<span class="badges">
			{#if task.size}<span class="badge">{task.size}</span>{/if}
			{#if task.energy}<span class="badge">{task.energy} energy</span>{/if}
		</span>
		{#if task.feeling}<span class="feeling">"{task.feeling}"</span>{/if}
		{#if card.rationale}<span class="why" class:shown={why}>{card.rationale}</span>{/if}
	</button>
	<details class="more" bind:open={menuOpen}>
		<summary aria-label="More actions">⋯</summary>
		<div class="sticker menu">
			<button onclick={push}>Push to tomorrow</button>
			<button onclick={drop}>Drop</button>
		</div>
	</details>
</article>

<style>
	.card {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		padding: 10px 12px;
		margin-bottom: 10px;
	}
	.must {
		background: var(--must);
	}
	.should {
		background: var(--should);
	}
	.could {
		background: var(--could);
	}
	.unplanned {
		border-style: dashed;
		box-shadow: none;
	}
	input[type='checkbox'] {
		width: 20px;
		height: 20px;
		margin-top: 2px;
		accent-color: var(--primary);
	}
	.body {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 3px;
		background: none;
		border: none;
		padding: 0;
		text-align: left;
		cursor: default;
	}
	.body:not(:disabled) {
		cursor: pointer;
	}
	.title {
		font-weight: var(--heavy);
	}
	.badges {
		display: flex;
		gap: 6px;
	}
	.badge {
		font-size: 11px;
		font-weight: var(--heavy);
		border: 1.5px solid var(--ink);
		border-radius: var(--pill);
		padding: 0 7px;
		background: var(--card);
	}
	.feeling {
		font-style: italic;
		font-size: 13px;
		color: var(--ink-soft);
	}
	.why {
		display: none;
		font-size: 13px;
	}
	.why.shown,
	.card:hover .why {
		display: block;
	}
	.more {
		position: relative;
	}
	.more summary {
		list-style: none;
		cursor: pointer;
		font-weight: var(--heavy);
		padding: 0 4px;
	}
	.menu {
		position: absolute;
		right: 0;
		top: 24px;
		z-index: 10;
		display: flex;
		flex-direction: column;
		padding: 6px;
		min-width: 160px;
	}
	.menu button {
		background: none;
		border: none;
		text-align: left;
		padding: 6px 8px;
		border-radius: var(--radius-sm);
	}
	.menu button:hover {
		background: var(--chip);
	}
</style>
```

`web/src/lib/components/WhatNow.svelte`:
```svelte
<script lang="ts">
	import { api, type Pick, type Task } from '#lib/api.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	interface Props {
		pick: Pick;
		task: Task | undefined;
		asking: boolean;
		/** Asks again without this task, with the user's optional reason. */
		onnotthis: (reason: string) => void;
		onclose: () => void;
	}
	let { pick, task, asking, onnotthis, onclose }: Props = $props();

	let explaining = $state(false);
	let reason = $state('');

	async function done() {
		if (await toasts.track(`Done: ${task?.title}`, () => api.setStatus(pick.task_id, 'done'))) {
			onclose();
		}
	}

	function notThis(e: SubmitEvent) {
		e.preventDefault();
		onnotthis(reason.trim());
		explaining = false;
		reason = '';
	}
</script>

<section class="sticker whatnow" aria-live="polite">
	<button class="close" aria-label="Close" onclick={onclose}>✕</button>
	<p class="label">Do this now</p>
	<h2>{task?.title ?? 'That task'}</h2>
	<p>{pick.reason}</p>
	{#if explaining}
		<form onsubmit={notThis}>
			<input class="field" bind:value={reason} placeholder="Why not? (optional)" aria-label="Why not this one" />
			<button class="btn" disabled={asking}>Ask again</button>
		</form>
	{:else}
		<div class="actions">
			<button class="btn primary" onclick={done}>Done</button>
			<button class="btn" disabled={asking} onclick={() => (explaining = true)}>
				{asking ? 'Thinking…' : 'Not this one'}
			</button>
		</div>
	{/if}
</section>

<style>
	.whatnow {
		position: relative;
		background: var(--chip);
		padding: 16px 20px;
	}
	h2 {
		margin: 4px 0;
		font-size: 22px;
		font-weight: var(--heavy);
	}
	.close {
		position: absolute;
		top: 10px;
		right: 12px;
		background: none;
		border: none;
	}
	.actions,
	form {
		display: flex;
		gap: 10px;
	}
</style>
```

`web/src/lib/components/StartDay.svelte`:
```svelte
<script lang="ts">
	import { api, paceLabels } from '#lib/api.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { today } from '#lib/today.svelte.ts';

	let pace = $state(3);
	let mood = $state('');
	let starting = $state(false);

	// The good-morning flow replaces this card.
	async function start(e: SubmitEvent) {
		e.preventDefault();
		starting = true;
		try {
			await api.start(pace, mood.trim());
			await today.refresh();
		} catch (err) {
			toasts.error(err);
		} finally {
			starting = false;
		}
	}
</script>

<form class="sticker start" onsubmit={start}>
	<h2>Start my day</h2>
	<p class="label">Pace</p>
	<div class="paces">
		{#each paceLabels as label, i (label)}
			<button type="button" class="chip" class:picked={pace === i + 1} onclick={() => (pace = i + 1)}>
				{label}
			</button>
		{/each}
	</div>
	<textarea class="field" bind:value={mood} rows="2" placeholder="Anything on your mind? (optional)"></textarea>
	<button class="btn primary" disabled={starting}>{starting ? 'Planning…' : 'Plan my day'}</button>
</form>

<style>
	.start {
		display: flex;
		flex-direction: column;
		gap: 10px;
		padding: 16px 20px;
		align-items: flex-start;
	}
	h2 {
		margin: 0;
		font-weight: var(--heavy);
	}
	.paces {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
	}
	.chip {
		background: var(--card);
	}
	.chip.picked {
		background: var(--chip);
	}
</style>
```

`web/src/lib/components/DoneToday.svelte`:
```svelte
<script lang="ts">
	import type { Task } from '#lib/api.ts';
	import { winLine } from '#lib/plan.ts';

	interface Props {
		done: Task[];
		meetings: number;
	}
	let { done, meetings }: Props = $props();
</script>

<details class="sticker done">
	<summary>{winLine(done.length, meetings)}</summary>
	<ul>
		{#each done as task (task.id)}
			<li>{task.title}</li>
		{:else}
			<li class="muted">Nothing yet. That's allowed.</li>
		{/each}
	</ul>
</details>

<style>
	.done {
		padding: 10px 14px;
	}
	summary {
		font-weight: var(--heavy);
		cursor: pointer;
	}
	ul {
		margin: 8px 0 0;
		padding-left: 20px;
	}
	li {
		text-decoration: line-through;
		text-decoration-thickness: 2px;
	}
	li.muted {
		text-decoration: none;
	}
</style>
```

`web/src/lib/components/ChatDock.svelte`:
```svelte
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type ToolUse } from '#lib/api.ts';
	import { chat } from '#lib/chat.svelte.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	interface Props {
		/** The plan's opening line, shown first. It isn't a stored message. */
		opening: string;
	}
	let { opening }: Props = $props();

	const storageKey = 'whatnow.chat.open';
	let text = $state('');
	let log: HTMLElement | undefined = $state();

	// With no saved choice, the dock starts open on a wide screen and collapsed on a phone, where
	// it would cover the page.
	onMount(() => {
		let saved: string | null = null;
		try {
			saved = localStorage.getItem(storageKey);
		} catch {
			// Storage can be blocked; fall back to the screen size.
		}
		chat.open = saved === null ? !matchMedia('(max-width: 800px)').matches : saved === 'true';
	});

	function toggle() {
		chat.toggle();
		try {
			localStorage.setItem(storageKey, String(chat.open));
		} catch {
			// Not remembering is fine.
		}
	}

	$effect(() => {
		// Follow the conversation as it grows.
		void chat.messages.length;
		void chat.draft?.text;
		log?.scrollTo({ top: log.scrollHeight });
	});

	async function send(e: SubmitEvent) {
		e.preventDefault();
		const msg = text.trim();
		if (!msg || chat.busy) return;
		text = '';
		if (!(await chat.send(msg)) && !text) text = msg;
	}

	async function undo(batch: number) {
		try {
			await api.undo(batch);
		} catch (err) {
			toasts.error(err);
		}
	}

	const chipText = (t: ToolUse) => `${t.error ? '✕' : '✓'} ${t.label || t.tool}`;
</script>

{#if chat.open}
	<aside class="sticker dock">
		<header>
			<span class="label">Chat</span>
			<button class="collapse" aria-label="Collapse chat" onclick={toggle}>⟩</button>
		</header>
		<div class="log" bind:this={log}>
			{#if opening}
				<p class="bubble assistant">{opening}</p>
			{/if}
			{#each chat.messages as m (m.id)}
				<div class="bubble {m.role}" class:failed={m.error}>
					{#each m.tools ?? [] as t, i (i)}
						<span class="toolchip" class:bad={t.error} title={t.error}>{chipText(t)}</span>
					{/each}
					<p>{m.text}</p>
					{#if m.error}
						<a href="/settings">Check settings</a>
					{/if}
					{#if m.batch_id}
						{#if chat.undone.includes(m.batch_id)}
							<span class="muted small">undone</span>
						{:else}
							<button class="btn small" onclick={() => undo(m.batch_id)}>Undo</button>
						{/if}
					{/if}
				</div>
			{/each}
			{#if chat.draft}
				<div class="bubble assistant">
					{#each chat.draft.tools as t, i (i)}
						<span class="toolchip" class:bad={t.error}>{chipText(t)}</span>
					{/each}
					<p>{chat.draft.text || '…'}</p>
				</div>
			{:else if chat.busy}
				<p class="bubble assistant">…</p>
			{/if}
		</div>
		<form onsubmit={send}>
			<input class="field" bind:value={text} placeholder="Talk to whatnow…" aria-label="Message" />
		</form>
		{#if chat.notice}
			<p class="notice muted">{chat.notice}</p>
		{/if}
	</aside>
{:else}
	<button class="sticker tab" aria-label="Open chat" onclick={toggle}>
		⟨ Chat
		{#if chat.unread || chat.busy}<span class="dot"></span>{/if}
	</button>
{/if}

<style>
	.dock {
		display: flex;
		flex-direction: column;
		width: 360px;
		height: calc(100vh - 32px);
		position: sticky;
		top: 16px;
		padding: 12px;
		gap: 10px;
	}
	header {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}
	.collapse {
		background: none;
		border: none;
		font-size: 18px;
		font-weight: var(--heavy);
	}
	.log {
		flex: 1;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 8px;
	}
	.bubble {
		border-radius: var(--radius);
		padding: 8px 12px;
		max-width: 90%;
		margin: 0;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 4px;
	}
	.bubble p {
		margin: 0;
		white-space: pre-wrap;
	}
	.assistant {
		background: var(--paper-deep);
	}
	.user {
		background: var(--primary);
		color: var(--on-primary);
		align-self: flex-end;
	}
	.failed {
		background: var(--card);
		border: 2px dashed var(--ink-soft);
		color: var(--ink-soft);
	}
	.toolchip {
		font-size: 11px;
		font-weight: var(--heavy);
		background: var(--chip);
		border: 1.5px solid var(--ink);
		border-radius: var(--pill);
		padding: 0 8px;
	}
	.toolchip.bad {
		background: var(--must);
	}
	.btn.small {
		font-size: 12px;
		padding: 2px 8px;
	}
	.small {
		font-size: 12px;
	}
	.notice {
		margin: 0;
		font-size: 13px;
	}
	.tab {
		position: fixed;
		right: 0;
		top: 40%;
		writing-mode: vertical-rl;
		padding: 14px 6px;
		border-radius: var(--radius) 0 0 var(--radius);
		font-weight: var(--heavy);
		background: var(--primary);
		color: var(--on-primary);
	}
	.dot {
		display: inline-block;
		width: 10px;
		height: 10px;
		margin-top: 6px;
		border-radius: 50%;
		background: var(--now);
		border: 2px solid var(--ink);
	}

	/* Narrow screens: the dock is a bottom sheet, and the tab is a bar along the bottom. */
	@media (max-width: 800px) {
		.dock {
			position: fixed;
			left: 0;
			right: 0;
			bottom: 0;
			top: auto;
			width: auto;
			height: 65vh;
			border-radius: var(--radius) var(--radius) 0 0;
			z-index: 20;
		}
		.tab {
			top: auto;
			bottom: 0;
			left: 0;
			right: 0;
			writing-mode: horizontal-tb;
			border-radius: var(--radius) var(--radius) 0 0;
		}
	}
</style>
```

- [ ] **Step 2: Restore `Toasts` in `web/src/routes/+layout.svelte`** to the version shown in Task 3.

- [ ] **Step 3: Type-check**

Run: `cd web && npm run check`
Expected: `0 ERRORS 0 WARNINGS`.

- [ ] **Step 4: Commit**
```bash
git add web/src
git commit -m "Add the Today components"
```

---

### Task 7: The Today and settings pages

**Files:**
- Modify: `web/src/routes/+page.svelte`
- Create: `web/src/routes/settings/+page.svelte`

**Interfaces:**
- Consumes: the components from Task 6 and the state from Task 5.

- [ ] **Step 1: Write the pages**

`web/src/routes/+page.svelte`:
```svelte
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, configured, type Pick, type SettingsView, type Tier } from '#lib/api.ts';
	import { chat } from '#lib/chat.svelte.ts';
	import { doneOn, tiers, unplanned } from '#lib/plan.ts';
	import { Stream } from '#lib/stream.svelte.ts';
	import { survived } from '#lib/time.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import { today } from '#lib/today.svelte.ts';
	import Capture from '#lib/components/Capture.svelte';
	import ChatDock from '#lib/components/ChatDock.svelte';
	import DayStrip from '#lib/components/DayStrip.svelte';
	import DoneToday from '#lib/components/DoneToday.svelte';
	import Header from '#lib/components/Header.svelte';
	import StartDay from '#lib/components/StartDay.svelte';
	import TaskCard from '#lib/components/TaskCard.svelte';
	import WhatNow from '#lib/components/WhatNow.svelte';

	let now = $state(new Date());
	let view = $state<SettingsView | null>(null);
	let pick = $state<Pick | null>(null);
	let asking = $state(false);
	/** What now found nothing open and eligible. */
	let nothing = $state(false);

	const stream = new Stream(
		{
			'batch.applied': (d) => {
				today.changed();
				toasts.applied(d);
			},
			'batch.undone': (d) => {
				today.changed();
				toasts.undone(d.batch_id);
				chat.markUndone(d.batch_id);
			},
			'plan.updated': () => today.changed(),
			'chat.delta': (d) => chat.delta(d.text),
			'chat.tool': (d) => chat.tool(d),
			'chat.message': (d) => chat.message(d)
		},
		() => {
			today.changed();
			chat.load().catch((err) => toasts.error(err));
		}
	);

	onMount(() => {
		Promise.all([today.refresh(), chat.load(), api.settings().then((v) => (view = v))]).catch((err) =>
			toasts.error(err)
		);
		stream.open();
		const clock = setInterval(() => (now = new Date()), 60_000);
		return () => {
			clearInterval(clock);
			stream.close();
		};
	});

	const t = $derived(today.today);
	const planned = $derived(t ? tiers(t, today.tasks) : null);
	const tray = $derived(t ? unplanned(t, today.tasks) : []);
	const done = $derived(t ? doneOn(t.date, today.tasks) : []);
	const endOfDay = $derived(
		t ? new Date(`${t.date}T${view?.settings.end_of_day ?? '17:30'}:00`) : new Date()
	);
	const pickTask = $derived(pick ? today.tasks.find((x) => x.id === pick?.task_id) : undefined);
	const tierNames: Tier[] = ['must', 'should', 'could'];

	async function whatNow(exclude = 0, reason = '') {
		asking = true;
		nothing = false;
		try {
			pick = await api.whatNow(exclude, reason);
		} catch (err) {
			pick = null;
			// 404 means nothing is open and eligible: news to say gently, not an error.
			if (err instanceof ApiError && err.status === 404) nothing = true;
			else toasts.error(err);
		} finally {
			asking = false;
		}
	}
</script>

<div class="page">
	<main>
		{#if t}
			<Header
				today={t}
				events={today.events}
				{now}
				connected={stream.connected}
				configured={view ? configured(view) : true}
				{asking}
				onwhatnow={() => whatNow()}
			/>
			<DayStrip events={today.events} {now} {endOfDay} />
			<Capture />

			{#if pick}
				<WhatNow
					{pick}
					task={pickTask}
					{asking}
					onnotthis={(reason) => whatNow(pick?.task_id, reason)}
					onclose={() => (pick = null)}
				/>
			{:else if nothing}
				<p class="sticker nothing">Nothing to do right now. Enjoy it.</p>
			{/if}

			{#if !t.plan}
				<StartDay />
			{:else if planned}
				{#if t.plan.fallback}
					<p class="chip rough">rough plan, model was out</p>
				{/if}
				{#each tierNames as tier (tier)}
					<section>
						<h2 class="label">{tier}</h2>
						{#each planned[tier] as card (card.task.id)}
							<TaskCard {card} {tier} date={t.date} />
						{:else}
							<p class="muted empty">Nothing here.</p>
						{/each}
					</section>
				{/each}
			{/if}

			<div class="lower">
				<section>
					<h2 class="label">Unplanned</h2>
					{#each tray as card (card.task.id)}
						<TaskCard {card} date={t.date} />
					{:else}
						<p class="muted empty">Tray's empty.</p>
					{/each}
				</section>
				<section>
					<DoneToday {done} meetings={survived(today.events, now)} />
				</section>
			</div>
		{:else}
			<p class="muted">Loading…</p>
		{/if}
	</main>
	<ChatDock opening={t?.plan?.opening ?? ''} />
</div>

<style>
	.page {
		display: flex;
		gap: 16px;
		padding: 16px;
		align-items: flex-start;
	}
	main {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
	h2 {
		margin: 6px 0 8px;
	}
	.empty {
		margin: 0 0 8px;
		font-size: 14px;
	}
	.nothing {
		margin: 0;
		padding: 12px 16px;
		font-weight: var(--heavy);
	}
	.rough {
		align-self: flex-start;
		margin: 0;
	}
	.lower {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 16px;
		align-items: start;
	}

	@media (max-width: 800px) {
		.page {
			padding: 12px 12px 72px;
		}
		.lower {
			grid-template-columns: 1fr;
		}
	}
</style>
```

`web/src/routes/settings/+page.svelte`:
```svelte
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type ConnectionResult, type Provider, type SettingsView } from '#lib/api.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	let view = $state<SettingsView | null>(null);
	let key = $state('');
	let saving = $state(false);
	let testing = $state(false);
	let result = $state<ConnectionResult | null>(null);

	// Before any provider is saved, the form starts on the default one.
	onMount(() => {
		api.settings().then(
			(v) => {
				v.settings.provider ||= 'anthropic';
				view = v;
			},
			(err) => toasts.error(err)
		);
	});

	const provider = $derived((view?.settings.provider || 'anthropic') as Provider);
	const keyNote = $derived(
		view?.keys[provider] === 'env'
			? 'from env'
			: view?.keys[provider] === 'keychain'
				? 'saved'
				: 'not set'
	);

	// PUT takes the whole settings object, so this sends back everything GET returned.
	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!view) return;
		saving = true;
		result = null;
		try {
			if (key.trim()) {
				await api.saveKey(provider, key.trim());
				key = '';
			}
			view = await api.saveSettings(view.settings);
		} catch (err) {
			toasts.error(err);
		} finally {
			saving = false;
		}
	}

	async function test() {
		testing = true;
		try {
			result = await api.testConnection();
		} catch (err) {
			toasts.error(err);
		} finally {
			testing = false;
		}
	}
</script>

<main>
	<a href="/">← Today</a>
	<h1>Settings</h1>
	{#if view}
		<form class="sticker" onsubmit={save}>
			<label>
				<span class="label">Provider</span>
				<select class="field" bind:value={view.settings.provider}>
					<option value="anthropic">Anthropic</option>
					<option value="openai">OpenAI-compatible</option>
				</select>
			</label>
			<label>
				<span class="label">API key <span class="muted">({keyNote})</span></span>
				<input class="field" type="password" bind:value={key} autocomplete="off" placeholder="Leave blank to keep" />
			</label>
			<label>
				<span class="label">Main model</span>
				<input class="field" bind:value={view.settings[provider].main_model} />
			</label>
			<label>
				<span class="label">Light model</span>
				<input class="field" bind:value={view.settings[provider].light_model} />
			</label>
			{#if provider === 'openai'}
				<label>
					<span class="label">Base URL</span>
					<input class="field" bind:value={view.settings.openai.base_url} placeholder="https://api.openai.com/v1" />
				</label>
			{/if}
			<div class="actions">
				<button class="btn primary" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
				<button type="button" class="btn" disabled={testing} onclick={test}>
					{testing ? 'Testing…' : 'Test connection'}
				</button>
				{#if result}
					<span class:ok={result.ok} class:bad={!result.ok}>
						{result.ok ? 'Connected' : result.error}
					</span>
				{/if}
			</div>
		</form>
	{:else}
		<p class="muted">Loading…</p>
	{/if}
</main>

<style>
	main {
		max-width: 560px;
		margin: 0 auto;
		padding: 16px;
	}
	h1 {
		font-weight: var(--heavy);
	}
	form {
		display: flex;
		flex-direction: column;
		gap: 14px;
		padding: 20px;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.actions {
		display: flex;
		align-items: center;
		gap: 10px;
		flex-wrap: wrap;
	}
	.ok {
		font-weight: var(--heavy);
	}
	.bad {
		color: var(--danger);
		font-weight: var(--heavy);
	}
</style>
```

- [ ] **Step 2: Type-check and test**

Run: `cd web && npm run check && npm test`
Expected: `0 ERRORS`, and all tests pass.

- [ ] **Step 3: Commit**
```bash
git add web/src/routes
git commit -m "Add the Today and settings pages"
```

---

### Task 8: Smoke test the real binary

There is nothing to commit unless a step finds a bug. If one does, fix it with a test where the fix lives in a logic module.

- [ ] **Step 1: Build and run against a throwaway database.** Use another port, so a running `whatnow` on 7777 is untouched. The sandbox blocks localhost, so the curl commands need it disabled.
```bash
just build
./whatnow serve -addr 127.0.0.1:7788 -db "$TMPDIR/whatnow-smoke.db"
```

- [ ] **Step 2: Seed data.** Send each request with `-H 'Content-Type: application/json'`, and `D=$(date +%F)`:
  - `POST /api/v1/tasks` with:
    - `{"title":"Send Q3 numbers to Priya","size":"S","energy":"low","feeling":"dreading this a little","due_on":"$D"}`
    - `{"title":"Review FLEX1-666 PR","size":"M"}`
    - `{"title":"Clean out the garage","size":"L"}`
    - `{"title":"Research pubsub","size":"M"}`
  - `POST /api/v1/tasks/2/blockers` with `{"blocked_by_id":4}`.
  - `POST /api/v1/events` three times, for 09:30–09:45, 13:00–13:30, and 15:00 with no end. Use your local UTC offset in `starts_at`.
  - `POST /api/v1/today/start` with `{"pace":3,"mood_note":"","triage":[]}`.

- [ ] **Step 3: Check each behavior at http://127.0.0.1:7788** (desktop width, then about 390px):
  - The header shows the date, the pace chip, "<event> in Nm", and the no-model notice.
  - The strip shows the three events, each label in full, and a red now line.
  - The rough-plan tag shows. Must holds Q3 with its badges and feeling. FLEX1-666 shows ⛓.
  - Tick Q3: a "Done:" toast appears, the card leaves, and the win line reads "✓ 1 thing done". **Undo** brings it back and the toast goes.
  - ⋯ → Push to tomorrow on the garage: the card leaves its tier.
  - Capture "book flights": the box clears and a dashed card appears in Unplanned.
  - **What now?** shows a card. **Not this one** with a reason asks again, and the reason shows in the first task's feeling.
  - Complete or push every open task, then **What now?** again: the "Nothing to do right now. Enjoy it." note appears, not a red toast.
  - Pick a pace: the chip changes and an undo toast shows.
  - Send a chat message: the user bubble appears, then a dashed failed reply with "Check settings".
  - Collapse the dock and reload: it stays collapsed as a right-edge tab. A new private window at 390px starts collapsed as a bottom bar.
  - Stop the server: "reconnecting…" shows. Restart it, and the pill clears.
  - Stop the server and capture something: a red toast, and the text is back in the box.
  - Settings: the provider select shows Anthropic, Save keeps the models, and Test connection shows "no LLM provider is set up". **Don't enter a key.**

- [ ] **Step 4: Run every check**

Run: `gofmt -l . && just lint && just test`
Expected: no gofmt output, `0 ERRORS`, every Go package `ok`, and every Vitest test passing.
