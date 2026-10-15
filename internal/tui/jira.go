package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jameynakama/whatnow/internal/integrations"
)

// tickets is the morning's step that offers tickets from the ticket integrations that are on, to
// pick from.
type tickets struct {
	on      []integrations.View
	pulling bool
	pulled  []pulledFrom    // nil until the first pull
	at      int             // the ticket under the cursor, counted across every source
	ticked  map[string]bool // by ticket key
}

// pickTicket is a pulled ticket and the integration it came from.
type pickTicket struct {
	integration string
	ticket      integrations.Ticket
}

type (
	ticketsPulledMsg struct{ pulled []pulledFrom }
	ticketsFailedMsg struct{ err error }
	ticketSkippedMsg struct{ key string }
	ticketsAddedMsg  struct{ n int }
)

// list is every offered ticket in the order shown.
func (t tickets) list() []pickTicket {
	var out []pickTicket
	for _, p := range t.pulled {
		for _, s := range p.result.Sources {
			for _, tk := range s.Tickets {
				out = append(out, pickTicket{p.integration, tk})
			}
		}
	}
	return out
}

func (m Model) pullTickets() tea.Cmd {
	on := m.morning.tickets.on
	return func() tea.Msg {
		var pulled []pulledFrom
		for _, v := range on {
			res, err := m.app.Integrations.Pull(m.ctx, v.ID)
			if err != nil {
				return ticketsFailedMsg{err}
			}
			pulled = append(pulled, pulledFrom{v.ID, res})
		}
		return ticketsPulledMsg{pulled}
	}
}

// addTicked makes tasks of the ticked tickets, one batch per integration.
func (m Model) addTicked() tea.Cmd {
	byIntegration := map[string][]integrations.Ticket{}
	var order []string
	for _, p := range m.morning.tickets.list() {
		if !m.morning.tickets.ticked[p.ticket.Key] {
			continue
		}
		if _, ok := byIntegration[p.integration]; !ok {
			order = append(order, p.integration)
		}
		byIntegration[p.integration] = append(byIntegration[p.integration], p.ticket)
	}
	return func() tea.Msg {
		n := 0
		for _, id := range order {
			_, tasks, err := m.app.Integrations.Add(m.ctx, id, byIntegration[id])
			if err != nil {
				return failedMsg{err}
			}
			n += len(tasks)
		}
		return ticketsAddedMsg{n}
	}
}

func (m Model) skipTicket(p pickTicket) tea.Cmd {
	return func() tea.Msg {
		if _, err := m.app.Integrations.Skip(m.ctx, p.integration, integrations.Skip{Key: p.ticket.Key, Title: p.ticket.Title}, 0); err != nil {
			return failedMsg{err}
		}
		return ticketSkippedMsg{p.ticket.Key}
	}
}

func (m Model) onJiraMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	t := &m.morning.tickets
	switch msg := msg.(type) {
	case ticketsPulledMsg:
		t.pulling, t.pulled, t.at, t.ticked = false, msg.pulled, 0, map[string]bool{}
	case ticketsFailedMsg:
		t.pulling = false
		return m.show(friendly(msg.err), true)
	case ticketSkippedMsg:
		for i := range t.pulled {
			for j := range t.pulled[i].result.Sources {
				s := &t.pulled[i].result.Sources[j]
				s.Tickets = slices.DeleteFunc(s.Tickets, func(tk integrations.Ticket) bool { return tk.Key == msg.key })
			}
		}
		delete(t.ticked, msg.key)
		t.at = max(min(t.at, len(t.list())-1), 0)
		return m.show("Skipping "+msg.key+" from now on", false)
	case ticketsAddedMsg:
		next, cmd := m.toPace()
		shown, toast := next.(Model).show("Added "+count(msg.n, "task", "tasks"), false)
		return shown, tea.Batch(cmd, toast)
	}
	return m, nil
}

func (m Model) onJiraKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	t := &m.morning.tickets
	if t.pulling {
		return m, nil
	}
	if t.pulled == nil {
		switch k.String() {
		case "enter":
			t.pulling = true
			return m, tea.Batch(m.pullTickets(), m.spin.Tick)
		case "tab":
			return m.toPace()
		}
		return m, nil
	}
	list := t.list()
	switch k.String() {
	case "up", "k":
		t.at = max(t.at-1, 0)
	case "down", "j":
		t.at = max(min(t.at+1, len(list)-1), 0)
	case " ":
		if len(list) > 0 {
			key := list[t.at].ticket.Key
			t.ticked[key] = !t.ticked[key]
		}
	case "x":
		if len(list) > 0 {
			return m, m.skipTicket(list[t.at])
		}
	case "r":
		t.pulling = true
		return m, tea.Batch(m.pullTickets(), m.spin.Tick)
	case "enter":
		if slices.ContainsFunc(list, func(p pickTicket) bool { return t.ticked[p.ticket.Key] }) {
			return m, m.addTicked()
		}
		return m.toPace()
	case "tab":
		return m.toPace()
	}
	return m, nil
}

// jiraLines is the Jira step in at most room lines: the offer to pull, then each source's tickets
// or error, scrolled so the cursor's ticket is in view.
func (m Model) jiraLines(w, room int) (lines []string, hint string) {
	t := m.morning.tickets
	if t.pulling {
		return []string{m.spin.View() + softStyle.Render(" Pulling your tickets…")}, ""
	}
	if t.pulled == nil {
		return []string{"Pull from " + names(t.on) + "?"}, "enter pull · tab skip"
	}
	var body []string
	i, cursor := 0, 0
	for _, p := range t.pulled {
		for _, s := range p.result.Sources {
			if s.Error != "" {
				body = append(body, nowStyle.Render(s.Label+": "+s.Error))
				continue
			}
			if len(s.Tickets) == 0 {
				body = append(body, softStyle.Render("Nothing new in "+s.Label+"."))
				continue
			}
			body = append(body, lipgloss.NewStyle().Bold(true).Render(s.Label+" · "+count(len(s.Tickets), "ticket", "tickets")))
			for _, tk := range s.Tickets {
				if i == t.at {
					cursor = len(body)
				}
				body = append(body, ticketLine(tk, t.ticked[tk.Key], i == t.at, w))
				i++
			}
			if s.More > 0 {
				body = append(body, softStyle.Render(fmt.Sprintf("%d more matched — narrow the search", s.More)))
			}
		}
	}
	lines = append([]string{"Pick today's tickets", ""}, scrolled(body, cursor, room-2)...)
	return lines, "space pick · x skip · r pull · enter add · tab skip"
}

// names reads as "Jira", "Jira and Review Queue", or "A, B and C".
func names(views []integrations.View) string {
	var out []string
	for _, v := range views {
		out = append(out, v.Name)
	}
	if len(out) < 2 {
		return strings.Join(out, "")
	}
	return strings.Join(out[:len(out)-1], ", ") + " and " + out[len(out)-1]
}

func ticketLine(tk integrations.Ticket, ticked, current bool, w int) string {
	mark, box, title := "  ", "[ ] ", tk.Title
	if ticked {
		box = "[x] "
	}
	if current {
		mark, title = cursorStyle.Render("▸ "), lipgloss.NewStyle().Bold(true).Render(title)
	}
	return mark + box + lipgloss.NewStyle().MaxWidth(max(w-6, 1)).Render(title)
}
