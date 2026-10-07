package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/jameynakama/whatnow/internal/domain"
)

const keyHint = "space done · w what now · a add · p push · x drop · u undo · c chat · ? keys · q quit"

var helpKeys = [][2]string{
	{"↑ ↓  k j", "move"},
	{"space", "mark done"},
	{"w", "what now"},
	{"n", "not this one, ask again"},
	{"a", "add a task"},
	{"p", "push to tomorrow"},
	{"x", "drop"},
	{"u", "undo your last change"},
	{"c", "chat"},
	{"r", "refresh"},
	{"q", "quit"},
}

func (m Model) View() string {
	switch {
	case m.width == 0:
		return ""
	case m.snap == nil && m.loadErr != nil:
		return "Couldn't load today: " + m.loadErr.Error()
	case m.snap == nil:
		return "Loading…"
	case m.snap.today.Plan == nil:
		return m.noDayView()
	case m.mode == modeHelp:
		return m.helpView()
	}
	return m.todayView()
}

func (m Model) noDayView() string {
	card := sticker([]string{
		cursorStyle.Render("whatnow") + softStyle.Render(" · executive function for the rest of us"),
		"",
		"☀  Good morning.",
		"",
		"Start your day in the browser:",
		cursorStyle.Render(m.webURL),
		"",
		softStyle.Render("I'll pick it up from there."),
	}, min(56, m.width), 11)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card)
}

func (m Model) helpView() string {
	lines := []string{cursorStyle.Render("keys"), ""}
	for _, k := range helpKeys {
		lines = append(lines, fmt.Sprintf("%-10s %s", k[0], k[1]))
	}
	lines = append(lines, "", softStyle.Render("any key to close"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, sticker(lines, min(40, m.width), len(lines)+3))
}

func (m Model) todayView() string {
	top := []string{m.header()}
	if !m.wrapped() && wrapDue(m.snap.now, m.snap.endOfDay) {
		top = append(top, ansi.Truncate(toastStyle.Render("Nearly the end of the day. Wrap up in the browser: "+m.webURL), m.width, "…"))
	}
	head := strings.Join(top, "\n")
	body := m.body(max(m.height-lipgloss.Height(head)-2, 4))
	return strings.Join([]string{head, body, m.toastLine(), m.footer()}, "\n")
}

// wrapDue says it's time to wrap up: within an hour of the end of day, or past it.
func wrapDue(now, endOfDay time.Time) bool {
	return !now.Before(endOfDay.Add(-time.Hour))
}

func (m Model) header() string {
	s := m.snap
	line := fmt.Sprintf("%s  %s  %s",
		cursorStyle.Render("whatnow"),
		s.now.Format("Mon Jan 2"),
		softStyle.Render(fmt.Sprintf("%d of %d done", s.doneToday, s.doneToday+len(s.order()))))
	if m.stale {
		line += "  " + nowStyle.Render("stale")
	}
	if m.asking || m.sending {
		line += "  " + m.spin.View()
	}
	strip := layoutStrip(s.events, s.now, s.endOfDay, s.today.Day.StartedAt, max(m.width-5, 1)).render()
	return sticker(append([]string{line}, strings.Split(strip, "\n")...), m.width, 6)
}

func (m Model) body(h int) string {
	switch {
	case !m.chatOpen:
		return sticker(m.mainLines(m.width-5, h-3), m.width, h)
	case m.width < wideEnough:
		return sticker(m.chatLines(m.width-5, h-3), m.width, h)
	}
	chatW := m.chatPaneWidth()
	return lipgloss.JoinHorizontal(lipgloss.Top,
		sticker(m.mainLines(m.width-chatW-5, h-3), m.width-chatW, h),
		sticker(m.chatLines(chatW-5, h-3), chatW, h))
}

// mainLines is the main pane w columns wide: the list, or the goodnight card once the day is wrapped.
func (m Model) mainLines(w, rows int) []string {
	if m.wrapped() {
		return m.goodnightLines(w)
	}
	return m.listLines(rows)
}

// chatPaneWidth is the chat pane's width, shadow included: two fifths beside the list, or the
// whole width in its place.
func (m Model) chatPaneWidth() int {
	if m.width < wideEnough {
		return m.width
	}
	return m.width - m.width*3/5
}

// chatLines is today's chat, newest at the bottom above the input, wrapped to w columns.
func (m Model) chatLines(w, rows int) []string {
	var lines []string
	say := func(who, text string, style lipgloss.Style) {
		wrapped := lipgloss.NewStyle().Width(max(w-5, 1)).Render(text)
		for i, l := range strings.Split(wrapped, "\n") {
			label := "     "
			if i == 0 {
				label = style.Render(fmt.Sprintf("%-5s", who))
			}
			lines = append(lines, label+l)
		}
	}
	for _, msg := range m.snap.chat {
		if msg.Role == domain.RoleUser {
			say("you", msg.Text, softStyle)
		} else {
			say("wn", msg.Text, cursorStyle)
		}
	}
	if m.streaming != "" {
		say("wn", m.streaming, cursorStyle)
	}
	input := softStyle.Render("c to chat")
	if m.mode == modeChat {
		input = m.input.View()
	}
	room := max(rows-2, 0)
	if len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	for len(lines) < room {
		lines = append([]string{""}, lines...)
	}
	return append(lines, "", input)
}

// listLines is the list, scrolled so the cursor and the line below it are in view.
func (m Model) listLines(rows int) []string {
	groups := m.snap.groups()
	if len(groups) == 0 {
		return []string{softStyle.Render("All clear. Nothing open on today's list.")}
	}
	var lines []string
	at := 0
	for _, g := range groups {
		if g.tier == "" {
			lines = append(lines, softStyle.Render("not planned"))
		} else {
			lines = append(lines, chip(strings.ToUpper(string(g.tier)), tierFill[g.tier]))
		}
		for _, id := range g.ids {
			if id == m.cursor {
				at = len(lines)
			}
			lines = append(lines, m.row(m.snap.tasks[id]))
			if m.pick != nil && m.pick.TaskID == id {
				lines = append(lines, softStyle.Italic(true).Render("    ↳ "+m.pick.Reason+"  ·  n not this one"))
			}
		}
	}
	if top := at - rows + min(2, rows); top > 0 {
		lines = lines[top:]
	}
	return lines
}

func (m Model) row(t domain.Task) string {
	mark, title := "  ", t.Title
	if t.ID == m.cursor {
		mark, title = cursorStyle.Render("▸ "), lipgloss.NewStyle().Bold(true).Render(title)
	}
	line := mark + "☐ " + title
	if n := m.snap.waits[t.ID]; n > 0 {
		line += softStyle.Render(fmt.Sprintf("  ⛓ %d", n))
	}
	if m.pick != nil && m.pick.TaskID == t.ID {
		line += "  " + chip("what now", chipFill)
	}
	return line
}

func (m Model) toastLine() string {
	if m.toast.text == "" {
		return ""
	}
	style := toastStyle
	if m.toast.err {
		style = errorStyle
	}
	return ansi.Truncate(style.Render(m.toast.text), m.width, "…")
}

func (m Model) footer() string {
	if m.mode == modeChat {
		return softStyle.Render(ansi.Truncate("enter send · esc back to the list", m.width, "…"))
	}
	if m.mode == modeAdd || m.mode == modePass {
		return ansi.Truncate(m.input.View(), m.width, "…")
	}
	hint := keyHint
	if m.wrapped() {
		hint = goodnightKeyHint
	}
	return softStyle.Render(ansi.Truncate(hint, m.width, "…"))
}
