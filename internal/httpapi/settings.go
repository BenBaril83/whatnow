package httpapi

import (
	"net/http"

	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/settings"
)

// SettingsView is what the settings screen gets: the settings, and where each provider's key
// comes from. Keys themselves never leave the server.
type SettingsView struct {
	Settings settings.Settings                        `json:"settings"`
	Keys     map[settings.Provider]settings.KeySource `json:"keys"`
}

func (s *Server) settingsView(w http.ResponseWriter, r *http.Request) {
	saved, err := settings.Load(r.Context(), s.DB)
	if err != nil {
		writeError(w, err)
		return
	}
	view := SettingsView{Settings: saved, Keys: map[settings.Provider]settings.KeySource{}}
	for _, p := range []settings.Provider{settings.Anthropic, settings.OpenAI} {
		_, source, err := settings.Key(p)
		if err != nil {
			writeError(w, err)
			return
		}
		view.Keys[p] = source
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	s.settingsView(w, r)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var body settings.Settings
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := settings.Save(r.Context(), s.DB, body); err != nil {
		writeError(w, err)
		return
	}
	s.settingsView(w, r)
}

func (s *Server) putKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := settings.SetKey(settings.Provider(r.PathValue("provider")), body.Key); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	if err := settings.DeleteKey(settings.Provider(r.PathValue("provider"))); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ConnectionResult reports whether the saved provider answered a tiny request.
type ConnectionResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// testConnection sends one tiny request to the main model. A failure is a normal result for the
// settings screen to show, so it comes back as 200 with ok false.
func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	saved, err := settings.Load(r.Context(), s.DB)
	if err != nil {
		writeError(w, err)
		return
	}
	client, cfg, err := s.NewClient(saved)
	if err == nil {
		_, err = client.Complete(r.Context(), llm.Request{
			Model:     cfg.MainModel,
			User:      "Reply with the single word: ok",
			MaxTokens: 1024, // room for models that think before answering
		})
	}
	if err != nil {
		writeJSON(w, http.StatusOK, ConnectionResult{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ConnectionResult{OK: true})
}
