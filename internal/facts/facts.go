// Package facts works out what is true about the user's day: free time, which tasks are eligible
// and blocked, and notes such as "due today" or "pushed 4 times". Facts reach the model as
// annotations, never as filters. The package does no I/O.
package facts

import (
	"fmt"
	"sort"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
)

// Input is what facts are computed from. Tasks, Blockers, and Events must leave out anything
// deleted. Events may include ones outside today; only today's count.
type Input struct {
	Now      time.Time
	Loc      *time.Location
	EndOfDay string // local HH:MM
	Tasks    []domain.Task
	Blockers []domain.Blocker
	Events   []domain.Event
	// PushCounts maps a task ID to how many times it was pushed.
	PushCounts map[int64]int
	// Musts are the current plan's must tier. A must raises the urgency of whatever blocks it.
	Musts map[int64]bool
}

type Gap struct {
	Start time.Time
	End   time.Time
}

func (g Gap) Duration() time.Duration { return g.End.Sub(g.Start) }

type TaskFacts struct {
	Task domain.Task
	// BlockedBy lists the open tasks this one waits on.
	BlockedBy   []int64
	Annotations []string
}

type Facts struct {
	Now    time.Time // in the user's time zone
	Today  domain.Date
	DayEnd time.Time
	// Events are today's, earliest first.
	Events []domain.Event
	// Gaps are the free stretches between now and the end of the day.
	Gaps []Gap
	// Tasks are the eligible tasks: dated ones by due date, then undated ones, each oldest first.
	Tasks []TaskFacts
}

// Task returns an eligible task's facts.
func (f Facts) Task(id int64) (TaskFacts, bool) {
	for _, t := range f.Tasks {
		if t.Task.ID == id {
			return t, true
		}
	}
	return TaskFacts{}, false
}

// PointEventLength is how long an event without an end is taken to last.
const PointEventLength = 30 * time.Minute

// minGap is the shortest free gap a task of each size fits in. An S task fits anywhere.
var minGap = map[domain.Size]time.Duration{domain.SizeM: time.Hour, domain.SizeL: 3 * time.Hour}

// Today returns the date it is in loc.
func Today(now time.Time, loc *time.Location) domain.Date {
	return domain.Date(now.In(loc).Format(time.DateOnly))
}

// Eligible reports whether a task can go in a plan: open, not deleted, and not put off until a
// later date.
func Eligible(t domain.Task, today domain.Date) bool {
	return t.Status == domain.StatusOpen && t.DeletedAt == nil && (t.NotBefore == nil || *t.NotBefore <= today)
}

func Compute(in Input) Facts {
	now := in.Now.In(in.Loc)
	f := Facts{Now: now, Today: Today(now, in.Loc)}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, in.Loc)
	f.DayEnd = dayEnd(dayStart, in.EndOfDay, in.Loc)
	f.Events = todays(in.Events, dayStart, dayStart.AddDate(0, 0, 1))
	f.Gaps = gaps(now, f.DayEnd, f.Events)

	byID := map[int64]domain.Task{}
	for _, t := range in.Tasks {
		byID[t.ID] = t
	}
	open := func(id int64) bool { t, ok := byID[id]; return ok && t.Status == domain.StatusOpen }
	waitsOn := map[int64][]int64{}  // task -> open tasks it waits on
	waitedBy := map[int64][]int64{} // task -> open tasks waiting on it
	for _, b := range in.Blockers {
		if open(b.TaskID) && open(b.BlockedByID) {
			waitsOn[b.TaskID] = append(waitsOn[b.TaskID], b.BlockedByID)
			waitedBy[b.BlockedByID] = append(waitedBy[b.BlockedByID], b.TaskID)
		}
	}
	urgent := map[int64]string{}
	for _, t := range in.Tasks {
		switch {
		case t.Status != domain.StatusOpen:
		case in.Musts[t.ID]:
			urgent[t.ID] = "a must"
		case t.DueOn != nil && *t.DueOn < f.Today:
			urgent[t.ID] = "overdue"
		case t.DueOn != nil && *t.DueOn == f.Today:
			urgent[t.ID] = "due today"
		}
	}
	var longest time.Duration
	for _, g := range f.Gaps {
		longest = max(longest, g.Duration())
	}

	for _, t := range in.Tasks {
		if !Eligible(t, f.Today) {
			continue
		}
		tf := TaskFacts{Task: t, BlockedBy: waitsOn[t.ID]}
		if t.DueOn != nil {
			tf.Annotations = append(tf.Annotations, due(*t.DueOn, f.Today))
		}
		for _, id := range tf.BlockedBy {
			tf.Annotations = append(tf.Annotations, fmt.Sprintf("blocked by #%d %q", id, byID[id].Title))
		}
		for _, id := range reachable(t.ID, waitedBy) {
			if why, ok := urgent[id]; ok {
				tf.Annotations = append(tf.Annotations, fmt.Sprintf("blocks #%d %q, which is %s", id, byID[id].Title, why))
			}
		}
		switch n := in.PushCounts[t.ID]; {
		case n == 1:
			tf.Annotations = append(tf.Annotations, "pushed once")
		case n > 1:
			tf.Annotations = append(tf.Annotations, fmt.Sprintf("pushed %d times", n))
		}
		if t.Size != nil && len(f.Gaps) > 0 && longest < minGap[*t.Size] {
			tf.Annotations = append(tf.Annotations, fmt.Sprintf("too big for any free gap today (the longest is %s)", Duration(longest)))
		}
		f.Tasks = append(f.Tasks, tf)
	}
	sort.SliceStable(f.Tasks, func(i, j int) bool { return fallbackLess(f.Tasks[i].Task, f.Tasks[j].Task) })
	return f
}

// fallbackLess orders dated tasks by due date, then undated ones, each oldest first.
func fallbackLess(a, b domain.Task) bool {
	if (a.DueOn == nil) != (b.DueOn == nil) {
		return a.DueOn != nil
	}
	if a.DueOn != nil && *a.DueOn != *b.DueOn {
		return *a.DueOn < *b.DueOn
	}
	return a.CreatedAt.Before(b.CreatedAt) || (a.CreatedAt.Equal(b.CreatedAt) && a.ID < b.ID)
}

// dayEnd is the end of the working day. An unreadable setting means midnight.
func dayEnd(dayStart time.Time, clock string, loc *time.Location) time.Time {
	c, err := time.Parse("15:04", clock)
	if err != nil {
		return dayStart.AddDate(0, 0, 1)
	}
	return time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), c.Hour(), c.Minute(), 0, 0, loc)
}

func end(e domain.Event) time.Time {
	if e.EndsAt != nil {
		return *e.EndsAt
	}
	return e.StartsAt.Add(PointEventLength)
}

// todays returns the events that overlap [from, to), earliest first.
func todays(events []domain.Event, from, to time.Time) []domain.Event {
	out := []domain.Event{}
	for _, e := range events {
		if e.StartsAt.Before(to) && end(e).After(from) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartsAt.Before(out[j].StartsAt) })
	return out
}

// gaps returns the free stretches of [from, to) that no event covers.
func gaps(from, to time.Time, events []domain.Event) []Gap {
	out := []Gap{}
	cursor := from
	for _, e := range events {
		if !cursor.Before(to) {
			break
		}
		if e.StartsAt.After(cursor) {
			out = append(out, Gap{Start: cursor, End: minTime(e.StartsAt, to)})
		}
		if end(e).After(cursor) {
			cursor = end(e)
		}
	}
	if cursor.Before(to) {
		out = append(out, Gap{Start: cursor, End: to})
	}
	return out
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// reachable returns every task that waits on id, directly or through others.
func reachable(id int64, waitedBy map[int64][]int64) []int64 {
	var out []int64
	seen := map[int64]bool{id: true}
	stack := append([]int64(nil), waitedBy[id]...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		stack = append(stack, waitedBy[n]...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func due(on, today domain.Date) string {
	days := daysBetween(today, on)
	switch {
	case days < 0:
		return fmt.Sprintf("overdue, was due %s", on)
	case days == 0:
		return "due today"
	case days == 1:
		return "due tomorrow"
	}
	return fmt.Sprintf("due %s, in %d days", on, days)
}

// daysBetween counts calendar days, so a DST change can't make it off by one.
func daysBetween(from, to domain.Date) int {
	a, _ := time.Parse(time.DateOnly, string(from))
	b, _ := time.Parse(time.DateOnly, string(to))
	return int(b.Sub(a).Hours() / 24)
}

// Duration formats a duration as "45m", "2h", or "1h 30m".
func Duration(d time.Duration) string {
	d = d.Round(time.Minute)
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}
