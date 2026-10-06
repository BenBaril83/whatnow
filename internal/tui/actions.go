package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
