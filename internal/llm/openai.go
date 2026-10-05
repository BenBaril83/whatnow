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
