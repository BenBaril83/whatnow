# whatnow Days and Planning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The user starts the day by setting a pace and gets a must / should / could plan. The plan stays fixed until a change requires a replan, and "what now?" picks one task to start, live. Every model reply is validated and retried once if invalid, and code builds the answer if it still fails, so a bad model day still gives a valid plan.

**Architecture:** `facts` is pure: free gaps, eligibility, blocked tasks, and annotations, computed from tasks, blockers, events, and push counts. `prompt.Facts` renders them as live context. `planner` makes one structured main-model call per plan or pick, checks the reply against the facts, retries once with the problems attached, and otherwise builds the answer in code. Plans are versioned records, stored apart from activity. `planner.Run` watches the hub for batches (and undos) that call for a replan. `httpapi` gains today, start, pace, what-now, and push routes.

**Tech Stack:** Go 1.27, `github.com/anthropics/anthropic-sdk-go` v1.78.0 (beta messages endpoint for refusal fallbacks), plus everything from plans 1 and 2.

**Spec:** `docs/superpowers/specs/2026-10-05-whatnow-design.md`

**Where this sits:** plan 3. Core and the LLM foundation are merged. Later plans:
- 3b. Good morning reconcile, wrap up, and day summaries
- 4. Agent chat
- 5. Web UI
- 6. Packaging and prompt evals

**Decisions this plan makes:**
- **Plan 3 is split.** This plan builds what a day runs on. Reconciling a skipped day, wrap up, and day summaries are plan 3b. They build on this plan's push, plans, and days.
- **A push sets `not_before`** and is logged as its own `push` action, so push counts come from the activity log with undone pushes left out. An ordinary edit that sets `not_before` isn't a push.
- **Starting a day is not in a batch.** Undoing it would orphan the day's plans. Pace changes are in a batch and can be undone, and undoing one triggers a replan.
- **Plans are versioned history**, stored apart from activity. A replan adds a version. It never edits or undoes one.
- **An event without an end counts as 30 minutes** when free gaps are worked out.
- **"Too big for any free gap"** means M with no gap of 1h, or L with no gap of 3h. It's an annotation, never a filter. If no free time remains, no task gets this annotation.
- **The plan may leave tasks out.** Eligible tasks the plan omits are the unplanned tray, in fallback order.
- **What now works before the day starts.** It just has no pace to weigh.
- **Only provider-side refusals use fallbacks.** The Claude client always calls the beta endpoint. It sends the `server-side-fallback-2026-07-01` beta and `fallbacks: "default"` only when a request sets `Fallback`, and the planner sets it on every main-model call.
- **Retries are for bad replies only.** A provider error (outage, bad key, refusal) goes straight to the code fallback. The SDK already retries transient HTTP failures.
- **The end of the working day is a setting** (`end_of_day`, default `17:30`). Nudge toggles and alert lead time wait for plan 5, which uses them.

## Global Constraints

- Module path: `github.com/jameynakama/whatnow`. Go directive: `go 1.27`.
- Code establishes facts and the LLM makes every judgment. Facts reach the model as annotations, never as filters. The fallback plan and fallback pick are the only exceptions.
- Validation rejects a plan that names an unknown or ineligible task. Eligible means open, not soft-deleted, and with no `not_before` in the future.
- Fallback plan: due today or overdue is a must, due later is a should ordered by due date, and undated is a could ordered by creation time.
- What-now fallback: the first unblocked task in fallback-plan order.
- Pace has five levels: *running on fumes / gentle / steady / full steam / go hard*.
- Replan triggers: a pace change, a task becoming due today or being created that way, dropping a must, adding or removing a blocker that involves a must, and (in plan 4) the agent's `replan`. Completing a must just marks it done.
- Structured replies use JSON-schema output, never forced tool calls. Schemas list fields in the order the model should decide them.
- All API routes live under `/api/v1/`. Writes respond with `{batch_id, data}`.
- Tests use stdlib `testing` and `go-cmp`, with a real SQLite database in a temp dir. Tests never call a real provider.
- Commit messages: plain imperative subject, no `Co-Authored-By` trailer.

## Review Focus

1. **The model names a task twice, an unknown task, a task that isn't eligible yet, or a tier that doesn't exist.** Expected: one retry that lists every problem, then a fallback plan. Pinned in Task 7.
2. **No provider is set up, or the provider is down.** Expected: the morning still gets a plan, and what-now still gets a pick, both from code. Pinned in Tasks 7 and 9.
3. **An edit lands after the day starts but before its first plan.** Expected: no stray "edit" plan. The morning plan is version 1. Pinned in Task 8.
4. **The user undoes a pace change.** Expected: the pace goes back and the plan is remade for it. Pinned in Tasks 4 and 8.
5. **Events that run overnight, overlap, have no end, or fall on a DST change.** Expected: free gaps measured in real time in the user's zone. Pinned in Task 5.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/llm/llm.go`, `internal/llm/anthropic.go` | `Request.Fallback`. The Claude client moves to the beta endpoint |
| `internal/jsonschema/jsonschema.go` | Ordered schema builders shared by cleanup and the planner |
| `internal/cleanup/schema.go` | Uses `jsonschema` |
| `internal/settings/settings.go` | `EndOfDay` setting |
| `internal/store/migrations/0003_days.sql` | `days` and `plans` tables |
| `internal/domain/domain.go` | `Pace`, `Day`, `Tier`, `Trigger`, `PlanEntry`, `Plan` |
| `internal/service/days.go` | Start a day, change pace (undoable), save and load plan versions |
| `internal/service/tasks.go` | `PushTask`, `PushCounts`, `AppendFeeling` |
| `internal/service/undo.go` | Reverting a day snapshot |
| `internal/facts/facts.go` | Pure facts: gaps, eligibility, blockers, annotations, fallback order |
| `internal/prompt/facts.go` | Renders facts as prompt context |
| `internal/planner/planner.go` | `Planner`, gathering state, context, the ask-check-retry helper |
| `internal/planner/instructions.go` | Core instructions for plans and picks |
| `internal/planner/plan.go` | `Replan`, plan schema, plan check, fallback plan |
| `internal/planner/whatnow.go` | `WhatNow`, pick schema, pick check, fallback pick |
| `internal/planner/triggers.go` | `Run`: watches the hub and replans when a change calls for it |
| `internal/planner/today.go` | The home-screen view and starting the day |
| `internal/stream/hub.go` | `BatchUndone` payload |
| `internal/httpapi/days.go` | Today, start, pace, what-now, and push handlers |
| `cmd/whatnow/main.go` | Wires the planner and its watcher |

---


### Task 1: Refusal fallbacks on the Claude client

Claude's main-tier models can decline a request. With a server-side fallback, the API re-serves a declined request on a fallback model inside the same call. That needs the beta endpoint. The client moves to the beta endpoint for every call, and opts in only when the request asks.

**Files:**
- Modify: `internal/llm/llm.go`
- Modify: `internal/llm/anthropic.go`
- Modify: `internal/llm/llm_test.go`

**Interfaces:**
- Produces: `llm.Request.Fallback bool`. Set it on main-model calls; the Claude client then sends the `server-side-fallback-2026-07-01` beta and `"fallbacks": "default"`. OpenAI-compatible clients ignore it.

- [ ] **Step 1: Write the failing tests**

`internal/llm/llm_test.go`. Apply:

```diff
--- a/internal/llm/llm_test.go
+++ b/internal/llm/llm_test.go
@@ -18,6 +18,7 @@ type fakeAPI struct {
 	status int
 	reply  string
 	path   string
+	header http.Header
 	body   map[string]any
 }
 
@@ -25,6 +26,7 @@ func (f *fakeAPI) start(t *testing.T) string {
 	t.Helper()
 	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 		f.path = r.URL.Path
+		f.header = r.Header
 		raw, _ := io.ReadAll(r.Body)
 		if err := json.Unmarshal(raw, &f.body); err != nil {
 			t.Errorf("request body is not JSON: %s", raw)
@@ -96,6 +98,28 @@ func TestAnthropicRequestAndReply(t *testing.T) {
 	}
 }
 
+func TestAnthropicFallbackOptIn(t *testing.T) {
+	cases := []struct {
+		name     string
+		fallback bool
+	}{{"off", false}, {"on", true}}
+	for _, c := range cases {
+		t.Run(c.name, func(t *testing.T) {
+			api := &fakeAPI{status: 200, reply: anthropicReply("end_turn", `{"ok":true}`)}
+			req := request()
+			req.Fallback = c.fallback
+			if _, err := llm.NewAnthropic("sk-test", api.start(t)).Complete(context.Background(), req); err != nil {
+				t.Fatal(err)
+			}
+			beta := strings.Contains(api.header.Get("anthropic-beta"), "server-side-fallback-2026-07-01")
+			fallbacks, sent := api.body["fallbacks"]
+			if beta != c.fallback || sent != c.fallback || (sent && fallbacks != "default") {
+				t.Errorf("beta header %v, fallbacks %v (sent %v); want both only when opted in", beta, fallbacks, sent)
+			}
+		})
+	}
+}
+
 func TestOpenAIRequestAndReply(t *testing.T) {
 	reply, _ := json.Marshal(map[string]any{
 		"id": "c1", "object": "chat.completion", "created": 1, "model": "test-model",
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/llm/`
Expected: build fails: `req.Fallback undefined`

- [ ] **Step 3: Implement**

`internal/llm/llm.go`. Apply:

```diff
--- a/internal/llm/llm.go
+++ b/internal/llm/llm.go
@@ -34,4 +34,7 @@ type Request struct {
 	Schema     map[string]any
 	SchemaName string
 	MaxTokens  int64
+	// Fallback asks the provider to re-serve a refused request on another model. Only the Claude
+	// API supports it, on its main-tier models; other providers ignore it.
+	Fallback bool
 }
```

`internal/llm/anthropic.go`. Replace the whole file with:

```go
package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicClient struct {
	client anthropic.Client
}

// NewAnthropic returns a Client for the Claude API. An empty baseURL uses the default endpoint.
func NewAnthropic(apiKey, baseURL string) Client {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &anthropicClient{client: anthropic.NewClient(opts...)}
}

// Complete uses the beta endpoint, which refusal fallbacks need. Without Fallback set, a request
// there is the same as one to the standard endpoint.
func (a *anthropicClient) Complete(ctx context.Context, req Request) (string, error) {
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: req.MaxTokens,
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(req.User))},
	}
	for _, b := range req.System {
		block := anthropic.BetaTextBlockParam{Text: b.Text}
		if b.Cache {
			block.CacheControl = anthropic.NewBetaCacheControlEphemeralParam()
		}
		params.System = append(params.System, block)
	}
	if req.Schema != nil {
		params.OutputConfig = anthropic.BetaOutputConfigParam{Format: anthropic.BetaJSONOutputFormatParam{Schema: req.Schema}}
	}
	if req.Fallback {
		// "default" routes each refusal category to a fallback model the API picks.
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}

	msg, err := a.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return "", ErrRefused
	case anthropic.BetaStopReasonMaxTokens:
		return "", ErrTruncated
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	return text.String(), nil
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/llm/`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/llm/anthropic.go internal/llm/llm.go internal/llm/llm_test.go
git commit -m "Opt main-model Claude calls into refusal fallbacks"
```


### Task 2: Shared schema builders

Cleanup's ordered-schema helpers move to their own package so the planner can use them. Fields are built with `Prop`, which keeps `go vet` happy about keyed struct literals. Cleanup's schema doesn't change on the wire; its existing order test still guards it.

**Files:**
- Create: `internal/jsonschema/jsonschema.go`
- Modify: `internal/cleanup/schema.go`
- Create: `internal/jsonschema/jsonschema_test.go`

**Interfaces:**
- Produces: package `jsonschema` with `Field`, `Prop(name string, schema any) Field`, `Object(fs ...Field) map[string]any`, `Array(items map[string]any) map[string]any`, `String(desc)`, `Integer(desc)`, `Enum(desc, values...)`.

- [ ] **Step 1: Write the failing tests**

`internal/jsonschema/jsonschema_test.go`. Create it with:

```go
package jsonschema_test

import (
	"encoding/json"
	"testing"

	"github.com/jameynakama/whatnow/internal/jsonschema"
)

func TestObjectKeepsFieldOrderAndRequiresEveryField(t *testing.T) {
	s := jsonschema.Object(
		jsonschema.Prop("zeta", jsonschema.String("first")),
		jsonschema.Prop("alpha", jsonschema.Integer("second")),
	)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"additionalProperties":false,"properties":{"zeta":{"description":"first","type":"string"},` +
		`"alpha":{"description":"second","type":"integer"}},"required":["zeta","alpha"],"type":"object"}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/jsonschema/ ./internal/cleanup/`
Expected: build fails: the `jsonschema` package doesn't exist yet

- [ ] **Step 3: Implement**

`internal/jsonschema/jsonschema.go`. Create it with:

```go
// Package jsonschema builds the JSON schemas that constrain structured LLM replies. Every object
// field is required and objects allow no extra properties, which keeps a schema inside what both
// the Claude API and OpenAI's strict mode accept.
//
// Field order matters: constrained decoding writes fields in schema order, so list them in the
// order the model should decide them.
package jsonschema

import (
	"bytes"
	"encoding/json"
)

// Field is one property of an object. Build it with Prop.
type Field struct {
	name   string
	schema any
}

func Prop(name string, schema any) Field { return Field{name: name, schema: schema} }

// fields marshals as a JSON object that keeps its order, which a Go map would sort.
type fields []Field

func (fs fields) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range fs {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, err := json.Marshal(f.name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(f.schema)
		if err != nil {
			return nil, err
		}
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func Object(fs ...Field) map[string]any {
	required := make([]string, len(fs))
	for i, f := range fs {
		required[i] = f.name
	}
	return map[string]any{"type": "object", "properties": fields(fs), "required": required, "additionalProperties": false}
}

func Array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func String(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func Integer(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func Enum(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}
```

`internal/cleanup/schema.go`. Replace the whole file with:

```go
package cleanup

import js "github.com/jameynakama/whatnow/internal/jsonschema"

// schema is the reply shape. Empty strings stand for "none". A task's ref and title come before
// its details, and blockers come after the tasks they refer to.
var schema = js.Object(
	js.Prop("original", js.Object(append([]js.Field{
		js.Prop("keep", map[string]any{"type": "boolean", "description": "False only when the note holds nothing but events."}),
	}, taskFields()...)...)),
	js.Prop("new_tasks", js.Array(js.Object(append([]js.Field{
		js.Prop("ref", js.String(`A short name other entries use to refer to this task, like "n1".`)),
	}, taskFields()...)...))),
	js.Prop("events", js.Array(js.Object(
		js.Prop("title", js.String("What the event is.")),
		js.Prop("starts_at", js.String("Local start time as YYYY-MM-DDTHH:MM.")),
		js.Prop("ends_at", js.String("Local end time as YYYY-MM-DDTHH:MM, or empty.")),
	))),
	js.Prop("blockers", js.Array(js.Object(
		js.Prop("task", js.String(`The task that waits: "original", a new task's ref, or "#<id>".`)),
		js.Prop("blocked_by", js.String(`The task that must finish first, referenced the same way.`)),
	))),
)

func taskFields() []js.Field {
	return []js.Field{
		js.Prop("title", js.String("Short and concrete.")),
		js.Prop("notes", js.String("Useful detail that doesn't fit the title, or empty.")),
		js.Prop("size", js.Enum("S, M, or L, or empty if unclear.", "", "S", "M", "L")),
		js.Prop("energy", js.Enum("Mental effort: low, med, or high, or empty if unclear.", "", "low", "med", "high")),
		js.Prop("feeling", js.String("How the user feels about it, in their words, or empty.")),
		js.Prop("due_on", js.String("YYYY-MM-DD, or empty.")),
		js.Prop("not_before", js.String("YYYY-MM-DD, or empty.")),
		js.Prop("refs", js.Array(js.Object(
			js.Prop("label", js.String("A ticket key or short name.")),
			js.Prop("url", js.String("The link, or empty.")),
		))),
	}
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/jsonschema/ ./internal/cleanup/`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/cleanup/schema.go internal/jsonschema/jsonschema.go internal/jsonschema/jsonschema_test.go
git commit -m "Share structured-output schema helpers"
```


### Task 3: End-of-day setting

Free time is counted from now to the end of the working day, so that time needs a home. It's `HH:MM`, local, defaulting to `17:30`. Settings saved before the field existed load with the default, because `Load` starts from `Defaults()`.

**Files:**
- Modify: `internal/settings/settings.go`
- Modify: `internal/settings/settings_test.go`

**Interfaces:**
- Produces: `settings.Settings.EndOfDay string` (json `end_of_day`), `settings.ClockLayout = "15:04"`.

- [ ] **Step 1: Write the failing tests**

`internal/settings/settings_test.go`. Apply:

```diff
--- a/internal/settings/settings_test.go
+++ b/internal/settings/settings_test.go
@@ -55,6 +55,7 @@ func TestValidate(t *testing.T) {
 		"base URL without scheme":       func(s *settings.Settings) { s.OpenAI.BaseURL = "localhost:11434" },
 		"style over 500 characters":     func(s *settings.Settings) { s.Style = strings.Repeat("é", 501) },
 		"about me over 1000 characters": func(s *settings.Settings) { s.AboutMe = strings.Repeat("🧠", 1001) },
+		"end of day not HH:MM":          func(s *settings.Settings) { s.EndOfDay = "5pm" },
 	}
 	for name, mutate := range cases {
 		t.Run(name, func(t *testing.T) {
@@ -136,3 +137,23 @@ func TestKeychainFailureFallsBackToEnv(t *testing.T) {
 		t.Errorf("got %q %q %v, want the env key", key, source, err)
 	}
 }
+
+func TestSettingsSavedBeforeEndOfDayExistedGetTheDefault(t *testing.T) {
+	ctx := context.Background()
+	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer db.Close()
+	if _, err := db.Exec(`INSERT INTO settings (id, body) VALUES (1, '{"provider":"anthropic","style":"terse"}')`); err != nil {
+		t.Fatal(err)
+	}
+
+	got, err := settings.Load(ctx, db)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if got.EndOfDay != "17:30" || got.Style != "terse" {
+		t.Errorf("loaded %+v", got)
+	}
+}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/settings/`
Expected: build fails: `got.EndOfDay undefined`

- [ ] **Step 3: Implement**

`internal/settings/settings.go`. Apply:

```diff
--- a/internal/settings/settings.go
+++ b/internal/settings/settings.go
@@ -9,6 +9,7 @@ import (
 	"errors"
 	"fmt"
 	"net/url"
+	"time"
 	"unicode/utf8"
 )
 
@@ -38,6 +39,8 @@ type Settings struct {
 	OpenAI    ProviderConfig `json:"openai"`
 	Style     string         `json:"style"`
 	AboutMe   string         `json:"about_me"`
+	// EndOfDay is when the working day ends, as local HH:MM. Free time is counted up to it.
+	EndOfDay string `json:"end_of_day"`
 }
 
 const (
@@ -48,7 +51,10 @@ const (
 // Defaults has Claude models filled in. OpenAI-compatible models are left for the user, since
 // they depend on the server.
 func Defaults() Settings {
-	return Settings{Anthropic: ProviderConfig{MainModel: "claude-sonnet-5-5", LightModel: "claude-haiku-4-5"}}
+	return Settings{
+		Anthropic: ProviderConfig{MainModel: "claude-sonnet-5-5", LightModel: "claude-haiku-4-5"},
+		EndOfDay:  "17:30",
+	}
 }
 
 // Config returns the active provider's model settings.
@@ -83,9 +89,15 @@ func (s Settings) Validate() error {
 	if n := utf8.RuneCountInString(s.AboutMe); n > MaxAboutMeRunes {
 		return fmt.Errorf("%w: about me is %d characters, the limit is %d", ErrInvalid, n, MaxAboutMeRunes)
 	}
+	if _, err := time.Parse(ClockLayout, s.EndOfDay); err != nil {
+		return fmt.Errorf("%w: end of day %q must be HH:MM", ErrInvalid, s.EndOfDay)
+	}
 	return nil
 }
 
+// ClockLayout is the form of a local time of day, such as EndOfDay.
+const ClockLayout = "15:04"
+
 // Load returns the saved settings, or the defaults if nothing has been saved.
 func Load(ctx context.Context, db *sql.DB) (Settings, error) {
 	s := Defaults()
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/settings/`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/settings/settings.go internal/settings/settings_test.go
git commit -m "Add end-of-day setting"
```


### Task 4: Days, plan versions, and pushes

Days and plans get tables. A pace change is an undoable write on a new `day` entity. Starting a day isn't in a batch. Plans are versioned rows. Pushes are logged as their own action so they can be counted. `AppendFeeling` is what "not this one" uses.

**Files:**
- Create: `internal/store/migrations/0003_days.sql`
- Modify: `internal/domain/domain.go`
- Modify: `internal/service/service.go`
- Modify: `internal/service/undo.go`
- Create: `internal/service/days.go`
- Modify: `internal/service/tasks.go`
- Create: `internal/service/days_test.go`
- Create: `internal/service/push_test.go`

**Interfaces:**
- Consumes: `batchActor`, `record`, `changeTask`, `liveTask`, `inTx`, `formatTime`/`parseTime`/`formatNullTime`/`parseNullTime`, `querier`, `scanner` from `internal/service`.
- Produces:
  - `domain.Pace` (1-5) with `Valid()` and `Label()`; `domain.Day{ID, Date, Pace, MoodNote, StartedAt, WrappedAt}`; `domain.Tier` (`TierMust`, `TierShould`, `TierCould`); `domain.Trigger` (`TriggerMorning`, `TriggerPace`, `TriggerEdit`, `TriggerAgent`); `domain.PlanEntry{TaskID, Tier, Rationale}`; `domain.Plan{ID, DayID, Version, Trigger, Reason, Opening, Entries, Fallback, CreatedAt}`.
  - `(*Service).StartDay(ctx, date domain.Date, pace domain.Pace, moodNote string) (domain.Day, error)`: `ErrConflict` if the date already started.
  - `(*Service).GetDay(ctx, date) (domain.Day, error)`: `ErrNotFound` if not started.
  - `(*Service).SetPace(ctx, batch int64, date domain.Date, pace domain.Pace, note string) (domain.Day, error)`: an empty note keeps the mood note.
  - `(*Service).SavePlan(ctx, domain.Plan) (domain.Plan, error)`: assigns `ID`, `Version`, `CreatedAt`.
  - `(*Service).LatestPlan(ctx, dayID int64) (domain.Plan, error)`: `ErrNotFound` if none.
  - `(*Service).PushTask(ctx, batch, id int64, until domain.Date) (domain.Task, error)`, `(*Service).PushCounts(ctx) (map[int64]int, error)`, `(*Service).AppendFeeling(ctx, batch, id int64, note string) (domain.Task, error)`.

- [ ] **Step 1: Write the failing tests**

`internal/service/days_test.go`. Create it with:

```go
package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestStartDayOncePerDate(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	day, err := svc.StartDay(ctx, "2026-10-05", 2, "slept badly")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetDay(ctx, "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(day, got); diff != "" {
		t.Errorf("stored day differs (-started +got):\n%s", diff)
	}

	if _, err := svc.StartDay(ctx, "2026-10-05", 3, ""); !errors.Is(err, service.ErrConflict) {
		t.Errorf("second start: err = %v, want ErrConflict", err)
	}
	if _, err := svc.GetDay(ctx, "2026-10-06"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unstarted day: err = %v, want ErrNotFound", err)
	}
}

func TestStartDayRejectsBadInput(t *testing.T) {
	svc, _ := newService(t)
	for name, start := range map[string]func() error{
		"pace 0":   func() error { _, err := svc.StartDay(context.Background(), "2026-10-05", 0, ""); return err },
		"pace 6":   func() error { _, err := svc.StartDay(context.Background(), "2026-10-05", 6, ""); return err },
		"bad date": func() error { _, err := svc.StartDay(context.Background(), "10/05", 3, ""); return err },
	} {
		if err := start(); !errors.Is(err, service.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestSetPaceKeepsMoodUnlessGivenAndUndoes(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.StartDay(ctx, "2026-10-05", 3, "anxious about the demo"); err != nil {
		t.Fatal(err)
	}

	batch := newBatch(t, svc, domain.ActorUser)
	day, err := svc.SetPace(ctx, batch, "2026-10-05", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if day.Pace != 1 || day.MoodNote != "anxious about the demo" {
		t.Errorf("after pace change: %+v", day)
	}
	day, err = svc.SetPace(ctx, newBatch(t, svc, domain.ActorAgent), "2026-10-05", 2, "a bit better")
	if err != nil {
		t.Fatal(err)
	}
	if day.MoodNote != "a bit better" {
		t.Errorf("mood note = %q", day.MoodNote)
	}

	// The first change can't be undone under the second, but the second can.
	if err := svc.Undo(ctx, batch); !errors.Is(err, service.ErrConflict) {
		t.Errorf("undo under a newer change: err = %v, want ErrConflict", err)
	}
	last, err := svc.LastUndoableBatch(ctx, domain.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, last); err != nil {
		t.Fatal(err)
	}
	if day, _ := svc.GetDay(ctx, "2026-10-05"); day.Pace != 1 || day.MoodNote != "anxious about the demo" {
		t.Errorf("after undo: %+v", day)
	}
}

func TestSetPaceErrors(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SetPace(ctx, batch, "2026-10-05", 3, ""); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unstarted day: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.SetPace(ctx, batch, "2026-10-05", 9, ""); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("pace 9: err = %v, want ErrInvalid", err)
	}
}

func TestPlansAreVersionedPerDay(t *testing.T) {
	svc, clock := newService(t)
	ctx := context.Background()
	day, _ := svc.StartDay(ctx, "2026-10-05", 3, "")
	if _, err := svc.LatestPlan(ctx, day.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("no plan yet: err = %v, want ErrNotFound", err)
	}

	first, err := svc.SavePlan(ctx, domain.Plan{DayID: day.ID, Trigger: domain.TriggerMorning, Opening: "Morning!",
		Entries: []domain.PlanEntry{{TaskID: 1, Tier: domain.TierMust, Rationale: "due today"}}})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(30 * time.Minute)
	second, err := svc.SavePlan(ctx, domain.Plan{DayID: day.ID, Trigger: domain.TriggerPace, Reason: "pace changed", Fallback: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || second.Version != 2 {
		t.Errorf("versions = %d, %d", first.Version, second.Version)
	}

	latest, err := svc.LatestPlan(ctx, day.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(second, latest); diff != "" {
		t.Errorf("latest plan (-saved +loaded):\n%s", diff)
	}
	if latest.Entries == nil {
		t.Error("an empty plan should load with empty entries, not null")
	}
}

func TestSavePlanRejectsBadTierOrTrigger(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	day, _ := svc.StartDay(ctx, "2026-10-05", 3, "")
	bad := []domain.Plan{
		{DayID: day.ID, Trigger: "whim"},
		{DayID: day.ID, Trigger: domain.TriggerEdit, Entries: []domain.PlanEntry{{TaskID: 1, Tier: "maybe"}}},
	}
	for _, p := range bad {
		if _, err := svc.SavePlan(ctx, p); !errors.Is(err, service.ErrInvalid) {
			t.Errorf("%+v: err = %v, want ErrInvalid", p, err)
		}
	}
}
```

`internal/service/push_test.go`. Create it with:

```go
package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestPushCountsLeaveOutUndonePushesAndOtherEdits(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "write the RFC")
	other := createTask(t, svc, "water plants")

	for _, until := range []domain.Date{"2026-10-06", "2026-10-07"} {
		if _, err := svc.PushTask(ctx, newBatch(t, svc, domain.ActorUser), task.ID, until); err != nil {
			t.Fatal(err)
		}
	}
	undone := newBatch(t, svc, domain.ActorUser)
	pushed, err := svc.PushTask(ctx, undone, task.ID, "2026-10-08")
	if err != nil {
		t.Fatal(err)
	}
	if pushed.NotBefore == nil || *pushed.NotBefore != "2026-10-08" {
		t.Errorf("not_before = %v", pushed.NotBefore)
	}
	if err := svc.Undo(ctx, undone); err != nil {
		t.Fatal(err)
	}
	// An ordinary edit that happens to set not_before is not a push.
	if _, err := svc.UpdateTask(ctx, newBatch(t, svc, domain.ActorUser), other.ID,
		service.TaskFields{Title: "water plants", NotBefore: ptr(domain.Date("2026-10-09"))}); err != nil {
		t.Fatal(err)
	}

	counts, err := svc.PushCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 || counts[task.ID] != 2 {
		t.Errorf("counts = %v, want {%d: 2}", counts, task.ID)
	}
	if got, _ := svc.GetTask(ctx, task.ID); *got.NotBefore != "2026-10-07" {
		t.Errorf("undo left not_before at %v", *got.NotBefore)
	}
}

func TestPushRejectsABadDate(t *testing.T) {
	svc, _ := newService(t)
	task := createTask(t, svc, "x")
	if _, err := svc.PushTask(context.Background(), newBatch(t, svc, domain.ActorUser), task.ID, "tomorrow"); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestAppendFeeling(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "review FLEX1-420")

	got, err := svc.AppendFeeling(ctx, newBatch(t, svc, domain.ActorUser), task.ID, " not before coffee ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Feeling != "not before coffee" {
		t.Errorf("feeling = %q", got.Feeling)
	}
	got, _ = svc.AppendFeeling(ctx, newBatch(t, svc, domain.ActorUser), task.ID, "too fried")
	if got.Feeling != "not before coffee; too fried" {
		t.Errorf("feeling = %q", got.Feeling)
	}
	if _, err := svc.AppendFeeling(ctx, newBatch(t, svc, domain.ActorUser), task.ID, "  "); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("empty note: err = %v, want ErrInvalid", err)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/service/`
Expected: build fails: `svc.StartDay undefined`

- [ ] **Step 3: Implement**

`internal/store/migrations/0003_days.sql`. Create it with:

```sql
CREATE TABLE days (
    id INTEGER PRIMARY KEY,
    date TEXT NOT NULL UNIQUE,
    pace INTEGER NOT NULL,
    mood_note TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    wrapped_at TEXT
);

-- Plans are versioned history, not activity: a replan adds a version and never edits one.
CREATE TABLE plans (
    id INTEGER PRIMARY KEY,
    day_id INTEGER NOT NULL REFERENCES days(id),
    version INTEGER NOT NULL,
    triggered_by TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    opening TEXT NOT NULL,
    entries TEXT NOT NULL,
    fallback INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (day_id, version)
);
```

`internal/domain/domain.go`. Apply:

```diff
--- a/internal/domain/domain.go
+++ b/internal/domain/domain.go
@@ -107,3 +107,71 @@ type Activity struct {
 	After     json.RawMessage `json:"after"`
 	CreatedAt time.Time       `json:"created_at"`
 }
+
+// Pace is how hard the user wants to push today, from 1 to 5. The labels can change; there are
+// always five levels.
+type Pace int
+
+func (p Pace) Valid() bool { return p >= 1 && p <= 5 }
+
+var paceLabels = [...]string{"running on fumes", "gentle", "steady", "full steam", "go hard"}
+
+func (p Pace) Label() string {
+	if !p.Valid() {
+		return ""
+	}
+	return paceLabels[p-1]
+}
+
+type Day struct {
+	ID        int64      `json:"id"`
+	Date      Date       `json:"date"`
+	Pace      Pace       `json:"pace"`
+	MoodNote  string     `json:"mood_note"`
+	StartedAt time.Time  `json:"started_at"`
+	WrappedAt *time.Time `json:"wrapped_at"`
+}
+
+type Tier string
+
+const (
+	TierMust   Tier = "must"
+	TierShould Tier = "should"
+	TierCould  Tier = "could"
+)
+
+func (t Tier) Valid() bool { return t == TierMust || t == TierShould || t == TierCould }
+
+// Trigger is what caused a plan to be made.
+type Trigger string
+
+const (
+	TriggerMorning Trigger = "morning"
+	TriggerPace    Trigger = "pace"
+	TriggerEdit    Trigger = "edit"
+	TriggerAgent   Trigger = "agent"
+)
+
+func (t Trigger) Valid() bool {
+	return t == TriggerMorning || t == TriggerPace || t == TriggerEdit || t == TriggerAgent
+}
+
+type PlanEntry struct {
+	TaskID    int64  `json:"task_id"`
+	Tier      Tier   `json:"tier"`
+	Rationale string `json:"rationale"`
+}
+
+// Plan is one version of a day's plan. Entries are in order within each tier. Fallback means
+// code built the plan because the model couldn't.
+type Plan struct {
+	ID        int64       `json:"id"`
+	DayID     int64       `json:"day_id"`
+	Version   int         `json:"version"`
+	Trigger   Trigger     `json:"trigger"`
+	Reason    string      `json:"reason"`
+	Opening   string      `json:"opening"`
+	Entries   []PlanEntry `json:"entries"`
+	Fallback  bool        `json:"fallback"`
+	CreatedAt time.Time   `json:"created_at"`
+}
```

`internal/service/service.go`. Apply:

```diff
--- a/internal/service/service.go
+++ b/internal/service/service.go
@@ -1,4 +1,4 @@
-// Package service is the only write path for tasks, blockers, and events. Every write belongs to a
+// Package service is the only write path for tasks, blockers, events, and days. Every write belongs to a
 // batch and records an activity row with before and after snapshots, which is what undo replays.
 package service
 
@@ -23,6 +23,7 @@ const (
 	entityTask    = "task"
 	entityBlocker = "blocker"
 	entityEvent   = "event"
+	entityDay     = "day"
 
 	actionCreate = "create"
 	actionUpdate = "update"
@@ -30,6 +31,7 @@ const (
 	actionDelete = "delete"
 	actionAdd    = "add"
 	actionRemove = "remove"
+	actionPush   = "push"
 )
 
 type Service struct {
```

`internal/service/undo.go`. Apply:

```diff
--- a/internal/service/undo.go
+++ b/internal/service/undo.go
@@ -70,6 +70,12 @@ func revert(ctx context.Context, tx *sql.Tx, a domain.Activity, now time.Time) e
 			return err
 		}
 		return writeEvent(ctx, tx, e)
+	case entityDay:
+		var d domain.Day
+		if err := json.Unmarshal(a.Before, &d); err != nil {
+			return err
+		}
+		return writeDay(ctx, tx, d)
 	case entityBlocker:
 		if a.Before == nil {
 			var b domain.Blocker
```

`internal/service/days.go`. Create it with:

```go
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jameynakama/whatnow/internal/domain"
)

// StartDay begins a day with the user's pace. Starting a day isn't in a batch: undoing it would
// throw away the day's plans, and nothing about it needs taking back.
func (s *Service) StartDay(ctx context.Context, date domain.Date, pace domain.Pace, moodNote string) (domain.Day, error) {
	if !date.Valid() {
		return domain.Day{}, fmt.Errorf("%w: date %q", ErrInvalid, date)
	}
	if !pace.Valid() {
		return domain.Day{}, fmt.Errorf("%w: pace %d", ErrInvalid, pace)
	}
	d := domain.Day{Date: date, Pace: pace, MoodNote: moodNote, StartedAt: s.now().UTC()}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := dayByDate(ctx, tx, date); err == nil {
			return fmt.Errorf("%w: %s has already started", ErrConflict, date)
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO days (date, pace, mood_note, started_at) VALUES (?, ?, ?, ?)`,
			d.Date, d.Pace, d.MoodNote, formatTime(d.StartedAt))
		if err != nil {
			return err
		}
		d.ID, err = res.LastInsertId()
		return err
	})
	return d, err
}

func (s *Service) GetDay(ctx context.Context, date domain.Date) (domain.Day, error) {
	return dayByDate(ctx, s.db, date)
}

// SetPace changes a started day's pace. An empty note leaves the mood note as it was.
func (s *Service) SetPace(ctx context.Context, batch int64, date domain.Date, pace domain.Pace, note string) (domain.Day, error) {
	if !pace.Valid() {
		return domain.Day{}, fmt.Errorf("%w: pace %d", ErrInvalid, pace)
	}
	var d domain.Day
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		before, err := dayByDate(ctx, tx, date)
		if err != nil {
			return err
		}
		d = before
		d.Pace = pace
		if note != "" {
			d.MoodNote = note
		}
		if err := writeDay(ctx, tx, d); err != nil {
			return err
		}
		return record(ctx, tx, batch, actionUpdate, entityDay, d.ID, before, d)
	})
	return d, err
}

const dayColumns = `id, date, pace, mood_note, started_at, wrapped_at`

func dayByDate(ctx context.Context, q querier, date domain.Date) (domain.Day, error) {
	d, err := scanDay(q.QueryRowContext(ctx, `SELECT `+dayColumns+` FROM days WHERE date = ?`, date))
	if errors.Is(err, sql.ErrNoRows) {
		return d, fmt.Errorf("%w: day %s", ErrNotFound, date)
	}
	return d, err
}

func scanDay(row scanner) (domain.Day, error) {
	var d domain.Day
	var startedAt string
	var wrappedAt sql.NullString
	if err := row.Scan(&d.ID, &d.Date, &d.Pace, &d.MoodNote, &startedAt, &wrappedAt); err != nil {
		return d, err
	}
	var err error
	if d.StartedAt, err = parseTime(startedAt); err != nil {
		return d, err
	}
	d.WrappedAt, err = parseNullTime(wrappedAt)
	return d, err
}

// writeDay overwrites a day's editable columns. Undo uses it to restore a snapshot.
func writeDay(ctx context.Context, tx *sql.Tx, d domain.Day) error {
	_, err := tx.ExecContext(ctx, `UPDATE days SET pace = ?, mood_note = ?, wrapped_at = ? WHERE id = ?`,
		d.Pace, d.MoodNote, formatNullTime(d.WrappedAt), d.ID)
	return err
}

// SavePlan stores a new version of a day's plan and returns it with its ID and version.
func (s *Service) SavePlan(ctx context.Context, p domain.Plan) (domain.Plan, error) {
	if !p.Trigger.Valid() {
		return p, fmt.Errorf("%w: trigger %q", ErrInvalid, p.Trigger)
	}
	if p.Entries == nil {
		p.Entries = []domain.PlanEntry{}
	}
	for _, e := range p.Entries {
		if !e.Tier.Valid() {
			return p, fmt.Errorf("%w: tier %q", ErrInvalid, e.Tier)
		}
	}
	entries, err := json.Marshal(p.Entries)
	if err != nil {
		return p, err
	}
	p.CreatedAt = s.now().UTC()
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM plans WHERE day_id = ?`, p.DayID).Scan(&p.Version); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO plans (day_id, version, triggered_by, reason, opening, entries, fallback, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			p.DayID, p.Version, p.Trigger, p.Reason, p.Opening, string(entries), p.Fallback, formatTime(p.CreatedAt))
		if err != nil {
			return err
		}
		p.ID, err = res.LastInsertId()
		return err
	})
	return p, err
}

// LatestPlan returns the newest version of a day's plan.
func (s *Service) LatestPlan(ctx context.Context, dayID int64) (domain.Plan, error) {
	var p domain.Plan
	var entries, createdAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, day_id, version, triggered_by, reason, opening, entries, fallback, created_at
		 FROM plans WHERE day_id = ? ORDER BY version DESC LIMIT 1`, dayID).
		Scan(&p.ID, &p.DayID, &p.Version, &p.Trigger, &p.Reason, &p.Opening, &entries, &p.Fallback, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("%w: no plan for day %d", ErrNotFound, dayID)
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(entries), &p.Entries); err != nil {
		return p, err
	}
	p.CreatedAt, err = parseTime(createdAt)
	return p, err
}
```

`internal/service/tasks.go`. Apply:

```diff
--- a/internal/service/tasks.go
+++ b/internal/service/tasks.go
@@ -111,6 +111,51 @@ func (s *Service) SetTaskStatus(ctx context.Context, batch, id int64, status dom
 	return s.changeTask(ctx, batch, id, actionStatus, func(t *domain.Task) { t.Status = status })
 }
 
+// PushTask puts a task off until a date by setting its not_before. Pushes are counted apart from
+// other edits, because a task pushed again and again says something about it.
+func (s *Service) PushTask(ctx context.Context, batch, id int64, until domain.Date) (domain.Task, error) {
+	if !until.Valid() {
+		return domain.Task{}, fmt.Errorf("%w: until %q", ErrInvalid, until)
+	}
+	return s.changeTask(ctx, batch, id, actionPush, func(t *domain.Task) { t.NotBefore = &until })
+}
+
+// PushCounts returns how many times each task has been pushed, leaving out undone pushes.
+func (s *Service) PushCounts(ctx context.Context) (map[int64]int, error) {
+	rows, err := s.db.QueryContext(ctx,
+		`SELECT a.entity_id, COUNT(*) FROM activity a JOIN batches b ON b.id = a.batch_id
+		 WHERE a.entity = ? AND a.action = ? AND b.undone_at IS NULL GROUP BY a.entity_id`, entityTask, actionPush)
+	if err != nil {
+		return nil, err
+	}
+	defer rows.Close()
+	counts := map[int64]int{}
+	for rows.Next() {
+		var id int64
+		var n int
+		if err := rows.Scan(&id, &n); err != nil {
+			return nil, err
+		}
+		counts[id] = n
+	}
+	return counts, rows.Err()
+}
+
+// AppendFeeling adds a note to the end of a task's feeling.
+func (s *Service) AppendFeeling(ctx context.Context, batch, id int64, note string) (domain.Task, error) {
+	note = strings.TrimSpace(note)
+	if note == "" {
+		return domain.Task{}, fmt.Errorf("%w: note is empty", ErrInvalid)
+	}
+	return s.changeTask(ctx, batch, id, actionUpdate, func(t *domain.Task) {
+		if t.Feeling == "" {
+			t.Feeling = note
+		} else {
+			t.Feeling += "; " + note
+		}
+	})
+}
+
 // DeleteTask soft-deletes a task. Dropping (a status) means "not doing it"; deleting means
 // "this shouldn't exist", and deleted tasks are hidden everywhere.
 func (s *Service) DeleteTask(ctx context.Context, batch, id int64) error {
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/service/`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/domain.go internal/service/days.go internal/service/days_test.go internal/service/push_test.go internal/service/service.go internal/service/tasks.go internal/service/undo.go internal/store/migrations/0003_days.sql
git commit -m "Add days, versioned plans, and task pushes"
```


### Task 5: Facts

The pure core of planning. `Compute` takes everything as input and does no I/O, which is what lets the DST, midnight, and blocker-chain cases be tested exhaustively. Eligible tasks come back already in fallback order, so the fallback plan and fallback pick need no sorting of their own.

**Files:**
- Create: `internal/facts/facts.go`
- Create: `internal/facts/facts_test.go`

**Interfaces:**
- Consumes: `domain.Task`, `domain.Blocker`, `domain.Event`, `domain.Date`.
- Produces:
  - `facts.Input{Now, Loc, EndOfDay string, Tasks, Blockers, Events, PushCounts map[int64]int, Musts map[int64]bool}`
  - `facts.Facts{Now, Today domain.Date, DayEnd, Events, Gaps []Gap, Tasks []TaskFacts}` with `(Facts).Task(id) (TaskFacts, bool)`
  - `facts.TaskFacts{Task, BlockedBy []int64, Annotations []string}`, `facts.Gap{Start, End}` with `Duration()`
  - `facts.Compute(Input) Facts`, `facts.Today(now, loc) domain.Date`, `facts.Eligible(task, today) bool`, `facts.Duration(d) string`, `facts.PointEventLength`

- [ ] **Step 1: Write the failing tests**

`internal/facts/facts_test.go`. Create it with:

```go
package facts_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
)

// loc is UTC-5, so local and UTC times differ in every case.
var loc = time.FixedZone("UTC-5", -5*3600)

func at(day, hour, min int) time.Time { return time.Date(2026, 10, day, hour, min, 0, 0, loc) }

func event(title string, start time.Time, length time.Duration) domain.Event {
	e := domain.Event{Title: title, StartsAt: start}
	if length > 0 {
		end := start.Add(length)
		e.EndsAt = &end
	}
	return e
}

type span struct{ start, end time.Time }

func spans(gs []facts.Gap) []span {
	out := []span{}
	for _, g := range gs {
		out = append(out, span{g.Start, g.End})
	}
	return out
}

func TestGaps(t *testing.T) {
	cases := []struct {
		name   string
		now    time.Time
		end    string
		events []domain.Event
		want   []span
	}{
		{"no events", at(5, 9, 0), "17:30", nil, []span{{at(5, 9, 0), at(5, 17, 30)}}},
		{"one meeting", at(5, 9, 0), "17:30", []domain.Event{event("standup", at(5, 10, 0), 15*time.Minute)},
			[]span{{at(5, 9, 0), at(5, 10, 0)}, {at(5, 10, 15), at(5, 17, 30)}}},
		{"back to back leaves no gap between", at(5, 9, 0), "17:30", []domain.Event{
			event("a", at(5, 10, 0), time.Hour), event("b", at(5, 11, 0), time.Hour)},
			[]span{{at(5, 9, 0), at(5, 10, 0)}, {at(5, 12, 0), at(5, 17, 30)}}},
		{"overlapping events merge", at(5, 9, 0), "17:30", []domain.Event{
			event("long", at(5, 10, 0), 2*time.Hour), event("inside", at(5, 10, 30), 30*time.Minute)},
			[]span{{at(5, 9, 0), at(5, 10, 0)}, {at(5, 12, 0), at(5, 17, 30)}}},
		{"event without an end takes 30 minutes", at(5, 9, 0), "17:30", []domain.Event{event("dentist", at(5, 15, 0), 0)},
			[]span{{at(5, 9, 0), at(5, 15, 0)}, {at(5, 15, 30), at(5, 17, 30)}}},
		{"now inside a meeting", at(5, 10, 30), "17:30", []domain.Event{event("standup", at(5, 10, 0), time.Hour)},
			[]span{{at(5, 11, 0), at(5, 17, 30)}}},
		{"past events don't matter", at(5, 13, 0), "17:30", []domain.Event{event("standup", at(5, 10, 0), time.Hour)},
			[]span{{at(5, 13, 0), at(5, 17, 30)}}},
		{"event running past the end of the day", at(5, 16, 0), "17:30", []domain.Event{event("late", at(5, 17, 0), 2*time.Hour)},
			[]span{{at(5, 16, 0), at(5, 17, 0)}}},
		{"event after the end of the day", at(5, 16, 0), "17:30", []domain.Event{event("dinner", at(5, 19, 0), time.Hour)},
			[]span{{at(5, 16, 0), at(5, 17, 30)}}},
		{"overnight event from yesterday", at(5, 0, 30), "17:30", []domain.Event{event("on call", at(4, 22, 0), 4*time.Hour)},
			[]span{{at(5, 2, 0), at(5, 17, 30)}}},
		{"after the end of the day", at(5, 18, 0), "17:30", nil, []span{}},
		{"unreadable end of day means midnight", at(5, 22, 0), "", nil, []span{{at(5, 22, 0), at(6, 0, 0)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := facts.Compute(facts.Input{Now: c.now, Loc: loc, EndOfDay: c.end, Events: c.events})
			if diff := cmp.Diff(c.want, spans(f.Gaps), cmp.AllowUnexported(span{})); diff != "" {
				t.Errorf("gaps (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGapsAcrossADSTChange(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database")
	}
	// Clocks fall back at 02:00 on 2026-11-01, so midnight to 03:00 local is four real hours.
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	f := facts.Compute(facts.Input{Now: now, Loc: ny, EndOfDay: "03:00"})
	if len(f.Gaps) != 1 || f.Gaps[0].Duration() != 4*time.Hour {
		t.Errorf("gaps = %v, want one 4h gap", f.Gaps)
	}
	if f.Today != "2026-11-01" {
		t.Errorf("today = %s", f.Today)
	}
}

func TestTodayIsTheLocalDate(t *testing.T) {
	// 02:00 UTC on the 6th is still the evening of the 5th at UTC-5.
	if got := facts.Today(time.Date(2026, 10, 6, 2, 0, 0, 0, time.UTC), loc); got != "2026-10-05" {
		t.Errorf("today = %s", got)
	}
}

func date(d string) *domain.Date      { v := domain.Date(d); return &v }
func size(s domain.Size) *domain.Size { return &s }

func task(id int64, title string, opts ...func(*domain.Task)) domain.Task {
	t := domain.Task{ID: id, Title: title, Status: domain.StatusOpen, CreatedAt: at(1, 9, int(id))}
	for _, o := range opts {
		o(&t)
	}
	return t
}

func due(d string) func(*domain.Task)           { return func(t *domain.Task) { t.DueOn = date(d) } }
func notBefore(d string) func(*domain.Task)     { return func(t *domain.Task) { t.NotBefore = date(d) } }
func status(s domain.Status) func(*domain.Task) { return func(t *domain.Task) { t.Status = s } }
func sized(s domain.Size) func(*domain.Task)    { return func(t *domain.Task) { t.Size = size(s) } }
func deleted() func(*domain.Task)               { return func(t *domain.Task) { d := at(2, 0, 0); t.DeletedAt = &d } }
func ids(f facts.Facts) []int64 {
	out := []int64{}
	for _, t := range f.Tasks {
		out = append(out, t.Task.ID)
	}
	return out
}

func TestEligibleTasksInFallbackOrder(t *testing.T) {
	tasks := []domain.Task{
		task(1, "undated older"),
		task(2, "due later", due("2026-10-09")),
		task(3, "done", status(domain.StatusDone)),
		task(4, "dropped", status(domain.StatusDropped)),
		task(5, "not before tomorrow", notBefore("2026-10-06")),
		task(6, "not before today", notBefore("2026-10-05")),
		task(7, "overdue", due("2026-10-01")),
		task(8, "due today", due("2026-10-05")),
		task(9, "deleted", deleted()),
	}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks})
	if diff := cmp.Diff([]int64{7, 8, 2, 1, 6}, ids(f)); diff != "" {
		t.Errorf("eligible order (-want +got):\n%s", diff)
	}
}

func annotations(t *testing.T, f facts.Facts, id int64) []string {
	t.Helper()
	tf, ok := f.Task(id)
	if !ok {
		t.Fatalf("task %d is not eligible", id)
	}
	return tf.Annotations
}

func TestDueAnnotations(t *testing.T) {
	tasks := []domain.Task{
		task(1, "a", due("2026-10-03")),
		task(2, "b", due("2026-10-05")),
		task(3, "c", due("2026-10-06")),
		task(4, "d", due("2026-10-09")),
	}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks})
	want := map[int64]string{1: "overdue, was due 2026-10-03", 2: "due today", 3: "due tomorrow", 4: "due 2026-10-09, in 4 days"}
	for id, w := range want {
		if got := annotations(t, f, id); len(got) != 1 || got[0] != w {
			t.Errorf("task %d: %v, want [%s]", id, got, w)
		}
	}
}

func TestBlockerChains(t *testing.T) {
	// 1 waits on 2, which waits on 3. 4 waited on 5, which is done. 1 is due today; 6 is a must.
	tasks := []domain.Task{
		task(1, "ship FLEX1-666", due("2026-10-05")),
		task(2, "write the design"),
		task(3, "research pubsub"),
		task(4, "deploy"),
		task(5, "get approval", status(domain.StatusDone)),
		task(6, "demo prep"),
		task(7, "book the room"),
	}
	blockers := []domain.Blocker{{TaskID: 1, BlockedByID: 2}, {TaskID: 2, BlockedByID: 3}, {TaskID: 4, BlockedByID: 5},
		{TaskID: 6, BlockedByID: 7}}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks, Blockers: blockers,
		Musts: map[int64]bool{6: true}})

	cases := map[int64][]string{
		1: {"due today", `blocked by #2 "write the design"`},
		2: {`blocked by #3 "research pubsub"`, `blocks #1 "ship FLEX1-666", which is due today`},
		3: {`blocks #1 "ship FLEX1-666", which is due today`},
		4: nil, // its only blocker is done
		7: {`blocks #6 "demo prep", which is a must`},
	}
	for id, want := range cases {
		if diff := cmp.Diff(want, annotations(t, f, id)); diff != "" {
			t.Errorf("task %d (-want +got):\n%s", id, diff)
		}
	}
	if tf, _ := f.Task(4); len(tf.BlockedBy) != 0 {
		t.Errorf("task 4 blocked by %v", tf.BlockedBy)
	}
}

func TestABlockerThatIsNotYetEligibleStillBlocks(t *testing.T) {
	tasks := []domain.Task{task(1, "launch"), task(2, "vendor reply", notBefore("2026-10-08"))}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks,
		Blockers: []domain.Blocker{{TaskID: 1, BlockedByID: 2}}})
	if tf, _ := f.Task(1); len(tf.BlockedBy) != 1 {
		t.Errorf("task 1 should wait on task 2: %+v", tf)
	}
}

func TestPushAnnotations(t *testing.T) {
	tasks := []domain.Task{task(1, "a"), task(2, "b"), task(3, "c")}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks,
		PushCounts: map[int64]int{1: 1, 2: 4}})
	for id, want := range map[int64][]string{1: {"pushed once"}, 2: {"pushed 4 times"}, 3: nil} {
		if diff := cmp.Diff(want, annotations(t, f, id)); diff != "" {
			t.Errorf("task %d (-want +got):\n%s", id, diff)
		}
	}
}

func TestTooBigForTheGaps(t *testing.T) {
	tasks := []domain.Task{task(1, "small", sized(domain.SizeS)), task(2, "medium", sized(domain.SizeM)),
		task(3, "large", sized(domain.SizeL))}
	meetings := []domain.Event{event("a", at(5, 10, 0), 4*time.Hour), event("b", at(5, 15, 0), 2*time.Hour)}
	// Free: 09:00-10:00, 14:00-15:00, 17:00-17:30. The longest gap is 1h.
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks, Events: meetings})
	for id, want := range map[int64][]string{1: nil, 2: nil, 3: {"too big for any free gap today (the longest is 1h)"}} {
		if diff := cmp.Diff(want, annotations(t, f, id)); diff != "" {
			t.Errorf("task %d (-want +got):\n%s", id, diff)
		}
	}

	// With no free time left at all, nothing is called too big: the day is simply over.
	f = facts.Compute(facts.Input{Now: at(5, 18, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks})
	if got := annotations(t, f, 3); got != nil {
		t.Errorf("after hours: %v", got)
	}
}

func TestTodaysEventsOnly(t *testing.T) {
	events := []domain.Event{
		event("tomorrow", at(6, 9, 0), time.Hour),
		event("standup", at(5, 10, 0), 15*time.Minute),
		event("yesterday", at(4, 9, 0), time.Hour),
		event("early", at(5, 8, 0), time.Hour),
	}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Events: events})
	var got []string
	for _, e := range f.Events {
		got = append(got, e.Title)
	}
	if diff := cmp.Diff([]string{"early", "standup"}, got); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{45 * time.Minute: "45m", 2 * time.Hour: "2h", 90 * time.Minute: "1h 30m", 0: "0m"} {
		if got := facts.Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/facts/`
Expected: build fails: the `facts` package doesn't exist yet

- [ ] **Step 3: Implement**

`internal/facts/facts.go`. Create it with:

```go
// Package facts works out what is true about the user's day: free time, which tasks are eligible
// and blocked, and notes such as "due today" or "pushed 4 times". Facts reach the model as
// annotations, never as filters. The package does no I/O.
package facts

import (
	"fmt"
	"sort"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
)

// Input is what facts are computed from. Tasks, Blockers, and Events must leave out anything
// deleted. Events may include ones outside today; only today's count.
type Input struct {
	Now      time.Time
	Loc      *time.Location
	EndOfDay string // local HH:MM
	Tasks    []domain.Task
	Blockers []domain.Blocker
	Events   []domain.Event
	// PushCounts maps a task ID to how many times it was pushed.
	PushCounts map[int64]int
	// Musts are the current plan's must tier. A must raises the urgency of whatever blocks it.
	Musts map[int64]bool
}

type Gap struct {
	Start time.Time
	End   time.Time
}

func (g Gap) Duration() time.Duration { return g.End.Sub(g.Start) }

type TaskFacts struct {
	Task domain.Task
	// BlockedBy lists the open tasks this one waits on.
	BlockedBy   []int64
	Annotations []string
}

type Facts struct {
	Now    time.Time // in the user's time zone
	Today  domain.Date
	DayEnd time.Time
	// Events are today's, earliest first.
	Events []domain.Event
	// Gaps are the free stretches between now and the end of the day.
	Gaps []Gap
	// Tasks are the eligible tasks: dated ones by due date, then undated ones, each oldest first.
	Tasks []TaskFacts
}

// Task returns an eligible task's facts.
func (f Facts) Task(id int64) (TaskFacts, bool) {
	for _, t := range f.Tasks {
		if t.Task.ID == id {
			return t, true
		}
	}
	return TaskFacts{}, false
}

// PointEventLength is how long an event without an end is taken to last.
const PointEventLength = 30 * time.Minute

// minGap is the shortest free gap a task of each size fits in. An S task fits anywhere.
var minGap = map[domain.Size]time.Duration{domain.SizeM: time.Hour, domain.SizeL: 3 * time.Hour}

// Today returns the date it is in loc.
func Today(now time.Time, loc *time.Location) domain.Date {
	return domain.Date(now.In(loc).Format(time.DateOnly))
}

// Eligible reports whether a task can go in a plan: open, not deleted, and not put off until a
// later date.
func Eligible(t domain.Task, today domain.Date) bool {
	return t.Status == domain.StatusOpen && t.DeletedAt == nil && (t.NotBefore == nil || *t.NotBefore <= today)
}

func Compute(in Input) Facts {
	now := in.Now.In(in.Loc)
	f := Facts{Now: now, Today: Today(now, in.Loc)}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, in.Loc)
	f.DayEnd = dayEnd(dayStart, in.EndOfDay, in.Loc)
	f.Events = todays(in.Events, dayStart, dayStart.AddDate(0, 0, 1))
	f.Gaps = gaps(now, f.DayEnd, f.Events)

	byID := map[int64]domain.Task{}
	for _, t := range in.Tasks {
		byID[t.ID] = t
	}
	open := func(id int64) bool { t, ok := byID[id]; return ok && t.Status == domain.StatusOpen }
	waitsOn := map[int64][]int64{}  // task -> open tasks it waits on
	waitedBy := map[int64][]int64{} // task -> open tasks waiting on it
	for _, b := range in.Blockers {
		if open(b.TaskID) && open(b.BlockedByID) {
			waitsOn[b.TaskID] = append(waitsOn[b.TaskID], b.BlockedByID)
			waitedBy[b.BlockedByID] = append(waitedBy[b.BlockedByID], b.TaskID)
		}
	}
	urgent := map[int64]string{}
	for _, t := range in.Tasks {
		switch {
		case t.Status != domain.StatusOpen:
		case in.Musts[t.ID]:
			urgent[t.ID] = "a must"
		case t.DueOn != nil && *t.DueOn < f.Today:
			urgent[t.ID] = "overdue"
		case t.DueOn != nil && *t.DueOn == f.Today:
			urgent[t.ID] = "due today"
		}
	}
	var longest time.Duration
	for _, g := range f.Gaps {
		longest = max(longest, g.Duration())
	}

	for _, t := range in.Tasks {
		if !Eligible(t, f.Today) {
			continue
		}
		tf := TaskFacts{Task: t, BlockedBy: waitsOn[t.ID]}
		if t.DueOn != nil {
			tf.Annotations = append(tf.Annotations, due(*t.DueOn, f.Today))
		}
		for _, id := range tf.BlockedBy {
			tf.Annotations = append(tf.Annotations, fmt.Sprintf("blocked by #%d %q", id, byID[id].Title))
		}
		for _, id := range reachable(t.ID, waitedBy) {
			if why, ok := urgent[id]; ok {
				tf.Annotations = append(tf.Annotations, fmt.Sprintf("blocks #%d %q, which is %s", id, byID[id].Title, why))
			}
		}
		switch n := in.PushCounts[t.ID]; {
		case n == 1:
			tf.Annotations = append(tf.Annotations, "pushed once")
		case n > 1:
			tf.Annotations = append(tf.Annotations, fmt.Sprintf("pushed %d times", n))
		}
		if t.Size != nil && len(f.Gaps) > 0 && longest < minGap[*t.Size] {
			tf.Annotations = append(tf.Annotations, fmt.Sprintf("too big for any free gap today (the longest is %s)", Duration(longest)))
		}
		f.Tasks = append(f.Tasks, tf)
	}
	sort.SliceStable(f.Tasks, func(i, j int) bool { return fallbackLess(f.Tasks[i].Task, f.Tasks[j].Task) })
	return f
}

// fallbackLess orders dated tasks by due date, then undated ones, each oldest first.
func fallbackLess(a, b domain.Task) bool {
	if (a.DueOn == nil) != (b.DueOn == nil) {
		return a.DueOn != nil
	}
	if a.DueOn != nil && *a.DueOn != *b.DueOn {
		return *a.DueOn < *b.DueOn
	}
	return a.CreatedAt.Before(b.CreatedAt) || (a.CreatedAt.Equal(b.CreatedAt) && a.ID < b.ID)
}

// dayEnd is the end of the working day. An unreadable setting means midnight.
func dayEnd(dayStart time.Time, clock string, loc *time.Location) time.Time {
	c, err := time.Parse("15:04", clock)
	if err != nil {
		return dayStart.AddDate(0, 0, 1)
	}
	return time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), c.Hour(), c.Minute(), 0, 0, loc)
}

func end(e domain.Event) time.Time {
	if e.EndsAt != nil {
		return *e.EndsAt
	}
	return e.StartsAt.Add(PointEventLength)
}

// todays returns the events that overlap [from, to), earliest first.
func todays(events []domain.Event, from, to time.Time) []domain.Event {
	out := []domain.Event{}
	for _, e := range events {
		if e.StartsAt.Before(to) && end(e).After(from) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartsAt.Before(out[j].StartsAt) })
	return out
}

// gaps returns the free stretches of [from, to) that no event covers.
func gaps(from, to time.Time, events []domain.Event) []Gap {
	out := []Gap{}
	cursor := from
	for _, e := range events {
		if !cursor.Before(to) {
			break
		}
		if e.StartsAt.After(cursor) {
			out = append(out, Gap{Start: cursor, End: minTime(e.StartsAt, to)})
		}
		if end(e).After(cursor) {
			cursor = end(e)
		}
	}
	if cursor.Before(to) {
		out = append(out, Gap{Start: cursor, End: to})
	}
	return out
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// reachable returns every task that waits on id, directly or through others.
func reachable(id int64, waitedBy map[int64][]int64) []int64 {
	var out []int64
	seen := map[int64]bool{id: true}
	stack := append([]int64(nil), waitedBy[id]...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		stack = append(stack, waitedBy[n]...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func due(on, today domain.Date) string {
	days := daysBetween(today, on)
	switch {
	case days < 0:
		return fmt.Sprintf("overdue, was due %s", on)
	case days == 0:
		return "due today"
	case days == 1:
		return "due tomorrow"
	}
	return fmt.Sprintf("due %s, in %d days", on, days)
}

// daysBetween counts calendar days, so a DST change can't make it off by one.
func daysBetween(from, to domain.Date) int {
	a, _ := time.Parse(time.DateOnly, string(from))
	b, _ := time.Parse(time.DateOnly, string(to))
	return int(b.Sub(a).Hours() / 24)
}

// Duration formats a duration as "45m", "2h", or "1h 30m".
func Duration(d time.Duration) string {
	d = d.Round(time.Minute)
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/facts/`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/facts/facts.go internal/facts/facts_test.go
git commit -m "Add facts"
```


### Task 6: Facts as prompt context

The prompt package renders facts, as the spec gives it that job. Times are shown in the user's zone, and annotations sit indented under their task.

**Files:**
- Create: `internal/prompt/facts.go`
- Create: `internal/prompt/facts_test.go`

**Interfaces:**
- Produces: `prompt.Facts(facts.Facts) string`.

- [ ] **Step 1: Write the failing tests**

`internal/prompt/facts_test.go`. Create it with:

```go
package prompt_test

import (
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	"github.com/jameynakama/whatnow/internal/prompt"
)

func TestFactsRenderInLocalTime(t *testing.T) {
	loc := time.FixedZone("EST", -5*3600)
	at := func(h, m int) time.Time { return time.Date(2026, 10, 5, h, m, 0, 0, loc) }
	standupEnd := at(10, 15).UTC()
	size, energy := domain.SizeL, domain.EnergyHigh
	f := facts.Facts{
		Now:    at(9, 0),
		DayEnd: at(17, 30),
		Gaps:   []facts.Gap{{Start: at(9, 0), End: at(10, 0)}, {Start: at(10, 15), End: at(17, 30)}},
		Events: []domain.Event{
			{Title: "standup", StartsAt: at(10, 0).UTC(), EndsAt: &standupEnd},
			{Title: "dentist", StartsAt: at(15, 0).UTC()},
		},
		Tasks: []facts.TaskFacts{
			{Task: domain.Task{ID: 3, Title: "Do FLEX1-666", Size: &size, Energy: &energy, Feeling: "dreading it"},
				Annotations: []string{"due today", `blocked by #4 "Research pubsub"`}},
			{Task: domain.Task{ID: 9, Title: "water plants"}},
		},
	}
	want := `Now: Monday 2026-10-05 09:00 (EST). The working day ends at 17:30.
Free time left today: 09:00-10:00 (1h), 10:15-17:30 (7h 15m)

Today's events:
- 10:00-10:15 standup
- 15:00 dentist

Eligible tasks, as #<id> title:
#3 Do FLEX1-666 [size L, high energy] feeling: "dreading it"
  - due today
  - blocked by #4 "Research pubsub"
#9 water plants
`
	if got := prompt.Facts(f); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFactsRenderAnEmptyDay(t *testing.T) {
	now := time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC)
	got := prompt.Facts(facts.Facts{Now: now, DayEnd: now})
	want := `Now: Monday 2026-10-05 18:00 (UTC). The working day ends at 18:00.
Free time left today: none

Today's events:
(none)

Eligible tasks, as #<id> title:
(none)
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/prompt/`
Expected: build fails: `undefined: prompt.Facts`

- [ ] **Step 3: Implement**

`internal/prompt/facts.go`. Create it with:

```go
package prompt

import (
	"fmt"
	"strings"

	"github.com/jameynakama/whatnow/internal/facts"
)

// Facts renders the facts of the day as live context for the model.
func Facts(f facts.Facts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Now: %s (%s). The working day ends at %s.\n", f.Now.Format("Monday 2006-01-02 15:04"),
		f.Now.Format("MST"), f.DayEnd.Format("15:04"))

	b.WriteString("Free time left today:")
	if len(f.Gaps) == 0 {
		b.WriteString(" none")
	}
	for i, g := range f.Gaps {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, " %s-%s (%s)", g.Start.Format("15:04"), g.End.Format("15:04"), facts.Duration(g.Duration()))
	}

	b.WriteString("\n\nToday's events:\n")
	if len(f.Events) == 0 {
		b.WriteString("(none)\n")
	}
	for _, e := range f.Events {
		start := e.StartsAt.In(f.Now.Location()).Format("15:04")
		if e.EndsAt != nil {
			fmt.Fprintf(&b, "- %s-%s %s\n", start, e.EndsAt.In(f.Now.Location()).Format("15:04"), e.Title)
		} else {
			fmt.Fprintf(&b, "- %s %s\n", start, e.Title)
		}
	}

	b.WriteString("\nEligible tasks, as #<id> title:\n")
	if len(f.Tasks) == 0 {
		b.WriteString("(none)\n")
	}
	for _, tf := range f.Tasks {
		t := tf.Task
		fmt.Fprintf(&b, "#%d %s", t.ID, t.Title)
		var details []string
		if t.Size != nil {
			details = append(details, "size "+string(*t.Size))
		}
		if t.Energy != nil {
			details = append(details, string(*t.Energy)+" energy")
		}
		if len(details) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(details, ", "))
		}
		if t.Feeling != "" {
			fmt.Fprintf(&b, " feeling: %q", t.Feeling)
		}
		b.WriteByte('\n')
		for _, a := range tf.Annotations {
			fmt.Fprintf(&b, "  - %s\n", a)
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/prompt/`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/prompt/facts.go internal/prompt/facts_test.go
git commit -m "Render facts into prompt context"
```


### Task 7: The planner: plans and what now

One structured main-model call per plan or pick, with `Fallback` set. `ask` makes the call, checks the reply, and retries once with the reply and its problems attached. A second bad reply, or any provider error, falls through to the fallback built from facts. `Replan` saves the plan and publishes `plan.updated`. `gather` only builds a client when asked, so the home-screen view in Task 9 doesn't touch the keychain.

**Files:**
- Create: `internal/planner/instructions.go`
- Create: `internal/planner/planner.go`
- Create: `internal/planner/plan.go`
- Create: `internal/planner/whatnow.go`
- Create: `internal/planner/helpers_test.go`
- Create: `internal/planner/plan_test.go`
- Create: `internal/planner/whatnow_test.go`

**Interfaces:**
- Consumes: `facts.Compute`, `prompt.Facts`, `prompt.System`, `jsonschema`, and the Task 4 service methods.
- Produces:
  - `planner.ClientFunc func(ctx) (llm.Client, settings.ProviderConfig, settings.Settings, error)`. It's the same shape as `cleanup.ClientFunc`, so one function literal serves both.
  - `planner.New(svc, hub, client ClientFunc, loc *time.Location, now func() time.Time) *Planner`
  - `(*Planner).Replan(ctx, domain.Trigger, reason string) (domain.Plan, error)`: `ErrNoDay` if today hasn't started.
  - `(*Planner).WhatNow(ctx, exclude int64) (Pick, error)`: `ErrNothingToDo` if no eligible task is free.
  - `(*Planner).Date() domain.Date`
  - `planner.Pick{TaskID, Reason, Fallback}`
  - Hub message `plan.updated` with a `domain.Plan`.

- [ ] **Step 1: Write the failing tests**

`internal/planner/helpers_test.go`. Create it with:

```go
package planner_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

// loc is UTC-5. The clock reads 09:00 local on Monday 2026-10-05.
var loc = time.FixedZone("UTC-5", -5*3600)

const today = domain.Date("2026-10-05")

// script is a fake model. It answers each call with the next reply in order and records the
// requests. A reply that is an error is returned as one.
type script struct {
	t       *testing.T
	replies []any
	sent    []llm.Request
}

func (s *script) Complete(_ context.Context, req llm.Request) (string, error) {
	s.sent = append(s.sent, req)
	if len(s.replies) == 0 {
		s.t.Fatalf("unexpected model call %d", len(s.sent))
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	if err, ok := r.(error); ok {
		return "", err
	}
	if str, ok := r.(string); ok {
		return str, nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		s.t.Fatal(err)
	}
	return string(b), nil
}

type fixture struct {
	svc     *service.Service
	hub     *stream.Hub
	planner *planner.Planner
	model   *script
	// configured false means no provider is set up.
	configured bool
}

func newFixture(t *testing.T, replies ...any) *fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := func() time.Time { return time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC) }
	f := &fixture{svc: service.New(db, now), hub: stream.NewHub(), model: &script{t: t, replies: replies}, configured: true}
	s := settings.Defaults()
	s.Provider = settings.Anthropic
	s.Style = "dry humor"
	client := func(context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
		if !f.configured {
			return nil, settings.ProviderConfig{}, settings.Defaults(), settings.ErrNotConfigured
		}
		return f.model, s.Config(), s, nil
	}
	f.planner = planner.New(f.svc, f.hub, client, loc, now)
	return f
}

func (f *fixture) task(t *testing.T, fields service.TaskFields) domain.Task {
	t.Helper()
	ctx := context.Background()
	batch, err := f.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.svc.CreateTask(ctx, batch, fields)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (f *fixture) block(t *testing.T, waiting, first int64) {
	t.Helper()
	ctx := context.Background()
	batch, _ := f.svc.NewBatch(ctx, domain.ActorUser)
	if err := f.svc.AddBlocker(ctx, batch, domain.Blocker{TaskID: waiting, BlockedByID: first}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) start(t *testing.T, pace domain.Pace, mood string) domain.Day {
	t.Helper()
	day, err := f.svc.StartDay(context.Background(), today, pace, mood)
	if err != nil {
		t.Fatal(err)
	}
	return day
}

func system(req llm.Request) string {
	var b strings.Builder
	for _, blk := range req.System {
		b.WriteString(blk.Text)
	}
	return b.String()
}

func date(d string) *domain.Date { v := domain.Date(d); return &v }

func entry(id int64, tier domain.Tier) map[string]any {
	return map[string]any{"task_id": id, "rationale": "because", "tier": tier}
}
```

`internal/planner/plan_test.go`. Create it with:

```go
package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestMorningPlanFromTheModel(t *testing.T) {
	f := newFixture(t)
	flex := f.task(t, service.TaskFields{Title: "Do FLEX1-666", DueOn: date("2026-10-05"), Feeling: "dreading it"})
	plants := f.task(t, service.TaskFields{Title: "water plants"})
	f.model.replies = []any{map[string]any{
		"entries": []any{entry(flex.ID, domain.TierMust), entry(plants.ID, domain.TierCould)},
		"opening": "Rough night. One must today.",
	}}
	f.start(t, 2, "slept badly")
	msgs, unsub := f.hub.Subscribe()
	defer unsub()

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}

	want := []domain.PlanEntry{{TaskID: flex.ID, Tier: domain.TierMust, Rationale: "because"},
		{TaskID: plants.ID, Tier: domain.TierCould, Rationale: "because"}}
	if diff := cmp.Diff(want, plan.Entries); diff != "" {
		t.Errorf("entries (-want +got):\n%s", diff)
	}
	if plan.Opening != "Rough night. One must today." || plan.Fallback || plan.Version != 1 || plan.Trigger != domain.TriggerMorning {
		t.Errorf("plan = %+v", plan)
	}

	req := f.model.sent[0]
	if req.Model != "claude-sonnet-5-5" || !req.Fallback || req.Schema == nil || req.User != "Plan my day." {
		t.Errorf("request: model %q, fallback %v, user %q", req.Model, req.Fallback, req.User)
	}
	sys := system(req)
	for _, want := range []string{"dry humor", `Pace today: 2 of 5, "gentle".`, `On their mind: "slept badly"`,
		"Monday 2026-10-05 09:00", `#1 Do FLEX1-666 feeling: "dreading it"`, "  - due today"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt is missing %q", want)
		}
	}

	m := <-msgs
	if got, ok := m.Data.(domain.Plan); m.Type != "plan.updated" || !ok || got.ID != plan.ID {
		t.Errorf("published %+v", m)
	}
}

func TestReplanShowsTheCurrentPlanAndUnplannedTasks(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	b := f.task(t, service.TaskFields{Title: "b"})
	f.model.replies = []any{
		map[string]any{"entries": []any{entry(a.ID, domain.TierShould)}, "opening": "hi"},
		map[string]any{"entries": []any{entry(a.ID, domain.TierShould), entry(b.ID, domain.TierCould)}, "opening": "ok"},
	}
	f.start(t, 3, "")
	ctx := context.Background()
	if _, err := f.planner.Replan(ctx, domain.TriggerMorning, ""); err != nil {
		t.Fatal(err)
	}

	plan, err := f.planner.Replan(ctx, domain.TriggerPace, "pace changed to full steam")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != 2 || plan.Reason != "pace changed to full steam" {
		t.Errorf("plan = %+v", plan)
	}
	req := f.model.sent[1]
	if req.User != "Something changed: pace changed to full steam. Update the plan." {
		t.Errorf("user = %q", req.User)
	}
	sys := system(req)
	for _, want := range []string{"must: (none)\nshould: #1\ncould: (none)\n", "unplanned: #2\n"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt is missing %q", want)
		}
	}
}

func TestAnInvalidPlanIsRetriedWithItsProblems(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	later := f.task(t, service.TaskFields{Title: "later", NotBefore: date("2026-10-09")})
	f.model.replies = []any{
		map[string]any{"entries": []any{entry(a.ID, domain.TierMust), entry(a.ID, domain.TierCould),
			entry(later.ID, domain.TierCould), entry(99, domain.TierShould), entry(a.ID, "someday")}, "opening": "x"},
		map[string]any{"entries": []any{entry(a.ID, domain.TierMust)}, "opening": "fixed"},
	}
	f.start(t, 3, "")

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fallback || plan.Opening != "fixed" {
		t.Errorf("plan = %+v", plan)
	}
	retry := f.model.sent[1].User
	for _, want := range []string{"Your previous reply was", "#1 appears more than once", "#2 is not an eligible task",
		"#99 is not an eligible task", `#1 has tier "someday"`} {
		if !strings.Contains(retry, want) {
			t.Errorf("retry message is missing %q:\n%s", want, retry)
		}
	}
}

func TestFallbackPlanAfterTwoBadReplies(t *testing.T) {
	f := newFixture(t, "not json", map[string]any{"entries": []any{entry(99, domain.TierMust)}, "opening": "x"})
	undated := f.task(t, service.TaskFields{Title: "undated"})
	later := f.task(t, service.TaskFields{Title: "due friday", DueOn: date("2026-10-09")})
	overdue := f.task(t, service.TaskFields{Title: "overdue", DueOn: date("2026-10-01")})
	dueToday := f.task(t, service.TaskFields{Title: "due today", DueOn: date("2026-10-05")})
	blocker := f.task(t, service.TaskFields{Title: "research"})
	f.block(t, dueToday.ID, blocker.ID)
	f.start(t, 3, "")

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fallback || len(f.model.sent) != 2 {
		t.Fatalf("fallback %v after %d calls", plan.Fallback, len(f.model.sent))
	}
	var got []string
	for _, e := range plan.Entries {
		got = append(got, string(e.Tier)+" "+map[int64]string{undated.ID: "undated", later.ID: "later",
			overdue.ID: "overdue", dueToday.ID: "today", blocker.ID: "research"}[e.TaskID])
	}
	// The blocked must stays a must, and its blocker is planned like any other task.
	want := []string{"must overdue", "must today", "should later", "could undated", "could research"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("fallback plan (-want +got):\n%s", diff)
	}
}

func TestProviderErrorFallsBackWithoutARetry(t *testing.T) {
	f := newFixture(t, errors.New("anthropic: 529 overloaded"))
	f.task(t, service.TaskFields{Title: "a"})
	f.start(t, 3, "")

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fallback || len(f.model.sent) != 1 || !strings.Contains(plan.Opening, "couldn't") {
		t.Errorf("plan = %+v after %d calls", plan, len(f.model.sent))
	}
}

func TestNoProviderGivesAFallbackPlan(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	f.task(t, service.TaskFields{Title: "a"})
	f.start(t, 3, "")

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fallback || len(plan.Entries) != 1 || !strings.Contains(plan.Opening, "No AI provider") {
		t.Errorf("plan = %+v", plan)
	}
}

func TestReplanNeedsAStartedDay(t *testing.T) {
	f := newFixture(t)
	if _, err := f.planner.Replan(context.Background(), domain.TriggerEdit, "x"); !errors.Is(err, planner.ErrNoDay) {
		t.Errorf("err = %v, want ErrNoDay", err)
	}
}

func TestAnEmptyDayStillGetsAPlan(t *testing.T) {
	f := newFixture(t, map[string]any{"entries": []any{}, "opening": "Nothing on the list. Enjoy it."})
	f.start(t, 3, "")
	msgs, unsub := f.hub.Subscribe()
	defer unsub()

	plan, err := f.planner.Replan(context.Background(), domain.TriggerMorning, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fallback || len(plan.Entries) != 0 {
		t.Errorf("plan = %+v", plan)
	}
	if m := <-msgs; m.Type != "plan.updated" {
		t.Errorf("published %+v", m)
	}
}
```

`internal/planner/whatnow_test.go`. Create it with:

```go
package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
)

func pick(id int64, reason string) map[string]any {
	return map[string]any{"reason": reason, "task_id": id}
}

func TestWhatNowPicksFromTheModel(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	f.model.replies = []any{pick(a.ID, "Ten minutes before standup: perfect for this.")}
	f.start(t, 2, "")

	got, err := f.planner.WhatNow(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != (planner.Pick{TaskID: a.ID, Reason: "Ten minutes before standup: perfect for this."}) {
		t.Errorf("pick = %+v", got)
	}
	req := f.model.sent[0]
	if req.User != "What should I do right now?" || !req.Fallback || req.Model != "claude-sonnet-5-5" {
		t.Errorf("request: %+v", req)
	}
}

func TestWhatNowWorksBeforeTheDayStarts(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	f.model.replies = []any{pick(a.ID, "go")}

	if got, err := f.planner.WhatNow(context.Background(), 0); err != nil || got.TaskID != a.ID {
		t.Errorf("pick %+v, err %v", got, err)
	}
	if strings.Contains(system(f.model.sent[0]), "Pace today") {
		t.Error("no day, so no pace should be claimed")
	}
}

func TestWhatNowRetriesABlockedOrExcludedPick(t *testing.T) {
	f := newFixture(t)
	blocked := f.task(t, service.TaskFields{Title: "blocked"})
	first := f.task(t, service.TaskFields{Title: "first"})
	passed := f.task(t, service.TaskFields{Title: "passed on"})
	f.block(t, blocked.ID, first.ID)

	cases := []struct {
		bad     int64
		problem string
	}{{blocked.ID, "#1 is blocked"}, {passed.ID, "#3 is the task the user just passed on"}, {42, "#42 is not an eligible task"}}
	for _, c := range cases {
		f.model.sent = nil
		f.model.replies = []any{pick(c.bad, "x"), pick(first.ID, "fine")}
		got, err := f.planner.WhatNow(context.Background(), passed.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.TaskID != first.ID || got.Fallback {
			t.Errorf("pick = %+v", got)
		}
		if !strings.Contains(f.model.sent[0].User, "Not #3") || !strings.Contains(f.model.sent[1].User, c.problem) {
			t.Errorf("requests: %q then %q", f.model.sent[0].User, f.model.sent[1].User)
		}
	}
}

func TestWhatNowFallsBackToTheFirstFreeTaskInFallbackOrder(t *testing.T) {
	f := newFixture(t, errors.New("rate limited"))
	f.task(t, service.TaskFields{Title: "undated"})
	dueToday := f.task(t, service.TaskFields{Title: "due today", DueOn: date("2026-10-05")})
	overdue := f.task(t, service.TaskFields{Title: "overdue", DueOn: date("2026-10-01")})
	research := f.task(t, service.TaskFields{Title: "research", DueOn: date("2026-10-07")})
	f.block(t, overdue.ID, research.ID)

	// Overdue is first but blocked; due today is the user's pass; research is next by due date.
	got, err := f.planner.WhatNow(context.Background(), dueToday.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskID != research.ID || !got.Fallback {
		t.Errorf("pick = %+v", got)
	}
}

func TestWhatNowWithNothingFree(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	f.task(t, service.TaskFields{Title: "later", NotBefore: date("2026-10-09")})
	if _, err := f.planner.WhatNow(context.Background(), a.ID); !errors.Is(err, planner.ErrNothingToDo) {
		t.Errorf("err = %v, want ErrNothingToDo", err)
	}
	if len(f.model.sent) != 0 {
		t.Error("the model was asked with nothing to pick")
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/planner/`
Expected: build fails: the `planner` package doesn't exist yet

- [ ] **Step 3: Implement**

`internal/planner/instructions.go`. Create it with:

```go
package planner

const persona = `You help someone with ADHD get through their day. You hold the day loosely, not as an hourly schedule: what they must do, what they should do, and what they could do if they have the space. You're warm and direct, and you pay attention to how they feel about their work.`

const planInstructions = persona + `

Make today's plan from the eligible tasks in the context. Reply only with JSON that matches the schema.

- Put each task you plan in one tier. must: it really has to happen today. should: it matters, and today is a good day for it. could: worth doing if there's room.
- Fit the plan to the pace. At a low pace, keep must short and leave plenty unplanned. At a high pace, plan more. The free time in the context is how much room the day really has.
- You don't have to plan every task. Anything you leave out waits in an unplanned tray.
- Read the notes under each task. A blocked task can still be planned. A blocked must makes whatever blocks it more urgent. A task pushed many times may need to be broken down or talked about more than it needs to be a must again.
- Within a tier, list tasks in the order you'd suggest doing them.
- rationale: one short line the user will see, saying why the task sits where it does.
- opening: two or three sentences in your voice. In the morning, start the day and acknowledge what's on their mind if they said. When replanning, say briefly what changed. Don't list the tasks; the plan shows them.
- When there's a current plan, keep it unless the change gives you a reason to move something. A plan that holds still is easier to trust.`

const whatNowInstructions = persona + `

They've asked "what now?". Pick the one task they should start right now from the eligible tasks in the context. Reply only with JSON that matches the schema.

- Never pick a blocked task.
- Weigh the plan, due dates, the free time before their next event, their pace, and how they feel. A short gap before a meeting suits a small task. A low pace or a rough mood suits something lighter.
- Unplanned tasks can be picked too.
- reason: one or two sentences, to them, on why this task now. Be concrete and kind.`
```

`internal/planner/planner.go`. Create it with:

```go
// Package planner makes the day's plan and answers "what now?". Each is one structured call to the
// main model. The reply is validated against the facts; a rejected reply gets one retry with the
// problems attached, and if that fails too, code builds the answer instead, so a bad model day
// still gives the user something valid.
package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/prompt"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/stream"
)

// ErrNoDay means today hasn't been started, so there is no pace to plan for.
var ErrNoDay = errors.New("today hasn't started")

// ClientFunc returns the client and settings for one call. It returns settings.ErrNotConfigured,
// along with the saved settings, when no provider is set up.
type ClientFunc func(ctx context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error)

type Planner struct {
	svc    *service.Service
	hub    *stream.Hub
	client ClientFunc
	loc    *time.Location
	now    func() time.Time
	// mu runs one replan at a time, so plan versions follow the order of what triggered them.
	mu sync.Mutex
}

const maxTokens = 8192

func New(svc *service.Service, hub *stream.Hub, client ClientFunc, loc *time.Location, now func() time.Time) *Planner {
	return &Planner{svc: svc, hub: hub, client: client, loc: loc, now: now}
}

// state is what one call sees. day and plan are nil when today hasn't started or has no plan yet.
// model is nil when no client could be built, and modelErr says why.
type state struct {
	day      *domain.Day
	plan     *domain.Plan
	facts    facts.Facts
	settings settings.Settings
	model    llm.Client
	cfg      settings.ProviderConfig
	modelErr error
}

// Date is today's date in the user's time zone.
func (p *Planner) Date() domain.Date { return facts.Today(p.now(), p.loc) }

// gather reads the current state. withModel also builds a client and loads settings; without it,
// the facts use the default end of day.
func (p *Planner) gather(ctx context.Context, withModel bool) (state, error) {
	st := state{settings: settings.Defaults()}
	now := p.now()
	today := facts.Today(now, p.loc)
	day, err := p.svc.GetDay(ctx, today)
	switch {
	case err == nil:
		st.day = &day
		plan, err := p.svc.LatestPlan(ctx, day.ID)
		if err == nil {
			st.plan = &plan
		} else if !errors.Is(err, service.ErrNotFound) {
			return st, err
		}
	case !errors.Is(err, service.ErrNotFound):
		return st, err
	}

	if withModel {
		st.model, st.cfg, st.settings, st.modelErr = p.client(ctx)
		if st.modelErr != nil {
			st.model = nil
		}
	}
	tasks, err := p.svc.ListTasks(ctx)
	if err != nil {
		return st, err
	}
	blockers, err := p.svc.ListBlockers(ctx)
	if err != nil {
		return st, err
	}
	pushes, err := p.svc.PushCounts(ctx)
	if err != nil {
		return st, err
	}
	// From a day back, so an overnight event still covering this morning is seen.
	local := now.In(p.loc)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, p.loc)
	events, err := p.svc.ListEvents(ctx, dayStart.AddDate(0, 0, -1), dayStart.AddDate(0, 0, 1))
	if err != nil {
		return st, err
	}
	musts := map[int64]bool{}
	if st.plan != nil {
		for _, e := range st.plan.Entries {
			if e.Tier == domain.TierMust {
				musts[e.TaskID] = true
			}
		}
	}
	st.facts = facts.Compute(facts.Input{Now: now, Loc: p.loc, EndOfDay: st.settings.EndOfDay, Tasks: tasks,
		Blockers: blockers, Events: events, PushCounts: pushes, Musts: musts})
	return st, nil
}

// context renders the live context layer: pace and mood, the facts, and the current plan.
func (st state) context() string {
	var b strings.Builder
	if st.day != nil {
		fmt.Fprintf(&b, "Pace today: %d of 5, %q.\n", st.day.Pace, st.day.Pace.Label())
		if st.day.MoodNote != "" {
			fmt.Fprintf(&b, "On their mind: %q\n", st.day.MoodNote)
		}
		b.WriteByte('\n')
	}
	b.WriteString(prompt.Facts(st.facts))
	if st.plan == nil {
		return b.String()
	}
	fmt.Fprintf(&b, "\nCurrent plan:\n")
	planned := map[int64]bool{}
	for _, tier := range []domain.Tier{domain.TierMust, domain.TierShould, domain.TierCould} {
		var ids []string
		for _, e := range st.plan.Entries {
			if e.Tier == tier {
				ids = append(ids, fmt.Sprintf("#%d", e.TaskID))
				planned[e.TaskID] = true
			}
		}
		if len(ids) == 0 {
			ids = []string{"(none)"}
		}
		fmt.Fprintf(&b, "%s: %s\n", tier, strings.Join(ids, ", "))
	}
	var unplanned []string
	for _, tf := range st.facts.Tasks {
		if !planned[tf.Task.ID] {
			unplanned = append(unplanned, fmt.Sprintf("#%d", tf.Task.ID))
		}
	}
	if len(unplanned) > 0 {
		fmt.Fprintf(&b, "unplanned: %s\n", strings.Join(unplanned, ", "))
	}
	return b.String()
}

func (st state) request(instructions, user string, schema map[string]any, name string) llm.Request {
	return llm.Request{
		Model:      st.cfg.MainModel,
		System:     prompt.System(instructions, st.settings.Style, st.settings.AboutMe, st.context()),
		User:       user,
		Schema:     schema,
		SchemaName: name,
		MaxTokens:  maxTokens,
		Fallback:   true,
	}
}

// ask makes a structured call and checks the reply. A reply that fails the check gets one retry
// with the problems attached. Provider errors are returned at once: retrying them is the SDK's job.
func ask[T any](ctx context.Context, model llm.Client, req llm.Request, check func(T) error) (T, error) {
	var out T
	reply, err := model.Complete(ctx, req)
	if err != nil {
		return out, err
	}
	if err = parse(reply, &out, check); err == nil {
		return out, nil
	}
	req.User += fmt.Sprintf("\n\nYour previous reply was:\n%s\n\nIt was rejected:\n%v\n\nReply again with that fixed.", reply, err)
	reply, err = model.Complete(ctx, req)
	if err != nil {
		return out, err
	}
	out = *new(T)
	return out, parse(reply, &out, check)
}

func parse[T any](reply string, out *T, check func(T) error) error {
	if err := json.Unmarshal([]byte(reply), out); err != nil {
		return fmt.Errorf("the reply is not valid JSON: %v", err)
	}
	return check(*out)
}

// fallbackNote explains a plan or pick that code made instead of the model.
func fallbackNote(err error) string {
	if errors.Is(err, settings.ErrNotConfigured) {
		return "No AI provider is set up, so this is sorted by due date."
	}
	log.Printf("planner fell back: %v", err)
	return "I couldn't think this through just now, so this is sorted by due date."
}
```

`internal/planner/plan.go`. Create it with:

```go
package planner

import (
	"context"
	"errors"
	"fmt"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	js "github.com/jameynakama/whatnow/internal/jsonschema"
	"github.com/jameynakama/whatnow/internal/stream"
)

// planSchema puts the entries before the opening, so the model decides the plan before it
// writes about it, and each rationale before its tier.
var planSchema = js.Object(
	js.Prop("entries", js.Array(js.Object(
		js.Prop("task_id", js.Integer("The task's number, from #<id>.")),
		js.Prop("rationale", js.String("One short line for the user on why the task sits in this tier.")),
		js.Prop("tier", js.Enum("must, should, or could.", "must", "should", "could")),
	))),
	js.Prop("opening", js.String("Two or three sentences to the user, in your voice.")),
)

type planReply struct {
	Entries []domain.PlanEntry `json:"entries"`
	Opening string             `json:"opening"`
}

// Replan makes a new version of today's plan and publishes it as plan.updated. It returns ErrNoDay
// if today hasn't started.
func (p *Planner) Replan(ctx context.Context, trigger domain.Trigger, reason string) (domain.Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, err := p.gather(ctx, true)
	if err != nil {
		return domain.Plan{}, err
	}
	if st.day == nil {
		return domain.Plan{}, ErrNoDay
	}
	plan := domain.Plan{DayID: st.day.ID, Trigger: trigger, Reason: reason}

	reply, err := p.askPlan(ctx, st, trigger, reason)
	if err == nil {
		plan.Entries, plan.Opening = reply.Entries, reply.Opening
	} else {
		plan.Entries, plan.Opening, plan.Fallback = fallbackPlan(st.facts), fallbackNote(err), true
	}
	saved, err := p.svc.SavePlan(ctx, plan)
	if err != nil {
		return domain.Plan{}, err
	}
	p.hub.Publish(stream.Message{Type: "plan.updated", Data: saved})
	return saved, nil
}

func (p *Planner) askPlan(ctx context.Context, st state, trigger domain.Trigger, reason string) (planReply, error) {
	if st.model == nil {
		return planReply{}, st.modelErr
	}
	user := "Plan my day."
	if trigger != domain.TriggerMorning {
		user = fmt.Sprintf("Something changed: %s. Update the plan.", reason)
	}
	return ask(ctx, st.model, st.request(planInstructions, user, planSchema, "plan"), func(r planReply) error {
		return checkPlan(r, st.facts)
	})
}

// checkPlan rejects a plan that names a task twice, an unknown or ineligible task, or a bad tier.
func checkPlan(r planReply, f facts.Facts) error {
	var errs []error
	seen := map[int64]bool{}
	for _, e := range r.Entries {
		if _, ok := f.Task(e.TaskID); !ok {
			errs = append(errs, fmt.Errorf("#%d is not an eligible task", e.TaskID))
		}
		if seen[e.TaskID] {
			errs = append(errs, fmt.Errorf("#%d appears more than once", e.TaskID))
		}
		seen[e.TaskID] = true
		if !e.Tier.Valid() {
			errs = append(errs, fmt.Errorf("#%d has tier %q", e.TaskID, e.Tier))
		}
	}
	return errors.Join(errs...)
}

// fallbackPlan is the plan code makes when the model can't: due today or overdue is a must, due
// later is a should, and undated is a could. Facts already lists tasks in that order.
func fallbackPlan(f facts.Facts) []domain.PlanEntry {
	entries := []domain.PlanEntry{}
	for _, tf := range f.Tasks {
		t := tf.Task
		e := domain.PlanEntry{TaskID: t.ID, Tier: domain.TierCould, Rationale: "No due date."}
		switch {
		case t.DueOn != nil && *t.DueOn <= f.Today:
			e.Tier, e.Rationale = domain.TierMust, "Due today or overdue."
		case t.DueOn != nil:
			e.Tier, e.Rationale = domain.TierShould, "Due "+string(*t.DueOn)+"."
		}
		entries = append(entries, e)
	}
	return entries
}
```

`internal/planner/whatnow.go`. Create it with:

```go
package planner

import (
	"context"
	"errors"
	"fmt"

	"github.com/jameynakama/whatnow/internal/facts"
	js "github.com/jameynakama/whatnow/internal/jsonschema"
)

// ErrNothingToDo means no eligible task is free to start.
var ErrNothingToDo = errors.New("no task is free to start")

var whatNowSchema = js.Object(
	js.Prop("reason", js.String("One or two sentences to the user on why this task, now.")),
	js.Prop("task_id", js.Integer("The task's number, from #<id>.")),
)

// Pick is the answer to "what now?". Fallback means code picked because the model couldn't.
type Pick struct {
	TaskID   int64  `json:"task_id"`
	Reason   string `json:"reason"`
	Fallback bool   `json:"fallback"`
}

// WhatNow picks one task to start now from every eligible task, planned or not. exclude, when not
// zero, is a task the user just passed on; it is left out of this answer only.
func (p *Planner) WhatNow(ctx context.Context, exclude int64) (Pick, error) {
	st, err := p.gather(ctx, true)
	if err != nil {
		return Pick{}, err
	}
	var first int64
	for _, tf := range st.facts.Tasks {
		if len(tf.BlockedBy) == 0 && tf.Task.ID != exclude {
			first = tf.Task.ID
			break
		}
	}
	if first == 0 {
		return Pick{}, ErrNothingToDo
	}

	pick, err := p.askPick(ctx, st, exclude)
	if err != nil {
		return Pick{TaskID: first, Reason: fallbackNote(err), Fallback: true}, nil
	}
	return pick, nil
}

func (p *Planner) askPick(ctx context.Context, st state, exclude int64) (Pick, error) {
	if st.model == nil {
		return Pick{}, st.modelErr
	}
	user := "What should I do right now?"
	if exclude != 0 {
		user += fmt.Sprintf(" Not #%d, I just passed on that one.", exclude)
	}
	return ask(ctx, st.model, st.request(whatNowInstructions, user, whatNowSchema, "pick"), func(pk Pick) error {
		return checkPick(pk, st.facts, exclude)
	})
}

func checkPick(pk Pick, f facts.Facts, exclude int64) error {
	tf, ok := f.Task(pk.TaskID)
	switch {
	case !ok:
		return fmt.Errorf("#%d is not an eligible task", pk.TaskID)
	case len(tf.BlockedBy) > 0:
		return fmt.Errorf("#%d is blocked", pk.TaskID)
	case pk.TaskID == exclude:
		return fmt.Errorf("#%d is the task the user just passed on", pk.TaskID)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/planner/`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/planner/helpers_test.go internal/planner/instructions.go internal/planner/plan.go internal/planner/plan_test.go internal/planner/planner.go internal/planner/whatnow.go internal/planner/whatnow_test.go
git commit -m "Add the planner: plans and what now"
```


### Task 8: Replan triggers

`Run` reads a hub subscription. For each `batch.applied` it checks the batch's activity, and for each `batch.undone` it reads the batch's activity back and runs it backward. Messages that pile up during a replan fold into one more replan. Only a day that already has a plan is replanned. The undo message gets a typed payload so the planner can read it.

**Files:**
- Modify: `internal/stream/hub.go`
- Modify: `internal/httpapi/tasks.go`
- Create: `internal/planner/triggers.go`
- Create: `internal/planner/triggers_internal_test.go`
- Create: `internal/planner/triggers_test.go`
- Modify: `internal/httpapi/tasks_test.go`

**Interfaces:**
- Consumes: `(*Planner).Replan`, `(*Service).BatchActivity`, `GetDay`, `LatestPlan`.
- Produces: `(*Planner).Run(ctx, msgs <-chan stream.Message)`. The caller subscribes, so tests can subscribe before anything is published. Also `stream.BatchUndone{BatchID}` and `(*Hub).PublishUndone(batch int64)`. The JSON shape of `batch.undone` is unchanged.

- [ ] **Step 1: Write the failing tests**

`internal/planner/triggers_internal_test.go`. Create it with:

```go
package planner

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
)

func snap(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func act(entity string, id int64, before, after any) domain.Activity {
	return domain.Activity{Entity: entity, EntityID: id, Before: snap(before), After: snap(after)}
}

func TestReplanReasons(t *testing.T) {
	const today = domain.Date("2026-10-05")
	d := func(s string) *domain.Date { v := domain.Date(s); return &v }
	task := func(id int64, mutate func(*domain.Task)) domain.Task {
		t := domain.Task{ID: id, Title: "t", Status: domain.StatusOpen}
		if mutate != nil {
			mutate(&t)
		}
		return t
	}
	dueToday := func(t *domain.Task) { t.DueOn = d("2026-10-05") }
	dueFriday := func(t *domain.Task) { t.DueOn = d("2026-10-09") }
	dropped := func(t *domain.Task) { t.Status = domain.StatusDropped }
	done := func(t *domain.Task) { t.Status = domain.StatusDone }
	gone := func(t *domain.Task) { deleted := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC); t.DeletedAt = &deleted }
	musts := map[int64]bool{1: true}
	day := func(p domain.Pace, date domain.Date) domain.Day { return domain.Day{ID: 1, Date: date, Pace: p} }

	cases := []struct {
		name    string
		act     domain.Activity
		undone  bool
		trigger domain.Trigger
		reasons []string
	}{
		{"pace change", act("day", 1, day(3, today), day(5, today)), false, domain.TriggerPace, []string{`pace changed to "go hard"`}},
		{"mood only", act("day", 1, day(3, today), day(3, today)), false, domain.TriggerEdit, nil},
		{"pace on another day", act("day", 1, day(3, "2026-10-04"), day(5, "2026-10-04")), false, domain.TriggerEdit, nil},
		{"undoing a pace change", act("day", 1, day(3, today), day(5, today)), true, domain.TriggerPace, []string{`pace changed to "steady"`}},
		{"created due today", act("task", 7, nil, task(7, dueToday)), false, domain.TriggerEdit, []string{`#7 "t" is due now`}},
		{"created due friday", act("task", 7, nil, task(7, dueFriday)), false, domain.TriggerEdit, nil},
		{"moved to today", act("task", 7, task(7, dueFriday), task(7, dueToday)), false, domain.TriggerEdit, []string{`#7 "t" is due now`}},
		{"edited, already due", act("task", 7, task(7, dueToday), task(7, dueToday)), false, domain.TriggerEdit, nil},
		{"undoing a delete of a due task", act("task", 7, task(7, dueToday), task(7, func(t *domain.Task) { dueToday(t); gone(t) })), true,
			domain.TriggerEdit, []string{`#7 "t" is due now`}},
		{"undoing a create", act("task", 7, nil, task(7, dueToday)), true, domain.TriggerEdit, nil},
		{"dropped a must", act("task", 1, task(1, nil), task(1, dropped)), false, domain.TriggerEdit, []string{`the must #1 "t" was dropped`}},
		{"deleted a must", act("task", 1, task(1, nil), task(1, gone)), false, domain.TriggerEdit, []string{`the must #1 "t" was dropped`}},
		{"finished a must", act("task", 1, task(1, nil), task(1, done)), false, domain.TriggerEdit, nil},
		{"dropped a could", act("task", 2, task(2, nil), task(2, dropped)), false, domain.TriggerEdit, nil},
		{"blocker on a must", act("blocker", 1, nil, domain.Blocker{TaskID: 1, BlockedByID: 9}), false, domain.TriggerEdit,
			[]string{"a blocker on the must #1 changed"}},
		{"must as the blocker", act("blocker", 4, domain.Blocker{TaskID: 4, BlockedByID: 1}, nil), false, domain.TriggerEdit,
			[]string{"a blocker on the must #1 changed"}},
		{"blocker between others", act("blocker", 4, nil, domain.Blocker{TaskID: 4, BlockedByID: 5}), false, domain.TriggerEdit, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			trigger, reasons := replanReasons([]domain.Activity{c.act}, musts, today, c.undone)
			if trigger != c.trigger {
				t.Errorf("trigger = %q, want %q", trigger, c.trigger)
			}
			if diff := cmp.Diff(c.reasons, reasons); diff != "" {
				t.Errorf("reasons (-want +got):\n%s", diff)
			}
		})
	}
}
```

`internal/planner/triggers_test.go`. Create it with:

```go
package planner_test

import (
	"context"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

// run starts the trigger watcher and returns a subscription that sees the plans it publishes.
func (f *fixture) run(t *testing.T) <-chan stream.Message {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	watched, unwatch := f.hub.Subscribe()
	plans, unsub := f.hub.Subscribe()
	t.Cleanup(func() { cancel(); unwatch(); unsub() })
	go f.planner.Run(ctx, watched)
	return plans
}

// write applies a change in a new batch and announces it, as the API does.
func (f *fixture) write(t *testing.T, change func(batch int64) error) int64 {
	t.Helper()
	ctx := context.Background()
	batch, err := f.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := change(batch); err != nil {
		t.Fatal(err)
	}
	acts, err := f.svc.BatchActivity(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	f.hub.PublishBatch(batch, acts)
	return batch
}

func nextPlan(t *testing.T, msgs <-chan stream.Message) domain.Plan {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case m := <-msgs:
			if p, ok := m.Data.(domain.Plan); ok {
				return p
			}
		case <-timeout:
			t.Fatal("no plan.updated")
		}
	}
}

func TestEditsThatMatterTriggerAReplan(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	plants := f.task(t, service.TaskFields{Title: "water plants"})
	rfc := f.task(t, service.TaskFields{Title: "write the RFC"})
	f.start(t, 3, "")
	if _, err := f.planner.Replan(ctx, domain.TriggerMorning, ""); err != nil {
		t.Fatal(err)
	}
	plans := f.run(t)

	// An edit that changes nothing about the plan is ignored; the next plan is for the due date.
	f.write(t, func(b int64) error {
		_, err := f.svc.UpdateTask(ctx, b, plants.ID, service.TaskFields{Title: "water the plants"})
		return err
	})
	f.write(t, func(b int64) error {
		_, err := f.svc.UpdateTask(ctx, b, rfc.ID, service.TaskFields{Title: "write the RFC", DueOn: date("2026-10-05")})
		return err
	})
	plan := nextPlan(t, plans)
	if plan.Version != 2 || plan.Trigger != domain.TriggerEdit || plan.Reason != `#2 "write the RFC" is due now` {
		t.Errorf("plan = %+v", plan)
	}
}

func TestPaceChangesAndTheirUndoTriggerAReplan(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	f.start(t, 3, "")
	if _, err := f.planner.Replan(ctx, domain.TriggerMorning, ""); err != nil {
		t.Fatal(err)
	}
	plans := f.run(t)

	batch := f.write(t, func(b int64) error {
		_, err := f.svc.SetPace(ctx, b, today, 1, "")
		return err
	})
	if plan := nextPlan(t, plans); plan.Trigger != domain.TriggerPace || plan.Reason != `pace changed to "running on fumes"` {
		t.Errorf("after the change: %+v", plan)
	}

	if err := f.svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	f.hub.PublishUndone(batch)
	if plan := nextPlan(t, plans); plan.Trigger != domain.TriggerPace || plan.Reason != `pace changed to "steady"` || plan.Version != 3 {
		t.Errorf("after the undo: %+v", plan)
	}
}

func TestNoReplanBeforeTheMorningPlan(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	rfc := f.task(t, service.TaskFields{Title: "write the RFC"})
	f.start(t, 3, "")
	plans := f.run(t)

	// The day has started but has no plan yet: the morning makes the first one, not an edit.
	f.write(t, func(b int64) error {
		_, err := f.svc.UpdateTask(ctx, b, rfc.ID, service.TaskFields{Title: "write the RFC", DueOn: date("2026-10-05")})
		return err
	})
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case m := <-plans:
			if p, ok := m.Data.(domain.Plan); ok {
				t.Fatalf("unexpected plan: %+v", p)
			}
		case <-timeout:
			return
		}
	}
}
```

`internal/httpapi/tasks_test.go`. Apply:

```diff
--- a/internal/httpapi/tasks_test.go
+++ b/internal/httpapi/tasks_test.go
@@ -87,8 +87,8 @@ func TestEditStatusDeleteAndUndo(t *testing.T) {
 	defer unsub()
 	rec := api.do(http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/undo", done.BatchID), nil)
 	wantStatus(t, rec, http.StatusNoContent)
-	if m := <-msgs; m.Type != "batch.undone" {
-		t.Errorf("published %q, want batch.undone", m.Type)
+	if m := <-msgs; m.Type != "batch.undone" || m.Data != (stream.BatchUndone{BatchID: done.BatchID}) {
+		t.Errorf("published %+v, want batch.undone for batch %d", m, done.BatchID)
 	}
 
 	// The later status batch is undone, so nothing blocks undoing the edit now.
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test -race ./internal/planner/ ./internal/httpapi/`
Expected: build fails: `undefined: replanReasons`

- [ ] **Step 3: Implement**

`internal/stream/hub.go`. Apply:

```diff
--- a/internal/stream/hub.go
+++ b/internal/stream/hub.go
@@ -64,3 +64,13 @@ type BatchApplied struct {
 func (h *Hub) PublishBatch(batch int64, activity []domain.Activity) {
 	h.Publish(Message{Type: "batch.applied", Data: BatchApplied{BatchID: batch, Activity: activity}})
 }
+
+// BatchUndone is the batch.undone payload.
+type BatchUndone struct {
+	BatchID int64 `json:"batch_id"`
+}
+
+// PublishUndone announces that a batch was undone.
+func (h *Hub) PublishUndone(batch int64) {
+	h.Publish(Message{Type: "batch.undone", Data: BatchUndone{BatchID: batch}})
+}
```

`internal/httpapi/tasks.go`. Apply:

```diff
--- a/internal/httpapi/tasks.go
+++ b/internal/httpapi/tasks.go
@@ -5,7 +5,6 @@ import (
 
 	"github.com/jameynakama/whatnow/internal/domain"
 	"github.com/jameynakama/whatnow/internal/service"
-	"github.com/jameynakama/whatnow/internal/stream"
 )
 
 func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
@@ -130,6 +129,6 @@ func (s *Server) undo(w http.ResponseWriter, r *http.Request) {
 		writeError(w, err)
 		return
 	}
-	s.Hub.Publish(stream.Message{Type: "batch.undone", Data: map[string]int64{"batch_id": batch}})
+	s.Hub.PublishUndone(batch)
 	w.WriteHeader(http.StatusNoContent)
 }
```

`internal/planner/triggers.go`. Create it with:

```go
package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	"github.com/jameynakama/whatnow/internal/stream"
)

// Run replans today whenever a change calls for it, until ctx is done or msgs closes. msgs is a
// hub subscription, so changes from the API, cleanup, and the agent are all seen, and so are
// undos. Changes that pile up while a replan runs are folded into one more replan.
func (p *Planner) Run(ctx context.Context, msgs <-chan stream.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-msgs:
			if !ok {
				return
			}
			trigger, reasons := p.reasons(ctx, m)
			for more := true; more; {
				select {
				case m, ok := <-msgs:
					if !ok {
						return
					}
					t, r := p.reasons(ctx, m)
					if t == domain.TriggerPace {
						trigger = t
					}
					reasons = append(reasons, r...)
				default:
					more = false
				}
			}
			if len(reasons) == 0 {
				continue
			}
			if _, err := p.Replan(ctx, trigger, strings.Join(reasons, "; ")); err != nil && !errors.Is(err, ErrNoDay) {
				log.Printf("replan: %v", err)
			}
		}
	}
}

// reasons says why one hub message calls for a replan. Only a day that already has a plan is
// replanned; before that, the morning makes the first one.
func (p *Planner) reasons(ctx context.Context, m stream.Message) (domain.Trigger, []string) {
	var acts []domain.Activity
	undone := false
	switch d := m.Data.(type) {
	case stream.BatchApplied:
		acts = d.Activity
	case stream.BatchUndone:
		var err error
		if acts, err = p.svc.BatchActivity(ctx, d.BatchID); err != nil {
			log.Printf("replan: read undone batch %d: %v", d.BatchID, err)
			return "", nil
		}
		undone = true
	default:
		return "", nil
	}
	today := facts.Today(p.now(), p.loc)
	day, err := p.svc.GetDay(ctx, today)
	if err != nil {
		return "", nil
	}
	plan, err := p.svc.LatestPlan(ctx, day.ID)
	if err != nil {
		return "", nil
	}
	musts := map[int64]bool{}
	for _, e := range plan.Entries {
		if e.Tier == domain.TierMust {
			musts[e.TaskID] = true
		}
	}
	return replanReasons(acts, musts, today, undone)
}

// replanReasons lists what in a batch calls for a replan: a pace change, a task becoming due
// today, a must being dropped or deleted, or a blocker added or removed on a must. undone runs
// the batch's changes backward, since an undo reverses each of them.
func replanReasons(acts []domain.Activity, musts map[int64]bool, today domain.Date, undone bool) (domain.Trigger, []string) {
	trigger := domain.TriggerEdit
	var reasons []string
	for _, a := range acts {
		before, after := a.Before, a.After
		if undone {
			before, after = after, before
		}
		switch a.Entity {
		case "day":
			var b, af domain.Day
			if decode(before, &b) && decode(after, &af) && b.Pace != af.Pace && af.Date == today {
				trigger = domain.TriggerPace
				reasons = append(reasons, fmt.Sprintf("pace changed to %q", af.Pace.Label()))
			}
		case "task":
			var b, af domain.Task
			hadB, hadA := decode(before, &b), decode(after, &af)
			switch {
			case hadA && dueNow(af, today) && !(hadB && dueNow(b, today)):
				reasons = append(reasons, fmt.Sprintf("#%d %q is due now", af.ID, af.Title))
			case musts[a.EntityID] && hadB && live(b) && (!hadA || af.Status == domain.StatusDropped || af.DeletedAt != nil):
				reasons = append(reasons, fmt.Sprintf("the must #%d %q was dropped", b.ID, b.Title))
			}
		case "blocker":
			var b domain.Blocker
			if !decode(before, &b) {
				decode(after, &b)
			}
			for _, id := range []int64{b.TaskID, b.BlockedByID} {
				if musts[id] {
					reasons = append(reasons, fmt.Sprintf("a blocker on the must #%d changed", id))
					break
				}
			}
		}
	}
	return trigger, reasons
}

func decode(raw json.RawMessage, v any) bool {
	return raw != nil && json.Unmarshal(raw, v) == nil
}

func live(t domain.Task) bool { return t.Status == domain.StatusOpen && t.DeletedAt == nil }

func dueNow(t domain.Task, today domain.Date) bool {
	return live(t) && t.DueOn != nil && *t.DueOn <= today
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test -race ./internal/planner/ ./internal/httpapi/`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/tasks.go internal/httpapi/tasks_test.go internal/planner/triggers.go internal/planner/triggers_internal_test.go internal/planner/triggers_test.go internal/stream/hub.go
git commit -m "Replan when a change calls for it"
```


### Task 9: Day endpoints and wiring

The routes the web UI's home screen, good-morning flow, pace chip, and what-now card need. Starting the day waits for the first plan, so the morning flow can reveal it. A pace change returns at once; its replan arrives as `plan.updated`. `ErrNoDay` maps to 409 and `ErrNothingToDo` to 404.

**Files:**
- Create: `internal/planner/today.go`
- Create: `internal/httpapi/days.go`
- Modify: `internal/httpapi/server.go`
- Modify: `cmd/whatnow/main.go`
- Modify: `internal/httpapi/helpers_test.go`
- Create: `internal/httpapi/days_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `planner.Today{Date, Day *domain.Day, Plan *domain.Plan, Unplanned []int64, Blocked []int64}`, `(*Planner).Today(ctx) (Today, error)`, and `(*Planner).StartDay(ctx, pace, moodNote) (domain.Day, domain.Plan, error)`.
  - `httpapi.Deps.Planner *planner.Planner`
  - `GET /api/v1/today` → `planner.Today`
  - `POST /api/v1/today/start` `{pace, mood_note}` → 201 `{day, plan}`. 409 if already started.
  - `PUT /api/v1/today/pace` `{pace, note}` → `{batch_id, data: Day}`
  - `POST /api/v1/whatnow` `{exclude_task_id?, reason?}` → `planner.Pick`. 404 when nothing is free.
  - `POST /api/v1/tasks/{id}/push` `{until}` → `{batch_id, data: Task}`

- [ ] **Step 1: Write the failing tests**

`internal/httpapi/helpers_test.go`. Apply:

```diff
--- a/internal/httpapi/helpers_test.go
+++ b/internal/httpapi/helpers_test.go
@@ -2,6 +2,7 @@ package httpapi_test
 
 import (
 	"bytes"
+	"context"
 	"encoding/json"
 	"io"
 	"net/http"
@@ -13,6 +14,7 @@ import (
 	"github.com/jameynakama/whatnow/internal/domain"
 	"github.com/jameynakama/whatnow/internal/httpapi"
 	"github.com/jameynakama/whatnow/internal/llm"
+	"github.com/jameynakama/whatnow/internal/planner"
 	"github.com/jameynakama/whatnow/internal/service"
 	"github.com/jameynakama/whatnow/internal/settings"
 	"github.com/jameynakama/whatnow/internal/store"
@@ -26,7 +28,7 @@ type testAPI struct {
 	handler  http.Handler
 	hub      *stream.Hub
 	captured []int64
-	// model answers the connection test. Nil means no provider is set up.
+	// model answers the connection test and the planner. Nil means no provider is set up.
 	model llm.Client
 }
 
@@ -37,9 +39,15 @@ func newAPI(t *testing.T) *testAPI {
 		t.Fatal(err)
 	}
 	t.Cleanup(func() { db.Close() })
-	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
-	svc := service.New(db, func() time.Time { return now })
+	now := func() time.Time { return time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC) } // 09:00 local
+	svc := service.New(db, now)
 	api := &testAPI{t: t, hub: stream.NewHub()}
+	active := func(context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
+		if api.model == nil {
+			return nil, settings.ProviderConfig{}, settings.Defaults(), settings.ErrNotConfigured
+		}
+		return api.model, settings.ProviderConfig{MainModel: "main"}, settings.Defaults(), nil
+	}
 	api.handler = httpapi.New(httpapi.Deps{
 		Service:   svc,
 		Hub:       api.hub,
@@ -52,6 +60,7 @@ func newAPI(t *testing.T) *testAPI {
 			}
 			return api.model, settings.ProviderConfig{MainModel: "main"}, nil
 		},
+		Planner: planner.New(svc, api.hub, active, testLoc, now),
 	})
 	return api
 }
```

`internal/httpapi/days_test.go`. Create it with:

```go
package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/stream"
)

type started struct {
	Day  domain.Day  `json:"day"`
	Plan domain.Plan `json:"plan"`
}

func TestStartTheDayThenReadToday(t *testing.T) {
	api := newAPI(t)
	due := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "ship it", "due_on": "2026-10-05"}))
	research := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "research"}))
	api.do(http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/blockers", due.Data.ID), map[string]int64{"blocked_by_id": research.Data.ID})

	before := decodeAs[planner.Today](t, api.do(http.MethodGet, "/api/v1/today", nil))
	if before.Date != "2026-10-05" || before.Day != nil || before.Plan != nil || len(before.Unplanned) != 2 {
		t.Errorf("before starting: %+v", before)
	}

	// No provider is set up, so the first plan is the fallback.
	rec := api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 2, "mood_note": "tired"})
	wantStatus(t, rec, http.StatusCreated)
	got := decodeAs[started](t, rec)
	if got.Day.Pace != 2 || got.Day.MoodNote != "tired" || !got.Plan.Fallback || len(got.Plan.Entries) != 2 {
		t.Errorf("started: %+v", got)
	}

	today := decodeAs[planner.Today](t, api.do(http.MethodGet, "/api/v1/today", nil))
	if today.Plan == nil || today.Plan.ID != got.Plan.ID || len(today.Unplanned) != 0 ||
		len(today.Blocked) != 1 || today.Blocked[0] != due.Data.ID {
		t.Errorf("today: %+v", today)
	}

	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3}), http.StatusConflict)
}

func TestTodayListsTasksAddedAfterThePlanAsUnplanned(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3}), http.StatusCreated)
	added := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "new thing"}))

	today := decodeAs[planner.Today](t, api.do(http.MethodGet, "/api/v1/today", nil))
	if len(today.Unplanned) != 1 || today.Unplanned[0] != added.Data.ID {
		t.Errorf("unplanned = %v", today.Unplanned)
	}
}

func TestSetPace(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/today/pace", map[string]any{"pace": 3}), http.StatusNotFound)
	api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3, "mood_note": "ok"})
	msgs, unsub := api.hub.Subscribe()
	defer unsub()

	rec := api.do(http.MethodPut, "/api/v1/today/pace", map[string]any{"pace": 5})
	wantStatus(t, rec, http.StatusOK)
	got := decodeAs[struct {
		BatchID int64      `json:"batch_id"`
		Data    domain.Day `json:"data"`
	}](t, rec)
	if got.BatchID == 0 || got.Data.Pace != 5 || got.Data.MoodNote != "ok" {
		t.Errorf("pace change: %+v", got)
	}
	if m := <-msgs; m.Type != "batch.applied" {
		t.Errorf("published %q", m.Type)
	}
	wantStatus(t, api.do(http.MethodPut, "/api/v1/today/pace", map[string]any{"pace": 0}), http.StatusBadRequest)
}

func TestWhatNow(t *testing.T) {
	api := newAPI(t)
	first := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "first"}))
	second := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "second"}))
	api.model = modelFunc(func(req llm.Request) (string, error) {
		return fmt.Sprintf(`{"reason":"quick win","task_id":%d}`, first.Data.ID), nil
	})

	rec := api.do(http.MethodPost, "/api/v1/whatnow", map[string]any{})
	wantStatus(t, rec, http.StatusOK)
	if got := decodeAs[planner.Pick](t, rec); got != (planner.Pick{TaskID: first.Data.ID, Reason: "quick win"}) {
		t.Errorf("pick = %+v", got)
	}

	// "Not this one": the reason lands on the task's feeling, and the model's repeat pick is refused.
	msgs, unsub := api.hub.Subscribe()
	defer unsub()
	rec = api.do(http.MethodPost, "/api/v1/whatnow", map[string]any{"exclude_task_id": first.Data.ID, "reason": "too fried"})
	wantStatus(t, rec, http.StatusOK)
	if got := decodeAs[planner.Pick](t, rec); got.TaskID != second.Data.ID || !got.Fallback {
		t.Errorf("pick = %+v", got)
	}
	if m := <-msgs; m.Type != "batch.applied" || len(m.Data.(stream.BatchApplied).Activity) != 1 {
		t.Errorf("published %+v", m)
	}
	list := decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil))
	if list[0].Feeling != "too fried" {
		t.Errorf("feeling = %q", list[0].Feeling)
	}
}

func TestWhatNowWithNothingToDo(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/whatnow", map[string]any{}), http.StatusNotFound)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/whatnow", map[string]any{"exclude_task_id": 99, "reason": "no"}), http.StatusNotFound)
}

func TestPushTask(t *testing.T) {
	api := newAPI(t)
	task := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "rfc"}))
	path := fmt.Sprintf("/api/v1/tasks/%d/push", task.Data.ID)

	got := decodeAs[taskMutation](t, api.do(http.MethodPost, path, map[string]string{"until": "2026-10-06"}))
	if got.Data.NotBefore == nil || *got.Data.NotBefore != "2026-10-06" {
		t.Errorf("pushed: %+v", got.Data)
	}
	wantStatus(t, api.do(http.MethodPost, path, map[string]string{"until": "monday"}), http.StatusBadRequest)
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./... && go vet ./...`
Expected: build fails: `unknown field Planner in struct literal of type httpapi.Deps`

- [ ] **Step 3: Implement**

`internal/planner/today.go`. Create it with:

```go
package planner

import (
	"context"

	"github.com/jameynakama/whatnow/internal/domain"
)

// Today is the state of the day for the home screen. Day and Plan are nil until the day starts.
type Today struct {
	Date domain.Date  `json:"date"`
	Day  *domain.Day  `json:"day"`
	Plan *domain.Plan `json:"plan"`
	// Unplanned are eligible tasks the plan leaves out, in fallback order.
	Unplanned []int64 `json:"unplanned"`
	// Blocked are eligible tasks still waiting on an open task.
	Blocked []int64 `json:"blocked"`
}

func (p *Planner) Today(ctx context.Context) (Today, error) {
	st, err := p.gather(ctx, false)
	if err != nil {
		return Today{}, err
	}
	t := Today{Date: st.facts.Today, Day: st.day, Plan: st.plan, Unplanned: []int64{}, Blocked: []int64{}}
	planned := map[int64]bool{}
	if st.plan != nil {
		for _, e := range st.plan.Entries {
			planned[e.TaskID] = true
		}
	}
	for _, tf := range st.facts.Tasks {
		if !planned[tf.Task.ID] {
			t.Unplanned = append(t.Unplanned, tf.Task.ID)
		}
		if len(tf.BlockedBy) > 0 {
			t.Blocked = append(t.Blocked, tf.Task.ID)
		}
	}
	return t, nil
}

// StartDay starts today at a pace and makes its first plan.
func (p *Planner) StartDay(ctx context.Context, pace domain.Pace, moodNote string) (domain.Day, domain.Plan, error) {
	day, err := p.svc.StartDay(ctx, p.Date(), pace, moodNote)
	if err != nil {
		return domain.Day{}, domain.Plan{}, err
	}
	plan, err := p.Replan(ctx, domain.TriggerMorning, "")
	return day, plan, err
}
```

`internal/httpapi/days.go`. Create it with:

```go
package httpapi

import (
	"net/http"

	"github.com/jameynakama/whatnow/internal/domain"
)

func (s *Server) today(w http.ResponseWriter, r *http.Request) {
	today, err := s.Planner.Today(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, today)
}

// Started is the response to starting the day.
type Started struct {
	Day  domain.Day  `json:"day"`
	Plan domain.Plan `json:"plan"`
}

// startDay starts today and waits for its first plan, so the morning flow can reveal it.
func (s *Server) startDay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pace     domain.Pace `json:"pace"`
		MoodNote string      `json:"mood_note"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	day, plan, err := s.Planner.StartDay(r.Context(), body.Pace, body.MoodNote)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, Started{Day: day, Plan: plan})
}

// setPace changes today's pace. The replan it causes arrives later as plan.updated.
func (s *Server) setPace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pace domain.Pace `json:"pace"`
		Note string      `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return s.Service.SetPace(r.Context(), batch, s.Planner.Date(), body.Pace, body.Note)
	})
}

// whatNow answers "what now?". With exclude_task_id it answers "not this one": that task is left
// out of this answer, and the optional reason is added to its feeling note first.
func (s *Server) whatNow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExcludeTaskID int64  `json:"exclude_task_id"`
		Reason        string `json:"reason"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	ctx := r.Context()
	if body.ExcludeTaskID != 0 && body.Reason != "" {
		batch, err := s.Service.NewBatch(ctx, domain.ActorUser)
		if err != nil {
			writeError(w, err)
			return
		}
		if _, err := s.Service.AppendFeeling(ctx, batch, body.ExcludeTaskID, body.Reason); err != nil {
			writeError(w, err)
			return
		}
		s.publishBatch(ctx, batch)
	}
	pick, err := s.Planner.WhatNow(ctx, body.ExcludeTaskID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pick)
}

func (s *Server) pushTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		Until domain.Date `json:"until"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return s.Service.PushTask(r.Context(), batch, id, body.Until)
	})
}
```

`internal/httpapi/server.go`. Apply:

```diff
--- a/internal/httpapi/server.go
+++ b/internal/httpapi/server.go
@@ -17,6 +17,7 @@ import (
 
 	"github.com/jameynakama/whatnow/internal/domain"
 	"github.com/jameynakama/whatnow/internal/llm"
+	"github.com/jameynakama/whatnow/internal/planner"
 	"github.com/jameynakama/whatnow/internal/service"
 	"github.com/jameynakama/whatnow/internal/settings"
 	"github.com/jameynakama/whatnow/internal/stream"
@@ -34,6 +35,7 @@ type Deps struct {
 	OnCapture func(task domain.Task)
 	// NewClient builds an LLM client from settings, for the connection test.
 	NewClient func(settings.Settings) (llm.Client, settings.ProviderConfig, error)
+	Planner   *planner.Planner
 }
 
 type Server struct {
@@ -54,6 +56,7 @@ func New(d Deps) http.Handler {
 	mux.HandleFunc("PUT /api/v1/tasks/{id}", s.updateTask)
 	mux.HandleFunc("POST /api/v1/tasks/{id}/status", s.setTaskStatus)
 	mux.HandleFunc("DELETE /api/v1/tasks/{id}", s.deleteTask)
+	mux.HandleFunc("POST /api/v1/tasks/{id}/push", s.pushTask)
 	mux.HandleFunc("GET /api/v1/blockers", s.listBlockers)
 	mux.HandleFunc("POST /api/v1/tasks/{id}/blockers", s.addBlocker)
 	mux.HandleFunc("DELETE /api/v1/tasks/{id}/blockers/{blockedBy}", s.removeBlocker)
@@ -62,6 +65,10 @@ func New(d Deps) http.Handler {
 	mux.HandleFunc("POST /api/v1/events", s.createEvent)
 	mux.HandleFunc("PUT /api/v1/events/{id}", s.updateEvent)
 	mux.HandleFunc("DELETE /api/v1/events/{id}", s.deleteEvent)
+	mux.HandleFunc("GET /api/v1/today", s.today)
+	mux.HandleFunc("POST /api/v1/today/start", s.startDay)
+	mux.HandleFunc("PUT /api/v1/today/pace", s.setPace)
+	mux.HandleFunc("POST /api/v1/whatnow", s.whatNow)
 	mux.HandleFunc("GET /api/v1/stream", s.stream)
 	mux.HandleFunc("GET /api/v1/settings", s.getSettings)
 	mux.HandleFunc("PUT /api/v1/settings", s.putSettings)
@@ -146,9 +153,9 @@ func writeError(w http.ResponseWriter, err error) {
 	switch {
 	case errors.Is(err, service.ErrInvalid), errors.Is(err, settings.ErrInvalid):
 		writeJSON(w, http.StatusBadRequest, errorBody{err.Error()})
-	case errors.Is(err, service.ErrNotFound):
+	case errors.Is(err, service.ErrNotFound), errors.Is(err, planner.ErrNothingToDo):
 		writeJSON(w, http.StatusNotFound, errorBody{err.Error()})
-	case errors.Is(err, service.ErrConflict):
+	case errors.Is(err, service.ErrConflict), errors.Is(err, planner.ErrNoDay):
 		writeJSON(w, http.StatusConflict, errorBody{err.Error()})
 	default:
 		log.Printf("internal error: %v", err)
```

`cmd/whatnow/main.go`. Apply:

```diff
--- a/cmd/whatnow/main.go
+++ b/cmd/whatnow/main.go
@@ -14,6 +14,7 @@ import (
 	"github.com/jameynakama/whatnow/internal/cleanup"
 	"github.com/jameynakama/whatnow/internal/httpapi"
 	"github.com/jameynakama/whatnow/internal/llm"
+	"github.com/jameynakama/whatnow/internal/planner"
 	"github.com/jameynakama/whatnow/internal/service"
 	"github.com/jameynakama/whatnow/internal/settings"
 	"github.com/jameynakama/whatnow/internal/store"
@@ -54,10 +55,14 @@ func serve(addr, dbPath string) error {
 
 	svc := service.New(db, time.Now)
 	hub := stream.NewHub()
-	runner := cleanup.New(svc, hub, func(ctx context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
+	active := func(ctx context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
 		return settings.Active(ctx, db)
-	}, time.Local, time.Now)
+	}
+	runner := cleanup.New(svc, hub, active, time.Local, time.Now)
 	go runner.Run(context.Background())
+	plans := planner.New(svc, hub, active, time.Local, time.Now)
+	watched, _ := hub.Subscribe() // for the life of the process
+	go plans.Run(context.Background(), watched)
 
 	handler := httpapi.New(httpapi.Deps{
 		Service:   svc,
@@ -66,6 +71,7 @@ func serve(addr, dbPath string) error {
 		Loc:       time.Local,
 		OnCapture: runner.Enqueue,
 		NewClient: settings.NewClient,
+		Planner:   plans,
 	})
 	// No WriteTimeout: it would cut off the long-lived SSE stream.
 	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./... && go vet ./...`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Run the whole suite with the race detector**

Run: `go test -race ./... && just cover`
Expected: PASS. Total coverage around 83%; what's left uncovered is database-error paths.

- [ ] **Step 6: Commit**

```bash
git add cmd/whatnow/main.go internal/httpapi/days.go internal/httpapi/days_test.go internal/httpapi/helpers_test.go internal/httpapi/server.go internal/planner/today.go
git commit -m "Add day, pace, what-now, and push endpoints"
```
