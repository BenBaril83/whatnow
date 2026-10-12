package integrations

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

// Runner keeps each integration's settings and syncs what it pulls into today's events.
type Runner struct {
	db  *sql.DB
	svc *service.Service
	hub *stream.Hub
	loc *time.Location
	now func() time.Time
	all []Integration
}

func NewRunner(db *sql.DB, svc *service.Service, hub *stream.Hub, loc *time.Location, now func() time.Time, all ...Integration) *Runner {
	return &Runner{db: db, svc: svc, hub: hub, loc: loc, now: now, all: all}
}

// View is an integration as the settings screen sees it. Secrets show only whether they're set.
type View struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Fields  []Field `json:"fields"`
	Enabled bool    `json:"enabled"`
	// Ready means every required field is filled, so it can pull once it's enabled.
	Ready   bool                 `json:"ready"`
	Values  map[string]string    `json:"values"`
	Secrets map[string]bool      `json:"secrets"`
	Lists   map[string][]RowView `json:"lists"`
	Skipped []Skip               `json:"skipped"`
}

type RowView struct {
	ID      string            `json:"id"`
	Values  map[string]string `json:"values"`
	Secrets map[string]bool   `json:"secrets"`
}

// On says whether the integration can pull.
func (v View) On() bool { return v.Enabled && v.Ready }

// Update is what saving an integration's fields sends. A blank secret keeps the saved one. A row
// with no ID is new; a saved row left out is removed, with its secrets.
type Update struct {
	Values map[string]string      `json:"values"`
	Lists  map[string][]RowUpdate `json:"lists"`
}

type RowUpdate struct {
	ID     string            `json:"id"`
	Values map[string]string `json:"values"`
}

func (r *Runner) find(id string) (Integration, error) {
	for _, in := range r.all {
		if in.ID() == id {
			return in, nil
		}
	}
	return nil, fmt.Errorf("%w: integration %q", service.ErrNotFound, id)
}

func (r *Runner) Views(ctx context.Context) ([]View, error) {
	views := []View{}
	for _, in := range r.all {
		v, err := r.view(ctx, in)
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

func (r *Runner) View(ctx context.Context, id string) (View, error) {
	in, err := r.find(id)
	if err != nil {
		return View{}, err
	}
	return r.view(ctx, in)
}

func (r *Runner) view(ctx context.Context, in Integration) (View, error) {
	s, err := load(ctx, r.db, in.ID())
	if err != nil {
		return View{}, err
	}
	v := View{
		ID: in.ID(), Name: in.Name(), Fields: in.Fields(), Enabled: s.Enabled, Ready: true,
		Values: map[string]string{}, Secrets: map[string]bool{}, Lists: map[string][]RowView{}, Skipped: s.Skipped,
	}
	if v.Skipped == nil {
		v.Skipped = []Skip{}
	}
	for _, f := range v.Fields {
		switch f.Kind {
		case Text:
			v.Values[f.Key] = s.Values[f.Key]
			v.Ready = v.Ready && (!f.Required || s.Values[f.Key] != "")
		case Secret:
			val, err := secret(account(in.ID(), "", f.Key))
			if err != nil {
				return View{}, err
			}
			v.Secrets[f.Key] = val != ""
			v.Ready = v.Ready && (!f.Required || val != "")
		case List:
			rows := []RowView{}
			for _, row := range s.Lists[f.Key] {
				rv := RowView{ID: row.ID, Values: map[string]string{}, Secrets: map[string]bool{}}
				for _, c := range f.Fields {
					if c.Kind == Secret {
						val, err := secret(account(in.ID(), row.ID, c.Key))
						if err != nil {
							return View{}, err
						}
						rv.Secrets[c.Key] = val != ""
						continue
					}
					rv.Values[c.Key] = row.Values[c.Key]
				}
				rows = append(rows, rv)
			}
			v.Lists[f.Key] = rows
			v.Ready = v.Ready && (!f.Required || len(rows) > 0)
		}
	}
	return v, nil
}

func (r *Runner) SetEnabled(ctx context.Context, id string, on bool) (View, error) {
	in, err := r.find(id)
	if err != nil {
		return View{}, err
	}
	s, err := load(ctx, r.db, id)
	if err != nil {
		return View{}, err
	}
	s.Enabled = on
	if err := store(ctx, r.db, id, s); err != nil {
		return View{}, err
	}
	return r.view(ctx, in)
}

// Save replaces the integration's fields with u. It writes new secrets before the settings, and
// removes the secrets of removed rows after, so a failure never leaves a row without its secret.
func (r *Runner) Save(ctx context.Context, id string, u Update) (View, error) {
	in, err := r.find(id)
	if err != nil {
		return View{}, err
	}
	s, err := load(ctx, r.db, id)
	if err != nil {
		return View{}, err
	}
	next := saved{Enabled: s.Enabled, Values: map[string]string{}, Lists: map[string][]Row{}, Skipped: s.Skipped}
	writes := map[string]string{}
	fields := in.Fields()
	for key := range u.Values {
		if f := field(fields, key); f == nil || f.Kind == List {
			return View{}, fmt.Errorf("%w: %s has no field %q", service.ErrInvalid, in.Name(), key)
		}
	}
	for key := range u.Lists {
		if f := field(fields, key); f == nil || f.Kind != List {
			return View{}, fmt.Errorf("%w: %s has no list %q", service.ErrInvalid, in.Name(), key)
		}
	}
	for _, f := range fields {
		switch f.Kind {
		case Text:
			next.Values[f.Key] = strings.TrimSpace(u.Values[f.Key])
		case Secret:
			if v := strings.TrimSpace(u.Values[f.Key]); v != "" {
				writes[account(id, "", f.Key)] = v
			}
		case List:
			rows, err := listRows(id, f, s.Lists[f.Key], u.Lists[f.Key], writes)
			if err != nil {
				return View{}, err
			}
			next.Lists[f.Key] = rows
		}
	}
	for acct, v := range writes {
		if err := keyring.Set(keychainService, acct, v); err != nil {
			return View{}, err
		}
	}
	if err := store(ctx, r.db, id, next); err != nil {
		return View{}, err
	}
	gone := slices.DeleteFunc(rowSecrets(in, s), func(acct string) bool { return slices.Contains(rowSecrets(in, next), acct) })
	if err := DeleteSecrets(gone); err != nil {
		return View{}, err
	}
	return r.view(ctx, in)
}

// listRows checks a list's updated rows against what's saved, giving each new row an ID and
// adding the secrets to write.
func listRows(id string, f Field, saved []Row, updates []RowUpdate, writes map[string]string) ([]Row, error) {
	rows := []Row{}
	for i, ru := range updates {
		rowID := ru.ID
		if rowID == "" {
			rowID = newRowID()
		} else if !slices.ContainsFunc(saved, func(r Row) bool { return r.ID == rowID }) {
			return nil, fmt.Errorf("%w: %s has no row %q", service.ErrInvalid, f.Label, rowID)
		}
		for key := range ru.Values {
			if field(f.Fields, key) == nil {
				return nil, fmt.Errorf("%w: %s has no column %q", service.ErrInvalid, f.Label, key)
			}
		}
		row := Row{ID: rowID, Values: map[string]string{}}
		for _, c := range f.Fields {
			v := strings.TrimSpace(ru.Values[c.Key])
			missing := v == ""
			if c.Kind == Secret {
				acct := account(id, rowID, c.Key)
				if v != "" {
					writes[acct] = v
				} else if ru.ID != "" {
					old, err := secret(acct)
					if err != nil {
						return nil, err
					}
					missing = old == ""
				}
			} else {
				row.Values[c.Key] = v
			}
			if c.Required && missing {
				return nil, fmt.Errorf("%w: %s row %d needs a %s", service.ErrInvalid, f.Label, i+1, strings.ToLower(c.Label))
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func field(fields []Field, key string) *Field {
	for i := range fields {
		if fields[i].Key == key {
			return &fields[i]
		}
	}
	return nil
}

// rowSecrets names the keychain entries of every secret column in s's list rows.
func rowSecrets(in Integration, s saved) []string {
	var accts []string
	for _, f := range in.Fields() {
		for _, row := range s.Lists[f.Key] {
			for _, c := range f.Fields {
				if c.Kind == Secret {
					accts = append(accts, account(in.ID(), row.ID, c.Key))
				}
			}
		}
	}
	return accts
}

// Secrets names every secret the integrations could have in the keychain, for wiping.
func (r *Runner) Secrets(ctx context.Context) ([]string, error) {
	var accts []string
	for _, in := range r.all {
		s, err := load(ctx, r.db, in.ID())
		if err != nil {
			return nil, err
		}
		for _, f := range in.Fields() {
			if f.Kind == Secret {
				accts = append(accts, account(in.ID(), "", f.Key))
			}
		}
		accts = append(accts, rowSecrets(in, s)...)
	}
	return accts, nil
}

// config fills in the saved settings' secrets.
func config(in Integration, s saved) (Config, error) {
	cfg := Config{Values: map[string]string{}, Lists: map[string][]Row{}}
	for _, f := range in.Fields() {
		switch f.Kind {
		case Text:
			cfg.Values[f.Key] = s.Values[f.Key]
		case Secret:
			v, err := secret(account(in.ID(), "", f.Key))
			if err != nil {
				return cfg, err
			}
			cfg.Values[f.Key] = v
		case List:
			for _, row := range s.Lists[f.Key] {
				full := Row{ID: row.ID, Values: map[string]string{}}
				for _, c := range f.Fields {
					v := row.Values[c.Key]
					if c.Kind == Secret {
						var err error
						if v, err = secret(account(in.ID(), row.ID, c.Key)); err != nil {
							return cfg, err
						}
					}
					full.Values[c.Key] = v
				}
				cfg.Lists[f.Key] = append(cfg.Lists[f.Key], full)
			}
		}
	}
	return cfg, nil
}

// PullResult is what a pull brought in, by source, with each event's ID in whatnow. A source that
// failed has Error and no events.
type PullResult struct {
	BatchID int64          `json:"batch_id"`
	Sources []SourceResult `json:"sources"`
}

type SourceResult struct {
	Label  string        `json:"label"`
	Error  string        `json:"error,omitempty"`
	Events []PulledEvent `json:"events"`
}

type PulledEvent struct {
	ID       int64      `json:"id"`
	Title    string     `json:"title"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`
	// Series is set on an occurrence of a recurring event, which removing offers to skip for good.
	Series string `json:"series"`
}

// Pull brings in today's events from the integration, in one user batch, and announces it. An
// event in more than one source lands once, under the first. When any source fails, nothing is
// removed, since the missing source's events can't be told from canceled ones.
func (r *Runner) Pull(ctx context.Context, id string) (PullResult, error) {
	in, err := r.find(id)
	if err != nil {
		return PullResult{}, err
	}
	v, err := r.view(ctx, in)
	if err != nil {
		return PullResult{}, err
	}
	if !v.On() {
		return PullResult{}, fmt.Errorf("%w: %s is off or not set up", service.ErrConflict, in.Name())
	}
	s, err := load(ctx, r.db, id)
	if err != nil {
		return PullResult{}, err
	}
	cfg, err := config(in, s)
	if err != nil {
		return PullResult{}, err
	}
	now := r.now().In(r.loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, r.loc)
	to := from.AddDate(0, 0, 1)
	sources := in.Pull(ctx, cfg, from, to)

	skipped := map[string]bool{}
	for _, sk := range s.Skipped {
		skipped[sk.Key] = true
	}
	var pulled []service.PulledEvent
	type origin struct {
		source int
		event  Event
	}
	seen := map[string]origin{}
	prune := true
	for i, src := range sources {
		if src.Err != nil {
			prune = false
			continue
		}
		for _, e := range src.Events {
			if _, dup := seen[e.Key]; dup || skipped[e.Key] || (e.Series != "" && skipped[e.Series]) {
				continue
			}
			seen[e.Key] = origin{i, e}
			pulled = append(pulled, service.PulledEvent{Key: e.Key, Title: e.Title, StartsAt: e.StartsAt, EndsAt: e.EndsAt})
		}
	}

	batch, err := r.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		return PullResult{}, err
	}
	live, err := r.svc.SyncEvents(ctx, batch, id, from, to, pulled, prune)
	if err != nil {
		return PullResult{}, err
	}
	if err := r.publish(ctx, batch); err != nil {
		return PullResult{}, err
	}

	res := PullResult{BatchID: batch, Sources: make([]SourceResult, len(sources))}
	for i, src := range sources {
		res.Sources[i] = SourceResult{Label: src.Label, Events: []PulledEvent{}}
		if src.Err != nil {
			res.Sources[i].Error = src.Err.Error()
		}
	}
	for _, p := range pulled {
		e, ok := live[p.Key]
		if !ok {
			continue
		}
		o := seen[p.Key]
		res.Sources[o.source].Events = append(res.Sources[o.source].Events, PulledEvent{
			ID: e.ID, Title: e.Title, StartsAt: e.StartsAt, EndsAt: e.EndsAt, Series: o.event.Series,
		})
	}
	return res, nil
}

// Skip stops the integration bringing in what sk names, and removes eventID, today's occurrence,
// in a user batch it announces and returns. The occurrence is forgotten rather than tombstoned,
// since the skip list keeps it out, so bringing the series back brings today's back too. The skip
// itself isn't in the batch: undoing it brings back today's occurrence, and settings bring back
// the series.
func (r *Runner) Skip(ctx context.Context, id string, sk Skip, eventID int64) (int64, error) {
	if _, err := r.find(id); err != nil {
		return 0, err
	}
	sk.Key, sk.Title = strings.TrimSpace(sk.Key), strings.TrimSpace(sk.Title)
	if sk.Key == "" {
		return 0, fmt.Errorf("%w: key is required", service.ErrInvalid)
	}
	batch, err := r.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		return 0, err
	}
	if err := r.svc.ForgetEvent(ctx, batch, eventID); err != nil {
		return 0, err
	}
	s, err := load(ctx, r.db, id)
	if err != nil {
		return 0, err
	}
	if !slices.ContainsFunc(s.Skipped, func(old Skip) bool { return old.Key == sk.Key }) {
		s.Skipped = append(s.Skipped, sk)
	}
	if err := store(ctx, r.db, id, s); err != nil {
		return 0, err
	}
	return batch, r.publish(ctx, batch)
}

// Unskip brings back what key names. The next pull brings it in again.
func (r *Runner) Unskip(ctx context.Context, id, key string) (View, error) {
	in, err := r.find(id)
	if err != nil {
		return View{}, err
	}
	s, err := load(ctx, r.db, id)
	if err != nil {
		return View{}, err
	}
	s.Skipped = slices.DeleteFunc(s.Skipped, func(sk Skip) bool { return sk.Key == key })
	if err := store(ctx, r.db, id, s); err != nil {
		return View{}, err
	}
	return r.view(ctx, in)
}

func (r *Runner) publish(ctx context.Context, batch int64) error {
	acts, err := r.svc.BatchActivity(ctx, batch)
	if err != nil {
		return err
	}
	r.hub.PublishBatch(batch, stream.SourceUser, acts)
	return nil
}
