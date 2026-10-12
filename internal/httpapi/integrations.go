package httpapi

import (
	"net/http"

	"github.com/jameynakama/whatnow/internal/integrations"
)

func (s *Server) listIntegrations(w http.ResponseWriter, r *http.Request) {
	views, err := s.Integrations.Views(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) saveIntegration(w http.ResponseWriter, r *http.Request) {
	var u integrations.Update
	if err := decode(r, &u); err != nil {
		writeError(w, err)
		return
	}
	v, err := s.Integrations.Save(r.Context(), r.PathValue("id"), u)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) enableIntegration(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	v, err := s.Integrations.SetEnabled(r.Context(), r.PathValue("id"), body.Enabled)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// pullIntegration brings in today's events, or offers tickets. A source that failed is part of a
// normal result, so it comes back as 200 with that source's error.
func (s *Server) pullIntegration(w http.ResponseWriter, r *http.Request) {
	res, err := s.Integrations.Pull(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// addTickets makes tasks of the tickets the user picked from a pull. The client sends back what
// the pull returned, so Jira isn't asked twice.
func (s *Server) addTickets(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tickets []integrations.Ticket `json:"tickets"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	batch, tasks, err := s.Integrations.Add(r.Context(), r.PathValue("id"), body.Tickets)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Mutation{BatchID: batch, Data: tasks})
}

func (s *Server) skipIntegration(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key     string `json:"key"`
		Title   string `json:"title"`
		EventID int64  `json:"event_id"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	batch, err := s.Integrations.Skip(r.Context(), r.PathValue("id"), integrations.Skip{Key: body.Key, Title: body.Title}, body.EventID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Mutation{BatchID: batch})
}

func (s *Server) unskipIntegration(w http.ResponseWriter, r *http.Request) {
	v, err := s.Integrations.Unskip(r.Context(), r.PathValue("id"), r.PathValue("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
