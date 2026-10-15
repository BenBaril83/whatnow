package planner

import (
	"context"
	"fmt"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

// Move puts one task in a tier of today's plan by hand, or takes it out of the plan when tier is
// empty. It saves a new plan version and publishes it as plan.updated, with no model call. Its
// reason is what later replans are shown as a move by hand.
func (p *Planner) Move(ctx context.Context, taskID int64, tier domain.Tier) (domain.Plan, error) {
	if tier != "" && !tier.Valid() {
		return domain.Plan{}, fmt.Errorf("%w: tier %q", service.ErrInvalid, tier)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	st, err := p.gather(ctx, false)
	if err != nil {
		return domain.Plan{}, err
	}
	if st.day == nil || st.plan == nil {
		return domain.Plan{}, ErrNoDay
	}
	if st.day.WrappedAt != nil {
		return domain.Plan{}, fmt.Errorf("%w: %s is already wrapped", service.ErrConflict, st.day.Date)
	}
	tf, ok := st.facts.Task(taskID)
	if !ok {
		return domain.Plan{}, fmt.Errorf("%w: #%d is not an eligible task", service.ErrInvalid, taskID)
	}

	from := "unplanned"
	entries := []domain.PlanEntry{}
	for _, e := range st.plan.Entries {
		if e.TaskID == taskID {
			from = string(e.Tier)
		} else {
			entries = append(entries, e)
		}
	}
	to := "unplanned"
	if tier != "" {
		to = string(tier)
		entries = append(entries, domain.PlanEntry{TaskID: taskID, Tier: tier, Rationale: "You moved this here."})
	}
	saved, err := p.svc.SavePlan(ctx, domain.Plan{
		DayID:    st.day.ID,
		Trigger:  domain.TriggerMove,
		Reason:   fmt.Sprintf("#%d %q from %s to %s", taskID, tf.Task.Title, from, to),
		Opening:  st.plan.Opening,
		Entries:  entries,
		Fallback: st.plan.Fallback,
	})
	if err != nil {
		return domain.Plan{}, err
	}
	p.hub.Publish(stream.Message{Type: "plan.updated", Data: saved})
	return saved, nil
}
