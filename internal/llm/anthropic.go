package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicClient struct {
	client anthropic.Client
}

// NewAnthropic returns a Client for the Claude API. An empty baseURL uses the default endpoint, and a nil hc the SDK's HTTP client.
func NewAnthropic(apiKey, baseURL string, hc *http.Client) Client {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if hc != nil {
		opts = append(opts, option.WithHTTPClient(hc))
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
