# whatnow web UI part 3 (5c) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Full settings, delete everything, first-run onboarding, nudges, and a day strip that tells time.

**Architecture:** Go gains three settings fields and one endpoint (`DELETE /api/v1/data`, built on `store.Wipe`). The rest is client-side. Pure modules (`nudges.ts`, `welcome.ts`, and `strip()` in `time.ts`) hold the rules and carry the unit tests. `live` fires nudges on its minute tick. Svelte components stay thin.

**Tech Stack:** Go 1.x stdlib + `modernc.org/sqlite` + `go-keyring`; SvelteKit 3 / Svelte 5 runes, TypeScript strict, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-06-whatnow-web-ui-settings-design.md`

All code below was compiled, tested, and smoke-tested against the real binary in a scratch worktree before handoff. Diffs are against `main` at the spec commit.

## Global Constraints

- Settings JSON: `nudges: {before_event, event_end, late_day}` (default all `true`), `alert_lead_minutes` (default `10`, range 1 to 60), `onboarded` (default `false`).
- Delete everything wipes every table except `schema_migrations`, removes both keychain keys, publishes `data.reset`, and answers `204`.
- Confirm phrase: `delete everything`.
- Late-day nudge fires at end of day minus 2 hours. In-page nudges last 30 seconds; undo toasts stay at 8.
- Onboarding redirect: `!onboarded && provider === ''`, never on `/welcome` or `/settings`.
- Shared lib modules import siblings as `./x`; components and routes use `#lib/x.ts`. Components use tokens from `tokens.css`, never raw colors.
- Components get no unit tests (5d's Playwright covers them). Logic goes in `.ts` modules with Vitest.
- `just test` and `just lint` must pass at every commit.

## Review Focus

- **Settings fail to load on a day with no plan:** Today now waits for settings before redirecting to `/morning`, so it holds on "Loading…" with the error toast. A reload retries. Accepted, since every other call fails too when the server is down.
- **Blank alert-lead field:** the number input binds `null`. JSON `null` leaves the Go int at 0, Validate rejects it with the 1-to-60 message, and the page keeps the other edits (a failed save refetches only `keys`). Validate's zero case in Task 1 pins this.
- **Delete everything during a running cleanup or chat turn:** that turn's later writes fail or land in the empty database. Accepted for a single-user tool.
- **Two open tabs:** OS notifications share a `tag` and show once. In-page nudges show in each tab. That's expected.
- **An event moved after its nudge fired** gets a new key, so it can nudge again at the new time. Task 5's moved-event test pins the key change.

---

### Task 1: Settings fields for nudges, alert lead time, and onboarding

**Files:**
- Modify: `internal/settings/settings.go`
- Test: `internal/settings/settings_test.go`

**Interfaces:**
- Produces: `settings.Nudges{BeforeEvent, EventEnd, LateDay bool}`; `Settings.Nudges`, `Settings.AlertLeadMinutes int` (1 to `MaxAlertLead` = 60), `Settings.Onboarded bool`. JSON: `nudges.{before_event,event_end,late_day}`, `alert_lead_minutes`, `onboarded`. Defaults: all nudges on, 10 minutes, not onboarded.

- [ ] **Step 1: Write the failing tests**

Modify `internal/settings/settings_test.go`:

```diff
@@ -46,6 +46,31 @@ func TestLoadDefaultsThenSaveRoundTrip(t *testing.T) {
 	}
 }
 
+func TestLoadFillsFieldsAddedSinceTheSave(t *testing.T) {
+	ctx := context.Background()
+	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer db.Close()
+	// Saved before nudges, the alert lead time, and onboarding existed.
+	_, err = db.Exec(`INSERT INTO settings (id, body) VALUES (1, '{"provider":"anthropic","end_of_day":"18:00"}')`)
+	if err != nil {
+		t.Fatal(err)
+	}
+
+	got, err := settings.Load(ctx, db)
+	if err != nil {
+		t.Fatal(err)
+	}
+	want := settings.Defaults()
+	want.Provider = settings.Anthropic
+	want.EndOfDay = "18:00"
+	if diff := cmp.Diff(want, got); diff != "" {
+		t.Errorf("old settings didn't pick up the new defaults (-want +got):\n%s", diff)
+	}
+}
+
 func TestValidate(t *testing.T) {
 	valid := settings.Defaults()
 	valid.Provider = settings.Anthropic
@@ -56,6 +81,8 @@ func TestValidate(t *testing.T) {
 		"style over 500 characters":     func(s *settings.Settings) { s.Style = strings.Repeat("é", 501) },
 		"about me over 1000 characters": func(s *settings.Settings) { s.AboutMe = strings.Repeat("🧠", 1001) },
 		"end of day not HH:MM":          func(s *settings.Settings) { s.EndOfDay = "5pm" },
+		"no alert lead time":            func(s *settings.Settings) { s.AlertLeadMinutes = 0 },
+		"alert lead over an hour":       func(s *settings.Settings) { s.AlertLeadMinutes = 61 },
 	}
 	for name, mutate := range cases {
 		t.Run(name, func(t *testing.T) {
@@ -70,6 +97,7 @@ func TestValidate(t *testing.T) {
 	atLimit := valid
 	atLimit.Style = strings.Repeat("é", 500) // 1000 bytes, 500 characters
 	atLimit.OpenAI.BaseURL = "http://localhost:11434/v1"
+	atLimit.AlertLeadMinutes = 60
 	if err := atLimit.Validate(); err != nil {
 		t.Errorf("settings at the limits rejected: %v", err)
 	}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/settings/`
Expected: compile errors (no `AlertLeadMinutes`, no `Nudges`).

- [ ] **Step 3: Implement**

Modify `internal/settings/settings.go`:

```diff
@@ -41,19 +41,35 @@ type Settings struct {
 	AboutMe   string         `json:"about_me"`
 	// EndOfDay is when the working day ends, as local HH:MM. Free time is counted up to it.
 	EndOfDay string `json:"end_of_day"`
+	Nudges   Nudges `json:"nudges"`
+	// AlertLeadMinutes is how long before an event its nudge comes.
+	AlertLeadMinutes int `json:"alert_lead_minutes"`
+	// Onboarded is set once first-run setup is finished or skipped.
+	Onboarded bool `json:"onboarded"`
+}
+
+// Nudges turns each kind of nudge on or off. The web client sends them; the server only keeps
+// the choice.
+type Nudges struct {
+	BeforeEvent bool `json:"before_event"`
+	EventEnd    bool `json:"event_end"`
+	LateDay     bool `json:"late_day"`
 }
 
 const (
 	MaxStyleRunes   = 500
 	MaxAboutMeRunes = 1000
+	MaxAlertLead    = 60
 )
 
 // Defaults has Claude models filled in. OpenAI-compatible models are left for the user, since
 // they depend on the server.
 func Defaults() Settings {
 	return Settings{
-		Anthropic: ProviderConfig{MainModel: "claude-sonnet-5-5", LightModel: "claude-haiku-4-5"},
-		EndOfDay:  "17:30",
+		Anthropic:        ProviderConfig{MainModel: "claude-sonnet-5-5", LightModel: "claude-haiku-4-5"},
+		EndOfDay:         "17:30",
+		Nudges:           Nudges{BeforeEvent: true, EventEnd: true, LateDay: true},
+		AlertLeadMinutes: 10,
 	}
 }
 
@@ -92,6 +108,9 @@ func (s Settings) Validate() error {
 	if _, err := time.Parse(ClockLayout, s.EndOfDay); err != nil {
 		return fmt.Errorf("%w: end of day %q must be HH:MM", ErrInvalid, s.EndOfDay)
 	}
+	if s.AlertLeadMinutes < 1 || s.AlertLeadMinutes > MaxAlertLead {
+		return fmt.Errorf("%w: alert lead time is %d minutes, it must be 1 to %d", ErrInvalid, s.AlertLeadMinutes, MaxAlertLead)
+	}
 	return nil
 }
```

- [ ] **Step 4: Run the checks**

Run: `gofmt -l internal && go vet ./... && go test ./...`
Expected: all pass, no type errors.

- [ ] **Step 5: Commit**

```bash
git add -A internal/settings/settings.go internal/settings/settings_test.go
git commit -m "Add nudge, alert lead time, and onboarded settings"
```

### Task 2: Delete everything: `store.Wipe` and `DELETE /api/v1/data`

**Files:**
- Create: `internal/store/wipe.go`
- Test: `internal/store/wipe_test.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/settings.go`
- Test: `internal/httpapi/settings_test.go`

**Interfaces:**
- Consumes: `settings.Defaults()` from Task 1 (the test compares against it, including `Onboarded`).
- Produces: `store.Wipe(ctx, db) error`; `DELETE /api/v1/data` answering `204`; the stream message `data.reset` with `null` data.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/wipe_test.go`:

```go
package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jameynakama/whatnow/internal/store"
)

func TestWipeEmptiesEveryTableButKeepsTheSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "w.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// A blocker references tasks, so the wipe has to get past foreign keys in either order.
	for _, q := range []string{
		`INSERT INTO tasks (id, title, raw_text, created_by, created_at, updated_at) VALUES (1, 'a', 'a', 'user', 'x', 'x'), (2, 'b', 'b', 'user', 'x', 'x')`,
		`INSERT INTO blockers (task_id, blocked_by_id) VALUES (1, 2)`,
		`INSERT INTO batches (actor, created_at) VALUES ('user', 'x')`,
		`INSERT INTO settings (id, body) VALUES (1, '{}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	if err := store.Wipe(ctx, db); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	for _, table := range tables {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM "` + table + `"`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if table == "schema_migrations" && n == 0 {
			t.Error("the migration record was wiped, so the next open would re-run every migration")
		}
		if table != "schema_migrations" && n != 0 {
			t.Errorf("%s still has %d rows", table, n)
		}
	}
	db.Close()

	db, err = store.Open(path)
	if err != nil {
		t.Fatalf("reopen after wipe: %v", err)
	}
	db.Close()
}
```

Modify `internal/httpapi/settings_test.go`:

```diff
@@ -2,11 +2,13 @@ package httpapi_test
 
 import (
 	"context"
+	"encoding/json"
 	"errors"
 	"net/http"
 	"strings"
 	"testing"
 
+	"github.com/google/go-cmp/cmp"
 	"github.com/zalando/go-keyring"
 
 	"github.com/jameynakama/whatnow/internal/httpapi"
@@ -92,3 +94,40 @@ func TestConnectionTest(t *testing.T) {
 		t.Errorf("failing provider: %+v", got)
 	}
 }
+
+func TestDeleteEverything(t *testing.T) {
+	keyring.MockInit()
+	t.Setenv("ANTHROPIC_API_KEY", "")
+	t.Setenv("OPENAI_API_KEY", "")
+	api := newAPI(t)
+	wantStatus(t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "buy oat milk"}), http.StatusCreated)
+	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/anthropic", map[string]string{"key": "sk-ant"}), http.StatusNoContent)
+	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/openai", map[string]string{"key": "sk-oai"}), http.StatusNoContent)
+	set := settings.Defaults()
+	set.Provider = settings.Anthropic
+	set.Onboarded = true
+	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings", set), http.StatusOK)
+	msgs, unsub := api.hub.Subscribe()
+	defer unsub()
+
+	wantStatus(t, api.do(http.MethodDelete, "/api/v1/data", nil), http.StatusNoContent)
+
+	if tasks := decodeAs[[]json.RawMessage](t, api.do(http.MethodGet, "/api/v1/tasks", nil)); len(tasks) != 0 {
+		t.Errorf("%d tasks left", len(tasks))
+	}
+	view := decodeAs[httpapi.SettingsView](t, api.do(http.MethodGet, "/api/v1/settings", nil))
+	if diff := cmp.Diff(settings.Defaults(), view.Settings); diff != "" {
+		t.Errorf("settings aren't back to the defaults (-want +got):\n%s", diff)
+	}
+	if view.Keys[settings.Anthropic] != "" || view.Keys[settings.OpenAI] != "" {
+		t.Errorf("keys left: %+v", view.Keys)
+	}
+	select {
+	case m := <-msgs:
+		if m.Type != "data.reset" {
+			t.Errorf("published %q, want data.reset", m.Type)
+		}
+	default:
+		t.Error("data.reset wasn't published")
+	}
+}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/store/ ./internal/httpapi/`
Expected: compile error (`store.Wipe` undefined), then `TestDeleteEverything` fails with 405 once it compiles.

- [ ] **Step 3: Implement**

Create `internal/store/wipe.go`:

```go
package store

import (
	"context"
	"database/sql"
)

// Wipe deletes every row of every table except the migration record, leaving the schema. It reads
// the table list from the schema, so a table added later is wiped too.
func Wipe(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Tables empty in no particular order, so foreign keys are checked once, at commit.
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range tables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM "`+name+`"`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
```

Modify `internal/httpapi/server.go`:

```diff
@@ -84,6 +84,7 @@ func New(d Deps) http.Handler {
 	mux.HandleFunc("PUT /api/v1/settings/keys/{provider}", s.putKey)
 	mux.HandleFunc("DELETE /api/v1/settings/keys/{provider}", s.deleteKey)
 	mux.HandleFunc("POST /api/v1/settings/test", s.testConnection)
+	mux.HandleFunc("DELETE /api/v1/data", s.deleteData)
 	mux.Handle("GET /", d.Static)
 	return localOnly(mux)
 }
```

Modify `internal/httpapi/settings.go`:

```diff
@@ -5,6 +5,8 @@ import (
 
 	"github.com/jameynakama/whatnow/internal/llm"
 	"github.com/jameynakama/whatnow/internal/settings"
+	"github.com/jameynakama/whatnow/internal/store"
+	"github.com/jameynakama/whatnow/internal/stream"
 )
 
 // SettingsView is what the settings screen gets: the settings, and where each provider's key
@@ -100,3 +102,20 @@ func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
 	}
 	writeJSON(w, http.StatusOK, ConnectionResult{OK: true})
 }
+
+// deleteData wipes every row and both saved keys, then tells every open tab to start over.
+// Environment keys can't be removed and are found again on the next setup.
+func (s *Server) deleteData(w http.ResponseWriter, r *http.Request) {
+	if err := store.Wipe(r.Context(), s.DB); err != nil {
+		writeError(w, err)
+		return
+	}
+	for _, p := range []settings.Provider{settings.Anthropic, settings.OpenAI} {
+		if err := settings.DeleteKey(p); err != nil {
+			writeError(w, err)
+			return
+		}
+	}
+	s.Hub.Publish(stream.Message{Type: "data.reset"})
+	w.WriteHeader(http.StatusNoContent)
+}
```

`PRAGMA defer_foreign_keys = ON` is load-bearing: without it, deleting `tasks` before `blockers` fails with `FOREIGN KEY constraint failed (787)`, and the store test catches that.

- [ ] **Step 4: Run the checks**

Run: `gofmt -l internal && go vet ./... && go test ./...`
Expected: all pass, no type errors.

- [ ] **Step 5: Commit**

```bash
git add -A internal/httpapi/server.go internal/httpapi/settings.go internal/httpapi/settings_test.go internal/store/wipe.go internal/store/wipe_test.go
git commit -m "Add delete everything"
```

### Task 3: Client types for the new fields and `data.reset`

**Files:**
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/lib/stream.svelte.ts`
- Modify: `web/src/lib/live.svelte.ts`
- Modify: `web/src/lib/api.test.ts`
- Modify: `web/src/lib/plan.test.ts`
- Modify: `web/src/lib/rituals.test.ts`
- Modify: `web/src/lib/triage.test.ts`
- Modify: `web/src/lib/stream.test.ts`
- Modify: `web/src/lib/time.test.ts`

**Interfaces:**
- Produces: `Settings.nudges: NudgeToggles`, `Settings.alert_lead_minutes`, `Settings.onboarded`; `Day.started_at`; `CalendarEvent.created_at` (the server already sends both); `api.deleteData()`; `Handlers['data.reset']`.
- The test files change only to add the new required fields to their fixtures. No assertion changes.

- [ ] **Step 1: Implement**

Modify `web/src/lib/api.ts`:

```diff
@@ -55,11 +55,13 @@ export interface CalendarEvent {
 	title: string;
 	starts_at: string;
 	ends_at: string | null;
+	created_at: string;
 }
 
 export interface Day {
 	date: string;
 	pace: number;
+	started_at: string;
 	wrapped_at: string | null;
 	tomorrow_note: string;
 	summary: string;
@@ -160,6 +162,15 @@ export interface Settings {
 	style: string;
 	about_me: string;
 	end_of_day: string;
+	nudges: NudgeToggles;
+	alert_lead_minutes: number;
+	onboarded: boolean;
+}
+
+export interface NudgeToggles {
+	before_event: boolean;
+	event_end: boolean;
+	late_day: boolean;
 }
 
 export interface SettingsView {
@@ -226,7 +237,8 @@ export const api = {
 	saveSettings: (s: Settings) => request<SettingsView>('PUT', '/settings', s),
 	saveKey: (provider: Provider, key: string) =>
 		request<void>('PUT', `/settings/keys/${provider}`, { key }),
-	testConnection: () => request<ConnectionResult>('POST', '/settings/test')
+	testConnection: () => request<ConnectionResult>('POST', '/settings/test'),
+	deleteData: () => request<void>('DELETE', '/data')
 };
 
 /** Whether a model is set up. Without one, capture still works and plans come from code. */
```

Modify `web/src/lib/stream.svelte.ts`:

```diff
@@ -7,6 +7,7 @@ export interface Handlers {
 	'chat.delta': (d: { text: string }) => void;
 	'chat.tool': (d: ToolUse) => void;
 	'chat.message': (d: Message) => void;
+	'data.reset': () => void;
 }
 
 /** The parts of EventSource the stream uses, so tests can drive it. */
```

Modify `web/src/lib/api.test.ts`:

```diff
@@ -34,7 +34,10 @@ describe('configured', () => {
 			openai: { main_model: '', light_model: '', base_url: '' },
 			style: '',
 			about_me: '',
-			end_of_day: '17:30'
+			end_of_day: '17:30',
+			nudges: { before_event: true, event_end: true, late_day: true },
+			alert_lead_minutes: 10,
+			onboarded: true
 		},
 		keys: { anthropic: key, openai: '' }
 	});
```

Modify `web/src/lib/plan.test.ts`:

```diff
@@ -20,7 +20,7 @@ const task = (id: number, status: Status = 'open', updated_at = '2026-10-05T15:0
 
 const today = (over: Partial<Today> = {}): Today => ({
 	date: '2026-10-05',
-	day: { date: '2026-10-05', pace: 3, wrapped_at: null, tomorrow_note: '', summary: '' },
+	day: { date: '2026-10-05', pace: 3, started_at: '', wrapped_at: null, tomorrow_note: '', summary: '' },
 	plan: {
 		id: 1,
 		opening: '',
```

Modify `web/src/lib/rituals.test.ts`:

```diff
@@ -2,7 +2,7 @@ import { describe, expect, it } from 'vitest';
 import type { Today } from './api';
 import { ritualOver } from './rituals';
 
-const day = { date: '2026-10-06', pace: 3, wrapped_at: null, tomorrow_note: '', summary: '' };
+const day = { date: '2026-10-06', pace: 3, started_at: '', wrapped_at: null, tomorrow_note: '', summary: '' };
 const plan = { id: 1, opening: '', entries: [], fallback: false };
 const today = (over: Partial<Today>): Today => ({
 	date: '2026-10-06',
```

Modify `web/src/lib/triage.test.ts`:

```diff
@@ -19,7 +19,7 @@ const task = (id: number): Task => ({
 });
 
 const review = (ids: number[], over: Partial<Review> = {}): Review => ({
-	day: { date: '2026-10-05', pace: 3, wrapped_at: null, tomorrow_note: '', summary: '' },
+	day: { date: '2026-10-05', pace: 3, started_at: '', wrapped_at: null, tomorrow_note: '', summary: '' },
 	done: [],
 	unfinished: ids.map(task),
 	suggestions: [
```

Modify `web/src/lib/stream.test.ts`:

```diff
@@ -19,7 +19,8 @@ function handlers(): Handlers {
 		'plan.updated': vi.fn(),
 		'chat.delta': vi.fn(),
 		'chat.tool': vi.fn(),
-		'chat.message': vi.fn()
+		'chat.message': vi.fn(),
+		'data.reset': vi.fn()
 	};
 }
```

This step leaves `live.svelte.ts` failing the type check: `Handlers` now needs `data.reset`. Task 6 adds it. To keep this commit green on its own, add the handler now as `'data.reset': () => location.assign('/')` after `'chat.message'` in `live.svelte.ts`, with the comment shown in Task 6. Task 6's diff then shows it already present. Likewise add `created_at: at('08:00').toISOString()` to the `ev` fixture in `time.test.ts`; Task 4's diff shows it already present.

- [ ] **Step 2: Run the checks**

Run: `cd web && npm run check && npm test`
Expected: all pass, no type errors.

- [ ] **Step 3: Commit**

```bash
git add -A web/src/lib/api.test.ts web/src/lib/api.ts web/src/lib/live.svelte.ts web/src/lib/plan.test.ts web/src/lib/rituals.test.ts web/src/lib/stream.svelte.ts web/src/lib/stream.test.ts web/src/lib/time.test.ts web/src/lib/triage.test.ts
git commit -m "Add the client types for nudges, onboarding, and delete everything"
```

### Task 4: A day strip that tells time

**Files:**
- Modify: `web/src/lib/time.ts`
- Test: `web/src/lib/time.test.ts`
- Modify: `web/src/lib/components/DayStrip.svelte`
- Modify: `web/src/routes/(app)/+page.svelte`

**Interfaces:**
- Consumes: `Day.started_at` from Task 3.
- Produces: `strip(events, now, endOfDay, startedAt: string | null): Strip`, with `Strip.ticks: StripTick[]` (`{left, label}`) and `Strip.endOfDay: number` (percent). `DayStrip` takes a `startedAt` prop.
- The `(app)/+page.svelte` change for this task is only the `<DayStrip … startedAt=…>` line; its other hunks belong to Tasks 6 and 8.

- [ ] **Step 1: Write the failing tests**

Modify `web/src/lib/time.test.ts`:

```diff
@@ -7,7 +7,8 @@ const ev = (title: string, start: string, end: string | null = null): CalendarEv
 	id: 0,
 	title,
 	starts_at: at(start).toISOString(),
-	ends_at: end ? at(end).toISOString() : null
+	ends_at: end ? at(end).toISOString() : null,
+	created_at: at('08:00').toISOString()
 });
 
 describe('countdown', () => {
@@ -40,31 +41,54 @@ describe('countdown', () => {
 
 describe('strip', () => {
 	const eod = at('17:30');
+	const labels = (s: ReturnType<typeof strip>) => s.ticks.map((t) => t.label);
 
-	it('Should run from now to the end of day with no events', () => {
-		const s = strip([], at('09:30'), eod);
-		expect(s.items).toEqual([]);
-		expect(s.now).toBe(0);
+	it('Should start at the day start, so the now line moves across as the day goes on', () => {
+		const early = strip([], at('09:00'), eod, at('08:40').toISOString());
+		const later = strip([], at('13:00'), eod, at('08:40').toISOString());
+		expect(early.now).toBeGreaterThan(0);
+		expect(later.now).toBeGreaterThan(early.now);
 	});
 
-	it('Should start at the first event when it is earlier than now', () => {
-		const s = strip([ev('Standup', '09:00', '09:30')], at('13:00'), at('17:00'));
-		expect(s.items[0].left).toBe(0);
-		expect(s.items[0].width).toBeCloseTo(6.25); // 30 of 480 minutes
-		expect(s.now).toBe(50);
+	it('Should round the window out to whole hours, from 8:00 to 18:00', () => {
+		const s = strip([], at('13:00'), eod, at('08:40').toISOString());
+		expect(s.now).toBe(50); // 13:00 is halfway through 8:00 to 18:00
+		expect(s.endOfDay).toBe(95); // 17:30
+	});
+
+	it('Should start at the first event when it is earlier than the day start', () => {
+		const s = strip([ev('Standup', '07:30', '08:00')], at('13:00'), eod, at('08:40').toISOString());
+		expect(s.items[0].left).toBeCloseTo(4.55, 1); // 30 of 660 minutes, from 7:00
+	});
+
+	it('Should start at now with no start and no earlier event', () => {
+		const s = strip([], at('09:30'), eod, null);
+		expect(s.now).toBeCloseTo(5.56, 1); // 30 of 540 minutes, from 9:00 to 18:00
 	});
 
 	it('Should draw an event with no end as 30 minutes', () => {
-		const s = strip([ev('Focus', '13:00')], at('09:00'), at('17:00'));
-		expect(s.items[0].width).toBeCloseTo(6.25);
+		const s = strip([ev('Focus', '13:00')], at('09:00'), at('17:00'), at('09:00').toISOString());
+		expect(s.items[0].width).toBeCloseTo(5.56, 1); // 30 of 540 minutes
 	});
 
 	it('Should stretch past the end of day to keep now and late events on it', () => {
-		const late = strip([ev('Late call', '18:00', '19:00')], at('09:00'), at('17:00'));
-		expect(late.items[0].left + late.items[0].width).toBeCloseTo(100);
-		const after = strip([], at('20:00'), at('17:00'));
-		expect(after.now).toBe(0);
-		expect(Number.isFinite(after.now)).toBe(true);
+		const late = strip([ev('Late call', '18:00', '19:00')], at('09:00'), at('17:00'), at('09:00').toISOString());
+		expect(late.items[0].left + late.items[0].width).toBeLessThanOrEqual(100);
+		expect(late.endOfDay).toBeLessThan(late.items[0].left);
+		const after = strip([], at('20:00'), at('17:00'), at('09:00').toISOString());
+		expect(after.now).toBeLessThan(100);
+	});
+
+	it('Should tick every third clock hour on a long strip', () => {
+		expect(labels(strip([], at('09:00'), eod, at('08:40').toISOString()))).toEqual(['9a', '12p', '3p']);
+	});
+
+	it('Should tick every hour on a strip of 6 hours or less', () => {
+		expect(labels(strip([], at('13:10'), eod, at('13:05').toISOString()))).toEqual(['2p', '3p', '4p', '5p']);
+	});
+
+	it('Should label noon 12p', () => {
+		expect(labels(strip([], at('11:10'), at('14:00'), at('11:05').toISOString()))).toContain('12p');
 	});
 });
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/lib/time.test.ts`
Expected: the strip tests fail: `ticks` and `endOfDay` are undefined, and the window still starts at now.

- [ ] **Step 3: Implement**

Modify `web/src/lib/time.ts`:

```diff
@@ -45,27 +45,60 @@ export interface StripItem {
 	width: number;
 }
 
+export interface StripTick {
+	left: number;
+	/** Like "9a" or "12p". */
+	label: string;
+}
+
 export interface Strip {
 	items: StripItem[];
 	now: number;
+	ticks: StripTick[];
+	endOfDay: number;
+}
+
+const hour = 60 * minute;
+
+function floorHour(ms: number): number {
+	const d = new Date(ms);
+	d.setMinutes(0, 0, 0);
+	return d.getTime();
+}
+
+function hourLabel(h: number): string {
+	return `${h % 12 || 12}${h < 12 ? 'a' : 'p'}`;
 }
 
 /**
- * Places events on a strip from the earlier of now and the first event, to the end of day. The
- * strip stretches past the end of day to keep now and every event on it, and is at least an hour.
+ * Places events on a strip that holds still while the now line crosses it. It runs from the
+ * earliest of when the day started, the first event, and now, to the end of day, rounded out to
+ * whole hours. It stretches past the end of day to keep now and every event on it. A start
+ * anchored at now would pin the now line to the left edge.
+ *
+ * Ticks mark every third clock hour, or every hour on a strip of 6 hours or less.
  */
-export function strip(events: CalendarEvent[], now: Date, endOfDay: Date): Strip {
+export function strip(events: CalendarEvent[], now: Date, endOfDay: Date, startedAt: string | null): Strip {
 	const t = now.getTime();
-	const start = Math.min(t, ...events.map((e) => Date.parse(e.starts_at)));
-	const end = Math.max(endOfDay.getTime(), t, start + 60 * minute, ...events.map(eventEnd));
+	const starts = events.map((e) => Date.parse(e.starts_at));
+	const start = floorHour(Math.min(t, ...starts, ...(startedAt ? [Date.parse(startedAt)] : [])));
+	const end = floorHour(Math.max(endOfDay.getTime(), t, ...events.map(eventEnd))) + hour;
 	const pct = (ms: number) => ((ms - start) / (end - start)) * 100;
+	const step = end - start <= 6 * hour ? 1 : 3;
+	const ticks: StripTick[] = [];
+	for (let h = start + hour; h < end; h += hour) {
+		const clock = new Date(h).getHours();
+		if (clock % step === 0) ticks.push({ left: pct(h), label: hourLabel(clock) });
+	}
 	return {
 		items: events.map((event) => ({
 			event,
 			left: pct(Date.parse(event.starts_at)),
 			width: pct(eventEnd(event)) - pct(Date.parse(event.starts_at))
 		})),
-		now: pct(t)
+		now: pct(t),
+		ticks,
+		endOfDay: pct(endOfDay.getTime())
 	};
 }
```

Modify `web/src/lib/components/DayStrip.svelte`:

```diff
@@ -6,15 +6,21 @@
 		events: CalendarEvent[];
 		now: Date;
 		endOfDay: Date;
+		/** When the day was started, which anchors the strip's left edge. */
+		startedAt: string | null;
 	}
-	let { events, now, endOfDay }: Props = $props();
+	let { events, now, endOfDay, startedAt }: Props = $props();
 
-	const layout = $derived(strip(events, now, endOfDay));
-	const time = (iso: string) =>
-		new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
+	const layout = $derived(strip(events, now, endOfDay, startedAt));
+	const time = (d: Date | string) =>
+		new Date(d).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
 </script>
 
 <div class="sticker strip" aria-label="Today's events">
+	{#each layout.ticks as tick (tick.label)}
+		<div class="tick" style:left="{tick.left}%"><span>{tick.label}</span></div>
+	{/each}
+	<div class="eod" style:left="{layout.endOfDay}%"><span>{time(endOfDay)}</span></div>
 	{#each layout.items as item (item.event.id)}
 		<div
 			class="event"
@@ -22,6 +28,7 @@
 			style:width="{item.width}%"
 			title="{time(item.event.starts_at)} {item.event.title}"
 		>
+			<span class="at">{time(item.event.starts_at)}</span>
 			{item.event.title}
 		</div>
 	{/each}
@@ -34,23 +41,47 @@
 <style>
 	.strip {
 		position: relative;
-		height: 48px;
+		height: 62px;
 		overflow: hidden;
 	}
+	.tick,
+	.eod {
+		position: absolute;
+		top: 0;
+		bottom: 0;
+		border-left: 1px solid var(--paper-deep);
+	}
+	.eod {
+		border-left: 2px dashed var(--ink-soft);
+	}
+	.tick span,
+	.eod span {
+		position: absolute;
+		bottom: 2px;
+		left: 3px;
+		font-size: 10px;
+		font-weight: var(--heavy);
+		color: var(--ink-soft);
+		white-space: nowrap;
+	}
 	.event {
 		position: absolute;
-		top: 8px;
-		bottom: 8px;
+		top: 6px;
+		bottom: 20px;
 		background: var(--event);
 		border: var(--line);
 		border-radius: var(--radius-sm);
 		padding: 4px 6px;
-		/* A short event grows to fit its title rather than cutting it off. Hover gives the time. */
+		/* A short event grows to fit its title rather than cutting it off. */
 		min-width: max-content;
 		font-size: 12px;
 		font-weight: var(--heavy);
 		white-space: nowrap;
 	}
+	.at {
+		font-weight: normal;
+		margin-right: 4px;
+	}
 	.event:hover {
 		z-index: 2;
 	}
```

Then in `web/src/routes/(app)/+page.svelte`, pass the day's start to the strip:

```svelte
	<DayStrip events={today.events} now={live.now} endOfDay={live.endOfDay} startedAt={t.day?.started_at ?? null} />
```

- [ ] **Step 4: Run the checks**

Run: `cd web && npm run check && npm test`
Expected: all pass, no type errors.

- [ ] **Step 5: Commit**

```bash
git add -A web/src/lib/components/DayStrip.svelte web/src/lib/time.test.ts web/src/lib/time.ts web/src/routes/(app)/+page.svelte
git commit -m "Hold the day strip still and mark the hours"
```

### Task 5: Nudge logic and in-page nudge toasts

**Files:**
- Create: `web/src/lib/nudges.ts`
- Test: `web/src/lib/nudges.test.ts`
- Modify: `web/src/lib/toasts.svelte.ts`
- Test: `web/src/lib/toasts.test.ts`
- Modify: `web/src/lib/components/Toasts.svelte`

**Interfaces:**
- Consumes: the Task 3 types.
- Produces: `nudges(events, plan, tasks, settings, date, endOfDay): Nudge[]`, `due(list, from, to): Nudge[]`, `type Nudge = {key, at, kind, text}`, `type NudgeKind = 'before_event' | 'event_end' | 'late_day'`. `Toast.life` (ms) on every toast, plus `Toast.nudge` and `Toast.action {label, run}`. `toasts.nudge(text, action?)` lasts 30 seconds. The exported `lifetime` constant is gone; `Toasts.svelte` reads `toast.life`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/nudges.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import type { CalendarEvent, Plan, Settings, Task } from './api';
import { due, nudges, type Nudge } from './nudges';

const date = '2026-10-06';
const at = (hhmm: string, day = date) => new Date(`${day}T${hhmm}:00`);
const iso = (hhmm: string, day = date) => at(hhmm, day).toISOString();
const eod = at('17:30');

const settings = (over: Partial<Settings> = {}): Settings => ({
	provider: 'openai',
	anthropic: { main_model: '', light_model: '', base_url: '' },
	openai: { main_model: '', light_model: '', base_url: '' },
	style: '',
	about_me: '',
	end_of_day: '17:30',
	nudges: { before_event: true, event_end: true, late_day: true },
	alert_lead_minutes: 10,
	onboarded: true,
	...over
});

const ev = (id: number, title: string, start: string, end: string | null, created = '08:00'): CalendarEvent => ({
	id,
	title,
	starts_at: iso(start),
	ends_at: end ? iso(end) : null,
	created_at: iso(created)
});

const task = (id: number, over: Partial<Task> = {}): Task => ({
	id,
	title: `task ${id}`,
	notes: '',
	raw_text: '',
	size: null,
	energy: null,
	feeling: '',
	due_on: date,
	not_before: null,
	status: 'open',
	refs: null,
	created_at: iso('09:00', '2026-10-05'),
	updated_at: iso('09:00', '2026-10-05'),
	...over
});

const plan = (musts: number[]): Plan => ({
	id: 1,
	opening: '',
	fallback: false,
	entries: musts.map((task_id) => ({ task_id, tier: 'must', rationale: '' }))
});

const times = (list: Nudge[]) => list.map((n) => [n.kind, new Date(n.at).toTimeString().slice(0, 5)]);

describe('nudges', () => {
	it('Should nudge before an event and when it ends', () => {
		const list = nudges([ev(1, 'Standup', '09:30', '09:45')], null, [], settings(), date, eod);
		expect(times(list)).toEqual([
			['before_event', '09:20'],
			['event_end', '09:45']
		]);
		expect(list.map((n) => n.text)).toEqual([
			'Standup in 10 min, find a stopping point.',
			"Standup's done. What now?"
		]);
	});

	it('Should use the lead time from settings', () => {
		const list = nudges([ev(1, 'Standup', '09:30', null)], null, [], settings({ alert_lead_minutes: 25 }), date, eod);
		expect(times(list)).toEqual([['before_event', '09:05']]);
		expect(list[0].text).toBe('Standup in 25 min, find a stopping point.');
	});

	it('Should skip the before nudge for an event added inside its own lead time', () => {
		const list = nudges([ev(1, 'Call', '14:00', '14:30', '13:55')], null, [], settings(), date, eod);
		expect(times(list)).toEqual([['event_end', '14:30']]);
	});

	it('Should give an event with no end no end nudge', () => {
		const list = nudges([ev(1, 'Focus', '10:00', null)], null, [], settings(), date, eod);
		expect(list.map((n) => n.kind)).toEqual(['before_event']);
	});

	it('Should nudge two hours before the end of day about a must due today that nothing touched', () => {
		const list = nudges([], plan([1]), [task(1, { title: 'File taxes' })], settings(), date, eod);
		expect(times(list)).toEqual([['late_day', '15:30']]);
		expect(list[0].text).toBe("File taxes is due today and hasn't moved yet. Still time.");
		expect(list[0].key).toBe(`late_day:${date}`);
	});

	it.each([
		['the must was touched today', [task(1, { updated_at: iso('08:00') })], [1]],
		['the task is a should, not a must', [task(1)], []],
		['the must is due tomorrow', [task(1, { due_on: '2026-10-07' })], [1]],
		['the must is done', [task(1, { status: 'done' })], [1]]
	])('Should skip the late-day nudge when %s', (_, tasks, musts) => {
		expect(nudges([], plan(musts), tasks, settings(), date, eod)).toEqual([]);
	});

	it('Should leave out the kinds turned off in settings', () => {
		const off = settings({ nudges: { before_event: false, event_end: true, late_day: false } });
		const list = nudges([ev(1, 'Standup', '09:30', '09:45')], plan([1]), [task(1)], off, date, eod);
		expect(list.map((n) => n.kind)).toEqual(['event_end']);
	});

	it('Should key a moved event apart from where it was', () => {
		const before = nudges([ev(1, 'Standup', '09:30', '09:45')], null, [], settings(), date, eod);
		const after = nudges([ev(1, 'Standup', '10:00', '10:15')], null, [], settings(), date, eod);
		expect(before[0].key).not.toBe(after[0].key);
	});
});

describe('due', () => {
	const list = nudges([ev(1, 'Standup', '09:30', '09:45')], null, [], settings(), date, eod);
	const t = (hhmm: string) => at(hhmm).getTime();

	it('Should fire a nudge in the tick that passes it, and not again in the next', () => {
		expect(due(list, t('09:19'), t('09:20')).map((n) => n.kind)).toEqual(['before_event']);
		expect(due(list, t('09:20'), t('09:21'))).toEqual([]);
	});

	it('Should not fire nudges that came before the page loaded', () => {
		expect(due(list, t('09:50'), t('09:51'))).toEqual([]);
	});

	it('Should fire every nudge inside a long gap between throttled ticks', () => {
		expect(due(list, t('09:00'), t('10:00')).map((n) => n.kind)).toEqual(['before_event', 'event_end']);
	});
});
```

Modify `web/src/lib/toasts.test.ts`:

```diff
@@ -63,4 +63,14 @@ describe('Toasts', () => {
 		vi.advanceTimersByTime(1);
 		expect(t.items.length).toBe(0);
 	});
+
+	it('Should keep a nudge for 30 seconds, with its button', () => {
+		const t = new Toasts();
+		const run = vi.fn();
+		t.nudge("Standup's done. What now?", { label: 'What now?', run });
+		vi.advanceTimersByTime(29_999);
+		expect(t.items).toMatchObject([{ nudge: true, action: { label: 'What now?' } }]);
+		vi.advanceTimersByTime(1);
+		expect(t.items.length).toBe(0);
+	});
 });
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/lib/nudges.test.ts src/lib/toasts.test.ts`
Expected: fails to resolve `./nudges`, and `t.nudge` is not a function.

- [ ] **Step 3: Implement**

Create `web/src/lib/nudges.ts`:

```ts
import type { CalendarEvent, Plan, Settings, Task } from './api';

const minute = 60_000;

export type NudgeKind = 'before_event' | 'event_end' | 'late_day';

export interface Nudge {
	/** The same nudge always has the same key, so two tabs can share one notification. */
	key: string;
	/** When it's due, in ms since the epoch. */
	at: number;
	kind: NudgeKind;
	text: string;
}

/**
 * Today's nudges from its events, plan, and tasks, leaving out the kinds turned off in settings.
 * `date` is today's YYYY-MM-DD and `endOfDay` the moment the working day ends.
 */
export function nudges(
	events: CalendarEvent[],
	plan: Plan | null,
	tasks: Task[],
	settings: Settings,
	date: string,
	endOfDay: Date
): Nudge[] {
	const lead = settings.alert_lead_minutes;
	const out: Nudge[] = [];
	for (const e of events) {
		const start = Date.parse(e.starts_at);
		const before = start - lead * minute;
		// An event added inside its own lead time is news already; a nudge would just repeat it.
		if (before >= Date.parse(e.created_at)) {
			out.push({
				key: `before_event:${e.id}:${start}`,
				at: before,
				kind: 'before_event',
				text: `${e.title} in ${lead} min, find a stopping point.`
			});
		}
		if (e.ends_at) {
			const end = Date.parse(e.ends_at);
			out.push({ key: `event_end:${e.id}:${end}`, at: end, kind: 'event_end', text: `${e.title}'s done. What now?` });
		}
	}
	const stalled = stalledMust(plan, tasks, date);
	if (stalled) {
		out.push({
			key: `late_day:${date}`,
			at: endOfDay.getTime() - 120 * minute,
			kind: 'late_day',
			text: `${stalled.title} is due today and hasn't moved yet. Still time.`
		});
	}
	return out.filter((n) => settings.nudges[n.kind]);
}

// The first open must due today that nothing has touched today. Every edit, push, and tick moves
// updated_at, which stands in for "no activity today".
function stalledMust(plan: Plan | null, tasks: Task[], date: string): Task | undefined {
	const dayStart = new Date(`${date}T00:00:00`).getTime();
	const musts = new Set(plan?.entries.filter((e) => e.tier === 'must').map((e) => e.task_id));
	return tasks.find(
		(t) => musts.has(t.id) && t.status === 'open' && t.due_on === date && Date.parse(t.updated_at) < dayStart
	);
}

/**
 * The nudges due after `from` and by `to`. Each tick asks for the span since the last one, so a
 * nudge fires once, a reload never fires old ones, and a long gap in a throttled tab still fires.
 */
export function due(list: Nudge[], from: number, to: number): Nudge[] {
	return list.filter((n) => from < n.at && n.at <= to);
}
```

Modify `web/src/lib/toasts.svelte.ts`:

```diff
@@ -8,10 +8,16 @@ export interface Toast {
 	error?: boolean;
 	/** Set while hovered or focused: the countdown and its fill stand still. */
 	paused?: boolean;
+	/** How long it stays, in ms. Its fill sweeps across in the same time. */
+	life: number;
+	nudge?: boolean;
+	/** A nudge's button, such as What now? for an event that just ended. */
+	action?: { label: string; run: () => void };
 }
 
-/** How long a toast stays, in ms. Its fill sweeps across in the same time. */
-export const lifetime = 8000;
+const lifetime = 8000;
+/** A nudge stays longer: it arrives unasked, often while you're looking elsewhere. */
+const nudgeLifetime = 30_000;
 
 interface Timer {
 	handle: ReturnType<typeof setTimeout>;
@@ -24,10 +30,10 @@ export class Toasts {
 	#next = 1;
 	#timers = new Map<number, Timer>();
 
-	#add(toast: Omit<Toast, 'id'>): void {
+	#add(toast: Omit<Toast, 'id' | 'life'>, life = lifetime): void {
 		const id = this.#next++;
-		this.items.push({ id, ...toast });
-		this.#run(id, lifetime);
+		this.items.push({ id, life, ...toast });
+		this.#run(id, life);
 	}
 
 	#run(id: number, ms: number): void {
@@ -61,6 +67,11 @@ export class Toasts {
 		this.#add({ text: err instanceof Error ? err.message : String(err), error: true });
 	}
 
+	/** A nudge shown in the page, for when the browser won't show notifications. */
+	nudge(text: string, action?: Toast['action']): void {
+		this.#add({ text, nudge: true, action }, nudgeLifetime);
+	}
+
 	dismiss(id: number): void {
 		clearTimeout(this.#timers.get(id)?.handle);
 		this.#timers.delete(id);
```

Modify `web/src/lib/components/Toasts.svelte`:

```diff
@@ -1,5 +1,5 @@
 <script lang="ts">
-	import { lifetime, toasts } from '#lib/toasts.svelte.ts';
+	import { toasts } from '#lib/toasts.svelte.ts';
 </script>
 
 <div class="stack" aria-live="polite">
@@ -7,8 +7,9 @@
 		<div
 			class="sticker toast"
 			class:error={toast.error}
+			class:nudge={toast.nudge}
 			class:paused={toast.paused}
-			style:--lifetime="{lifetime}ms"
+			style:--lifetime="{toast.life}ms"
 			role="status"
 			onpointerenter={() => toasts.pause(toast.id)}
 			onpointerleave={() => toasts.resume(toast.id)}
@@ -17,6 +18,15 @@
 		>
 			<span class="fill" aria-hidden="true"></span>
 			<span class="text">{toast.text}</span>
+			{#if toast.action}
+				<button
+					class="btn primary"
+					onclick={() => {
+						toast.action?.run();
+						toasts.dismiss(toast.id);
+					}}>{toast.action.label}</button
+				>
+			{/if}
 			{#if toast.batch !== undefined}
 				<button class="btn" onclick={() => toasts.undo(toast)}>Undo</button>
 			{/if}
@@ -76,6 +86,9 @@
 	.error {
 		background: var(--must);
 	}
+	.nudge {
+		background: var(--chip);
+	}
 	.close {
 		background: none;
 		border: none;
```

- [ ] **Step 4: Run the checks**

Run: `cd web && npm run check && npm test`
Expected: all pass, no type errors.

- [ ] **Step 5: Commit**

```bash
git add -A web/src/lib/components/Toasts.svelte web/src/lib/nudges.test.ts web/src/lib/nudges.ts web/src/lib/toasts.svelte.ts web/src/lib/toasts.test.ts
git commit -m "Add nudge scheduling and in-page nudges"
```

### Task 6: Fire and deliver nudges, and open What now from one

**Files:**
- Modify: `web/src/lib/live.svelte.ts`
- Modify: `web/src/routes/(app)/+page.svelte`

**Interfaces:**
- Consumes: `nudges`, `due` (Task 5), `toasts.nudge` (Task 5), `Handlers['data.reset']` (Task 3).
- Produces: `live` fires due nudges on each minute tick and on focus. A nudge's What now navigates to `/?whatnow`, which Today turns into a What now ask.
- For `(app)/+page.svelte`, apply only the `page` import and the `?whatnow` effect here. The `welcomeDue` hunks belong to Task 8.

- [ ] **Step 1: Implement**

Modify `web/src/lib/live.svelte.ts`:

```diff
@@ -2,17 +2,20 @@ import { goto } from '$app/navigation';
 import { api, type SettingsView } from './api';
 import { chat } from './chat.svelte';
 import { Stream } from './stream.svelte';
+import { due, nudges, type Nudge } from './nudges';
 import { morningDue } from './time';
 import { toasts } from './toasts.svelte';
 import { today } from './today.svelte';
 
 /**
  * The page's live connection to the server, shared by every screen: the event stream, the
- * settings, and a clock that ticks each minute.
+ * settings, and a clock that ticks each minute and fires the nudges due.
  */
 class Live {
 	view = $state<SettingsView | null>(null);
 	now = $state(new Date());
+	/** Nudges fire for the span since this, so each fires once and none fire for before the page loaded. */
+	#lastTick = Date.now();
 
 	#stream = new Stream(
 		{
@@ -28,7 +31,9 @@ class Live {
 			'plan.updated': () => today.changed(),
 			'chat.delta': (d) => chat.delta(d.text),
 			'chat.tool': (d) => chat.tool(d),
-			'chat.message': (d) => chat.message(d)
+			'chat.message': (d) => chat.message(d),
+			// Everything was deleted, maybe from another tab. Start over from first-run setup.
+			'data.reset': () => location.assign('/')
 		},
 		() => {
 			today.changed();
@@ -52,7 +57,7 @@ class Live {
 			(err) => toasts.error(err)
 		);
 		this.#stream.open();
-		const clock = setInterval(() => (this.now = new Date()), 60_000);
+		const clock = setInterval(() => this.#tick(), 60_000);
 		const focus = () => this.#caughtUp();
 		window.addEventListener('focus', focus);
 		return () => {
@@ -65,7 +70,7 @@ class Live {
 	// A tab left open overnight still shows yesterday. Coming back to it catches up, and starts the
 	// morning when the new day has no plan yet.
 	async #caughtUp(): Promise<void> {
-		this.now = new Date();
+		this.#tick();
 		const seen = today.today?.date;
 		try {
 			await today.refresh();
@@ -76,6 +81,35 @@ class Live {
 		const t = today.today;
 		if (seen && t && morningDue(seen, t.date, t.plan !== null)) goto('/morning');
 	}
+
+	#tick(): void {
+		this.now = new Date();
+		const to = this.now.getTime();
+		const t = today.today;
+		if (this.view && t) {
+			const list = nudges(today.events, t.plan, today.tasks, this.view.settings, t.date, this.endOfDay);
+			for (const n of due(list, this.#lastTick, to)) deliver(n);
+		}
+		this.#lastTick = to;
+	}
+}
+
+/**
+ * Shows a nudge as a notification when the browser allows it, else in the page. The tag makes
+ * two open tabs share one notification. An event-end nudge opens What now when clicked.
+ */
+function deliver(n: Nudge): void {
+	const open = n.kind === 'event_end' ? () => goto('/?whatnow') : undefined;
+	if (typeof Notification !== 'undefined' && Notification.permission === 'granted') {
+		const note = new Notification('whatnow', { body: n.text, tag: n.key });
+		note.onclick = () => {
+			window.focus();
+			open?.();
+			note.close();
+		};
+	} else {
+		toasts.nudge(n.text, open && { label: 'What now?', run: open });
+	}
 }
 
 export const live = new Live();
```

Then in `web/src/routes/(app)/+page.svelte`, add `import { page } from '$app/state';` after the `goto` import, and add this after the morning-redirect effect:

```svelte
	// A nudge's What now? arrives as ?whatnow, from any page.
	$effect(() => {
		if (t?.plan && page.url.searchParams.has('whatnow')) {
			goto('/', { replaceState: true });
			if (!t.day?.wrapped_at) whatNow();
		}
	});
```

There are no unit tests here: the firing rule is `due()`, tested in Task 5. Verify by hand in Task 9.

- [ ] **Step 2: Run the checks**

Run: `cd web && npm run check && npm test`
Expected: all pass, no type errors.

- [ ] **Step 3: Commit**

```bash
git add -A web/src/lib/live.svelte.ts web/src/routes/(app)/+page.svelte
git commit -m "Fire nudges as notifications or in the page"
```

### Task 7: The full settings page and delete-everything UI

**Files:**
- Create: `web/src/lib/components/AboutYou.svelte`
- Create: `web/src/lib/components/NotifyPermission.svelte`
- Create: `web/src/lib/components/DangerZone.svelte`
- Modify: `web/src/routes/settings/+page.svelte`

**Interfaces:**
- Consumes: the Task 3 settings fields and `api.deleteData()`.
- Produces: `AboutYou` (`bind:style`, `bind:aboutMe`) and `NotifyPermission` (no props), both reused by onboarding in Task 8. On a failed save, the settings page refetches only `keys`, so typed edits survive a validation error.

- [ ] **Step 1: Implement**

Create `web/src/lib/components/AboutYou.svelte`:

```svelte
<script lang="ts">
	interface Props {
		style: string;
		aboutMe: string;
	}
	let { style = $bindable(), aboutMe = $bindable() }: Props = $props();
</script>

<label>
	<span class="label">Assistant style <span class="muted">({style.length}/500)</span></span>
	<span class="muted hint">How it talks to you, like "gentle and brief" or "a drill sergeant who cares."</span>
	<textarea class="field" rows="2" maxlength="500" bind:value={style}></textarea>
</label>
<label>
	<span class="label">About me <span class="muted">({aboutMe.length}/1000)</span></span>
	<span class="muted hint">What drains you and what's easy. It shapes how plans weigh each task.</span>
	<textarea class="field" rows="4" maxlength="1000" bind:value={aboutMe}></textarea>
</label>

<style>
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.hint {
		font-size: 13px;
	}
</style>
```

Create `web/src/lib/components/NotifyPermission.svelte`:

```svelte
<script lang="ts">
	/** Notification.permission, or denied where the browser has no notifications at all. */
	let permission = $state<NotificationPermission>(
		typeof Notification === 'undefined' ? 'denied' : Notification.permission
	);

	async function ask() {
		permission = await Notification.requestPermission();
	}
</script>

{#if permission === 'default'}
	<button type="button" class="btn" onclick={ask}>Turn on notifications</button>
{:else if permission === 'granted'}
	<span class="chip">Notifications on</span>
{:else}
	<span class="muted">Blocked in your browser. Nudges show in the page instead.</span>
{/if}
```

Create `web/src/lib/components/DangerZone.svelte`:

```svelte
<script lang="ts">
	import { api } from '#lib/api.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	const phrase = 'delete everything';
	let typed = $state('');
	let deleting = $state(false);

	async function wipe() {
		deleting = true;
		try {
			await api.deleteData();
			location.assign('/'); // the stream's data.reset does this too, unless it's down
		} catch (err) {
			toasts.error(err);
			deleting = false;
		}
	}
</script>

<section class="sticker danger">
	<h2 class="label">Danger zone</h2>
	<p>
		Deletes every task, event, day, plan, chat, and summary, your settings, and the saved keys. There's
		no undo.
	</p>
	<label>
		<span class="muted">Type <strong>{phrase}</strong> to confirm</span>
		<input class="field" bind:value={typed} autocomplete="off" />
	</label>
	<button class="btn wipe" disabled={typed !== phrase || deleting} onclick={wipe}>
		{deleting ? 'Deleting…' : 'Delete everything'}
	</button>
</section>

<style>
	.danger {
		border-color: var(--danger);
		box-shadow: 3px 3px 0 var(--danger);
		padding: 20px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	h2,
	p {
		margin: 0;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.wipe {
		align-self: flex-start;
		background: var(--danger);
		color: var(--on-primary);
	}
	.wipe:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}
</style>
```

Modify `web/src/routes/settings/+page.svelte`:

```diff
@@ -1,6 +1,9 @@
 <script lang="ts">
 	import { onMount } from 'svelte';
 	import { api, type ConnectionResult, type Provider, type SettingsView } from '#lib/api.ts';
+	import AboutYou from '#lib/components/AboutYou.svelte';
+	import DangerZone from '#lib/components/DangerZone.svelte';
+	import NotifyPermission from '#lib/components/NotifyPermission.svelte';
 	import { live } from '#lib/live.svelte.ts';
 	import { toasts } from '#lib/toasts.svelte.ts';
 
@@ -30,7 +33,8 @@
 				: 'not set'
 	);
 
-	// PUT takes the whole settings object, so this sends back everything GET returned.
+	// PUT takes the whole settings object, so this sends back everything GET returned. The key
+	// is saved first, so whatever happens next, the key note is refetched to match.
 	async function save(e: SubmitEvent) {
 		e.preventDefault();
 		if (!view) return;
@@ -45,6 +49,8 @@
 			live.view = view;
 		} catch (err) {
 			toasts.error(err);
+			const fresh = await api.settings().catch(() => null);
+			if (fresh && view) view.keys = fresh.keys;
 		} finally {
 			saving = false;
 		}
@@ -67,6 +73,7 @@
 	<h1>Settings</h1>
 	{#if view}
 		<form class="sticker" onsubmit={save}>
+			<h2 class="label">Connection</h2>
 			<label>
 				<span class="label">Provider</span>
 				<select class="field" bind:value={view.settings.provider}>
@@ -93,7 +100,6 @@
 				</label>
 			{/if}
 			<div class="actions">
-				<button class="btn primary" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
 				<button type="button" class="btn" disabled={testing} onclick={test}>
 					{testing ? 'Testing…' : 'Test connection'}
 				</button>
@@ -103,7 +109,42 @@
 					</span>
 				{/if}
 			</div>
+
+			<h2 class="label">You</h2>
+			<AboutYou bind:style={view.settings.style} bind:aboutMe={view.settings.about_me} />
+
+			<h2 class="label">Day</h2>
+			<div class="row">
+				<label>
+					<span class="label">End of day</span>
+					<input class="field" type="time" required bind:value={view.settings.end_of_day} />
+				</label>
+				<label>
+					<span class="label">Event alerts, minutes before</span>
+					<input class="field" type="number" min="1" max="60" required bind:value={view.settings.alert_lead_minutes} />
+				</label>
+			</div>
+
+			<h2 class="label">Nudges</h2>
+			<label class="check">
+				<input type="checkbox" bind:checked={view.settings.nudges.before_event} />
+				Before each event
+			</label>
+			<label class="check">
+				<input type="checkbox" bind:checked={view.settings.nudges.event_end} />
+				When an event ends, with a What now?
+			</label>
+			<label class="check">
+				<input type="checkbox" bind:checked={view.settings.nudges.late_day} />
+				Late in the day, if a must due today hasn't moved
+			</label>
+			<div><NotifyPermission /></div>
+
+			<div class="actions">
+				<button class="btn primary" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
+			</div>
 		</form>
+		<DangerZone />
 	{:else}
 		<p class="muted">Loading…</p>
 	{/if}
@@ -114,10 +155,21 @@
 		max-width: 560px;
 		margin: 0 auto;
 		padding: 16px;
+		display: flex;
+		flex-direction: column;
+		gap: 16px;
 	}
 	h1 {
+		margin: 0;
 		font-weight: var(--heavy);
 	}
+	h2 {
+		margin: 10px 0 0;
+		font-size: 14px;
+	}
+	h2:first-child {
+		margin-top: 0;
+	}
 	form {
 		display: flex;
 		flex-direction: column;
@@ -129,6 +181,16 @@
 		flex-direction: column;
 		gap: 4px;
 	}
+	.row {
+		display: grid;
+		grid-template-columns: 1fr 1fr;
+		gap: 12px;
+	}
+	.check {
+		flex-direction: row;
+		align-items: center;
+		gap: 8px;
+	}
 	.actions {
 		display: flex;
 		align-items: center;
```

- [ ] **Step 2: Run the checks**

Run: `cd web && npm run check && npm test`
Expected: all pass, no type errors.

- [ ] **Step 3: Commit**

```bash
git add -A web/src/lib/components/AboutYou.svelte web/src/lib/components/DangerZone.svelte web/src/lib/components/NotifyPermission.svelte web/src/routes/settings/+page.svelte
git commit -m "Fill out Settings and add delete everything"
```

### Task 8: Onboarding

**Files:**
- Create: `web/src/lib/welcome.ts`
- Test: `web/src/lib/welcome.test.ts`
- Modify: `web/src/routes/+layout.svelte`
- Modify: `web/src/routes/(app)/+page.svelte`
- Create: `web/src/routes/welcome/+page.svelte`

**Interfaces:**
- Consumes: `AboutYou`, `NotifyPermission` (Task 7); `Dots`; `live.view`.
- Produces: `welcomeDue(settings, path): boolean`; the `/welcome` route. Today's morning redirect now waits for settings and holds back while `welcomeDue(settings, '/')`, so a fresh install goes to `/welcome`, not `/morning`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/welcome.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import type { Settings } from './api';
import { welcomeDue } from './welcome';

const fresh: Settings = {
	provider: '',
	anthropic: { main_model: 'claude-sonnet-5-5', light_model: 'claude-haiku-4-5', base_url: '' },
	openai: { main_model: '', light_model: '', base_url: '' },
	style: '',
	about_me: '',
	end_of_day: '17:30',
	nudges: { before_event: true, event_end: true, late_day: true },
	alert_lead_minutes: 10,
	onboarded: false
};

describe('welcomeDue', () => {
	it.each([
		['a fresh install opening Today', fresh, '/', true],
		['a fresh install opening the morning', fresh, '/morning', true],
		['setup was skipped', { ...fresh, onboarded: true }, '/', false],
		['a provider is saved', { ...fresh, provider: 'anthropic' }, '/', false],
		['already in onboarding', fresh, '/welcome', false],
		['in Settings', fresh, '/settings', false]
	] as const)('Should know whether to onboard when %s', (_, s, path, want) => {
		expect(welcomeDue(s, path)).toBe(want);
	});
});
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/lib/welcome.test.ts`
Expected: fails to resolve `./welcome`.

- [ ] **Step 3: Implement**

Create `web/src/lib/welcome.ts`:

```ts
import type { Settings } from './api';

/**
 * Whether opening `path` should go to onboarding first: setup was never finished or skipped, and
 * nothing is set up. Onboarding and Settings are where setup happens, so they never redirect.
 */
export function welcomeDue(s: Settings, path: string): boolean {
	return !s.onboarded && s.provider === '' && path !== '/welcome' && path !== '/settings';
}
```

Modify `web/src/routes/+layout.svelte`:

```diff
@@ -1,14 +1,21 @@
 <script lang="ts">
 	import { onMount } from 'svelte';
+	import { goto } from '$app/navigation';
+	import { page } from '$app/state';
 	import '#lib/tokens.css';
 	import favicon from '#lib/assets/favicon.svg';
 	import Toasts from '#lib/components/Toasts.svelte';
 	import { live } from '#lib/live.svelte.ts';
+	import { welcomeDue } from '#lib/welcome.ts';
 	import type { LayoutProps } from './$types';
 
 	let { children }: LayoutProps = $props();
 
 	onMount(() => live.start());
+
+	$effect(() => {
+		if (live.view && welcomeDue(live.view.settings, page.url.pathname)) goto('/welcome', { replaceState: true });
+	});
 </script>
 
 <svelte:head>
```

Create `web/src/routes/welcome/+page.svelte`:

```svelte
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, type ConnectionResult, type Provider, type SettingsView } from '#lib/api.ts';
	import AboutYou from '#lib/components/AboutYou.svelte';
	import Dots from '#lib/components/Dots.svelte';
	import NotifyPermission from '#lib/components/NotifyPermission.svelte';
	import { live } from '#lib/live.svelte.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	const steps = ['provider', 'key', 'test', 'you', 'nudges', 'ready'] as const;
	let at = $state(0);
	const step = $derived(steps[at]);

	let view = $state<SettingsView | null>(null);
	let key = $state('');
	/** Set when the user wants to type a key even though one came from the environment. */
	let ownKey = $state(false);
	let busy = $state(false);
	let result = $state<ConnectionResult | null>(null);

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
	const envVar = $derived(provider === 'anthropic' ? 'ANTHROPIC_API_KEY' : 'OPENAI_API_KEY');
	const fromEnv = $derived(view?.keys[provider] === 'env' && !ownKey);
	const modelsSet = $derived(
		!!view?.settings[provider].main_model.trim() && !!view?.settings[provider].light_model.trim()
	);
	const keyReady = $derived(fromEnv || key.trim() !== '' || view?.keys[provider] === 'keychain');

	/** Saves the form's settings. Returns whether it worked. */
	async function save(): Promise<boolean> {
		if (!view) return false;
		try {
			view = await api.saveSettings(view.settings);
			live.view = view;
			return true;
		} catch (err) {
			toasts.error(err);
			return false;
		}
	}

	async function saveKey() {
		busy = true;
		try {
			if (!fromEnv && key.trim()) {
				await api.saveKey(provider, key.trim());
				key = '';
			}
			if (await save()) {
				at++;
				test();
			}
		} catch (err) {
			toasts.error(err);
		} finally {
			busy = false;
		}
	}

	async function test() {
		busy = true;
		result = null;
		try {
			result = await api.testConnection();
		} catch (err) {
			toasts.error(err);
		} finally {
			busy = false;
		}
	}

	async function saveYou() {
		busy = true;
		if (await save()) at++;
		busy = false;
	}

	async function finish() {
		if (!view) return;
		view.settings.onboarded = true;
		if (await save()) at++;
	}

	// Skipping keeps only what was already saved, so a half-chosen provider never sticks.
	async function later() {
		try {
			const saved = await api.settings();
			saved.settings.onboarded = true;
			live.view = await api.saveSettings(saved.settings);
			goto('/', { replaceState: true });
		} catch (err) {
			toasts.error(err);
		}
	}
</script>

<main class="welcome">
	<Dots count={steps.length} {at} />
	{#if view}
		<section class="sticker card">
			{#if step === 'provider'}
				<h1>Hi, I'm whatnow</h1>
				<p>I turn the pile in your head into one next thing to do. First, which model should I use?</p>
				<div class="choices">
					<button
						class="sticker choice"
						class:on={provider === 'anthropic'}
						onclick={() => view && (view.settings.provider = 'anthropic')}
					>
						<strong>Anthropic</strong>
						<span class="muted">Claude</span>
					</button>
					<button
						class="sticker choice"
						class:on={provider === 'openai'}
						onclick={() => view && (view.settings.provider = 'openai')}
					>
						<strong>OpenAI-compatible</strong>
						<span class="muted">OpenAI, Ollama, OpenRouter, and others</span>
					</button>
				</div>
				{#if provider === 'openai'}
					<label>
						<span class="label">Base URL <span class="muted">(optional)</span></span>
						<input class="field" bind:value={view.settings.openai.base_url} placeholder="https://api.openai.com/v1" />
					</label>
				{/if}
				<label>
					<span class="label">Main model</span>
					<input class="field" bind:value={view.settings[provider].main_model} />
				</label>
				<label>
					<span class="label">Light model</span>
					<input class="field" bind:value={view.settings[provider].light_model} />
				</label>
			{:else if step === 'key'}
				<h1>Your API key</h1>
				{#if fromEnv}
					<p>Found your key in <code>{envVar}</code>.</p>
					<button class="link" onclick={() => (ownKey = true)}>Use a different key</button>
				{:else}
					<p>It stays in your system keychain, never in whatnow's database.</p>
					<input
						class="field"
						type="password"
						bind:value={key}
						autocomplete="off"
						aria-label="API key"
						placeholder={view.keys[provider] === 'keychain' ? 'Saved. Leave blank to keep' : ''}
					/>
				{/if}
			{:else if step === 'test'}
				<h1>Checking the connection</h1>
				{#if busy}
					<p class="muted">Saying hello…</p>
				{:else if result?.ok}
					<p class="ok">Connected.</p>
				{:else if result}
					<p class="bad">{result.error}</p>
					<button class="btn" onclick={test}>Try again</button>
				{/if}
			{:else if step === 'you'}
				<h1>About you</h1>
				<p>Optional. Both can change later in Settings.</p>
				<AboutYou bind:style={view.settings.style} bind:aboutMe={view.settings.about_me} />
			{:else if step === 'nudges'}
				<h1>Nudges</h1>
				<ul>
					<li>A heads-up before each event, so you can find a stopping point.</li>
					<li>A What now? when an event ends.</li>
					<li>One late-day check if a must due today hasn't moved.</li>
				</ul>
				<p class="muted">Without notifications, they show in the page while it's open.</p>
				<NotifyPermission />
			{:else}
				<h1>You're set</h1>
				<p>Let's plan your first day.</p>
			{/if}
		</section>

		<div class="nav">
			{#if at > 0 && step !== 'ready'}
				<button class="btn" disabled={busy} onclick={() => at--}>Back</button>
			{/if}
			<span class="spacer"></span>
			{#if step !== 'ready'}
				<button class="link" onclick={later}>Set up later</button>
			{/if}
			{#if step === 'provider'}
				<button class="btn primary" disabled={!modelsSet} onclick={() => at++}>Next</button>
			{:else if step === 'key'}
				<button class="btn primary" disabled={!keyReady || busy} onclick={saveKey}>
					{busy ? 'Saving…' : 'Next'}
				</button>
			{:else if step === 'test'}
				<button class="btn primary" disabled={!result?.ok} onclick={() => at++}>Next</button>
			{:else if step === 'you'}
				<button class="btn" onclick={() => at++}>Skip</button>
				<button class="btn primary" disabled={busy} onclick={saveYou}>Next</button>
			{:else if step === 'nudges'}
				<button class="btn primary" onclick={finish}>Next</button>
			{:else}
				<button class="btn primary" onclick={() => goto('/morning')}>Start my day</button>
			{/if}
		</div>
	{:else}
		<p class="muted">Loading…</p>
	{/if}
</main>

<style>
	.welcome {
		max-width: 560px;
		margin: 0 auto;
		padding: 32px 16px;
		display: flex;
		flex-direction: column;
		gap: 18px;
	}
	.card {
		padding: 24px;
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
	h1 {
		margin: 0;
		font-weight: var(--heavy);
	}
	p {
		margin: 0;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.choices {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 12px;
	}
	.choice {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 14px;
		text-align: left;
	}
	.choice.on {
		background: var(--chip);
	}
	.nav {
		display: flex;
		align-items: center;
		gap: 10px;
	}
	.spacer {
		flex: 1;
	}
	.link {
		background: none;
		border: none;
		padding: 0;
		color: var(--primary);
		font-weight: var(--heavy);
		text-align: left;
	}
	.ok {
		font-weight: var(--heavy);
	}
	.bad {
		color: var(--danger);
		font-weight: var(--heavy);
	}

	@media (max-width: 600px) {
		.choices {
			grid-template-columns: 1fr;
		}
	}
</style>
```

Then in `web/src/routes/(app)/+page.svelte`, add `import { welcomeDue } from '#lib/welcome.ts';` after the `today` import, and replace the morning-redirect effect with:

```svelte
	// A day with no plan starts with the morning check-in, once first-run setup has had its turn.
	$effect(() => {
		if (t && !t.plan && live.view && !welcomeDue(live.view.settings, '/')) goto('/morning', { replaceState: true });
	});
```

- [ ] **Step 4: Run the checks**

Run: `cd web && npm run check && npm test`
Expected: all pass, no type errors.

- [ ] **Step 5: Commit**

```bash
git add -A web/src/lib/welcome.test.ts web/src/lib/welcome.ts web/src/routes/(app)/+page.svelte web/src/routes/+layout.svelte web/src/routes/welcome/+page.svelte
git commit -m "Add first-run onboarding"
```

### Task 9: Smoke test by hand and tidy the records

**Files:**
- Modify: `docs/superpowers/specs/2026-10-05-whatnow-web-ui-today-design.md` (the slice table)

**Interfaces:** none.

- [ ] **Step 1: Build and run on a throwaway database**

```bash
just build
./whatnow serve -addr 127.0.0.1:7791 -db /tmp/whatnow-smoke.db
```

Check each of these in the browser at `http://127.0.0.1:7791`:
- `/` goes to `/welcome`. On OpenAI-compatible, Next stays disabled until both models are filled in. The key step says "Found your key in `OPENAI_API_KEY`".
- **Set up later** opens `/morning`, and `GET /api/v1/settings` shows `onboarded: true` with `provider: ""`.
- After the morning, the strip shows hour ticks, a dashed end-of-day mark, and start times in the blocks.
- An event that ends a minute from now gives a yellow "…'s done. What now?" toast within a minute, and its button opens What now.
- Settings shows all four sections plus the Danger zone. Typing `delete everything` turns the button on. Deleting sends this tab and a second open tab to `/welcome`.
- A full onboarding run (Test connection makes one tiny paid call; get the go-ahead first) ends on **Start my day**.

- [ ] **Step 2: Point the part 1 spec's slice table at the split**

In `docs/superpowers/specs/2026-10-05-whatnow-web-ui-today-design.md`, replace the 5c row:

```markdown
| 5c | Full settings, delete everything, onboarding, nudges, the day strip fix |
| 5d | The `e2e` build tag, a fake provider, and Playwright |
```

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/specs/2026-10-05-whatnow-web-ui-today-design.md
git commit -m "Point the part 1 spec at the 5c and 5d split"
```
