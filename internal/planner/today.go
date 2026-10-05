package planner

import (
	"context"

	"github.com/jameynakama/whatnow/internal/domain"
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

// StartDay starts today at a pace and makes its first plan.
func (p *Planner) StartDay(ctx context.Context, pace domain.Pace, moodNote string) (domain.Day, domain.Plan, error) {
	day, err := p.svc.StartDay(ctx, p.Date(), pace, moodNote)
	if err != nil {
		return domain.Day{}, domain.Plan{}, err
	}
	plan, err := p.Replan(ctx, domain.TriggerMorning, "")
	return day, plan, err
}
