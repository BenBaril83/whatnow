package cleanup

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

// result mirrors schema.
type result struct {
	Original original  `json:"original"`
	NewTasks []newTask `json:"new_tasks"`
	Events   []event   `json:"events"`
	Blockers []blocker `json:"blockers"`
}

type taskOut struct {
	Title     string       `json:"title"`
	Notes     string       `json:"notes"`
	Size      string       `json:"size"`
	Energy    string       `json:"energy"`
	Feeling   string       `json:"feeling"`
	DueOn     string       `json:"due_on"`
	NotBefore string       `json:"not_before"`
	Refs      []domain.Ref `json:"refs"`
}

type original struct {
	Keep bool `json:"keep"`
	taskOut
}

type newTask struct {
	Ref string `json:"ref"`
	taskOut
}

type event struct {
	Title    string `json:"title"`
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
}

type blocker struct {
	Task      string `json:"task"`
	BlockedBy string `json:"blocked_by"`
}

// fields turns the model's empty strings into unset fields. The service validates the rest.
func (t taskOut) fields() service.TaskFields {
	return service.TaskFields{
		Title:     t.Title,
		Notes:     t.Notes,
		Size:      optional[domain.Size](t.Size),
		Energy:    optional[domain.Energy](t.Energy),
		Feeling:   t.Feeling,
		DueOn:     optional[domain.Date](t.DueOn),
		NotBefore: optional[domain.Date](t.NotBefore),
		Refs:      t.Refs,
	}
}

func optional[T ~string](s string) *T {
	if s == "" {
		return nil
	}
	v := T(s)
	return &v
}

const localTime = "2006-01-02T15:04"

// fields reads the model's local times in the user's time zone.
func (e event) fields(loc *time.Location) (service.EventFields, error) {
	start, err := time.ParseInLocation(localTime, e.StartsAt, loc)
	if err != nil {
		return service.EventFields{}, fmt.Errorf("event %q: starts_at %q is not YYYY-MM-DDTHH:MM", e.Title, e.StartsAt)
	}
	f := service.EventFields{Title: e.Title, StartsAt: start}
	if e.EndsAt != "" {
		end, err := time.ParseInLocation(localTime, e.EndsAt, loc)
		if err != nil {
			return service.EventFields{}, fmt.Errorf("event %q: ends_at %q is not YYYY-MM-DDTHH:MM", e.Title, e.EndsAt)
		}
		f.EndsAt = &end
	}
	return f, nil
}

// resolve turns "original", a new task's ref, or "#<id>" into a task ID.
func resolve(ids map[string]int64, ref string) (int64, error) {
	if id, ok := ids[ref]; ok {
		return id, nil
	}
	if rest, ok := strings.CutPrefix(ref, "#"); ok {
		if id, err := strconv.ParseInt(rest, 10, 64); err == nil {
			return id, nil
		}
	}
	return 0, fmt.Errorf("unknown task reference %q", ref)
}
