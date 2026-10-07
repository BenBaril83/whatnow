package planner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

// Triage is what the user decided for one unfinished task. A push with no Until goes to tomorrow.
type Triage struct {
	TaskID int64        `json:"task_id"`
	Action Action       `json:"action"`
	Until  *domain.Date `json:"until"`
}

const summaryTokens = 2048

// Close wraps up a day. It applies the triage as one user batch, wraps the day with the note, and
// has the light model write the day's summary. It returns the batch, or 0 when the triage changed
// nothing. A summary that fails is logged and left empty: it's context for later, never worth
// failing a wrap-up over. It writes no reflection; WrapUp does, since only today's goodnight is
// seen.
func (p *Planner) Close(ctx context.Context, day domain.Day, triage []Triage, note string) (int64, domain.Day, error) {
	if day.WrappedAt != nil {
		return 0, day, fmt.Errorf("%w: %s is already wrapped", service.ErrConflict, day.Date)
	}
	if err := p.checkTriage(triage); err != nil {
		return 0, day, err
	}
	done, unfinished, err := p.outcome(ctx, day)
	if err != nil {
		return 0, day, err
	}
	if err := checkStale(triage, unfinished); err != nil {
		return 0, day, err
	}
	batch, err := p.applyTriage(ctx, triage)
	if err != nil {
		return 0, day, err
	}
	wrapped, err := p.svc.WrapDay(ctx, day.ID, note)
	if err != nil {
		if batch != 0 {
			err = errors.Join(err, p.svc.Undo(ctx, batch))
		}
		return 0, day, err
	}
	// Published after the wrap, so the replan watcher sees a finished day and leaves it alone.
	if batch != 0 {
		acts, err := p.svc.BatchActivity(ctx, batch)
		if err != nil {
			return 0, wrapped, err
		}
		p.hub.PublishBatch(batch, stream.SourceTriage, acts)
	}

	// The day is wrapped now, so the summary is written even if the user has already left.
	// modelTimeout still bounds it.
	ctx = context.WithoutCancel(ctx)
	if summary, err := p.summarize(ctx, wrapped, done, unfinished, triage); err != nil {
		fallbackNote(err)
	} else if err := p.svc.SetDaySummary(ctx, wrapped.ID, summary); err != nil {
		return batch, wrapped, err
	} else {
		wrapped.Summary = summary
	}
	return batch, wrapped, nil
}

// WrapUp closes today and writes the user's reflection from the summary. It returns ErrNoDay if
// today hasn't started. A reflection that fails is logged and left empty, like the summary.
func (p *Planner) WrapUp(ctx context.Context, triage []Triage, note string) (int64, domain.Day, error) {
	day, err := p.svc.GetDay(ctx, p.Date())
	if errors.Is(err, service.ErrNotFound) {
		return 0, domain.Day{}, ErrNoDay
	}
	if err != nil {
		return 0, domain.Day{}, err
	}
	// Held from before the summary is saved, so a lazy fill can't start a second reflection.
	p.fills.hold(day.ID)
	defer p.fills.release(day.ID, false)
	batch, wrapped, err := p.Close(ctx, day, triage, note)
	if err != nil || wrapped.Summary == "" {
		return batch, wrapped, err
	}
	// Written even if the user has left, like the summary.
	if r, err := p.writeReflection(context.WithoutCancel(ctx), wrapped); err != nil {
		fallbackNote(err)
	} else {
		wrapped.Reflection = r
	}
	return batch, wrapped, nil
}

func (p *Planner) checkTriage(triage []Triage) error {
	seen := map[int64]bool{}
	for _, t := range triage {
		switch {
		case !t.Action.Valid():
			return fmt.Errorf("%w: task %d has action %q", service.ErrInvalid, t.TaskID, t.Action)
		case seen[t.TaskID]:
			return fmt.Errorf("%w: task %d is triaged twice", service.ErrInvalid, t.TaskID)
		case t.Until != nil && !t.Until.Valid():
			return fmt.Errorf("%w: task %d has until %q", service.ErrInvalid, t.TaskID, *t.Until)
		}
		seen[t.TaskID] = true
	}
	return nil
}

// checkStale rejects triage for a task that isn't unfinished, such as one finished in another
// tab since the review loaded. Applying it would turn a completion into a drop.
func checkStale(triage []Triage, unfinished []domain.Task) error {
	open := map[int64]bool{}
	for _, t := range unfinished {
		open[t.ID] = true
	}
	var stale []string
	for _, t := range triage {
		if !open[t.TaskID] {
			stale = append(stale, fmt.Sprintf("#%d", t.TaskID))
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("%w: no longer unfinished: %s; reload the review", service.ErrConflict, strings.Join(stale, ", "))
	}
	return nil
}

// applyTriage writes every push and drop in one user batch. If any write fails, the batch is
// undone, so the triage applies whole or not at all.
func (p *Planner) applyTriage(ctx context.Context, triage []Triage) (int64, error) {
	var batch int64
	for _, t := range triage {
		if t.Action == ActionRollOver {
			continue
		}
		var err error
		if batch == 0 {
			if batch, err = p.svc.NewBatch(ctx, domain.ActorUser); err != nil {
				return 0, err
			}
		}
		switch t.Action {
		case ActionPush:
			until := p.tomorrow()
			if t.Until != nil {
				until = *t.Until
			}
			_, err = p.svc.PushTask(ctx, batch, t.TaskID, until)
		case ActionDrop:
			_, err = p.svc.SetTaskStatus(ctx, batch, t.TaskID, domain.StatusDropped)
		}
		if err != nil {
			return 0, errors.Join(err, p.svc.Undo(ctx, batch))
		}
	}
	return batch, nil
}

func (p *Planner) tomorrow() domain.Date {
	return domain.Date(p.now().In(p.loc).AddDate(0, 0, 1).Format(time.DateOnly))
}

// summarize asks the light model for a few sentences about the day, for later context.
func (p *Planner) summarize(ctx context.Context, day domain.Day, done, unfinished []domain.Task, triage []Triage) (string, error) {
	model, cfg, _, err := p.client(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, modelTimeout)
	defer cancel()

	var b strings.Builder
	date, _ := time.Parse(time.DateOnly, string(day.Date))
	fmt.Fprintf(&b, "Day: %s. Pace: %q, %d of 5.", date.Format("Monday 2006-01-02"), day.Pace.Label(), day.Pace)
	if day.MoodNote != "" {
		fmt.Fprintf(&b, " On their mind that morning: %q.", day.MoodNote)
	}
	start, _ := time.ParseInLocation(time.DateOnly, string(day.Date), p.loc)
	events, err := p.svc.ListEvents(ctx, start, start.AddDate(0, 0, 1))
	if err != nil {
		return "", err
	}
	b.WriteString("\nEvents:")
	writeTitles(&b, len(events), func(i int) string { return events[i].Title })
	b.WriteString("\nDone:")
	writeTitles(&b, len(done), func(i int) string { return done[i].Title })
	actions := map[int64]Triage{}
	for _, t := range triage {
		actions[t.TaskID] = t
	}
	b.WriteString("\nLeft unfinished:")
	writeTitles(&b, len(unfinished), func(i int) string {
		t := unfinished[i]
		switch a := actions[t.ID]; a.Action {
		case ActionPush:
			until := p.tomorrow()
			if a.Until != nil {
				until = *a.Until
			}
			return fmt.Sprintf("%s (pushed to %s)", t.Title, until)
		case ActionDrop:
			return t.Title + " (dropped)"
		}
		return t.Title + " (rolled over)"
	})
	if day.TomorrowNote != "" {
		fmt.Fprintf(&b, "\nTheir note for next time: %q.", day.TomorrowNote)
	}

	reply, err := model.Complete(ctx, llm.Request{
		Model:     cfg.LightModel,
		System:    []llm.Block{{Text: summaryInstructions, Cache: true}},
		User:      b.String(),
		MaxTokens: summaryTokens,
	})
	return strings.TrimSpace(reply), err
}

func writeTitles(b *strings.Builder, n int, title func(int) string) {
	if n == 0 {
		b.WriteString(" none.")
		return
	}
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(';')
		}
		fmt.Fprintf(b, " %s", title(i))
	}
	b.WriteByte('.')
}
