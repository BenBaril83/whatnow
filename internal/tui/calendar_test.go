package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/integrations"
)

// calendarFeed has the fixture day's standup at 14:00 and a daily blockout at 15:00, both UTC.
const calendarFeed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" +
	"BEGIN:VEVENT\r\nUID:standup\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Standup\r\nDTSTART:20261006T140000Z\r\nDTEND:20261006T141500Z\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:blockout\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Blockout\r\nRRULE:FREQ=DAILY\r\nDTSTART:20261001T150000Z\r\nDTEND:20261001T160000Z\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

// calendars sets up Google Calendar with a Work calendar serving calendarFeed, and a Personal
// one that's gone, and turns it on.
func (f *fixture) calendars(t *testing.T) {
	t.Helper()
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "gone") {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, calendarFeed)
	}))
	t.Cleanup(feed.Close)
	ctx := context.Background()
	_, err := f.app.Integrations.Save(ctx, "gcal", integrations.Update{Lists: map[string][]integrations.RowUpdate{"calendars": {
		{Values: map[string]string{"label": "Work", "url": feed.URL + "/work.ics"}},
		{Values: map[string]string{"label": "Personal", "url": feed.URL + "/gone.ics"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Integrations.SetEnabled(ctx, "gcal", true); err != nil {
		t.Fatal(err)
	}
}

// todaysEvents lists the fixture day's live event titles.
func (f *fixture) todaysEvents(t *testing.T) []string {
	t.Helper()
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	events, err := f.app.Service.ListEvents(context.Background(), day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, e := range events {
		titles = append(titles, e.Title)
	}
	return titles
}

// calendarAt is a morning on the calendar step with today pulled.
func (f *fixture) calendarAt(t *testing.T) Model {
	t.Helper()
	m := f.morningAt(t)
	m = send(m, m.loadIntegrations()(), key("esc"))
	if m.morning.step != stepCalendar {
		t.Fatalf("step = %d, want the calendar", m.morning.step)
	}
	return send(m, m.pullAll()())
}

func TestTheMorningPullsTheCalendarAndSkipsASeries(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	tm := f.run(t)
	waitFor(t, tm, "to start your day")
	tm.Send(key("enter"))
	waitFor(t, tm, "What's on your plate?")
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	waitFor(t, tm, "Pull today's calendar?")
	tm.Send(key("enter"))
	waitFor(t, tm, "Work · 2 events")
	tm.Send(key("down"))
	tm.Send(key("x"))
	waitFor(t, tm, "t today · a always")
	tm.Send(key("a"))
	waitFor(t, tm, "Skipping Blockout from now on")
	tm.Send(key("enter"))
	waitFor(t, tm, "How hard")
	quit(t, tm)

	if diff := cmp.Diff([]string{"Standup"}, f.todaysEvents(t)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	v, err := f.app.Integrations.View(context.Background(), "gcal")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]integrations.Skip{{Key: "blockout", Title: "Blockout"}}, v.Skipped); diff != "" {
		t.Errorf("skipped (-want +got):\n%s", diff)
	}
}

func TestCalendarView(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := send(f.calendarAt(t), key("down"), key("x"))
	golden(t, "morning-calendar-80", m.View())
}

func TestXOnAOneOffRemovesItWithoutAsking(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := f.calendarAt(t)
	next, cmd := m.Update(key("x"))
	m = next.(Model)
	if m.morning.calendar.asking || cmd == nil {
		t.Fatalf("asking %v, cmd %v; want the standup removed", m.morning.calendar.asking, cmd)
	}
	m = send(m, cmd())
	if diff := cmp.Diff([]string{"Blockout"}, f.todaysEvents(t)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if got := m.morning.calendar.events(); len(got) != 1 || got[0].event.Title != "Blockout" {
		t.Errorf("listed = %+v", got)
	}
}

func TestJustTodayLeavesTheSeries(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := send(f.calendarAt(t), key("down"), key("x"))
	_, cmd := m.Update(key("t"))
	send(m, cmd())
	if diff := cmp.Diff([]string{"Standup"}, f.todaysEvents(t)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if v, _ := f.app.Integrations.View(context.Background(), "gcal"); len(v.Skipped) != 0 {
		t.Errorf("skipped = %+v", v.Skipped)
	}
}

func TestEscCancelsTheSkipQuestion(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := send(f.calendarAt(t), key("down"), key("x"))
	next, cmd := m.Update(key("esc"))
	if m = next.(Model); m.morning.calendar.asking || cmd != nil {
		t.Errorf("asking %v, cmd %v; want nothing removed", m.morning.calendar.asking, cmd)
	}
}

func TestTabSkipsThePull(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := f.morningAt(t)
	m = send(m, m.loadIntegrations()(), key("esc"), tea.KeyMsg{Type: tea.KeyTab})
	if m.morning.step != stepPace || len(f.todaysEvents(t)) != 0 {
		t.Errorf("step %d, events %v; want pace with nothing pulled", m.morning.step, f.todaysEvents(t))
	}
}

func TestAFailedPullStaysOnTheStep(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := f.morningAt(t)
	m = send(m, m.loadIntegrations()(), key("esc"), key("enter"))
	if !m.morning.calendar.pulling {
		t.Fatal("enter didn't start a pull")
	}
	m = send(m, pullFailedMsg{context.DeadlineExceeded})
	if m.morning.step != stepCalendar || m.morning.calendar.pulling || !strings.Contains(m.View(), "Pull today's calendar?") {
		t.Errorf("step %d, pulling %v:\n%s", m.morning.step, m.morning.calendar.pulling, m.View())
	}
}

func TestTheCursorStaysOnTheListAndRPullsAgain(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := send(f.calendarAt(t), key("up"), key("down"), key("down"), key("down"))
	if m.morning.calendar.at != 1 {
		t.Errorf("cursor = %d, want the last event", m.morning.calendar.at)
	}
	next, cmd := m.Update(key("r"))
	if m = next.(Model); !m.morning.calendar.pulling || cmd == nil {
		t.Fatal("r didn't pull again")
	}
	if m = send(m, key("enter")); m.morning.step != stepCalendar {
		t.Error("a key mid-pull moved on")
	}
}

func TestABusyCalendarScrollsToTheCursor(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := f.calendarAt(t)
	var events []integrations.PulledEvent
	for i := range 20 {
		events = append(events, integrations.PulledEvent{ID: int64(i + 1), Title: fmt.Sprintf("meeting %02d", i), StartsAt: f.clock.t})
	}
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24},
		pulledMsg{[]pulledFrom{{"gcal", integrations.PullResult{Sources: []integrations.SourceResult{{Label: "Work", Events: events}}}}}})
	for range events {
		m = send(m, key("down"))
	}
	v := m.View()
	for _, want := range []string{"meeting 19", "enter next"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
}
