package planner

import (
	"context"
	"errors"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

// Today is the state of the day for the home screen. Day and Plan are nil until the day starts.
type Today struct {
	Date domain.Date  `json:"date"`
	Day  *domain.Day  `json:"day"`
	Plan *domain.Plan `json:"plan"`
	// Unplanned are eligible tasks the plan leaves out, in fallback order.
	Unplanned []int64 `json:"unplanned"`
	// Blocked are eligible tasks still waiting on an open task.
	Blocked []int64 `json:"blocked"`
}

func (p *Planner) Today(ctx context.Context) (Today, error) {
	st, err := p.gather(ctx, false)
	if err != nil {
		return Today{}, err
	}
	t := Today{Date: st.facts.Today, Day: st.day, Plan: st.plan, Unplanned: []int64{}, Blocked: []int64{}}
	planned := map[int64]bool{}
	if st.plan != nil {
		for _, e := range st.plan.Entries {
			planned[e.TaskID] = true
		}
	}
	for _, tf := range st.facts.Tasks {
		if !planned[tf.Task.ID] {
			t.Unplanned = append(t.Unplanned, tf.Task.ID)
		}
		if len(tf.BlockedBy) > 0 {
			t.Blocked = append(t.Blocked, tf.Task.ID)
		}
	}
	return t, nil
}

// StartDay starts today at a pace and makes its first plan. First, if the last day was never
// wrapped up, it closes that day with the triage from the morning's reconcile; with no such day,
// the triage is ignored. If today started but its first plan was never saved (the request was
// cancelled, say), starting again makes that plan and keeps the pace already chosen. A day that
// has a plan can't start again.
func (p *Planner) StartDay(ctx context.Context, pace domain.Pace, moodNote string, triage []Triage) (domain.Day, domain.Plan, error) {
	last, err := p.unwrapped(ctx)
	if err != nil {
		return domain.Day{}, domain.Plan{}, err
	}
	if last != nil {
		if _, _, err := p.Close(ctx, *last, triage, ""); err != nil {
			return domain.Day{}, domain.Plan{}, err
		}
	}
	day, err := p.svc.StartDay(ctx, p.Date(), pace, moodNote)
	if started := err; errors.Is(started, service.ErrConflict) {
		if day, err = p.svc.GetDay(ctx, p.Date()); err == nil {
			if _, err = p.svc.LatestPlan(ctx, day.ID); err == nil {
				err = started
			} else if errors.Is(err, service.ErrNotFound) {
				err = nil
			}
		}
	}
	if err != nil {
		return domain.Day{}, domain.Plan{}, err
	}
	plan, err := p.Replan(ctx, domain.TriggerMorning, "")
	return day, plan, err
}
