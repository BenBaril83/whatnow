package tui

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
)

// updating reports the -update flag, which teatest's golden package defines for the test binary.
func updating() bool {
	f := flag.Lookup("update")
	return f != nil && f.Value.String() == "true"
}

// golden compares got with testdata/<name>.golden. Run with -update to rewrite it after checking
// the change by eye.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if updating() {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s (-want +got):\n%s", name, diff)
	}
}

// model is a TUI sized to width × height with a fresh snapshot loaded.
func (f *fixture) model(t *testing.T, width, height int) Model {
	t.Helper()
	m := New(f.app, "http://127.0.0.1:7777")
	t.Cleanup(m.unsubscribe)
	return send(m, tea.WindowSizeMsg{Width: width, Height: height}, snapshotMsg{f.load(t)})
}

// send feeds msgs to m in turn and returns the result, dropping commands.
func send(m Model, msgs ...tea.Msg) Model {
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func (f *fixture) today(t *testing.T) {
	t.Helper()
	f.task(t, "buy milk")
	f.task(t, "call mom")
	f.task(t, "fix the gutter")
	f.start(t)
	f.task(t, "renew passport")
}

func TestTodayView(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 30}} {
		f := newFixture(t)
		f.today(t)
		golden(t, "today-"+strconv.Itoa(size.w), f.model(t, size.w, size.h).View())
	}
}

func TestNoDayViewOffersTheMorning(t *testing.T) {
	f := newFixture(t)
	golden(t, "no-day", f.model(t, 80, 24).View())
}

func TestTheDayRollsOverAtMidnight(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	f.clock.t = f.clock.t.Add(24 * time.Hour)
	if v := f.model(t, 80, 24).View(); !strings.Contains(v, "to start your day") {
		t.Errorf("after midnight the view is:\n%s", v)
	}
}

func TestAWrapBannerShowsWithinAnHourOfTheEndOfDay(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	f.clock.t = time.Date(2026, 10, 6, 16, 45, 0, 0, time.UTC)
	if v := f.model(t, 80, 24).View(); !strings.Contains(v, "Wrap up in the browser") {
		t.Errorf("no wrap banner at 16:45:\n%s", v)
	}
}

func TestAFailedReloadKeepsTheListAndMarksItStale(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	v := send(f.model(t, 80, 24), loadFailedMsg{errors.New("disk")}).View()
	if !strings.Contains(v, "stale") || !strings.Contains(v, "buy milk") {
		t.Errorf("view after a failed reload:\n%s", v)
	}
}

func TestAFailedFirstLoadSaysWhy(t *testing.T) {
	f := newFixture(t)
	m := New(f.app, "")
	t.Cleanup(m.unsubscribe)
	v := send(m, tea.WindowSizeMsg{Width: 80, Height: 24}, loadFailedMsg{errors.New("disk")}).View()
	if !strings.Contains(v, "disk") {
		t.Errorf("view = %q", v)
	}
}

func TestTheCursorStaysOnItsTaskWhenTheListReorders(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 80, 24)
	m = send(m, key("j"))
	held := m.cursor

	s := f.load(t)
	entries := s.today.Plan.Entries
	entries[0], entries[1] = entries[1], entries[0]
	entries[0].Tier, entries[1].Tier = entries[1].Tier, entries[0].Tier
	if got := send(m, snapshotMsg{s}).cursor; got != held {
		t.Errorf("cursor moved from %d to %d", held, got)
	}
}

func TestTheCursorTakesThePlaceOfATaskThatLeaves(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := send(f.model(t, 80, 24), key("j"))
	s := f.load(t)
	gone := m.cursor
	next := s.order()[2]
	task := s.tasks[gone]
	task.Status = domain.StatusDone
	s.tasks[gone] = task
	if got := send(m, snapshotMsg{s}).cursor; got != next {
		t.Errorf("cursor = %d, want %d", got, next)
	}
}

func TestALongTitleIsCutNotWrapped(t *testing.T) {
	f := newFixture(t)
	f.task(t, strings.Repeat("very long title ", 10))
	f.start(t)
	v := f.model(t, 60, 20).View()
	for _, line := range strings.Split(v, "\n") {
		if w := lipgloss.Width(line); w > 60 {
			t.Errorf("line is %d wide: %q", w, line)
		}
	}
	if !strings.Contains(v, "…") {
		t.Errorf("no ellipsis:\n%s", v)
	}
}

func TestATinyTerminalDoesNotPanic(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	_ = f.model(t, 20, 6).View()
	m := New(f.app, "")
	t.Cleanup(m.unsubscribe)
	if v := m.View(); v != "" {
		t.Errorf("view before the first size = %q", v)
	}
}

func TestHelpListsTheKeysAndAnyKeyCloses(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := send(f.model(t, 80, 24), key("?"))
	if v := m.View(); !strings.Contains(v, "what now") || !strings.Contains(v, "push to tomorrow") {
		t.Errorf("help:\n%s", v)
	}
	if send(m, key("z")).mode != modeList {
		t.Error("a key didn't close help")
	}
}

func TestMovingStopsAtTheEnds(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 80, 24)
	first := m.cursor
	if got := send(m, key("k")).cursor; got != first {
		t.Errorf("up from the top moved to %d", got)
	}
	m = send(m, key("j"), key("j"), key("j"), key("j"), key("j"))
	if got, last := m.cursor, f.load(t).order()[3]; got != last {
		t.Errorf("cursor = %d, want the last task %d", got, last)
	}
}

func TestListenStopsWhenTheSubscriptionEnds(t *testing.T) {
	f := newFixture(t)
	m := New(f.app, "")
	m.unsubscribe()
	if msg := m.listen()(); msg != nil {
		t.Errorf("listen after unsubscribe = %v", msg)
	}
}

func TestLoadReportsAFailure(t *testing.T) {
	f := newFixture(t)
	m := New(f.app, "")
	t.Cleanup(m.unsubscribe)
	f.app.DB.Close() // the fixture's Close closes it again, which database/sql allows
	if _, ok := m.load()().(loadFailedMsg); !ok {
		t.Error("load on a closed database didn't fail")
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestAWaitingTaskShowsHowManyItWaitsOn(t *testing.T) {
	f := newFixture(t)
	first, waiting := f.task(t, "buy milk"), f.task(t, "pour cereal")
	ctx := context.Background()
	batch, _ := f.app.Service.NewBatch(ctx, domain.ActorUser)
	if err := f.app.Service.AddBlocker(ctx, batch, domain.Blocker{TaskID: waiting.ID, BlockedByID: first.ID}); err != nil {
		t.Fatal(err)
	}
	f.start(t)
	if v := f.model(t, 80, 24).View(); !strings.Contains(v, "pour cereal  ⛓ 1") {
		t.Errorf("view:\n%s", v)
	}
}

func TestTheGoodMorningScreenTakesOnlyRefreshAndQuit(t *testing.T) {
	f := newFixture(t)
	m := f.model(t, 80, 24)
	if _, cmd := m.Update(key("j")); cmd != nil {
		t.Error("j did something before the day started")
	}
	if _, cmd := m.Update(key("r")); cmd == nil {
		t.Error("r didn't reload")
	}
	if _, cmd := m.Update(key("q")); cmd == nil || cmd() != tea.Quit() {
		t.Error("q didn't quit")
	}
}

func TestATickReloads(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 80, 24)
	f.task(t, "added elsewhere") // as another process would, without telling this one
	_, cmd := m.Update(tickMsg{})
	reload, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("a tick returned %T", cmd())
	}
	snap, ok := reload[0]().(snapshotMsg)
	if !ok {
		t.Fatal("the tick's first command didn't load")
	}
	if v := send(m, snap).View(); !strings.Contains(v, "added elsewhere") {
		t.Errorf("view after the tick:\n%s", v)
	}
}

func TestChatShowsTheNewestMessagesWhenItOverflows(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	s := f.load(t)
	for i := range 30 {
		s.chat = append(s.chat, domain.Message{Role: domain.RoleUser, Text: "message " + strconv.Itoa(i)})
	}
	v := send(f.model(t, 120, 30), snapshotMsg{s}).View()
	if !strings.Contains(v, "message 29") || strings.Contains(v, "message 0 ") {
		t.Errorf("view:\n%s", v)
	}
}

func TestAShortTerminalStillShowsTheCursorRow(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	if v := f.model(t, 80, 12).View(); !strings.Contains(v, "▸ ☐ buy milk") {
		t.Errorf("the cursor row is out of view:\n%s", v)
	}
}

func TestNarrowingTheTerminalBringsTheListBack(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := send(f.model(t, 120, 30), tea.WindowSizeMsg{Width: 70, Height: 30})
	if v := m.View(); !strings.Contains(v, "buy milk") {
		t.Errorf("the list is hidden at 70 columns:\n%s", v)
	}
}

func TestLongInputScrollsToKeepTheEndInView(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	long := strings.Repeat("words ", 20) + "TAIL"
	for _, c := range []struct {
		name string
		keys string
	}{{"chat", "c"}, {"add", "a"}} {
		m := send(f.model(t, 100, 30), key(c.keys))
		for _, r := range long {
			m = send(m, key(string(r)))
		}
		if v := m.View(); !strings.Contains(v, "TAIL") {
			t.Errorf("%s: the end of the input is cut off:\n%s", c.name, v)
		}
	}
}
