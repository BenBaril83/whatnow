package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/llm/fake"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/settings"
)

// yesterday starts the day before the fixture's with tasks and leaves it unwrapped, so this
// morning has them to reconcile.
func (f *fixture) yesterday(t *testing.T, titles ...string) []domain.Task {
	t.Helper()
	today := f.clock.t
	f.clock.t = today.AddDate(0, 0, -1)
	var tasks []domain.Task
	for _, title := range titles {
		tasks = append(tasks, f.task(t, title))
	}
	f.start(t)
	f.clock.t = today
	return tasks
}

// morningAt is a TUI on the good-morning card with the ritual begun and the review fetched.
func (f *fixture) morningAt(t *testing.T) Model {
	t.Helper()
	next, _ := f.model(t, 80, 30).Update(key("enter"))
	m := next.(Model)
	return send(m, m.reviewMorning()())
}

func TestTriageRows(t *testing.T) {
	tomorrow := domain.Date("2026-10-07")
	gutter, banjo, bank := domain.Task{ID: 1, Title: "fix the gutter"}, domain.Task{ID: 2, Title: "learn banjo"}, domain.Task{ID: 3, Title: "call the bank"}
	review := &planner.Review{
		Unfinished: []domain.Task{gutter, banjo, bank},
		Suggestions: []planner.Suggestion{
			{TaskID: 1, Reason: "rainy all week", Action: planner.ActionPush, Until: "2026-10-10"},
			{TaskID: 2, Reason: "untouched for 9 days", Action: planner.ActionPush},
		},
	}
	tests := []struct {
		name string
		kept []triageRow
		want []triageRow
	}{
		{
			name: "suggestions prefill, and a task without one rolls over",
			want: []triageRow{
				{task: gutter, reason: "rainy all week", action: planner.ActionPush, until: "2026-10-10"},
				{task: banjo, reason: "untouched for 9 days", action: planner.ActionPush, until: tomorrow},
				{task: bank, action: planner.ActionRollOver, until: tomorrow},
			},
		},
		{
			name: "a task already on the list keeps its choice",
			kept: []triageRow{{task: banjo, action: planner.ActionDrop}, {task: domain.Task{ID: 9}, action: planner.ActionDrop}},
			want: []triageRow{
				{task: gutter, reason: "rainy all week", action: planner.ActionPush, until: "2026-10-10"},
				{task: banjo, reason: "untouched for 9 days", action: planner.ActionDrop, until: tomorrow},
				{task: bank, action: planner.ActionRollOver, until: tomorrow},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := triageRows(review, tomorrow, tt.kept)
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(triageRow{})); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestCycleWrapsAroundTheActions(t *testing.T) {
	r := triageRow{action: planner.ActionRollOver}
	var got []planner.Action
	for range 3 {
		r.cycle(1)
		got = append(got, r.action)
	}
	r.cycle(-1)
	got = append(got, r.action)
	want := []planner.Action{planner.ActionPush, planner.ActionDrop, planner.ActionRollOver, planner.ActionDrop}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestPayloadSetsUntilOnlyForAPush(t *testing.T) {
	got := payload([]triageRow{
		{task: domain.Task{ID: 1}, action: planner.ActionPush, until: "2026-10-10"},
		{task: domain.Task{ID: 2}, action: planner.ActionDrop, until: "2026-10-07"},
	})
	until := domain.Date("2026-10-10")
	want := []planner.Triage{{TaskID: 1, Action: planner.ActionPush, Until: &until}, {TaskID: 2, Action: planner.ActionDrop}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestAFirstMorningSkipsReconcileAndPlansTheDump(t *testing.T) {
	f := newFixture(t)
	tm := f.run(t)
	waitFor(t, tm, "to start your day")
	tm.Send(key("enter"))
	waitFor(t, tm, "What's on your plate?")
	tm.Type("buy milk")
	tm.Send(key("enter"))
	tm.Type("call mom")
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	waitFor(t, tm, "How hard")
	tm.Send(key("right"))
	tm.Send(key("right"))
	tm.Send(key("left"))
	tm.Type("sleepy")
	tm.Send(key("enter"))
	waitFor(t, tm, fake.Opening)
	tm.Send(key("enter"))
	waitFor(t, tm, "space done")
	quit(t, tm)

	today, err := f.app.Planner.Today(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if today.Day.Pace != 4 || today.Day.MoodNote != "sleepy" {
		t.Errorf("day = pace %d, mood %q; want 4 and sleepy", today.Day.Pace, today.Day.MoodNote)
	}
	s := f.load(t)
	var titles []string
	for _, id := range s.order() {
		titles = append(titles, s.tasks[id].Title)
	}
	if diff := cmp.Diff([]string{"buy milk", "call mom"}, titles); diff != "" {
		t.Errorf("planned (-want +got):\n%s", diff)
	}
}

func TestReconcileStartsTheDayWithTheChosenTriage(t *testing.T) {
	f := newFixture(t)
	tasks := f.yesterday(t, "fix the gutter", "learn banjo", "call the bank")
	tm := f.run(t)
	waitFor(t, tm, "to start your day")
	tm.Send(key("enter"))
	waitFor(t, tm, fake.Suggest)
	tm.Send(key("right")) // the gutter: push
	tm.Send(key("down"))
	tm.Send(key("left")) // the banjo: drop
	tm.Send(key("up"))
	tm.Send(key("enter"))
	waitFor(t, tm, "What's on your plate?")
	tm.Type("a thought")
	tm.Send(key("esc")) // skips the dump, clearing it
	waitFor(t, tm, "How hard")
	tm.Send(key("enter"))
	waitFor(t, tm, fake.Opening)
	quit(t, tm)

	gutter, banjo, bank := f.status(t, tasks[0].ID), f.status(t, tasks[1].ID), f.status(t, tasks[2].ID)
	if gutter.NotBefore == nil || *gutter.NotBefore != "2026-10-07" {
		t.Errorf("gutter not before = %v, want pushed to tomorrow", gutter.NotBefore)
	}
	if banjo.Status != domain.StatusDropped || bank.Status != domain.StatusOpen {
		t.Errorf("banjo %s, bank %s; want dropped and still open", banjo.Status, bank.Status)
	}
	all, err := f.app.Service.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("%d tasks, want the skipped dump left unsaved", len(all))
	}
}

func TestTheDumpIsPlannedRawWhenCleanupFails(t *testing.T) {
	f := newFixtureWith(t, func(settings.Settings) (llm.Client, settings.ProviderConfig, error) {
		return nil, settings.ProviderConfig{}, settings.ErrNotConfigured
	})
	m := f.morningAt(t)
	m = send(m, m.startDay(3, "", nil, "call the bank")())
	if m.morning.step != stepReveal {
		t.Fatalf("step = %d, want the reveal", m.morning.step)
	}
	s := f.load(t)
	if ids := s.order(); len(ids) != 1 || s.tasks[ids[0]].RawText != "call the bank" {
		t.Errorf("planned %v, want the raw card", ids)
	}
}

func TestReconcileView(t *testing.T) {
	f := newFixture(t)
	f.yesterday(t, "fix the gutter", "learn banjo")
	golden(t, "morning-reconcile-80", f.morningAt(t).View())
}

func TestRevealView(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.task(t, "call mom")
	m := f.morningAt(t)
	m = send(m, m.startDay(3, "", nil, "")())
	f.task(t, "renew passport") // not planned, so the reveal leaves it out
	m = send(m, snapshotMsg{f.load(t)})
	golden(t, "morning-reveal-80", m.View())
	if m = send(m, key("enter")); m.morning.step != stepNone {
		t.Errorf("enter on the reveal left the ritual at step %d", m.morning.step)
	}
}

func TestAConflictFetchesTheReviewAgainAndKeepsTheChoices(t *testing.T) {
	f := newFixture(t)
	tasks := f.yesterday(t, "fix the gutter", "learn banjo")
	m := f.morningAt(t)
	m = send(m, key("right"), key("enter"))
	m.morning.dump.SetValue("already saved")
	m = send(m, conflictMsg{})
	if m.morning.step != stepGreet || m.morning.dump.Value() != "" {
		t.Fatalf("step %d, dump %q; want looking back again with the dump cleared", m.morning.step, m.morning.dump.Value())
	}
	// The banjo was finished elsewhere, so only the gutter is left to triage.
	m = send(m, reviewedMsg{&planner.Review{Unfinished: tasks[:1]}})
	if m.morning.step != stepReconcile || m.morning.rows[0].action != planner.ActionPush {
		t.Errorf("step %d, rows %+v; want reconcile with the push kept", m.morning.step, m.morning.rows)
	}
	if !strings.Contains(m.View(), freshList) {
		t.Errorf("no fresh-list notice:\n%s", m.View())
	}
}

func TestAConflictWithNothingLeftToTriageGoesToPace(t *testing.T) {
	f := newFixture(t)
	f.yesterday(t, "fix the gutter")
	m := f.morningAt(t)
	m = send(m, key("enter"), conflictMsg{}, reviewedMsg{&planner.Review{}})
	if m.morning.step != stepPace || !strings.Contains(m.View(), freshList) {
		t.Errorf("step = %d, want pace with the fresh-list notice:\n%s", m.morning.step, m.View())
	}
}

func TestADayStartedElsewhereEndsTheRitual(t *testing.T) {
	f := newFixture(t)
	m := f.morningAt(t)
	f.start(t)
	if m = send(m, snapshotMsg{f.load(t)}); m.morning.step != stepNone {
		t.Errorf("step = %d, want Today", m.morning.step)
	}
}

func TestAFailedReviewReturnsToTheCard(t *testing.T) {
	f := newFixture(t)
	m := f.model(t, 80, 24)
	m = send(m, key("enter"), reviewFailedMsg{errors.New("db gone")})
	if m.morning.step != stepNone || !strings.Contains(m.View(), "db gone") || !strings.Contains(m.View(), "to start your day") {
		t.Errorf("step %d:\n%s", m.morning.step, m.View())
	}
}

func TestAFailedStartStaysOnPace(t *testing.T) {
	f := newFixture(t)
	m := f.morningAt(t)
	m = send(m, key("esc"), key("enter"), startFailedMsg{err: errors.New("db gone")})
	if m.morning.step != stepPace || !strings.Contains(m.View(), "db gone") {
		t.Errorf("step %d:\n%s", m.morning.step, m.View())
	}
}

func TestQDoesNotQuitMidRitual(t *testing.T) {
	f := newFixture(t)
	m := f.morningAt(t)
	if _, cmd := m.Update(key("q")); cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Error("q quit the ritual")
		}
	}
}

func TestPushLabel(t *testing.T) {
	for in, want := range map[domain.Date]string{"2026-10-10": "Sat Oct 10", "someday": "someday"} {
		if got := pushLabel(in); got != want {
			t.Errorf("pushLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReconcileShowsTheWinsAndRoughSuggestions(t *testing.T) {
	f := newFixture(t)
	tasks := f.yesterday(t, "fix the gutter", "buy milk")
	m := send(f.model(t, 80, 30), key("enter"), reviewedMsg{&planner.Review{Done: tasks[1:], Unfinished: tasks[:1], Fallback: true}})
	for _, want := range []string{"Last time you got 1 thing done.", "rough suggestions"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("missing %q:\n%s", want, m.View())
		}
	}
}

func TestStartingADayThatAlreadyStartedIsAConflict(t *testing.T) {
	f := newFixture(t)
	m := f.morningAt(t)
	f.start(t)
	if got := m.startDay(3, "", nil, "")(); got != (conflictMsg{}) {
		t.Errorf("got %#v, want a conflict", got)
	}
	if got, ok := m.startDay(0, "", nil, "")().(startFailedMsg); !ok {
		t.Errorf("a start at pace 0 gave %#v, want it to fail", got)
	}
}

func TestTheRitualReportsDatabaseFailures(t *testing.T) {
	f := newFixture(t)
	m := f.morningAt(t)
	f.app.DB.Close()
	if _, ok := m.reviewMorning()().(reviewFailedMsg); !ok {
		t.Error("the review didn't fail")
	}
	if _, ok := m.startDay(3, "", nil, "call the bank")().(startFailedMsg); !ok {
		t.Error("saving the dump didn't fail")
	}
}

func TestKeysBeforeTheFirstLoadDoNothing(t *testing.T) {
	f := newFixture(t)
	m := New(f.app, "http://127.0.0.1:7777")
	t.Cleanup(m.unsubscribe)
	if m = send(m, key("enter")); m.morning.step != stepNone {
		t.Errorf("enter before the first load began the ritual")
	}
}

func TestAReviewLandingAfterTheDayStartedElsewhereIsDropped(t *testing.T) {
	f := newFixture(t)
	m := send(f.model(t, 80, 30), key("enter"))
	review := m.reviewMorning()()
	f.start(t)
	m = send(m, snapshotMsg{f.load(t)}, review)
	if m.morning.step != stepNone {
		t.Errorf("step = %d, want Today", m.morning.step)
	}
}

func TestARetryAfterAFailedStartDoesNotSaveTheDumpTwice(t *testing.T) {
	for _, saved := range []bool{true, false} {
		f := newFixture(t)
		m := f.morningAt(t)
		m.morning.dump.SetValue("call the bank")
		m = send(m, startFailedMsg{err: errors.New("db gone"), dumpSaved: saved})
		if kept := m.morning.dump.Value() != ""; kept == saved {
			t.Errorf("dump saved %v, kept %v", saved, kept)
		}
	}
}

func TestEveryPaceShowsItsWholeName(t *testing.T) {
	f := newFixture(t)
	m := send(f.morningAt(t), key("esc"))
	for range 4 {
		m = send(m, key("right"))
	}
	if v := m.View(); !strings.Contains(v, "go hard") || !strings.Contains(v, "●●●●●") {
		t.Errorf("pace 5:\n%s", v)
	}
}

func TestTheMoodScrollsInsideTheCard(t *testing.T) {
	f := newFixture(t)
	m := send(f.morningAt(t), key("esc"))
	for _, r := range strings.Repeat("so very tired ", 6) + "END" {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if v := m.View(); !strings.Contains(v, "END") {
		t.Errorf("the end of the mood is cut off:\n%s", v)
	}
}

func TestALongReconcileScrollsToTheCursor(t *testing.T) {
	f := newFixture(t)
	var titles []string
	for c := 'a'; c <= 'n'; c++ {
		titles = append(titles, "task "+string(c))
	}
	f.yesterday(t, titles...)
	m := f.morningAt(t)
	for range titles {
		m = send(m, key("down"))
	}
	v := m.View()
	for _, want := range []string{"task n", "enter next"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
}
