package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/stream"
)

// cheerSnap plans #1 as a must, #2 as a should, and #3 as a could, and leaves #4 unplanned.
func cheerSnap(done ...int64) *snapshot {
	s := &snapshot{tasks: map[int64]domain.Task{}, today: planner.Today{
		Plan: &domain.Plan{Entries: []domain.PlanEntry{
			{TaskID: 1, Tier: domain.TierMust}, {TaskID: 2, Tier: domain.TierShould}, {TaskID: 3, Tier: domain.TierCould},
		}},
		Unplanned: []int64{4},
	}}
	for id := int64(1); id <= 4; id++ {
		s.tasks[id] = domain.Task{ID: id, Status: domain.StatusOpen}
	}
	for _, id := range done {
		s.tasks[id] = domain.Task{ID: id, Status: domain.StatusDone}
	}
	return s
}

func statusChange(id int64, from, to domain.Status) domain.Activity {
	raw := func(s domain.Status) json.RawMessage {
		b, _ := json.Marshal(map[string]domain.Status{"status": s})
		return b
	}
	return domain.Activity{Action: "status", Entity: "task", EntityID: id, Before: raw(from), After: raw(to)}
}

func applied(source stream.Source, acts ...domain.Activity) stream.BatchApplied {
	return stream.BatchApplied{BatchID: 1, Source: source, Activity: acts}
}

func TestCheerFor(t *testing.T) {
	open, done, dropped := domain.StatusOpen, domain.StatusDone, domain.StatusDropped
	cases := []struct {
		name string
		b    stream.BatchApplied
		snap *snapshot
		want cheer
	}{
		{"a should", applied(stream.SourceUser, statusChange(2, open, done)), cheerSnap(), cheerSmall},
		{"an unplanned task", applied(stream.SourceChat, statusChange(4, open, done)), cheerSnap(), cheerSmall},
		{"a must", applied(stream.SourceUser, statusChange(1, open, done)), cheerSnap(), cheerMust},
		{"the last planned task", applied(stream.SourceUser, statusChange(3, open, done)), cheerSnap(1, 2), cheerFinale},
		{"the rest of the plan at once", applied(stream.SourceChat, statusChange(1, open, done), statusChange(3, open, done)), cheerSnap(2), cheerFinale},
		{"an unplanned task once the plan is done", applied(stream.SourceUser, statusChange(4, open, done)), cheerSnap(1, 2, 3), cheerSmall},
		{"reopening", applied(stream.SourceUser, statusChange(1, done, open)), cheerSnap(1), cheerNone},
		{"dropping", applied(stream.SourceUser, statusChange(1, open, dropped)), cheerSnap(), cheerNone},
		{"morning triage", applied(stream.SourceTriage, statusChange(1, open, done)), cheerSnap(), cheerNone},
		{"no plan yet", applied(stream.SourceUser, statusChange(1, open, done)), &snapshot{tasks: map[int64]domain.Task{}}, cheerSmall},
		{"no snapshot yet", applied(stream.SourceUser, statusChange(1, open, done)), nil, cheerSmall},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cheerFor(c.b, c.snap); got != c.want {
				t.Errorf("cheerFor = %v, want %v", got, c.want)
			}
		})
	}
}

func TestACompletionCheersThenClears(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 120, 30)
	must := m.snap.order()[0]

	m = send(m, hubMsg{Type: "batch.applied", Data: applied(stream.SourceChat, statusChange(must, domain.StatusOpen, domain.StatusDone))})
	if !strings.Contains(m.View(), "must done") {
		t.Fatalf("no must cheer in:\n%s", m.View())
	}
	for range cheerFrames {
		m = send(m, cheerTickMsg{seq: m.cheerSeq})
	}
	if strings.Contains(m.View(), "must done") {
		t.Error("the cheer stayed after its frames")
	}
}

func TestTheFinaleCoversTheTopOfTheList(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 120, 30)
	var acts []domain.Activity
	for _, g := range m.snap.groups() {
		if g.tier == "" {
			continue
		}
		for _, id := range g.ids {
			acts = append(acts, statusChange(id, domain.StatusOpen, domain.StatusDone))
		}
	}

	m = send(m, hubMsg{Type: "batch.applied", Data: applied(stream.SourceChat, acts...)})
	if !strings.Contains(m.View(), "plan cleared") {
		t.Errorf("no finale in:\n%s", m.View())
	}
}

func TestServerWorkShowsInTheHeader(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 120, 30)

	m = send(m, hubMsg{Type: "work.started", Data: stream.Work{Kind: stream.WorkReplan}})
	if !strings.Contains(m.View(), "replanning") {
		t.Fatalf("no replanning in:\n%s", m.View())
	}
	m = send(m, hubMsg{Type: "work.started", Data: stream.Work{Kind: stream.WorkCleanup}},
		hubMsg{Type: "work.finished", Data: stream.Work{Kind: stream.WorkReplan}})
	if v := m.View(); strings.Contains(v, "replanning") || !strings.Contains(v, "tidying") {
		t.Errorf("want only tidying in:\n%s", v)
	}
	m = send(m, hubMsg{Type: "work.finished", Data: stream.Work{Kind: stream.WorkCleanup}})
	if strings.Contains(m.View(), "tidying") {
		t.Error("tidying stayed after it finished")
	}
}
