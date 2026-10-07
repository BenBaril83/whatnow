package httpapi

import (
	"net/http"
	"time"
)

// Clock is the server's time. The web client keeps its clock in step with it, so a demo clock
// moves both.
type Clock struct {
	Now time.Time `json:"now"`
}

func (s *Server) clock(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Clock{Now: s.Now()})
}
