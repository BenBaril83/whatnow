package prompt_test

import (
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	"github.com/jameynakama/whatnow/internal/prompt"
)

func TestFactsRenderInLocalTime(t *testing.T) {
	loc := time.FixedZone("EST", -5*3600)
	at := func(h, m int) time.Time { return time.Date(2026, 10, 5, h, m, 0, 0, loc) }
	standupEnd := at(10, 15).UTC()
	size, energy := domain.SizeL, domain.EnergyHigh
	f := facts.Facts{
		Now:    at(9, 0),
		DayEnd: at(17, 30),
		Gaps:   []facts.Gap{{Start: at(9, 0), End: at(10, 0)}, {Start: at(10, 15), End: at(17, 30)}},
		Events: []domain.Event{
			{Title: "standup", StartsAt: at(10, 0).UTC(), EndsAt: &standupEnd},
			{Title: "dentist", StartsAt: at(15, 0).UTC()},
		},
		Tasks: []facts.TaskFacts{
			{Task: domain.Task{ID: 3, Title: "Do FLEX1-666", Size: &size, Energy: &energy, Feeling: "dreading it"},
				Annotations: []string{"due today", `blocked by #4 "Research pubsub"`}},
			{Task: domain.Task{ID: 9, Title: "water plants"}},
		},
	}
	want := `Now: Monday 2026-10-05 09:00 (EST). The working day ends at 17:30.
Free time left today: 09:00-10:00 (1h), 10:15-17:30 (7h 15m)

Today's events:
- 10:00-10:15 standup
- 15:00 dentist

Eligible tasks, as #<id> title:
#3 Do FLEX1-666 [size L, high energy] feeling: "dreading it"
  - due today
  - blocked by #4 "Research pubsub"
#9 water plants
`
	if got := prompt.Facts(f); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFactsRenderAnEmptyDay(t *testing.T) {
	now := time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC)
	got := prompt.Facts(facts.Facts{Now: now, DayEnd: now})
	want := `Now: Monday 2026-10-05 18:00 (UTC). The working day ends at 18:00.
Free time left today: none

Today's events:
(none)

Eligible tasks, as #<id> title:
(none)
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
