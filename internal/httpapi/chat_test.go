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
