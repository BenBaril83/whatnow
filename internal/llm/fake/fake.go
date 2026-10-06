// Package fake is a model that answers by fixed rules, for the end-to-end tests. It reads task
// numbers out of the prompt, so its replies pass the same checks a real model's must.
package fake

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/jameynakama/whatnow/internal/llm"
)

// The fixed text the fake answers with. The Playwright tests look for these.
const (
	Opening   = "A fake plan, in order."
	Rationale = "Next in line."
	Reason    = "First free task."
	Suggest   = "Keep it going."
	Sentence  = "A fake summary."
	ChatReply = "A fake reply. Nothing changed."
)

// Client answers Complete by the request's schema name, and Chat with ChatReply.
type Client struct{}

func (Client) Complete(_ context.Context, req llm.Request) (string, error) {
	var reply any
	switch req.SchemaName {
	case "cleanup":
		reply = cleanup(req.User)
	case "plan":
		reply = plan(eligible(system(req.System)))
	case "pick":
		reply = pick(eligible(system(req.System)), req.User)
	case "suggestions":
		reply = suggestions(req.User)
	default:
		return Sentence, nil
	}
	b, err := json.Marshal(reply)
	return string(b), err
}

// Chat streams ChatReply a word at a time and never calls a tool.
func (Client) Chat(_ context.Context, _ llm.ChatRequest, onText func(string)) (llm.Reply, error) {
	for _, word := range strings.SplitAfter(ChatReply, " ") {
		onText(word)
	}
	return llm.Reply{Text: ChatReply}, nil
}

func system(blocks []llm.Block) string {
	var b strings.Builder
	for _, blk := range blocks {
		b.WriteString(blk.Text)
	}
	return b.String()
}

var splitter = regexp.MustCompile(`,|\band\b|\n`)

// cleanup keeps the note's first piece as the original and makes each other piece a new task.
func cleanup(raw string) map[string]any {
	var pieces []string
	for _, p := range splitter.Split(raw, -1) {
		if p = strings.TrimSpace(p); p != "" {
			pieces = append(pieces, p)
		}
	}
	if len(pieces) == 0 {
		pieces = []string{strings.TrimSpace(raw)}
	}
	original := task(pieces[0])
	original["keep"] = true
	newTasks := []map[string]any{}
	for i, p := range pieces[1:] {
		t := task(p)
		t["ref"] = "n" + strconv.Itoa(i+1)
		newTasks = append(newTasks, t)
	}
	return map[string]any{
		"original":  original,
		"new_tasks": newTasks,
		"events":    []any{},
		"blockers":  []any{},
	}
}

func task(title string) map[string]any {
	return map[string]any{
		"title": title, "notes": "", "size": "", "energy": "", "feeling": "",
		"due_on": "", "not_before": "", "refs": []any{},
	}
}

type fact struct {
	id      int64
	blocked bool
}

const eligibleHeading = "Eligible tasks, as #<id> title:\n"

var taskLine = regexp.MustCompile(`^#(\d+) `)

// eligible reads the facts' task list: "#<id> title" lines, each followed by indented notes such
// as "  - blocked by #3". The list ends at the first line that is neither.
func eligible(prompt string) []fact {
	_, list, _ := strings.Cut(prompt, eligibleHeading)
	var facts []fact
	for _, line := range strings.Split(list, "\n") {
		if m := taskLine.FindStringSubmatch(line); m != nil {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			facts = append(facts, fact{id: id})
			continue
		}
		if !strings.HasPrefix(line, "  ") || len(facts) == 0 {
			break
		}
		if strings.HasPrefix(line, "  - blocked by #") {
			facts[len(facts)-1].blocked = true
		}
	}
	return facts
}

// plan tiers tasks in the order the facts list them: a must, a should, then coulds.
func plan(facts []fact) map[string]any {
	tiers := []string{"must", "should"}
	entries := []map[string]any{}
	for i, f := range facts {
		tier := "could"
		if i < len(tiers) {
			tier = tiers[i]
		}
		entries = append(entries, map[string]any{"task_id": f.id, "rationale": Rationale, "tier": tier})
	}
	return map[string]any{"entries": entries, "opening": Opening}
}

var excluded = regexp.MustCompile(`Not #(\d+),`)

// pick is the first task that isn't blocked and isn't the one the user just passed on.
func pick(facts []fact, user string) map[string]any {
	var skip int64
	if m := excluded.FindStringSubmatch(user); m != nil {
		skip, _ = strconv.ParseInt(m[1], 10, 64)
	}
	var id int64
	for _, f := range facts {
		if !f.blocked && f.id != skip {
			id = f.id
			break
		}
	}
	return map[string]any{"reason": Reason, "task_id": id}
}

var taskRef = regexp.MustCompile(`#(\d+)`)

// suggestions rolls over every task the review asks about.
func suggestions(user string) map[string]any {
	_, asked, _ := strings.Cut(user, "unfinished task:")
	out := []map[string]any{}
	for _, m := range taskRef.FindAllStringSubmatch(asked, -1) {
		id, _ := strconv.ParseInt(m[1], 10, 64)
		out = append(out, map[string]any{"task_id": id, "reason": Suggest, "action": "roll_over", "until": ""})
	}
	return map[string]any{"suggestions": out}
}
