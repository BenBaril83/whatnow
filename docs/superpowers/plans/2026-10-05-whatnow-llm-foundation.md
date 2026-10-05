# whatnow LLM Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Captured text turns itself into tidy cards. A user sets up a provider and key once, and every raw capture is cleaned up in the background by one structured LLM call, applied as one undoable agent batch.

**Architecture:** `llm` is a provider-neutral `Client` with two implementations: the Claude API and OpenAI-compatible chat completions. `settings` stores configuration as one JSON document in SQLite and keeps API keys in the OS keychain. `prompt` assembles layered system prompts. `cleanup` is a background runner that `httpapi` feeds through an `OnCapture` hook; it writes only through `service`, so its changes are logged, undoable, and broadcast like any other batch.

**Tech Stack:** Go 1.27, `github.com/anthropics/anthropic-sdk-go` v1.78.0, `github.com/openai/openai-go/v3` v3.71.2, `github.com/zalando/go-keyring` v0.2.8, plus everything from plan 1.

**Spec:** `docs/superpowers/specs/2026-10-05-whatnow-design.md`

**Where this sits:** plan 2. Plan 1 (core) is merged. Later plans:
3. Days and planning: `facts`, good morning, planner, what now, wrap up, day summaries
4. Agent chat
5. Web UI
6. Packaging and prompt evals

**Decisions this plan makes:**
- **`facts` moves to plan 3.** Nothing in this plan consumes it, and the planner is its first real caller.
- **Structured outputs, not forced tool calls.** Claude Sonnet 5.5 and Opus 5.5 reject forced `tool_choice` with a 400, so cleanup asks for JSON through `output_config.format` on Claude and `response_format` (strict JSON schema) on OpenAI-compatible servers. Both are supported on the default models.
- **The light model ID is `claude-haiku-4-5`.** The spec's dated ID is wrong: current model IDs take no date suffix. Task 2 fixes the spec.
- **Server-side refusal fallbacks wait for plan 3.** They need the beta endpoint and apply to the main-tier models, and the first main-model caller arrives in plan 3. Here, a refusal is just a failed cleanup: the card stays raw and the UI gets `cleanup.failed`.
- **No streaming yet.** Cleanup and the connection test are short calls. Streaming arrives with agent chat in plan 4.
- **The keychain seam is go-keyring's own `MockInit`.** No wrapper interface.
- **Tasks split out of a note keep the note's raw text**, so every card shows where it came from.
- **A note that holds only events deletes its original card** in the same batch, so one undo brings it back.

## Global Constraints

- Module path: `github.com/jameynakama/whatnow`. Go directive: `go 1.27`.
- SDK versions pinned exactly as in the Tech Stack line.
- API keys are never stored in SQLite, never returned by the API, and never logged.
- Default models: main `claude-sonnet-5-5`, light `claude-haiku-4-5`. OpenAI-compatible models have no defaults.
- Style is at most 500 characters and about me at most 1000, counted in runes.
- All API routes live under `/api/v1/`.
- Tests use stdlib `testing` and `go-cmp`. Tests never call a real provider: they use `httptest` servers or a fake `llm.Client`.
- Tests that touch keys call `keyring.MockInit()` first, so they never read or write the real keychain.
- Commit messages: plain imperative subject, no `Co-Authored-By` trailer.

## Review Focus

1. **The user edits a card while cleanup is still waiting on the model.** Expected: cleanup leaves the card alone and the edit stands. Pinned in Task 6.
2. **The model returns something unusable** (an unknown blocker ref, an invalid size, a malformed time, a blocker on a card it chose to delete). Expected: nothing is half-applied; the raw card is exactly as captured. Pinned in Task 6.
3. **No provider or key is set up.** Expected: capture works, the card stays raw, and no failure is reported. Pinned in Tasks 2 and 6.
4. **An API key leaks** through an API response or a provider error message. Expected: never. Pinned in Tasks 1 and 5.
5. **An event without a date or zone** ("standup at 10"). Expected: today, at 10:00 in the user's time zone, not UTC. Pinned in Task 6.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/llm/llm.go` | `Client`, `Request`, `Block`, and the refusal and truncation errors |
| `internal/llm/anthropic.go` | Claude API implementation |
| `internal/llm/openai.go` | OpenAI-compatible implementation |
| `internal/store/migrations/0002_settings.sql` | The settings table |
| `internal/settings/settings.go` | Settings document: defaults, validation, load, save |
| `internal/settings/keys.go` | Keychain keys with environment fallback; building the active client |
| `internal/prompt/prompt.go` | Layered system prompts |
| `internal/stream/hub.go` | Gains `BatchApplied` and `PublishBatch`, shared by the API and cleanup |
| `internal/httpapi/server.go` | `Deps`; settings routes |
| `internal/httpapi/settings.go` | Settings, key, and connection-test handlers |
| `internal/cleanup/cleanup.go` | Queue, runner, one cleanup call, applying the result |
| `internal/cleanup/result.go` | The reply's Go shape and its mapping onto service fields |
| `internal/cleanup/schema.go` | The reply's JSON schema |
| `internal/cleanup/instructions.go` | Cleanup's core instructions |
| `cmd/whatnow/main.go` | Wires the runner into the server |

---

### Task 1: The `llm` package

**Files:**
- Create: `internal/llm/llm.go`, `internal/llm/anthropic.go`, `internal/llm/openai.go`
- Test: `internal/llm/llm_test.go`

**Interfaces:**
- Produces:
  - `llm.Client` interface: `Complete(ctx context.Context, req llm.Request) (string, error)`
  - `llm.Request{Model string; System []llm.Block; User string; Schema map[string]any; SchemaName string; MaxTokens int64}`
  - `llm.Block{Text string; Cache bool}`
  - `llm.ErrRefused`, `llm.ErrTruncated`
  - `llm.NewAnthropic(apiKey, baseURL string) llm.Client`, `llm.NewOpenAI(apiKey, baseURL string) llm.Client`. An empty `baseURL` uses the provider's default endpoint.

- [ ] **Step 1: Add the SDKs**

```bash
go get github.com/anthropics/anthropic-sdk-go@v1.78.0 github.com/openai/openai-go/v3@v3.71.2
```

- [ ] **Step 2: Write the failing tests**

The tests stand up an `httptest` server that plays the provider, so they check the exact request each SDK sends and how each reply is read. Create `internal/llm/llm_test.go`:

```go
package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/llm"
)

// fakeAPI serves one canned reply and records the request body it received.
type fakeAPI struct {
	status int
	reply  string
	path   string
	body   map[string]any
}

func (f *fakeAPI) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &f.body); err != nil {
			t.Errorf("request body is not JSON: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		io.WriteString(w, f.reply)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

var schema = map[string]any{
	"type":                 "object",
	"properties":           map[string]any{"ok": map[string]any{"type": "boolean"}},
	"required":             []string{"ok"},
	"additionalProperties": false,
}

func request() llm.Request {
	return llm.Request{
		Model:      "test-model",
		System:     []llm.Block{{Text: "core"}, {Text: "about me", Cache: true}, {Text: "context"}},
		User:       "do FLEX1-666 today",
		Schema:     schema,
		SchemaName: "result",
		MaxTokens:  512,
	}
}

func anthropicReply(stopReason, text string) string {
	b, _ := json.Marshal(map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "test-model",
		"content":     []map[string]any{{"type": "text", "text": text}},
		"stop_reason": stopReason,
		"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
	})
	return string(b)
}

func TestAnthropicRequestAndReply(t *testing.T) {
	api := &fakeAPI{status: 200, reply: anthropicReply("end_turn", `{"ok":true}`)}
	client := llm.NewAnthropic("sk-test", api.start(t))

	got, err := client.Complete(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"ok":true}` {
		t.Errorf("reply = %q", got)
	}

	if api.path != "/v1/messages" || api.body["model"] != "test-model" || api.body["max_tokens"] != float64(512) {
		t.Errorf("unexpected request: path %s, body %v", api.path, api.body)
	}
	system := api.body["system"].([]any)
	if len(system) != 3 {
		t.Fatalf("system has %d blocks, want 3", len(system))
	}
	for i, wantCache := range []bool{false, true, false} {
		_, hasCache := system[i].(map[string]any)["cache_control"]
		if hasCache != wantCache {
			t.Errorf("system[%d] cache_control present = %v, want %v", i, hasCache, wantCache)
		}
	}
	format := api.body["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("output_config.format = %v", format)
	}
}

func TestOpenAIRequestAndReply(t *testing.T) {
	reply, _ := json.Marshal(map[string]any{
		"id": "c1", "object": "chat.completion", "created": 1, "model": "test-model",
		"choices": []map[string]any{{
			"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": `{"ok":true}`, "refusal": nil},
		}},
	})
	api := &fakeAPI{status: 200, reply: string(reply)}
	client := llm.NewOpenAI("sk-test", api.start(t))

	got, err := client.Complete(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"ok":true}` {
		t.Errorf("reply = %q", got)
	}

	if api.path != "/chat/completions" {
		t.Errorf("path = %s", api.path)
	}
	messages := api.body["messages"].([]any)
	system := messages[0].(map[string]any)
	if system["role"] != "system" || system["content"] != "core\n\nabout me\n\ncontext" {
		t.Errorf("system message = %v", system)
	}
	format := api.body["response_format"].(map[string]any)
	jsonSchema := format["json_schema"].(map[string]any)
	if format["type"] != "json_schema" || jsonSchema["name"] != "result" || jsonSchema["strict"] != true {
		t.Errorf("response_format = %v", format)
	}
}

func TestStopReasonsBecomeErrors(t *testing.T) {
	openAIReply := func(finish, refusal string) string {
		var r any
		if refusal != "" {
			r = refusal
		}
		b, _ := json.Marshal(map[string]any{
			"id": "c1", "object": "chat.completion", "created": 1, "model": "m",
			"choices": []map[string]any{{"index": 0, "finish_reason": finish,
				"message": map[string]any{"role": "assistant", "content": "", "refusal": r}}},
		})
		return string(b)
	}
	cases := []struct {
		name   string
		client func(url string) llm.Client
		reply  string
		want   error
	}{
		{"anthropic refusal", func(u string) llm.Client { return llm.NewAnthropic("k", u) }, anthropicReply("refusal", ""), llm.ErrRefused},
		{"anthropic max tokens", func(u string) llm.Client { return llm.NewAnthropic("k", u) }, anthropicReply("max_tokens", `{"ok":`), llm.ErrTruncated},
		{"openai refusal", func(u string) llm.Client { return llm.NewOpenAI("k", u) }, openAIReply("stop", "I can't help with that"), llm.ErrRefused},
		{"openai length", func(u string) llm.Client { return llm.NewOpenAI("k", u) }, openAIReply("length", ""), llm.ErrTruncated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &fakeAPI{status: 200, reply: c.reply}
			_, err := c.client(api.start(t)).Complete(context.Background(), request())
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestAuthErrorDoesNotLeakTheKey(t *testing.T) {
	const key = "sk-very-secret-123"
	body := `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`
	for name, newClient := range map[string]func(string, string) llm.Client{"anthropic": llm.NewAnthropic, "openai": llm.NewOpenAI} {
		t.Run(name, func(t *testing.T) {
			api := &fakeAPI{status: 401, reply: body}
			_, err := newClient(key, api.start(t)).Complete(context.Background(), request())
			if err == nil {
				t.Fatal("expected an error for a 401")
			}
			if strings.Contains(err.Error(), key) {
				t.Errorf("error message contains the API key: %v", err)
			}
		})
	}
}

func TestOpenAISkipsAnEmptySystemMessage(t *testing.T) {
	reply, _ := json.Marshal(map[string]any{
		"id": "c1", "object": "chat.completion", "created": 1, "model": "m",
		"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": "ok", "refusal": nil}}},
	})
	api := &fakeAPI{status: 200, reply: string(reply)}

	_, err := llm.NewOpenAI("k", api.start(t)).Complete(context.Background(), llm.Request{Model: "m", User: "hi", MaxTokens: 16})
	if err != nil {
		t.Fatal(err)
	}
	messages := api.body["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["role"] != "user" {
		t.Errorf("messages = %v, want only the user message", messages)
	}
	if _, ok := api.body["response_format"]; ok {
		t.Error("response_format sent without a schema")
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/llm/`
Expected: FAIL, `no non-test Go files` (the package doesn't exist yet).

- [ ] **Step 4: Write the package**

Create `internal/llm/llm.go`:

```go
// Package llm is the provider-neutral way to call a language model.
package llm

import (
	"context"
	"errors"
)

var (
	// ErrRefused means the model declined the request.
	ErrRefused = errors.New("model declined the request")
	// ErrTruncated means the reply hit the token limit before it finished.
	ErrTruncated = errors.New("model reply was cut off")
)

// Client calls one provider. The Claude API and OpenAI-compatible APIs implement it.
type Client interface {
	Complete(ctx context.Context, req Request) (string, error)
}

// Block is one layer of a system prompt. Cache marks the end of a prefix that stays the same
// across calls, so providers with prompt caching can reuse it.
type Block struct {
	Text  string
	Cache bool
}

type Request struct {
	Model  string
	System []Block
	User   string
	// Schema, when set, constrains the reply to JSON matching it. SchemaName labels the schema
	// for providers that require a name.
	Schema     map[string]any
	SchemaName string
	MaxTokens  int64
}
```

Create `internal/llm/anthropic.go`:

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

func (a *anthropicClient) Complete(ctx context.Context, req Request) (string, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: req.MaxTokens,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(req.User))},
	}
	for _, b := range req.System {
		block := anthropic.TextBlockParam{Text: b.Text}
		if b.Cache {
			block.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
		params.System = append(params.System, block)
	}
	if req.Schema != nil {
		params.OutputConfig = anthropic.OutputConfigParam{Format: anthropic.JSONOutputFormatParam{Schema: req.Schema}}
	}

	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	switch msg.StopReason {
	case anthropic.StopReasonRefusal:
		return "", ErrRefused
	case anthropic.StopReasonMaxTokens:
		return "", ErrTruncated
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	return text.String(), nil
}
```

Create `internal/llm/openai.go`:

```go
package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

type openAIClient struct {
	client openai.Client
}

// NewOpenAI returns a Client for the OpenAI chat completions API or any server compatible with
// it, such as Ollama, LM Studio, or OpenRouter. An empty baseURL uses OpenAI.
func NewOpenAI(apiKey, baseURL string) Client {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &openAIClient{client: openai.NewClient(opts...)}
}

func (o *openAIClient) Complete(ctx context.Context, req Request) (string, error) {
	params := openai.ChatCompletionNewParams{
		Model:               shared.ChatModel(req.Model),
		MaxCompletionTokens: openai.Int(req.MaxTokens),
	}
	// Chat completions take one system message, so the layers are joined in order.
	if len(req.System) > 0 {
		layers := make([]string, len(req.System))
		for i, b := range req.System {
			layers[i] = b.Text
		}
		params.Messages = append(params.Messages, openai.SystemMessage(strings.Join(layers, "\n\n")))
	}
	params.Messages = append(params.Messages, openai.UserMessage(req.User))
	if req.Schema != nil {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   req.SchemaName,
					Schema: req.Schema,
					Strict: openai.Bool(true),
				},
			},
		}
	}

	resp, err := o.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("openai: reply had no choices")
	}
	choice := resp.Choices[0]
	if choice.Message.Refusal != "" {
		return "", ErrRefused
	}
	if choice.FinishReason == "length" {
		return "", ErrTruncated
	}
	return choice.Message.Content, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go mod tidy && go test ./internal/llm/ && go vet ./...`
Expected: PASS, no vet output.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/llm
git commit -m "Add provider-neutral LLM client for Claude and OpenAI-compatible APIs"
```

---

### Task 2: Settings and keys

**Files:**
- Create: `internal/store/migrations/0002_settings.sql`
- Modify: `internal/store/store_test.go` (the table list in `TestOpenCreatesSchema`)
- Create: `internal/settings/settings.go`, `internal/settings/keys.go`
- Modify: `docs/superpowers/specs/2026-10-05-whatnow-design.md` (light model ID)
- Test: `internal/settings/settings_test.go`

**Interfaces:**
- Consumes: `store.Open`; `llm.Client`, `llm.NewAnthropic`, `llm.NewOpenAI`.
- Produces:
  - `settings.Provider` (`settings.Anthropic`, `settings.OpenAI`) with `Valid() bool`
  - `settings.ProviderConfig{MainModel, LightModel, BaseURL string}`
  - `settings.Settings{Provider; Anthropic, OpenAI ProviderConfig; Style, AboutMe string}` with `Config() ProviderConfig` and `Validate() error`. JSON tags: `provider`, `anthropic`, `openai`, `style`, `about_me`, `main_model`, `light_model`, `base_url`.
  - `settings.Defaults() Settings`, `settings.Load(ctx, *sql.DB) (Settings, error)`, `settings.Save(ctx, *sql.DB, Settings) error`
  - `settings.MaxStyleRunes`, `settings.MaxAboutMeRunes`, `settings.ErrInvalid`, `settings.ErrNotConfigured`
  - `settings.KeySource` (`KeyFromKeychain` = `"keychain"`, `KeyFromEnv` = `"env"`, empty for none)
  - `settings.Key(Provider) (string, KeySource, error)`, `settings.SetKey(Provider, string) error`, `settings.DeleteKey(Provider) error`
  - `settings.NewClient(Settings) (llm.Client, ProviderConfig, error)`
  - `settings.Active(ctx, *sql.DB) (llm.Client, ProviderConfig, Settings, error)`

- [ ] **Step 1: Add go-keyring**

```bash
go get github.com/zalando/go-keyring@v0.2.8
```

- [ ] **Step 2: Write the failing tests**

In `internal/store/store_test.go`, add `"settings"` to the table list in `TestOpenCreatesSchema`:

```go
	for _, table := range []string{"tasks", "blockers", "events", "batches", "activity", "settings"} {
```

Create `internal/settings/settings_test.go`:

```go
package settings_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
)

func TestLoadDefaultsThenSaveRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	got, err := settings.Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(settings.Defaults(), got); diff != "" {
		t.Errorf("fresh load differs from defaults:\n%s", diff)
	}

	want := settings.Defaults()
	want.Provider = settings.Anthropic
	want.Style = "gentle, like Thich Nhat Hanh"
	want.AboutMe = "Code review drains me. Writing is easy."
	if err := settings.Save(ctx, db, want); err != nil {
		t.Fatal(err)
	}
	got, err = settings.Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("saved settings differ (-want +got):\n%s", diff)
	}
}

func TestValidate(t *testing.T) {
	valid := settings.Defaults()
	valid.Provider = settings.Anthropic
	cases := map[string]func(*settings.Settings){
		"unknown provider":              func(s *settings.Settings) { s.Provider = "gemini" },
		"openai without models":         func(s *settings.Settings) { s.Provider = settings.OpenAI },
		"base URL without scheme":       func(s *settings.Settings) { s.OpenAI.BaseURL = "localhost:11434" },
		"style over 500 characters":     func(s *settings.Settings) { s.Style = strings.Repeat("é", 501) },
		"about me over 1000 characters": func(s *settings.Settings) { s.AboutMe = strings.Repeat("🧠", 1001) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := valid
			mutate(&s)
			if err := s.Validate(); !errors.Is(err, settings.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}

	atLimit := valid
	atLimit.Style = strings.Repeat("é", 500) // 1000 bytes, 500 characters
	atLimit.OpenAI.BaseURL = "http://localhost:11434/v1"
	if err := atLimit.Validate(); err != nil {
		t.Errorf("settings at the limits rejected: %v", err)
	}
}

func TestKeysPreferKeychainOverEnv(t *testing.T) {
	keyring.MockInit()
	t.Setenv("ANTHROPIC_API_KEY", "from-env")

	key, source, err := settings.Key(settings.Anthropic)
	if err != nil || key != "from-env" || source != settings.KeyFromEnv {
		t.Fatalf("env fallback: %q %q %v", key, source, err)
	}

	if err := settings.SetKey(settings.Anthropic, "  from-keychain \n"); err != nil {
		t.Fatal(err)
	}
	key, source, err = settings.Key(settings.Anthropic)
	if err != nil || key != "from-keychain" || source != settings.KeyFromKeychain {
		t.Fatalf("keychain: %q %q %v", key, source, err)
	}

	if err := settings.DeleteKey(settings.Anthropic); err != nil {
		t.Fatal(err)
	}
	if err := settings.DeleteKey(settings.Anthropic); err != nil {
		t.Errorf("deleting a missing key: %v", err)
	}
	if _, source, _ := settings.Key(settings.Anthropic); source != settings.KeyFromEnv {
		t.Errorf("after delete, source = %q, want env", source)
	}
	if err := settings.SetKey(settings.Anthropic, "   "); !errors.Is(err, settings.ErrInvalid) {
		t.Errorf("blank key: err = %v, want ErrInvalid", err)
	}
}

func TestNewClientNeedsProviderAndKey(t *testing.T) {
	keyring.MockInit()
	t.Setenv("OPENAI_API_KEY", "")
	s := settings.Defaults()

	if _, _, err := settings.NewClient(s); !errors.Is(err, settings.ErrNotConfigured) {
		t.Errorf("no provider: err = %v, want ErrNotConfigured", err)
	}
	s.Provider = settings.OpenAI
	s.OpenAI = settings.ProviderConfig{MainModel: "big", LightModel: "small"}
	if _, _, err := settings.NewClient(s); !errors.Is(err, settings.ErrNotConfigured) {
		t.Errorf("no key: err = %v, want ErrNotConfigured", err)
	}
	if err := settings.SetKey(settings.OpenAI, "sk-test"); err != nil {
		t.Fatal(err)
	}
	client, cfg, err := settings.NewClient(s)
	if err != nil || client == nil || cfg.LightModel != "small" {
		t.Errorf("configured: client %v, cfg %+v, err %v", client, cfg, err)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/store/ ./internal/settings/`
Expected: FAIL. The store test reports `table "settings" missing`; the settings package doesn't build.

- [ ] **Step 4: Write the migration and the package**

Create `internal/store/migrations/0002_settings.sql`:

```sql
-- One row holding the settings document as JSON. New settings fields need no migration.
CREATE TABLE settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    body TEXT NOT NULL
);
```

Create `internal/settings/settings.go`:

```go
// Package settings holds the user's configuration and API keys. Settings live in SQLite as one
// JSON document. API keys live in the OS keychain, never in the database.
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid settings")

type Provider string

const (
	Anthropic Provider = "anthropic"
	OpenAI    Provider = "openai"
)

func (p Provider) Valid() bool { return p == Anthropic || p == OpenAI }

// ProviderConfig picks the models for one provider. Main runs the agent, plans, and "what now?";
// light runs cleanup and day summaries.
type ProviderConfig struct {
	MainModel  string `json:"main_model"`
	LightModel string `json:"light_model"`
	BaseURL    string `json:"base_url"`
}

type Settings struct {
	// Provider is empty until the user sets one up.
	Provider  Provider       `json:"provider"`
	Anthropic ProviderConfig `json:"anthropic"`
	OpenAI    ProviderConfig `json:"openai"`
	Style     string         `json:"style"`
	AboutMe   string         `json:"about_me"`
}

const (
	MaxStyleRunes   = 500
	MaxAboutMeRunes = 1000
)

// Defaults has Claude models filled in. OpenAI-compatible models are left for the user, since
// they depend on the server.
func Defaults() Settings {
	return Settings{Anthropic: ProviderConfig{MainModel: "claude-sonnet-5-5", LightModel: "claude-haiku-4-5"}}
}

// Config returns the active provider's model settings.
func (s Settings) Config() ProviderConfig {
	if s.Provider == OpenAI {
		return s.OpenAI
	}
	return s.Anthropic
}

func (s Settings) Validate() error {
	if s.Provider != "" && !s.Provider.Valid() {
		return fmt.Errorf("%w: provider %q", ErrInvalid, s.Provider)
	}
	if s.Provider != "" {
		if cfg := s.Config(); cfg.MainModel == "" || cfg.LightModel == "" {
			return fmt.Errorf("%w: %s needs a main and a light model", ErrInvalid, s.Provider)
		}
	}
	for _, cfg := range []ProviderConfig{s.Anthropic, s.OpenAI} {
		if cfg.BaseURL == "" {
			continue
		}
		u, err := url.Parse(cfg.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%w: base URL %q must be an http or https URL", ErrInvalid, cfg.BaseURL)
		}
	}
	if n := utf8.RuneCountInString(s.Style); n > MaxStyleRunes {
		return fmt.Errorf("%w: style is %d characters, the limit is %d", ErrInvalid, n, MaxStyleRunes)
	}
	if n := utf8.RuneCountInString(s.AboutMe); n > MaxAboutMeRunes {
		return fmt.Errorf("%w: about me is %d characters, the limit is %d", ErrInvalid, n, MaxAboutMeRunes)
	}
	return nil
}

// Load returns the saved settings, or the defaults if nothing has been saved.
func Load(ctx context.Context, db *sql.DB) (Settings, error) {
	s := Defaults()
	var body string
	err := db.QueryRowContext(ctx, `SELECT body FROM settings WHERE id = 1`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return Settings{}, err
	}
	err = json.Unmarshal([]byte(body), &s)
	return s, err
}

func Save(ctx context.Context, db *sql.DB, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO settings (id, body) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET body = excluded.body`, string(body))
	return err
}
```

Create `internal/settings/keys.go`:

```go
package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/llm"
)

// ErrNotConfigured means no provider is chosen, or the chosen one has no key.
var ErrNotConfigured = errors.New("no LLM provider is set up")

// KeySource says where a provider's key comes from. Empty means there is no key.
type KeySource string

const (
	KeyFromKeychain KeySource = "keychain"
	KeyFromEnv      KeySource = "env"
)

const keychainService = "whatnow"

var envVars = map[Provider]string{Anthropic: "ANTHROPIC_API_KEY", OpenAI: "OPENAI_API_KEY"}

// Key returns a provider's API key from the keychain, falling back to the provider's usual
// environment variable.
func Key(p Provider) (string, KeySource, error) {
	key, err := keyring.Get(keychainService, string(p))
	if err == nil {
		return key, KeyFromKeychain, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return "", "", fmt.Errorf("read %s key from keychain: %w", p, err)
	}
	if key := os.Getenv(envVars[p]); key != "" {
		return key, KeyFromEnv, nil
	}
	return "", "", nil
}

func SetKey(p Provider, key string) error {
	if !p.Valid() {
		return fmt.Errorf("%w: provider %q", ErrInvalid, p)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("%w: key is empty", ErrInvalid)
	}
	return keyring.Set(keychainService, string(p), key)
}

// DeleteKey removes a provider's key from the keychain. Deleting a key that isn't there is fine.
func DeleteKey(p Provider) error {
	if !p.Valid() {
		return fmt.Errorf("%w: provider %q", ErrInvalid, p)
	}
	if err := keyring.Delete(keychainService, string(p)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

// NewClient returns a client for the active provider, along with its model settings.
func NewClient(s Settings) (llm.Client, ProviderConfig, error) {
	if s.Provider == "" {
		return nil, ProviderConfig{}, ErrNotConfigured
	}
	key, _, err := Key(s.Provider)
	if err != nil {
		return nil, ProviderConfig{}, err
	}
	if key == "" {
		return nil, ProviderConfig{}, ErrNotConfigured
	}
	cfg := s.Config()
	if s.Provider == OpenAI {
		return llm.NewOpenAI(key, cfg.BaseURL), cfg, nil
	}
	return llm.NewAnthropic(key, cfg.BaseURL), cfg, nil
}

// Active loads the saved settings and returns a client for them.
func Active(ctx context.Context, db *sql.DB) (llm.Client, ProviderConfig, Settings, error) {
	s, err := Load(ctx, db)
	if err != nil {
		return nil, ProviderConfig{}, Settings{}, err
	}
	client, cfg, err := NewClient(s)
	return client, cfg, s, err
}
```

- [ ] **Step 5: Fix the light model ID in the spec**

In `docs/superpowers/specs/2026-10-05-whatnow-design.md`, in the provider models table, change `` `claude-haiku-4-5-20251001` `` to `` `claude-haiku-4-5` ``.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go mod tidy && go test ./... && go vet ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/store internal/settings docs/superpowers/specs
git commit -m "Add settings with keychain-held API keys"
```

---

### Task 3: Layered prompts

**Files:**
- Create: `internal/prompt/prompt.go`
- Test: `internal/prompt/prompt_test.go`

**Interfaces:**
- Consumes: `llm.Block`.
- Produces: `prompt.System(core, style, aboutMe, context string) []llm.Block`. Layers in that order, empty ones skipped, `Cache` set on the last of core, style, and about me.

- [ ] **Step 1: Write the failing tests**

Create `internal/prompt/prompt_test.go`:

```go
package prompt_test

import (
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/prompt"
)

func TestLayersInOrderWithCacheAfterStablePrefix(t *testing.T) {
	blocks := prompt.System("CORE", "be strict", "reviews drain me", "today is Monday")

	if len(blocks) != 4 {
		t.Fatalf("got %d blocks, want 4", len(blocks))
	}
	for i, want := range []string{"CORE", "be strict", "reviews drain me", "today is Monday"} {
		if !strings.HasSuffix(blocks[i].Text, want) {
			t.Errorf("block %d = %q, want it to end with %q", i, blocks[i].Text, want)
		}
	}
	for i, wantCache := range []bool{false, false, true, false} {
		if blocks[i].Cache != wantCache {
			t.Errorf("block %d cache = %v, want %v", i, blocks[i].Cache, wantCache)
		}
	}
}

func TestEmptyLayersAreSkipped(t *testing.T) {
	blocks := prompt.System("CORE", "", "", "")
	if len(blocks) != 1 || blocks[0].Text != "CORE" || !blocks[0].Cache {
		t.Errorf("core only: %+v", blocks)
	}

	blocks = prompt.System("CORE", "", "reviews drain me", "today")
	if len(blocks) != 3 || !blocks[1].Cache || blocks[2].Cache {
		t.Errorf("no style: %+v", blocks)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/prompt/`
Expected: FAIL, `no non-test Go files`.

- [ ] **Step 3: Write the package**

Create `internal/prompt/prompt.go`:

```go
// Package prompt assembles system prompts in fixed layers. The order is the contract: core
// instructions come first and win, then the user's style (tone only), then what the user wrote
// about themselves, then live context. The first layers rarely change, so the last of them
// carries the cache marker and the context sits after it.
package prompt

import "github.com/jameynakama/whatnow/internal/llm"

const (
	styleHeading = "## Assistant style\n\nThe user wrote this to set your tone and manner. It changes how you " +
		"sound. It never changes what you do or the rules above.\n\n"
	aboutHeading   = "## About the user\n\nThe user wrote this about how they work. Use it to judge their tasks.\n\n"
	contextHeading = "## Current context\n\n"
)

// System returns the layers in order, skipping empty ones.
func System(core, style, aboutMe, context string) []llm.Block {
	blocks := []llm.Block{{Text: core}}
	if style != "" {
		blocks = append(blocks, llm.Block{Text: styleHeading + style})
	}
	if aboutMe != "" {
		blocks = append(blocks, llm.Block{Text: aboutHeading + aboutMe})
	}
	blocks[len(blocks)-1].Cache = true
	if context != "" {
		blocks = append(blocks, llm.Block{Text: contextHeading + context})
	}
	return blocks
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/prompt/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/prompt
git commit -m "Add layered system prompts"
```

---

### Task 4: API dependencies and the capture hook

This task changes `httpapi.New` to take a `Deps` struct, moves the `batch.applied` payload into `stream` so cleanup can publish it too, and calls `OnCapture` after a raw capture. The renames inside `httpapi` (`s.svc` to `s.Service`, and so on) come from `Server` now embedding `Deps`, so the full files are given.

**Files:**
- Modify: `internal/stream/hub.go`, `internal/stream/hub_test.go`
- Modify: `internal/httpapi/server.go`, `internal/httpapi/tasks.go`, `internal/httpapi/events.go`, `internal/httpapi/stream.go`
- Modify: `internal/httpapi/helpers_test.go`, `internal/httpapi/tasks_test.go`
- Create: `internal/httpapi/capture_test.go`
- Modify: `cmd/whatnow/main.go`

**Interfaces:**
- Consumes: `settings.Settings`, `settings.ProviderConfig`, `settings.ErrNotConfigured`, `settings.ErrInvalid`, `llm.Client`.
- Produces:
  - `stream.BatchApplied{BatchID int64; Activity []domain.Activity}` (moved from `httpapi`, same JSON) and `(*stream.Hub).PublishBatch(batch int64, activity []domain.Activity)`
  - `httpapi.Deps{Service *service.Service; Hub *stream.Hub; DB *sql.DB; Loc *time.Location; OnCapture func(taskID int64); NewClient func(settings.Settings) (llm.Client, settings.ProviderConfig, error)}`
  - `httpapi.New(d httpapi.Deps) http.Handler`
  - `writeError` maps `settings.ErrInvalid` to 400, like `service.ErrInvalid`.
  - Route registrations for Task 5's handlers are added in Task 5, not here.

- [ ] **Step 1: Write the failing tests**

Append to `internal/stream/hub_test.go`, and add `"github.com/jameynakama/whatnow/internal/domain"` to its imports:

```go
func TestPublishBatchSendsTypedPayload(t *testing.T) {
	hub := stream.NewHub()
	ch, unsub := hub.Subscribe()
	defer unsub()

	hub.PublishBatch(7, []domain.Activity{{ID: 1, BatchID: 7}})

	m := <-ch
	applied, ok := m.Data.(stream.BatchApplied)
	if m.Type != "batch.applied" || !ok || applied.BatchID != 7 || len(applied.Activity) != 1 {
		t.Errorf("message = %+v", m)
	}
}
```

Replace `internal/httpapi/helpers_test.go` with:

```go
package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

var testLoc = time.FixedZone("UTC-5", -5*3600)

type testAPI struct {
	t        *testing.T
	handler  http.Handler
	hub      *stream.Hub
	captured []int64
	// model answers the connection test. Nil means no provider is set up.
	model llm.Client
}

func newAPI(t *testing.T) *testAPI {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	svc := service.New(db, func() time.Time { return now })
	api := &testAPI{t: t, hub: stream.NewHub()}
	api.handler = httpapi.New(httpapi.Deps{
		Service:   svc,
		Hub:       api.hub,
		DB:        db,
		Loc:       testLoc,
		OnCapture: func(id int64) { api.captured = append(api.captured, id) },
		NewClient: func(settings.Settings) (llm.Client, settings.ProviderConfig, error) {
			if api.model == nil {
				return nil, settings.ProviderConfig{}, settings.ErrNotConfigured
			}
			return api.model, settings.ProviderConfig{MainModel: "main"}, nil
		},
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
```

In `internal/httpapi/tasks_test.go`, the payload type moved: change `m.Data.(httpapi.BatchApplied)` to `m.Data.(stream.BatchApplied)`, and in the imports replace `"github.com/jameynakama/whatnow/internal/httpapi"` with `"github.com/jameynakama/whatnow/internal/stream"`.

Create `internal/httpapi/capture_test.go`:

```go
package httpapi_test

import (
	"net/http"
	"testing"
)

func TestOnlyRawCapturesTriggerCleanup(t *testing.T) {
	api := newAPI(t)
	raw := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"raw_text": "do the thing"}))
	api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "typed in the form"})

	if len(api.captured) != 1 || api.captured[0] != raw.Data.ID {
		t.Errorf("captured = %v, want only task %d", api.captured, raw.Data.ID)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/stream/ ./internal/httpapi/`
Expected: FAIL to compile: `hub.PublishBatch undefined`, `undefined: httpapi.Deps`.

- [ ] **Step 3: Move the payload into `stream`**

In `internal/stream/hub.go`, replace `import "sync"` with:

```go
import (
	"sync"

	"github.com/jameynakama/whatnow/internal/domain"
)
```

and append:

```go
// BatchApplied is the batch.applied payload, sent after every write.
type BatchApplied struct {
	BatchID  int64             `json:"batch_id"`
	Activity []domain.Activity `json:"activity"`
}

// PublishBatch announces a batch's changes to every client.
func (h *Hub) PublishBatch(batch int64, activity []domain.Activity) {
	h.Publish(Message{Type: "batch.applied", Data: BatchApplied{BatchID: batch, Activity: activity}})
}
```

- [ ] **Step 4: Switch `httpapi` to `Deps`**

Replace `internal/httpapi/server.go` with the version below. It is plan 1's file with `Deps`, `PublishBatch`, the `settings.ErrInvalid` mapping, and no settings routes yet:

```go
// Package httpapi serves the REST API under /api/v1/ and the SSE stream.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/stream"
)

// Deps is what the API needs from the rest of the app.
type Deps struct {
	Service *service.Service
	Hub     *stream.Hub
	// DB holds the settings document.
	DB *sql.DB
	// Loc is the user's time zone, which turns a date into a day of events.
	Loc *time.Location
	// OnCapture is called with the ID of each task created from raw text.
	OnCapture func(taskID int64)
	// NewClient builds an LLM client from settings, for the connection test.
	NewClient func(settings.Settings) (llm.Client, settings.ProviderConfig, error)
}

type Server struct {
	Deps
}

// Mutation is the response to every write. It carries the batch ID so the UI can offer undo.
type Mutation struct {
	BatchID int64 `json:"batch_id"`
	Data    any   `json:"data,omitempty"`
}

func New(d Deps) http.Handler {
	s := &Server{Deps: d}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", s.listTasks)
	mux.HandleFunc("POST /api/v1/tasks", s.createTask)
	mux.HandleFunc("PUT /api/v1/tasks/{id}", s.updateTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/status", s.setTaskStatus)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}", s.deleteTask)
	mux.HandleFunc("GET /api/v1/blockers", s.listBlockers)
	mux.HandleFunc("POST /api/v1/tasks/{id}/blockers", s.addBlocker)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}/blockers/{blockedBy}", s.removeBlocker)
	mux.HandleFunc("POST /api/v1/batches/{id}/undo", s.undo)
	mux.HandleFunc("GET /api/v1/events", s.listEvents)
	mux.HandleFunc("POST /api/v1/events", s.createEvent)
	mux.HandleFunc("PUT /api/v1/events/{id}", s.updateEvent)
	mux.HandleFunc("DELETE /api/v1/events/{id}", s.deleteEvent)
	mux.HandleFunc("GET /api/v1/stream", s.stream)
	return localOnly(mux)
}

// localOnly rejects requests another site could make through the user's browser. A Host that
// isn't loopback means DNS rebinding. A write that isn't JSON means a cross-site form post, which
// skips the CORS preflight a JSON request triggers and this server never answers.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.Host) {
			writeJSON(w, http.StatusForbidden, errorBody{"requests must come from this machine"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				writeJSON(w, http.StatusUnsupportedMediaType, errorBody{"writes must be application/json"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// write runs op in a new user batch, publishes the batch, and responds with its ID.
func (s *Server) write(w http.ResponseWriter, r *http.Request, status int, op func(batch int64) (any, error)) {
	ctx := r.Context()
	batch, err := s.Service.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := op(batch)
	if err != nil {
		writeError(w, err)
		return
	}
	s.publishBatch(ctx, batch)
	writeJSON(w, status, Mutation{BatchID: batch, Data: data})
}

func (s *Server) publishBatch(ctx context.Context, batch int64) {
	acts, err := s.Service.BatchActivity(ctx, batch)
	if err != nil {
		log.Printf("publish batch %d: %v", batch, err)
		return
	}
	s.Hub.PublishBatch(batch, acts)
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalid), errors.Is(err, settings.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, errorBody{err.Error()})
	case errors.Is(err, service.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody{err.Error()})
	case errors.Is(err, service.ErrConflict):
		writeJSON(w, http.StatusConflict, errorBody{err.Error()})
	default:
		log.Printf("internal error: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{"internal error"})
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", service.ErrInvalid, err)
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", service.ErrInvalid, name)
	}
	return id, nil
}
```

In `internal/httpapi/tasks.go`, `internal/httpapi/events.go`, and `internal/httpapi/stream.go`, rename the fields: `s.svc.` to `s.Service.`, `s.hub.` to `s.Hub.`, and `s.loc` to `s.Loc`:

```bash
sed -i '' -e 's/s\.svc\./s.Service./g' -e 's/s\.hub\./s.Hub./g' -e 's/s\.loc\([,)]\)/s.Loc\1/g' \
  internal/httpapi/tasks.go internal/httpapi/events.go internal/httpapi/stream.go
```

(On Linux, use `sed -i` without the `''`.)

In `internal/httpapi/tasks.go`, replace the body of `createTask`'s `s.write` callback so a raw capture calls the hook:

```go
	s.write(w, r, http.StatusCreated, func(batch int64) (any, error) {
		task, err := s.Service.CreateTask(r.Context(), batch, f)
		if err == nil && task.RawText != "" {
			s.OnCapture(task.ID)
		}
		return task, err
	})
```

- [ ] **Step 5: Update `main`**

Replace `cmd/whatnow/main.go` with:

```go
// Command whatnow runs the whatnow server.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: whatnow serve [-addr 127.0.0.1:7777] [-db PATH]")
		os.Exit(2)
	}
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := flags.String("addr", "127.0.0.1:7777", "listen address; keep it on loopback")
	dbPath := flags.String("db", "", "database path (default <user config dir>/whatnow/whatnow.db)")
	flags.Parse(os.Args[2:])

	if err := serve(*addr, *dbPath); err != nil {
		log.Fatal(err)
	}
}

func serve(addr, dbPath string) error {
	if dbPath == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		dbPath = filepath.Join(dir, "whatnow", "whatnow.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	svc := service.New(db, time.Now)
	hub := stream.NewHub()

	handler := httpapi.New(httpapi.Deps{
		Service:   svc,
		Hub:       hub,
		DB:        db,
		Loc:       time.Local,
		OnCapture: func(int64) {}, // cleanup is wired in once it exists
		NewClient: settings.NewClient,
	})
	// No WriteTimeout: it would cut off the long-lived SSE stream.
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("whatnow listening on http://%s (db %s)", addr, dbPath)
	return srv.ListenAndServe()
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go build ./... && go test ./... && go vet ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/stream internal/httpapi cmd
git commit -m "Pass API dependencies as a struct and add the capture hook"
```

---

### Task 5: Settings API

**Files:**
- Create: `internal/httpapi/settings.go`
- Modify: `internal/httpapi/server.go` (routes)
- Test: `internal/httpapi/settings_test.go`

**Interfaces:**
- Consumes: Task 2's `settings` functions; Task 4's `Deps.DB` and `Deps.NewClient`.
- Produces routes:
  - `GET /api/v1/settings` → `httpapi.SettingsView{Settings settings.Settings; Keys map[settings.Provider]settings.KeySource}` (JSON `settings`, `keys`)
  - `PUT /api/v1/settings` with `settings.Settings` → 200 `SettingsView`, or 400
  - `PUT /api/v1/settings/keys/{provider}` with `{"key": "..."}` → 204, or 400
  - `DELETE /api/v1/settings/keys/{provider}` → 204
  - `POST /api/v1/settings/test` → 200 `httpapi.ConnectionResult{OK bool; Error string}` (JSON `ok`, `error`), whether or not the provider answered

- [ ] **Step 1: Write the failing tests**

Create `internal/httpapi/settings_test.go`:

```go
package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/settings"
)

type modelFunc func(llm.Request) (string, error)

func (f modelFunc) Complete(_ context.Context, req llm.Request) (string, error) { return f(req) }

func TestSettingsRoundTripWithoutExposingKeys(t *testing.T) {
	keyring.MockInit()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	api := newAPI(t)

	view := decodeAs[httpapi.SettingsView](t, api.do(http.MethodGet, "/api/v1/settings", nil))
	if view.Settings.Provider != "" || view.Keys[settings.Anthropic] != "" || view.Keys[settings.OpenAI] != settings.KeyFromEnv {
		t.Fatalf("fresh settings: %+v", view)
	}

	const secret = "sk-ant-very-secret"
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/anthropic", map[string]string{"key": secret}), http.StatusNoContent)
	next := view.Settings
	next.Provider = settings.Anthropic
	next.AboutMe = "Code review drains me."
	rec := api.do(http.MethodPut, "/api/v1/settings", next)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatal("settings response contains the API key")
	}
	view = decodeAs[httpapi.SettingsView](t, rec)
	if view.Settings.AboutMe != next.AboutMe || view.Keys[settings.Anthropic] != settings.KeyFromKeychain {
		t.Errorf("after save: %+v", view)
	}

	wantStatus(t, api.do(http.MethodDelete, "/api/v1/settings/keys/anthropic", nil), http.StatusNoContent)
	view = decodeAs[httpapi.SettingsView](t, api.do(http.MethodGet, "/api/v1/settings", nil))
	if view.Keys[settings.Anthropic] != "" {
		t.Errorf("key still reported after delete: %+v", view.Keys)
	}
}

func TestSettingsErrors(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	tooLong := settings.Defaults()
	tooLong.Style = strings.Repeat("x", settings.MaxStyleRunes+1)

	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings", tooLong), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/gemini", map[string]string{"key": "k"}), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/openai", map[string]string{"key": " "}), http.StatusBadRequest)
}

func TestConnectionTest(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)

	got := decodeAs[httpapi.ConnectionResult](t, api.do(http.MethodPost, "/api/v1/settings/test", nil))
	if got.OK || !strings.Contains(got.Error, "no LLM provider") {
		t.Errorf("unconfigured: %+v", got)
	}

	var sentModel string
	api.model = modelFunc(func(req llm.Request) (string, error) {
		sentModel = req.Model
		return "ok", nil
	})
	got = decodeAs[httpapi.ConnectionResult](t, api.do(http.MethodPost, "/api/v1/settings/test", nil))
	if !got.OK || sentModel != "main" {
		t.Errorf("working provider: %+v, model %q", got, sentModel)
	}

	api.model = modelFunc(func(llm.Request) (string, error) { return "", errors.New("anthropic: 401 invalid x-api-key") })
	rec := api.do(http.MethodPost, "/api/v1/settings/test", nil)
	wantStatus(t, rec, http.StatusOK)
	if got := decodeAs[httpapi.ConnectionResult](t, rec); got.OK || !strings.Contains(got.Error, "401") {
		t.Errorf("failing provider: %+v", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/httpapi/ -run 'Settings|Connection'`
Expected: FAIL to compile, `undefined: httpapi.SettingsView`.

- [ ] **Step 3: Write the handlers and register them**

Create `internal/httpapi/settings.go`:

```go
package httpapi

import (
	"net/http"

	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/settings"
)

// SettingsView is what the settings screen gets: the settings, and where each provider's key
// comes from. Keys themselves never leave the server.
type SettingsView struct {
	Settings settings.Settings                        `json:"settings"`
	Keys     map[settings.Provider]settings.KeySource `json:"keys"`
}

func (s *Server) settingsView(w http.ResponseWriter, r *http.Request) {
	saved, err := settings.Load(r.Context(), s.DB)
	if err != nil {
		writeError(w, err)
		return
	}
	view := SettingsView{Settings: saved, Keys: map[settings.Provider]settings.KeySource{}}
	for _, p := range []settings.Provider{settings.Anthropic, settings.OpenAI} {
		_, source, err := settings.Key(p)
		if err != nil {
			writeError(w, err)
			return
		}
		view.Keys[p] = source
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	s.settingsView(w, r)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var body settings.Settings
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := settings.Save(r.Context(), s.DB, body); err != nil {
		writeError(w, err)
		return
	}
	s.settingsView(w, r)
}

func (s *Server) putKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := settings.SetKey(settings.Provider(r.PathValue("provider")), body.Key); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	if err := settings.DeleteKey(settings.Provider(r.PathValue("provider"))); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ConnectionResult reports whether the saved provider answered a tiny request.
type ConnectionResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// testConnection sends one tiny request to the main model. A failure is a normal result for the
// settings screen to show, so it comes back as 200 with ok false.
func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	saved, err := settings.Load(r.Context(), s.DB)
	if err != nil {
		writeError(w, err)
		return
	}
	client, cfg, err := s.NewClient(saved)
	if err == nil {
		_, err = client.Complete(r.Context(), llm.Request{
			Model:     cfg.MainModel,
			User:      "Reply with the single word: ok",
			MaxTokens: 1024, // room for models that think before answering
		})
	}
	if err != nil {
		writeJSON(w, http.StatusOK, ConnectionResult{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ConnectionResult{OK: true})
}
```

In `internal/httpapi/server.go`, inside `New`, after the stream route:

```go
	mux.HandleFunc("GET /api/v1/settings", s.getSettings)
	mux.HandleFunc("PUT /api/v1/settings", s.putSettings)
	mux.HandleFunc("PUT /api/v1/settings/keys/{provider}", s.putKey)
	mux.HandleFunc("DELETE /api/v1/settings/keys/{provider}", s.deleteKey)
	mux.HandleFunc("POST /api/v1/settings/test", s.testConnection)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./... && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi
git commit -m "Add settings, key, and connection-test API"
```

---

### Task 6: Cleanup

**Files:**
- Create: `internal/cleanup/cleanup.go`, `internal/cleanup/result.go`, `internal/cleanup/schema.go`, `internal/cleanup/instructions.go`
- Test: `internal/cleanup/cleanup_test.go`

**Interfaces:**
- Consumes: `service` (tasks, events, blockers, batches, undo, `ErrNotFound`); `stream.Hub` with `PublishBatch`; `llm.Client`, `llm.Request`; `prompt.System`; `settings.Settings`, `settings.ProviderConfig`, `settings.ErrNotConfigured`.
- Produces:
  - `cleanup.ClientFunc func(ctx context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error)`
  - `cleanup.New(svc *service.Service, hub *stream.Hub, client ClientFunc, loc *time.Location, now func() time.Time) *cleanup.Runner`
  - `(*Runner).Enqueue(taskID int64)`, never blocks
  - `(*Runner).Run(ctx context.Context)`, which works the queue until `ctx` is done
  - `(*Runner).Clean(ctx context.Context, taskID int64) (int64, error)`, which returns the applied batch
  - `cleanup.ErrStale`; `cleanup.Failed{TaskID int64; Error string}`, the `cleanup.failed` SSE payload (JSON `task_id`, `error`)

- [ ] **Step 1: Write the failing tests**

The tests use a fake model (a function that sees each request and returns a canned reply), a real database, and a real hub. Create `internal/cleanup/cleanup_test.go`:

```go
package cleanup_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/cleanup"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

// loc is UTC-5, so local times and UTC differ in every assertion that involves them.
var loc = time.FixedZone("UTC-5", -5*3600)

// replyFunc is a fake model: it sees each request and returns whatever the test decides.
type replyFunc func(req llm.Request) (string, error)

func (f replyFunc) Complete(_ context.Context, req llm.Request) (string, error) { return f(req) }

type fixture struct {
	svc    *service.Service
	hub    *stream.Hub
	runner *cleanup.Runner
}

func newFixture(t *testing.T, model replyFunc) *fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := func() time.Time { return time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC) } // 09:00 local
	svc := service.New(db, now)
	hub := stream.NewHub()
	s := settings.Defaults()
	s.Provider = settings.Anthropic
	s.AboutMe = "Code review drains me."
	client := func(context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
		if model == nil {
			return nil, settings.ProviderConfig{}, settings.Settings{}, settings.ErrNotConfigured
		}
		return model, s.Config(), s, nil
	}
	return &fixture{svc: svc, hub: hub, runner: cleanup.New(svc, hub, client, loc, now)}
}

func (f *fixture) capture(t *testing.T, raw string) domain.Task {
	t.Helper()
	ctx := context.Background()
	batch, err := f.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.svc.CreateTask(ctx, batch, service.TaskFields{RawText: raw})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func reply(t *testing.T, v any) replyFunc {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return func(llm.Request) (string, error) { return string(b), nil }
}

func task(fields map[string]any) map[string]any {
	base := map[string]any{"title": "", "notes": "", "size": "", "energy": "", "feeling": "",
		"due_on": "", "not_before": "", "refs": []any{}}
	for k, v := range fields {
		base[k] = v
	}
	return base
}

const flexNote = "do FLEX1-666 today, it feels difficult and needs research on pubsub first. oh i have to review FLEX1-420 as well"

var flexReply = map[string]any{
	"original": task(map[string]any{"keep": true, "title": "Do FLEX1-666", "size": "L", "energy": "high",
		"feeling": "feels difficult", "due_on": "2026-10-05", "refs": []any{map[string]any{"label": "FLEX1-666", "url": ""}}}),
	"new_tasks": []any{
		task(map[string]any{"ref": "n1", "title": "Research pubsub", "size": "M", "energy": "med"}),
		task(map[string]any{"ref": "n2", "title": "Review FLEX1-420", "size": "S"}),
	},
	"events":   []any{},
	"blockers": []any{map[string]any{"task": "original", "blocked_by": "n1"}},
}

func TestSplitsANoteIntoTasksWithABlocker(t *testing.T) {
	var sent llm.Request
	f := newFixture(t, func(req llm.Request) (string, error) {
		sent = req
		return reply(t, flexReply)(req)
	})
	existing := f.capture(t, "water the plants")
	captured := f.capture(t, flexNote)
	msgs, unsub := f.hub.Subscribe()
	defer unsub()
	ctx := context.Background()

	batch, err := f.runner.Clean(ctx, captured.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The request: light model, the raw note, a schema, and context the model needs.
	if sent.Model != "claude-haiku-4-5" || sent.User != flexNote || sent.Schema == nil {
		t.Errorf("request: model %q, user %q, schema set %v", sent.Model, sent.User, sent.Schema != nil)
	}
	var system strings.Builder
	for _, b := range sent.System {
		system.WriteString(b.Text)
	}
	for _, want := range []string{"Code review drains me.", "Monday 2026-10-05 09:00", "#1 water the plants"} {
		if !strings.Contains(system.String(), want) {
			t.Errorf("system prompt is missing %q", want)
		}
	}
	if strings.Contains(system.String(), "#2 ") {
		t.Error("the captured task itself was listed as an existing task")
	}

	// The result: three tasks, one blocker, all in one agent batch.
	tasks, _ := f.svc.ListTasks(ctx)
	if len(tasks) != 4 {
		t.Fatalf("got %d tasks, want 4 (existing + 3)", len(tasks))
	}
	main, research, review := tasks[1], tasks[2], tasks[3]
	if main.ID != captured.ID || main.Title != "Do FLEX1-666" || main.Feeling != "feels difficult" ||
		main.DueOn == nil || *main.DueOn != "2026-10-05" || main.RawText != flexNote {
		t.Errorf("original task: %+v", main)
	}
	if research.Title != "Research pubsub" || research.RawText != flexNote || research.CreatedBy != domain.ActorAgent {
		t.Errorf("research task: %+v", research)
	}
	if review.Title != "Review FLEX1-420" || review.Size == nil || *review.Size != domain.SizeS {
		t.Errorf("review task: %+v", review)
	}
	blockers, _ := f.svc.ListBlockers(ctx)
	if len(blockers) != 1 || blockers[0] != (domain.Blocker{TaskID: main.ID, BlockedByID: research.ID}) {
		t.Errorf("blockers = %+v", blockers)
	}
	if existing.Title != "water the plants" {
		t.Error("an unrelated task was touched")
	}

	m := <-msgs
	if applied, ok := m.Data.(stream.BatchApplied); !ok || applied.BatchID != batch || len(applied.Activity) != 4 {
		t.Errorf("published %+v", m)
	}

	// One undo puts the card back the way it was captured.
	if err := f.svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	tasks, _ = f.svc.ListTasks(ctx)
	if len(tasks) != 2 || tasks[1].Title != captured.Title {
		t.Errorf("after undo: %+v", tasks)
	}
}

func TestEventsOnlyNoteBecomesEventsInLocalTime(t *testing.T) {
	f := newFixture(t, reply(t, map[string]any{
		"original":  task(map[string]any{"keep": false}),
		"new_tasks": []any{},
		"events": []any{
			map[string]any{"title": "Standup", "starts_at": "2026-10-05T10:00", "ends_at": "2026-10-05T10:15"},
			map[string]any{"title": "1:1", "starts_at": "2026-10-05T14:00", "ends_at": ""},
		},
		"blockers": []any{},
	}))
	captured := f.capture(t, "standup at 10, 1:1 at 2")
	ctx := context.Background()

	if _, err := f.runner.Clean(ctx, captured.ID); err != nil {
		t.Fatal(err)
	}

	if tasks, _ := f.svc.ListTasks(ctx); len(tasks) != 0 {
		t.Errorf("the events-only card should be gone: %+v", tasks)
	}
	day := time.Date(2026, 10, 5, 0, 0, 0, 0, loc)
	events, _ := f.svc.ListEvents(ctx, day, day.AddDate(0, 0, 1))
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if want := time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC); !events[0].StartsAt.Equal(want) {
		t.Errorf("standup starts %v, want %v (10:00 at UTC-5)", events[0].StartsAt, want)
	}
	if events[0].EndsAt == nil || events[1].EndsAt != nil {
		t.Errorf("ends: %v, %v", events[0].EndsAt, events[1].EndsAt)
	}
}

func TestABadReplyChangesNothing(t *testing.T) {
	cases := map[string]map[string]any{
		"unknown blocker ref": {
			"original":  task(map[string]any{"keep": true, "title": "Do it"}),
			"new_tasks": []any{task(map[string]any{"ref": "n1", "title": "Prep"})},
			"events":    []any{},
			"blockers":  []any{map[string]any{"task": "original", "blocked_by": "n9"}},
		},
		"invalid size": {
			"original":  task(map[string]any{"keep": true, "title": "Do it"}),
			"new_tasks": []any{task(map[string]any{"ref": "n1", "title": "Prep", "size": "XL"})},
			"events":    []any{},
			"blockers":  []any{},
		},
		"blocker on a deleted original": {
			"original":  task(map[string]any{"keep": false}),
			"new_tasks": []any{task(map[string]any{"ref": "n1", "title": "Prep"})},
			"events":    []any{},
			"blockers":  []any{map[string]any{"task": "original", "blocked_by": "n1"}},
		},
		"bad event time": {
			"original":  task(map[string]any{"keep": false}),
			"new_tasks": []any{},
			"events":    []any{map[string]any{"title": "Standup", "starts_at": "10am", "ends_at": ""}},
			"blockers":  []any{},
		},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, reply(t, bad))
			captured := f.capture(t, "do it, prep first")
			ctx := context.Background()

			if _, err := f.runner.Clean(ctx, captured.ID); err == nil {
				t.Fatal("expected an error")
			}

			tasks, _ := f.svc.ListTasks(ctx)
			if len(tasks) != 1 || tasks[0].Title != captured.Title || !tasks[0].UpdatedAt.Equal(captured.UpdatedAt) {
				t.Errorf("tasks after a failed cleanup: %+v", tasks)
			}
			if blockers, _ := f.svc.ListBlockers(ctx); len(blockers) != 0 {
				t.Errorf("blockers after a failed cleanup: %+v", blockers)
			}
		})
	}
}

func TestLeavesACardTheUserEditedMeanwhile(t *testing.T) {
	var f *fixture
	var captured domain.Task
	f = newFixture(t, func(req llm.Request) (string, error) {
		// The user retitles the card while the model is thinking.
		ctx := context.Background()
		batch, _ := f.svc.NewBatch(ctx, domain.ActorUser)
		if _, err := f.svc.UpdateTask(ctx, batch, captured.ID, service.TaskFields{Title: "my own title"}); err != nil {
			t.Error(err)
		}
		return reply(t, flexReply)(req)
	})
	captured = f.capture(t, flexNote)

	_, err := f.runner.Clean(context.Background(), captured.ID)
	if !errors.Is(err, cleanup.ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	tasks, _ := f.svc.ListTasks(context.Background())
	if len(tasks) != 1 || tasks[0].Title != "my own title" {
		t.Errorf("cleanup overwrote the user's edit: %+v", tasks)
	}
}

func TestRunReportsFailuresButNotMissingSetup(t *testing.T) {
	cases := []struct {
		name       string
		model      replyFunc
		wantFailed bool
	}{
		{"no provider set up", nil, false},
		{"provider error", func(llm.Request) (string, error) { return "", llm.ErrRefused }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, c.model)
			captured := f.capture(t, "do the thing")
			msgs, unsub := f.hub.Subscribe()
			defer unsub()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go f.runner.Run(ctx)

			f.runner.Enqueue(captured.ID)

			select {
			case m := <-msgs:
				failed, ok := m.Data.(cleanup.Failed)
				if !c.wantFailed || m.Type != "cleanup.failed" || !ok || failed.TaskID != captured.ID {
					t.Errorf("unexpected message %+v", m)
				}
			case <-time.After(300 * time.Millisecond):
				if c.wantFailed {
					t.Error("no cleanup.failed published")
				}
			}
			tasks, _ := f.svc.ListTasks(context.Background())
			if len(tasks) != 1 || tasks[0].Title != "do the thing" {
				t.Errorf("the raw card should be untouched: %+v", tasks)
			}
		})
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cleanup/`
Expected: FAIL, `no non-test Go files`.

- [ ] **Step 3: Write the schema and instructions**

Create `internal/cleanup/schema.go`:

```go
package cleanup

import "sort"

// schema is the reply shape. Every field is required and empty strings stand for "none", which
// keeps it inside what both the Claude API and OpenAI's strict mode accept.
var schema = object(map[string]any{
	"original": object(withFields(map[string]any{
		"keep": map[string]any{"type": "boolean", "description": "False only when the note holds nothing but events."},
	})),
	"new_tasks": array(object(withFields(map[string]any{
		"ref": str(`A short name other entries use to refer to this task, like "n1".`),
	}))),
	"events": array(object(map[string]any{
		"title":     str("What the event is."),
		"starts_at": str("Local start time as YYYY-MM-DDTHH:MM."),
		"ends_at":   str("Local end time as YYYY-MM-DDTHH:MM, or empty."),
	})),
	"blockers": array(object(map[string]any{
		"task":       str(`The task that waits: "original", a new task's ref, or "#<id>".`),
		"blocked_by": str(`The task that must finish first, referenced the same way.`),
	})),
})

func withFields(extra map[string]any) map[string]any {
	props := map[string]any{
		"title":      str("Short and concrete."),
		"notes":      str("Useful detail that doesn't fit the title, or empty."),
		"size":       enum("S, M, or L, or empty if unclear.", "", "S", "M", "L"),
		"energy":     enum("Mental effort: low, med, or high, or empty if unclear.", "", "low", "med", "high"),
		"feeling":    str("How the user feels about it, in their words, or empty."),
		"due_on":     str("YYYY-MM-DD, or empty."),
		"not_before": str("YYYY-MM-DD, or empty."),
		"refs": array(object(map[string]any{
			"label": str("A ticket key or short name."),
			"url":   str("The link, or empty."),
		})),
	}
	for k, v := range extra {
		props[k] = v
	}
	return props
}

func object(props map[string]any) map[string]any {
	required := make([]string, 0, len(props))
	for k := range props {
		required = append(required, k)
	}
	sort.Strings(required)
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func enum(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}
```

Create `internal/cleanup/instructions.go`:

```go
package cleanup

const instructions = `You tidy up a note that someone with ADHD just typed into their day planner. Turn it into clean tasks, fixed-time events, and blockers. Reply only with JSON that matches the schema.

The note is already saved as one task, called "original". Decide what it becomes:
- If the note describes one task, keep the original and fill in its fields.
- If it describes several tasks, keep the original as the main one and put the others in new_tasks.
- If it holds nothing but events, set keep to false. The original is then deleted.

Task fields:
- title: short and concrete, in the user's words where you can. Keep ticket keys like FLEX1-666.
- notes: useful detail from the note that doesn't fit the title. Links go in refs, not here.
- size: S is under 30 minutes, M is a few hours, L is most of a day or more. Empty if you can't tell.
- energy: the mental effort it takes. Use what the note says about how hard it feels. Empty if you can't tell.
- feeling: a few words on how the user feels about it, taken from what they wrote ("dreading it", "excited"). Empty if they didn't say. Never invent a feeling.
- due_on and not_before: dates as YYYY-MM-DD, worked out from the current date. "Today" means today's date. Empty unless the note says so.
- refs: ticket keys and URLs in the note. label is the key or a short name. url is the link, or empty.

Events are things at a fixed time, like "standup at 10" or "1:1 at 2pm". Give starts_at and ends_at as local times in the form YYYY-MM-DDTHH:MM. Assume today unless the note says otherwise. Leave ends_at empty when no end is given.

Blockers: when one task has to happen before another ("needs pubsub research first"), make the prerequisite its own task and add a blocker. task is the one that waits. blocked_by is the one that has to finish first. Refer to tasks as "original", a new task's ref, or "#<id>" for an existing open task from the context.

Don't create a task that duplicates an existing open task. Refer to the existing one by "#<id>" instead.`
```

- [ ] **Step 4: Write the result mapping and the runner**

Create `internal/cleanup/result.go`:

```go
package cleanup

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

// result mirrors schema.
type result struct {
	Original original  `json:"original"`
	NewTasks []newTask `json:"new_tasks"`
	Events   []event   `json:"events"`
	Blockers []blocker `json:"blockers"`
}

type taskOut struct {
	Title     string       `json:"title"`
	Notes     string       `json:"notes"`
	Size      string       `json:"size"`
	Energy    string       `json:"energy"`
	Feeling   string       `json:"feeling"`
	DueOn     string       `json:"due_on"`
	NotBefore string       `json:"not_before"`
	Refs      []domain.Ref `json:"refs"`
}

type original struct {
	Keep bool `json:"keep"`
	taskOut
}

type newTask struct {
	Ref string `json:"ref"`
	taskOut
}

type event struct {
	Title    string `json:"title"`
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
}

type blocker struct {
	Task      string `json:"task"`
	BlockedBy string `json:"blocked_by"`
}

// fields turns the model's empty strings into unset fields. The service validates the rest.
func (t taskOut) fields() service.TaskFields {
	return service.TaskFields{
		Title:     t.Title,
		Notes:     t.Notes,
		Size:      optional[domain.Size](t.Size),
		Energy:    optional[domain.Energy](t.Energy),
		Feeling:   t.Feeling,
		DueOn:     optional[domain.Date](t.DueOn),
		NotBefore: optional[domain.Date](t.NotBefore),
		Refs:      t.Refs,
	}
}

func optional[T ~string](s string) *T {
	if s == "" {
		return nil
	}
	v := T(s)
	return &v
}

const localTime = "2006-01-02T15:04"

// fields reads the model's local times in the user's time zone.
func (e event) fields(loc *time.Location) (service.EventFields, error) {
	start, err := time.ParseInLocation(localTime, e.StartsAt, loc)
	if err != nil {
		return service.EventFields{}, fmt.Errorf("event %q: starts_at %q is not YYYY-MM-DDTHH:MM", e.Title, e.StartsAt)
	}
	f := service.EventFields{Title: e.Title, StartsAt: start}
	if e.EndsAt != "" {
		end, err := time.ParseInLocation(localTime, e.EndsAt, loc)
		if err != nil {
			return service.EventFields{}, fmt.Errorf("event %q: ends_at %q is not YYYY-MM-DDTHH:MM", e.Title, e.EndsAt)
		}
		f.EndsAt = &end
	}
	return f, nil
}

// resolve turns "original", a new task's ref, or "#<id>" into a task ID.
func resolve(ids map[string]int64, ref string) (int64, error) {
	if id, ok := ids[ref]; ok {
		return id, nil
	}
	if rest, ok := strings.CutPrefix(ref, "#"); ok {
		if id, err := strconv.ParseInt(rest, 10, 64); err == nil {
			return id, nil
		}
	}
	return 0, fmt.Errorf("unknown task reference %q", ref)
}
```

Create `internal/cleanup/cleanup.go`:

```go
// Package cleanup turns a freshly captured card into tidy tasks, events, and blockers. It makes
// one structured call to the light model and applies the result as one undoable agent batch.
package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/prompt"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/stream"
)

// ErrStale means the card was edited or deleted while cleanup ran, so cleanup left it alone.
var ErrStale = errors.New("task changed since capture")

// ClientFunc returns the client and settings for one run. It returns settings.ErrNotConfigured
// when no provider is set up.
type ClientFunc func(ctx context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error)

// Failed is the cleanup.failed payload.
type Failed struct {
	TaskID int64  `json:"task_id"`
	Error  string `json:"error"`
}

type Runner struct {
	svc    *service.Service
	hub    *stream.Hub
	client ClientFunc
	loc    *time.Location
	now    func() time.Time
	queue  chan int64
}

const (
	queueSize = 64
	// maxOpenTasks bounds how many existing tasks the model sees when looking for duplicates.
	maxOpenTasks = 200
	maxTokens    = 4096
)

func New(svc *service.Service, hub *stream.Hub, client ClientFunc, loc *time.Location, now func() time.Time) *Runner {
	return &Runner{svc: svc, hub: hub, client: client, loc: loc, now: now, queue: make(chan int64, queueSize)}
}

// Enqueue schedules cleanup for a captured task. It never blocks: when the queue is full the card
// stays raw, which is always a valid state.
func (r *Runner) Enqueue(taskID int64) {
	select {
	case r.queue <- taskID:
	default:
		log.Printf("cleanup queue full, task %d stays raw", taskID)
	}
}

// Run cleans queued tasks one at a time until ctx is done. A missing provider or a card the user
// already changed is not a failure; anything else is published as cleanup.failed.
func (r *Runner) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-r.queue:
			_, err := r.Clean(ctx, id)
			if err == nil || errors.Is(err, settings.ErrNotConfigured) || errors.Is(err, ErrStale) {
				continue
			}
			log.Printf("cleanup task %d: %v", id, err)
			r.hub.Publish(stream.Message{Type: "cleanup.failed", Data: Failed{TaskID: id, Error: err.Error()}})
		}
	}
}

// Clean runs cleanup on one task and returns the batch it applied. If any write fails, the batch
// is undone and the card is left exactly as it was.
func (r *Runner) Clean(ctx context.Context, taskID int64) (int64, error) {
	task, err := r.svc.GetTask(ctx, taskID)
	if err != nil {
		return 0, err
	}
	client, cfg, s, err := r.client(ctx)
	if err != nil {
		return 0, err
	}
	situation, err := r.situation(ctx, taskID)
	if err != nil {
		return 0, err
	}

	reply, err := client.Complete(ctx, llm.Request{
		Model:      cfg.LightModel,
		System:     prompt.System(instructions, "", s.AboutMe, situation),
		User:       task.RawText,
		Schema:     schema,
		SchemaName: "cleanup",
		MaxTokens:  maxTokens,
	})
	if err != nil {
		return 0, err
	}
	var res result
	if err := json.Unmarshal([]byte(reply), &res); err != nil {
		return 0, fmt.Errorf("parse reply: %w", err)
	}

	// Compare the whole card, not updated_at: two writes can land on the same clock tick.
	current, err := r.svc.GetTask(ctx, taskID)
	if errors.Is(err, service.ErrNotFound) || (err == nil && !reflect.DeepEqual(current, task)) {
		return 0, ErrStale
	}
	if err != nil {
		return 0, err
	}
	return r.apply(ctx, task, res)
}

// situation tells the model the date and time and which open tasks already exist.
func (r *Runner) situation(ctx context.Context, taskID int64) (string, error) {
	tasks, err := r.svc.ListTasks(ctx)
	if err != nil {
		return "", err
	}
	now := r.now().In(r.loc)
	var b strings.Builder
	fmt.Fprintf(&b, "Now: %s (%s)\n\n", now.Format("Monday 2006-01-02 15:04"), now.Format("MST, UTC-07:00"))
	b.WriteString("Existing open tasks, as #<id> title:\n")
	open := 0
	for i := len(tasks) - 1; i >= 0 && open < maxOpenTasks; i-- {
		if t := tasks[i]; t.ID != taskID && t.Status == domain.StatusOpen {
			fmt.Fprintf(&b, "#%d %s\n", t.ID, t.Title)
			open++
		}
	}
	if open == 0 {
		b.WriteString("(none)\n")
	}
	return b.String(), nil
}

func (r *Runner) apply(ctx context.Context, task domain.Task, res result) (int64, error) {
	batch, err := r.svc.NewBatch(ctx, domain.ActorAgent)
	if err != nil {
		return 0, err
	}
	if err := r.write(ctx, batch, task, res); err != nil {
		if undoErr := r.svc.Undo(ctx, batch); undoErr != nil {
			return 0, errors.Join(err, undoErr)
		}
		return 0, err
	}
	acts, err := r.svc.BatchActivity(ctx, batch)
	if err != nil {
		return 0, err
	}
	r.hub.PublishBatch(batch, acts)
	return batch, nil
}

func (r *Runner) write(ctx context.Context, batch int64, task domain.Task, res result) error {
	ids := map[string]int64{}
	if res.Original.Keep {
		if _, err := r.svc.UpdateTask(ctx, batch, task.ID, res.Original.fields()); err != nil {
			return err
		}
		ids["original"] = task.ID
	}
	for _, n := range res.NewTasks {
		if n.Ref == "original" || strings.HasPrefix(n.Ref, "#") || ids[n.Ref] != 0 {
			return fmt.Errorf("new task ref %q is reserved or used twice", n.Ref)
		}
		f := n.fields()
		f.RawText = task.RawText
		created, err := r.svc.CreateTask(ctx, batch, f)
		if err != nil {
			return err
		}
		ids[n.Ref] = created.ID
	}
	for _, e := range res.Events {
		f, err := e.fields(r.loc)
		if err != nil {
			return err
		}
		f.RawText = task.RawText
		if _, err := r.svc.CreateEvent(ctx, batch, f); err != nil {
			return err
		}
	}
	for _, b := range res.Blockers {
		waiting, err := resolve(ids, b.Task)
		if err != nil {
			return err
		}
		first, err := resolve(ids, b.BlockedBy)
		if err != nil {
			return err
		}
		if err := r.svc.AddBlocker(ctx, batch, domain.Blocker{TaskID: waiting, BlockedByID: first}); err != nil {
			return err
		}
	}
	if !res.Original.Keep {
		return r.svc.DeleteTask(ctx, batch, task.ID)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/cleanup/ && go vet ./...`
Expected: PASS. A log line like `cleanup task 1: model declined the request` is expected output from the `provider error` case.

- [ ] **Step 6: Commit**

```bash
git add internal/cleanup
git commit -m "Add background cleanup of captured cards"
```

---

### Task 7: Wire cleanup into the server

**Files:**
- Modify: `cmd/whatnow/main.go`

**Interfaces:**
- Consumes: `cleanup.New`, `(*Runner).Run`, `(*Runner).Enqueue`, `settings.Active`, `settings.NewClient`.

`main` is wiring only, so this task has no new unit test. Step 2 checks it by hand.

- [ ] **Step 1: Wire the runner**

Replace `cmd/whatnow/main.go` with:

```go
// Command whatnow runs the whatnow server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jameynakama/whatnow/internal/cleanup"
	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: whatnow serve [-addr 127.0.0.1:7777] [-db PATH]")
		os.Exit(2)
	}
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := flags.String("addr", "127.0.0.1:7777", "listen address; keep it on loopback")
	dbPath := flags.String("db", "", "database path (default <user config dir>/whatnow/whatnow.db)")
	flags.Parse(os.Args[2:])

	if err := serve(*addr, *dbPath); err != nil {
		log.Fatal(err)
	}
}

func serve(addr, dbPath string) error {
	if dbPath == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		dbPath = filepath.Join(dir, "whatnow", "whatnow.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	svc := service.New(db, time.Now)
	hub := stream.NewHub()
	runner := cleanup.New(svc, hub, func(ctx context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
		return settings.Active(ctx, db)
	}, time.Local, time.Now)
	go runner.Run(context.Background())

	handler := httpapi.New(httpapi.Deps{
		Service:   svc,
		Hub:       hub,
		DB:        db,
		Loc:       time.Local,
		OnCapture: runner.Enqueue,
		NewClient: settings.NewClient,
	})
	// No WriteTimeout: it would cut off the long-lived SSE stream.
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("whatnow listening on http://%s (db %s)", addr, dbPath)
	return srv.ListenAndServe()
}
```

- [ ] **Step 2: Smoke-test the binary**

```bash
just serve -addr 127.0.0.1:7777 -db "$(mktemp -d)/smoke test.db"
```

In a second terminal:

```bash
curl -s http://127.0.0.1:7777/api/v1/settings
curl -s -X POST http://127.0.0.1:7777/api/v1/settings/test -H 'Content-Type: application/json'
curl -s -X POST http://127.0.0.1:7777/api/v1/tasks -H 'Content-Type: application/json' -d '{"raw_text":"standup at 10"}'
curl -s http://127.0.0.1:7777/api/v1/tasks
```

Expected, with no provider set up:
- Settings show `"provider":""` and the Claude default models. `keys` show `"env"` for any provider whose key is in your environment.
- The connection test returns `{"ok":false,"error":"no LLM provider is set up"}`.
- The capture succeeds, the task list still shows the raw card, and the server logs no cleanup error.

If you have a Claude API key and want to see cleanup work end to end (this makes one small paid API call):

```bash
curl -s -X PUT http://127.0.0.1:7777/api/v1/settings/keys/anthropic -H 'Content-Type: application/json' -d "{\"key\":\"$ANTHROPIC_API_KEY\"}"
curl -s -X PUT http://127.0.0.1:7777/api/v1/settings -H 'Content-Type: application/json' \
  -d '{"provider":"anthropic","anthropic":{"main_model":"claude-sonnet-5-5","light_model":"claude-haiku-4-5","base_url":""},"openai":{"main_model":"","light_model":"","base_url":""},"style":"","about_me":""}'
curl -s -X POST http://127.0.0.1:7777/api/v1/tasks -H 'Content-Type: application/json' \
  -d '{"raw_text":"do FLEX1-666 today, it feels difficult and needs research on pubsub first. oh i have to review FLEX1-420 as well"}'
sleep 10; curl -s http://127.0.0.1:7777/api/v1/tasks; curl -s http://127.0.0.1:7777/api/v1/blockers
```

Expected: three tasks and one blocker (FLEX1-666 waits on the pubsub research). Then delete the test key from your keychain: `curl -s -X DELETE http://127.0.0.1:7777/api/v1/settings/keys/anthropic -H 'Content-Type: application/json'`.

- [ ] **Step 3: Check coverage**

Run: `just cover`
Expected: about 80% total. Scan `go tool cover -html=coverage.out` for uncovered *behavior* and add tests for any you find. Error pass-throughs don't need tests.

- [ ] **Step 4: Commit**

```bash
git add cmd
git commit -m "Run cleanup in the background after capture"
```
