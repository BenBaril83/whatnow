package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/settings"
)

type modelFunc func(llm.Request) (string, error)

func (f modelFunc) Complete(_ context.Context, req llm.Request) (string, error) { return f(req) }

func (modelFunc) Chat(context.Context, llm.ChatRequest, func(string)) (llm.Reply, error) {
	panic("modelFunc doesn't chat; use chatModel")
}

func TestSettingsRoundTripWithoutExposingKeys(t *testing.T) {
	keyring.MockInit()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	api := newAPI(t)

	view := decodeAs[httpapi.SettingsView](t, api.do(http.MethodGet, "/api/v1/settings", nil))
	if view.Settings.Provider != "" || view.Keys[settings.Anthropic] != "" || view.Keys[settings.OpenAI] != settings.KeyFromEnv {
		t.Fatalf("fresh settings: %+v", view)
	}

	const secret = "sk-ant-very-secret"
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/anthropic", map[string]string{"key": secret}), http.StatusNoContent)
	next := view.Settings
	next.Provider = settings.Anthropic
	next.AboutMe = "Code review drains me."
	rec := api.do(http.MethodPut, "/api/v1/settings", next)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatal("settings response contains the API key")
	}
	view = decodeAs[httpapi.SettingsView](t, rec)
	if view.Settings.AboutMe != next.AboutMe || view.Keys[settings.Anthropic] != settings.KeyFromKeychain {
		t.Errorf("after save: %+v", view)
	}

	wantStatus(t, api.do(http.MethodDelete, "/api/v1/settings/keys/anthropic", nil), http.StatusNoContent)
	view = decodeAs[httpapi.SettingsView](t, api.do(http.MethodGet, "/api/v1/settings", nil))
	if view.Keys[settings.Anthropic] != "" {
		t.Errorf("key still reported after delete: %+v", view.Keys)
	}
}

func TestSettingsErrors(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	tooLong := settings.Defaults()
	tooLong.Style = strings.Repeat("x", settings.MaxStyleRunes+1)

	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings", tooLong), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/gemini", map[string]string{"key": "k"}), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/openai", map[string]string{"key": " "}), http.StatusBadRequest)
}

func TestConnectionTest(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)

	got := decodeAs[httpapi.ConnectionResult](t, api.do(http.MethodPost, "/api/v1/settings/test", nil))
	if got.OK || !strings.Contains(got.Error, "no LLM provider") {
		t.Errorf("unconfigured: %+v", got)
	}

	var sentModel string
	api.model = modelFunc(func(req llm.Request) (string, error) {
		sentModel = req.Model
		return "ok", nil
	})
	got = decodeAs[httpapi.ConnectionResult](t, api.do(http.MethodPost, "/api/v1/settings/test", nil))
	if !got.OK || sentModel != "main" {
		t.Errorf("working provider: %+v, model %q", got, sentModel)
	}

	api.model = modelFunc(func(llm.Request) (string, error) { return "", errors.New("anthropic: 401 invalid x-api-key") })
	rec := api.do(http.MethodPost, "/api/v1/settings/test", nil)
	wantStatus(t, rec, http.StatusOK)
	if got := decodeAs[httpapi.ConnectionResult](t, rec); got.OK || !strings.Contains(got.Error, "401") {
		t.Errorf("failing provider: %+v", got)
	}
}
