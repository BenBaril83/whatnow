package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jameynakama/whatnow/internal/domain"
	js "github.com/jameynakama/whatnow/internal/jsonschema"
	"github.com/jameynakama/whatnow/internal/llm"
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

var (
	taskID   = js.Integer("The task's number, from #<id>.")
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
)

var tools = []tool{
	{"list_tasks", "List tasks with one status, including ones the context leaves out: done, dropped, and put off until later.",
		js.Object(js.Prop("status", js.Enum("Which tasks to list.", "open", "done", "dropped"))), listTasks},
	{"create_task", "Add a task. It waits in the unplanned tray until the next replan.", js.Object(taskProps...), createTask},
	{"complete_task", "Mark a task done.", js.Object(js.Prop("task_id", taskID)), statusTool(domain.StatusDone, "Done")},
	{"drop_task", "Stop carrying a task without doing it.", js.Object(js.Prop("task_id", taskID)), statusTool(domain.StatusDropped, "Dropped")},
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
