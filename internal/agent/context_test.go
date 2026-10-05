package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
)

func (f *fixture) said(t *testing.T, role domain.Role, text, errText string) domain.Message {
	t.Helper()
	m, err := f.svc.AddMessage(context.Background(), domain.Message{Date: today, Role: role, Text: text, Error: errText})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestHistoryIsTodaysTextWithoutFailedReplies(t *testing.T) {
	f := newFixture(t, text("Sure."))
	if _, err := f.svc.AddMessage(context.Background(), domain.Message{Date: "2026-10-04", Role: domain.RoleUser, Text: "yesterday"}); err != nil {
		t.Fatal(err)
	}
	f.said(t, domain.RoleUser, "hi", "")
	f.said(t, domain.RoleAssistant, "Morning!", "")
	f.said(t, domain.RoleUser, "plan please", "")
	f.said(t, domain.RoleAssistant, "I couldn't reach the AI provider.", "openai: 500")

	f.send(t, "try again")

	want := []llm.Message{
		{Role: llm.RoleUser, Text: "hi"},
		{Role: llm.RoleAssistant, Text: "Morning!"},
		{Role: llm.RoleUser, Text: "plan please\n\ntry again"},
	}
	if diff := cmp.Diff(want, f.model.chats[0].Messages); diff != "" {
		t.Errorf("messages (-want +got):\n%s", diff)
	}
}

func TestOldTurnsFoldIntoARunningSummary(t *testing.T) {
	f := newFixture(t, text("Got it."), text("Still here."))
	long := strings.Repeat("x", 5000)
	var first domain.Message
	for i := range 3 {
		m := f.said(t, domain.RoleUser, long, "")
		if i == 0 {
			first = m
		}
		f.said(t, domain.RoleAssistant, long, "")
	}
	f.model.completions = []any{"They asked about x three times."}

	f.send(t, "and now?")

	sent := f.model.chats[0]
	if n := len(sent.Messages); n != 3 || sent.Messages[0].Role != llm.RoleUser || sent.Messages[2].Text != "and now?" {
		t.Errorf("sent %d messages: %+v", n, sent.Messages)
	}
	if !strings.Contains(system(sent.System), "Earlier in today's chat: They asked about x three times.") {
		t.Error("the summary is missing from the context")
	}
	fold, _ := f.svc.ChatFold(context.Background(), today)
	if fold.Summary != "They asked about x three times." || fold.ThroughID != first.ID+3 {
		t.Errorf("fold = %+v", fold)
	}
	folded := f.model.completes[0]
	if folded.Model != "claude-haiku-4-5" || strings.Count(folded.User, long) != 4 {
		t.Errorf("fold request: model %s, %d long messages", folded.Model, strings.Count(folded.User, long))
	}

	// The next turn is under budget again, so it doesn't fold.
	f.send(t, "ok")
	if len(f.model.completes) != 1 {
		t.Errorf("folded again: %d completions", len(f.model.completes))
	}
}

func TestAFailedFoldSendsTheWholeChat(t *testing.T) {
	f := newFixture(t, text("Got it."))
	long := strings.Repeat("x", 9000)
	for range 2 {
		f.said(t, domain.RoleUser, long, "")
		f.said(t, domain.RoleAssistant, long, "")
	}
	f.model.completions = []any{errors.New("openai: 500")}

	reply := f.send(t, "and now?")

	if reply.Error != "" || len(f.model.chats[0].Messages) != 5 {
		t.Errorf("reply %+v after sending %d messages", reply, len(f.model.chats[0].Messages))
	}
	if fold, _ := f.svc.ChatFold(context.Background(), today); fold.ThroughID != 0 {
		t.Errorf("fold = %+v", fold)
	}
}

func TestTheContextCarriesTheWeekAndTheDay(t *testing.T) {
	f := newFixture(t, text("Hi."))
	ctx := context.Background()
	for _, d := range []struct {
		date, summary, note string
	}{
		{"2026-09-27", "Too long ago.", ""},
		{"2026-10-02", "Shipped the auth fix.", "start with the RFC"},
		{"2026-10-03", "", ""},
	} {
		day, err := f.svc.StartDay(ctx, domain.Date(d.date), 3, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.WrapDay(ctx, day.ID, d.note); err != nil {
			t.Fatal(err)
		}
		f.svc.SetDaySummary(ctx, day.ID, d.summary)
	}
	if _, err := f.svc.StartDay(ctx, today, 2, "tired"); err != nil {
		t.Fatal(err)
	}
	f.task(t, service.TaskFields{Title: "Write the RFC"})
	batch, _ := f.svc.NewBatch(ctx, domain.ActorUser)
	end := time.Date(2026, 10, 7, 16, 0, 0, 0, loc)
	for _, e := range []service.EventFields{
		{Title: "Dentist", StartsAt: time.Date(2026, 10, 7, 15, 0, 0, 0, loc), EndsAt: &end},
		{Title: "Next month", StartsAt: time.Date(2026, 11, 7, 15, 0, 0, 0, loc)},
	} {
		if _, err := f.svc.CreateEvent(ctx, batch, e); err != nil {
			t.Fatal(err)
		}
	}

	f.send(t, "hi")

	got := system(f.model.chats[0].System)
	for _, want := range []string{
		"dry humor",
		`- 2026-10-02: Shipped the auth fix. Their note for the next day: "start with the RFC"`,
		`Pace today: 2 of 5, "gentle".`,
		`On their mind: "tired"`,
		"#1 Write the RFC",
		"[1] Wed 2026-10-07 15:00-16:00 Dentist",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context lacks %q", want)
		}
	}
	for _, unwanted := range []string{"Too long ago", "2026-10-03", "Next month", "Last day they used this"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("context has %q", unwanted)
		}
	}
	if req := f.model.chats[0]; req.Model != "claude-sonnet-5-5" || !req.Fallback {
		t.Errorf("request: model %s, fallback %v", req.Model, req.Fallback)
	}
}
