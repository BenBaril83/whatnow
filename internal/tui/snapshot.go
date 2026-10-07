package tui

import (
	"context"
	"time"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/settings"
)

// snapshot is everything the screen shows, read in one go.
type snapshot struct {
	now       time.Time
	today     planner.Today
	tasks     map[int64]domain.Task
	events    []domain.Event
	waits     map[int64]int // open blockers per task
	doneToday int
	meetings  int // today's events that have ended
	endOfDay  time.Time
	chat      []domain.Message
}

func load(ctx context.Context, a *app.App) (snapshot, error) {
	now := a.Now().In(a.Loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, a.Loc)
	dayEnd := dayStart.AddDate(0, 0, 1)
	s := snapshot{now: now, tasks: map[int64]domain.Task{}, waits: map[int64]int{}}

	var err error
	if s.today, err = a.Planner.Today(ctx); err != nil {
		return snapshot{}, err
	}
	tasks, err := a.Service.ListTasks(ctx)
	if err != nil {
		return snapshot{}, err
	}
	for _, t := range tasks {
		s.tasks[t.ID] = t
	}
	if s.events, err = a.Service.ListEvents(ctx, dayStart, dayEnd); err != nil {
		return snapshot{}, err
	}
	for _, e := range s.events {
		if !eventEnd(e).After(now) {
			s.meetings++
		}
	}
	blockers, err := a.Service.ListBlockers(ctx)
	if err != nil {
		return snapshot{}, err
	}
	for _, b := range blockers {
		if s.tasks[b.BlockedByID].Status == domain.StatusOpen {
			s.waits[b.TaskID]++
		}
	}
	done, err := a.Service.DoneBetween(ctx, dayStart, dayEnd)
	if err != nil {
		return snapshot{}, err
	}
	s.doneToday = len(done)
	if s.chat, err = a.Agent.History(ctx); err != nil {
		return snapshot{}, err
	}
	set, err := settings.Load(ctx, a.DB)
	if err != nil {
		return snapshot{}, err
	}
	// Settings are checked when saved, so the end of day always parses.
	clock, _ := time.Parse(settings.ClockLayout, set.EndOfDay)
	s.endOfDay = dayStart.Add(time.Duration(clock.Hour())*time.Hour + time.Duration(clock.Minute())*time.Minute)
	return s, nil
}

// group is one heading of the list: a plan tier, or the tasks the plan leaves out when tier is "".
type group struct {
	tier domain.Tier
	ids  []int64
}

// groups is the list as shown: open tasks by plan tier in plan order, then the rest. It's empty
// until the day has a plan.
func (s snapshot) groups() []group {
	if s.today.Plan == nil {
		return nil
	}
	var out []group
	for _, tier := range []domain.Tier{domain.TierMust, domain.TierShould, domain.TierCould} {
		g := group{tier: tier}
		for _, e := range s.today.Plan.Entries {
			if e.Tier == tier && s.open(e.TaskID) {
				g.ids = append(g.ids, e.TaskID)
			}
		}
		if len(g.ids) > 0 {
			out = append(out, g)
		}
	}
	rest := group{}
	for _, id := range s.today.Unplanned {
		if s.open(id) {
			rest.ids = append(rest.ids, id)
		}
	}
	if len(rest.ids) > 0 {
		out = append(out, rest)
	}
	return out
}

// order is every task on the list, top to bottom.
func (s snapshot) order() []int64 {
	var ids []int64
	for _, g := range s.groups() {
		ids = append(ids, g.ids...)
	}
	return ids
}

func (s snapshot) open(id int64) bool {
	t, ok := s.tasks[id]
	return ok && t.Status == domain.StatusOpen
}
