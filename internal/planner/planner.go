// Package planner makes the day's plan and answers "what now?". Each is one structured call to the
// main model. The reply is validated against the facts; a rejected reply gets one retry with the
// problems attached, and if that fails too, code builds the answer instead, so a bad model day
// still gives the user something valid.
package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/prompt"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/stream"
)

// ErrNoDay means today hasn't been started, so there is no pace to plan for.
var ErrNoDay = errors.New("today hasn't started")

// ClientFunc returns the client and settings for one call. It returns settings.ErrNotConfigured,
// along with the saved settings, when no provider is set up.
type ClientFunc func(ctx context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error)

type Planner struct {
	svc    *service.Service
	hub    *stream.Hub
	client ClientFunc
	loc    *time.Location
	now    func() time.Time
	// mu runs one replan at a time, so plan versions follow the order of what triggered them.
	mu sync.Mutex
}

const maxTokens = 8192

// modelTimeout bounds one plan or pick, retry included. A hung provider then falls back instead
// of stalling the trigger watcher while the changes it misses pile up.
var modelTimeout = time.Minute

func New(svc *service.Service, hub *stream.Hub, client ClientFunc, loc *time.Location, now func() time.Time) *Planner {
	return &Planner{svc: svc, hub: hub, client: client, loc: loc, now: now}
}

// state is what one call sees. day and plan are nil when today hasn't started or has no plan yet.
// model is nil when no client could be built, and modelErr says why.
type state struct {
	// last is the most recent day before today, if any.
	last     *domain.Day
	day      *domain.Day
	plan     *domain.Plan
	facts    facts.Facts
	settings settings.Settings
	model    llm.Client
	cfg      settings.ProviderConfig
	modelErr error
}

// Date is today's date in the user's time zone.
func (p *Planner) Date() domain.Date { return facts.Today(p.now(), p.loc) }

// gather reads the current state. withModel also builds a client and loads settings; without it,
// the facts use the default end of day.
func (p *Planner) gather(ctx context.Context, withModel bool) (state, error) {
	st := state{settings: settings.Defaults()}
	now := p.now()
	today := facts.Today(now, p.loc)
	day, err := p.svc.GetDay(ctx, today)
	switch {
	case err == nil:
		st.day = &day
		plan, err := p.svc.LatestPlan(ctx, day.ID)
		if err == nil {
			st.plan = &plan
		} else if !errors.Is(err, service.ErrNotFound) {
			return st, err
		}
	case !errors.Is(err, service.ErrNotFound):
		return st, err
	}
	if last, err := p.svc.LastDayBefore(ctx, today); err == nil {
		st.last = &last
	} else if !errors.Is(err, service.ErrNotFound) {
		return st, err
	}

	if withModel {
		st.model, st.cfg, st.settings, st.modelErr = p.client(ctx)
		if st.modelErr != nil {
			st.model = nil
		}
	}
	tasks, err := p.svc.ListTasks(ctx)
	if err != nil {
		return st, err
	}
	blockers, err := p.svc.ListBlockers(ctx)
	if err != nil {
		return st, err
	}
	pushes, err := p.svc.PushCounts(ctx)
	if err != nil {
		return st, err
	}
	// From a day back, so an overnight event still covering this morning is seen.
	local := now.In(p.loc)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, p.loc)
	events, err := p.svc.ListEvents(ctx, dayStart.AddDate(0, 0, -1), dayStart.AddDate(0, 0, 1))
	if err != nil {
		return st, err
	}
	musts := map[int64]bool{}
	if st.plan != nil {
		for _, e := range st.plan.Entries {
			if e.Tier == domain.TierMust {
				musts[e.TaskID] = true
			}
		}
	}
	st.facts = facts.Compute(facts.Input{Now: now, Loc: p.loc, EndOfDay: st.settings.EndOfDay, Tasks: tasks,
		Blockers: blockers, Events: events, PushCounts: pushes, Musts: musts})
	return st, nil
}

// context renders the live context layer: pace and mood, the facts, and the current plan.
func (st state) context() string {
	var b strings.Builder
	if st.last != nil && (st.last.Summary != "" || st.last.TomorrowNote != "") {
		fmt.Fprintf(&b, "Last day they used this, %s:", st.last.Date)
		if st.last.Summary != "" {
			fmt.Fprintf(&b, " %s", st.last.Summary)
		}
		if st.last.TomorrowNote != "" {
			fmt.Fprintf(&b, "\nThe note they left for next time: %q", st.last.TomorrowNote)
		}
		b.WriteString("\n\n")
	}
	if st.day != nil {
		fmt.Fprintf(&b, "Pace today: %d of 5, %q.\n", st.day.Pace, st.day.Pace.Label())
		if st.day.MoodNote != "" {
			fmt.Fprintf(&b, "On their mind: %q\n", st.day.MoodNote)
		}
		b.WriteByte('\n')
	}
	b.WriteString(prompt.Facts(st.facts))
	if st.plan == nil {
		return b.String()
	}
	fmt.Fprintf(&b, "\nCurrent plan:\n")
	planned := map[int64]bool{}
	for _, tier := range []domain.Tier{domain.TierMust, domain.TierShould, domain.TierCould} {
		var ids []string
		for _, e := range st.plan.Entries {
			if e.Tier == tier {
				ids = append(ids, fmt.Sprintf("#%d", e.TaskID))
				planned[e.TaskID] = true
			}
		}
		if len(ids) == 0 {
			ids = []string{"(none)"}
		}
		fmt.Fprintf(&b, "%s: %s\n", tier, strings.Join(ids, ", "))
	}
	var unplanned []string
	for _, tf := range st.facts.Tasks {
		if !planned[tf.Task.ID] {
			unplanned = append(unplanned, fmt.Sprintf("#%d", tf.Task.ID))
		}
	}
	if len(unplanned) > 0 {
		fmt.Fprintf(&b, "unplanned: %s\n", strings.Join(unplanned, ", "))
	}
	return b.String()
}

func (st state) request(instructions, user string, schema map[string]any, name string) llm.Request {
	return llm.Request{
		Model:      st.cfg.MainModel,
		System:     prompt.System(instructions, st.settings.Style, st.settings.AboutMe, st.context()),
		User:       user,
		Schema:     schema,
		SchemaName: name,
		MaxTokens:  maxTokens,
		Fallback:   true,
	}
}

// ask makes a structured call and checks the reply. A reply that fails the check gets one retry
// with the problems attached. Provider errors are returned at once: retrying them is the SDK's job.
func ask[T any](ctx context.Context, model llm.Client, req llm.Request, check func(T) error) (T, error) {
	var out T
	reply, err := model.Complete(ctx, req)
	if err != nil {
		return out, err
	}
	if err = parse(reply, &out, check); err == nil {
		return out, nil
	}
	req.User += fmt.Sprintf("\n\nYour previous reply was:\n%s\n\nIt was rejected:\n%v\n\nReply again with that fixed.", reply, err)
	reply, err = model.Complete(ctx, req)
	if err != nil {
		return out, err
	}
	out = *new(T)
	return out, parse(reply, &out, check)
}

func parse[T any](reply string, out *T, check func(T) error) error {
	if err := json.Unmarshal([]byte(reply), out); err != nil {
		return fmt.Errorf("the reply is not valid JSON: %v", err)
	}
	return check(*out)
}

// fallbackNote explains a plan or pick that code made instead of the model.
func fallbackNote(err error) string {
	if errors.Is(err, settings.ErrNotConfigured) {
		return "No AI provider is set up, so this is sorted by due date."
	}
	log.Printf("planner fell back: %v", err)
	return "I couldn't think this through just now, so this is sorted by due date."
}
