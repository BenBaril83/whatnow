package planner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	js "github.com/jameynakama/whatnow/internal/jsonschema"
	"github.com/jameynakama/whatnow/internal/service"
)

// Action is what happens to an unfinished task at the end of a day.
type Action string

const (
	ActionRollOver Action = "roll_over"
	ActionPush     Action = "push"
	ActionDrop     Action = "drop"
)

func (a Action) Valid() bool { return a == ActionRollOver || a == ActionPush || a == ActionDrop }

// Suggestion is the model's advice for one unfinished task. Until is set only for a push.
type Suggestion struct {
	TaskID int64       `json:"task_id"`
	Reason string      `json:"reason"`
	Action Action      `json:"action"`
	Until  domain.Date `json:"until"`
}

// Review is what wrap-up and the morning's reconcile show for a day: what got done, what's
// unfinished, and a suggestion for each unfinished task. Fallback means code made the suggestions.
type Review struct {
	Day         domain.Day    `json:"day"`
	Done        []domain.Task `json:"done"`
	Unfinished  []domain.Task `json:"unfinished"`
	Suggestions []Suggestion  `json:"suggestions"`
	Fallback    bool          `json:"fallback"`
}

var suggestSchema = js.Object(
	js.Prop("suggestions", js.Array(js.Object(
		js.Prop("task_id", js.Integer("The task's number, from #<id>.")),
		js.Prop("reason", js.String("One short line for the user on why.")),
		js.Prop("action", js.Enum("roll_over, push, or drop.", "roll_over", "push", "drop")),
		js.Prop("until", js.String("For a push, the date to push to as YYYY-MM-DD, after today. Otherwise empty.")),
	))),
)

type suggestReply struct {
	Suggestions []Suggestion `json:"suggestions"`
}

// WrapReview reviews today, for wrap-up. It returns ErrNoDay if today hasn't started.
func (p *Planner) WrapReview(ctx context.Context) (Review, error) {
	day, err := p.svc.GetDay(ctx, p.Date())
	if errors.Is(err, service.ErrNotFound) {
		return Review{}, ErrNoDay
	}
	if err != nil {
		return Review{}, err
	}
	return p.review(ctx, day)
}

// MorningReview reviews the last day before today if it was never wrapped up, so the morning can
// reconcile it. It returns nil when there is nothing to reconcile. However many days were
// skipped, only the last one started is reviewed.
func (p *Planner) MorningReview(ctx context.Context) (*Review, error) {
	day, err := p.unwrapped(ctx)
	if day == nil || err != nil {
		return nil, err
	}
	r, err := p.review(ctx, *day)
	return &r, err
}

// unwrapped returns the last day before today if it was never wrapped up, or nil.
func (p *Planner) unwrapped(ctx context.Context) (*domain.Day, error) {
	day, err := p.svc.LastDayBefore(ctx, p.Date())
	if errors.Is(err, service.ErrNotFound) || (err == nil && day.WrappedAt != nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &day, nil
}

func (p *Planner) review(ctx context.Context, day domain.Day) (Review, error) {
	r := Review{Day: day, Suggestions: []Suggestion{}}
	var err error
	if r.Done, r.Unfinished, err = p.outcome(ctx, day); err != nil {
		return r, err
	}
	if len(r.Unfinished) == 0 {
		return r, nil
	}
	st, err := p.gather(ctx, true)
	if err != nil {
		return r, err
	}
	reply, err := p.askSuggestions(ctx, st, r)
	if err != nil {
		fallbackNote(err)
		reply.Suggestions, r.Fallback = nil, true
	}
	given := map[int64]Suggestion{}
	for _, s := range reply.Suggestions {
		given[s.TaskID] = s
	}
	for _, t := range r.Unfinished {
		s, ok := given[t.ID]
		if !ok {
			s = Suggestion{TaskID: t.ID, Action: ActionRollOver}
		}
		r.Suggestions = append(r.Suggestions, s)
	}
	return r, nil
}

// outcome returns what got done on a day and what's still unfinished from its plan: open,
// not deleted, and not already pushed past today.
func (p *Planner) outcome(ctx context.Context, day domain.Day) (done, unfinished []domain.Task, err error) {
	start, err := time.ParseInLocation(time.DateOnly, string(day.Date), p.loc)
	if err != nil {
		return nil, nil, err
	}
	if done, err = p.svc.DoneBetween(ctx, start, start.AddDate(0, 0, 1)); err != nil {
		return nil, nil, err
	}
	unfinished = []domain.Task{}
	plan, err := p.svc.LatestPlan(ctx, day.ID)
	if errors.Is(err, service.ErrNotFound) {
		return done, unfinished, nil
	}
	if err != nil {
		return nil, nil, err
	}
	today := p.Date()
	for _, e := range plan.Entries {
		t, err := p.svc.GetTask(ctx, e.TaskID)
		if errors.Is(err, service.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if facts.Eligible(t, today) {
			unfinished = append(unfinished, t)
		}
	}
	return done, unfinished, nil
}

func (p *Planner) askSuggestions(ctx context.Context, st state, r Review) (suggestReply, error) {
	if st.model == nil {
		return suggestReply{}, st.modelErr
	}
	ctx, cancel := context.WithTimeout(ctx, modelTimeout)
	defer cancel()
	var user strings.Builder
	fmt.Fprintf(&user, "I'm wrapping up %s.", r.Day.Date)
	if len(r.Done) > 0 {
		user.WriteString(" Done:")
		for _, t := range r.Done {
			fmt.Fprintf(&user, " #%d %q.", t.ID, t.Title)
		}
	}
	user.WriteString(" Suggest what to do with each unfinished task:")
	for _, t := range r.Unfinished {
		fmt.Fprintf(&user, " #%d", t.ID)
	}
	req := st.request(reviewInstructions, user.String(), suggestSchema, "suggestions")
	return ask(ctx, st.model, req, func(s suggestReply) error {
		return checkSuggestions(s, r.Unfinished, st.facts.Today)
	})
}

func checkSuggestions(r suggestReply, unfinished []domain.Task, today domain.Date) error {
	open := map[int64]bool{}
	for _, t := range unfinished {
		open[t.ID] = true
	}
	var errs []error
	seen := map[int64]bool{}
	for _, s := range r.Suggestions {
		switch {
		case !open[s.TaskID]:
			errs = append(errs, fmt.Errorf("#%d is not an unfinished task", s.TaskID))
		case seen[s.TaskID]:
			errs = append(errs, fmt.Errorf("#%d appears more than once", s.TaskID))
		case !s.Action.Valid():
			errs = append(errs, fmt.Errorf("#%d has action %q", s.TaskID, s.Action))
		case s.Action == ActionPush && (!s.Until.Valid() || s.Until <= today):
			errs = append(errs, fmt.Errorf("#%d is pushed to %q, which is not a date after %s", s.TaskID, s.Until, today))
		case s.Action != ActionPush && s.Until != "":
			errs = append(errs, fmt.Errorf("#%d has a date but isn't a push", s.TaskID))
		}
		seen[s.TaskID] = true
	}
	return errors.Join(errs...)
}
