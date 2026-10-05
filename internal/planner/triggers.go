package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	"github.com/jameynakama/whatnow/internal/stream"
)

// Run replans today whenever a change calls for it, until ctx is done or msgs closes. msgs is a
// hub subscription, so changes from the API, cleanup, and the agent are all seen, and so are
// undos. Changes that pile up while a replan runs are folded into one more replan.
func (p *Planner) Run(ctx context.Context, msgs <-chan stream.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-msgs:
			if !ok {
				return
			}
			trigger, reasons := p.reasons(ctx, m)
			for more := true; more; {
				select {
				case m, ok := <-msgs:
					if !ok {
						return
					}
					t, r := p.reasons(ctx, m)
					if t == domain.TriggerPace {
						trigger = t
					}
					reasons = append(reasons, r...)
				default:
					more = false
				}
			}
			if len(reasons) == 0 {
				continue
			}
			if _, err := p.Replan(ctx, trigger, strings.Join(reasons, "; ")); err != nil && !errors.Is(err, ErrNoDay) {
				log.Printf("replan: %v", err)
			}
		}
	}
}

// reasons says why one hub message calls for a replan. Only a day that already has a plan is
// replanned; before that, the morning makes the first one.
func (p *Planner) reasons(ctx context.Context, m stream.Message) (domain.Trigger, []string) {
	var acts []domain.Activity
	undone := false
	switch d := m.Data.(type) {
	case stream.BatchApplied:
		acts = d.Activity
	case stream.BatchUndone:
		var err error
		if acts, err = p.svc.BatchActivity(ctx, d.BatchID); err != nil {
			log.Printf("replan: read undone batch %d: %v", d.BatchID, err)
			return "", nil
		}
		undone = true
	default:
		return "", nil
	}
	today := facts.Today(p.now(), p.loc)
	day, err := p.svc.GetDay(ctx, today)
	if err != nil {
		return "", nil
	}
	plan, err := p.svc.LatestPlan(ctx, day.ID)
	if err != nil {
		return "", nil
	}
	musts := map[int64]bool{}
	for _, e := range plan.Entries {
		if e.Tier == domain.TierMust {
			musts[e.TaskID] = true
		}
	}
	return replanReasons(acts, musts, today, undone)
}

// replanReasons lists what in a batch calls for a replan: a pace change, a task becoming due
// today, a must being dropped or deleted, or a blocker added or removed on a must. undone runs
// the batch's changes backward, since an undo reverses each of them.
func replanReasons(acts []domain.Activity, musts map[int64]bool, today domain.Date, undone bool) (domain.Trigger, []string) {
	trigger := domain.TriggerEdit
	var reasons []string
	for _, a := range acts {
		before, after := a.Before, a.After
		if undone {
			before, after = after, before
		}
		switch a.Entity {
		case "day":
			var b, af domain.Day
			if decode(before, &b) && decode(after, &af) && b.Pace != af.Pace && af.Date == today {
				trigger = domain.TriggerPace
				reasons = append(reasons, fmt.Sprintf("pace changed to %q", af.Pace.Label()))
			}
		case "task":
			var b, af domain.Task
			hadB, hadA := decode(before, &b), decode(after, &af)
			switch {
			case hadA && dueNow(af, today) && !(hadB && dueNow(b, today)):
				reasons = append(reasons, fmt.Sprintf("#%d %q is due now", af.ID, af.Title))
			case musts[a.EntityID] && hadB && live(b) && (!hadA || af.Status == domain.StatusDropped || af.DeletedAt != nil):
				reasons = append(reasons, fmt.Sprintf("the must #%d %q was dropped", b.ID, b.Title))
			}
		case "blocker":
			var b domain.Blocker
			if !decode(before, &b) {
				decode(after, &b)
			}
			for _, id := range []int64{b.TaskID, b.BlockedByID} {
				if musts[id] {
					reasons = append(reasons, fmt.Sprintf("a blocker on the must #%d changed", id))
					break
				}
			}
		}
	}
	return trigger, reasons
}

func decode(raw json.RawMessage, v any) bool {
	return raw != nil && json.Unmarshal(raw, v) == nil
}

func live(t domain.Task) bool { return t.Status == domain.StatusOpen && t.DeletedAt == nil }

func dueNow(t domain.Task, today domain.Date) bool {
	return live(t) && t.DueOn != nil && *t.DueOn <= today
}
