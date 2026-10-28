//go:build e2e

package main

import (
	"context"
	"os"

	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/llm/fake"
	"github.com/jameynakama/whatnow/internal/settings"
)

// The e2e build never reaches a real model or the real keychain. Keys live in memory, and the
// environment's keys are dropped so every run starts from the same place.
func init() {
	keyring.MockInit()
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	linearURL = os.Getenv("WHATNOW_E2E_LINEAR_URL")
	newClient = func(ctx context.Context, c settings.Clients, s settings.Settings) (llm.Client, settings.ProviderConfig, error) {
		_, cfg, err := settings.NewClient(ctx, c, s)
		if err != nil {
			return nil, cfg, err
		}
		return fake.Client{}, cfg, nil
	}
}
