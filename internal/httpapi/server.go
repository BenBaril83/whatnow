// Package httpapi serves the REST API under /api/v1/, the SSE stream, and the web client.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/agent"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/stream"
)

// Deps is what the API needs from the rest of the app.
type Deps struct {
	Service *service.Service
	Hub     *stream.Hub
	// DB holds the settings document.
	DB *sql.DB
	// Loc is the user's time zone, which turns a date into a day of events.
	Loc *time.Location
	// OnCapture is called with each task created from raw text, as it was captured.
	OnCapture func(task domain.Task)
	// Clean runs cleanup on a captured task right away, for the morning's brain dump.
	Clean func(ctx context.Context, captured domain.Task) (int64, error)
	// NewClient builds an LLM client from settings, for the connection test.
	NewClient func(settings.Settings) (llm.Client, settings.ProviderConfig, error)
	Planner   *planner.Planner
	Agent     *agent.Agent
	// Static serves the web client for every GET outside the API.
	Static http.Handler
}

type Server struct {
	Deps
}

// Mutation is the response to every write. It carries the batch ID so the UI can offer undo.
type Mutation struct {
	BatchID int64 `json:"batch_id"`
	Data    any   `json:"data,omitempty"`
}

func New(d Deps) http.Handler {
	s := &Server{Deps: d}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", s.listTasks)
	mux.HandleFunc("POST /api/v1/tasks", s.createTask)
	mux.HandleFunc("PUT /api/v1/tasks/{id}", s.updateTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/status", s.setTaskStatus)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}", s.deleteTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/push", s.pushTask)
	mux.HandleFunc("GET /api/v1/blockers", s.listBlockers)
	mux.HandleFunc("POST /api/v1/tasks/{id}/blockers", s.addBlocker)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}/blockers/{blockedBy}", s.removeBlocker)
	mux.HandleFunc("POST /api/v1/batches/{id}/undo", s.undo)
	mux.HandleFunc("GET /api/v1/events", s.listEvents)
	mux.HandleFunc("POST /api/v1/events", s.createEvent)
	mux.HandleFunc("PUT /api/v1/events/{id}", s.updateEvent)
	mux.HandleFunc("DELETE /api/v1/events/{id}", s.deleteEvent)
	mux.HandleFunc("GET /api/v1/today", s.today)
	mux.HandleFunc("GET /api/v1/morning", s.morning)
	mux.HandleFunc("POST /api/v1/today/start", s.startDay)
	mux.HandleFunc("GET /api/v1/today/wrap", s.wrapReview)
	mux.HandleFunc("POST /api/v1/today/wrap", s.wrapUp)
	mux.HandleFunc("PUT /api/v1/today/pace", s.setPace)
	mux.HandleFunc("POST /api/v1/today/replan", s.replan)
	mux.HandleFunc("POST /api/v1/whatnow", s.whatNow)
	mux.HandleFunc("GET /api/v1/chat", s.chatHistory)
	mux.HandleFunc("POST /api/v1/chat", s.chat)
	mux.HandleFunc("GET /api/v1/stream", s.stream)
	mux.HandleFunc("GET /api/v1/settings", s.getSettings)
	mux.HandleFunc("PUT /api/v1/settings", s.putSettings)
	mux.HandleFunc("PUT /api/v1/settings/keys/{provider}", s.putKey)
	mux.HandleFunc("DELETE /api/v1/settings/keys/{provider}", s.deleteKey)
	mux.HandleFunc("POST /api/v1/settings/test", s.testConnection)
	mux.HandleFunc("DELETE /api/v1/data", s.deleteData)
	mux.Handle("GET /", d.Static)
	return localOnly(mux)
}

// localOnly rejects requests another site could make through the user's browser. A Host that
// isn't loopback means DNS rebinding. A write that isn't JSON means a cross-site form post, which
// skips the CORS preflight a JSON request triggers and this server never answers.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.Host) {
			writeJSON(w, http.StatusForbidden, errorBody{"requests must come from this machine"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				writeJSON(w, http.StatusUnsupportedMediaType, errorBody{"writes must be application/json"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// write runs op in a new user batch, publishes the batch, and responds with its ID.
func (s *Server) write(w http.ResponseWriter, r *http.Request, status int, op func(batch int64) (any, error)) {
	ctx := r.Context()
	batch, err := s.Service.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := op(batch)
	if err != nil {
		writeError(w, err)
		return
	}
	s.publishBatch(ctx, batch)
	writeJSON(w, status, Mutation{BatchID: batch, Data: data})
}

func (s *Server) publishBatch(ctx context.Context, batch int64) {
	acts, err := s.Service.BatchActivity(ctx, batch)
	if err != nil {
		log.Printf("publish batch %d: %v", batch, err)
		return
	}
	s.Hub.PublishBatch(batch, stream.SourceUser, acts)
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalid), errors.Is(err, settings.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, errorBody{err.Error()})
	case errors.Is(err, service.ErrNotFound), errors.Is(err, planner.ErrNothingToDo):
		writeJSON(w, http.StatusNotFound, errorBody{err.Error()})
	case errors.Is(err, service.ErrConflict), errors.Is(err, planner.ErrNoDay), errors.Is(err, agent.ErrBusy):
		writeJSON(w, http.StatusConflict, errorBody{err.Error()})
	default:
		log.Printf("internal error: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{"internal error"})
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", service.ErrInvalid, err)
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", service.ErrInvalid, name)
	}
	return id, nil
}
