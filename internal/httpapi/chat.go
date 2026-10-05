package httpapi

import (
	"context"
	"net/http"
)

func (s *Server) chatHistory(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.Agent.History(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

// chat sends a message and waits for the reply, which also streams over SSE as it's written.
// The turn finishes even if the client leaves, so its changes and its reply are never cut short.
func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	reply, err := s.Agent.Send(context.WithoutCancel(r.Context()), body.Text)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}
