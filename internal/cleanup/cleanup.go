// Package cleanup turns a freshly captured card into tidy tasks, events, and blockers. It makes
// one structured call to the light model and applies the result as one undoable agent batch.
package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/prompt"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/stream"
)

// ErrStale means the card was edited or deleted while cleanup ran, so cleanup left it alone.
var ErrStale = errors.New("task changed since capture")

// ClientFunc returns the client and settings for one run. It returns settings.ErrNotConfigured
// when no provider is set up.
type ClientFunc func(ctx context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error)

// Failed is the cleanup.failed payload.
type Failed struct {
	TaskID int64  `json:"task_id"`
	Error  string `json:"error"`
}

type Runner struct {
	svc    *service.Service
	hub    *stream.Hub
	client ClientFunc
	loc    *time.Location
	now    func() time.Time
	queue  chan int64
}

const (
	queueSize = 64
	// maxOpenTasks bounds how many existing tasks the model sees when looking for duplicates.
	maxOpenTasks = 200
	maxTokens    = 4096
)

func New(svc *service.Service, hub *stream.Hub, client ClientFunc, loc *time.Location, now func() time.Time) *Runner {
	return &Runner{svc: svc, hub: hub, client: client, loc: loc, now: now, queue: make(chan int64, queueSize)}
}

// Enqueue schedules cleanup for a captured task. It never blocks: when the queue is full the card
// stays raw, which is always a valid state.
func (r *Runner) Enqueue(taskID int64) {
	select {
	case r.queue <- taskID:
	default:
		log.Printf("cleanup queue full, task %d stays raw", taskID)
	}
}

// Run cleans queued tasks one at a time until ctx is done. A missing provider or a card the user
// already changed is not a failure; anything else is published as cleanup.failed.
func (r *Runner) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-r.queue:
			_, err := r.Clean(ctx, id)
			if err == nil || errors.Is(err, settings.ErrNotConfigured) || errors.Is(err, ErrStale) {
				continue
			}
			log.Printf("cleanup task %d: %v", id, err)
			r.hub.Publish(stream.Message{Type: "cleanup.failed", Data: Failed{TaskID: id, Error: err.Error()}})
		}
	}
}

// Clean runs cleanup on one task and returns the batch it applied. If any write fails, the batch
// is undone and the card is left exactly as it was.
func (r *Runner) Clean(ctx context.Context, taskID int64) (int64, error) {
	task, err := r.svc.GetTask(ctx, taskID)
	if err != nil {
		return 0, err
	}
	client, cfg, s, err := r.client(ctx)
	if err != nil {
		return 0, err
	}
	situation, err := r.situation(ctx, taskID)
	if err != nil {
		return 0, err
	}

	reply, err := client.Complete(ctx, llm.Request{
		Model:      cfg.LightModel,
		System:     prompt.System(instructions, "", s.AboutMe, situation),
		User:       task.RawText,
		Schema:     schema,
		SchemaName: "cleanup",
		MaxTokens:  maxTokens,
	})
	if err != nil {
		return 0, err
	}
	var res result
	if err := json.Unmarshal([]byte(reply), &res); err != nil {
		return 0, fmt.Errorf("parse reply: %w", err)
	}

	// Compare the whole card, not updated_at: two writes can land on the same clock tick.
	current, err := r.svc.GetTask(ctx, taskID)
	if errors.Is(err, service.ErrNotFound) || (err == nil && !reflect.DeepEqual(current, task)) {
		return 0, ErrStale
	}
	if err != nil {
		return 0, err
	}
	return r.apply(ctx, task, res)
}

// situation tells the model the date and time and which open tasks already exist.
func (r *Runner) situation(ctx context.Context, taskID int64) (string, error) {
	tasks, err := r.svc.ListTasks(ctx)
	if err != nil {
		return "", err
	}
	now := r.now().In(r.loc)
	var b strings.Builder
	fmt.Fprintf(&b, "Now: %s (%s)\n\n", now.Format("Monday 2006-01-02 15:04"), now.Format("MST, UTC-07:00"))
	b.WriteString("Existing open tasks, as #<id> title:\n")
	open := 0
	for i := len(tasks) - 1; i >= 0 && open < maxOpenTasks; i-- {
		if t := tasks[i]; t.ID != taskID && t.Status == domain.StatusOpen {
			fmt.Fprintf(&b, "#%d %s\n", t.ID, t.Title)
			open++
		}
	}
	if open == 0 {
		b.WriteString("(none)\n")
	}
	return b.String(), nil
}

func (r *Runner) apply(ctx context.Context, task domain.Task, res result) (int64, error) {
	batch, err := r.svc.NewBatch(ctx, domain.ActorAgent)
	if err != nil {
		return 0, err
	}
	if err := r.write(ctx, batch, task, res); err != nil {
		if undoErr := r.svc.Undo(ctx, batch); undoErr != nil {
			return 0, errors.Join(err, undoErr)
		}
		return 0, err
	}
	acts, err := r.svc.BatchActivity(ctx, batch)
	if err != nil {
		return 0, err
	}
	r.hub.PublishBatch(batch, acts)
	return batch, nil
}

func (r *Runner) write(ctx context.Context, batch int64, task domain.Task, res result) error {
	ids := map[string]int64{}
	if res.Original.Keep {
		if _, err := r.svc.UpdateTask(ctx, batch, task.ID, res.Original.fields()); err != nil {
			return err
		}
		ids["original"] = task.ID
	}
	for _, n := range res.NewTasks {
		if n.Ref == "original" || strings.HasPrefix(n.Ref, "#") || ids[n.Ref] != 0 {
			return fmt.Errorf("new task ref %q is reserved or used twice", n.Ref)
		}
		f := n.fields()
		f.RawText = task.RawText
		created, err := r.svc.CreateTask(ctx, batch, f)
		if err != nil {
			return err
		}
		ids[n.Ref] = created.ID
	}
	for _, e := range res.Events {
		f, err := e.fields(r.loc)
		if err != nil {
			return err
		}
		f.RawText = task.RawText
		if _, err := r.svc.CreateEvent(ctx, batch, f); err != nil {
			return err
		}
	}
	for _, b := range res.Blockers {
		waiting, err := resolve(ids, b.Task)
		if err != nil {
			return err
		}
		first, err := resolve(ids, b.BlockedBy)
		if err != nil {
			return err
		}
		if err := r.svc.AddBlocker(ctx, batch, domain.Blocker{TaskID: waiting, BlockedByID: first}); err != nil {
			return err
		}
	}
	if !res.Original.Keep {
		return r.svc.DeleteTask(ctx, batch, task.ID)
	}
	return nil
}
