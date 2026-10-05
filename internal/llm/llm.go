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
	// Fallback asks the provider to re-serve a refused request on another model. Only the Claude
	// API supports it, on its main-tier models; other providers ignore it.
	Fallback bool
}
