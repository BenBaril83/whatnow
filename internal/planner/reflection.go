package planner

import (
	"context"
	"strings"
	"sync"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/prompt"
)

// reflect turns the day's summary into a message to the user, in the voice they set.
func (p *Planner) reflect(ctx context.Context, summary string) (string, error) {
	model, cfg, s, err := p.client(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, modelTimeout)
	defer cancel()
	reply, err := model.Complete(ctx, llm.Request{
		Model:     cfg.LightModel,
		System:    prompt.System(reflectionInstructions, s.Style, s.AboutMe, ""),
		User:      summary,
		MaxTokens: summaryTokens,
	})
	return strings.TrimSpace(reply), err
}

// writeReflection writes the day's reflection from its summary and saves it.
func (p *Planner) writeReflection(ctx context.Context, day domain.Day) (string, error) {
	r, err := p.reflect(ctx, day.Summary)
	if err != nil {
		return "", err
	}
	return r, p.svc.SetDayReflection(ctx, day.ID, r)
}

// fills keeps one reflection writer per day in this process. busy days are being written;
// gaveUp days failed a lazy fill and aren't retried, so a bad key doesn't cost a failing model
// call on every reload.
type fills struct {
	mu           sync.Mutex
	busy, gaveUp map[int64]bool
}

func newFills() *fills {
	return &fills{busy: map[int64]bool{}, gaveUp: map[int64]bool{}}
}

// hold marks a day busy whatever its state. Wrap-up uses it.
func (f *fills) hold(dayID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy[dayID] = true
}

// claim marks a day busy unless it already is, or was given up on.
func (f *fills) claim(dayID int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy[dayID] || f.gaveUp[dayID] {
		return false
	}
	f.busy[dayID] = true
	return true
}

func (f *fills) release(dayID int64, gaveUp bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.busy, dayID)
	if gaveUp {
		f.gaveUp[dayID] = true
	}
}
