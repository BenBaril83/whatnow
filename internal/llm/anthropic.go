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
