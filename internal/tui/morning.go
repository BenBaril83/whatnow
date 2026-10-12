package tui

import (
	"context"
	"errors"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
)

// step is where the good-morning ritual is. stepNone means it isn't running.
type step int

const (
	stepNone step = iota
	stepGreet
	stepReconcile
	stepDump
	stepCalendar
	stepPace
	stepMaking
	stepReveal
)

const freshList = "Something changed, here's the fresh list."

// cardWidth is the widest the good-morning card and the ritual's steps get.
const cardWidth = 56

// morning is the good-morning ritual: reconcile, brain dump, pace and mood, then the plan.
type morning struct {
	step     step
	review   *planner.Review
	rows     []triageRow
	at       int // the triage row under the cursor
	notice   string
	dump     textarea.Model
	pace     domain.Pace
	rowsKept []triageRow // the choices to keep when the review is fetched again after a conflict
	calendar calendar
}

// triageRow is one unfinished task from the last day and what happens to it.
type triageRow struct {
	task   domain.Task
	reason string
	action planner.Action
	until  domain.Date // where a push goes: the suggested date, or tomorrow
}

type (
	reviewedMsg     struct{ review *planner.Review }
	reviewFailedMsg struct{ err error }
	startedMsg      struct{}
	startFailedMsg  struct {
		err       error
		dumpSaved bool // the dump went in before the start failed, so a retry mustn't send it again
	}
	// conflictMsg is a start refused because a task in the triage changed elsewhere.
	conflictMsg struct{}
)

var actions = []planner.Action{planner.ActionRollOver, planner.ActionPush, planner.ActionDrop}

// triageRows lists the review's unfinished tasks, each on the model's suggestion or on roll over
// when it has none. A task that was already on the list keeps the choice made for it.
func triageRows(r *planner.Review, tomorrow domain.Date, kept []triageRow) []triageRow {
	var rows []triageRow
	for _, t := range r.Unfinished {
		row := triageRow{task: t, action: planner.ActionRollOver, until: tomorrow}
		for _, s := range r.Suggestions {
			if s.TaskID != t.ID {
				continue
			}
			row.reason = s.Reason
			if s.Action.Valid() {
				row.action = s.Action
			}
			if s.Until != "" {
				row.until = s.Until
			}
		}
		if i := slices.IndexFunc(kept, func(k triageRow) bool { return k.task.ID == t.ID }); i >= 0 {
			row.action = kept[i].action
		}
		rows = append(rows, row)
	}
	return rows
}

// cycle moves the row's action by one, wrapping around.
func (r *triageRow) cycle(by int) {
	i := slices.Index(actions, r.action)
	r.action = actions[(i+by+len(actions))%len(actions)]
}

// payload is the triage to start the day with. Until is set only for a push.
func payload(rows []triageRow) []planner.Triage {
	out := make([]planner.Triage, 0, len(rows))
	for _, r := range rows {
		t := planner.Triage{TaskID: r.task.ID, Action: r.action}
		if r.action == planner.ActionPush {
			until := r.until
			t.Until = &until
		}
		out = append(out, t)
	}
	return out
}

func newDump() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Everything on your mind, one thing per line"
	ta.ShowLineNumbers = false
	ta.SetHeight(6)
	return ta
}

// beginMorning starts the ritual from the good-morning card by looking back at the last day.
func (m Model) beginMorning() (tea.Model, tea.Cmd) {
	m.morning = morning{step: stepGreet, dump: newDump(), pace: 3}
	return m, tea.Batch(m.reviewMorning(), m.loadIntegrations(), m.spin.Tick)
}

func (m Model) reviewMorning() tea.Cmd {
	return func() tea.Msg {
		r, err := m.app.Planner.MorningReview(m.ctx)
		if err != nil {
			return reviewFailedMsg{err}
		}
		return reviewedMsg{r}
	}
}

// startDay saves the brain dump and starts the day, as the API's start does. The dump is one
// captured card, cleaned up right away so its tasks land in the plan; if cleanup fails, the raw
// card is planned as it is and goes to the background runner.
func (m Model) startDay(pace domain.Pace, mood string, triage []planner.Triage, dump string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(dump) != "" {
			var task domain.Task
			if _, err := m.commit(func(ctx context.Context, batch int64) (err error) {
				task, err = m.app.Service.CreateTask(ctx, batch, service.TaskFields{RawText: dump})
				return err
			}); err != nil {
				return startFailedMsg{err: err}
			}
			if _, err := m.app.Cleanup.Clean(m.ctx, task); err != nil {
				if !errors.Is(err, settings.ErrNotConfigured) {
					log.Printf("clean morning dump: %v", err)
				}
				m.app.Cleanup.Enqueue(task)
			}
		}
		_, _, err := m.app.Planner.StartDay(m.ctx, pace, mood, triage)
		if errors.Is(err, service.ErrConflict) {
			return conflictMsg{}
		}
		if err != nil {
			return startFailedMsg{err: err, dumpSaved: strings.TrimSpace(dump) != ""}
		}
		return startedMsg{}
	}
}

func (m Model) onMorningMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	// A review that lands after the ritual ended, because the day started elsewhere, is dropped.
	switch msg.(type) {
	case reviewedMsg, reviewFailedMsg:
		if m.morning.step != stepGreet {
			return m, nil
		}
	}
	switch msg := msg.(type) {
	case reviewedMsg:
		m.morning.review = msg.review
		m.morning.rows = nil
		if msg.review != nil {
			tomorrow := domain.Date(m.app.Now().In(m.app.Loc).AddDate(0, 0, 1).Format(time.DateOnly))
			m.morning.rows = triageRows(msg.review, tomorrow, m.morning.rowsKept)
		}
		m.morning.at = 0
		if len(m.morning.rows) > 0 {
			m.morning.step = stepReconcile
			return m, nil
		}
		if m.morning.rowsKept != nil {
			return m.toPace()
		}
		return m.toDump()
	case reviewFailedMsg:
		m.morning = morning{}
		return m.show("Couldn't look back at yesterday: "+friendly(msg.err), true)
	case startedMsg:
		m.morning.step = stepReveal
		return m, m.load()
	case conflictMsg:
		// The dump was saved before the start was refused, so it mustn't be sent again.
		m.morning.dump.Reset()
		m.morning.rowsKept = m.morning.rows
		m.morning.step, m.morning.notice = stepGreet, freshList
		return m, tea.Batch(m.reviewMorning(), m.spin.Tick)
	default:
		failed := msg.(startFailedMsg)
		if failed.dumpSaved {
			m.morning.dump.Reset()
		}
		m.morning.step = stepPace
		next, cmd := m.show(friendly(failed.err), true)
		return next, tea.Batch(cmd, next.input.Focus())
	}
}

func (m Model) toDump() (tea.Model, tea.Cmd) {
	m.morning.step = stepDump
	m.fitInput()
	return m, m.morning.dump.Focus()
}

func (m Model) toPace() (tea.Model, tea.Cmd) {
	m.morning.dump.Blur()
	m.morning.step = stepPace
	m.input.Reset()
	m.input.Placeholder = "How are you feeling? (optional)"
	m.fitInput()
	return m, m.input.Focus()
}

func (m Model) onMorningKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	mo := &m.morning
	switch mo.step {
	case stepReconcile:
		switch k.String() {
		case "up", "k":
			mo.at = max(mo.at-1, 0)
		case "down", "j":
			mo.at = min(mo.at+1, len(mo.rows)-1)
		case "left", "h":
			mo.rows[mo.at].cycle(-1)
		case "right", "l":
			mo.rows[mo.at].cycle(1)
		case "enter":
			return m.toDump()
		}
	case stepDump:
		mo.notice = ""
		switch k.String() {
		case "tab":
			return m.afterDump()
		case "esc":
			mo.dump.Reset()
			return m.afterDump()
		}
		var cmd tea.Cmd
		mo.dump, cmd = mo.dump.Update(k)
		return m, cmd
	case stepCalendar:
		return m.onCalendarKey(k)
	case stepPace:
		switch k.String() {
		case "left":
			mo.pace = max(mo.pace-1, 1)
		case "right":
			mo.pace = min(mo.pace+1, 5)
		case "enter":
			mood := strings.TrimSpace(m.input.Value())
			m.input.Blur()
			mo.step = stepMaking
			return m, tea.Batch(m.startDay(mo.pace, mood, payload(mo.rows), mo.dump.Value()), m.spin.Tick)
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(k)
			return m, cmd
		}
	case stepReveal:
		if k.String() == "enter" && m.snap.today.Plan != nil {
			m.morning = morning{}
		}
	}
	return m, nil
}

// morningView is the ritual's current step, centered in a sticker card.
func (m Model) morningView() string {
	w := min(cardWidth, m.width)
	inner := w - 5
	mo := m.morning
	lines := []string{cursorStyle.Render("whatnow") + softStyle.Render(" · executive function for the rest of us"), ""}
	var hint string
	switch mo.step {
	case stepGreet:
		lines = append(lines, "☀  Good morning.", "", m.spin.View()+softStyle.Render(" Looking back…"))
	case stepReconcile:
		if n := len(mo.review.Done); n > 0 {
			lines = append(lines, lipgloss.NewStyle().Bold(true).Render("Last time you got "+count(n, "thing", "things")+" done."))
		}
		lines = append(lines, "Left over from last time:")
		if mo.review.Fallback {
			lines = append(lines, chip("rough suggestions", chipFill))
		}
		if mo.notice != "" {
			lines = append(lines, toastStyle.Render(mo.notice))
		}
		lines = append(lines, "")
		// Everything else in the card: the toast, the hint, and the frame.
		lines = append(lines, triageLines(mo.rows, mo.at, inner, m.height-len(lines)-7)...)
		hint = "↑↓ move · ←→ change · enter next"
	case stepDump:
		lines = append(lines, "What's on your plate?", "", mo.dump.View())
		hint = "enter new line · tab next · esc skip"
	case stepCalendar:
		var more []string
		// Everything else in the card, as for reconcile: the toast, the hint, and the frame.
		more, hint = m.calendarLines(inner, m.height-len(lines)-7)
		lines = append(lines, more...)
	case stepPace:
		if mo.notice != "" {
			lines = append(lines, toastStyle.Render(mo.notice), "")
		}
		lines = append(lines, "How hard do you want to push today?", "", paceRow(mo.pace), "", m.input.View())
		hint = "←→ pace · enter start the day"
	case stepMaking:
		lines = append(lines, m.spin.View()+softStyle.Render(" Making your plan…"))
	case stepReveal:
		lines = append(lines, m.revealLines(inner)...)
		hint = "enter let's go"
	}
	if t := m.toastLine(); t != "" {
		lines = append(lines, "", t)
	}
	if hint != "" {
		lines = append(lines, "", softStyle.Render(hint))
	}
	card := sticker(lines, w, min(len(lines)+3, m.height))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card)
}

// triageLines is the triage list in at most rows lines, scrolled so the cursor's task and the
// line below it are in view.
func triageLines(rows []triageRow, at, w, room int) []string {
	var lines []string
	cursor := 0
	for i, r := range rows {
		if i == at {
			cursor = len(lines)
		}
		lines = append(lines, triageLine(r, i == at, w))
		if r.reason != "" {
			lines = append(lines, softStyle.Render("    "+r.reason))
		}
	}
	return scrolled(lines, cursor, room)
}

// scrolled is at most room of lines, scrolled so the cursor's line and the one below it are in view.
func scrolled(lines []string, cursor, room int) []string {
	room = max(room, 2)
	top := min(max(cursor+2-room, 0), max(len(lines)-room, 0))
	return lines[top:min(top+room, len(lines))]
}

func triageLine(r triageRow, current bool, w int) string {
	label := "roll over"
	switch r.action {
	case planner.ActionPush:
		label = "push → " + pushLabel(r.until)
	case planner.ActionDrop:
		label = "drop"
	}
	choice := "‹ " + label + " ›"
	mark, title := "  ", r.task.Title
	if current {
		mark, choice = cursorStyle.Render("▸ "), cursorStyle.Render(choice)
		title = lipgloss.NewStyle().Bold(true).Render(title)
	}
	room := max(w-lipgloss.Width(choice)-4, 1)
	return mark + lipgloss.NewStyle().Width(room).MaxWidth(room).Render(title) + "  " + choice
}

// pushLabel names a push date the way the header names today, such as "Sat Oct 10".
func pushLabel(d domain.Date) string {
	t, err := time.Parse(time.DateOnly, string(d))
	if err != nil {
		return string(d)
	}
	return t.Format("Mon Jan 2")
}

// paceRow is the chosen pace by name, with a dot for each of the five levels. All five names don't
// fit across the card.
func paceRow(pace domain.Pace) string {
	dots := strings.Repeat("●", int(pace)) + strings.Repeat("○", 5-int(pace))
	return "‹ " + chip(pace.Label(), chipFill) + " ›  " + cursorStyle.Render(dots)
}

// revealLines is the new plan: its opening line, then each tier's titles in its color.
func (m Model) revealLines(w int) []string {
	plan := m.snap.today.Plan
	if plan == nil {
		return []string{m.spin.View() + softStyle.Render(" Making your plan…")}
	}
	lines := strings.Split(lipgloss.NewStyle().Bold(true).Width(max(w, 1)).Render(plan.Opening), "\n")
	for _, g := range m.snap.groups() {
		if g.tier == "" {
			continue
		}
		lines = append(lines, "", chip(strings.ToUpper(string(g.tier)), tierFill[g.tier]))
		for _, id := range g.ids {
			lines = append(lines, "  "+m.snap.tasks[id].Title)
		}
	}
	return lines
}
