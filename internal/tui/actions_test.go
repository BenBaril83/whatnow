package tui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/jameynakama/whatnow/internal/agent"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
)

// run starts the TUI in a test program, 120 × 30.
func (f *fixture) run(t *testing.T) *teatest.TestModel {
	t.Helper()
	m := New(f.app, "http://127.0.0.1:7777")
	t.Cleanup(m.unsubscribe)
	return teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 30))
}

func waitFor(t *testing.T, tm *teatest.TestModel, text string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte(text)) },
		teatest.WithDuration(5*time.Second))
}

// quit ends the program and returns its last model. It sends ctrl+c, which quits from any mode.
func quit(t *testing.T, tm *teatest.TestModel) Model {
	t.Helper()
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	return tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
}

func (f *fixture) status(t *testing.T, id int64) domain.Task {
	t.Helper()
	task, err := f.app.Service.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestSpaceMarksDoneAndUUndoesIt(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.start(t)
	tm := f.run(t)
	waitFor(t, tm, "buy milk")

	tm.Send(key(" "))
	waitFor(t, tm, "Done: buy milk · u to undo")
	if got := f.status(t, milk.ID).Status; got != domain.StatusDone {
		t.Errorf("status = %s, want done", got)
	}

	tm.Send(key("u"))
	waitFor(t, tm, "Undone")
	if got := f.status(t, milk.ID).Status; got != domain.StatusOpen {
		t.Errorf("status after undo = %s, want open", got)
	}
	quit(t, tm)
}

func TestPPushesToTomorrow(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.start(t)
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("p"))
	waitFor(t, tm, "Pushed to tomorrow: buy milk")
	if got := f.status(t, milk.ID).NotBefore; got == nil || *got != "2026-10-07" {
		t.Errorf("not_before = %v, want 2026-10-07", got)
	}
	quit(t, tm)
}

func (f *fixture) pace(t *testing.T) domain.Pace {
	t.Helper()
	today, err := f.app.Planner.Today(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return today.Day.Pace
}

func TestPlusAndMinusShiftThePaceAndUUndoesIt(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	tm := f.run(t)
	waitFor(t, tm, "steady")

	tm.Send(key("+"))
	waitFor(t, tm, "Pace: full steam · u to undo")
	if got := f.pace(t); got != 4 {
		t.Errorf("pace = %d, want 4", got)
	}
	tm.Send(key("u"))
	waitFor(t, tm, "Undone")
	tm.Send(key("-"))
	waitFor(t, tm, "Pace: gentle")
	if got := f.pace(t); got != 2 {
		t.Errorf("pace = %d, want 2", got)
	}
	quit(t, tm)
}

func TestThePaceStopsAtItsEnds(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	if _, _, err := f.app.Planner.StartDay(context.Background(), 5, "", nil); err != nil {
		t.Fatal(err)
	}
	tm := f.run(t)
	waitFor(t, tm, "go hard")
	tm.Send(key("="))
	waitFor(t, tm, "pace is already go hard")
	quit(t, tm)
	if got := f.pace(t); got != 5 {
		t.Errorf("pace = %d, want it left at 5", got)
	}
}

func TestXDrops(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.start(t)
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("x"))
	waitFor(t, tm, "Dropped: buy milk")
	if got := f.status(t, milk.ID).Status; got != domain.StatusDropped {
		t.Errorf("status = %s, want dropped", got)
	}
	quit(t, tm)
}

func TestAddCapturesAndCleanupSplitsIt(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("a"))
	tm.Type("pour cereal and wash bowl")
	tm.Send(key("enter"))
	// The fake's cleanup splits the card in two. Wait on the new task's row, not the toast, which
	// also says "wash bowl" and can arrive in the same read.
	waitFor(t, tm, "☐ wash bowl")
	quit(t, tm)
}

func TestEscCancelsAnAdd(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	m := send(f.model(t, 80, 24), key("a"), key("h"), key("esc"))
	if m.mode != modeList {
		t.Errorf("mode = %d after esc", m.mode)
	}
	if _, cmd := send(m, key("a")).Update(key("enter")); cmd != nil {
		t.Error("an empty add made a command")
	}
}

func TestUndoWithNothingToUndoSaysSo(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	if v := send(f.model(t, 80, 24), key("u")).View(); !strings.Contains(v, "Nothing to undo") {
		t.Errorf("view:\n%s", v)
	}
}

func TestActionsDoNothingOnAnEmptyList(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.start(t)
	ctx := context.Background()
	batch, _ := f.app.Service.NewBatch(ctx, domain.ActorUser)
	if _, err := f.app.Service.SetTaskStatus(ctx, batch, milk.ID, domain.StatusDone); err != nil {
		t.Fatal(err)
	}
	m := f.model(t, 80, 24)
	if !strings.Contains(m.View(), "All clear") {
		t.Errorf("view:\n%s", m.View())
	}
	for _, k := range []string{" ", "p", "x", "j"} {
		if _, cmd := m.Update(key(k)); cmd != nil {
			t.Errorf("%q on an empty list made a command", k)
		}
	}
}

func TestAFailedWriteShowsTheError(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 80, 24)
	msg := m.write("x", func(context.Context, int64) error { return errors.New("disk full") })()
	if v := send(m, msg).View(); !strings.Contains(v, "disk full") {
		t.Errorf("view:\n%s", v)
	}
}

func TestFriendlyErrors(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{planner.ErrNothingToDo, "Nothing is free to start right now."},
		{agent.ErrBusy, "A reply is still being written."},
		{errors.New("disk full"), "disk full"},
	} {
		if got := friendly(c.err); got != c.want {
			t.Errorf("friendly(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestAToastLeavesWhenItsTimeIsUp(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m, _ := f.model(t, 80, 24).show("hello", false)
	older := m.toast.id
	m, _ = m.show("again", false)
	if send(m, toastExpiredMsg{older}).toast.text != "again" {
		t.Error("an older toast's timer cleared a newer toast")
	}
	if send(m, toastExpiredMsg{m.toast.id}).toast.text != "" {
		t.Error("the toast stayed past its time")
	}
}

func TestUndoingSomethingChangedSinceShowsWhy(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.start(t)
	m := f.model(t, 80, 24)
	done, ok := m.act(" ", milk)().(wroteMsg)
	if !ok {
		t.Fatal("marking done failed")
	}
	if _, ok := m.act("x", milk)().(wroteMsg); !ok {
		t.Fatal("dropping failed")
	}
	if _, ok := m.undo(done.batch)().(failedMsg); !ok {
		t.Error("undoing a change that was changed again didn't fail")
	}
}

func TestAWriteOnAClosedDatabaseFails(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 80, 24)
	f.app.DB.Close()
	if _, ok := m.capture("buy milk")().(failedMsg); !ok {
		t.Error("a write on a closed database didn't fail")
	}
}
