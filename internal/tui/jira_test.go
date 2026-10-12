package tui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/integrations"
)

// jiraSearch has one ticket of the user's and one up for grabs.
const jiraSearch = `{"total": 2, "issues": [
	{"key": "FLEX1-1", "fields": {"summary": "Mine", "description": "d", "assignee": {"name": "me"}}},
	{"key": "FLEX1-2", "fields": {"summary": "Up for grabs", "description": null, "assignee": null}}
]}`

// jira sets up Jira against a fake search and turns it on. With fail, the search answers 401.
func (f *fixture) jira(t *testing.T, fail bool) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, jiraSearch)
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	if _, err := f.app.Integrations.Save(ctx, "jira", integrations.Update{Values: map[string]string{"url": srv.URL, "token": "t", "jql": "x"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Integrations.SetEnabled(ctx, "jira", true); err != nil {
		t.Fatal(err)
	}
}

// jiraAt is a morning on the Jira step, pulled.
func (f *fixture) jiraAt(t *testing.T) Model {
	t.Helper()
	m := f.morningAt(t)
	m = send(m, m.loadIntegrations()(), key("esc"))
	if m.morning.step != stepJira {
		t.Fatalf("step = %d, want Jira", m.morning.step)
	}
	return send(m, m.pullTickets()())
}

func (f *fixture) taskTitles(t *testing.T) []string {
	t.Helper()
	tasks, err := f.app.Service.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, tk := range tasks {
		titles = append(titles, tk.Title)
	}
	return titles
}

func TestTheMorningPicksJiraTickets(t *testing.T) {
	f := newFixture(t)
	f.jira(t, false)
	tm := f.run(t)
	waitFor(t, tm, "to start your day")
	tm.Send(key("enter"))
	waitFor(t, tm, "What's on your plate?")
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	waitFor(t, tm, "Pull your Jira tickets?")
	tm.Send(key("enter"))
	waitFor(t, tm, "Jira · 2 tickets")
	tm.Send(key(" "))
	tm.Send(key("enter"))
	waitFor(t, tm, "How hard")
	quit(t, tm)
	if diff := cmp.Diff([]string{"FLEX1-1 Mine"}, f.taskTitles(t)); diff != "" {
		t.Errorf("tasks (-want +got):\n%s", diff)
	}
}

func TestJiraView(t *testing.T) {
	f := newFixture(t)
	f.jira(t, false)
	m := send(f.jiraAt(t), key(" "), key("down"))
	golden(t, "morning-jira-80", m.View())
}

func TestXSkipsATicketForGood(t *testing.T) {
	f := newFixture(t)
	f.jira(t, false)
	m := send(f.jiraAt(t), key("down"))
	_, cmd := m.Update(key("x"))
	m = send(m, cmd())
	if got := m.morning.tickets.list(); len(got) != 1 || got[0].ticket.Key != "FLEX1-1" {
		t.Errorf("listed = %+v", got)
	}
	v, err := f.app.Integrations.View(context.Background(), "jira")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Skipped) != 1 || v.Skipped[0].Key != "FLEX1-2" {
		t.Errorf("skipped = %+v", v.Skipped)
	}
}

func TestTabLeavesJiraWithoutAdding(t *testing.T) {
	f := newFixture(t)
	f.jira(t, false)
	m := send(f.jiraAt(t), key(" "), tea.KeyMsg{Type: tea.KeyTab})
	if m.morning.step != stepPace || len(f.taskTitles(t)) != 0 {
		t.Errorf("step %d, tasks %v", m.morning.step, f.taskTitles(t))
	}
}

func TestAFailedJiraPullShowsWhyAndRRetries(t *testing.T) {
	f := newFixture(t)
	f.jira(t, true)
	m := f.jiraAt(t)
	if !strings.Contains(m.View(), "Jira rejected the token") {
		t.Errorf("view:\n%s", m.View())
	}
	next, cmd := m.Update(key("r"))
	if !next.(Model).morning.tickets.pulling || cmd == nil {
		t.Error("r didn't pull again")
	}
}

func TestTheCalendarStepIsOnlyForCalendars(t *testing.T) {
	f := newFixture(t)
	f.jira(t, false)
	m := f.morningAt(t)
	m = send(m, m.loadIntegrations()(), key("esc"))
	if m.morning.step != stepJira {
		t.Errorf("step = %d, want Jira, skipping the calendar", m.morning.step)
	}
}

func TestTheCalendarStepGoesOnToJira(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	f.jira(t, false)
	m := send(f.calendarAt(t), key("enter"))
	if m.morning.step != stepJira {
		t.Errorf("step = %d, want Jira", m.morning.step)
	}
}
