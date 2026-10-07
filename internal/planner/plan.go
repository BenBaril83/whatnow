package planner

import (
	"context"
	"errors"
	"fmt"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	js "github.com/jameynakama/whatnow/internal/jsonschema"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

// planSchema puts the entries before the opening, so the model decides the plan before it
// writes about it, and each rationale before its tier.
var planSchema = js.Object(
	js.Prop("entries", js.Array(js.Object(
		js.Prop("task_id", js.Integer("The task's number, from #<id>.")),
		js.Prop("rationale", js.String("One short line for the user on why the task sits in this tier.")),
		js.Prop("tier", js.Enum("must, should, or could.", "must", "should", "could")),
	))),
	js.Prop("opening", js.String("Two or three sentences to the user, in your voice.")),
)

type planReply struct {
	Entries []domain.PlanEntry `json:"entries"`
	Opening string             `json:"opening"`
}

// Replan makes a new version of today's plan and publishes it as plan.updated. It returns ErrNoDay
// if today hasn't started, and ErrConflict if it's already wrapped: a wrapped day is never replanned.
func (p *Planner) Replan(ctx context.Context, trigger domain.Trigger, reason string) (domain.Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, err := p.gather(ctx, true)
	if err != nil {
		return domain.Plan{}, err
	}
	if st.day == nil {
		return domain.Plan{}, ErrNoDay
	}
	if st.day.WrappedAt != nil {
		return domain.Plan{}, fmt.Errorf("%w: %s is already wrapped", service.ErrConflict, st.day.Date)
	}
	defer p.hub.StartWork(stream.WorkReplan)()
	plan := domain.Plan{DayID: st.day.ID, Trigger: trigger, Reason: reason}

	reply, err := p.askPlan(ctx, st, trigger, reason)
	if err == nil {
		plan.Entries, plan.Opening = reply.Entries, reply.Opening
	} else {
		plan.Entries, plan.Opening, plan.Fallback = fallbackPlan(st.facts), fallbackNote(err), true
	}
	saved, err := p.svc.SavePlan(ctx, plan)
	if err != nil {
		return domain.Plan{}, err
	}
	p.hub.Publish(stream.Message{Type: "plan.updated", Data: saved})
	return saved, nil
}

func (p *Planner) askPlan(ctx context.Context, st state, trigger domain.Trigger, reason string) (planReply, error) {
	if st.model == nil {
		return planReply{}, st.modelErr
	}
	ctx, cancel := context.WithTimeout(ctx, modelTimeout)
	defer cancel()
	user := "Plan my day."
	if trigger != domain.TriggerMorning {
		user = fmt.Sprintf("Something changed: %s. Update the plan.", reason)
	}
	return ask(ctx, st.model, st.request(planInstructions, user, planSchema, "plan"), func(r planReply) error {
		return checkPlan(r, st.facts)
	})
}

// checkPlan rejects a plan that names a task twice, an unknown or ineligible task, or a bad tier.
func checkPlan(r planReply, f facts.Facts) error {
	var errs []error
	seen := map[int64]bool{}
	for _, e := range r.Entries {
		if _, ok := f.Task(e.TaskID); !ok {
			errs = append(errs, fmt.Errorf("#%d is not an eligible task", e.TaskID))
		}
		if seen[e.TaskID] {
			errs = append(errs, fmt.Errorf("#%d appears more than once", e.TaskID))
		}
		seen[e.TaskID] = true
		if !e.Tier.Valid() {
			errs = append(errs, fmt.Errorf("#%d has tier %q", e.TaskID, e.Tier))
		}
	}
	return errors.Join(errs...)
}

// fallbackPlan is the plan code makes when the model can't: due today or overdue is a must, due
// later is a should, and undated is a could. Facts already lists tasks in that order.
func fallbackPlan(f facts.Facts) []domain.PlanEntry {
	entries := []domain.PlanEntry{}
	for _, tf := range f.Tasks {
		t := tf.Task
		e := domain.PlanEntry{TaskID: t.ID, Tier: domain.TierCould, Rationale: "No due date."}
		switch {
		case t.DueOn != nil && *t.DueOn <= f.Today:
			e.Tier, e.Rationale = domain.TierMust, "Due today or overdue."
		case t.DueOn != nil:
			e.Tier, e.Rationale = domain.TierShould, "Due "+string(*t.DueOn)+"."
		}
		entries = append(entries, e)
	}
	return entries
}
