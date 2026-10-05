# whatnow Agent Chat Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The user can chat with the assistant. It answers in the persona's voice, streams its text as it writes, and changes tasks, events, blockers, pace, and the plan through tools. All the changes one reply makes can be undone at once. A long day of chat folds its oldest turns into a running summary.

**Architecture:** `llm.Client` gains `Chat`, a streaming call that can use tools, for both providers. The service stores chat messages by date, plus the running summary. The planner exports `Situation`, which provides the model, the settings, and the live context it already renders. The new `agent` package runs each turn: it builds the context, calls the model, runs tools against `service` and `planner`, and loops for up to 10 rounds. Text and tool chips stream over the hub. `httpapi` adds `GET` and `POST /api/v1/chat`.

**Tech Stack:** Go 1.27 and everything from plans 1 to 3b. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-05-whatnow-design.md` (Agent, Architecture, Prompt layers, Message)

**Where this sits:** plan 4. Later plans:
- 5. Web UI
- 6. Packaging and prompt evals

**Verified before handoff:** the tasks below were replayed in order in a scratch copy. After each task, the project builds, passes `go vet`, and passes the whole suite, including under `-race`. A live run on `gpt-5.5` with real OpenAI keys accepted every strict tool schema. Over five turns it completed, added, updated, pushed, undid, and changed pace correctly, in 2 to 6 seconds per turn. The Claude API path has only been tested against fakes.

**Decisions this plan makes:**
- **History is text only.** Stored messages keep the user's text, the reply's text, the tool chips, and the reply's batch. Tool calls and results exist only inside a turn. The facts in the system prompt show what tools changed, so earlier replies don't need them replayed.
- **Messages are keyed by date rather than day,** so chat works before the morning flow starts the day.
- **`push_task` is added beyond the spec's tool list.** Without it, the agent would defer tasks by setting `not_before` with `update_task`, and push counts would miss them.
- **Updates take a list of field changes.** `update_task` and `update_event` take `[{field, value}]`, and an empty value clears a field. Both providers' strict modes accept that, and it needs no nullable types.
- **The context lists the next 7 days of events with their IDs,** so the agent can move or delete one. Today's events also appear in the facts, without IDs.
- **`undo_last` skips the current reply's batch.** "Undo that" means the last reply. A reply that changes something and then undoes it would otherwise undo itself.
- **One turn at a time.** A second message while a reply is still being written gets `409`.
- **`POST /api/v1/chat` waits for the reply and returns it.** The turn finishes even if the client leaves. The reply also arrives over SSE as `chat.message`, after `chat.delta` (text pieces) and `chat.tool` (chips). Deltas are best-effort, because the hub drops messages for a slow client. `chat.message` is authoritative.
- **The turn's batch is published once, at the end.** The replan watcher then sees each change once.
- **The replan watcher subscribes to batch messages only.** Otherwise a stream of chat deltas during a replan could push a batch message out of its buffer.
- **Folding:** once today's unfolded chat passes 24,000 bytes of text, the oldest whole turns are folded until the rest fits in half that. The fold uses the light model. If the fold fails, the whole chat is sent instead.
- **Provider errors become a stored reply** with `error` set and plain text the UI links to settings. Those replies are never replayed to the model.
- **A user message is limited to 4,000 characters.**
- **Every existing fake `llm.Client` gains a `Chat` stub that panics,** since nothing it stands in for ever chats.
- **The agent's `replan` can be followed by a watcher replan.** If the same reply also made a change that triggers one (a task due today, for example), the day is replanned twice. That's acceptable for now.

## Global Constraints

- Module path: `github.com/jameynakama/whatnow`. Go directive: `go 1.27`.
- `service` is the only write path. Agent tools call `service` and `planner`, never SQL.
- Chat uses the main model with `Fallback` set. Folding uses the light model.
- Every model call is bounded: `callTimeout` (2 minutes) for each agent call. The planner's own calls keep `modelTimeout`.
- The loop stops after 10 tool rounds.
- Tool and validation errors go back to the model as tool results. Provider errors become a plain reply.
- All API routes live under `/api/v1/`.
- Tests use stdlib `testing` and `go-cmp`, with a real SQLite database in a temp dir. Tests never call a real provider.
- Commit messages: plain imperative subject, no `Co-Authored-By` trailer.

## Review Focus

1. **Chat deltas stream while a replan is running.** Expected: the replan watcher still sees every batch message. Pinned in Task 3.
2. **The user says "undo that" in a reply that has already changed something.** Expected: the previous reply's batch is undone, never the current one. Pinned in Task 5.
3. **The provider fails after tools have written.** Expected: the writes stay, the batch is published, and the reply says what failed. Pinned in Task 4.
4. **The user closes the tab mid-reply.** Expected: the turn finishes, and its writes and reply are stored. Pinned in Task 6.
5. **A long day of chat, or a fold that fails.** Expected: the oldest whole turns fold into the summary, and the history still starts with a user message. A failed fold sends everything and doesn't block the reply. Pinned in Task 4.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/llm/llm.go` | `Client.Chat`, plus `ChatRequest`, `Message`, `ToolCall`, `ToolResult`, `Tool`, and `Reply` |
| `internal/llm/anthropic.go`, `openai.go` | Streaming chat with tools for each provider. The system-prompt helpers are shared with `Complete`. |
| `internal/store/migrations/0005_chat.sql` | `messages` and `chat_folds` |
| `internal/domain/domain.go` | `Role`, `ToolUse`, `Message`, `ChatFold` |
| `internal/service/messages.go` | `AddMessage`, `Messages`, `ChatFold`, `SetChatFold` |
| `internal/service/days.go` | `DaysBetween` |
| `internal/service/events.go` | `GetEvent`, and `liveEvent` shared with `changeEvent` |
| `internal/service/undo.go` | `LastUndoableBatch` can leave out one batch |
| `internal/stream/hub.go` | `Subscribe` can limit itself to some message types |
| `internal/planner/planner.go` | `Situation`. The live context split out of `context`. |
| `internal/planner/instructions.go` | `Persona` exported |
| `internal/agent/agent.go` | `Agent`, `Send`, `History`, the turn loop, the turn's batch |
| `internal/agent/context.go` | History, folding, and the system prompt |
| `internal/agent/tools.go` | Tool schemas and handlers |
| `internal/agent/instructions.go` | Agent and fold instructions |
| `internal/httpapi/chat.go` | Chat routes |
| `cmd/whatnow/main.go` | Builds the agent, and filters the watcher's subscription |

---

### Task 1: Streaming chat with tools in `llm`

`Client` gains `Chat`: a single model turn in a conversation, with tools, streaming text through a callback. Both providers stream through their SDK's accumulator, so the reply comes back whole once the model stops. Tools are sent in strict mode. The system-prompt conversion moves into helpers that `Complete` now shares. Every existing fake client gains a `Chat` stub that panics.

**Files:**
- Modify: `internal/llm/llm.go`
- Modify: `internal/llm/anthropic.go`
- Modify: `internal/llm/openai.go`
- Create: `internal/llm/chat_test.go`
- Modify: `internal/cleanup/cleanup_test.go`, `internal/httpapi/settings_test.go`, `internal/planner/helpers_test.go`, `internal/planner/close_test.go`, `internal/planner/plan_test.go` (stubs)

**Interfaces:**
- Produces:
  - `llm.Client.Chat(ctx, req llm.ChatRequest, onText func(string)) (llm.Reply, error)`
  - `llm.Role` (`llm.RoleUser`, `llm.RoleAssistant`). `llm.Message{Role, Text, ToolCalls []ToolCall, ToolResults []ToolResult}`
  - `llm.ToolCall{ID, Name string; Input json.RawMessage}`, `llm.ToolResult{CallID, Content string; IsError bool}`
  - `llm.Tool{Name, Description string; Schema map[string]any}`: the schema comes from `jsonschema.Object`
  - `llm.ChatRequest{Model, System []Block, Messages, Tools, MaxTokens int64, Fallback bool}`, `llm.Reply{Text string; ToolCalls []ToolCall}`
  - Refusals and token-limit stops return `ErrRefused` and `ErrTruncated`, the same as `Complete`.

- [ ] **Step 1: Write the failing tests**

`internal/llm/chat_test.go`. Create it with:

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

	"github.com/google/go-cmp/cmp"
	"github.com/jameynakama/whatnow/internal/llm"
)

// sseAPI streams canned server-sent events and records the request body it received.
type sseAPI struct {
	events []string
	body   map[string]any
}

func (f *sseAPI) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &f.body); err != nil {
			t.Errorf("request body is not JSON: %s", raw)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range f.events {
			io.WriteString(w, e+"\n\n")
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func anthropicEvent(v map[string]any) string {
	b, _ := json.Marshal(v)
	return "event: " + v["type"].(string) + "\ndata: " + string(b)
}

func anthropicStream(stopReason string, blocks ...[]map[string]any) []string {
	events := []string{anthropicEvent(map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "test-model", "content": []any{},
		"stop_reason": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}})}
	for i, block := range blocks {
		events = append(events, anthropicEvent(map[string]any{"type": "content_block_start", "index": i, "content_block": block[0]}))
		for _, delta := range block[1:] {
			events = append(events, anthropicEvent(map[string]any{"type": "content_block_delta", "index": i, "delta": delta}))
		}
		events = append(events, anthropicEvent(map[string]any{"type": "content_block_stop", "index": i}))
	}
	return append(events,
		anthropicEvent(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason},
			"usage": map[string]any{"output_tokens": 5}}),
		anthropicEvent(map[string]any{"type": "message_stop"}))
}

func openAIChunk(delta map[string]any, finish any) string {
	b, _ := json.Marshal(map[string]any{"id": "c1", "object": "chat.completion.chunk", "created": 1, "model": "test-model",
		"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}})
	return "data: " + string(b)
}

var completeTask = llm.Tool{Name: "complete_task", Description: "Mark a task done.", Schema: map[string]any{
	"type":                 "object",
	"properties":           map[string]any{"task_id": map[string]any{"type": "integer"}},
	"required":             []string{"task_id"},
	"additionalProperties": false,
}}

// chatRequest is a turn mid-conversation: the model called a tool, got its result, and goes on.
func chatRequest() llm.ChatRequest {
	return llm.ChatRequest{
		Model:  "test-model",
		System: []llm.Block{{Text: "core", Cache: true}, {Text: "context"}},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Text: "I finished the RFC"},
			{Role: llm.RoleAssistant, Text: "Nice.", ToolCalls: []llm.ToolCall{{ID: "call_0", Name: "complete_task", Input: json.RawMessage(`{"task_id":3}`)}}},
			{Role: llm.RoleUser, ToolResults: []llm.ToolResult{{CallID: "call_0", Content: "no task #3", IsError: true}}},
		},
		Tools:     []llm.Tool{completeTask},
		MaxTokens: 512,
	}
}

func collect(t *testing.T, client llm.Client, req llm.ChatRequest) (llm.Reply, []string) {
	t.Helper()
	var deltas []string
	reply, err := client.Chat(context.Background(), req, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}
	return reply, deltas
}

func TestAnthropicChatStreamsTextAndToolCalls(t *testing.T) {
	api := &sseAPI{events: anthropicStream("tool_use",
		[]map[string]any{{"type": "text", "text": ""}, {"type": "text_delta", "text": "Let me "}, {"type": "text_delta", "text": "fix that."}},
		[]map[string]any{{"type": "tool_use", "id": "toolu_1", "name": "complete_task", "input": map[string]any{}},
			{"type": "input_json_delta", "partial_json": `{"task_`}, {"type": "input_json_delta", "partial_json": `id": 4}`}},
	)}
	reply, deltas := collect(t, llm.NewAnthropic("sk-test", api.start(t)), chatRequest())

	want := llm.Reply{Text: "Let me fix that.", ToolCalls: []llm.ToolCall{{ID: "toolu_1", Name: "complete_task", Input: json.RawMessage(`{"task_id": 4}`)}}}
	if diff := cmp.Diff(want, reply); diff != "" {
		t.Errorf("reply (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"Let me ", "fix that."}, deltas); diff != "" {
		t.Errorf("deltas (-want +got):\n%s", diff)
	}

	if api.body["stream"] != true {
		t.Errorf("stream = %v", api.body["stream"])
	}
	tool := api.body["tools"].([]any)[0].(map[string]any)
	schema := tool["input_schema"].(map[string]any)
	if tool["name"] != "complete_task" || tool["strict"] != true || schema["additionalProperties"] != false || schema["type"] != "object" {
		t.Errorf("tool = %v", tool)
	}
	msgs := api.body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("sent %d messages, want 3", len(msgs))
	}
	assistant := msgs[1].(map[string]any)["content"].([]any)
	use := assistant[1].(map[string]any)
	if use["type"] != "tool_use" || use["id"] != "call_0" || use["input"].(map[string]any)["task_id"] != float64(3) {
		t.Errorf("tool use = %v", use)
	}
	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "call_0" || result["is_error"] != true {
		t.Errorf("tool result = %v", result)
	}
}

func TestOpenAIChatStreamsTextAndToolCalls(t *testing.T) {
	api := &sseAPI{events: []string{
		openAIChunk(map[string]any{"role": "assistant", "content": "Let me "}, nil),
		openAIChunk(map[string]any{"content": "fix that."}, nil),
		openAIChunk(map[string]any{"tool_calls": []map[string]any{{"index": 0, "id": "call_1", "type": "function",
			"function": map[string]any{"name": "complete_task", "arguments": ""}}}}, nil),
		openAIChunk(map[string]any{"tool_calls": []map[string]any{{"index": 0, "function": map[string]any{"arguments": `{"task_id":4}`}}}}, nil),
		openAIChunk(map[string]any{}, "tool_calls"),
		"data: [DONE]",
	}}
	reply, deltas := collect(t, llm.NewOpenAI("sk-test", api.start(t)), chatRequest())

	want := llm.Reply{Text: "Let me fix that.", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "complete_task", Input: json.RawMessage(`{"task_id":4}`)}}}
	if diff := cmp.Diff(want, reply); diff != "" {
		t.Errorf("reply (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"Let me ", "fix that."}, deltas); diff != "" {
		t.Errorf("deltas (-want +got):\n%s", diff)
	}

	tool := api.body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if tool["name"] != "complete_task" || tool["strict"] != true {
		t.Errorf("tool = %v", tool)
	}
	msgs := api.body["messages"].([]any)
	roles := make([]string, len(msgs))
	for i, m := range msgs {
		roles[i] = m.(map[string]any)["role"].(string)
	}
	if diff := cmp.Diff([]string{"system", "user", "assistant", "tool"}, roles); diff != "" {
		t.Errorf("roles (-want +got):\n%s", diff)
	}
	call := msgs[2].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if call["id"] != "call_0" || call["function"].(map[string]any)["arguments"] != `{"task_id":3}` {
		t.Errorf("tool call = %v", call)
	}
	result := msgs[3].(map[string]any)
	if result["tool_call_id"] != "call_0" || result["content"] != "Error: no task #3" {
		t.Errorf("tool result = %v", result)
	}
}

func TestChatStopReasonsBecomeErrors(t *testing.T) {
	text := []map[string]any{{"type": "text", "text": ""}, {"type": "text_delta", "text": "Sure, here"}}
	cases := []struct {
		name   string
		client func(url string) llm.Client
		events []string
		want   error
	}{
		{"anthropic refusal", anthropicClient, anthropicStream("refusal", text), llm.ErrRefused},
		{"anthropic max tokens", anthropicClient, anthropicStream("max_tokens", text), llm.ErrTruncated},
		{"openai length", openAIClient, []string{openAIChunk(map[string]any{"content": "Sure, here"}, "length"), "data: [DONE]"}, llm.ErrTruncated},
		{"openai refusal", openAIClient, []string{openAIChunk(map[string]any{"refusal": "I can't"}, "stop"), "data: [DONE]"}, llm.ErrRefused},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &sseAPI{events: c.events}
			_, err := c.client(api.start(t)).Chat(context.Background(), chatRequest(), func(string) {})
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestChatProviderErrorIsReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	t.Cleanup(srv.Close)
	for name, client := range map[string]llm.Client{"anthropic": anthropicClient(srv.URL), "openai": openAIClient(srv.URL)} {
		t.Run(name, func(t *testing.T) {
			_, err := client.Chat(context.Background(), chatRequest(), func(string) {})
			if err == nil || !strings.Contains(err.Error(), "401") {
				t.Errorf("err = %v, want a 401", err)
			}
		})
	}
}

func anthropicClient(url string) llm.Client { return llm.NewAnthropic("sk-test", url) }
func openAIClient(url string) llm.Client    { return llm.NewOpenAI("sk-test", url) }
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/llm/`
Expected: build failure. `client.Chat undefined`, `undefined: llm.ChatRequest`.

- [ ] **Step 3: Add the chat types**

`internal/llm/llm.go`. Replace the whole file with:

```go
// Package llm is the provider-neutral way to call a language model.
package llm

import (
	"context"
	"encoding/json"
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
	// Chat runs one model turn of a conversation that may call tools. It calls onText with each
	// piece of reply text as it arrives, and returns the whole reply once the model stops.
	Chat(ctx context.Context, req ChatRequest, onText func(string)) (Reply, error)
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
	// Fallback asks the provider to re-serve a refused request on another model. Only the Claude
	// API supports it, on its main-tier models; other providers ignore it.
	Fallback bool
}

// Role is who sent a chat message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one message of a chat. An assistant message can carry tool calls, and the user
// message after it carries their results.
type Message struct {
	Role        Role
	Text        string
	ToolCalls   []ToolCall
	ToolResults []ToolResult
}

// ToolCall is the model asking to run a tool. Input is a JSON object matching the tool's schema.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult answers one tool call. IsError tells the model the call failed and why.
type ToolResult struct {
	CallID  string
	Content string
	IsError bool
}

// Tool is a function the model can call. Schema is a JSON schema for its input, built with the
// jsonschema package so both providers accept it in strict mode.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
}

type ChatRequest struct {
	Model     string
	System    []Block
	Messages  []Message
	Tools     []Tool
	MaxTokens int64
	// Fallback is as for Request.
	Fallback bool
}

// Reply is what the model said in one turn. Tool calls, if any, wait for their results.
type Reply struct {
	Text      string
	ToolCalls []ToolCall
}
```

- [ ] **Step 4: Implement Anthropic chat**

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
		System:    anthropicSystem(req.System),
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(req.User))},
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

func (a *anthropicClient) Chat(ctx context.Context, req ChatRequest, onText func(string)) (Reply, error) {
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: req.MaxTokens,
		System:    anthropicSystem(req.System),
	}
	for _, m := range req.Messages {
		params.Messages = append(params.Messages, anthropicMessage(m))
	}
	for _, t := range req.Tools {
		params.Tools = append(params.Tools, anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{
				Properties:  t.Schema["properties"],
				Required:    t.Schema["required"].([]string),
				ExtraFields: map[string]any{"additionalProperties": false},
			},
			Strict: anthropic.Bool(true),
		}})
	}
	if req.Fallback {
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}

	stream := a.client.Beta.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	var msg anthropic.BetaMessage
	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			return Reply{}, fmt.Errorf("anthropic: %w", err)
		}
		if delta, ok := event.AsAny().(anthropic.BetaRawContentBlockDeltaEvent); ok {
			if text, ok := delta.Delta.AsAny().(anthropic.BetaTextDelta); ok {
				onText(text.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return Reply{}, fmt.Errorf("anthropic: %w", err)
	}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return Reply{}, ErrRefused
	case anthropic.BetaStopReasonMaxTokens:
		return Reply{}, ErrTruncated
	}
	var reply Reply
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			reply.Text += block.Text
		case "tool_use":
			reply.ToolCalls = append(reply.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Input: block.Input})
		}
	}
	return reply, nil
}

func anthropicSystem(blocks []Block) []anthropic.BetaTextBlockParam {
	var system []anthropic.BetaTextBlockParam
	for _, b := range blocks {
		block := anthropic.BetaTextBlockParam{Text: b.Text}
		if b.Cache {
			block.CacheControl = anthropic.NewBetaCacheControlEphemeralParam()
		}
		system = append(system, block)
	}
	return system
}

func anthropicMessage(m Message) anthropic.BetaMessageParam {
	var blocks []anthropic.BetaContentBlockParamUnion
	if m.Text != "" {
		blocks = append(blocks, anthropic.NewBetaTextBlock(m.Text))
	}
	for _, c := range m.ToolCalls {
		blocks = append(blocks, anthropic.NewBetaToolUseBlock(c.ID, c.Input, c.Name))
	}
	for _, r := range m.ToolResults {
		blocks = append(blocks, anthropic.NewBetaToolResultBlock(r.CallID, r.Content, r.IsError))
	}
	if m.Role == RoleAssistant {
		return anthropic.BetaMessageParam{Role: anthropic.BetaMessageParamRoleAssistant, Content: blocks}
	}
	return anthropic.NewBetaUserMessage(blocks...)
}
```

The streamed `BetaMessage.Accumulate` keeps each `tool_use` block's input as raw JSON on the content union, which is why the reply reads `block.Input` rather than the typed `BetaToolUseBlock` (whose `Input` is `any`). `InputSchema` takes `properties` and `required` as fields. `additionalProperties` goes through `ExtraFields`.

- [ ] **Step 5: Implement OpenAI chat**

`internal/llm/openai.go`. Replace the whole file with:

```go
package llm

import (
	"context"
	"encoding/json"
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
	if system := openAISystem(req.System); system != "" {
		params.Messages = append(params.Messages, openai.SystemMessage(system))
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

func (o *openAIClient) Chat(ctx context.Context, req ChatRequest, onText func(string)) (Reply, error) {
	params := openai.ChatCompletionNewParams{
		Model:               shared.ChatModel(req.Model),
		MaxCompletionTokens: openai.Int(req.MaxTokens),
	}
	if system := openAISystem(req.System); system != "" {
		params.Messages = append(params.Messages, openai.SystemMessage(system))
	}
	for _, m := range req.Messages {
		params.Messages = append(params.Messages, openAIMessages(m)...)
	}
	for _, t := range req.Tools {
		params.Tools = append(params.Tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  shared.FunctionParameters(t.Schema),
			Strict:      openai.Bool(true),
		}))
	}

	stream := o.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()
	var acc openai.ChatCompletionAccumulator
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			onText(chunk.Choices[0].Delta.Content)
		}
	}
	if err := stream.Err(); err != nil {
		return Reply{}, fmt.Errorf("openai: %w", err)
	}
	if len(acc.Choices) == 0 {
		return Reply{}, errors.New("openai: reply had no choices")
	}
	choice := acc.Choices[0]
	if choice.Message.Refusal != "" {
		return Reply{}, ErrRefused
	}
	if choice.FinishReason == "length" {
		return Reply{}, ErrTruncated
	}
	reply := Reply{Text: choice.Message.Content}
	for _, c := range choice.Message.ToolCalls {
		reply.ToolCalls = append(reply.ToolCalls, ToolCall{ID: c.ID, Name: c.Function.Name, Input: json.RawMessage(c.Function.Arguments)})
	}
	return reply, nil
}

// openAISystem joins the layers in order, since chat completions take one system message.
func openAISystem(blocks []Block) string {
	layers := make([]string, len(blocks))
	for i, b := range blocks {
		layers[i] = b.Text
	}
	return strings.Join(layers, "\n\n")
}

// openAIMessages converts one message. Tool results become one tool message each.
func openAIMessages(m Message) []openai.ChatCompletionMessageParamUnion {
	if m.Role == RoleUser {
		var msgs []openai.ChatCompletionMessageParamUnion
		for _, r := range m.ToolResults {
			content := r.Content
			if r.IsError {
				content = "Error: " + content
			}
			msgs = append(msgs, openai.ToolMessage(content, r.CallID))
		}
		if m.Text != "" {
			msgs = append(msgs, openai.UserMessage(m.Text))
		}
		return msgs
	}
	assistant := openai.ChatCompletionAssistantMessageParam{}
	if m.Text != "" {
		assistant.Content.OfString = openai.String(m.Text)
	}
	for _, c := range m.ToolCalls {
		assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID:       c.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{Name: c.Name, Arguments: string(c.Input)},
			},
		})
	}
	return []openai.ChatCompletionMessageParamUnion{{OfAssistant: &assistant}}
}
```

- [ ] **Step 6: Stub `Chat` on the existing fakes**

They only stand in for `Complete`. Each stub panics, because a call would mean a bug.

`internal/cleanup/cleanup_test.go`, after `replyFunc.Complete`:

```go
func (replyFunc) Chat(context.Context, llm.ChatRequest, func(string)) (llm.Reply, error) {
	panic("cleanup never chats")
}
```

`internal/httpapi/settings_test.go`, after `modelFunc.Complete`:

```go
func (modelFunc) Chat(context.Context, llm.ChatRequest, func(string)) (llm.Reply, error) {
	panic("modelFunc doesn't chat; use chatModel")
}
```

`internal/planner/helpers_test.go`: embed `noChat` as the first field of `script`, and add it just before `script.Complete`:

```go
type script struct {
	noChat
	t       *testing.T
	replies []any
	sent    []llm.Request
}

// noChat completes the llm.Client of a fake that the planner only ever calls with Complete.
type noChat struct{}

func (noChat) Chat(context.Context, llm.ChatRequest, func(string)) (llm.Reply, error) {
	panic("the planner never chats")
}
```

`internal/planner/close_test.go`:

```go
type leaving struct {
	noChat
	cancel context.CancelFunc
}
```

`internal/planner/plan_test.go`:

```go
type hang struct{ noChat }
```

- [ ] **Step 7: Run everything**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: no files listed, and every package `ok`.

- [ ] **Step 8: Commit**

```bash
git add internal/llm internal/cleanup/cleanup_test.go internal/httpapi/settings_test.go internal/planner/helpers_test.go internal/planner/close_test.go internal/planner/plan_test.go
git commit -m "Stream chat turns with tool calls from both providers"
```

---

### Task 2: Chat storage, recent days, and event lookup

Messages are stored by date, with their tool chips and the reply's batch. They aren't in batches, because undo reverts changes, never the conversation. A date's running summary sits in `chat_folds`. `DaysBetween` gives the agent the last week of summaries. `GetEvent` lets update tools read an event before changing it. `LastUndoableBatch` learns to leave out the reply in progress.

**Files:**
- Create: `internal/store/migrations/0005_chat.sql`
- Modify: `internal/domain/domain.go`
- Create: `internal/service/messages.go`
- Modify: `internal/service/days.go`, `internal/service/events.go`, `internal/service/undo.go`
- Create: `internal/service/messages_test.go`
- Modify: `internal/service/events_test.go`, `internal/service/undo_test.go`, `internal/service/days_test.go`

**Interfaces:**
- Produces:
  - `domain.Role` (`RoleUser`, `RoleAssistant`). `domain.ToolUse{Tool, Label, Error string}`.
  - `domain.Message{ID int64; Date Date; Role Role; Text string; Tools []ToolUse; BatchID int64; Error string; CreatedAt time.Time}`, where `BatchID` 0 means no changes.
  - `domain.ChatFold{Summary string; ThroughID int64}`
  - `(*Service).AddMessage(ctx, domain.Message) (domain.Message, error)`: `ErrInvalid` for an unknown role. Nil `Tools` is stored as an empty list.
  - `(*Service).Messages(ctx, date domain.Date) ([]domain.Message, error)`: oldest first.
  - `(*Service).ChatFold(ctx, date) (domain.ChatFold, error)`: zero value if none. `(*Service).SetChatFold(ctx, date, domain.ChatFold) error` replaces it.
  - `(*Service).DaysBetween(ctx, from, to domain.Date) ([]domain.Day, error)`: dates in `[from, to)`.
  - `(*Service).GetEvent(ctx, id int64) (domain.Event, error)`: `ErrNotFound` if deleted or unknown.
  - `(*Service).LastUndoableBatch(ctx, actor, except int64) (int64, error)`: pass 0 to leave none out.

- [ ] **Step 1: Write the failing tests**

`internal/service/messages_test.go`. Create it with:

```go
package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestMessagesAreKeptByDate(t *testing.T) {
	svc, clock := newService(t)
	ctx := context.Background()
	sent, err := svc.AddMessage(ctx, domain.Message{Date: "2026-10-05", Role: domain.RoleUser, Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(1)
	reply, err := svc.AddMessage(ctx, domain.Message{Date: "2026-10-05", Role: domain.RoleAssistant, Text: "Morning!",
		Tools: []domain.ToolUse{{Tool: "what_now", Label: `Picked #3 "RFC"`}}, BatchID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddMessage(ctx, domain.Message{Date: "2026-10-06", Role: domain.RoleUser, Text: "tomorrow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddMessage(ctx, domain.Message{Date: "2026-10-05", Role: "system", Text: "x"}); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("bad role: err = %v, want ErrInvalid", err)
	}

	got, err := svc.Messages(ctx, "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]domain.Message{sent, reply}, got); diff != "" {
		t.Errorf("messages (-want +got):\n%s", diff)
	}
	if sent.Tools == nil || len(got[0].Tools) != 0 {
		t.Errorf("a message without tools should hold an empty list: %+v", sent)
	}
}

func TestChatFoldIsReplaced(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	if f, err := svc.ChatFold(ctx, "2026-10-05"); err != nil || f != (domain.ChatFold{}) {
		t.Fatalf("no fold yet: %+v, %v", f, err)
	}
	for _, f := range []domain.ChatFold{{Summary: "first", ThroughID: 4}, {Summary: "second", ThroughID: 9}} {
		if err := svc.SetChatFold(ctx, "2026-10-05", f); err != nil {
			t.Fatal(err)
		}
	}
	if f, _ := svc.ChatFold(ctx, "2026-10-05"); f != (domain.ChatFold{Summary: "second", ThroughID: 9}) {
		t.Errorf("fold = %+v", f)
	}
}

func TestDaysBetween(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	for _, d := range []domain.Date{"2026-09-28", "2026-09-29", "2026-10-04", "2026-10-05"} {
		if _, err := svc.StartDay(ctx, d, 3, ""); err != nil {
			t.Fatal(err)
		}
	}
	days, err := svc.DaysBetween(ctx, "2026-09-29", "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	var dates []domain.Date
	for _, d := range days {
		dates = append(dates, d.Date)
	}
	if diff := cmp.Diff([]domain.Date{"2026-09-29", "2026-10-04"}, dates); diff != "" {
		t.Errorf("dates (-want +got):\n%s", diff)
	}
}
```

`internal/service/undo_test.go`. In `TestLastUndoableBatch`, pass `0` as the new last argument to both existing `LastUndoableBatch` calls. Then add this at the end of the test:

```go
	newer := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.CreateTask(ctx, newer, service.TaskFields{Title: "c"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.LastUndoableBatch(ctx, domain.ActorAgent, newer); got != older {
		t.Errorf("leaving out %d: got batch %d, want %d", newer, got, older)
	}
```

`internal/service/days_test.go`. Its one `LastUndoableBatch` call becomes `svc.LastUndoableBatch(ctx, domain.ActorAgent, 0)`.

`internal/service/events_test.go`. Add `"github.com/google/go-cmp/cmp"` to the imports. In `TestUpdateAndDeleteEvent`, after the raw-text check, add:

```go
	if got, err := svc.GetEvent(ctx, e.ID); err != nil || !cmp.Equal(got, updated) {
		t.Errorf("get = %+v, %v; want %+v", got, err, updated)
	}
```

At the end of the test, add:

```go
	if _, err := svc.GetEvent(ctx, e.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("get deleted event: err = %v, want ErrNotFound", err)
	}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/service/`
Expected: build failure. `svc.AddMessage undefined`, `undefined: domain.Message`, and `too many arguments in call to svc.LastUndoableBatch`.

- [ ] **Step 3: Add the migration**

`internal/store/migrations/0005_chat.sql`:

```sql
-- Chat messages are keyed by date, not day, so chat works before the day is started.
CREATE TABLE messages (
    id INTEGER PRIMARY KEY,
    date TEXT NOT NULL,
    role TEXT NOT NULL,
    text TEXT NOT NULL,
    tools TEXT NOT NULL DEFAULT '[]',
    batch_id INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX messages_date ON messages(date, id);

-- The running summary of a date's chat. Messages up to through_id are folded into it.
CREATE TABLE chat_folds (
    date TEXT PRIMARY KEY,
    summary TEXT NOT NULL,
    through_id INTEGER NOT NULL
);
```

- [ ] **Step 4: Add the domain types**

`internal/domain/domain.go`. Append:

```go
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ToolUse is one tool the agent ran in a reply, shown as a chip. Error is set when it failed.
type ToolUse struct {
	Tool  string `json:"tool"`
	Label string `json:"label"`
	Error string `json:"error"`
}

// Message is one chat message. BatchID is the reply's changes, or 0 when it changed nothing. Error
// is set on a reply the provider couldn't give, and Text then says so in plain words.
type Message struct {
	ID        int64     `json:"id"`
	Date      Date      `json:"date"`
	Role      Role      `json:"role"`
	Text      string    `json:"text"`
	Tools     []ToolUse `json:"tools"`
	BatchID   int64     `json:"batch_id"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
}

// ChatFold is the running summary of a date's older chat. Messages up to ThroughID are in it.
type ChatFold struct {
	Summary   string `json:"summary"`
	ThroughID int64  `json:"through_id"`
}
```

- [ ] **Step 5: Store messages and folds**

`internal/service/messages.go`:

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

// AddMessage stores a chat message and returns it with its ID and time. Messages aren't in
// batches: undo takes back what a reply changed, never the conversation.
func (s *Service) AddMessage(ctx context.Context, m domain.Message) (domain.Message, error) {
	if m.Role != domain.RoleUser && m.Role != domain.RoleAssistant {
		return m, fmt.Errorf("%w: role %q", ErrInvalid, m.Role)
	}
	if m.Tools == nil {
		m.Tools = []domain.ToolUse{}
	}
	tools, err := json.Marshal(m.Tools)
	if err != nil {
		return m, err
	}
	m.CreatedAt = s.now().UTC()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (date, role, text, tools, batch_id, error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.Date, m.Role, m.Text, string(tools), m.BatchID, m.Error, formatTime(m.CreatedAt))
	if err != nil {
		return m, err
	}
	m.ID, err = res.LastInsertId()
	return m, err
}

// Messages returns a date's chat, oldest first.
func (s *Service) Messages(ctx context.Context, date domain.Date) ([]domain.Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, date, role, text, tools, batch_id, error, created_at FROM messages WHERE date = ? ORDER BY id`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs := []domain.Message{}
	for rows.Next() {
		var m domain.Message
		var tools, createdAt string
		if err := rows.Scan(&m.ID, &m.Date, &m.Role, &m.Text, &tools, &m.BatchID, &m.Error, &createdAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tools), &m.Tools); err != nil {
			return nil, err
		}
		if m.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// ChatFold returns a date's running chat summary, or an empty one if nothing is folded yet.
func (s *Service) ChatFold(ctx context.Context, date domain.Date) (domain.ChatFold, error) {
	var f domain.ChatFold
	err := s.db.QueryRowContext(ctx, `SELECT summary, through_id FROM chat_folds WHERE date = ?`, date).
		Scan(&f.Summary, &f.ThroughID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChatFold{}, nil
	}
	return f, err
}

func (s *Service) SetChatFold(ctx context.Context, date domain.Date, f domain.ChatFold) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_folds (date, summary, through_id) VALUES (?, ?, ?)
		 ON CONFLICT (date) DO UPDATE SET summary = excluded.summary, through_id = excluded.through_id`,
		date, f.Summary, f.ThroughID)
	return err
}
```

- [ ] **Step 6: `DaysBetween`, `GetEvent`, and `LastUndoableBatch(except)`**

`internal/service/days.go`. Append:

```go
// DaysBetween returns the days started on dates in [from, to), oldest first.
func (s *Service) DaysBetween(ctx context.Context, from, to domain.Date) ([]domain.Day, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+dayColumns+` FROM days WHERE date >= ? AND date < ? ORDER BY date`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := []domain.Day{}
	for rows.Next() {
		d, err := scanDay(rows)
		if err != nil {
			return nil, err
		}
		days = append(days, d)
	}
	return days, rows.Err()
}
```

`internal/service/events.go`. Add `GetEvent` above `ListEvents`. Add `liveEvent` above `eventColumns`. In `changeEvent`, replace the inline lookup (the `scanEvent(tx.QueryRowContext(...))` call and its `sql.ErrNoRows` check) with `liveEvent`:

```go
func (s *Service) GetEvent(ctx context.Context, id int64) (domain.Event, error) {
	return liveEvent(ctx, s.db, id)
}
```

```go
func liveEvent(ctx context.Context, q querier, id int64) (domain.Event, error) {
	e, err := scanEvent(q.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE id = ? AND deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, fmt.Errorf("%w: event %d", ErrNotFound, id)
	}
	return e, err
}
```

```go
		before, err := liveEvent(ctx, tx, id)
		if err != nil {
			return err
		}
```

`internal/service/undo.go`. Replace `LastUndoableBatch` with:

```go
// LastUndoableBatch returns the newest batch by actor that changed something and isn't undone,
// other than except. Pass 0 to leave none out.
func (s *Service) LastUndoableBatch(ctx context.Context, actor domain.Actor, except int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM batches
		 WHERE actor = ? AND id != ? AND undone_at IS NULL
		   AND EXISTS (SELECT 1 FROM activity WHERE activity.batch_id = batches.id)
		 ORDER BY id DESC LIMIT 1`, actor, except).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: no undoable %s batch", ErrNotFound, actor)
	}
	return id, err
}
```

- [ ] **Step 7: Run everything**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: every package `ok`.

- [ ] **Step 8: Commit**

```bash
git add internal/store/migrations/0005_chat.sql internal/domain/domain.go internal/service
git commit -m "Store chat messages and folds, and read recent days and single events"
```

---

### Task 3: Typed hub subscriptions and the planner's situation

The replan watcher has a 64-message buffer, and the hub drops messages when a buffer is full. Once chat deltas flow through the hub, a long reply during a replan could push a `batch.applied` out of that buffer. `Subscribe` therefore takes optional message types, and the watcher in `main` takes batch messages only. The planner exports `Situation`: the model, settings, and live context, which is the rendered state minus the last day's summary (the agent renders a week of them). `Persona` is exported so the agent shares the voice.

**Files:**
- Modify: `internal/stream/hub.go`, `internal/stream/hub_test.go`
- Modify: `internal/planner/planner.go`, `internal/planner/instructions.go`
- Create: `internal/planner/situation_test.go`
- Modify: `cmd/whatnow/main.go`

**Interfaces:**
- Produces:
  - `(*stream.Hub).Subscribe(types ...string) (<-chan stream.Message, func())`: no types means every message.
  - `planner.Persona` (const string)
  - `planner.Situation{Model llm.Client; Config settings.ProviderConfig; Settings settings.Settings; ModelErr error; Context string}`
  - `(*Planner).Situation(ctx) (Situation, error)`: `Model` is nil and `ModelErr` is set when no client could be built.

- [ ] **Step 1: Write the failing tests**

`internal/stream/hub_test.go`. Append:

```go
func TestSubscribeToSomeTypes(t *testing.T) {
	hub := stream.NewHub()
	batches, unsub := hub.Subscribe("batch.applied", "batch.undone")
	defer unsub()

	for range 100 { // more than the buffer holds
		hub.Publish(stream.Message{Type: "chat.delta", Data: "hi"})
	}
	hub.PublishUndone(7)

	select {
	case m := <-batches:
		if m.Type != "batch.undone" {
			t.Errorf("got %q, want batch.undone", m.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("the undo was crowded out")
	}
}
```

`internal/planner/situation_test.go`:

```go
package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/settings"
)

func TestSituationIsTheLiveStateWithoutTheLastDay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	last, err := f.svc.StartDay(ctx, "2026-10-02", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.WrapDay(ctx, last.ID, "start with the RFC"); err != nil {
		t.Fatal(err)
	}
	f.start(t, 2, "tired")

	sit, err := f.planner.Situation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sit.Model == nil || sit.Config.MainModel != "claude-sonnet-5-5" || sit.Settings.Style != "dry humor" {
		t.Errorf("situation = %+v", sit)
	}
	if !strings.Contains(sit.Context, `Pace today: 2 of 5, "gentle".`) || strings.Contains(sit.Context, "start with the RFC") {
		t.Errorf("context:\n%s", sit.Context)
	}

	f.configured = false
	if sit, err = f.planner.Situation(ctx); err != nil || sit.Model != nil || !errors.Is(sit.ModelErr, settings.ErrNotConfigured) {
		t.Errorf("without a provider: %+v, %v", sit, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/stream/ ./internal/planner/`
Expected: build failures. `too many arguments in call to hub.Subscribe`, `f.planner.Situation undefined`.

- [ ] **Step 3: Filter subscriptions**

`internal/stream/hub.go`. Replace `Hub`, `NewHub`, `Subscribe`, and the loop in `Publish`:

```go
type Hub struct {
	mu sync.Mutex
	// subs maps each subscriber to the message types it wants, or nil for every type.
	subs map[chan Message]map[string]bool
}

func NewHub() *Hub {
	return &Hub{subs: map[chan Message]map[string]bool{}}
}

// Subscribe returns a channel of messages and a function that ends the subscription and closes
// the channel. The function is safe to call more than once. Naming types limits the channel to
// them, so a busy stream of other messages can't crowd them out of its buffer.
func (h *Hub) Subscribe(types ...string) (<-chan Message, func()) {
	ch := make(chan Message, buffer)
	var wanted map[string]bool
	if len(types) > 0 {
		wanted = map[string]bool{}
		for _, t := range types {
			wanted[t] = true
		}
	}
	h.mu.Lock()
	h.subs[ch] = wanted
	h.mu.Unlock()
	// ...the rest of Subscribe is unchanged
}

// Publish sends m to every subscriber. A subscriber whose buffer is full misses the message
// instead of stalling every write; the web client refetches when it reconnects.
func (h *Hub) Publish(m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, wanted := range h.subs {
		if wanted != nil && !wanted[m.Type] {
			continue
		}
		select {
		// ...unchanged
```

- [ ] **Step 4: Export the persona and add `Situation`**

`internal/planner/instructions.go`. Rename `persona` to `Persona` in its declaration and in all three uses, and give it a doc comment:

```go
// Persona opens every main-model prompt, so the plans, picks, and chat share one voice.
const Persona = `You help someone with ADHD ...` // text unchanged
```

`internal/planner/planner.go`. Split `context` so the live part stands alone:

```go
// context renders the live context layer: the last day, then the live state.
func (st state) context() string {
	var b strings.Builder
	if st.last != nil && (st.last.Summary != "" || st.last.TomorrowNote != "") {
		fmt.Fprintf(&b, "Last day they used this, %s:", st.last.Date)
		if st.last.Summary != "" {
			fmt.Fprintf(&b, " %s", st.last.Summary)
		}
		if st.last.TomorrowNote != "" {
			fmt.Fprintf(&b, "\nThe note they left for next time: %q", st.last.TomorrowNote)
		}
		b.WriteString("\n\n")
	}
	b.WriteString(st.live())
	return b.String()
}

// live renders pace and mood, the facts, and the current plan.
func (st state) live() string {
	var b strings.Builder
	// ...the rest is the old body of context from "if st.day != nil {" on, unchanged
```

Append:

```go
// Situation is what the chat agent needs for one turn: a model and the live context. Model is
// nil when no client could be built, and ModelErr says why. Context leaves out the last day's
// summary, since the agent renders a week of them.
type Situation struct {
	Model    llm.Client
	Config   settings.ProviderConfig
	Settings settings.Settings
	ModelErr error
	Context  string
}

func (p *Planner) Situation(ctx context.Context) (Situation, error) {
	st, err := p.gather(ctx, true)
	if err != nil {
		return Situation{}, err
	}
	return Situation{Model: st.model, Config: st.cfg, Settings: st.settings, ModelErr: st.modelErr, Context: st.live()}, nil
}
```

- [ ] **Step 5: Give the watcher batch messages only**

`cmd/whatnow/main.go`:

```go
	watched, _ := hub.Subscribe("batch.applied", "batch.undone") // for the life of the process
```

- [ ] **Step 6: Run everything**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: every package `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/stream internal/planner cmd/whatnow/main.go
git commit -m "Let subscribers pick message types and expose the planner's situation"
```

---

### Task 4: The agent's turn

`Send` stores the user's message, then runs a turn. The turn reads the planner's situation, then builds the history (folding it if it has outgrown its budget) and the system prompt, then loops. In each round it calls the model, streams its text as `chat.delta`, and runs any tool calls, publishing each chip as `chat.tool`. Their results go back to the model. Writes share one agent batch, started on the first write and published once, at the end. A provider error ends the turn with a plain reply that keeps the text and writes made so far. The tool set here is the minimum the loop needs: `list_tasks`, `create_task`, `complete_task`, `drop_task`. Task 5 adds the rest.

**Files:**
- Create: `internal/agent/agent.go`, `internal/agent/context.go`, `internal/agent/instructions.go`, `internal/agent/tools.go`
- Create: `internal/agent/helpers_test.go`, `internal/agent/agent_test.go`, `internal/agent/context_test.go`

**Interfaces:**
- Consumes: `llm.Client.Chat` and the chat types (Task 1). `service.AddMessage`, `Messages`, `ChatFold`, `SetChatFold`, and `DaysBetween` (Task 2). `planner.Situation` and `planner.Persona` (Task 3).
- Produces:
  - `agent.New(svc *service.Service, hub *stream.Hub, p *planner.Planner, loc *time.Location, now func() time.Time) *agent.Agent`
  - `(*Agent).Send(ctx, text string) (domain.Message, error)`: returns the stored reply. Errors: `service.ErrInvalid` for an empty message or one over 4,000 characters, and `agent.ErrBusy` while another turn runs. A provider error is not returned; it becomes the reply.
  - `(*Agent).History(ctx) ([]domain.Message, error)`: today's chat.
  - Hub messages: `chat.message` (`domain.Message`, for the user's message and the reply), `chat.delta` (`agent.Delta{Text}`), `chat.tool` (`domain.ToolUse`).
  - In package `agent`, for Task 5: `tool{name, description string; schema map[string]any; run func(ctx, *turn, json.RawMessage) (label string, result any, err error)}`, the `tools` slice, `args[T]`, `optional[T]`, `brief`/`briefOf`, `name`, `taskID`, `(*turn).batch(ctx) (int64, error)`, `(*Agent).today() domain.Date`.

- [ ] **Step 1: Write the failing tests**

`internal/agent/helpers_test.go`:

```go
package agent_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/agent"
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

// model is a fake provider. Chat answers with the next scripted reply, streaming its text in two
// pieces; Complete answers with the next scripted completion. A scripted error is returned as one.
// It records every request.
type model struct {
	t           *testing.T
	mu          sync.Mutex
	replies     []any
	completions []any
	chats       []llm.ChatRequest
	completes   []llm.Request
}

func (m *model) Chat(_ context.Context, req llm.ChatRequest, onText func(string)) (llm.Reply, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chats = append(m.chats, req)
	if len(m.replies) == 0 {
		m.t.Fatalf("unexpected chat call %d", len(m.chats))
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	if err, ok := r.(error); ok {
		return llm.Reply{}, err
	}
	reply := r.(llm.Reply)
	if half := len(reply.Text) / 2; half > 0 {
		onText(reply.Text[:half])
		onText(reply.Text[half:])
	}
	return reply, nil
}

func (m *model) Complete(_ context.Context, req llm.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completes = append(m.completes, req)
	if len(m.completions) == 0 {
		m.t.Fatalf("unexpected completion %d", len(m.completes))
	}
	r := m.completions[0]
	m.completions = m.completions[1:]
	if err, ok := r.(error); ok {
		return "", err
	}
	if s, ok := r.(string); ok {
		return s, nil
	}
	b, _ := json.Marshal(r)
	return string(b), nil
}

type fixture struct {
	svc   *service.Service
	hub   *stream.Hub
	agent *agent.Agent
	model *model
	// configured false means no provider is set up.
	configured bool
	// client, when set, answers instead of model.
	client llm.Client
}

func newFixture(t *testing.T, replies ...any) *fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := func() time.Time { return time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC) }
	f := &fixture{svc: service.New(db, now), hub: stream.NewHub(), model: &model{t: t, replies: replies}, configured: true}
	s := settings.Defaults()
	s.Provider = settings.Anthropic
	s.Style = "dry humor"
	client := func(context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
		if !f.configured {
			return nil, settings.ProviderConfig{}, settings.Defaults(), settings.ErrNotConfigured
		}
		if f.client != nil {
			return f.client, s.Config(), s, nil
		}
		return f.model, s.Config(), s, nil
	}
	f.agent = agent.New(f.svc, f.hub, planner.New(f.svc, f.hub, client, loc, now), loc, now)
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

func (f *fixture) send(t *testing.T, text string) domain.Message {
	t.Helper()
	reply, err := f.agent.Send(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

// call is a tool call with its input given as a value.
func call(id, name string, input any) llm.ToolCall {
	b, _ := json.Marshal(input)
	return llm.ToolCall{ID: id, Name: name, Input: b}
}

func calls(cs ...llm.ToolCall) llm.Reply { return llm.Reply{ToolCalls: cs} }

func text(s string) llm.Reply { return llm.Reply{Text: s} }

// drain returns every message waiting on a subscription.
func drain(msgs <-chan stream.Message) []stream.Message {
	var got []stream.Message
	for {
		select {
		case m := <-msgs:
			got = append(got, m)
		default:
			return got
		}
	}
}

func system(blocks []llm.Block) string {
	var b strings.Builder
	for _, blk := range blocks {
		b.WriteString(blk.Text)
	}
	return b.String()
}

func date(d string) *domain.Date { v := domain.Date(d); return &v }
```

`internal/agent/agent_test.go`:

```go
package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jameynakama/whatnow/internal/agent"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestATurnsWritesShareOneBatchAndStream(t *testing.T) {
	f := newFixture(t,
		calls(call("c1", "create_task", map[string]any{"title": "Call the dentist", "notes": "", "size": "S", "energy": "",
			"feeling": "", "due_on": "2026-10-06", "not_before": ""}),
			call("c2", "complete_task", map[string]any{"task_id": 1})),
		text("Booked in, and the RFC's done."))
	rfc := f.task(t, service.TaskFields{Title: "Write the RFC"})
	msgs, unsub := f.hub.Subscribe()
	defer unsub()

	reply := f.send(t, "finished the RFC, and remind me to call the dentist tomorrow")

	if reply.Role != domain.RoleAssistant || reply.Text != "Booked in, and the RFC's done." || reply.Error != "" {
		t.Errorf("reply = %+v", reply)
	}
	wantTools := []domain.ToolUse{
		{Tool: "create_task", Label: `Added #2 "Call the dentist"`},
		{Tool: "complete_task", Label: fmt.Sprintf("Done: #%d %q", rfc.ID, rfc.Title)},
	}
	if diff := cmp.Diff(wantTools, reply.Tools); diff != "" {
		t.Errorf("tools (-want +got):\n%s", diff)
	}
	acts, err := f.svc.BatchActivity(context.Background(), reply.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 2 {
		t.Errorf("batch %d has %d changes, want both", reply.BatchID, len(acts))
	}

	var types []string
	for _, m := range drain(msgs) {
		types = append(types, m.Type)
	}
	want := []string{"chat.message", "chat.tool", "chat.tool", "chat.delta", "chat.delta", "batch.applied", "chat.message"}
	if diff := cmp.Diff(want, types); diff != "" {
		t.Errorf("published (-want +got):\n%s", diff)
	}

	// The second call carries the first reply's calls and their results.
	second := f.model.chats[1].Messages
	if n := len(second); n != 3 || second[1].Role != llm.RoleAssistant || len(second[2].ToolResults) != 2 {
		t.Fatalf("second call messages = %+v", second)
	}
	for _, r := range second[2].ToolResults {
		if r.IsError {
			t.Errorf("result %s failed: %s", r.CallID, r.Content)
		}
	}
	history, _ := f.agent.History(context.Background())
	if len(history) != 2 || history[0].Text != "finished the RFC, and remind me to call the dentist tomorrow" {
		t.Errorf("history = %+v", history)
	}
}

func TestAFailedToolGoesBackToTheModelAndEarlierWritesStay(t *testing.T) {
	f := newFixture(t,
		calls(call("c1", "create_task", map[string]any{"title": "Water plants", "notes": "", "size": "", "energy": "",
			"feeling": "", "due_on": "", "not_before": ""}),
			call("c2", "complete_task", map[string]any{"task_id": 99}),
			call("c3", "make_coffee", map[string]any{}),
			llm.ToolCall{ID: "c4", Name: "complete_task", Input: json.RawMessage(`{"task_id":"seven"}`)}),
		text("Added it. I couldn't find the other one."))

	reply := f.send(t, "add water plants and finish #99")

	results := f.model.chats[1].Messages[2].ToolResults
	if results[0].IsError || !results[1].IsError || !strings.Contains(results[1].Content, "not found") ||
		!results[2].IsError || !strings.Contains(results[2].Content, `no tool "make_coffee"`) ||
		!results[3].IsError || !strings.Contains(results[3].Content, "doesn't match the schema") {
		t.Errorf("results = %+v", results)
	}
	if reply.Tools[1].Error == "" || reply.Tools[0].Error != "" {
		t.Errorf("tools = %+v", reply.Tools)
	}
	acts, _ := f.svc.BatchActivity(context.Background(), reply.BatchID)
	if len(acts) != 1 || acts[0].Action != "create" {
		t.Errorf("batch = %+v, want the one create", acts)
	}
}

func TestAReplyWithoutWritesHasNoBatch(t *testing.T) {
	f := newFixture(t, calls(call("c1", "list_tasks", map[string]any{"status": "done"})), text("Nothing done yet."))
	reply := f.send(t, "what have I finished?")
	if reply.BatchID != 0 {
		t.Errorf("batch = %d, want 0", reply.BatchID)
	}
	if got := f.model.chats[1].Messages[2].ToolResults[0].Content; got != "[]" {
		t.Errorf("list result = %s", got)
	}
}

func TestProviderErrorsBecomeAPlainReply(t *testing.T) {
	cases := []struct {
		name       string
		configured bool
		reply      any
		want       string
	}{
		{"no provider", false, nil, "No AI provider is set up yet"},
		{"outage", true, errors.New("anthropic: 529 overloaded"), "couldn't reach the AI provider"},
		{"refusal", true, llm.ErrRefused, "declined"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.configured = c.configured
			if c.reply != nil {
				f.model.replies = []any{c.reply}
			}
			reply := f.send(t, "hello?")
			if !strings.Contains(reply.Text, c.want) || reply.Error == "" {
				t.Errorf("reply = %+v", reply)
			}
		})
	}
}

func TestAnErrorAfterWritesKeepsThem(t *testing.T) {
	f := newFixture(t,
		calls(call("c1", "create_task", map[string]any{"title": "Water plants", "notes": "", "size": "", "energy": "",
			"feeling": "", "due_on": "", "not_before": ""})),
		errors.New("openai: 500"))
	msgs, unsub := f.hub.Subscribe("batch.applied")
	defer unsub()

	reply := f.send(t, "add water plants")

	if reply.Error == "" || reply.BatchID == 0 || len(reply.Tools) != 1 {
		t.Errorf("reply = %+v", reply)
	}
	if got := drain(msgs); len(got) != 1 {
		t.Errorf("published %d batches, want the one with the write", len(got))
	}
}

func TestTheLoopStopsAfterTenRounds(t *testing.T) {
	look := calls(call("c", "list_tasks", map[string]any{"status": "open"}))
	replies := make([]any, 11)
	for i := range replies {
		replies[i] = look
	}
	f := newFixture(t, replies...)

	reply := f.send(t, "keep looking")

	if len(f.model.chats) != 11 || len(reply.Tools) != 10 {
		t.Errorf("%d calls and %d tool runs, want 11 and 10", len(f.model.chats), len(reply.Tools))
	}
	if !strings.Contains(reply.Text, "stopped after 10 rounds") {
		t.Errorf("text = %q", reply.Text)
	}
}

func TestOneTurnAtATime(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	f.client = blocking{model: f.model, release: release}
	f.model.replies = []any{text("first"), text("second")}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		f.send(t, "one")
	}()
	<-release // the first turn is in its model call
	if _, err := f.agent.Send(context.Background(), "two"); !errors.Is(err, agent.ErrBusy) {
		t.Errorf("err = %v, want ErrBusy", err)
	}
	release <- struct{}{}
	wg.Wait()
	f.client = f.model
	if reply := f.send(t, "three"); reply.Text != "second" {
		t.Errorf("after the first turn: %+v", reply)
	}
}

// blocking hands over release when a chat starts, then waits for it back.
type blocking struct {
	*model
	release chan struct{}
}

func (b blocking) Chat(ctx context.Context, req llm.ChatRequest, onText func(string)) (llm.Reply, error) {
	b.release <- struct{}{}
	<-b.release
	return b.model.Chat(ctx, req, onText)
}

func TestMessagesAreChecked(t *testing.T) {
	f := newFixture(t)
	for _, text := range []string{"   ", strings.Repeat("a", 4001)} {
		if _, err := f.agent.Send(context.Background(), text); !errors.Is(err, service.ErrInvalid) {
			t.Errorf("%d chars: err = %v, want ErrInvalid", len(text), err)
		}
	}
	if history, _ := f.agent.History(context.Background()); len(history) != 0 {
		t.Errorf("stored %d messages", len(history))
	}
}
```

`internal/agent/context_test.go`:

```go
package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
)

func (f *fixture) said(t *testing.T, role domain.Role, text, errText string) domain.Message {
	t.Helper()
	m, err := f.svc.AddMessage(context.Background(), domain.Message{Date: today, Role: role, Text: text, Error: errText})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestHistoryIsTodaysTextWithoutFailedReplies(t *testing.T) {
	f := newFixture(t, text("Sure."))
	if _, err := f.svc.AddMessage(context.Background(), domain.Message{Date: "2026-10-04", Role: domain.RoleUser, Text: "yesterday"}); err != nil {
		t.Fatal(err)
	}
	f.said(t, domain.RoleUser, "hi", "")
	f.said(t, domain.RoleAssistant, "Morning!", "")
	f.said(t, domain.RoleUser, "plan please", "")
	f.said(t, domain.RoleAssistant, "I couldn't reach the AI provider.", "openai: 500")

	f.send(t, "try again")

	want := []llm.Message{
		{Role: llm.RoleUser, Text: "hi"},
		{Role: llm.RoleAssistant, Text: "Morning!"},
		{Role: llm.RoleUser, Text: "plan please\n\ntry again"},
	}
	if diff := cmp.Diff(want, f.model.chats[0].Messages); diff != "" {
		t.Errorf("messages (-want +got):\n%s", diff)
	}
}

func TestOldTurnsFoldIntoARunningSummary(t *testing.T) {
	f := newFixture(t, text("Got it."), text("Still here."))
	long := strings.Repeat("x", 5000)
	var first domain.Message
	for i := range 3 {
		m := f.said(t, domain.RoleUser, long, "")
		if i == 0 {
			first = m
		}
		f.said(t, domain.RoleAssistant, long, "")
	}
	f.model.completions = []any{"They asked about x three times."}

	f.send(t, "and now?")

	sent := f.model.chats[0]
	if n := len(sent.Messages); n != 3 || sent.Messages[0].Role != llm.RoleUser || sent.Messages[2].Text != "and now?" {
		t.Errorf("sent %d messages: %+v", n, sent.Messages)
	}
	if !strings.Contains(system(sent.System), "Earlier in today's chat: They asked about x three times.") {
		t.Error("the summary is missing from the context")
	}
	fold, _ := f.svc.ChatFold(context.Background(), today)
	if fold.Summary != "They asked about x three times." || fold.ThroughID != first.ID+3 {
		t.Errorf("fold = %+v", fold)
	}
	folded := f.model.completes[0]
	if folded.Model != "claude-haiku-4-5" || strings.Count(folded.User, long) != 4 {
		t.Errorf("fold request: model %s, %d long messages", folded.Model, strings.Count(folded.User, long))
	}

	// The next turn is under budget again, so it doesn't fold.
	f.send(t, "ok")
	if len(f.model.completes) != 1 {
		t.Errorf("folded again: %d completions", len(f.model.completes))
	}
}

func TestAFailedFoldSendsTheWholeChat(t *testing.T) {
	f := newFixture(t, text("Got it."))
	long := strings.Repeat("x", 9000)
	for range 2 {
		f.said(t, domain.RoleUser, long, "")
		f.said(t, domain.RoleAssistant, long, "")
	}
	f.model.completions = []any{errors.New("openai: 500")}

	reply := f.send(t, "and now?")

	if reply.Error != "" || len(f.model.chats[0].Messages) != 5 {
		t.Errorf("reply %+v after sending %d messages", reply, len(f.model.chats[0].Messages))
	}
	if fold, _ := f.svc.ChatFold(context.Background(), today); fold.ThroughID != 0 {
		t.Errorf("fold = %+v", fold)
	}
}

func TestTheContextCarriesTheWeekAndTheDay(t *testing.T) {
	f := newFixture(t, text("Hi."))
	ctx := context.Background()
	for _, d := range []struct {
		date, summary, note string
	}{
		{"2026-09-27", "Too long ago.", ""},
		{"2026-10-02", "Shipped the auth fix.", "start with the RFC"},
		{"2026-10-03", "", ""},
	} {
		day, err := f.svc.StartDay(ctx, domain.Date(d.date), 3, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.WrapDay(ctx, day.ID, d.note); err != nil {
			t.Fatal(err)
		}
		f.svc.SetDaySummary(ctx, day.ID, d.summary)
	}
	if _, err := f.svc.StartDay(ctx, today, 2, "tired"); err != nil {
		t.Fatal(err)
	}
	f.task(t, service.TaskFields{Title: "Write the RFC"})
	batch, _ := f.svc.NewBatch(ctx, domain.ActorUser)
	end := time.Date(2026, 10, 7, 16, 0, 0, 0, loc)
	for _, e := range []service.EventFields{
		{Title: "Dentist", StartsAt: time.Date(2026, 10, 7, 15, 0, 0, 0, loc), EndsAt: &end},
		{Title: "Next month", StartsAt: time.Date(2026, 11, 7, 15, 0, 0, 0, loc)},
	} {
		if _, err := f.svc.CreateEvent(ctx, batch, e); err != nil {
			t.Fatal(err)
		}
	}

	f.send(t, "hi")

	got := system(f.model.chats[0].System)
	for _, want := range []string{
		"dry humor",
		`- 2026-10-02: Shipped the auth fix. Their note for the next day: "start with the RFC"`,
		`Pace today: 2 of 5, "gentle".`,
		`On their mind: "tired"`,
		"#1 Write the RFC",
		"[1] Wed 2026-10-07 15:00-16:00 Dentist",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context lacks %q", want)
		}
	}
	for _, unwanted := range []string{"Too long ago", "2026-10-03", "Next month", "Last day they used this"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("context has %q", unwanted)
		}
	}
	if req := f.model.chats[0]; req.Model != "claude-sonnet-5-5" || !req.Fallback {
		t.Errorf("request: model %s, fallback %v", req.Model, req.Fallback)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/agent/`
Expected: build failure. `no non-test Go files` or `undefined: agent.New`.

- [ ] **Step 3: Write the instructions**

`internal/agent/instructions.go`:

```go
package agent

import "github.com/jameynakama/whatnow/internal/planner"

const instructions = planner.Persona + `

You're chatting with them during their day. The context shows their tasks, events, and plan as of their latest message. Your tools change things.

- Act when they ask for a change. Every change can be undone, so don't ask first. Ask only when you can't tell what they mean.
- Call tasks by their titles when you write to them. Use #<id> only in tool calls.
- After a tool runs, trust its result over the context, which was rendered before it ran.
- Times are their local time, as YYYY-MM-DDTHH:MM. Dates are YYYY-MM-DD.
- To put a task off, use push_task, not update_task. Pushes are counted, and a task pushed again and again tells you something.
- The plan holds still unless something warrants a change. The app replans by itself when the pace changes, a task becomes due today, a must is dropped, or a blocker on a must changes. Call replan only when they ask for a new plan or when what they tell you changes what today should hold, such as feeling unwell or a new deadline.
- New tasks wait in the unplanned tray until the next replan. You can suggest slotting one in.
- When they ask what to do now, call what_now and pass on its pick in your own words.
- undo_last takes back the most recent change you or the automatic cleanup made, whole. Changes from your current reply aren't included.
- Keep replies short: a sentence or two, unless they ask for more. No lists of what you did; they see each change as it happens.`

const foldInstructions = `You keep a running summary of a chat between someone with ADHD and their planning assistant, so the assistant can follow the conversation after older messages are dropped. Fold the new messages into the summary. Keep what still matters for the rest of the day: requests, decisions, how they're feeling, and anything they asked to be reminded of. Leave out what the tasks and plan already record, and small talk. Write plain sentences, at most 150 words. Reply with the summary only.`
```

The instructions name every tool, including the ones Task 5 adds. A tool the model can't see yet is harmless: it just won't be called.

- [ ] **Step 4: Write the turn**

`internal/agent/agent.go`:

```go
// Package agent runs the chat. Each message from the user starts one turn: the main model replies,
// calling tools that wrap the service and the planner, for up to maxRounds rounds. Text streams to
// every client over the hub as it arrives, and everything the turn changes shares one batch.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/stream"
)

// ErrBusy means a reply is still being written. Chat runs one turn at a time.
var ErrBusy = errors.New("a reply is still being written")

const (
	// maxRounds is how many rounds of tool calls one turn may run.
	maxRounds = 10
	maxTokens = 8192
	// maxMessageRunes bounds what the user sends in one message.
	maxMessageRunes = 4000
)

// callTimeout bounds one model call, so a hung provider can't hold the chat forever.
var callTimeout = 2 * time.Minute

type Agent struct {
	svc     *service.Service
	hub     *stream.Hub
	planner *planner.Planner
	loc     *time.Location
	now     func() time.Time
	mu      sync.Mutex
}

func New(svc *service.Service, hub *stream.Hub, p *planner.Planner, loc *time.Location, now func() time.Time) *Agent {
	return &Agent{svc: svc, hub: hub, planner: p, loc: loc, now: now}
}

// Delta is the chat.delta payload: the next piece of the reply being written.
type Delta struct {
	Text string `json:"text"`
}

// History returns today's chat.
func (a *Agent) History(ctx context.Context) ([]domain.Message, error) {
	return a.svc.Messages(ctx, a.today())
}

func (a *Agent) today() domain.Date { return facts.Today(a.now(), a.loc) }

// Send stores the user's message and runs a turn, returning the reply. Both messages are also
// published as chat.message, and the reply's text as chat.delta while it's written. A provider
// error is not returned: it becomes a reply that says what went wrong.
func (a *Agent) Send(ctx context.Context, text string) (domain.Message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return domain.Message{}, fmt.Errorf("%w: the message is empty", service.ErrInvalid)
	}
	if n := len([]rune(text)); n > maxMessageRunes {
		return domain.Message{}, fmt.Errorf("%w: the message is %d characters; the limit is %d", service.ErrInvalid, n, maxMessageRunes)
	}
	if !a.mu.TryLock() {
		return domain.Message{}, ErrBusy
	}
	defer a.mu.Unlock()

	date := a.today()
	sent, err := a.svc.AddMessage(ctx, domain.Message{Date: date, Role: domain.RoleUser, Text: text})
	if err != nil {
		return domain.Message{}, err
	}
	a.publish(sent)

	t := &turn{agent: a}
	reply := domain.Message{Date: date, Role: domain.RoleAssistant}
	reply.Text, err = t.run(ctx, date)
	if err != nil {
		reply.Text, reply.Error = joinText(reply.Text, errorText(err)), err.Error()
		if !errors.Is(err, settings.ErrNotConfigured) {
			log.Printf("chat: %v", err)
		}
	}
	reply.Tools, reply.BatchID = t.uses, t.batchID
	if t.batchID != 0 {
		acts, err := a.svc.BatchActivity(ctx, t.batchID)
		if err != nil {
			return domain.Message{}, err
		}
		a.hub.PublishBatch(t.batchID, acts)
	}
	reply, err = a.svc.AddMessage(ctx, reply)
	if err != nil {
		return domain.Message{}, err
	}
	a.publish(reply)
	return reply, nil
}

func (a *Agent) publish(m domain.Message) {
	a.hub.Publish(stream.Message{Type: "chat.message", Data: m})
}

// errorText is what the user reads when the model can't reply. The UI links it to settings.
func errorText(err error) string {
	switch {
	case errors.Is(err, settings.ErrNotConfigured):
		return "No AI provider is set up yet, so I can't chat. Add one in settings."
	case errors.Is(err, llm.ErrRefused):
		return "The model declined to answer that."
	case errors.Is(err, llm.ErrTruncated):
		return "My reply ran too long and was cut off. Try asking for less at once."
	}
	return "I couldn't reach the AI provider just now. Check settings, or try again in a bit."
}

func joinText(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	return a + "\n\n" + b
}

// turn is one reply in progress. Its writes share a batch, made on the first write.
type turn struct {
	agent   *Agent
	batchID int64
	uses    []domain.ToolUse
}

// run talks to the model until it stops calling tools or runs out of rounds, and returns what it
// said. On an error, the text so far is returned with it.
func (t *turn) run(ctx context.Context, date domain.Date) (string, error) {
	a := t.agent
	sit, err := a.planner.Situation(ctx)
	if err != nil {
		return "", err
	}
	if sit.Model == nil {
		return "", sit.ModelErr
	}
	history, err := a.history(ctx, date, sit)
	if err != nil {
		return "", err
	}
	system, err := a.system(ctx, date, sit, history.fold)
	if err != nil {
		return "", err
	}
	req := llm.ChatRequest{
		Model:     sit.Config.MainModel,
		System:    system,
		Messages:  history.messages,
		Tools:     toolDefs(),
		MaxTokens: maxTokens,
		Fallback:  true,
	}

	var text string
	for round := 0; ; round++ {
		sep := text != ""
		reply, err := t.call(ctx, sit.Model, req, func(delta string) {
			if sep {
				delta, sep = "\n\n"+delta, false
			}
			a.hub.Publish(stream.Message{Type: "chat.delta", Data: Delta{Text: delta}})
		})
		text = joinText(text, reply.Text)
		if err != nil {
			return text, err
		}
		if len(reply.ToolCalls) == 0 {
			return text, nil
		}
		if round == maxRounds {
			return joinText(text, fmt.Sprintf("(I stopped after %d rounds of changes. Ask me to keep going if there's more.)", maxRounds)), nil
		}
		results := make([]llm.ToolResult, len(reply.ToolCalls))
		for i, c := range reply.ToolCalls {
			results[i] = t.runTool(ctx, c)
		}
		req.Messages = append(req.Messages,
			llm.Message{Role: llm.RoleAssistant, Text: reply.Text, ToolCalls: reply.ToolCalls},
			llm.Message{Role: llm.RoleUser, ToolResults: results})
	}
}

func (t *turn) call(ctx context.Context, model llm.Client, req llm.ChatRequest, onText func(string)) (llm.Reply, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return model.Chat(ctx, req, onText)
}

// batch returns the turn's batch, starting it on the first write.
func (t *turn) batch(ctx context.Context) (int64, error) {
	if t.batchID == 0 {
		id, err := t.agent.svc.NewBatch(ctx, domain.ActorAgent)
		if err != nil {
			return 0, err
		}
		t.batchID = id
	}
	return t.batchID, nil
}
```

- [ ] **Step 5: Write the context and history**

`internal/agent/context.go`:

```go
package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/prompt"
)

const (
	// historyBudget is how much of today's chat, in bytes of text, is sent before the oldest turns are
	// folded into the running summary. Folding keeps about half of it.
	historyBudget = 24000
	recentDays    = 7
	// eventDays is how far ahead the agent sees events, so it can move one later in the week.
	eventDays     = 7
	summaryTokens = 2048
)

// history is today's chat as the model sees it: the running summary of older turns, then the
// rest. Facts are never in it; they're rendered fresh in the system prompt each turn.
type history struct {
	fold     domain.ChatFold
	messages []llm.Message
}

// history reads today's chat, folding the oldest turns into the summary when it has outgrown its
// budget. If folding fails, the whole chat is sent unfolded: too long beats forgetting.
func (a *Agent) history(ctx context.Context, date domain.Date, sit planner.Situation) (history, error) {
	fold, err := a.svc.ChatFold(ctx, date)
	if err != nil {
		return history{}, err
	}
	all, err := a.svc.Messages(ctx, date)
	if err != nil {
		return history{}, err
	}
	var live []domain.Message
	size := 0
	for _, m := range all {
		// A reply that failed told the user so, but it isn't something the model said.
		if m.ID > fold.ThroughID && m.Error == "" {
			live = append(live, m)
			size += len(m.Text)
		}
	}
	if size > historyBudget {
		if cut := foldPoint(live); cut > 0 {
			if summary, err := a.fold(ctx, sit, fold.Summary, live[:cut]); err != nil {
				log.Printf("chat fold: %v", err)
			} else {
				fold = domain.ChatFold{Summary: summary, ThroughID: live[cut-1].ID}
				if err := a.svc.SetChatFold(ctx, date, fold); err != nil {
					return history{}, err
				}
				live = live[cut:]
			}
		}
	}
	return history{fold: fold, messages: toLLM(live)}, nil
}

// foldPoint returns how many of the oldest messages to fold so that the rest fits in half the
// budget. The rest always starts with a user message, and the newest one is never folded.
func foldPoint(msgs []domain.Message) int {
	size := 0
	for _, m := range msgs {
		size += len(m.Text)
	}
	for i, m := range msgs[:len(msgs)-1] {
		size -= len(m.Text)
		if next := msgs[i+1]; next.Role == domain.RoleUser && size <= historyBudget/2 {
			return i + 1
		}
	}
	return len(msgs) - 1
}

// toLLM converts stored messages, merging neighbors from the same sender: a failed reply leaves
// two user messages in a row.
func toLLM(msgs []domain.Message) []llm.Message {
	var out []llm.Message
	for _, m := range msgs {
		role := llm.RoleUser
		if m.Role == domain.RoleAssistant {
			role = llm.RoleAssistant
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Text = joinText(out[n-1].Text, m.Text)
			continue
		}
		out = append(out, llm.Message{Role: role, Text: m.Text})
	}
	return out
}

func (a *Agent) fold(ctx context.Context, sit planner.Situation, summary string, msgs []domain.Message) (string, error) {
	var b strings.Builder
	if summary != "" {
		fmt.Fprintf(&b, "The summary so far:\n%s\n\n", summary)
	}
	b.WriteString("The messages to fold in:\n")
	for _, m := range msgs {
		fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Text)
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return sit.Model.Complete(ctx, llm.Request{
		Model:     sit.Config.LightModel,
		System:    []llm.Block{{Text: foldInstructions}},
		User:      b.String(),
		MaxTokens: summaryTokens,
	})
}

// system builds the turn's system prompt. The context layer carries the last week's summaries,
// the running chat summary, the live state, and the week's events with their IDs.
func (a *Agent) system(ctx context.Context, date domain.Date, sit planner.Situation, fold domain.ChatFold) ([]llm.Block, error) {
	var b strings.Builder
	days, err := a.svc.DaysBetween(ctx, addDays(date, -recentDays), date)
	if err != nil {
		return nil, err
	}
	var recent []string
	for _, d := range days {
		if d.Summary == "" && d.TomorrowNote == "" {
			continue
		}
		line := fmt.Sprintf("- %s:", d.Date)
		if d.Summary != "" {
			line += " " + d.Summary
		}
		if d.TomorrowNote != "" {
			line += fmt.Sprintf(" Their note for the next day: %q", d.TomorrowNote)
		}
		recent = append(recent, line)
	}
	if len(recent) > 0 {
		fmt.Fprintf(&b, "The last few days:\n%s\n\n", strings.Join(recent, "\n"))
	}
	if fold.Summary != "" {
		fmt.Fprintf(&b, "Earlier in today's chat: %s\n\n", fold.Summary)
	}
	b.WriteString(sit.Context)

	now := a.now().In(a.loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, a.loc)
	events, err := a.svc.ListEvents(ctx, start, start.AddDate(0, 0, eventDays+1))
	if err != nil {
		return nil, err
	}
	b.WriteString("\nEvents this week, as [event id]:\n")
	if len(events) == 0 {
		b.WriteString("(none)\n")
	}
	for _, e := range events {
		when := e.StartsAt.In(a.loc).Format("Mon 2006-01-02 15:04")
		if e.EndsAt != nil {
			when += "-" + e.EndsAt.In(a.loc).Format("15:04")
		}
		fmt.Fprintf(&b, "[%d] %s %s\n", e.ID, when, e.Title)
	}
	return prompt.System(instructions, sit.Settings.Style, sit.Settings.AboutMe, b.String()), nil
}

func addDays(d domain.Date, n int) domain.Date {
	t, _ := time.Parse(time.DateOnly, string(d))
	return domain.Date(t.AddDate(0, 0, n).Format(time.DateOnly))
}
```

- [ ] **Step 6: Write the core tools**

`internal/agent/tools.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jameynakama/whatnow/internal/domain"
	js "github.com/jameynakama/whatnow/internal/jsonschema"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

// tool is one function the model can call. run returns the chip label the user sees and the
// result the model reads; an error goes back to the model as a failed call.
type tool struct {
	name, description string
	schema            map[string]any
	run               func(ctx context.Context, t *turn, input json.RawMessage) (label string, result any, err error)
}

// args decodes a tool's input. Strict schemas make a mismatch unlikely, but not impossible.
func args[T any](input json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(input, &v); err != nil {
		return v, fmt.Errorf("%w: the input doesn't match the schema: %v", service.ErrInvalid, err)
	}
	return v, nil
}

var (
	taskID   = js.Integer("The task's number, from #<id>.")
	dateProp = func(what string) map[string]any { return js.String(what + " as YYYY-MM-DD, or empty for none.") }

	// taskProps are create_task's fields. An empty string leaves a field unset.
	taskProps = []js.Field{
		js.Prop("title", js.String("Short and specific, starting with a verb when it fits.")),
		js.Prop("notes", js.String("Details worth keeping, or empty.")),
		js.Prop("size", js.Enum("S (under an hour), M (one to three hours), L (more), or empty if unclear.", "S", "M", "L", "")),
		js.Prop("energy", js.Enum("How much it takes out of them, or empty if unclear.", "low", "med", "high", "")),
		js.Prop("feeling", js.String("How they feel about it, in their words, or empty.")),
		js.Prop("due_on", dateProp("When it's due,")),
		js.Prop("not_before", dateProp("The first day it can be done,")),
	}
)

var tools = []tool{
	{"list_tasks", "List tasks with one status, including ones the context leaves out: done, dropped, and put off until later.",
		js.Object(js.Prop("status", js.Enum("Which tasks to list.", "open", "done", "dropped"))), listTasks},
	{"create_task", "Add a task. It waits in the unplanned tray until the next replan.", js.Object(taskProps...), createTask},
	{"complete_task", "Mark a task done.", js.Object(js.Prop("task_id", taskID)), statusTool(domain.StatusDone, "Done")},
	{"drop_task", "Stop carrying a task without doing it.", js.Object(js.Prop("task_id", taskID)), statusTool(domain.StatusDropped, "Dropped")},
}

func toolDefs() []llm.Tool {
	defs := make([]llm.Tool, len(tools))
	for i, t := range tools {
		defs[i] = llm.Tool{Name: t.name, Description: t.description, Schema: t.schema}
	}
	return defs
}

// runTool runs one call and records its chip. The model gets the result as JSON, or the error.
func (t *turn) runTool(ctx context.Context, c llm.ToolCall) llm.ToolResult {
	use := domain.ToolUse{Tool: c.Name}
	result := llm.ToolResult{CallID: c.ID}
	label, out, err := t.dispatch(ctx, c)
	if err == nil {
		var b []byte
		if b, err = json.Marshal(out); err == nil {
			use.Label, result.Content = label, string(b)
		}
	}
	if err != nil {
		use.Label, use.Error = c.Name, err.Error()
		result.Content, result.IsError = err.Error(), true
	}
	t.uses = append(t.uses, use)
	t.agent.hub.Publish(stream.Message{Type: "chat.tool", Data: use})
	return result
}

func (t *turn) dispatch(ctx context.Context, c llm.ToolCall) (string, any, error) {
	for _, tl := range tools {
		if tl.name == c.Name {
			return tl.run(ctx, t, c.Input)
		}
	}
	return "", nil, fmt.Errorf("there is no tool %q", c.Name)
}

// brief is a task as the model reads it in tool results.
type brief struct {
	ID        int64          `json:"id"`
	Title     string         `json:"title"`
	Status    domain.Status  `json:"status"`
	Notes     string         `json:"notes,omitempty"`
	Size      *domain.Size   `json:"size,omitempty"`
	Energy    *domain.Energy `json:"energy,omitempty"`
	Feeling   string         `json:"feeling,omitempty"`
	DueOn     *domain.Date   `json:"due_on,omitempty"`
	NotBefore *domain.Date   `json:"not_before,omitempty"`
}

func briefOf(t domain.Task) brief {
	return brief{ID: t.ID, Title: t.Title, Status: t.Status, Notes: t.Notes, Size: t.Size, Energy: t.Energy,
		Feeling: t.Feeling, DueOn: t.DueOn, NotBefore: t.NotBefore}
}

func name(t domain.Task) string { return fmt.Sprintf("#%d %q", t.ID, t.Title) }

func listTasks(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		Status domain.Status `json:"status"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	all, err := t.agent.svc.ListTasks(ctx)
	if err != nil {
		return "", nil, err
	}
	out := []brief{}
	for _, task := range all {
		if task.Status == in.Status {
			out = append(out, briefOf(task))
		}
	}
	return fmt.Sprintf("Looked at %s tasks", in.Status), out, nil
}

// taskArgs are create_task's input. Empty strings are unset fields.
type taskArgs struct {
	Title     string `json:"title"`
	Notes     string `json:"notes"`
	Size      string `json:"size"`
	Energy    string `json:"energy"`
	Feeling   string `json:"feeling"`
	DueOn     string `json:"due_on"`
	NotBefore string `json:"not_before"`
}

func optional[T ~string](s string) *T {
	if s == "" {
		return nil
	}
	v := T(s)
	return &v
}

func createTask(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[taskArgs](input)
	if err != nil {
		return "", nil, err
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	task, err := t.agent.svc.CreateTask(ctx, batch, service.TaskFields{
		Title: in.Title, Notes: in.Notes, Size: optional[domain.Size](in.Size), Energy: optional[domain.Energy](in.Energy),
		Feeling: in.Feeling, DueOn: optional[domain.Date](in.DueOn), NotBefore: optional[domain.Date](in.NotBefore),
	})
	if err != nil {
		return "", nil, err
	}
	return "Added " + name(task), briefOf(task), nil
}

func statusTool(status domain.Status, verb string) func(context.Context, *turn, json.RawMessage) (string, any, error) {
	return func(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
		in, err := args[struct {
			TaskID int64 `json:"task_id"`
		}](input)
		if err != nil {
			return "", nil, err
		}
		batch, err := t.batch(ctx)
		if err != nil {
			return "", nil, err
		}
		task, err := t.agent.svc.SetTaskStatus(ctx, batch, in.TaskID, status)
		if err != nil {
			return "", nil, err
		}
		return verb + ": " + name(task), briefOf(task), nil
	}
}
```

- [ ] **Step 7: Run the tests**

Run: `go test -race ./internal/agent/`
Expected: `ok`. If `TestOneTurnAtATime` hangs, `Send` is blocking on the lock instead of using `TryLock`.

- [ ] **Step 8: Run everything**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: every package `ok`.

- [ ] **Step 9: Commit**

```bash
git add internal/agent
git commit -m "Add the chat agent's turn loop, history folding, and task tools"
```

---

### Task 5: The rest of the tools

These cover editing, pushing, blockers, events, planning, pace, and undo. Event times are the user's wall clock as `YYYY-MM-DDTHH:MM`, the same as in cleanup. `set_pace` and `replan` answer `today hasn't started` before the morning flow has run. `undo_last` leaves the current reply's batch alone and publishes `batch.undone` at once, so the replan watcher sees it.

**Files:**
- Modify: `internal/agent/tools.go`
- Create: `internal/agent/tools_test.go`

**Interfaces:**
- Consumes: `service.GetEvent` and `LastUndoableBatch(…, except)` (Task 2). `planner.Replan`, `planner.WhatNow`, and `planner.ErrNoDay`. The tool plumbing from Task 4.
- Produces: the tools `update_task`, `push_task`, `add_blocker`, `remove_blocker`, `create_event`, `update_event`, `delete_event`, `replan`, `what_now`, `set_pace`, `undo_last`, in the order `TestEveryToolIsOffered` lists.

- [ ] **Step 1: Write the failing tests**

`internal/agent/tools_test.go`:

```go
package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestEveryToolIsOffered(t *testing.T) {
	f := newFixture(t, text("Hi."))
	f.send(t, "hi")
	var names []string
	for _, tl := range f.model.chats[0].Tools {
		names = append(names, tl.Name)
	}
	want := []string{"list_tasks", "create_task", "update_task", "complete_task", "drop_task", "push_task", "add_blocker",
		"remove_blocker", "create_event", "update_event", "delete_event", "replan", "what_now", "set_pace", "undo_last"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("tools (-want +got):\n%s", diff)
	}
}

// results runs one round of tool calls and returns what the model got back.
func results(t *testing.T, f *fixture, cs ...llm.ToolCall) []llm.ToolResult {
	t.Helper()
	f.model.replies = append(f.model.replies, calls(cs...), text("ok"))
	f.send(t, "go")
	msgs := f.model.chats[len(f.model.chats)-1].Messages
	return msgs[len(msgs)-1].ToolResults
}

func TestUpdateTaskChangesOnlyTheNamedFields(t *testing.T) {
	f := newFixture(t)
	size := domain.SizeM
	task := f.task(t, service.TaskFields{Title: "Review FLEX1-420", Notes: "the auth bits", Size: &size,
		DueOn: date("2026-10-07"), Refs: []domain.Ref{{Label: "FLEX1-420"}}})

	got := results(t, f, call("c1", "update_task", map[string]any{"task_id": task.ID, "changes": []map[string]string{
		{"field": "due_on", "value": ""}, {"field": "feeling", "value": "dreading it"}}}))
	if got[0].IsError {
		t.Fatal(got[0].Content)
	}
	after, _ := f.svc.GetTask(context.Background(), task.ID)
	if after.DueOn != nil || after.Feeling != "dreading it" || after.Notes != "the auth bits" || after.Size == nil ||
		len(after.Refs) != 1 {
		t.Errorf("after = %+v", after)
	}

	got = results(t, f,
		call("c2", "update_task", map[string]any{"task_id": task.ID, "changes": []map[string]string{{"field": "status", "value": "done"}}}),
		call("c3", "update_task", map[string]any{"task_id": task.ID, "changes": []map[string]string{{"field": "size", "value": "XL"}}}))
	if !got[0].IsError || !strings.Contains(got[0].Content, `no field "status"`) || !got[1].IsError {
		t.Errorf("results = %+v", got)
	}
}

func TestPushTaskCountsAndMustBeLater(t *testing.T) {
	f := newFixture(t)
	task := f.task(t, service.TaskFields{Title: "Clean the garage"})

	got := results(t, f,
		call("c1", "push_task", map[string]any{"task_id": task.ID, "until": string(today)}),
		call("c2", "push_task", map[string]any{"task_id": task.ID, "until": "2026-10-10"}))
	if !got[0].IsError || got[1].IsError {
		t.Errorf("results = %+v", got)
	}
	counts, _ := f.svc.PushCounts(context.Background())
	if counts[task.ID] != 1 {
		t.Errorf("pushes = %d, want 1", counts[task.ID])
	}
}

func TestBlockers(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "Ship it"})
	b := f.task(t, service.TaskFields{Title: "Research pubsub"})
	pair := map[string]any{"task_id": a.ID, "blocked_by_id": b.ID}
	reversed := map[string]any{"task_id": b.ID, "blocked_by_id": a.ID}

	got := results(t, f, call("c1", "add_blocker", pair), call("c2", "add_blocker", reversed))
	if got[0].IsError || !got[1].IsError {
		t.Errorf("add: %+v", got)
	}
	got = results(t, f, call("c3", "remove_blocker", pair), call("c4", "remove_blocker", pair))
	if got[0].IsError || !got[1].IsError {
		t.Errorf("remove: %+v", got)
	}
}

func TestEventsUseLocalTime(t *testing.T) {
	f := newFixture(t)
	got := results(t, f, call("c1", "create_event", map[string]any{"title": "Dentist", "starts_at": "2026-10-07T15:00", "ends_at": ""}))
	if got[0].IsError {
		t.Fatal(got[0].Content)
	}
	var created struct {
		ID       int64  `json:"id"`
		StartsAt string `json:"starts_at"`
	}
	json.Unmarshal([]byte(got[0].Content), &created)
	e, _ := f.svc.GetEvent(context.Background(), created.ID)
	if !e.StartsAt.Equal(time.Date(2026, 10, 7, 20, 0, 0, 0, time.UTC)) || created.StartsAt != "2026-10-07T15:00" {
		t.Errorf("starts at %v, reported %s", e.StartsAt, created.StartsAt)
	}

	got = results(t, f,
		call("c2", "update_event", map[string]any{"event_id": e.ID, "changes": []map[string]string{{"field": "ends_at", "value": "2026-10-07T16:00"}}}),
		call("c3", "update_event", map[string]any{"event_id": e.ID, "changes": []map[string]string{{"field": "starts_at", "value": "3pm"}}}),
		call("c4", "update_event", map[string]any{"event_id": e.ID, "changes": []map[string]string{{"field": "starts_at", "value": ""}}}))
	if got[0].IsError || !strings.Contains(got[1].Content, "not YYYY-MM-DDTHH:MM") || !got[2].IsError {
		t.Errorf("updates = %+v", got)
	}
	e, _ = f.svc.GetEvent(context.Background(), e.ID)
	if e.EndsAt == nil || !e.EndsAt.Equal(time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)) {
		t.Errorf("ends at %v", e.EndsAt)
	}

	got = results(t, f, call("c5", "delete_event", map[string]any{"event_id": e.ID}), call("c6", "delete_event", map[string]any{"event_id": e.ID}))
	if got[0].IsError || !got[1].IsError {
		t.Errorf("deletes = %+v", got)
	}
}

func TestPaceAndPlanningNeedAStartedDay(t *testing.T) {
	f := newFixture(t)
	f.task(t, service.TaskFields{Title: "Write the RFC", DueOn: date(string(today))})

	got := results(t, f,
		call("c1", "set_pace", map[string]any{"level": 2, "note": ""}),
		call("c2", "replan", map[string]any{"reason": "feeling rough"}))
	for _, r := range got {
		if !r.IsError || !strings.Contains(r.Content, "today hasn't started") {
			t.Errorf("result = %+v", r)
		}
	}

	if _, err := f.svc.StartDay(context.Background(), today, 3, ""); err != nil {
		t.Fatal(err)
	}
	f.model.completions = []any{map[string]any{"entries": []any{map[string]any{"task_id": 1, "rationale": "due", "tier": "must"}}, "opening": "Gently."}}
	got = results(t, f,
		call("c3", "set_pace", map[string]any{"level": 2, "note": "slept badly"}),
		call("c4", "replan", map[string]any{"reason": "feeling rough"}))
	if got[0].IsError || got[1].IsError {
		t.Fatalf("results = %+v", got)
	}
	day, _ := f.svc.GetDay(context.Background(), today)
	if day.Pace != 2 || day.MoodNote != "slept badly" {
		t.Errorf("day = %+v", day)
	}
	plan, _ := f.svc.LatestPlan(context.Background(), day.ID)
	if plan.Trigger != domain.TriggerAgent || plan.Reason != "feeling rough" || plan.Opening != "Gently." {
		t.Errorf("plan = %+v", plan)
	}
}

func TestWhatNow(t *testing.T) {
	f := newFixture(t)
	task := f.task(t, service.TaskFields{Title: "Water plants"})
	f.model.completions = []any{map[string]any{"task_id": task.ID, "reason": "quick win"}}

	got := results(t, f, call("c1", "what_now", map[string]any{}))
	if got[0].IsError || !strings.Contains(got[0].Content, "quick win") {
		t.Errorf("result = %+v", got[0])
	}
}

func TestUndoLastLeavesThisTurnAlone(t *testing.T) {
	f := newFixture(t)
	add := func(id, title string) llm.ToolCall {
		return call(id, "create_task", map[string]any{"title": title, "notes": "", "size": "", "energy": "", "feeling": "",
			"due_on": "", "not_before": ""})
	}
	results(t, f, add("c1", "From the last turn"))
	undone, unsub := f.hub.Subscribe("batch.undone")
	defer unsub()

	got := results(t, f, add("c2", "From this turn"), call("c3", "undo_last", map[string]any{}))
	if got[1].IsError {
		t.Fatal(got[1].Content)
	}

	var titles []string
	tasks, _ := f.svc.ListTasks(context.Background())
	for _, task := range tasks {
		titles = append(titles, task.Title)
	}
	if diff := cmp.Diff([]string{"From this turn"}, titles); diff != "" {
		t.Errorf("tasks (-want +got):\n%s", diff)
	}
	if len(drain(undone)) != 1 {
		t.Error("the undo wasn't published")
	}

	got = results(t, f, call("c4", "undo_last", map[string]any{}), call("c5", "undo_last", map[string]any{}))
	if got[0].IsError || !got[1].IsError {
		t.Errorf("undoing until nothing is left: %+v", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/agent/`
Expected: `TestEveryToolIsOffered` fails with only four tools. The other new tests fail with `there is no tool "update_task"` (and so on) in their results.

- [ ] **Step 3: Add the tools**

`internal/agent/tools.go`. Replace the whole file with:

```go
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	js "github.com/jameynakama/whatnow/internal/jsonschema"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

// tool is one function the model can call. run returns the chip label the user sees and the
// result the model reads; an error goes back to the model as a failed call.
type tool struct {
	name, description string
	schema            map[string]any
	run               func(ctx context.Context, t *turn, input json.RawMessage) (label string, result any, err error)
}

// args decodes a tool's input. Strict schemas make a mismatch unlikely, but not impossible.
func args[T any](input json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(input, &v); err != nil {
		return v, fmt.Errorf("%w: the input doesn't match the schema: %v", service.ErrInvalid, err)
	}
	return v, nil
}

// localTime is how the model writes times: the user's wall clock, no zone.
const localTime = "2006-01-02T15:04"

var (
	taskID   = js.Integer("The task's number, from #<id>.")
	eventID  = js.Integer("The event's number, from [event id].")
	dateProp = func(what string) map[string]any { return js.String(what + " as YYYY-MM-DD, or empty for none.") }

	// taskProps are create_task's fields. An empty string leaves a field unset.
	taskProps = []js.Field{
		js.Prop("title", js.String("Short and specific, starting with a verb when it fits.")),
		js.Prop("notes", js.String("Details worth keeping, or empty.")),
		js.Prop("size", js.Enum("S (under an hour), M (one to three hours), L (more), or empty if unclear.", "S", "M", "L", "")),
		js.Prop("energy", js.Enum("How much it takes out of them, or empty if unclear.", "low", "med", "high", "")),
		js.Prop("feeling", js.String("How they feel about it, in their words, or empty.")),
		js.Prop("due_on", dateProp("When it's due,")),
		js.Prop("not_before", dateProp("The first day it can be done,")),
	}
	changes = func(fields ...string) map[string]any {
		return js.Array(js.Object(
			js.Prop("field", js.Enum("The field to change.", fields...)),
			js.Prop("value", js.String("The new value, in the same form as when creating. Empty clears it.")),
		))
	}
)

var tools = []tool{
	{"list_tasks", "List tasks with one status, including ones the context leaves out: done, dropped, and put off until later.",
		js.Object(js.Prop("status", js.Enum("Which tasks to list.", "open", "done", "dropped"))), listTasks},
	{"create_task", "Add a task. It waits in the unplanned tray until the next replan.", js.Object(taskProps...), createTask},
	{"update_task", "Change some of a task's fields. To put a task off, use push_task instead.",
		js.Object(js.Prop("task_id", taskID),
			js.Prop("changes", changes("title", "notes", "size", "energy", "feeling", "due_on", "not_before"))), updateTask},
	{"complete_task", "Mark a task done.", js.Object(js.Prop("task_id", taskID)), statusTool(domain.StatusDone, "Done")},
	{"drop_task", "Stop carrying a task without doing it.", js.Object(js.Prop("task_id", taskID)), statusTool(domain.StatusDropped, "Dropped")},
	{"push_task", "Put a task off until a later date. Pushes are counted.",
		js.Object(js.Prop("task_id", taskID), js.Prop("until", js.String("The first day to see it again, as YYYY-MM-DD."))), pushTask},
	{"add_blocker", "Record that a task can't start until another is finished.",
		js.Object(js.Prop("task_id", js.Integer("The task that waits.")), js.Prop("blocked_by_id", js.Integer("The task it waits on."))), addBlocker},
	{"remove_blocker", "Remove a blocker.",
		js.Object(js.Prop("task_id", js.Integer("The task that waits.")), js.Prop("blocked_by_id", js.Integer("The task it waits on."))), removeBlocker},
	{"create_event", "Add something at a fixed time.",
		js.Object(js.Prop("title", js.String("What it is.")),
			js.Prop("starts_at", js.String("Local time, as YYYY-MM-DDTHH:MM.")),
			js.Prop("ends_at", js.String("Local time, as YYYY-MM-DDTHH:MM, or empty if it has no set end."))), createEvent},
	{"update_event", "Change some of an event's fields.",
		js.Object(js.Prop("event_id", eventID), js.Prop("changes", changes("title", "starts_at", "ends_at"))), updateEvent},
	{"delete_event", "Remove an event.", js.Object(js.Prop("event_id", eventID)), deleteEvent},
	{"replan", "Make a new version of today's plan. Use it when what they've told you changes what today should hold.",
		js.Object(js.Prop("reason", js.String("What changed, in a few words."))), replan},
	{"what_now", "Pick the one task they should start right now.", js.Object(), whatNow},
	{"set_pace", "Change today's pace. The plan updates on its own.",
		js.Object(js.Prop("level", js.Integer("1 running on fumes, 2 gentle, 3 steady, 4 full steam, 5 go hard.")),
			js.Prop("note", js.String("What's on their mind, if they said, or empty."))), setPace},
	{"undo_last", "Take back the most recent change you or the automatic cleanup made, as a whole. Changes from this reply are left alone.",
		js.Object(), undoLast},
}

func toolDefs() []llm.Tool {
	defs := make([]llm.Tool, len(tools))
	for i, t := range tools {
		defs[i] = llm.Tool{Name: t.name, Description: t.description, Schema: t.schema}
	}
	return defs
}

// runTool runs one call and records its chip. The model gets the result as JSON, or the error.
func (t *turn) runTool(ctx context.Context, c llm.ToolCall) llm.ToolResult {
	use := domain.ToolUse{Tool: c.Name}
	result := llm.ToolResult{CallID: c.ID}
	label, out, err := t.dispatch(ctx, c)
	if err == nil {
		var b []byte
		if b, err = json.Marshal(out); err == nil {
			use.Label, result.Content = label, string(b)
		}
	}
	if err != nil {
		use.Label, use.Error = c.Name, err.Error()
		result.Content, result.IsError = err.Error(), true
	}
	t.uses = append(t.uses, use)
	t.agent.hub.Publish(stream.Message{Type: "chat.tool", Data: use})
	return result
}

func (t *turn) dispatch(ctx context.Context, c llm.ToolCall) (string, any, error) {
	for _, tl := range tools {
		if tl.name == c.Name {
			return tl.run(ctx, t, c.Input)
		}
	}
	return "", nil, fmt.Errorf("there is no tool %q", c.Name)
}

// brief is a task as the model reads it in tool results.
type brief struct {
	ID        int64          `json:"id"`
	Title     string         `json:"title"`
	Status    domain.Status  `json:"status"`
	Notes     string         `json:"notes,omitempty"`
	Size      *domain.Size   `json:"size,omitempty"`
	Energy    *domain.Energy `json:"energy,omitempty"`
	Feeling   string         `json:"feeling,omitempty"`
	DueOn     *domain.Date   `json:"due_on,omitempty"`
	NotBefore *domain.Date   `json:"not_before,omitempty"`
}

func briefOf(t domain.Task) brief {
	return brief{ID: t.ID, Title: t.Title, Status: t.Status, Notes: t.Notes, Size: t.Size, Energy: t.Energy,
		Feeling: t.Feeling, DueOn: t.DueOn, NotBefore: t.NotBefore}
}

func name(t domain.Task) string { return fmt.Sprintf("#%d %q", t.ID, t.Title) }

func listTasks(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		Status domain.Status `json:"status"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	all, err := t.agent.svc.ListTasks(ctx)
	if err != nil {
		return "", nil, err
	}
	out := []brief{}
	for _, task := range all {
		if task.Status == in.Status {
			out = append(out, briefOf(task))
		}
	}
	return fmt.Sprintf("Looked at %s tasks", in.Status), out, nil
}

// taskArgs are create_task's input. Empty strings are unset fields.
type taskArgs struct {
	Title     string `json:"title"`
	Notes     string `json:"notes"`
	Size      string `json:"size"`
	Energy    string `json:"energy"`
	Feeling   string `json:"feeling"`
	DueOn     string `json:"due_on"`
	NotBefore string `json:"not_before"`
}

func optional[T ~string](s string) *T {
	if s == "" {
		return nil
	}
	v := T(s)
	return &v
}

func createTask(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[taskArgs](input)
	if err != nil {
		return "", nil, err
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	task, err := t.agent.svc.CreateTask(ctx, batch, service.TaskFields{
		Title: in.Title, Notes: in.Notes, Size: optional[domain.Size](in.Size), Energy: optional[domain.Energy](in.Energy),
		Feeling: in.Feeling, DueOn: optional[domain.Date](in.DueOn), NotBefore: optional[domain.Date](in.NotBefore),
	})
	if err != nil {
		return "", nil, err
	}
	return "Added " + name(task), briefOf(task), nil
}

type change struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

func updateTask(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		TaskID  int64    `json:"task_id"`
		Changes []change `json:"changes"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	svc := t.agent.svc
	task, err := svc.GetTask(ctx, in.TaskID)
	if err != nil {
		return "", nil, err
	}
	f := service.TaskFields{Title: task.Title, Notes: task.Notes, Size: task.Size, Energy: task.Energy, Feeling: task.Feeling,
		DueOn: task.DueOn, NotBefore: task.NotBefore, Refs: task.Refs}
	for _, c := range in.Changes {
		switch c.Field {
		case "title":
			f.Title = c.Value
		case "notes":
			f.Notes = c.Value
		case "size":
			f.Size = optional[domain.Size](c.Value)
		case "energy":
			f.Energy = optional[domain.Energy](c.Value)
		case "feeling":
			f.Feeling = c.Value
		case "due_on":
			f.DueOn = optional[domain.Date](c.Value)
		case "not_before":
			f.NotBefore = optional[domain.Date](c.Value)
		default:
			return "", nil, fmt.Errorf("%w: a task has no field %q", service.ErrInvalid, c.Field)
		}
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	task, err = svc.UpdateTask(ctx, batch, in.TaskID, f)
	if err != nil {
		return "", nil, err
	}
	return "Updated " + name(task), briefOf(task), nil
}

func statusTool(status domain.Status, verb string) func(context.Context, *turn, json.RawMessage) (string, any, error) {
	return func(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
		in, err := args[struct {
			TaskID int64 `json:"task_id"`
		}](input)
		if err != nil {
			return "", nil, err
		}
		batch, err := t.batch(ctx)
		if err != nil {
			return "", nil, err
		}
		task, err := t.agent.svc.SetTaskStatus(ctx, batch, in.TaskID, status)
		if err != nil {
			return "", nil, err
		}
		return verb + ": " + name(task), briefOf(task), nil
	}
}

func pushTask(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		TaskID int64       `json:"task_id"`
		Until  domain.Date `json:"until"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	if in.Until <= t.agent.today() {
		return "", nil, fmt.Errorf("%w: until must be after today, %s", service.ErrInvalid, t.agent.today())
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	task, err := t.agent.svc.PushTask(ctx, batch, in.TaskID, in.Until)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("Pushed %s to %s", name(task), in.Until), briefOf(task), nil
}

func blockerArgs(input json.RawMessage) (domain.Blocker, error) {
	in, err := args[struct {
		TaskID      int64 `json:"task_id"`
		BlockedByID int64 `json:"blocked_by_id"`
	}](input)
	return domain.Blocker{TaskID: in.TaskID, BlockedByID: in.BlockedByID}, err
}

func addBlocker(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	b, err := blockerArgs(input)
	if err != nil {
		return "", nil, err
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := t.agent.svc.AddBlocker(ctx, batch, b); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("#%d now waits on #%d", b.TaskID, b.BlockedByID), b, nil
}

func removeBlocker(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	b, err := blockerArgs(input)
	if err != nil {
		return "", nil, err
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := t.agent.svc.RemoveBlocker(ctx, batch, b); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("#%d no longer waits on #%d", b.TaskID, b.BlockedByID), b, nil
}

// parseLocal reads one of the model's local times. An empty string is no time.
func (a *Agent) parseLocal(field, s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	v, err := time.ParseInLocation(localTime, s, a.loc)
	if err != nil {
		return nil, fmt.Errorf("%w: %s %q is not YYYY-MM-DDTHH:MM", service.ErrInvalid, field, s)
	}
	return &v, nil
}

// eventBrief is an event as the model reads it, in local time.
type eventBrief struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
}

func (a *Agent) eventBrief(e domain.Event) eventBrief {
	b := eventBrief{ID: e.ID, Title: e.Title, StartsAt: e.StartsAt.In(a.loc).Format(localTime)}
	if e.EndsAt != nil {
		b.EndsAt = e.EndsAt.In(a.loc).Format(localTime)
	}
	return b
}

func createEvent(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		Title    string `json:"title"`
		StartsAt string `json:"starts_at"`
		EndsAt   string `json:"ends_at"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	a := t.agent
	f := service.EventFields{Title: in.Title}
	start, err := a.parseLocal("starts_at", in.StartsAt)
	if err != nil {
		return "", nil, err
	}
	if start != nil {
		f.StartsAt = *start
	}
	if f.EndsAt, err = a.parseLocal("ends_at", in.EndsAt); err != nil {
		return "", nil, err
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	e, err := a.svc.CreateEvent(ctx, batch, f)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("Added %q at %s", e.Title, e.StartsAt.In(a.loc).Format("Mon 15:04")), a.eventBrief(e), nil
}

func updateEvent(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		EventID int64    `json:"event_id"`
		Changes []change `json:"changes"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	a := t.agent
	e, err := a.svc.GetEvent(ctx, in.EventID)
	if err != nil {
		return "", nil, err
	}
	f := service.EventFields{Title: e.Title, StartsAt: e.StartsAt, EndsAt: e.EndsAt}
	for _, c := range in.Changes {
		switch c.Field {
		case "title":
			f.Title = c.Value
		case "starts_at":
			start, err := a.parseLocal("starts_at", c.Value)
			if err != nil {
				return "", nil, err
			}
			if start == nil {
				return "", nil, fmt.Errorf("%w: starts_at can't be cleared", service.ErrInvalid)
			}
			f.StartsAt = *start
		case "ends_at":
			if f.EndsAt, err = a.parseLocal("ends_at", c.Value); err != nil {
				return "", nil, err
			}
		default:
			return "", nil, fmt.Errorf("%w: an event has no field %q", service.ErrInvalid, c.Field)
		}
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	if e, err = a.svc.UpdateEvent(ctx, batch, in.EventID, f); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("Updated %q", e.Title), a.eventBrief(e), nil
}

func deleteEvent(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		EventID int64 `json:"event_id"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	a := t.agent
	e, err := a.svc.GetEvent(ctx, in.EventID)
	if err != nil {
		return "", nil, err
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := a.svc.DeleteEvent(ctx, batch, in.EventID); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("Removed %q", e.Title), map[string]bool{"deleted": true}, nil
}

func replan(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		Reason string `json:"reason"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	plan, err := t.agent.planner.Replan(ctx, domain.TriggerAgent, in.Reason)
	if err != nil {
		return "", nil, err
	}
	return "Replanned the day", struct {
		Opening  string             `json:"opening"`
		Entries  []domain.PlanEntry `json:"entries"`
		Fallback bool               `json:"fallback"`
	}{plan.Opening, plan.Entries, plan.Fallback}, nil
}

func whatNow(ctx context.Context, t *turn, _ json.RawMessage) (string, any, error) {
	pick, err := t.agent.planner.WhatNow(ctx, 0)
	if err != nil {
		return "", nil, err
	}
	task, err := t.agent.svc.GetTask(ctx, pick.TaskID)
	if err != nil {
		return "", nil, err
	}
	return "Picked " + name(task), pick, nil
}

func setPace(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		Level domain.Pace `json:"level"`
		Note  string      `json:"note"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	a := t.agent
	if _, err := a.svc.GetDay(ctx, a.today()); errors.Is(err, service.ErrNotFound) {
		return "", nil, planner.ErrNoDay
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	day, err := a.svc.SetPace(ctx, batch, a.today(), in.Level, strings.TrimSpace(in.Note))
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("Pace set to %q", day.Pace.Label()), day, nil
}

func undoLast(ctx context.Context, t *turn, _ json.RawMessage) (string, any, error) {
	a := t.agent
	batch, err := a.svc.LastUndoableBatch(ctx, domain.ActorAgent, t.batchID)
	if err != nil {
		return "", nil, err
	}
	acts, err := a.svc.BatchActivity(ctx, batch)
	if err != nil {
		return "", nil, err
	}
	if err := a.svc.Undo(ctx, batch); err != nil {
		return "", nil, err
	}
	a.hub.PublishUndone(batch)
	return "Undid the last change", struct {
		Undone []domain.Activity `json:"undone"`
	}{acts}, nil
}
```

- [ ] **Step 4: Run everything**

Run: `gofmt -l . && go vet ./... && go test -race ./internal/agent/ && go test ./...`
Expected: every package `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tools.go internal/agent/tools_test.go
git commit -m "Give the agent tools for edits, pushes, blockers, events, planning, pace, and undo"
```

---

### Task 6: Chat routes

`GET /api/v1/chat` returns today's messages. `POST /api/v1/chat` sends a message and waits for the reply. The turn runs on a context the client can't cancel, so leaving mid-reply never cuts off its changes or its stored reply. `ErrBusy` maps to `409`.

**Files:**
- Create: `internal/httpapi/chat.go`, `internal/httpapi/chat_test.go`
- Modify: `internal/httpapi/server.go`, `internal/httpapi/helpers_test.go`
- Modify: `cmd/whatnow/main.go`

**Interfaces:**
- Consumes: `agent.New`, `(*Agent).Send`, `(*Agent).History`, `agent.ErrBusy` (Task 4).
- Produces:
  - `httpapi.Deps.Agent *agent.Agent`
  - `GET /api/v1/chat` → `200 []domain.Message`
  - `POST /api/v1/chat {text}` → `200 domain.Message` (the reply), `400` for an empty or too-long message, `409` while busy.

- [ ] **Step 1: Write the failing tests**

`internal/httpapi/helpers_test.go`. Add `"github.com/jameynakama/whatnow/internal/agent"` to the imports. Build the planner once and give the agent to `Deps`:

```go
	plans := planner.New(svc, api.hub, active, testLoc, now)
	api.handler = httpapi.New(httpapi.Deps{
		// ...the fields before Planner are unchanged
		Planner: plans,
		Agent:   agent.New(svc, api.hub, plans, testLoc, now),
	})
```

Split `do` so a test can pass a request context:

```go
// do sends a request the way the web client will: from a loopback host, with a JSON body type on
// every write.
func (a *testAPI) do(method, path string, body any) *httptest.ResponseRecorder {
	a.t.Helper()
	return a.doContext(context.Background(), method, path, body)
}

// doContext is do with the request's context, such as one the client has already cancelled.
func (a *testAPI) doContext(ctx context.Context, method, path string, body any) *httptest.ResponseRecorder {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(ctx, method, path, rd)
	// ...the rest of the old do is unchanged
```

`internal/httpapi/chat_test.go`:

```go
package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
)

// chatModel replies to every chat with one tool call, then with its text.
type chatModel struct {
	modelFunc
	call llm.ToolCall
	text string
}

func (m *chatModel) Chat(ctx context.Context, req llm.ChatRequest, onText func(string)) (llm.Reply, error) {
	if err := ctx.Err(); err != nil {
		return llm.Reply{}, err
	}
	if last := req.Messages[len(req.Messages)-1]; len(last.ToolResults) == 0 {
		return llm.Reply{ToolCalls: []llm.ToolCall{m.call}}, nil
	}
	onText(m.text)
	return llm.Reply{Text: m.text}, nil
}

func TestChatRepliesAndKeepsHistory(t *testing.T) {
	api := newAPI(t)
	api.model = &chatModel{
		call: llm.ToolCall{ID: "c1", Name: "create_task", Input: []byte(
			`{"title":"Call the dentist","notes":"","size":"","energy":"","feeling":"","due_on":"","not_before":""}`)},
		text: "Added it.",
	}

	rec := api.do(http.MethodPost, "/api/v1/chat", map[string]string{"text": "remind me to call the dentist"})
	wantStatus(t, rec, http.StatusOK)
	reply := decodeAs[domain.Message](t, rec)
	if reply.Text != "Added it." || reply.BatchID == 0 || len(reply.Tools) != 1 {
		t.Errorf("reply = %+v", reply)
	}

	rec = api.do(http.MethodGet, "/api/v1/chat", nil)
	wantStatus(t, rec, http.StatusOK)
	if history := decodeAs[[]domain.Message](t, rec); len(history) != 2 || history[1].ID != reply.ID {
		t.Errorf("history = %+v", history)
	}

	wantStatus(t, api.do(http.MethodPost, "/api/v1/chat", map[string]string{"text": " "}), http.StatusBadRequest)
}

func TestChatWithoutAProviderSaysSo(t *testing.T) {
	api := newAPI(t)
	rec := api.do(http.MethodPost, "/api/v1/chat", map[string]string{"text": "hi"})
	wantStatus(t, rec, http.StatusOK)
	if reply := decodeAs[domain.Message](t, rec); reply.Error == "" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestChatFinishesAfterTheClientLeaves(t *testing.T) {
	api := newAPI(t)
	api.model = &chatModel{
		call: llm.ToolCall{ID: "c1", Name: "list_tasks", Input: []byte(`{"status":"open"}`)},
		text: "Nothing open.",
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := api.doContext(ctx, http.MethodPost, "/api/v1/chat", map[string]string{"text": "anything open?"})

	wantStatus(t, rec, http.StatusOK)
	if reply := decodeAs[domain.Message](t, rec); reply.Text != "Nothing open." || reply.Error != "" {
		t.Errorf("reply = %+v", reply)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/httpapi/`
Expected: build failure. `unknown field Agent in struct literal of type httpapi.Deps`.

- [ ] **Step 3: Add the routes**

`internal/httpapi/chat.go`:

```go
package httpapi

import (
	"context"
	"net/http"
)

func (s *Server) chatHistory(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.Agent.History(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

// chat sends a message and waits for the reply, which also streams over SSE as it's written.
// The turn finishes even if the client leaves, so its changes and its reply are never cut short.
func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	reply, err := s.Agent.Send(context.WithoutCancel(r.Context()), body.Text)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}
```

`internal/httpapi/server.go`. Import `internal/agent`, add the field to `Deps`, register the routes before the stream route, and map `ErrBusy`:

```go
	Planner   *planner.Planner
	Agent     *agent.Agent
}
```

```go
	mux.HandleFunc("GET /api/v1/chat", s.chatHistory)
	mux.HandleFunc("POST /api/v1/chat", s.chat)
```

```go
	case errors.Is(err, service.ErrConflict), errors.Is(err, planner.ErrNoDay), errors.Is(err, agent.ErrBusy):
```

- [ ] **Step 4: Wire the agent into `main`**

`cmd/whatnow/main.go`. Import `internal/agent` and add it to `Deps`:

```go
		Planner:   plans,
		Agent:     agent.New(svc, hub, plans, time.Local, time.Now),
	})
```

- [ ] **Step 5: Run everything**

Run: `gofmt -l . && go vet ./... && go test ./... && go build ./cmd/whatnow`
Expected: every package `ok`, and the build succeeds.

- [ ] **Step 6: Smoke test against a real provider (needs Jamey's go-ahead, since it spends on his key)**

The bash sandbox blocks localhost, so run these with the sandbox disabled. Use a throwaway database:

```bash
go run ./cmd/whatnow serve -db "$TMPDIR/whatnow-chat.db" &
curl -s -X PUT localhost:7777/api/v1/settings -H 'Content-Type: application/json' \
  -d '{"provider":"openai","openai":{"main_model":"gpt-5.5","light_model":"gpt-5.5","base_url":""},"anthropic":{"main_model":"claude-sonnet-5-5","light_model":"claude-haiku-4-5","base_url":""},"style":"","about_me":"","end_of_day":"23:30"}'
curl -sN localhost:7777/api/v1/stream &   # watch chat.delta, chat.tool, chat.message
curl -s -X POST localhost:7777/api/v1/chat -H 'Content-Type: application/json' \
  -d '{"text":"add calling the dentist, due Friday, it is quick"}'
curl -s -X POST localhost:7777/api/v1/chat -H 'Content-Type: application/json' -d '{"text":"undo that"}'
curl -s localhost:7777/api/v1/chat
```

Expected: the first reply has a `create_task` chip and a nonzero `batch_id`. The stream shows deltas before the reply's `chat.message`. The second reply undoes the first, and the task is gone from `GET /api/v1/tasks`. Stop both background processes afterward.

- [ ] **Step 7: Commit**

```bash
git add internal/httpapi cmd/whatnow/main.go
git commit -m "Add chat endpoints"
```
