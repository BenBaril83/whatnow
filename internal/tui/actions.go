package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jameynakama/whatnow/internal/agent"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

func (m Model) load() tea.Cmd {
	return func() tea.Msg {
		s, err := load(m.ctx, m.app)
		if err != nil {
			return loadFailedMsg{err}
		}
		return snapshotMsg{s}
	}
}

// listen hands the next hub message to the update loop, which listens again.
func (m Model) listen() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-m.hub
		if !ok {
			return nil
		}
		return hubMsg(msg)
	}
}

// tick reloads on a timer, which catches changes made in another process and moves the now line.
func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

// commit runs op in a new user batch and announces the batch, as the API's writes do.
func (m Model) commit(op func(ctx context.Context, batch int64) error) (int64, error) {
	batch, err := m.app.Service.NewBatch(m.ctx, domain.ActorUser)
	if err != nil {
		return 0, err
	}
	if err := op(m.ctx, batch); err != nil {
		return 0, err
	}
	acts, err := m.app.Service.BatchActivity(m.ctx, batch)
	if err != nil {
		return 0, err
	}
	m.app.Hub.PublishBatch(batch, stream.SourceUser, acts)
	return batch, nil
}

// write commits op and toasts what it did, or why it failed.
func (m Model) write(toast string, op func(ctx context.Context, batch int64) error) tea.Cmd {
	return func() tea.Msg {
		batch, err := m.commit(op)
		if err != nil {
			return failedMsg{err}
		}
		return wroteMsg{batch: batch, toast: toast}
	}
}

// act runs one of the list's task keys on t.
func (m Model) act(key string, t domain.Task) tea.Cmd {
	svc := m.app.Service
	switch key {
	case "p":
		tomorrow := domain.Date(m.app.Now().In(m.app.Loc).AddDate(0, 0, 1).Format(time.DateOnly))
		return m.write("Pushed to tomorrow: "+t.Title, func(ctx context.Context, batch int64) error {
			_, err := svc.PushTask(ctx, batch, t.ID, tomorrow)
			return err
		})
	case "x":
		return m.write("Dropped: "+t.Title, func(ctx context.Context, batch int64) error {
			_, err := svc.SetTaskStatus(ctx, batch, t.ID, domain.StatusDropped)
			return err
		})
	}
	return m.write("Done: "+t.Title, func(ctx context.Context, batch int64) error {
		_, err := svc.SetTaskStatus(ctx, batch, t.ID, domain.StatusDone)
		return err
	})
}

// shiftPace moves today's pace by one step. The replan it causes arrives over the hub.
func (m Model) shiftPace(by int) tea.Cmd {
	pace := m.snap.today.Day.Pace + domain.Pace(by)
	if !pace.Valid() {
		return func() tea.Msg { return failedMsg{fmt.Errorf("pace is already %s", m.snap.today.Day.Pace.Label())} }
	}
	return m.write("Pace: "+pace.Label(), func(ctx context.Context, batch int64) error {
		_, err := m.app.Service.SetPace(ctx, batch, m.app.Planner.Date(), pace, "")
		return err
	})
}

// capture saves a line as a raw card and queues it for cleanup, like the web's Capture.
func (m Model) capture(text string) tea.Cmd {
	return m.write("Added: "+text, func(ctx context.Context, batch int64) error {
		task, err := m.app.Service.CreateTask(ctx, batch, service.TaskFields{RawText: text})
		if err == nil {
			m.app.Cleanup.Enqueue(task)
		}
		return err
	})
}

func (m Model) undo(batch int64) tea.Cmd {
	return func() tea.Msg {
		if err := m.app.Service.Undo(m.ctx, batch); err != nil {
			return failedMsg{err}
		}
		m.app.Hub.PublishUndone(batch)
		return undoneMsg{}
	}
}

// friendly says what went wrong in plain words, for the errors a user can expect.
func friendly(err error) string {
	switch {
	case errors.Is(err, planner.ErrNothingToDo):
		return "Nothing is free to start right now."
	case errors.Is(err, agent.ErrBusy):
		return "A reply is still being written."
	}
	return err.Error()
}

// whatNow asks for one task to start now. Passing on a task leaves it out of this answer, and a
// reason is added to its feeling note first, as the API's "not this one" does.
func (m Model) whatNow(exclude int64, reason string) tea.Cmd {
	return func() tea.Msg {
		if exclude != 0 && reason != "" {
			if _, err := m.commit(func(ctx context.Context, batch int64) error {
				_, err := m.app.Service.AppendFeeling(ctx, batch, exclude, reason)
				return err
			}); err != nil {
				return failedMsg{err}
			}
		}
		pick, err := m.app.Planner.WhatNow(m.ctx, exclude)
		if err != nil {
			return failedMsg{err}
		}
		return pickedMsg{pick}
	}
}

// send sends a chat message. Its reply streams in over the hub while the turn runs.
func (m Model) send(text string) tea.Cmd {
	return func() tea.Msg {
		if _, err := m.app.Agent.Send(m.ctx, text); err != nil {
			return failedMsg{err}
		}
		return chatSentMsg{}
	}
}
