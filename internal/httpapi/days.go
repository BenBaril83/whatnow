package httpapi

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
)

func (s *Server) today(w http.ResponseWriter, r *http.Request) {
	today, err := s.Planner.Today(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, today)
}

// Started is the response to starting the day.
type Started struct {
	Day  domain.Day  `json:"day"`
	Plan domain.Plan `json:"plan"`
}

// Morning is what the good-morning flow reconciles. Review is nil when the last day was wrapped
// up, or there is no last day.
type Morning struct {
	Review *planner.Review `json:"review"`
}

func (s *Server) morning(w http.ResponseWriter, r *http.Request) {
	review, err := s.Planner.MorningReview(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Morning{Review: review})
}

func (s *Server) wrapReview(w http.ResponseWriter, r *http.Request) {
	review, err := s.Planner.WrapReview(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, review)
}

// wrapUp closes today: the triage as one undoable batch, the note for tomorrow, and the summary.
// The batch ID is 0 when the triage changed nothing.
func (s *Server) wrapUp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Triage []planner.Triage `json:"triage"`
		Note   string           `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	batch, day, err := s.Planner.WrapUp(r.Context(), body.Triage, body.Note)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Mutation{BatchID: batch, Data: day})
}

// startDay starts today and waits for its first plan, so the morning flow can reveal it.
func (s *Server) startDay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pace     domain.Pace `json:"pace"`
		MoodNote string      `json:"mood_note"`
		// Triage settles the last day's unfinished tasks if it was never wrapped up.
		Triage []planner.Triage `json:"triage"`
		// Dump is the morning's brain dump, captured and cleaned up before the plan is made.
		Dump string `json:"dump"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(body.Dump) != "" {
		if err := s.captureDump(r.Context(), body.Dump); err != nil {
			writeError(w, err)
			return
		}
	}
	day, plan, err := s.Planner.StartDay(r.Context(), body.Pace, body.MoodNote, body.Triage)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, Started{Day: day, Plan: plan})
}

// replan makes a fresh plan on request, so tasks waiting in the unplanned tray get slotted in.
func (s *Server) replan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.Planner.Replan(r.Context(), domain.TriggerUser, "")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// moveTask puts a task in a tier of today's plan, or out of it when tier is null. The plan
// version it saves is in no batch, so there is nothing to undo.
func (s *Server) moveTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		Tier domain.Tier `json:"tier"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	plan, err := s.Planner.Move(r.Context(), id, body.Tier)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Mutation{Data: plan})
}

// captureDump saves a brain dump as one captured card and cleans it up right away, so the tasks
// in it land in the plan rather than the tray. If cleanup fails, the raw card stays and goes to the
// background runner like any other capture.
func (s *Server) captureDump(ctx context.Context, text string) error {
	batch, err := s.Service.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		return err
	}
	task, err := s.Service.CreateTask(ctx, batch, service.TaskFields{RawText: text})
	if err != nil {
		return err
	}
	s.publishBatch(ctx, batch)
	if _, err := s.Clean(ctx, task); err != nil {
		if !errors.Is(err, settings.ErrNotConfigured) {
			log.Printf("clean morning dump: %v", err)
		}
		s.OnCapture(task)
	}
	return nil
}

// setPace changes today's pace. The replan it causes arrives later as plan.updated.
func (s *Server) setPace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pace domain.Pace `json:"pace"`
		Note string      `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return s.Service.SetPace(r.Context(), batch, s.Planner.Date(), body.Pace, body.Note)
	})
}

// whatNow answers "what now?". With exclude_task_id it answers "not this one": that task is left
// out of this answer, and the optional reason is added to its feeling note first.
func (s *Server) whatNow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExcludeTaskID int64  `json:"exclude_task_id"`
		Reason        string `json:"reason"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	ctx := r.Context()
	if body.ExcludeTaskID != 0 && body.Reason != "" {
		batch, err := s.Service.NewBatch(ctx, domain.ActorUser)
		if err != nil {
			writeError(w, err)
			return
		}
		if _, err := s.Service.AppendFeeling(ctx, batch, body.ExcludeTaskID, body.Reason); err != nil {
			writeError(w, err)
			return
		}
		s.publishBatch(ctx, batch)
	}
	pick, err := s.Planner.WhatNow(ctx, body.ExcludeTaskID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pick)
}

func (s *Server) pushTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		Until domain.Date `json:"until"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return s.Service.PushTask(r.Context(), batch, id, body.Until)
	})
}
