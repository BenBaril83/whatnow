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
