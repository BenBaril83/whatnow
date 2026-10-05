# whatnow Wrap-Up and Reconcile Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The user can close a day. Wrap-up shows what got done, suggests what to do with each unfinished task, applies the user's choices as one undoable batch, records a note for tomorrow, and has the light model write a short summary. A day that was never wrapped is reconciled the same way the next morning, and the next plan reads its summary and note.

**Architecture:** The service adds wrapping, summaries, a lookup of the last day before a date, and the tasks completed within a window. The planner adds `Review` (done, unfinished, one main-model call for suggestions, then a code fallback) and `Close` (the triage batch, the wrap, the light-model summary). `StartDay` closes an unwrapped last day before starting today. The plan context carries the last day's summary and note. `httpapi` adds the morning and wrap routes.

**Tech Stack:** Go 1.27 and everything from plans 1 to 3. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-05-whatnow-design.md` (Good morning, Wrap up, DaySummary)

**Where this sits:** plan 3b. Days and planning (plan 3) is merged. Later plans:
- 4. Agent chat
- 5. Web UI
- 6. Packaging and prompt evals

**Decisions this plan makes:**
- **Unfinished means planned and still live.** These are tasks in the day's latest plan that are open, not deleted, and not pushed past today. Unplanned tasks aren't reviewed; they stay in the tray.
- **Triage actions are `roll_over`, `push`, and `drop`.** Rolling over writes nothing. A push without a date defaults to tomorrow. All the writes share one user batch, so one undo takes back the whole triage. If any write fails, the batch is undone.
- **Wrapping isn't in a batch,** like starting a day. Undoing a pace change after a wrap restores pace and mood only, never the wrap or the summary.
- **The summary is plain text from the light model,** with no schema. If it fails, the day still wraps, with an empty summary.
- **Suggestions use the main model,** since they're planning judgment. A task the model skips is filled in as roll over. With no provider, every suggestion is roll over.
- **Morning reconciliation runs inside `POST /today/start`.** The body takes an optional `triage`. If there's no unwrapped last day, the triage is ignored, so retrying a start that half-failed stays safe.
- **The plan sees only the last day.** Its summary and note go into the plan's context. Plan 4 adds a seven-day window for the agent.
- **A wrapped day is never replanned.** The triage batch is published after the wrap, so the trigger watcher sees a finished day.

## Global Constraints

- Module path: `github.com/jameynakama/whatnow`. Go directive: `go 1.27`.
- Code establishes facts and the LLM makes every judgment. The suggestions' and summary's fallbacks are code. Validation rejects only impossibilities.
- A skipped week gets one pass, not seven: only the last day started is reconciled.
- Main model for suggestions, with `Fallback` set. Light model for summaries.
- Every model call is bounded by `modelTimeout`.
- All API routes live under `/api/v1/`. Writes respond with `{batch_id, data}`.
- Tests use stdlib `testing` and `go-cmp`, with a real SQLite database in a temp dir. Tests never call a real provider.
- Commit messages: plain imperative subject, no `Co-Authored-By` trailer.

## Review Focus

1. **The user undoes a pace change after wrapping up.** Expected: the pace goes back, and the day stays wrapped with its note and summary. Pinned in Task 1.
2. **The model suggests a push to today or the past, a push with no date, a date on a drop, a task twice, or a task that isn't unfinished.** Expected: one retry listing every problem; anything still missing rolls over. Pinned in Task 2.
3. **One triage write fails part way** (a task deleted in another tab). Expected: nothing applied, and the day not wrapped. Pinned in Task 3.
4. **Wrap-up drops a must.** Expected: no replan of a finished day. Pinned in Task 3.
5. **Days were skipped, or start was retried after a half-failure.** Expected: only the last day is reconciled, and a retried start with stale triage doesn't touch tasks. Pinned in Tasks 2 and 3.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/store/migrations/0004_day_wrap.sql` | `tomorrow_note` and `summary` on days |
| `internal/domain/domain.go` | `Day.TomorrowNote`, `Day.Summary` |
| `internal/service/days.go` | `WrapDay`, `SetDaySummary`, `LastDayBefore`; `writeDay` narrowed to pace and mood |
| `internal/service/tasks.go` | `DoneBetween` |
| `internal/planner/review.go` | `Action`, `Suggestion`, `Review`, `WrapReview`, `MorningReview`, and the suggestions call |
| `internal/planner/close.go` | `Triage`, `Close`, `WrapUp`, the triage batch, and the summary call |
| `internal/planner/instructions.go` | Review and summary instructions |
| `internal/planner/planner.go` | The last day in state and context |
| `internal/planner/today.go` | `StartDay` reconciles first |
| `internal/planner/triggers.go` | Wrapped days are left alone |
| `internal/httpapi/days.go` | Morning, wrap review, and wrap-up handlers; start takes triage |

---


### Task 1: Wrapping days, summaries, and completions

Days gain a note for tomorrow and a summary. Wrapping and summaries aren't in batches, so `writeDay`, which undo uses, now writes only pace and mood. Before this, undoing a pace change after a wrap would have unwrapped the day. `DoneBetween` reads completions from the activity log, so an undone completion doesn't count.

**Files:**
- Create: `internal/store/migrations/0004_day_wrap.sql`
- Modify: `internal/domain/domain.go`
- Modify: `internal/service/days.go`
- Modify: `internal/service/tasks.go`
- Create: `internal/service/wrap_test.go`

**Interfaces:**
- Produces:
  - `domain.Day.TomorrowNote string` (json `tomorrow_note`) and `domain.Day.Summary string` (json `summary`)
  - `(*Service).WrapDay(ctx, dayID int64, note string) (domain.Day, error)`: `ErrConflict` if already wrapped, `ErrNotFound` if no such day. Trims the note.
  - `(*Service).SetDaySummary(ctx, dayID int64, summary string) error`
  - `(*Service).LastDayBefore(ctx, date domain.Date) (domain.Day, error)`: `ErrNotFound` if none.
  - `(*Service).DoneBetween(ctx, from, to time.Time) ([]domain.Task, error)`

- [ ] **Step 1: Write the failing tests**

`internal/service/wrap_test.go`. Create it with:

```go
package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestWrapDayOnceWithANote(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	day, _ := svc.StartDay(ctx, "2026-10-05", 3, "")

	wrapped, err := svc.WrapDay(ctx, day.ID, "  start with the RFC  ")
	if err != nil {
		t.Fatal(err)
	}
	if wrapped.WrappedAt == nil || wrapped.TomorrowNote != "start with the RFC" {
		t.Errorf("wrapped = %+v", wrapped)
	}
	if got, _ := svc.GetDay(ctx, "2026-10-05"); got.WrappedAt == nil || got.TomorrowNote != "start with the RFC" {
		t.Errorf("stored = %+v", got)
	}
	if _, err := svc.WrapDay(ctx, day.ID, ""); !errors.Is(err, service.ErrConflict) {
		t.Errorf("second wrap: err = %v, want ErrConflict", err)
	}
	if _, err := svc.WrapDay(ctx, 99, ""); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("missing day: err = %v, want ErrNotFound", err)
	}
}

func TestUndoingAPaceChangeKeepsTheDayWrapped(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	day, _ := svc.StartDay(ctx, "2026-10-05", 3, "")
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SetPace(ctx, batch, "2026-10-05", 1, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WrapDay(ctx, day.ID, "rest"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDaySummary(ctx, day.ID, "A slow day."); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetDay(ctx, "2026-10-05")
	if got.Pace != 3 || got.WrappedAt == nil || got.TomorrowNote != "rest" || got.Summary != "A slow day." {
		t.Errorf("after undo: %+v", got)
	}
}

func TestSetDaySummaryOnAMissingDay(t *testing.T) {
	svc, _ := newService(t)
	if err := svc.SetDaySummary(context.Background(), 99, "x"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLastDayBefore(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.LastDayBefore(ctx, "2026-10-05"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("no days: err = %v, want ErrNotFound", err)
	}
	svc.StartDay(ctx, "2026-09-28", 3, "")
	svc.StartDay(ctx, "2026-10-01", 3, "")
	svc.StartDay(ctx, "2026-10-05", 3, "")

	got, err := svc.LastDayBefore(ctx, "2026-10-05")
	if err != nil || got.Date != "2026-10-01" {
		t.Errorf("got %+v, err %v; want 2026-10-01", got, err)
	}
}

func TestDoneBetweenCountsOnlyLiveCompletionsInTheWindow(t *testing.T) {
	svc, clock := newService(t) // 09:00 UTC
	ctx := context.Background()
	done := func(task domain.Task) int64 {
		batch := newBatch(t, svc, domain.ActorUser)
		if _, err := svc.SetTaskStatus(ctx, batch, task.ID, domain.StatusDone); err != nil {
			t.Fatal(err)
		}
		return batch
	}
	before := createTask(t, svc, "done yesterday")
	done(before)
	clock.advance(24 * time.Hour)
	inside := createTask(t, svc, "done today")
	done(inside)
	undone := createTask(t, svc, "done then undone")
	if err := svc.Undo(ctx, done(undone)); err != nil {
		t.Fatal(err)
	}
	reopened := createTask(t, svc, "done then reopened")
	done(reopened)
	svc.SetTaskStatus(ctx, newBatch(t, svc, domain.ActorUser), reopened.ID, domain.StatusOpen)
	createTask(t, svc, "still open")

	from := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	got, err := svc.DoneBetween(ctx, from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != inside.ID {
		t.Errorf("done = %+v, want only %q", got, inside.Title)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/service/`
Expected: build fails: `svc.WrapDay undefined`

- [ ] **Step 3: Implement**

`internal/store/migrations/0004_day_wrap.sql`. Create it with:

```sql
ALTER TABLE days ADD COLUMN tomorrow_note TEXT NOT NULL DEFAULT '';
ALTER TABLE days ADD COLUMN summary TEXT NOT NULL DEFAULT '';
```

`internal/domain/domain.go`. Apply:

```diff
--- a/internal/domain/domain.go
+++ b/internal/domain/domain.go
@@ -130,6 +130,10 @@ type Day struct {
 	MoodNote  string     `json:"mood_note"`
 	StartedAt time.Time  `json:"started_at"`
 	WrappedAt *time.Time `json:"wrapped_at"`
+	// TomorrowNote is what the user left for the next day when wrapping up.
+	TomorrowNote string `json:"tomorrow_note"`
+	// Summary is the light model's short account of the day, written when it's wrapped.
+	Summary string `json:"summary"`
 }
 
 type Tier string
```

`internal/service/days.go`. Apply:

```diff
--- a/internal/service/days.go
+++ b/internal/service/days.go
@@ -6,6 +6,7 @@ import (
 	"encoding/json"
 	"errors"
 	"fmt"
+	"strings"
 
 	"github.com/jameynakama/whatnow/internal/domain"
 )
@@ -68,7 +69,7 @@ func (s *Service) SetPace(ctx context.Context, batch int64, date domain.Date, pa
 	return d, err
 }
 
-const dayColumns = `id, date, pace, mood_note, started_at, wrapped_at`
+const dayColumns = `id, date, pace, mood_note, started_at, wrapped_at, tomorrow_note, summary`
 
 func dayByDate(ctx context.Context, q querier, date domain.Date) (domain.Day, error) {
 	d, err := scanDay(q.QueryRowContext(ctx, `SELECT `+dayColumns+` FROM days WHERE date = ?`, date))
@@ -82,7 +83,7 @@ func scanDay(row scanner) (domain.Day, error) {
 	var d domain.Day
 	var startedAt string
 	var wrappedAt sql.NullString
-	if err := row.Scan(&d.ID, &d.Date, &d.Pace, &d.MoodNote, &startedAt, &wrappedAt); err != nil {
+	if err := row.Scan(&d.ID, &d.Date, &d.Pace, &d.MoodNote, &startedAt, &wrappedAt, &d.TomorrowNote, &d.Summary); err != nil {
 		return d, err
 	}
 	var err error
@@ -93,13 +94,62 @@ func scanDay(row scanner) (domain.Day, error) {
 	return d, err
 }
 
-// writeDay overwrites a day's editable columns. Undo uses it to restore a snapshot.
+// writeDay writes the columns a batch can change: pace and mood. Undo uses it to restore a
+// snapshot, so it must leave wrapping and the summary alone, which aren't in batches.
 func writeDay(ctx context.Context, tx *sql.Tx, d domain.Day) error {
-	_, err := tx.ExecContext(ctx, `UPDATE days SET pace = ?, mood_note = ?, wrapped_at = ? WHERE id = ?`,
-		d.Pace, d.MoodNote, formatNullTime(d.WrappedAt), d.ID)
+	_, err := tx.ExecContext(ctx, `UPDATE days SET pace = ?, mood_note = ? WHERE id = ?`, d.Pace, d.MoodNote, d.ID)
 	return err
 }
 
+// WrapDay closes a day, with an optional note for the next one. Like starting, it isn't in a
+// batch; what happens to the day's tasks is.
+func (s *Service) WrapDay(ctx context.Context, dayID int64, note string) (domain.Day, error) {
+	var d domain.Day
+	err := s.inTx(ctx, func(tx *sql.Tx) error {
+		var err error
+		if d, err = dayByID(ctx, tx, dayID); err != nil {
+			return err
+		}
+		if d.WrappedAt != nil {
+			return fmt.Errorf("%w: %s is already wrapped", ErrConflict, d.Date)
+		}
+		wrapped := s.now().UTC()
+		d.WrappedAt, d.TomorrowNote = &wrapped, strings.TrimSpace(note)
+		_, err = tx.ExecContext(ctx, `UPDATE days SET wrapped_at = ?, tomorrow_note = ? WHERE id = ?`,
+			formatTime(wrapped), d.TomorrowNote, d.ID)
+		return err
+	})
+	return d, err
+}
+
+func (s *Service) SetDaySummary(ctx context.Context, dayID int64, summary string) error {
+	res, err := s.db.ExecContext(ctx, `UPDATE days SET summary = ? WHERE id = ?`, summary, dayID)
+	if err != nil {
+		return err
+	}
+	if n, err := res.RowsAffected(); err != nil || n == 0 {
+		return errors.Join(err, fmt.Errorf("%w: day %d", ErrNotFound, dayID))
+	}
+	return nil
+}
+
+// LastDayBefore returns the most recent day started before date.
+func (s *Service) LastDayBefore(ctx context.Context, date domain.Date) (domain.Day, error) {
+	d, err := scanDay(s.db.QueryRowContext(ctx, `SELECT `+dayColumns+` FROM days WHERE date < ? ORDER BY date DESC LIMIT 1`, date))
+	if errors.Is(err, sql.ErrNoRows) {
+		return d, fmt.Errorf("%w: no day before %s", ErrNotFound, date)
+	}
+	return d, err
+}
+
+func dayByID(ctx context.Context, q querier, id int64) (domain.Day, error) {
+	d, err := scanDay(q.QueryRowContext(ctx, `SELECT `+dayColumns+` FROM days WHERE id = ?`, id))
+	if errors.Is(err, sql.ErrNoRows) {
+		return d, fmt.Errorf("%w: day %d", ErrNotFound, id)
+	}
+	return d, err
+}
+
 // SavePlan stores a new version of a day's plan and returns it with its ID and version.
 func (s *Service) SavePlan(ctx context.Context, p domain.Plan) (domain.Plan, error) {
 	if !p.Trigger.Valid() {
```

`internal/service/tasks.go`. Apply:

```diff
--- a/internal/service/tasks.go
+++ b/internal/service/tasks.go
@@ -7,6 +7,7 @@ import (
 	"errors"
 	"fmt"
 	"strings"
+	"time"
 
 	"github.com/jameynakama/whatnow/internal/domain"
 )
@@ -209,6 +210,32 @@ func (s *Service) ListTasks(ctx context.Context) ([]domain.Task, error) {
 	return tasks, rows.Err()
 }
 
+// DoneBetween returns tasks that are done now and were marked done in [from, to), by a batch
+// that hasn't been undone.
+func (s *Service) DoneBetween(ctx context.Context, from, to time.Time) ([]domain.Task, error) {
+	rows, err := s.db.QueryContext(ctx,
+		`SELECT `+taskColumns+` FROM tasks t
+		 WHERE t.deleted_at IS NULL AND t.status = ? AND EXISTS (
+		   SELECT 1 FROM activity a JOIN batches b ON b.id = a.batch_id
+		   WHERE a.entity = ? AND a.entity_id = t.id AND a.action = ? AND b.undone_at IS NULL
+		     AND json_extract(a.after_json, '$.status') = ? AND a.created_at >= ? AND a.created_at < ?)
+		 ORDER BY t.id`,
+		domain.StatusDone, entityTask, actionStatus, domain.StatusDone, formatTime(from), formatTime(to))
+	if err != nil {
+		return nil, err
+	}
+	defer rows.Close()
+	tasks := []domain.Task{}
+	for rows.Next() {
+		t, err := scanTask(rows)
+		if err != nil {
+			return nil, err
+		}
+		tasks = append(tasks, t)
+	}
+	return tasks, rows.Err()
+}
+
 const taskColumns = `id, title, notes, raw_text, size, energy, feeling, due_on, not_before, status, refs,
 	created_by, created_at, updated_at, deleted_at`
 
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/service/`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/domain.go internal/service/days.go internal/service/tasks.go internal/service/wrap_test.go internal/store/migrations/0004_day_wrap.sql
git commit -m "Wrap days, keep summaries, and find completions in a window"
```


### Task 2: Reviewing a day

A review lists what got done that day, what's unfinished from its plan, and one suggestion per unfinished task. Suggestions come from one main-model call through the existing `ask` helper, so a bad reply gets one retry with its problems. Any task still without a suggestion rolls over. With no unfinished tasks, the model isn't called.

**Files:**
- Create: `internal/planner/review.go`
- Modify: `internal/planner/instructions.go`
- Create: `internal/planner/review_test.go`

**Interfaces:**
- Consumes: `ask`, `state.request`, `gather`, `fallbackNote`, `modelTimeout`, `persona` from the planner; `DoneBetween`, `LatestPlan`, `GetTask`, `LastDayBefore` from the service; `facts.Eligible`.
- Produces:
  - `planner.Action` (`ActionRollOver` "roll_over", `ActionPush` "push", `ActionDrop` "drop") with `Valid()`
  - `planner.Suggestion{TaskID, Reason, Action, Until domain.Date}`, `planner.Review{Day, Done, Unfinished, Suggestions, Fallback}`
  - `(*Planner).WrapReview(ctx) (Review, error)`: `ErrNoDay` if today hasn't started.
  - `(*Planner).MorningReview(ctx) (*Review, error)`: nil when the last day was wrapped, or there is none.
  - unexported `(*Planner).unwrapped(ctx) (*domain.Day, error)` and `(*Planner).outcome(ctx, day) (done, unfinished []domain.Task, err error)`, which Task 3 uses.

- [ ] **Step 1: Write the failing tests**

`internal/planner/review_test.go`. Create it with:

```go
package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
)

// planDay saves a plan for a day with every given task as a should.
func (f *fixture) planDay(t *testing.T, day domain.Day, tasks ...domain.Task) {
	t.Helper()
	p := domain.Plan{DayID: day.ID, Trigger: domain.TriggerMorning}
	for _, task := range tasks {
		p.Entries = append(p.Entries, domain.PlanEntry{TaskID: task.ID, Tier: domain.TierShould})
	}
	if _, err := f.svc.SavePlan(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) finish(t *testing.T, task domain.Task) {
	t.Helper()
	f.write(t, func(b int64) error {
		_, err := f.svc.SetTaskStatus(context.Background(), b, task.ID, domain.StatusDone)
		return err
	})
}

func suggestion(id int64, action, until string) map[string]any {
	return map[string]any{"task_id": id, "reason": "because", "action": action, "until": until}
}

func TestWrapReviewSuggestsForEachUnfinishedTask(t *testing.T) {
	f := newFixture(t)
	shipped := f.task(t, service.TaskFields{Title: "ship it"})
	rfc := f.task(t, service.TaskFields{Title: "write the RFC"})
	taxes := f.task(t, service.TaskFields{Title: "do taxes"})
	f.planDay(t, f.start(t, 3, ""), shipped, rfc, taxes)
	f.finish(t, shipped)
	f.model.replies = []any{map[string]any{"suggestions": []any{
		suggestion(rfc.ID, "roll_over", ""), suggestion(taxes.ID, "push", "2026-10-12")}}}

	r, err := f.planner.WrapReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Done) != 1 || r.Done[0].ID != shipped.ID || len(r.Unfinished) != 2 || r.Fallback {
		t.Errorf("review = %+v", r)
	}
	want := []planner.Suggestion{{TaskID: rfc.ID, Reason: "because", Action: planner.ActionRollOver},
		{TaskID: taxes.ID, Reason: "because", Action: planner.ActionPush, Until: "2026-10-12"}}
	if diff := cmp.Diff(want, r.Suggestions); diff != "" {
		t.Errorf("suggestions (-want +got):\n%s", diff)
	}
	req := f.model.sent[0]
	if req.Model != "claude-sonnet-5-5" || !req.Fallback ||
		req.User != `I'm wrapping up 2026-10-05. Done: #1 "ship it". Suggest what to do with each unfinished task: #2 #3` {
		t.Errorf("request: %+v", req)
	}
}

func TestBadSuggestionsAreRetriedAndGapsRollOver(t *testing.T) {
	f := newFixture(t)
	a := f.task(t, service.TaskFields{Title: "a"})
	b := f.task(t, service.TaskFields{Title: "b"})
	c := f.task(t, service.TaskFields{Title: "c"})
	f.planDay(t, f.start(t, 3, ""), a, b, c)
	f.model.replies = []any{
		map[string]any{"suggestions": []any{suggestion(a.ID, "push", ""), suggestion(b.ID, "push", "2026-10-05"),
			suggestion(c.ID, "drop", "2026-10-09"), suggestion(c.ID, "roll_over", ""), suggestion(42, "drop", "")}},
		map[string]any{"suggestions": []any{suggestion(b.ID, "drop", "")}},
	}

	r, err := f.planner.WrapReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	retry := f.model.sent[1].User
	for _, want := range []string{`#1 is pushed to "", which is not a date after 2026-10-05`,
		`#2 is pushed to "2026-10-05"`, "#3 has a date but isn't a push", "#3 appears more than once",
		"#42 is not an unfinished task"} {
		if !strings.Contains(retry, want) {
			t.Errorf("retry is missing %q:\n%s", want, retry)
		}
	}
	got := []planner.Action{}
	for _, s := range r.Suggestions {
		got = append(got, s.Action)
	}
	if diff := cmp.Diff([]planner.Action{planner.ActionRollOver, planner.ActionDrop, planner.ActionRollOver}, got); diff != "" {
		t.Errorf("actions (-want +got):\n%s", diff)
	}
}

func TestReviewWithoutAProviderRollsEverythingOver(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	a := f.task(t, service.TaskFields{Title: "a"})
	f.planDay(t, f.start(t, 3, ""), a)

	r, err := f.planner.WrapReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Fallback || len(r.Suggestions) != 1 || r.Suggestions[0] != (planner.Suggestion{TaskID: a.ID, Action: planner.ActionRollOver}) {
		t.Errorf("review = %+v", r)
	}
}

func TestReviewSkipsTasksThatAreNoLongerUnfinished(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	done := f.task(t, service.TaskFields{Title: "done"})
	pushed := f.task(t, service.TaskFields{Title: "pushed"})
	gone := f.task(t, service.TaskFields{Title: "deleted"})
	dropped := f.task(t, service.TaskFields{Title: "dropped"})
	f.planDay(t, f.start(t, 3, ""), done, pushed, gone, dropped)
	f.finish(t, done)
	f.write(t, func(b int64) error { _, err := f.svc.PushTask(ctx, b, pushed.ID, "2026-10-08"); return err })
	f.write(t, func(b int64) error { return f.svc.DeleteTask(ctx, b, gone.ID) })
	f.write(t, func(b int64) error {
		_, err := f.svc.SetTaskStatus(ctx, b, dropped.ID, domain.StatusDropped)
		return err
	})

	r, err := f.planner.WrapReview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Unfinished) != 0 || len(r.Suggestions) != 0 || len(f.model.sent) != 0 {
		t.Errorf("review = %+v after %d model calls", r, len(f.model.sent))
	}
}

func TestWrapReviewNeedsAStartedDay(t *testing.T) {
	f := newFixture(t)
	if _, err := f.planner.WrapReview(context.Background()); !errors.Is(err, planner.ErrNoDay) {
		t.Errorf("err = %v, want ErrNoDay", err)
	}
}

func TestMorningReviewReconcilesOnlyAnUnwrappedLastDay(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	if r, err := f.planner.MorningReview(ctx); r != nil || err != nil {
		t.Errorf("first ever morning: %+v, %v", r, err)
	}

	a := f.task(t, service.TaskFields{Title: "a"})
	friday, _ := f.svc.StartDay(ctx, "2026-10-02", 3, "")
	f.planDay(t, friday, a)
	r, err := f.planner.MorningReview(ctx)
	if err != nil || r == nil || r.Day.Date != "2026-10-02" || len(r.Unfinished) != 1 {
		t.Fatalf("unwrapped friday: %+v, %v", r, err)
	}

	if _, err := f.svc.WrapDay(ctx, friday.ID, ""); err != nil {
		t.Fatal(err)
	}
	if r, err := f.planner.MorningReview(ctx); r != nil || err != nil {
		t.Errorf("wrapped friday: %+v, %v", r, err)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/planner/`
Expected: build fails: `f.planner.WrapReview undefined`

- [ ] **Step 3: Implement**

`internal/planner/review.go`. Create it with:

```go
package planner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	js "github.com/jameynakama/whatnow/internal/jsonschema"
	"github.com/jameynakama/whatnow/internal/service"
)

// Action is what happens to an unfinished task at the end of a day.
type Action string

const (
	ActionRollOver Action = "roll_over"
	ActionPush     Action = "push"
	ActionDrop     Action = "drop"
)

func (a Action) Valid() bool { return a == ActionRollOver || a == ActionPush || a == ActionDrop }

// Suggestion is the model's advice for one unfinished task. Until is set only for a push.
type Suggestion struct {
	TaskID int64       `json:"task_id"`
	Reason string      `json:"reason"`
	Action Action      `json:"action"`
	Until  domain.Date `json:"until"`
}

// Review is what wrap-up and the morning's reconcile show for a day: what got done, what's
// unfinished, and a suggestion for each unfinished task. Fallback means code made the suggestions.
type Review struct {
	Day         domain.Day    `json:"day"`
	Done        []domain.Task `json:"done"`
	Unfinished  []domain.Task `json:"unfinished"`
	Suggestions []Suggestion  `json:"suggestions"`
	Fallback    bool          `json:"fallback"`
}

var suggestSchema = js.Object(
	js.Prop("suggestions", js.Array(js.Object(
		js.Prop("task_id", js.Integer("The task's number, from #<id>.")),
		js.Prop("reason", js.String("One short line for the user on why.")),
		js.Prop("action", js.Enum("roll_over, push, or drop.", "roll_over", "push", "drop")),
		js.Prop("until", js.String("For a push, the date to push to as YYYY-MM-DD, after today. Otherwise empty.")),
	))),
)

type suggestReply struct {
	Suggestions []Suggestion `json:"suggestions"`
}

// WrapReview reviews today, for wrap-up. It returns ErrNoDay if today hasn't started.
func (p *Planner) WrapReview(ctx context.Context) (Review, error) {
	day, err := p.svc.GetDay(ctx, p.Date())
	if errors.Is(err, service.ErrNotFound) {
		return Review{}, ErrNoDay
	}
	if err != nil {
		return Review{}, err
	}
	return p.review(ctx, day)
}

// MorningReview reviews the last day before today if it was never wrapped up, so the morning can
// reconcile it. It returns nil when there is nothing to reconcile. However many days were
// skipped, only the last one started is reviewed.
func (p *Planner) MorningReview(ctx context.Context) (*Review, error) {
	day, err := p.unwrapped(ctx)
	if day == nil || err != nil {
		return nil, err
	}
	r, err := p.review(ctx, *day)
	return &r, err
}

// unwrapped returns the last day before today if it was never wrapped up, or nil.
func (p *Planner) unwrapped(ctx context.Context) (*domain.Day, error) {
	day, err := p.svc.LastDayBefore(ctx, p.Date())
	if errors.Is(err, service.ErrNotFound) || (err == nil && day.WrappedAt != nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &day, nil
}

func (p *Planner) review(ctx context.Context, day domain.Day) (Review, error) {
	r := Review{Day: day, Suggestions: []Suggestion{}}
	var err error
	if r.Done, r.Unfinished, err = p.outcome(ctx, day); err != nil {
		return r, err
	}
	if len(r.Unfinished) == 0 {
		return r, nil
	}
	st, err := p.gather(ctx, true)
	if err != nil {
		return r, err
	}
	reply, err := p.askSuggestions(ctx, st, r)
	if err != nil {
		fallbackNote(err)
		reply.Suggestions, r.Fallback = nil, true
	}
	given := map[int64]Suggestion{}
	for _, s := range reply.Suggestions {
		given[s.TaskID] = s
	}
	for _, t := range r.Unfinished {
		s, ok := given[t.ID]
		if !ok {
			s = Suggestion{TaskID: t.ID, Action: ActionRollOver}
		}
		r.Suggestions = append(r.Suggestions, s)
	}
	return r, nil
}

// outcome returns what got done on a day and what's still unfinished from its plan: open,
// not deleted, and not already pushed past today.
func (p *Planner) outcome(ctx context.Context, day domain.Day) (done, unfinished []domain.Task, err error) {
	start, err := time.ParseInLocation(time.DateOnly, string(day.Date), p.loc)
	if err != nil {
		return nil, nil, err
	}
	if done, err = p.svc.DoneBetween(ctx, start, start.AddDate(0, 0, 1)); err != nil {
		return nil, nil, err
	}
	unfinished = []domain.Task{}
	plan, err := p.svc.LatestPlan(ctx, day.ID)
	if errors.Is(err, service.ErrNotFound) {
		return done, unfinished, nil
	}
	if err != nil {
		return nil, nil, err
	}
	today := p.Date()
	for _, e := range plan.Entries {
		t, err := p.svc.GetTask(ctx, e.TaskID)
		if errors.Is(err, service.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if facts.Eligible(t, today) {
			unfinished = append(unfinished, t)
		}
	}
	return done, unfinished, nil
}

func (p *Planner) askSuggestions(ctx context.Context, st state, r Review) (suggestReply, error) {
	if st.model == nil {
		return suggestReply{}, st.modelErr
	}
	ctx, cancel := context.WithTimeout(ctx, modelTimeout)
	defer cancel()
	var user strings.Builder
	fmt.Fprintf(&user, "I'm wrapping up %s.", r.Day.Date)
	if len(r.Done) > 0 {
		user.WriteString(" Done:")
		for _, t := range r.Done {
			fmt.Fprintf(&user, " #%d %q.", t.ID, t.Title)
		}
	}
	user.WriteString(" Suggest what to do with each unfinished task:")
	for _, t := range r.Unfinished {
		fmt.Fprintf(&user, " #%d", t.ID)
	}
	req := st.request(reviewInstructions, user.String(), suggestSchema, "suggestions")
	return ask(ctx, st.model, req, func(s suggestReply) error {
		return checkSuggestions(s, r.Unfinished, st.facts.Today)
	})
}

func checkSuggestions(r suggestReply, unfinished []domain.Task, today domain.Date) error {
	open := map[int64]bool{}
	for _, t := range unfinished {
		open[t.ID] = true
	}
	var errs []error
	seen := map[int64]bool{}
	for _, s := range r.Suggestions {
		switch {
		case !open[s.TaskID]:
			errs = append(errs, fmt.Errorf("#%d is not an unfinished task", s.TaskID))
		case seen[s.TaskID]:
			errs = append(errs, fmt.Errorf("#%d appears more than once", s.TaskID))
		case !s.Action.Valid():
			errs = append(errs, fmt.Errorf("#%d has action %q", s.TaskID, s.Action))
		case s.Action == ActionPush && (!s.Until.Valid() || s.Until <= today):
			errs = append(errs, fmt.Errorf("#%d is pushed to %q, which is not a date after %s", s.TaskID, s.Until, today))
		case s.Action != ActionPush && s.Until != "":
			errs = append(errs, fmt.Errorf("#%d has a date but isn't a push", s.TaskID))
		}
		seen[s.TaskID] = true
	}
	return errors.Join(errs...)
}
```

`internal/planner/instructions.go`. Apply:

```diff
--- a/internal/planner/instructions.go
+++ b/internal/planner/instructions.go
@@ -23,3 +23,12 @@ They've asked "what now?". Pick the one task they should start right now from th
 - Weigh the plan, due dates, the free time before their next event, their pace, and how they feel. A short gap before a meeting suits a small task. A low pace or a rough mood suits something lighter.
 - Unplanned tasks can be picked too.
 - reason: one or two sentences, to them, on why this task now. Be concrete and kind.`
+
+const reviewInstructions = persona + `
+
+They're wrapping up a day, and some planned tasks didn't get done. For each unfinished task, suggest one action. Reply only with JSON that matches the schema. They decide; you're prefilling the form.
+
+- roll_over: it stays open and comes back into tomorrow's planning. The default for anything still worth doing soon.
+- push: put it off until a date. Use it when the task can't or shouldn't happen for a while. until is that date, after today.
+- drop: stop carrying it. Suggest it gently, for tasks that no longer matter or keep getting pushed with no sign of happening.
+- reason: one short line, kind and concrete. No guilt about what didn't get done.`
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/planner/`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/planner/instructions.go internal/planner/review.go internal/planner/review_test.go
git commit -m "Review a day's unfinished tasks with suggestions"
```


### Task 3: Closing a day

`Close` checks the triage, applies it as one user batch (undone whole if any write fails), wraps the day, publishes the batch, and writes the summary. `StartDay` closes an unwrapped last day first. The plan context opens with the last day's summary and note. The trigger watcher skips a wrapped day. `httpapi` passes `nil` triage for now, and Task 4 wires the body field.

**Files:**
- Create: `internal/planner/close.go`
- Modify: `internal/planner/instructions.go`
- Modify: `internal/planner/planner.go`
- Modify: `internal/planner/today.go`
- Modify: `internal/planner/triggers.go`
- Modify: `internal/httpapi/days.go`
- Create: `internal/planner/close_test.go`
- Modify: `internal/planner/plan_test.go`

**Interfaces:**
- Consumes: Task 1's service methods; Task 2's `unwrapped`, `outcome`, `Action`.
- Produces:
  - `planner.Triage{TaskID, Action, Until *domain.Date}`
  - `(*Planner).Close(ctx, day domain.Day, triage []Triage, note string) (batch int64, domain.Day, error)`: `service.ErrInvalid` for a bad action, a duplicate, or a bad date. `service.ErrConflict` if already wrapped.
  - `(*Planner).StartDay(ctx, pace, moodNote string, triage []Triage) (domain.Day, domain.Plan, error)`. It gains the `triage` argument.

- [ ] **Step 1: Write the failing tests**

`internal/planner/close_test.go`. Create it with:

```go
package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

func until(d string) *domain.Date { v := domain.Date(d); return &v }

func TestCloseAppliesTriageWrapsAndSummarizes(t *testing.T) {
	f := newFixture(t, "A steady day: shipped it, and the RFC rolls on.")
	ctx := context.Background()
	shipped := f.task(t, service.TaskFields{Title: "ship it"})
	rfc := f.task(t, service.TaskFields{Title: "write the RFC"})
	taxes := f.task(t, service.TaskFields{Title: "do taxes"})
	gym := f.task(t, service.TaskFields{Title: "gym"})
	old := f.task(t, service.TaskFields{Title: "old idea"})
	day := f.start(t, 3, "a bit tired")
	f.planDay(t, day, shipped, rfc, taxes, gym, old)
	f.finish(t, shipped)
	msgs, unsub := f.hub.Subscribe()
	defer unsub()

	batch, wrapped, err := f.planner.Close(ctx, day, []planner.Triage{
		{TaskID: rfc.ID, Action: planner.ActionRollOver},
		{TaskID: taxes.ID, Action: planner.ActionPush, Until: until("2026-10-12")},
		{TaskID: gym.ID, Action: planner.ActionPush},
		{TaskID: old.ID, Action: planner.ActionDrop},
	}, "start with the RFC")
	if err != nil {
		t.Fatal(err)
	}

	if wrapped.WrappedAt == nil || wrapped.TomorrowNote != "start with the RFC" ||
		wrapped.Summary != "A steady day: shipped it, and the RFC rolls on." {
		t.Errorf("wrapped = %+v", wrapped)
	}
	if stored, _ := f.svc.GetDay(ctx, today); stored.Summary != wrapped.Summary {
		t.Errorf("stored summary = %q", stored.Summary)
	}
	check := func(task domain.Task, status domain.Status, notBefore string) {
		t.Helper()
		got, _ := f.svc.GetTask(ctx, task.ID)
		nb := ""
		if got.NotBefore != nil {
			nb = string(*got.NotBefore)
		}
		if got.Status != status || nb != notBefore {
			t.Errorf("%s: status %s, not_before %q", task.Title, got.Status, nb)
		}
	}
	check(rfc, domain.StatusOpen, "")
	check(taxes, domain.StatusOpen, "2026-10-12")
	check(gym, domain.StatusOpen, "2026-10-06")
	check(old, domain.StatusDropped, "")

	m := <-msgs
	if applied, ok := m.Data.(stream.BatchApplied); !ok || applied.BatchID != batch || len(applied.Activity) != 3 {
		t.Errorf("published %+v", m)
	}

	req := f.model.sent[0]
	if req.Model != "claude-haiku-4-5" || req.Schema != nil || req.Fallback {
		t.Errorf("summary request: %+v", req)
	}
	for _, want := range []string{"Day: Monday 2026-10-05.", `Pace: "steady"`, `On their mind that morning: "a bit tired".`,
		"Done: ship it.", "write the RFC (rolled over); do taxes (pushed to 2026-10-12); gym (pushed to 2026-10-06); old idea (dropped).",
		`Their note for next time: "start with the RFC".`} {
		if !strings.Contains(req.User, want) {
			t.Errorf("summary input is missing %q:\n%s", want, req.User)
		}
	}
}

func TestBadTriageChangesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.task(t, service.TaskFields{Title: "a"})
	day := f.start(t, 3, "")
	f.planDay(t, day, a)

	cases := map[string][]planner.Triage{
		"unknown action": {{TaskID: a.ID, Action: "postpone"}},
		"twice":          {{TaskID: a.ID, Action: planner.ActionDrop}, {TaskID: a.ID, Action: planner.ActionPush}},
		"bad date":       {{TaskID: a.ID, Action: planner.ActionPush, Until: until("next week")}},
	}
	for name, triage := range cases {
		if _, _, err := f.planner.Close(ctx, day, triage, ""); !errors.Is(err, service.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	// A write that fails part way undoes the ones before it.
	_, _, err := f.planner.Close(ctx, day, []planner.Triage{{TaskID: a.ID, Action: planner.ActionDrop},
		{TaskID: 99, Action: planner.ActionDrop}}, "")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("missing task: err = %v, want ErrNotFound", err)
	}
	if got, _ := f.svc.GetTask(ctx, a.ID); got.Status != domain.StatusOpen {
		t.Errorf("task left %s", got.Status)
	}
	if got, _ := f.svc.GetDay(ctx, today); got.WrappedAt != nil {
		t.Error("day was wrapped despite the failed triage")
	}
}

func TestCloseTwiceConflicts(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	day := f.start(t, 3, "")
	_, wrapped, err := f.planner.Close(context.Background(), day, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.planner.Close(context.Background(), wrapped, nil, ""); !errors.Is(err, service.ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestAFailedSummaryStillWrapsTheDay(t *testing.T) {
	for name, f := range map[string]*fixture{
		"provider error": newFixture(t, errors.New("overloaded")),
		"no provider":    func() *fixture { f := newFixture(t); f.configured = false; return f }(),
	} {
		day := f.start(t, 3, "")
		batch, wrapped, err := f.planner.Close(context.Background(), day, nil, "rest")
		if err != nil || batch != 0 || wrapped.WrappedAt == nil || wrapped.Summary != "" {
			t.Errorf("%s: batch %d, day %+v, err %v", name, batch, wrapped, err)
		}
	}
}

func TestStartDayReconcilesTheUnwrappedLastDay(t *testing.T) {
	f := newFixture(t, "Friday slipped; the RFC carried over.", map[string]any{"entries": []any{}, "opening": "Morning."})
	ctx := context.Background()
	rfc := f.task(t, service.TaskFields{Title: "write the RFC"})
	friday, _ := f.svc.StartDay(ctx, "2026-10-02", 2, "")
	f.planDay(t, friday, rfc)

	_, plan, err := f.planner.StartDay(ctx, 3, "", []planner.Triage{{TaskID: rfc.ID, Action: planner.ActionPush}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.GetDay(ctx, "2026-10-02")
	if got.WrappedAt == nil || got.Summary != "Friday slipped; the RFC carried over." {
		t.Errorf("friday = %+v", got)
	}
	if task, _ := f.svc.GetTask(ctx, rfc.ID); task.NotBefore == nil || *task.NotBefore != "2026-10-06" {
		t.Errorf("rfc not_before = %v", task.NotBefore)
	}
	// The plan sees what the summary says about Friday.
	if plan.Opening != "Morning." || !strings.Contains(system(f.model.sent[1]),
		"Last day they used this, 2026-10-02: Friday slipped; the RFC carried over.") {
		t.Errorf("plan %+v; system:\n%s", plan, system(f.model.sent[1]))
	}
}

func TestStartDayIgnoresTriageWithNothingToReconcile(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	a := f.task(t, service.TaskFields{Title: "a"})
	if _, _, err := f.planner.StartDay(context.Background(), 3, "", []planner.Triage{{TaskID: a.ID, Action: planner.ActionDrop}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.GetTask(context.Background(), a.ID); got.Status != domain.StatusOpen {
		t.Errorf("task = %s", got.Status)
	}
}

func TestThePlanSeesTheNoteLeftForNextTime(t *testing.T) {
	f := newFixture(t, map[string]any{"entries": []any{}, "opening": "hi"})
	ctx := context.Background()
	friday, _ := f.svc.StartDay(ctx, "2026-10-02", 3, "")
	f.svc.WrapDay(ctx, friday.ID, "call the vet first")
	f.start(t, 3, "")

	if _, err := f.planner.Replan(ctx, domain.TriggerMorning, ""); err != nil {
		t.Fatal(err)
	}
	if sys := system(f.model.sent[0]); !strings.Contains(sys, `The note they left for next time: "call the vet first"`) {
		t.Errorf("system prompt:\n%s", sys)
	}
}

func TestNoReplanAfterTheDayIsWrapped(t *testing.T) {
	f := newFixture(t)
	f.configured = false
	ctx := context.Background()
	must := f.task(t, service.TaskFields{Title: "must", DueOn: date("2026-10-05")})
	day := f.start(t, 3, "")
	if _, err := f.planner.Replan(ctx, domain.TriggerMorning, ""); err != nil {
		t.Fatal(err)
	}
	plans := f.run(t)

	// Dropping a must would replan a live day; at wrap-up it must not.
	if _, _, err := f.planner.Close(ctx, day, []planner.Triage{{TaskID: must.ID, Action: planner.ActionDrop}}, ""); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case m := <-plans:
			if p, ok := m.Data.(domain.Plan); ok {
				t.Fatalf("unexpected replan: %+v", p)
			}
		case <-timeout:
			return
		}
	}
}
```

`internal/planner/plan_test.go`. Apply:

```diff
--- a/internal/planner/plan_test.go
+++ b/internal/planner/plan_test.go
@@ -233,14 +233,14 @@ func TestStartingAgainMakesTheMissingFirstPlan(t *testing.T) {
 	// The day started, but its first plan was never saved (say the request was cancelled).
 	f.start(t, 2, "tired")
 
-	day, plan, err := f.planner.StartDay(ctx, 4, "ignored")
+	day, plan, err := f.planner.StartDay(ctx, 4, "ignored", nil)
 	if err != nil {
 		t.Fatal(err)
 	}
 	if day.Pace != 2 || plan.Version != 1 || plan.Trigger != domain.TriggerMorning {
 		t.Errorf("day %+v, plan %+v", day, plan)
 	}
-	if _, _, err := f.planner.StartDay(ctx, 3, ""); !errors.Is(err, service.ErrConflict) {
+	if _, _, err := f.planner.StartDay(ctx, 3, "", nil); !errors.Is(err, service.ErrConflict) {
 		t.Errorf("starting a planned day: err = %v, want ErrConflict", err)
 	}
 }
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test -race ./internal/planner/`
Expected: build fails: `undefined: planner.Triage`

- [ ] **Step 3: Implement**

`internal/planner/close.go`. Create it with:

```go
package planner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
)

// Triage is what the user decided for one unfinished task. A push with no Until goes to tomorrow.
type Triage struct {
	TaskID int64        `json:"task_id"`
	Action Action       `json:"action"`
	Until  *domain.Date `json:"until"`
}

const summaryTokens = 2048

// Close wraps up a day. It applies the triage as one user batch, wraps the day with the note, and
// has the light model write the day's summary. It returns the batch, or 0 when the triage changed
// nothing. A summary that fails is logged and left empty: it's context for later, never worth
// failing a wrap-up over.
func (p *Planner) Close(ctx context.Context, day domain.Day, triage []Triage, note string) (int64, domain.Day, error) {
	if day.WrappedAt != nil {
		return 0, day, fmt.Errorf("%w: %s is already wrapped", service.ErrConflict, day.Date)
	}
	if err := p.checkTriage(triage); err != nil {
		return 0, day, err
	}
	done, unfinished, err := p.outcome(ctx, day)
	if err != nil {
		return 0, day, err
	}
	batch, err := p.applyTriage(ctx, triage)
	if err != nil {
		return 0, day, err
	}
	wrapped, err := p.svc.WrapDay(ctx, day.ID, note)
	if err != nil {
		if batch != 0 {
			err = errors.Join(err, p.svc.Undo(ctx, batch))
		}
		return 0, day, err
	}
	// Published after the wrap, so the replan watcher sees a finished day and leaves it alone.
	if batch != 0 {
		acts, err := p.svc.BatchActivity(ctx, batch)
		if err != nil {
			return 0, wrapped, err
		}
		p.hub.PublishBatch(batch, acts)
	}

	if summary, err := p.summarize(ctx, wrapped, done, unfinished, triage); err != nil {
		fallbackNote(err)
	} else if err := p.svc.SetDaySummary(ctx, wrapped.ID, summary); err != nil {
		return batch, wrapped, err
	} else {
		wrapped.Summary = summary
	}
	return batch, wrapped, nil
}

func (p *Planner) checkTriage(triage []Triage) error {
	seen := map[int64]bool{}
	for _, t := range triage {
		switch {
		case !t.Action.Valid():
			return fmt.Errorf("%w: task %d has action %q", service.ErrInvalid, t.TaskID, t.Action)
		case seen[t.TaskID]:
			return fmt.Errorf("%w: task %d is triaged twice", service.ErrInvalid, t.TaskID)
		case t.Until != nil && !t.Until.Valid():
			return fmt.Errorf("%w: task %d has until %q", service.ErrInvalid, t.TaskID, *t.Until)
		}
		seen[t.TaskID] = true
	}
	return nil
}

// applyTriage writes every push and drop in one user batch. If any write fails, the batch is
// undone, so the triage applies whole or not at all.
func (p *Planner) applyTriage(ctx context.Context, triage []Triage) (int64, error) {
	var batch int64
	for _, t := range triage {
		if t.Action == ActionRollOver {
			continue
		}
		var err error
		if batch == 0 {
			if batch, err = p.svc.NewBatch(ctx, domain.ActorUser); err != nil {
				return 0, err
			}
		}
		switch t.Action {
		case ActionPush:
			until := p.tomorrow()
			if t.Until != nil {
				until = *t.Until
			}
			_, err = p.svc.PushTask(ctx, batch, t.TaskID, until)
		case ActionDrop:
			_, err = p.svc.SetTaskStatus(ctx, batch, t.TaskID, domain.StatusDropped)
		}
		if err != nil {
			return 0, errors.Join(err, p.svc.Undo(ctx, batch))
		}
	}
	return batch, nil
}

func (p *Planner) tomorrow() domain.Date {
	return domain.Date(p.now().In(p.loc).AddDate(0, 0, 1).Format(time.DateOnly))
}

// summarize asks the light model for a few sentences about the day, for later context.
func (p *Planner) summarize(ctx context.Context, day domain.Day, done, unfinished []domain.Task, triage []Triage) (string, error) {
	model, cfg, _, err := p.client(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, modelTimeout)
	defer cancel()

	var b strings.Builder
	date, _ := time.Parse(time.DateOnly, string(day.Date))
	fmt.Fprintf(&b, "Day: %s. Pace: %q, %d of 5.", date.Format("Monday 2006-01-02"), day.Pace.Label(), day.Pace)
	if day.MoodNote != "" {
		fmt.Fprintf(&b, " On their mind that morning: %q.", day.MoodNote)
	}
	start, _ := time.ParseInLocation(time.DateOnly, string(day.Date), p.loc)
	events, err := p.svc.ListEvents(ctx, start, start.AddDate(0, 0, 1))
	if err != nil {
		return "", err
	}
	b.WriteString("\nEvents:")
	writeTitles(&b, len(events), func(i int) string { return events[i].Title })
	b.WriteString("\nDone:")
	writeTitles(&b, len(done), func(i int) string { return done[i].Title })
	actions := map[int64]Triage{}
	for _, t := range triage {
		actions[t.TaskID] = t
	}
	b.WriteString("\nLeft unfinished:")
	writeTitles(&b, len(unfinished), func(i int) string {
		t := unfinished[i]
		switch a := actions[t.ID]; a.Action {
		case ActionPush:
			until := p.tomorrow()
			if a.Until != nil {
				until = *a.Until
			}
			return fmt.Sprintf("%s (pushed to %s)", t.Title, until)
		case ActionDrop:
			return t.Title + " (dropped)"
		}
		return t.Title + " (rolled over)"
	})
	if day.TomorrowNote != "" {
		fmt.Fprintf(&b, "\nTheir note for next time: %q.", day.TomorrowNote)
	}

	reply, err := model.Complete(ctx, llm.Request{
		Model:     cfg.LightModel,
		System:    []llm.Block{{Text: summaryInstructions, Cache: true}},
		User:      b.String(),
		MaxTokens: summaryTokens,
	})
	return strings.TrimSpace(reply), err
}

func writeTitles(b *strings.Builder, n int, title func(int) string) {
	if n == 0 {
		b.WriteString(" none.")
		return
	}
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(';')
		}
		fmt.Fprintf(b, " %s", title(i))
	}
	b.WriteByte('.')
}
```

`internal/planner/instructions.go`. Apply:

```diff
--- a/internal/planner/instructions.go
+++ b/internal/planner/instructions.go
@@ -24,6 +24,8 @@ They've asked "what now?". Pick the one task they should start right now from th
 - Unplanned tasks can be picked too.
 - reason: one or two sentences, to them, on why this task now. Be concrete and kind.`
 
+const summaryInstructions = `Summarize one day of someone's work, for your own future reference when you help them plan later days. They have ADHD, and you're their planning assistant. Write two to four plain sentences: what got done, what slipped and what happened to it, and how they seemed to be doing. Be factual and kind. No headings, no lists, no advice.`
+
 const reviewInstructions = persona + `
 
 They're wrapping up a day, and some planned tasks didn't get done. For each unfinished task, suggest one action. Reply only with JSON that matches the schema. They decide; you're prefilling the form.
```

`internal/planner/planner.go`. Apply:

```diff
--- a/internal/planner/planner.go
+++ b/internal/planner/planner.go
@@ -53,6 +53,8 @@ func New(svc *service.Service, hub *stream.Hub, client ClientFunc, loc *time.Loc
 // state is what one call sees. day and plan are nil when today hasn't started or has no plan yet.
 // model is nil when no client could be built, and modelErr says why.
 type state struct {
+	// last is the most recent day before today, if any.
+	last     *domain.Day
 	day      *domain.Day
 	plan     *domain.Plan
 	facts    facts.Facts
@@ -84,6 +86,11 @@ func (p *Planner) gather(ctx context.Context, withModel bool) (state, error) {
 	case !errors.Is(err, service.ErrNotFound):
 		return st, err
 	}
+	if last, err := p.svc.LastDayBefore(ctx, today); err == nil {
+		st.last = &last
+	} else if !errors.Is(err, service.ErrNotFound) {
+		return st, err
+	}
 
 	if withModel {
 		st.model, st.cfg, st.settings, st.modelErr = p.client(ctx)
@@ -126,6 +133,16 @@ func (p *Planner) gather(ctx context.Context, withModel bool) (state, error) {
 // context renders the live context layer: pace and mood, the facts, and the current plan.
 func (st state) context() string {
 	var b strings.Builder
+	if st.last != nil && (st.last.Summary != "" || st.last.TomorrowNote != "") {
+		fmt.Fprintf(&b, "Last day they used this, %s:", st.last.Date)
+		if st.last.Summary != "" {
+			fmt.Fprintf(&b, " %s", st.last.Summary)
+		}
+		if st.last.TomorrowNote != "" {
+			fmt.Fprintf(&b, "\nThe note they left for next time: %q", st.last.TomorrowNote)
+		}
+		b.WriteString("\n\n")
+	}
 	if st.day != nil {
 		fmt.Fprintf(&b, "Pace today: %d of 5, %q.\n", st.day.Pace, st.day.Pace.Label())
 		if st.day.MoodNote != "" {
```

`internal/planner/today.go`. Apply:

```diff
--- a/internal/planner/today.go
+++ b/internal/planner/today.go
@@ -42,10 +42,21 @@ func (p *Planner) Today(ctx context.Context) (Today, error) {
 	return t, nil
 }
 
-// StartDay starts today at a pace and makes its first plan. If today started but its first plan
-// was never saved (the request was cancelled, say), starting again makes that plan and keeps the
-// pace already chosen. A day that has a plan can't start again.
-func (p *Planner) StartDay(ctx context.Context, pace domain.Pace, moodNote string) (domain.Day, domain.Plan, error) {
+// StartDay starts today at a pace and makes its first plan. First, if the last day was never
+// wrapped up, it closes that day with the triage from the morning's reconcile; with no such day,
+// the triage is ignored. If today started but its first plan was never saved (the request was
+// cancelled, say), starting again makes that plan and keeps the pace already chosen. A day that
+// has a plan can't start again.
+func (p *Planner) StartDay(ctx context.Context, pace domain.Pace, moodNote string, triage []Triage) (domain.Day, domain.Plan, error) {
+	last, err := p.unwrapped(ctx)
+	if err != nil {
+		return domain.Day{}, domain.Plan{}, err
+	}
+	if last != nil {
+		if _, _, err := p.Close(ctx, *last, triage, ""); err != nil {
+			return domain.Day{}, domain.Plan{}, err
+		}
+	}
 	day, err := p.svc.StartDay(ctx, p.Date(), pace, moodNote)
 	if started := err; errors.Is(started, service.ErrConflict) {
 		if day, err = p.svc.GetDay(ctx, p.Date()); err == nil {
```

`internal/planner/triggers.go`. Apply:

```diff
--- a/internal/planner/triggers.go
+++ b/internal/planner/triggers.go
@@ -51,8 +51,8 @@ func (p *Planner) Run(ctx context.Context, msgs <-chan stream.Message) {
 	}
 }
 
-// reasons says why one hub message calls for a replan. Only a day that already has a plan is
-// replanned; before that, the morning makes the first one.
+// reasons says why one hub message calls for a replan. Only a day that has a plan and isn't
+// wrapped up is replanned; before that, the morning makes the first plan.
 func (p *Planner) reasons(ctx context.Context, m stream.Message) (domain.Trigger, []string) {
 	var acts []domain.Activity
 	undone := false
@@ -71,7 +71,7 @@ func (p *Planner) reasons(ctx context.Context, m stream.Message) (domain.Trigger
 	}
 	today := facts.Today(p.now(), p.loc)
 	day, err := p.svc.GetDay(ctx, today)
-	if err != nil {
+	if err != nil || day.WrappedAt != nil {
 		return "", nil
 	}
 	plan, err := p.svc.LatestPlan(ctx, day.ID)
```

`internal/httpapi/days.go`. Apply:

```diff
--- a/internal/httpapi/days.go
+++ b/internal/httpapi/days.go
@@ -31,7 +31,7 @@ func (s *Server) startDay(w http.ResponseWriter, r *http.Request) {
 		writeError(w, err)
 		return
 	}
-	day, plan, err := s.Planner.StartDay(r.Context(), body.Pace, body.MoodNote)
+	day, plan, err := s.Planner.StartDay(r.Context(), body.Pace, body.MoodNote, nil)
 	if err != nil {
 		writeError(w, err)
 		return
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test -race ./internal/planner/`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/days.go internal/planner/close.go internal/planner/close_test.go internal/planner/instructions.go internal/planner/plan_test.go internal/planner/planner.go internal/planner/today.go internal/planner/triggers.go
git commit -m "Close a day: triage, wrap, and summary; reconcile in the morning"
```


### Task 4: Morning and wrap-up routes

The routes the good-morning and wrap-up screens need. The morning review is `{review: null}` when there is nothing to reconcile. Wrap-up answers with the usual mutation shape, so the UI can offer undo on the triage batch.

**Files:**
- Modify: `internal/planner/close.go`
- Modify: `internal/httpapi/days.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/helpers_test.go`
- Create: `internal/httpapi/wrap_test.go`

**Interfaces:**
- Consumes: Tasks 2 and 3.
- Produces:
  - `(*Planner).WrapUp(ctx, triage, note) (int64, domain.Day, error)`: `ErrNoDay` if today hasn't started.
  - `GET /api/v1/morning` → `{review: Review|null}`
  - `POST /api/v1/today/start` `{pace, mood_note, triage?}`
  - `GET /api/v1/today/wrap` → `Review` (409 before the day starts)
  - `POST /api/v1/today/wrap` `{triage, note}` → `{batch_id, data: Day}`. 400 for bad triage, 409 if already wrapped or not started.
  - `testAPI.svc` in the httpapi test helpers, for state the API can't create, such as earlier days.

- [ ] **Step 1: Write the failing tests**

`internal/httpapi/helpers_test.go`. Apply:

```diff
--- a/internal/httpapi/helpers_test.go
+++ b/internal/httpapi/helpers_test.go
@@ -24,9 +24,11 @@ import (
 var testLoc = time.FixedZone("UTC-5", -5*3600)
 
 type testAPI struct {
-	t        *testing.T
-	handler  http.Handler
-	hub      *stream.Hub
+	t       *testing.T
+	handler http.Handler
+	hub     *stream.Hub
+	// svc sets up state the API can't, such as days before today.
+	svc      *service.Service
 	captured []int64
 	// model answers the connection test and the planner. Nil means no provider is set up.
 	model llm.Client
@@ -41,7 +43,7 @@ func newAPI(t *testing.T) *testAPI {
 	t.Cleanup(func() { db.Close() })
 	now := func() time.Time { return time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC) } // 09:00 local
 	svc := service.New(db, now)
-	api := &testAPI{t: t, hub: stream.NewHub()}
+	api := &testAPI{t: t, hub: stream.NewHub(), svc: svc}
 	active := func(context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
 		if api.model == nil {
 			return nil, settings.ProviderConfig{}, settings.Defaults(), settings.ErrNotConfigured
```

`internal/httpapi/wrap_test.go`. Create it with:

```go
package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
)

type wrapped struct {
	BatchID int64      `json:"batch_id"`
	Data    domain.Day `json:"data"`
}

func TestWrapUpToday(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodGet, "/api/v1/today/wrap", nil), http.StatusConflict)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/wrap", map[string]any{}), http.StatusConflict)

	rfc := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "write the RFC"}))
	old := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "old idea"}))
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3}), http.StatusCreated)

	// No provider: suggestions roll everything over.
	review := decodeAs[planner.Review](t, api.do(http.MethodGet, "/api/v1/today/wrap", nil))
	if len(review.Unfinished) != 2 || len(review.Suggestions) != 2 || !review.Fallback {
		t.Errorf("review = %+v", review)
	}

	rec := api.do(http.MethodPost, "/api/v1/today/wrap", map[string]any{
		"triage": []any{map[string]any{"task_id": old.Data.ID, "action": "drop"},
			map[string]any{"task_id": rfc.Data.ID, "action": "push", "until": "2026-10-07"}},
		"note": "coffee first",
	})
	wantStatus(t, rec, http.StatusOK)
	got := decodeAs[wrapped](t, rec)
	if got.BatchID == 0 || got.Data.WrappedAt == nil || got.Data.TomorrowNote != "coffee first" {
		t.Errorf("wrap = %+v", got)
	}

	// One undo takes back the whole triage.
	wantStatus(t, api.do(http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/undo", got.BatchID), nil), http.StatusNoContent)
	for _, task := range decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil)) {
		if task.Status != domain.StatusOpen || task.NotBefore != nil {
			t.Errorf("after undo: %+v", task)
		}
	}

	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/wrap", map[string]any{}), http.StatusConflict)
}

func TestWrapUpRejectsBadTriage(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3}), http.StatusCreated)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/wrap", map[string]any{"triage": []any{
		map[string]any{"task_id": 1, "action": "later"}}}), http.StatusBadRequest)
	if today := decodeAs[planner.Today](t, api.do(http.MethodGet, "/api/v1/today", nil)); today.Day.WrappedAt != nil {
		t.Error("a rejected wrap-up wrapped the day")
	}
}

func TestMorningReconcilesAnUnwrappedDay(t *testing.T) {
	api := newAPI(t)
	ctx := context.Background()
	morning := func() *planner.Review {
		t.Helper()
		return decodeAs[struct {
			Review *planner.Review `json:"review"`
		}](t, api.do(http.MethodGet, "/api/v1/morning", nil)).Review
	}
	if r := morning(); r != nil {
		t.Errorf("nothing to reconcile, got %+v", r)
	}

	rfc := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "write the RFC"}))
	friday, err := api.svc.StartDay(ctx, "2026-10-02", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	api.svc.SavePlan(ctx, domain.Plan{DayID: friday.ID, Trigger: domain.TriggerMorning,
		Entries: []domain.PlanEntry{{TaskID: rfc.Data.ID, Tier: domain.TierMust}}})
	r := morning()
	if r == nil || r.Day.Date != "2026-10-02" || len(r.Unfinished) != 1 || r.Suggestions[0].Action != planner.ActionRollOver {
		t.Fatalf("review = %+v", r)
	}

	wantStatus(t, api.do(http.MethodPost, "/api/v1/today/start", map[string]any{"pace": 3,
		"triage": []any{map[string]any{"task_id": rfc.Data.ID, "action": "drop"}}}), http.StatusCreated)
	if r := morning(); r != nil {
		t.Errorf("friday should be reconciled, got %+v", r)
	}
	if got, _ := api.svc.GetTask(ctx, rfc.Data.ID); got.Status != domain.StatusDropped {
		t.Errorf("rfc = %s", got.Status)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./... && go vet ./...`
Expected: FAIL: the new routes answer `404 page not found` (for example `status = 404, want 409`)

- [ ] **Step 3: Implement**

`internal/planner/close.go`. Apply:

```diff
--- a/internal/planner/close.go
+++ b/internal/planner/close.go
@@ -66,6 +66,18 @@ func (p *Planner) Close(ctx context.Context, day domain.Day, triage []Triage, no
 	return batch, wrapped, nil
 }
 
+// WrapUp closes today. It returns ErrNoDay if today hasn't started.
+func (p *Planner) WrapUp(ctx context.Context, triage []Triage, note string) (int64, domain.Day, error) {
+	day, err := p.svc.GetDay(ctx, p.Date())
+	if errors.Is(err, service.ErrNotFound) {
+		return 0, domain.Day{}, ErrNoDay
+	}
+	if err != nil {
+		return 0, domain.Day{}, err
+	}
+	return p.Close(ctx, day, triage, note)
+}
+
 func (p *Planner) checkTriage(triage []Triage) error {
 	seen := map[int64]bool{}
 	for _, t := range triage {
```

`internal/httpapi/days.go`. Apply:

```diff
--- a/internal/httpapi/days.go
+++ b/internal/httpapi/days.go
@@ -4,6 +4,7 @@ import (
 	"net/http"
 
 	"github.com/jameynakama/whatnow/internal/domain"
+	"github.com/jameynakama/whatnow/internal/planner"
 )
 
 func (s *Server) today(w http.ResponseWriter, r *http.Request) {
@@ -21,17 +22,62 @@ type Started struct {
 	Plan domain.Plan `json:"plan"`
 }
 
+// Morning is what the good-morning flow reconciles. Review is nil when the last day was wrapped
+// up, or there is no last day.
+type Morning struct {
+	Review *planner.Review `json:"review"`
+}
+
+func (s *Server) morning(w http.ResponseWriter, r *http.Request) {
+	review, err := s.Planner.MorningReview(r.Context())
+	if err != nil {
+		writeError(w, err)
+		return
+	}
+	writeJSON(w, http.StatusOK, Morning{Review: review})
+}
+
+func (s *Server) wrapReview(w http.ResponseWriter, r *http.Request) {
+	review, err := s.Planner.WrapReview(r.Context())
+	if err != nil {
+		writeError(w, err)
+		return
+	}
+	writeJSON(w, http.StatusOK, review)
+}
+
+// wrapUp closes today: the triage as one undoable batch, the note for tomorrow, and the summary.
+// The batch ID is 0 when the triage changed nothing.
+func (s *Server) wrapUp(w http.ResponseWriter, r *http.Request) {
+	var body struct {
+		Triage []planner.Triage `json:"triage"`
+		Note   string           `json:"note"`
+	}
+	if err := decode(r, &body); err != nil {
+		writeError(w, err)
+		return
+	}
+	batch, day, err := s.Planner.WrapUp(r.Context(), body.Triage, body.Note)
+	if err != nil {
+		writeError(w, err)
+		return
+	}
+	writeJSON(w, http.StatusOK, Mutation{BatchID: batch, Data: day})
+}
+
 // startDay starts today and waits for its first plan, so the morning flow can reveal it.
 func (s *Server) startDay(w http.ResponseWriter, r *http.Request) {
 	var body struct {
 		Pace     domain.Pace `json:"pace"`
 		MoodNote string      `json:"mood_note"`
+		// Triage settles the last day's unfinished tasks if it was never wrapped up.
+		Triage []planner.Triage `json:"triage"`
 	}
 	if err := decode(r, &body); err != nil {
 		writeError(w, err)
 		return
 	}
-	day, plan, err := s.Planner.StartDay(r.Context(), body.Pace, body.MoodNote, nil)
+	day, plan, err := s.Planner.StartDay(r.Context(), body.Pace, body.MoodNote, body.Triage)
 	if err != nil {
 		writeError(w, err)
 		return
```

`internal/httpapi/server.go`. Apply:

```diff
--- a/internal/httpapi/server.go
+++ b/internal/httpapi/server.go
@@ -66,7 +66,10 @@ func New(d Deps) http.Handler {
 	mux.HandleFunc("PUT /api/v1/events/{id}", s.updateEvent)
 	mux.HandleFunc("DELETE /api/v1/events/{id}", s.deleteEvent)
 	mux.HandleFunc("GET /api/v1/today", s.today)
+	mux.HandleFunc("GET /api/v1/morning", s.morning)
 	mux.HandleFunc("POST /api/v1/today/start", s.startDay)
+	mux.HandleFunc("GET /api/v1/today/wrap", s.wrapReview)
+	mux.HandleFunc("POST /api/v1/today/wrap", s.wrapUp)
 	mux.HandleFunc("PUT /api/v1/today/pace", s.setPace)
 	mux.HandleFunc("POST /api/v1/whatnow", s.whatNow)
 	mux.HandleFunc("GET /api/v1/stream", s.stream)
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./... && go vet ./...`
Expected: PASS. Also run `gofmt -l .`; it should print nothing.

- [ ] **Step 5: Run the whole suite with the race detector**

Run: `go test -race ./... && just cover`
Expected: PASS. Total coverage around 84.6%; what's left uncovered is database-error paths.

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi/days.go internal/httpapi/helpers_test.go internal/httpapi/server.go internal/httpapi/wrap_test.go internal/planner/close.go
git commit -m "Add morning reconcile and wrap-up endpoints"
```
