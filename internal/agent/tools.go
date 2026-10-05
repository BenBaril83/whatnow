package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	js "github.com/jameynakama/whatnow/internal/jsonschema"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

// tool is one function the model can call. run returns the chip label the user sees and the
// result the model reads; an error goes back to the model as a failed call.
type tool struct {
	name, description string
	schema            map[string]any
	run               func(ctx context.Context, t *turn, input json.RawMessage) (label string, result any, err error)
}

// args decodes a tool's input. Strict schemas make a mismatch unlikely, but not impossible.
func args[T any](input json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(input, &v); err != nil {
		return v, fmt.Errorf("%w: the input doesn't match the schema: %v", service.ErrInvalid, err)
	}
	return v, nil
}

// localTime is how the model writes times: the user's wall clock, no zone.
const localTime = "2006-01-02T15:04"

var (
	taskID   = js.Integer("The task's number, from #<id>.")
	eventID  = js.Integer("The event's number, from [event id].")
	dateProp = func(what string) map[string]any { return js.String(what + " as YYYY-MM-DD, or empty for none.") }

	// taskProps are create_task's fields. An empty string leaves a field unset.
	taskProps = []js.Field{
		js.Prop("title", js.String("Short and specific, starting with a verb when it fits.")),
		js.Prop("notes", js.String("Details worth keeping, or empty.")),
		js.Prop("size", js.Enum("S (under an hour), M (one to three hours), L (more), or empty if unclear.", "S", "M", "L", "")),
		js.Prop("energy", js.Enum("How much it takes out of them, or empty if unclear.", "low", "med", "high", "")),
		js.Prop("feeling", js.String("How they feel about it, in their words, or empty.")),
		js.Prop("due_on", dateProp("When it's due,")),
		js.Prop("not_before", dateProp("The first day it can be done,")),
	}
	changes = func(fields ...string) map[string]any {
		return js.Array(js.Object(
			js.Prop("field", js.Enum("The field to change.", fields...)),
			js.Prop("value", js.String("The new value, in the same form as when creating. Empty clears it.")),
		))
	}
)

var tools = []tool{
	{"list_tasks", "List tasks with one status, including ones the context leaves out: done, dropped, and put off until later.",
		js.Object(js.Prop("status", js.Enum("Which tasks to list.", "open", "done", "dropped"))), listTasks},
	{"create_task", "Add a task. It waits in the unplanned tray until the next replan.", js.Object(taskProps...), createTask},
	{"update_task", "Change some of a task's fields. To put a task off, use push_task instead.",
		js.Object(js.Prop("task_id", taskID),
			js.Prop("changes", changes("title", "notes", "size", "energy", "feeling", "due_on", "not_before"))), updateTask},
	{"complete_task", "Mark a task done.", js.Object(js.Prop("task_id", taskID)), statusTool(domain.StatusDone, "Done")},
	{"drop_task", "Stop carrying a task without doing it.", js.Object(js.Prop("task_id", taskID)), statusTool(domain.StatusDropped, "Dropped")},
	{"push_task", "Put a task off until a later date. Pushes are counted.",
		js.Object(js.Prop("task_id", taskID), js.Prop("until", js.String("The first day to see it again, as YYYY-MM-DD."))), pushTask},
	{"add_blocker", "Record that a task can't start until another is finished.",
		js.Object(js.Prop("task_id", js.Integer("The task that waits.")), js.Prop("blocked_by_id", js.Integer("The task it waits on."))), addBlocker},
	{"remove_blocker", "Remove a blocker.",
		js.Object(js.Prop("task_id", js.Integer("The task that waits.")), js.Prop("blocked_by_id", js.Integer("The task it waits on."))), removeBlocker},
	{"create_event", "Add something at a fixed time.",
		js.Object(js.Prop("title", js.String("What it is.")),
			js.Prop("starts_at", js.String("Local time, as YYYY-MM-DDTHH:MM.")),
			js.Prop("ends_at", js.String("Local time, as YYYY-MM-DDTHH:MM, or empty if it has no set end."))), createEvent},
	{"update_event", "Change some of an event's fields.",
		js.Object(js.Prop("event_id", eventID), js.Prop("changes", changes("title", "starts_at", "ends_at"))), updateEvent},
	{"delete_event", "Remove an event.", js.Object(js.Prop("event_id", eventID)), deleteEvent},
	{"replan", "Make a new version of today's plan. Use it when what they've told you changes what today should hold.",
		js.Object(js.Prop("reason", js.String("What changed, in a few words."))), replan},
	{"what_now", "Pick the one task they should start right now.", js.Object(), whatNow},
	{"set_pace", "Change today's pace. The plan updates on its own.",
		js.Object(js.Prop("level", js.Integer("1 running on fumes, 2 gentle, 3 steady, 4 full steam, 5 go hard.")),
			js.Prop("note", js.String("What's on their mind, if they said, or empty."))), setPace},
	{"undo_last", "Take back the most recent change you or the automatic cleanup made, as a whole. Changes from this reply are left alone.",
		js.Object(), undoLast},
}

func toolDefs() []llm.Tool {
	defs := make([]llm.Tool, len(tools))
	for i, t := range tools {
		defs[i] = llm.Tool{Name: t.name, Description: t.description, Schema: t.schema}
	}
	return defs
}

// runTool runs one call and records its chip. The model gets the result as JSON, or the error.
func (t *turn) runTool(ctx context.Context, c llm.ToolCall) llm.ToolResult {
	use := domain.ToolUse{Tool: c.Name}
	result := llm.ToolResult{CallID: c.ID}
	label, out, err := t.dispatch(ctx, c)
	if err == nil {
		var b []byte
		if b, err = json.Marshal(out); err == nil {
			use.Label, result.Content = label, string(b)
		}
	}
	if err != nil {
		use.Label, use.Error = c.Name, err.Error()
		result.Content, result.IsError = err.Error(), true
	}
	t.uses = append(t.uses, use)
	t.agent.hub.Publish(stream.Message{Type: "chat.tool", Data: use})
	return result
}

func (t *turn) dispatch(ctx context.Context, c llm.ToolCall) (string, any, error) {
	for _, tl := range tools {
		if tl.name == c.Name {
			return tl.run(ctx, t, c.Input)
		}
	}
	return "", nil, fmt.Errorf("there is no tool %q", c.Name)
}

// brief is a task as the model reads it in tool results.
type brief struct {
	ID        int64          `json:"id"`
	Title     string         `json:"title"`
	Status    domain.Status  `json:"status"`
	Notes     string         `json:"notes,omitempty"`
	Size      *domain.Size   `json:"size,omitempty"`
	Energy    *domain.Energy `json:"energy,omitempty"`
	Feeling   string         `json:"feeling,omitempty"`
	DueOn     *domain.Date   `json:"due_on,omitempty"`
	NotBefore *domain.Date   `json:"not_before,omitempty"`
}

func briefOf(t domain.Task) brief {
	return brief{ID: t.ID, Title: t.Title, Status: t.Status, Notes: t.Notes, Size: t.Size, Energy: t.Energy,
		Feeling: t.Feeling, DueOn: t.DueOn, NotBefore: t.NotBefore}
}

func name(t domain.Task) string { return fmt.Sprintf("#%d %q", t.ID, t.Title) }

func listTasks(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		Status domain.Status `json:"status"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	all, err := t.agent.svc.ListTasks(ctx)
	if err != nil {
		return "", nil, err
	}
	out := []brief{}
	for _, task := range all {
		if task.Status == in.Status {
			out = append(out, briefOf(task))
		}
	}
	return fmt.Sprintf("Looked at %s tasks", in.Status), out, nil
}

// taskArgs are create_task's input. Empty strings are unset fields.
type taskArgs struct {
	Title     string `json:"title"`
	Notes     string `json:"notes"`
	Size      string `json:"size"`
	Energy    string `json:"energy"`
	Feeling   string `json:"feeling"`
	DueOn     string `json:"due_on"`
	NotBefore string `json:"not_before"`
}

func optional[T ~string](s string) *T {
	if s == "" {
		return nil
	}
	v := T(s)
	return &v
}

func createTask(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[taskArgs](input)
	if err != nil {
		return "", nil, err
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	task, err := t.agent.svc.CreateTask(ctx, batch, service.TaskFields{
		Title: in.Title, Notes: in.Notes, Size: optional[domain.Size](in.Size), Energy: optional[domain.Energy](in.Energy),
		Feeling: in.Feeling, DueOn: optional[domain.Date](in.DueOn), NotBefore: optional[domain.Date](in.NotBefore),
	})
	if err != nil {
		return "", nil, err
	}
	return "Added " + name(task), briefOf(task), nil
}

type change struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

func updateTask(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		TaskID  int64    `json:"task_id"`
		Changes []change `json:"changes"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	svc := t.agent.svc
	task, err := svc.GetTask(ctx, in.TaskID)
	if err != nil {
		return "", nil, err
	}
	f := service.TaskFields{Title: task.Title, Notes: task.Notes, Size: task.Size, Energy: task.Energy, Feeling: task.Feeling,
		DueOn: task.DueOn, NotBefore: task.NotBefore, Refs: task.Refs}
	for _, c := range in.Changes {
		switch c.Field {
		case "title":
			f.Title = c.Value
		case "notes":
			f.Notes = c.Value
		case "size":
			f.Size = optional[domain.Size](c.Value)
		case "energy":
			f.Energy = optional[domain.Energy](c.Value)
		case "feeling":
			f.Feeling = c.Value
		case "due_on":
			f.DueOn = optional[domain.Date](c.Value)
		case "not_before":
			f.NotBefore = optional[domain.Date](c.Value)
		default:
			return "", nil, fmt.Errorf("%w: a task has no field %q", service.ErrInvalid, c.Field)
		}
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	task, err = svc.UpdateTask(ctx, batch, in.TaskID, f)
	if err != nil {
		return "", nil, err
	}
	return "Updated " + name(task), briefOf(task), nil
}

func statusTool(status domain.Status, verb string) func(context.Context, *turn, json.RawMessage) (string, any, error) {
	return func(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
		in, err := args[struct {
			TaskID int64 `json:"task_id"`
		}](input)
		if err != nil {
			return "", nil, err
		}
		batch, err := t.batch(ctx)
		if err != nil {
			return "", nil, err
		}
		task, err := t.agent.svc.SetTaskStatus(ctx, batch, in.TaskID, status)
		if err != nil {
			return "", nil, err
		}
		return verb + ": " + name(task), briefOf(task), nil
	}
}

func pushTask(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		TaskID int64       `json:"task_id"`
		Until  domain.Date `json:"until"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	if in.Until <= t.agent.today() {
		return "", nil, fmt.Errorf("%w: until must be after today, %s", service.ErrInvalid, t.agent.today())
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	task, err := t.agent.svc.PushTask(ctx, batch, in.TaskID, in.Until)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("Pushed %s to %s", name(task), in.Until), briefOf(task), nil
}

func blockerArgs(input json.RawMessage) (domain.Blocker, error) {
	in, err := args[struct {
		TaskID      int64 `json:"task_id"`
		BlockedByID int64 `json:"blocked_by_id"`
	}](input)
	return domain.Blocker{TaskID: in.TaskID, BlockedByID: in.BlockedByID}, err
}

func addBlocker(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	b, err := blockerArgs(input)
	if err != nil {
		return "", nil, err
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := t.agent.svc.AddBlocker(ctx, batch, b); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("#%d now waits on #%d", b.TaskID, b.BlockedByID), b, nil
}

func removeBlocker(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	b, err := blockerArgs(input)
	if err != nil {
		return "", nil, err
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := t.agent.svc.RemoveBlocker(ctx, batch, b); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("#%d no longer waits on #%d", b.TaskID, b.BlockedByID), b, nil
}

// parseLocal reads one of the model's local times. An empty string is no time.
func (a *Agent) parseLocal(field, s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	v, err := time.ParseInLocation(localTime, s, a.loc)
	if err != nil {
		return nil, fmt.Errorf("%w: %s %q is not YYYY-MM-DDTHH:MM", service.ErrInvalid, field, s)
	}
	return &v, nil
}

// eventBrief is an event as the model reads it, in local time.
type eventBrief struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
}

func (a *Agent) eventBrief(e domain.Event) eventBrief {
	b := eventBrief{ID: e.ID, Title: e.Title, StartsAt: e.StartsAt.In(a.loc).Format(localTime)}
	if e.EndsAt != nil {
		b.EndsAt = e.EndsAt.In(a.loc).Format(localTime)
	}
	return b
}

func createEvent(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		Title    string `json:"title"`
		StartsAt string `json:"starts_at"`
		EndsAt   string `json:"ends_at"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	a := t.agent
	f := service.EventFields{Title: in.Title}
	start, err := a.parseLocal("starts_at", in.StartsAt)
	if err != nil {
		return "", nil, err
	}
	if start != nil {
		f.StartsAt = *start
	}
	if f.EndsAt, err = a.parseLocal("ends_at", in.EndsAt); err != nil {
		return "", nil, err
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	e, err := a.svc.CreateEvent(ctx, batch, f)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("Added %q at %s", e.Title, e.StartsAt.In(a.loc).Format("Mon 15:04")), a.eventBrief(e), nil
}

func updateEvent(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		EventID int64    `json:"event_id"`
		Changes []change `json:"changes"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	a := t.agent
	e, err := a.svc.GetEvent(ctx, in.EventID)
	if err != nil {
		return "", nil, err
	}
	f := service.EventFields{Title: e.Title, StartsAt: e.StartsAt, EndsAt: e.EndsAt}
	for _, c := range in.Changes {
		switch c.Field {
		case "title":
			f.Title = c.Value
		case "starts_at":
			start, err := a.parseLocal("starts_at", c.Value)
			if err != nil {
				return "", nil, err
			}
			if start == nil {
				return "", nil, fmt.Errorf("%w: starts_at can't be cleared", service.ErrInvalid)
			}
			f.StartsAt = *start
		case "ends_at":
			if f.EndsAt, err = a.parseLocal("ends_at", c.Value); err != nil {
				return "", nil, err
			}
		default:
			return "", nil, fmt.Errorf("%w: an event has no field %q", service.ErrInvalid, c.Field)
		}
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	if e, err = a.svc.UpdateEvent(ctx, batch, in.EventID, f); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("Updated %q", e.Title), a.eventBrief(e), nil
}

func deleteEvent(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		EventID int64 `json:"event_id"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	a := t.agent
	e, err := a.svc.GetEvent(ctx, in.EventID)
	if err != nil {
		return "", nil, err
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := a.svc.DeleteEvent(ctx, batch, in.EventID); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("Removed %q", e.Title), map[string]bool{"deleted": true}, nil
}

func replan(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		Reason string `json:"reason"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	plan, err := t.agent.planner.Replan(ctx, domain.TriggerAgent, in.Reason)
	if err != nil {
		return "", nil, err
	}
	return "Replanned the day", struct {
		Opening  string             `json:"opening"`
		Entries  []domain.PlanEntry `json:"entries"`
		Fallback bool               `json:"fallback"`
	}{plan.Opening, plan.Entries, plan.Fallback}, nil
}

func whatNow(ctx context.Context, t *turn, _ json.RawMessage) (string, any, error) {
	pick, err := t.agent.planner.WhatNow(ctx, 0)
	if err != nil {
		return "", nil, err
	}
	task, err := t.agent.svc.GetTask(ctx, pick.TaskID)
	if err != nil {
		return "", nil, err
	}
	return "Picked " + name(task), pick, nil
}

func setPace(ctx context.Context, t *turn, input json.RawMessage) (string, any, error) {
	in, err := args[struct {
		Level domain.Pace `json:"level"`
		Note  string      `json:"note"`
	}](input)
	if err != nil {
		return "", nil, err
	}
	a := t.agent
	if _, err := a.svc.GetDay(ctx, a.today()); errors.Is(err, service.ErrNotFound) {
		return "", nil, planner.ErrNoDay
	}
	batch, err := t.batch(ctx)
	if err != nil {
		return "", nil, err
	}
	day, err := a.svc.SetPace(ctx, batch, a.today(), in.Level, strings.TrimSpace(in.Note))
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("Pace set to %q", day.Pace.Label()), day, nil
}

func undoLast(ctx context.Context, t *turn, _ json.RawMessage) (string, any, error) {
	a := t.agent
	batch, err := a.svc.LastUndoableBatch(ctx, domain.ActorAgent, t.batchID)
	if err != nil {
		return "", nil, err
	}
	acts, err := a.svc.BatchActivity(ctx, batch)
	if err != nil {
		return "", nil, err
	}
	if err := a.svc.Undo(ctx, batch); err != nil {
		return "", nil, err
	}
	a.hub.PublishUndone(batch)
	return "Undid the last change", struct {
		Undone []domain.Activity `json:"undone"`
	}{acts}, nil
}
