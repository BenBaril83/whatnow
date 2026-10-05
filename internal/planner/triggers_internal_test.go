package planner

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
)

func snap(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func act(entity string, id int64, before, after any) domain.Activity {
	return domain.Activity{Entity: entity, EntityID: id, Before: snap(before), After: snap(after)}
}

func TestReplanReasons(t *testing.T) {
	const today = domain.Date("2026-10-05")
	d := func(s string) *domain.Date { v := domain.Date(s); return &v }
	task := func(id int64, mutate func(*domain.Task)) domain.Task {
		t := domain.Task{ID: id, Title: "t", Status: domain.StatusOpen}
		if mutate != nil {
			mutate(&t)
		}
		return t
	}
	dueToday := func(t *domain.Task) { t.DueOn = d("2026-10-05") }
	dueFriday := func(t *domain.Task) { t.DueOn = d("2026-10-09") }
	dropped := func(t *domain.Task) { t.Status = domain.StatusDropped }
	done := func(t *domain.Task) { t.Status = domain.StatusDone }
	gone := func(t *domain.Task) { deleted := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC); t.DeletedAt = &deleted }
	musts := map[int64]bool{1: true}
	day := func(p domain.Pace, date domain.Date) domain.Day { return domain.Day{ID: 1, Date: date, Pace: p} }

	cases := []struct {
		name    string
		act     domain.Activity
		undone  bool
		trigger domain.Trigger
		reasons []string
	}{
		{"pace change", act("day", 1, day(3, today), day(5, today)), false, domain.TriggerPace, []string{`pace changed to "go hard"`}},
		{"mood only", act("day", 1, day(3, today), day(3, today)), false, domain.TriggerEdit, nil},
		{"pace on another day", act("day", 1, day(3, "2026-10-04"), day(5, "2026-10-04")), false, domain.TriggerEdit, nil},
		{"undoing a pace change", act("day", 1, day(3, today), day(5, today)), true, domain.TriggerPace, []string{`pace changed to "steady"`}},
		{"created due today", act("task", 7, nil, task(7, dueToday)), false, domain.TriggerEdit, []string{`#7 "t" is due now`}},
		{"created due friday", act("task", 7, nil, task(7, dueFriday)), false, domain.TriggerEdit, nil},
		{"moved to today", act("task", 7, task(7, dueFriday), task(7, dueToday)), false, domain.TriggerEdit, []string{`#7 "t" is due now`}},
		{"edited, already due", act("task", 7, task(7, dueToday), task(7, dueToday)), false, domain.TriggerEdit, nil},
		{"undoing a delete of a due task", act("task", 7, task(7, dueToday), task(7, func(t *domain.Task) { dueToday(t); gone(t) })), true,
			domain.TriggerEdit, []string{`#7 "t" is due now`}},
		{"undoing a create", act("task", 7, nil, task(7, dueToday)), true, domain.TriggerEdit, nil},
		{"dropped a must", act("task", 1, task(1, nil), task(1, dropped)), false, domain.TriggerEdit, []string{`the must #1 "t" was dropped`}},
		{"deleted a must", act("task", 1, task(1, nil), task(1, gone)), false, domain.TriggerEdit, []string{`the must #1 "t" was dropped`}},
		{"finished a must", act("task", 1, task(1, nil), task(1, done)), false, domain.TriggerEdit, nil},
		{"dropped a could", act("task", 2, task(2, nil), task(2, dropped)), false, domain.TriggerEdit, nil},
		{"blocker on a must", act("blocker", 1, nil, domain.Blocker{TaskID: 1, BlockedByID: 9}), false, domain.TriggerEdit,
			[]string{"a blocker on the must #1 changed"}},
		{"must as the blocker", act("blocker", 4, domain.Blocker{TaskID: 4, BlockedByID: 1}, nil), false, domain.TriggerEdit,
			[]string{"a blocker on the must #1 changed"}},
		{"blocker between others", act("blocker", 4, nil, domain.Blocker{TaskID: 4, BlockedByID: 5}), false, domain.TriggerEdit, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			trigger, reasons := replanReasons([]domain.Activity{c.act}, musts, today, c.undone)
			if trigger != c.trigger {
				t.Errorf("trigger = %q, want %q", trigger, c.trigger)
			}
			if diff := cmp.Diff(c.reasons, reasons); diff != "" {
				t.Errorf("reasons (-want +got):\n%s", diff)
			}
		})
	}
}
