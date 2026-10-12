package integrations_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/integrations"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

var loc = time.FixedZone("UTC-5", -5*3600)

// now is 09:00 local on October 5th.
var now = time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)

// fake is an integration with a list of calendars, each a name and a secret feed, and a secret
// token. Its sources come from feeds, by feed value; a missing feed fails.
type fake struct {
	feeds map[string][]integrations.Event
	// got is the config and window of the last pull.
	got      integrations.Config
	from, to time.Time
}

func (*fake) ID() string   { return "fake" }
func (*fake) Name() string { return "Fake" }
func (*fake) Fields() []integrations.Field {
	return []integrations.Field{
		{Key: "team", Label: "Team", Kind: integrations.Text},
		{Key: "token", Label: "Token", Kind: integrations.Secret},
		{Key: "calendars", Label: "Calendars", Kind: integrations.List, Required: true, Fields: []integrations.Field{
			{Key: "label", Label: "Name", Kind: integrations.Text, Required: true},
			{Key: "feed", Label: "Feed", Kind: integrations.Secret, Required: true},
		}},
	}
}

func (f *fake) Pull(_ context.Context, cfg integrations.Config, from, to time.Time) []integrations.Source {
	f.got, f.from, f.to = cfg, from, to
	var out []integrations.Source
	for _, row := range cfg.Lists["calendars"] {
		src := integrations.Source{Label: row.Values["label"]}
		events, ok := f.feeds[row.Values["feed"]]
		if ok {
			src.Events = events
		} else {
			src.Err = errors.New("couldn't reach it")
		}
		out = append(out, src)
	}
	return out
}

type env struct {
	runner *integrations.Runner
	svc    *service.Service
	hub    *stream.Hub
	fake   *fake
}

func newEnv(t *testing.T) env {
	t.Helper()
	keyring.MockInit()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := func() time.Time { return now }
	svc := service.New(db, clock)
	hub := stream.NewHub()
	f := &fake{feeds: map[string][]integrations.Event{}}
	return env{runner: integrations.NewRunner(db, svc, hub, loc, clock, f), svc: svc, hub: hub, fake: f}
}

func at(hour int) time.Time { return time.Date(2026, 10, 5, hour, 0, 0, 0, loc) }

func event(key, series, title string, hour int) integrations.Event {
	return integrations.Event{Key: key, Series: series, Title: title, StartsAt: at(hour)}
}

// setUp saves calendars as name → feed, and turns the integration on.
func (e env) setUp(t *testing.T, calendars ...[2]string) integrations.View {
	t.Helper()
	var rows []integrations.RowUpdate
	for _, c := range calendars {
		rows = append(rows, integrations.RowUpdate{Values: map[string]string{"label": c[0], "feed": c[1]}})
	}
	if _, err := e.runner.Save(context.Background(), "fake", integrations.Update{Lists: map[string][]integrations.RowUpdate{"calendars": rows}}); err != nil {
		t.Fatal(err)
	}
	v, err := e.runner.SetEnabled(context.Background(), "fake", true)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (e env) pull(t *testing.T) integrations.PullResult {
	t.Helper()
	res, err := e.runner.Pull(context.Background(), "fake")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// pulled lists a result as "source: title, title" lines, or "source: error".
func pulled(res integrations.PullResult) []string {
	var out []string
	for _, s := range res.Sources {
		line := s.Label + ":"
		if s.Error != "" {
			line += " " + s.Error
		}
		for _, e := range s.Events {
			line += " " + e.Title
		}
		out = append(out, line)
	}
	return out
}

func (e env) today(t *testing.T) []string {
	t.Helper()
	events, err := e.svc.ListEvents(context.Background(), at(0), at(24))
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, ev := range events {
		titles = append(titles, ev.Title)
	}
	return titles
}

func TestViewShowsSecretsOnlyAsSet(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v, err := e.runner.View(ctx, "fake")
	if err != nil {
		t.Fatal(err)
	}
	if v.Enabled || v.Ready || v.Secrets["token"] || len(v.Lists["calendars"]) != 0 {
		t.Errorf("fresh view = %+v", v)
	}

	v, err = e.runner.Save(ctx, "fake", integrations.Update{
		Values: map[string]string{"team": " Platform ", "token": "s3cret"},
		Lists: map[string][]integrations.RowUpdate{"calendars": {
			{Values: map[string]string{"label": "Work", "feed": "https://example.com/private.ics"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	row := v.Lists["calendars"][0]
	if v.Values["team"] != "Platform" || !v.Secrets["token"] || !v.Ready || v.Enabled {
		t.Errorf("view = %+v", v)
	}
	if row.ID == "" || row.Values["label"] != "Work" || !row.Secrets["feed"] {
		t.Errorf("row = %+v", row)
	}
	if _, ok := row.Values["feed"]; ok {
		t.Errorf("row values hold the secret: %+v", row.Values)
	}
}

func TestSaveKeepsABlankSecretAndRemovesARowsSecret(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v := e.setUp(t, [2]string{"Work", "work-feed"}, [2]string{"Personal", "home-feed"})
	work, home := v.Lists["calendars"][0], v.Lists["calendars"][1]

	// Renaming Work with a blank feed keeps its feed. Personal is left out, so it goes.
	v, err := e.runner.Save(ctx, "fake", integrations.Update{Lists: map[string][]integrations.RowUpdate{"calendars": {
		{ID: work.ID, Values: map[string]string{"label": "Job"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Lists["calendars"]; len(got) != 1 || got[0].ID != work.ID || got[0].Values["label"] != "Job" || !got[0].Secrets["feed"] {
		t.Errorf("rows = %+v", got)
	}
	e.pull(t)
	if diff := cmp.Diff("work-feed", e.fake.got.Lists["calendars"][0].Values["feed"]); diff != "" {
		t.Errorf("feed (-want +got):\n%s", diff)
	}
	accts, err := e.runner.Secrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accts {
		if v, _ := keyring.Get("whatnow", a); v == "home-feed" {
			t.Errorf("removed row's feed is still in the keychain as %s", a)
		}
	}
	if _, err := keyring.Get("whatnow", "integration/fake/"+home.ID+"/feed"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("removed row's feed: err = %v", err)
	}
}

func TestSaveRejectsBadInput(t *testing.T) {
	e := newEnv(t)
	cases := map[string]integrations.Update{
		"unknown field":      {Values: map[string]string{"nope": "x"}},
		"list as a value":    {Values: map[string]string{"calendars": "x"}},
		"unknown list":       {Lists: map[string][]integrations.RowUpdate{"team": {}}},
		"unknown row":        {Lists: map[string][]integrations.RowUpdate{"calendars": {{ID: "abc", Values: map[string]string{"label": "x", "feed": "y"}}}}},
		"unknown column":     {Lists: map[string][]integrations.RowUpdate{"calendars": {{Values: map[string]string{"label": "x", "feed": "y", "z": "1"}}}}},
		"new row, no secret": {Lists: map[string][]integrations.RowUpdate{"calendars": {{Values: map[string]string{"label": "Work"}}}}},
		"no name":            {Lists: map[string][]integrations.RowUpdate{"calendars": {{Values: map[string]string{"label": " ", "feed": "y"}}}}},
	}
	for name, u := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := e.runner.Save(context.Background(), "fake", u); !errors.Is(err, service.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
	if _, err := e.runner.Save(context.Background(), "nope", integrations.Update{}); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown integration: err = %v", err)
	}
}

func TestSaveKeepsEnabledAndSkipped(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.fake.feeds["f"] = []integrations.Event{event("a|1", "a", "Blockout", 15)}
	v := e.setUp(t, [2]string{"Work", "f"})
	res := e.pull(t)
	if _, err := e.runner.Skip(ctx, "fake", integrations.Skip{Key: "a", Title: "Blockout"}, res.Sources[0].Events[0].ID); err != nil {
		t.Fatal(err)
	}

	v, err := e.runner.Save(ctx, "fake", integrations.Update{Lists: map[string][]integrations.RowUpdate{"calendars": {
		{ID: v.Lists["calendars"][0].ID, Values: map[string]string{"label": "Job"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Enabled || len(v.Skipped) != 1 {
		t.Errorf("view = %+v", v)
	}
}

func TestPullNeedsTheIntegrationOn(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.runner.Pull(ctx, "fake"); !errors.Is(err, service.ErrConflict) {
		t.Errorf("not set up: err = %v", err)
	}
	e.setUp(t, [2]string{"Work", "f"})
	if _, err := e.runner.SetEnabled(ctx, "fake", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.runner.Pull(ctx, "fake"); !errors.Is(err, service.ErrConflict) {
		t.Errorf("off: err = %v", err)
	}
	if _, err := e.runner.Pull(ctx, "nope"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown: err = %v", err)
	}
}

func TestPullBringsInTodayBySource(t *testing.T) {
	e := newEnv(t)
	e.fake.feeds["work"] = []integrations.Event{event("s|9", "s", "Standup", 9), event("shared", "", "All hands", 11)}
	e.fake.feeds["home"] = []integrations.Event{event("shared", "", "All hands", 11), event("d", "", "Dentist", 17)}
	e.setUp(t, [2]string{"Work", "work"}, [2]string{"Personal", "home"})
	msgs, unsubscribe := e.hub.Subscribe("batch.applied")
	defer unsubscribe()

	res := e.pull(t)

	if diff := cmp.Diff([]string{"Work: Standup All hands", "Personal: Dentist"}, pulled(res)); diff != "" {
		t.Errorf("result (-want +got):\n%s", diff)
	}
	if got := res.Sources[0].Events[0]; got.ID == 0 || got.Series != "s" {
		t.Errorf("standup = %+v", got)
	}
	if !e.fake.from.Equal(at(0)) || !e.fake.to.Equal(at(24)) {
		t.Errorf("window = %v to %v", e.fake.from, e.fake.to)
	}
	if diff := cmp.Diff([]string{"Standup", "All hands", "Dentist"}, e.today(t)); diff != "" {
		t.Errorf("today (-want +got):\n%s", diff)
	}
	select {
	case m := <-msgs:
		if b := m.Data.(stream.BatchApplied); b.BatchID != res.BatchID || len(b.Activity) != 3 {
			t.Errorf("published = %+v", b)
		}
	default:
		t.Error("the pull wasn't announced")
	}
}

func TestAFailedSourceRemovesNothing(t *testing.T) {
	e := newEnv(t)
	e.fake.feeds["work"] = []integrations.Event{event("s|9", "s", "Standup", 9)}
	e.fake.feeds["home"] = []integrations.Event{event("d", "", "Dentist", 17)}
	e.setUp(t, [2]string{"Work", "work"}, [2]string{"Personal", "home"})
	e.pull(t)

	delete(e.fake.feeds, "home")
	res := e.pull(t)

	if diff := cmp.Diff([]string{"Work: Standup", "Personal: couldn't reach it"}, pulled(res)); diff != "" {
		t.Errorf("result (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"Standup", "Dentist"}, e.today(t)); diff != "" {
		t.Errorf("today (-want +got):\n%s", diff)
	}
}

func TestSkipRemovesTodayAndKeepsTheSeriesOut(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.fake.feeds["work"] = []integrations.Event{event("b|15", "b", "Blockout", 15), event("s|9", "s", "Standup", 9)}
	e.setUp(t, [2]string{"Work", "work"})
	res := e.pull(t)
	blockout := res.Sources[0].Events[0]

	batch, err := e.runner.Skip(ctx, "fake", integrations.Skip{Key: blockout.Series, Title: " Blockout "}, blockout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"Standup"}, e.today(t)); diff != "" {
		t.Errorf("today after skip (-want +got):\n%s", diff)
	}

	// Tomorrow's occurrence has a new key but the same series, so it stays out too.
	e.fake.feeds["work"] = []integrations.Event{event("b|15-2", "b", "Blockout", 16), event("s|9", "s", "Standup", 9)}
	if diff := cmp.Diff([]string{"Work: Standup"}, pulled(e.pull(t))); diff != "" {
		t.Errorf("pull after skip (-want +got):\n%s", diff)
	}

	// Undoing the skip brings back today's occurrence; the series stays skipped.
	if err := e.svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	v, err := e.runner.View(ctx, "fake")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]integrations.Skip{{Key: "b", Title: "Blockout"}}, v.Skipped); diff != "" {
		t.Errorf("skipped (-want +got):\n%s", diff)
	}

	v, err = e.runner.Unskip(ctx, "fake", "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Skipped) != 0 {
		t.Errorf("after unskip = %+v", v.Skipped)
	}
	if diff := cmp.Diff([]string{"Work: Blockout Standup"}, pulled(e.pull(t))); diff != "" {
		t.Errorf("pull after unskip (-want +got):\n%s", diff)
	}
}

func TestSkipNeedsAKeyAndALiveEvent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.runner.Skip(ctx, "fake", integrations.Skip{Key: " "}, 1); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("blank key: err = %v", err)
	}
	if _, err := e.runner.Skip(ctx, "fake", integrations.Skip{Key: "b"}, 99); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("missing event: err = %v", err)
	}
	if v, _ := e.runner.View(ctx, "fake"); len(v.Skipped) != 0 {
		t.Errorf("a failed skip was saved: %+v", v.Skipped)
	}
}

func TestSecretsNamesEverySavedSecret(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v := e.setUp(t, [2]string{"Work", "f"})
	accts, err := e.runner.Secrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"integration/fake/token", "integration/fake/" + v.Lists["calendars"][0].ID + "/feed"}
	if diff := cmp.Diff(want, accts); diff != "" {
		t.Errorf("secrets (-want +got):\n%s", diff)
	}
	if err := integrations.DeleteSecrets(accts); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.runner.View(ctx, "fake"); v.Lists["calendars"][0].Secrets["feed"] {
		t.Error("the feed is still set")
	}
}
