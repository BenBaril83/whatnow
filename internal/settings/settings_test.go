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

func TestLoadFillsFieldsAddedSinceTheSave(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Saved before nudges, the alert lead time, and onboarding existed.
	_, err = db.Exec(`INSERT INTO settings (id, body) VALUES (1, '{"provider":"anthropic","end_of_day":"18:00"}')`)
	if err != nil {
		t.Fatal(err)
	}

	got, err := settings.Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	want := settings.Defaults()
	want.Provider = settings.Anthropic
	want.EndOfDay = "18:00"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("old settings didn't pick up the new defaults (-want +got):\n%s", diff)
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
		"end of day not HH:MM":          func(s *settings.Settings) { s.EndOfDay = "5pm" },
		"no alert lead time":            func(s *settings.Settings) { s.AlertLeadMinutes = 0 },
		"alert lead over an hour":       func(s *settings.Settings) { s.AlertLeadMinutes = 61 },
		"unknown theme":                 func(s *settings.Settings) { s.Theme = "geocities" },
		"allowed host with a port":      func(s *settings.Settings) { s.AllowedHosts = []string{"mac.example.ts.net:443"} },
		"allowed host with a scheme":    func(s *settings.Settings) { s.AllowedHosts = []string{"https://mac.example.ts.net"} },
		"blank allowed host":            func(s *settings.Settings) { s.AllowedHosts = []string{" "} },
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
	atLimit.AlertLeadMinutes = 60
	atLimit.Theme = settings.ThemeVaporwave
	atLimit.AllowedHosts = []string{"mac.example.ts.net", "100.64.0.1"}
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

func TestKeychainFailureFallsBackToEnv(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	t.Setenv("ANTHROPIC_API_KEY", "from-env")

	key, source, err := settings.Key(settings.Anthropic)
	if err != nil || key != "from-env" || source != settings.KeyFromEnv {
		t.Errorf("got %q %q %v, want the env key", key, source, err)
	}
}

func TestSettingsSavedBeforeEndOfDayExistedGetTheDefault(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO settings (id, body) VALUES (1, '{"provider":"anthropic","style":"terse"}')`); err != nil {
		t.Fatal(err)
	}

	got, err := settings.Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if got.EndOfDay != "17:30" || got.Style != "terse" {
		t.Errorf("loaded %+v", got)
	}
}
