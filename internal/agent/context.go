package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/prompt"
)

const (
	// historyBudget is how much of today's chat, in bytes of text, is sent before the oldest turns are
	// folded into the running summary. Folding keeps about half of it.
	historyBudget = 24000
	recentDays    = 7
	// eventDays is how far ahead the agent sees events, so it can move one later in the week.
	eventDays     = 7
	summaryTokens = 2048
)

// history is today's chat as the model sees it: the running summary of older turns, then the
// rest. Facts are never in it; they're rendered fresh in the system prompt each turn.
type history struct {
	fold     domain.ChatFold
	messages []llm.Message
}

// history reads today's chat, folding the oldest turns into the summary when it has outgrown its
// budget. If folding fails, the whole chat is sent unfolded: too long beats forgetting.
func (a *Agent) history(ctx context.Context, date domain.Date, sit planner.Situation) (history, error) {
	fold, err := a.svc.ChatFold(ctx, date)
	if err != nil {
		return history{}, err
	}
	all, err := a.svc.Messages(ctx, date)
	if err != nil {
		return history{}, err
	}
	var live []domain.Message
	size := 0
	for _, m := range all {
		// A reply that failed told the user so, but it isn't something the model said.
		if m.ID > fold.ThroughID && m.Error == "" {
			live = append(live, m)
			size += len(m.Text)
		}
	}
	if size > historyBudget {
		if cut := foldPoint(live); cut > 0 {
			if summary, err := a.fold(ctx, sit, fold.Summary, live[:cut]); err != nil {
				log.Printf("chat fold: %v", err)
			} else {
				fold = domain.ChatFold{Summary: summary, ThroughID: live[cut-1].ID}
				if err := a.svc.SetChatFold(ctx, date, fold); err != nil {
					return history{}, err
				}
				live = live[cut:]
			}
		}
	}
	return history{fold: fold, messages: toLLM(live)}, nil
}

// foldPoint returns how many of the oldest messages to fold so that the rest fits in half the
// budget. The rest always starts with a user message, and the newest one is never folded.
func foldPoint(msgs []domain.Message) int {
	size := 0
	for _, m := range msgs {
		size += len(m.Text)
	}
	for i, m := range msgs[:len(msgs)-1] {
		size -= len(m.Text)
		if next := msgs[i+1]; next.Role == domain.RoleUser && size <= historyBudget/2 {
			return i + 1
		}
	}
	return len(msgs) - 1
}

// toLLM converts stored messages, merging neighbors from the same sender: a failed reply leaves
// two user messages in a row.
func toLLM(msgs []domain.Message) []llm.Message {
	var out []llm.Message
	for _, m := range msgs {
		role := llm.RoleUser
		if m.Role == domain.RoleAssistant {
			role = llm.RoleAssistant
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Text = joinText(out[n-1].Text, m.Text)
			continue
		}
		out = append(out, llm.Message{Role: role, Text: m.Text})
	}
	return out
}

func (a *Agent) fold(ctx context.Context, sit planner.Situation, summary string, msgs []domain.Message) (string, error) {
	var b strings.Builder
	if summary != "" {
		fmt.Fprintf(&b, "The summary so far:\n%s\n\n", summary)
	}
	b.WriteString("The messages to fold in:\n")
	for _, m := range msgs {
		fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Text)
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return sit.Model.Complete(ctx, llm.Request{
		Model:     sit.Config.LightModel,
		System:    []llm.Block{{Text: foldInstructions}},
		User:      b.String(),
		MaxTokens: summaryTokens,
	})
}

// system builds the turn's system prompt. The context layer carries the last week's summaries,
// the running chat summary, the live state, and the week's events with their IDs.
func (a *Agent) system(ctx context.Context, date domain.Date, sit planner.Situation, fold domain.ChatFold) ([]llm.Block, error) {
	var b strings.Builder
	days, err := a.svc.DaysBetween(ctx, addDays(date, -recentDays), date)
	if err != nil {
		return nil, err
	}
	var recent []string
	for _, d := range days {
		if d.Summary == "" && d.TomorrowNote == "" {
			continue
		}
		line := fmt.Sprintf("- %s:", d.Date)
		if d.Summary != "" {
			line += " " + d.Summary
		}
		if d.TomorrowNote != "" {
			line += fmt.Sprintf(" Their note for the next day: %q", d.TomorrowNote)
		}
		recent = append(recent, line)
	}
	if len(recent) > 0 {
		fmt.Fprintf(&b, "The last few days:\n%s\n\n", strings.Join(recent, "\n"))
	}
	if fold.Summary != "" {
		fmt.Fprintf(&b, "Earlier in today's chat: %s\n\n", fold.Summary)
	}
	b.WriteString(sit.Context)

	now := a.now().In(a.loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, a.loc)
	events, err := a.svc.ListEvents(ctx, start, start.AddDate(0, 0, eventDays+1))
	if err != nil {
		return nil, err
	}
	b.WriteString("\nEvents this week, as [event id]:\n")
	if len(events) == 0 {
		b.WriteString("(none)\n")
	}
	for _, e := range events {
		when := e.StartsAt.In(a.loc).Format("Mon 2006-01-02 15:04")
		if e.EndsAt != nil {
			when += "-" + e.EndsAt.In(a.loc).Format("15:04")
		}
		fmt.Fprintf(&b, "[%d] %s %s\n", e.ID, when, e.Title)
	}
	return prompt.System(instructions, sit.Settings.Style, sit.Settings.AboutMe, b.String()), nil
}

func addDays(d domain.Date, n int) domain.Date {
	t, _ := time.Parse(time.DateOnly, string(d))
	return domain.Date(t.AddDate(0, 0, n).Format(time.DateOnly))
}
