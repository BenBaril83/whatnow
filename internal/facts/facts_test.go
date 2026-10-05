package facts_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
)

// loc is UTC-5, so local and UTC times differ in every case.
var loc = time.FixedZone("UTC-5", -5*3600)

func at(day, hour, min int) time.Time { return time.Date(2026, 10, day, hour, min, 0, 0, loc) }

func event(title string, start time.Time, length time.Duration) domain.Event {
	e := domain.Event{Title: title, StartsAt: start}
	if length > 0 {
		end := start.Add(length)
		e.EndsAt = &end
	}
	return e
}

type span struct{ start, end time.Time }

func spans(gs []facts.Gap) []span {
	out := []span{}
	for _, g := range gs {
		out = append(out, span{g.Start, g.End})
	}
	return out
}

func TestGaps(t *testing.T) {
	cases := []struct {
		name   string
		now    time.Time
		end    string
		events []domain.Event
		want   []span
	}{
		{"no events", at(5, 9, 0), "17:30", nil, []span{{at(5, 9, 0), at(5, 17, 30)}}},
		{"one meeting", at(5, 9, 0), "17:30", []domain.Event{event("standup", at(5, 10, 0), 15*time.Minute)},
			[]span{{at(5, 9, 0), at(5, 10, 0)}, {at(5, 10, 15), at(5, 17, 30)}}},
		{"back to back leaves no gap between", at(5, 9, 0), "17:30", []domain.Event{
			event("a", at(5, 10, 0), time.Hour), event("b", at(5, 11, 0), time.Hour)},
			[]span{{at(5, 9, 0), at(5, 10, 0)}, {at(5, 12, 0), at(5, 17, 30)}}},
		{"overlapping events merge", at(5, 9, 0), "17:30", []domain.Event{
			event("long", at(5, 10, 0), 2*time.Hour), event("inside", at(5, 10, 30), 30*time.Minute)},
			[]span{{at(5, 9, 0), at(5, 10, 0)}, {at(5, 12, 0), at(5, 17, 30)}}},
		{"event without an end takes 30 minutes", at(5, 9, 0), "17:30", []domain.Event{event("dentist", at(5, 15, 0), 0)},
			[]span{{at(5, 9, 0), at(5, 15, 0)}, {at(5, 15, 30), at(5, 17, 30)}}},
		{"now inside a meeting", at(5, 10, 30), "17:30", []domain.Event{event("standup", at(5, 10, 0), time.Hour)},
			[]span{{at(5, 11, 0), at(5, 17, 30)}}},
		{"past events don't matter", at(5, 13, 0), "17:30", []domain.Event{event("standup", at(5, 10, 0), time.Hour)},
			[]span{{at(5, 13, 0), at(5, 17, 30)}}},
		{"event running past the end of the day", at(5, 16, 0), "17:30", []domain.Event{event("late", at(5, 17, 0), 2*time.Hour)},
			[]span{{at(5, 16, 0), at(5, 17, 0)}}},
		{"event after the end of the day", at(5, 16, 0), "17:30", []domain.Event{event("dinner", at(5, 19, 0), time.Hour)},
			[]span{{at(5, 16, 0), at(5, 17, 30)}}},
		{"overnight event from yesterday", at(5, 0, 30), "17:30", []domain.Event{event("on call", at(4, 22, 0), 4*time.Hour)},
			[]span{{at(5, 2, 0), at(5, 17, 30)}}},
		{"after the end of the day", at(5, 18, 0), "17:30", nil, []span{}},
		{"unreadable end of day means midnight", at(5, 22, 0), "", nil, []span{{at(5, 22, 0), at(6, 0, 0)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := facts.Compute(facts.Input{Now: c.now, Loc: loc, EndOfDay: c.end, Events: c.events})
			if diff := cmp.Diff(c.want, spans(f.Gaps), cmp.AllowUnexported(span{})); diff != "" {
				t.Errorf("gaps (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGapsAcrossADSTChange(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database")
	}
	// Clocks fall back at 02:00 on 2026-11-01, so midnight to 03:00 local is four real hours.
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	f := facts.Compute(facts.Input{Now: now, Loc: ny, EndOfDay: "03:00"})
	if len(f.Gaps) != 1 || f.Gaps[0].Duration() != 4*time.Hour {
		t.Errorf("gaps = %v, want one 4h gap", f.Gaps)
	}
	if f.Today != "2026-11-01" {
		t.Errorf("today = %s", f.Today)
	}
}

func TestTodayIsTheLocalDate(t *testing.T) {
	// 02:00 UTC on the 6th is still the evening of the 5th at UTC-5.
	if got := facts.Today(time.Date(2026, 10, 6, 2, 0, 0, 0, time.UTC), loc); got != "2026-10-05" {
		t.Errorf("today = %s", got)
	}
}

func date(d string) *domain.Date      { v := domain.Date(d); return &v }
func size(s domain.Size) *domain.Size { return &s }

func task(id int64, title string, opts ...func(*domain.Task)) domain.Task {
	t := domain.Task{ID: id, Title: title, Status: domain.StatusOpen, CreatedAt: at(1, 9, int(id))}
	for _, o := range opts {
		o(&t)
	}
	return t
}

func due(d string) func(*domain.Task)           { return func(t *domain.Task) { t.DueOn = date(d) } }
func notBefore(d string) func(*domain.Task)     { return func(t *domain.Task) { t.NotBefore = date(d) } }
func status(s domain.Status) func(*domain.Task) { return func(t *domain.Task) { t.Status = s } }
func sized(s domain.Size) func(*domain.Task)    { return func(t *domain.Task) { t.Size = size(s) } }
func deleted() func(*domain.Task)               { return func(t *domain.Task) { d := at(2, 0, 0); t.DeletedAt = &d } }
func ids(f facts.Facts) []int64 {
	out := []int64{}
	for _, t := range f.Tasks {
		out = append(out, t.Task.ID)
	}
	return out
}

func TestEligibleTasksInFallbackOrder(t *testing.T) {
	tasks := []domain.Task{
		task(1, "undated older"),
		task(2, "due later", due("2026-10-09")),
		task(3, "done", status(domain.StatusDone)),
		task(4, "dropped", status(domain.StatusDropped)),
		task(5, "not before tomorrow", notBefore("2026-10-06")),
		task(6, "not before today", notBefore("2026-10-05")),
		task(7, "overdue", due("2026-10-01")),
		task(8, "due today", due("2026-10-05")),
		task(9, "deleted", deleted()),
	}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks})
	if diff := cmp.Diff([]int64{7, 8, 2, 1, 6}, ids(f)); diff != "" {
		t.Errorf("eligible order (-want +got):\n%s", diff)
	}
}

func annotations(t *testing.T, f facts.Facts, id int64) []string {
	t.Helper()
	tf, ok := f.Task(id)
	if !ok {
		t.Fatalf("task %d is not eligible", id)
	}
	return tf.Annotations
}

func TestDueAnnotations(t *testing.T) {
	tasks := []domain.Task{
		task(1, "a", due("2026-10-03")),
		task(2, "b", due("2026-10-05")),
		task(3, "c", due("2026-10-06")),
		task(4, "d", due("2026-10-09")),
	}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks})
	want := map[int64]string{1: "overdue, was due 2026-10-03", 2: "due today", 3: "due tomorrow", 4: "due 2026-10-09, in 4 days"}
	for id, w := range want {
		if got := annotations(t, f, id); len(got) != 1 || got[0] != w {
			t.Errorf("task %d: %v, want [%s]", id, got, w)
		}
	}
}

func TestBlockerChains(t *testing.T) {
	// 1 waits on 2, which waits on 3. 4 waited on 5, which is done. 1 is due today; 6 is a must.
	tasks := []domain.Task{
		task(1, "ship FLEX1-666", due("2026-10-05")),
		task(2, "write the design"),
		task(3, "research pubsub"),
		task(4, "deploy"),
		task(5, "get approval", status(domain.StatusDone)),
		task(6, "demo prep"),
		task(7, "book the room"),
	}
	blockers := []domain.Blocker{{TaskID: 1, BlockedByID: 2}, {TaskID: 2, BlockedByID: 3}, {TaskID: 4, BlockedByID: 5},
		{TaskID: 6, BlockedByID: 7}}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks, Blockers: blockers,
		Musts: map[int64]bool{6: true}})

	cases := map[int64][]string{
		1: {"due today", `blocked by #2 "write the design"`},
		2: {`blocked by #3 "research pubsub"`, `blocks #1 "ship FLEX1-666", which is due today`},
		3: {`blocks #1 "ship FLEX1-666", which is due today`},
		4: nil, // its only blocker is done
		7: {`blocks #6 "demo prep", which is a must`},
	}
	for id, want := range cases {
		if diff := cmp.Diff(want, annotations(t, f, id)); diff != "" {
			t.Errorf("task %d (-want +got):\n%s", id, diff)
		}
	}
	if tf, _ := f.Task(4); len(tf.BlockedBy) != 0 {
		t.Errorf("task 4 blocked by %v", tf.BlockedBy)
	}
}

func TestABlockerThatIsNotYetEligibleStillBlocks(t *testing.T) {
	tasks := []domain.Task{task(1, "launch"), task(2, "vendor reply", notBefore("2026-10-08"))}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks,
		Blockers: []domain.Blocker{{TaskID: 1, BlockedByID: 2}}})
	if tf, _ := f.Task(1); len(tf.BlockedBy) != 1 {
		t.Errorf("task 1 should wait on task 2: %+v", tf)
	}
}

func TestPushAnnotations(t *testing.T) {
	tasks := []domain.Task{task(1, "a"), task(2, "b"), task(3, "c")}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks,
		PushCounts: map[int64]int{1: 1, 2: 4}})
	for id, want := range map[int64][]string{1: {"pushed once"}, 2: {"pushed 4 times"}, 3: nil} {
		if diff := cmp.Diff(want, annotations(t, f, id)); diff != "" {
			t.Errorf("task %d (-want +got):\n%s", id, diff)
		}
	}
}

func TestTooBigForTheGaps(t *testing.T) {
	tasks := []domain.Task{task(1, "small", sized(domain.SizeS)), task(2, "medium", sized(domain.SizeM)),
		task(3, "large", sized(domain.SizeL))}
	meetings := []domain.Event{event("a", at(5, 10, 0), 4*time.Hour), event("b", at(5, 15, 0), 2*time.Hour)}
	// Free: 09:00-10:00, 14:00-15:00, 17:00-17:30. The longest gap is 1h.
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks, Events: meetings})
	for id, want := range map[int64][]string{1: nil, 2: nil, 3: {"too big for any free gap today (the longest is 1h)"}} {
		if diff := cmp.Diff(want, annotations(t, f, id)); diff != "" {
			t.Errorf("task %d (-want +got):\n%s", id, diff)
		}
	}

	// With no free time left at all, nothing is called too big: the day is simply over.
	f = facts.Compute(facts.Input{Now: at(5, 18, 0), Loc: loc, EndOfDay: "17:30", Tasks: tasks})
	if got := annotations(t, f, 3); got != nil {
		t.Errorf("after hours: %v", got)
	}
}

func TestTodaysEventsOnly(t *testing.T) {
	events := []domain.Event{
		event("tomorrow", at(6, 9, 0), time.Hour),
		event("standup", at(5, 10, 0), 15*time.Minute),
		event("yesterday", at(4, 9, 0), time.Hour),
		event("early", at(5, 8, 0), time.Hour),
	}
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Events: events})
	var got []string
	for _, e := range f.Events {
		got = append(got, e.Title)
	}
	if diff := cmp.Diff([]string{"early", "standup"}, got); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{45 * time.Minute: "45m", 2 * time.Hour: "2h", 90 * time.Minute: "1h 30m", 0: "0m"} {
		if got := facts.Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestGapsAreInTheUsersZoneWhenEventsAreStoredInUTC(t *testing.T) {
	// The store hands back UTC times. A gap after a meeting must still read as local time.
	standup := event("standup", at(5, 10, 0).UTC(), 15*time.Minute)
	f := facts.Compute(facts.Input{Now: at(5, 9, 0), Loc: loc, EndOfDay: "17:30", Events: []domain.Event{standup}})
	var got []string
	for _, g := range f.Gaps {
		got = append(got, g.Start.Format("15:04")+"-"+g.End.Format("15:04"))
	}
	if diff := cmp.Diff([]string{"09:00-10:00", "10:15-17:30"}, got); diff != "" {
		t.Errorf("gaps (-want +got):\n%s", diff)
	}
}
