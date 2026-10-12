package tui

import (
	"context"
	"log"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jameynakama/whatnow/internal/integrations"
)

// calendar is the morning's step that pulls today's events from the event integrations that are on.
type calendar struct {
	on      []integrations.View
	pulling bool
	pulled  []pulledFrom // nil until the first pull
	at      int          // the event under the cursor, counted across every source
	asking  bool         // x on a recurring event is asking whether to skip today or the series
}

type pulledFrom struct {
	integration string
	result      integrations.PullResult
}

// calEvent is a pulled event and the integration it came from.
type calEvent struct {
	integration string
	event       integrations.PulledEvent
}

type (
	integrationsMsg struct{ on []integrations.View }
	pulledMsg       struct{ pulled []pulledFrom }
	pullFailedMsg   struct{ err error }
	removedMsg      struct {
		eventID int64
		toast   string
	}
)

// events lists every pulled event in the order shown.
func (c calendar) events() []calEvent {
	var out []calEvent
	for _, p := range c.pulled {
		for _, s := range p.result.Sources {
			for _, e := range s.Events {
				out = append(out, calEvent{p.integration, e})
			}
		}
	}
	return out
}

// loadIntegrations finds the integrations that can pull. A failure only costs the step, so it's
// logged and the ritual goes on without it.
func (m Model) loadIntegrations() tea.Cmd {
	return func() tea.Msg {
		views, err := m.app.Integrations.Views(m.ctx)
		if err != nil {
			log.Printf("load integrations: %v", err)
		}
		return integrationsMsg{slices.DeleteFunc(views, func(v integrations.View) bool { return !v.On() })}
	}
}

func (m Model) pullAll() tea.Cmd {
	on := m.morning.calendar.on
	return func() tea.Msg {
		var pulled []pulledFrom
		for _, v := range on {
			res, err := m.app.Integrations.Pull(m.ctx, v.ID)
			if err != nil {
				return pullFailedMsg{err}
			}
			pulled = append(pulled, pulledFrom{v.ID, res})
		}
		return pulledMsg{pulled}
	}
}

// removePulled deletes today's occurrence of e, and with skip, keeps its series out for good.
func (m Model) removePulled(e calEvent, skip bool) tea.Cmd {
	return func() tea.Msg {
		var err error
		toast := "Removed: " + e.event.Title
		if skip {
			toast = "Skipping " + e.event.Title + " from now on"
			_, err = m.app.Integrations.Skip(m.ctx, e.integration, integrations.Skip{Key: e.event.Series, Title: e.event.Title}, e.event.ID)
		} else {
			_, err = m.commit(func(ctx context.Context, batch int64) error {
				return m.app.Service.DeleteEvent(ctx, batch, e.event.ID)
			})
		}
		if err != nil {
			return failedMsg{err}
		}
		return removedMsg{eventID: e.event.ID, toast: toast}
	}
}

func (m Model) onCalendarMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	c := &m.morning.calendar
	switch msg := msg.(type) {
	case integrationsMsg:
		c.on, m.morning.tickets.on = nil, nil
		for _, v := range msg.on {
			if v.Pulls == integrations.PullsTickets {
				m.morning.tickets.on = append(m.morning.tickets.on, v)
			} else {
				c.on = append(c.on, v)
			}
		}
	case pulledMsg:
		c.pulling, c.pulled, c.at, c.asking = false, msg.pulled, 0, false
	case pullFailedMsg:
		c.pulling = false
		return m.show(friendly(msg.err), true)
	case removedMsg:
		for i := range c.pulled {
			for j := range c.pulled[i].result.Sources {
				s := &c.pulled[i].result.Sources[j]
				s.Events = slices.DeleteFunc(s.Events, func(e integrations.PulledEvent) bool { return e.ID == msg.eventID })
			}
		}
		c.at = max(min(c.at, len(c.events())-1), 0)
		return m.show(msg.toast, false)
	}
	return m, nil
}

// afterDump goes to the calendar step when an event integration can pull, or on past it.
func (m Model) afterDump() (tea.Model, tea.Cmd) {
	if len(m.morning.calendar.on) == 0 {
		return m.afterCalendar()
	}
	m.morning.dump.Blur()
	m.morning.step = stepCalendar
	return m, nil
}

func (m Model) onCalendarKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := &m.morning.calendar
	if c.pulling {
		return m, nil
	}
	if c.pulled == nil {
		switch k.String() {
		case "enter":
			c.pulling = true
			return m, tea.Batch(m.pullAll(), m.spin.Tick)
		case "tab":
			return m.afterCalendar()
		}
		return m, nil
	}
	events := c.events()
	if c.asking {
		c.asking = false
		switch k.String() {
		case "t":
			return m, m.removePulled(events[c.at], false)
		case "a":
			return m, m.removePulled(events[c.at], true)
		}
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		c.at = max(c.at-1, 0)
	case "down", "j":
		c.at = max(min(c.at+1, len(events)-1), 0)
	case "x":
		if len(events) == 0 {
			break
		}
		if events[c.at].event.Series != "" {
			c.asking = true
			break
		}
		return m, m.removePulled(events[c.at], false)
	case "r":
		c.pulling = true
		return m, tea.Batch(m.pullAll(), m.spin.Tick)
	case "enter", "tab":
		return m.afterCalendar()
	}
	return m, nil
}

// afterCalendar goes to the Jira step when a ticket integration can pull, or on to pace.
func (m Model) afterCalendar() (tea.Model, tea.Cmd) {
	if len(m.morning.tickets.on) == 0 {
		return m.toPace()
	}
	m.morning.dump.Blur()
	m.morning.step = stepJira
	return m, nil
}

// calendarLines is the calendar step in at most room lines: the offer to pull, then each source's
// events or error, scrolled so the cursor's event is in view.
func (m Model) calendarLines(w, room int) (lines []string, hint string) {
	c := m.morning.calendar
	if c.pulling {
		return []string{m.spin.View() + softStyle.Render(" Pulling today's calendar…")}, ""
	}
	if c.pulled == nil {
		return []string{"Pull today's calendar?"}, "enter pull · tab skip"
	}
	var body []string
	i, cursor := 0, 0
	for _, p := range c.pulled {
		for _, s := range p.result.Sources {
			if s.Error != "" {
				body = append(body, nowStyle.Render(s.Label+": "+s.Error))
				continue
			}
			body = append(body, lipgloss.NewStyle().Bold(true).Render(s.Label+" · "+count(len(s.Events), "event", "events")))
			for _, e := range s.Events {
				if i == c.at {
					cursor = len(body)
				}
				body = append(body, eventLine(e, i == c.at, m.app.Loc, w))
				if i == c.at && c.asking {
					body = append(body, cursorStyle.Render("    t today · a always · esc cancel"))
				}
				i++
			}
		}
	}
	lines = append([]string{"Today's calendar", ""}, scrolled(body, cursor, room-2)...)
	return lines, "↑↓ move · x remove · r pull again · enter next"
}

func eventLine(e integrations.PulledEvent, current bool, loc *time.Location, w int) string {
	mark, title := "  ", e.Title
	if current {
		mark, title = cursorStyle.Render("▸ "), lipgloss.NewStyle().Bold(true).Render(title)
	}
	at := e.StartsAt.In(loc).Format("15:04")
	room := max(w-len(at)-3, 1)
	return mark + at + " " + lipgloss.NewStyle().MaxWidth(room).Render(title)
}
