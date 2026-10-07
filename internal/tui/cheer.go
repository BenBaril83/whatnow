package tui

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/stream"
)

// cheer is how big a celebration a batch earns. The finale is for the last open task in the plan.
type cheer int

const (
	cheerNone cheer = iota
	cheerSmall
	cheerMust
	cheerFinale
)

const (
	cheerFrames = 6
	cheerEvery  = 250 * time.Millisecond
)

type cheerTickMsg struct{ seq int }

// cheerFor is the cheer for the tasks a batch finished, from any client. Morning triage doesn't
// count: marking yesterday's leftovers done isn't today's win.
func cheerFor(b stream.BatchApplied, s *snapshot) cheer {
	if b.Source == stream.SourceTriage {
		return cheerNone
	}
	done := map[int64]bool{}
	for _, a := range b.Activity {
		if a.Entity == "task" && a.Action == "status" && status(a.After) == domain.StatusDone && status(a.Before) != domain.StatusDone {
			done[a.EntityID] = true
		}
	}
	if len(done) == 0 {
		return cheerNone
	}
	if s == nil || s.today.Plan == nil {
		return cheerSmall
	}
	left, must := 0, false
	for _, e := range s.today.Plan.Entries {
		if !s.open(e.TaskID) {
			continue
		}
		if done[e.TaskID] {
			must = must || e.Tier == domain.TierMust
		} else {
			left++
		}
	}
	switch {
	case left == 0 && s.planOpen():
		return cheerFinale
	case must:
		return cheerMust
	}
	return cheerSmall
}

// planOpen says the plan still has an open task.
func (s *snapshot) planOpen() bool {
	for _, e := range s.today.Plan.Entries {
		if s.open(e.TaskID) {
			return true
		}
	}
	return false
}

func status(raw json.RawMessage) domain.Status {
	var t struct{ Status domain.Status }
	_ = json.Unmarshal(raw, &t) // a create or delete has no status on one side
	return t.Status
}

// startCheer shows c, replacing any cheer still showing.
func (m *Model) startCheer(c cheer) tea.Cmd {
	if c == cheerNone {
		return nil
	}
	m.cheerSeq++
	m.cheer, m.cheerFrame = c, 0
	return m.cheerTick()
}

func (m Model) cheerTick() tea.Cmd {
	seq := m.cheerSeq
	return tea.Tick(cheerEvery, func(time.Time) tea.Msg { return cheerTickMsg{seq} })
}

func (m Model) onCheerTick(msg cheerTickMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.cheerSeq || m.cheer == cheerNone {
		return m, nil
	}
	m.cheerFrame++
	if m.cheerFrame >= cheerFrames {
		m.cheer = cheerNone
		return m, nil
	}
	return m, m.cheerTick()
}

// cheerChip is the header's celebration, twinkling from frame to frame.
func (m Model) cheerChip() string {
	spark := "✦"
	if m.cheerFrame%2 == 1 {
		spark = "✧"
	}
	switch m.cheer {
	case cheerSmall:
		return chip(spark+" nice", chipFill)
	case cheerMust:
		return chip(spark+" must done "+spark, tierFill[domain.TierMust])
	case cheerFinale:
		return chip(spark+" plan cleared "+spark, chipFill)
	}
	return ""
}

var sparkColors = []lipgloss.Color{primary, nowRed, chipFill, tierFill[domain.TierShould], eventFill}

// fireworks is a row of scattered sparks w columns wide, for the finale.
func fireworks(w int) string {
	var b strings.Builder
	for range w {
		if rand.IntN(4) != 0 {
			b.WriteByte(' ')
			continue
		}
		spark := []string{"✦", "✧", "*", "·", "+"}[rand.IntN(5)]
		b.WriteString(lipgloss.NewStyle().Foreground(sparkColors[rand.IntN(len(sparkColors))]).Render(spark))
	}
	return b.String()
}
