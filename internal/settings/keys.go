package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
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
		// An unavailable keychain (no Secret Service, a denied prompt) shouldn't hide an env key.
		log.Printf("read %s key from keychain: %v", p, err)
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
