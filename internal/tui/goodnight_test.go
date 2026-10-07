package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm/fake"
	"github.com/jameynakama/whatnow/internal/service"
)

// wrapUp finishes "buy milk", sits through a 9:00 standup, and wraps the day with a note. The fake
// writes the summary and the reflection.
func (f *fixture) wrapUp(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	milk := f.task(t, "buy milk")
	f.task(t, "call mom")
	f.start(t)
	batch, _ := f.app.Service.NewBatch(ctx, domain.ActorUser)
	if _, err := f.app.Service.SetTaskStatus(ctx, batch, milk.ID, domain.StatusDone); err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	if _, err := f.app.Service.CreateEvent(ctx, batch, service.EventFields{Title: "standup", StartsAt: end.Add(-30 * time.Minute), EndsAt: &end}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.app.Planner.WrapUp(ctx, nil, "call the plumber first"); err != nil {
		t.Fatal(err)
	}
}

func TestGoodnightView(t *testing.T) {
	f := newFixture(t)
	f.wrapUp(t)
	golden(t, "goodnight-80", f.model(t, 80, 30).View())
}

func TestGoodnightShowsTheReflectionNotTheSummary(t *testing.T) {
	f := newFixture(t)
	f.wrapUp(t)
	v := f.model(t, 120, 30).View()
	for _, want := range []string{"Goodnight", "✓ 1 thing done, 1 meeting survived", fake.Reflection, "call the plumber first", "executive function"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, fake.Sentence) || strings.Contains(v, "call mom") {
		t.Errorf("the summary or the list shows after wrap-up:\n%s", v)
	}
}

func TestGoodnightHasNoWrapUpBanner(t *testing.T) {
	f := newFixture(t)
	f.wrapUp(t)
	f.clock.t = time.Date(2026, 10, 6, 17, 0, 0, 0, time.UTC)
	if v := f.model(t, 80, 30).View(); strings.Contains(v, "Wrap up in the browser") {
		t.Errorf("wrap banner after the day is wrapped:\n%s", v)
	}
}

func TestGoodnightTurnsOffTheListKeys(t *testing.T) {
	f := newFixture(t)
	f.wrapUp(t)
	m := f.model(t, 80, 30)
	for _, k := range []string{" ", "p", "x", "w", "n", "j"} {
		if _, cmd := m.Update(key(k)); cmd != nil {
			t.Errorf("%q made a command after wrap-up", k)
		}
	}
	if got := send(m, key("a")); got.mode != modeAdd {
		t.Error("a doesn't add after wrap-up")
	}
	if v := m.View(); strings.Contains(v, "space done") {
		t.Errorf("the key hint still offers list keys:\n%s", v)
	}
}

func TestGoodnightWithoutAReflectionYet(t *testing.T) {
	f := newFixture(t)
	f.wrapUp(t)
	s := f.load(t)
	day := *s.today.Day
	day.Reflection = ""
	s.today.Day = &day
	v := send(f.model(t, 80, 30), snapshotMsg{s}).View()
	if !strings.Contains(v, "Goodnight") || strings.Contains(v, fake.Reflection) {
		t.Errorf("view:\n%s", v)
	}
}

func TestWinLine(t *testing.T) {
	for _, c := range []struct {
		done, meetings int
		want           string
	}{
		{0, 0, "Nothing checked off yet"},
		{1, 0, "✓ 1 thing done"},
		{2, 1, "✓ 2 things done, 1 meeting survived"},
		{0, 2, "✓ 2 meetings survived"},
	} {
		if got := winLine(c.done, c.meetings); got != c.want {
			t.Errorf("winLine(%d, %d) = %q, want %q", c.done, c.meetings, got, c.want)
		}
	}
}

func TestALongReflectionWrapsInsteadOfBeingCut(t *testing.T) {
	f := newFixture(t)
	f.wrapUp(t)
	s := f.load(t)
	day := *s.today.Day
	day.Reflection = strings.Repeat("You did a lot today. ", 8) + "Rest now."
	s.today.Day = &day
	if v := send(f.model(t, 60, 30), snapshotMsg{s}).View(); !strings.Contains(v, "Rest now.") {
		t.Errorf("the end of the reflection is cut off:\n%s", v)
	}
}
