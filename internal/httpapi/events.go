package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/jameynakama/whatnow/internal/service"
)

// listEvents returns the events that start on ?date= in the user's time zone.
func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	day, err := time.ParseInLocation(time.DateOnly, r.URL.Query().Get("date"), s.loc)
	if err != nil {
		writeError(w, fmt.Errorf("%w: date must be YYYY-MM-DD", service.ErrInvalid))
		return
	}
	events, err := s.svc.ListEvents(r.Context(), day, day.AddDate(0, 0, 1))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) createEvent(w http.ResponseWriter, r *http.Request) {
	var f service.EventFields
	if err := decode(r, &f); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusCreated, func(batch int64) (any, error) {
		return s.svc.CreateEvent(r.Context(), batch, f)
	})
}

func (s *Server) updateEvent(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var f service.EventFields
	if err := decode(r, &f); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return s.svc.UpdateEvent(r.Context(), batch, id, f)
	})
}

func (s *Server) deleteEvent(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return nil, s.svc.DeleteEvent(r.Context(), batch, id)
	})
}
