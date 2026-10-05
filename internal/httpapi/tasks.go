package httpapi

import (
	"net/http"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.Service.ListTasks(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var f service.TaskFields
	if err := decode(r, &f); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusCreated, func(batch int64) (any, error) {
		task, err := s.Service.CreateTask(r.Context(), batch, f)
		if err == nil && task.RawText != "" {
			s.OnCapture(task.ID)
		}
		return task, err
	})
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var f service.TaskFields
	if err := decode(r, &f); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return s.Service.UpdateTask(r.Context(), batch, id, f)
	})
}

func (s *Server) setTaskStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		Status domain.Status `json:"status"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return s.Service.SetTaskStatus(r.Context(), batch, id, body.Status)
	})
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return nil, s.Service.DeleteTask(r.Context(), batch, id)
	})
}

func (s *Server) listBlockers(w http.ResponseWriter, r *http.Request) {
	blockers, err := s.Service.ListBlockers(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, blockers)
}

func (s *Server) addBlocker(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		BlockedByID int64 `json:"blocked_by_id"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return nil, s.Service.AddBlocker(r.Context(), batch, domain.Blocker{TaskID: id, BlockedByID: body.BlockedByID})
	})
}

func (s *Server) removeBlocker(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	blockedBy, err := pathID(r, "blockedBy")
	if err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return nil, s.Service.RemoveBlocker(r.Context(), batch, domain.Blocker{TaskID: id, BlockedByID: blockedBy})
	})
}

func (s *Server) undo(w http.ResponseWriter, r *http.Request) {
	batch, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.Service.Undo(r.Context(), batch); err != nil {
		writeError(w, err)
		return
	}
	s.Hub.Publish(stream.Message{Type: "batch.undone", Data: map[string]int64{"batch_id": batch}})
	w.WriteHeader(http.StatusNoContent)
}
