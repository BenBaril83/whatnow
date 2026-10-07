# Wrap-up Reflection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wrap-up keeps its third-person summary for the planner and adds a second-person reflection for the user, in their assistant's voice, shown on the goodnight screen and the Day's-done card.

**Architecture:** A new `days.reflection` column. The planner rewrites the finished summary into the reflection with one more light-model call under `prompt.System` (so `Style` and `AboutMe` apply), at wrap-up and lazily from `Planner.Today` when a wrapped day is missing one. A small per-process guard keeps it to one writer per day and stops a failed fill from retrying on every reload.

**Tech Stack:** Go, SQLite migrations in `internal/store/migrations`, SvelteKit, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-06-whatnow-reflection-design.md`

## Global Constraints

- The summary's prompt is unchanged and carries no style; only the reflection uses `prompt.System(reflectionInstructions, Style, AboutMe, "")`.
- The reflection is written from `day.Summary` only, never from raw facts.
- `Close` never writes a reflection. Only `WrapUp` and the lazy fill do.
- The user never sees the summary: the web shows `reflection`, or nothing when it's empty.
- The fake's reflection text is exactly `A fake reflection, to you.`
- Test names follow the repo: `TestWrapUpWritesASummaryThenAReflection`, not `TestShould...`.

## Review Focus

1. **The app closes while a lazy fill runs** (quitting the TUI right after opening it on a wrapped day): the fill's save fails on the closed database and is logged; nothing panics and quitting isn't delayed by more than the model call.
2. **The reflection comes back empty or only whitespace:** it's trimmed and saved as empty, the screens show nothing, and the lazy fill tries it once more in that process.
3. **The persona asks for a format the instructions forbid** (headings, lists, emoji spam): the instructions come first in `prompt.System` and win; nothing in code enforces it, so a reviewer should check the instruction text says so plainly.
4. **A day wrapped before this change, with no summary** (the summary call failed back then): no reflection is attempted, and no model call happens on reloads.
5. **Changing the persona after wrapping:** tonight's reflection keeps the old voice. That's expected; nothing regenerates it.

Items 2 and 4 get tests in Task 3.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/store/migrations/0006_day_reflection.sql` | The new column |
| `internal/domain/domain.go` | `Day.Reflection` |
| `internal/service/days.go` | Read the column; `SetDayReflection` |
| `internal/planner/instructions.go` | `reflectionInstructions` |
| `internal/planner/reflection.go` | `reflect`, `writeReflection`, the per-day guard, the lazy fill |
| `internal/planner/close.go` | `WrapUp` writes the reflection |
| `internal/planner/today.go` | `Today` starts the lazy fill |
| `internal/llm/fake/fake.go` | `Reflection` reply |
| `web/src/lib/api.ts`, `DaysDone.svelte`, `routes/(ritual)/wrap/+page.svelte` | Show the reflection |
| `web/e2e/*` | Goodnight shows the reflection |

---

### Task 1: Store the reflection

**Files:**
- Create: `internal/store/migrations/0006_day_reflection.sql`
- Modify: `internal/domain/domain.go` (the `Day` struct), `internal/service/days.go` (`dayColumns`, `scanDay`, new `SetDayReflection`)
- Test: `internal/service/days_test.go`

**Interfaces:**
- Produces: `domain.Day.Reflection string` (`json:"reflection"`); `func (s *Service) SetDayReflection(ctx context.Context, dayID int64, reflection string) error`, returning `ErrNotFound` for a missing day.

- [ ] **Step 1: Write the failing test**

Append to `internal/service/days_test.go`:

```go
func TestSetDayReflectionIsReadBack(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	day, err := svc.StartDay(ctx, "2026-10-05", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDayReflection(ctx, day.ID, "You got a lot done."); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetDay(ctx, "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if got.Reflection != "You got a lot done." {
		t.Errorf("reflection = %q", got.Reflection)
	}
	if err := svc.SetDayReflection(ctx, day.ID+1, "x"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("missing day: err = %v", err)
	}
}
```

Add `"errors"` to the imports if it isn't there.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/service/ -run SetDayReflection`
Expected: FAIL to compile, `SetDayReflection` undefined.

- [ ] **Step 3: Implement**

`internal/store/migrations/0006_day_reflection.sql`:

```sql
ALTER TABLE days ADD COLUMN reflection TEXT NOT NULL DEFAULT '';
```

In `internal/domain/domain.go`, replace the `Summary` field and its comment in `Day`:

```go
	// Summary is the light model's plain notes on the day, written when it's wrapped. The planner
	// reads it on later days; the user never sees it.
	Summary string `json:"summary"`
	// Reflection is the day told to the user, in their assistant's voice, written from Summary.
	Reflection string `json:"reflection"`
```

In `internal/service/days.go`:

```go
const dayColumns = `id, date, pace, mood_note, started_at, wrapped_at, tomorrow_note, summary, reflection`
```

In `scanDay`, scan the new column last:

```go
	if err := row.Scan(&d.ID, &d.Date, &d.Pace, &d.MoodNote, &startedAt, &wrappedAt, &d.TomorrowNote, &d.Summary, &d.Reflection); err != nil {
```

Change `writeDay`'s comment to mention the reflection: `so it must leave wrapping, the summary, and the reflection alone, which aren't in batches.`

After `SetDaySummary`, add:

```go
func (s *Service) SetDayReflection(ctx context.Context, dayID int64, reflection string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE days SET reflection = ? WHERE id = ?`, reflection, dayID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, fmt.Errorf("%w: day %d", ErrNotFound, dayID))
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/service/ ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations/0006_day_reflection.sql internal/domain/domain.go internal/service
git commit -m "Store a reflection on each day, beside its summary"
```

---

### Task 2: Write the reflection at wrap-up

**Files:**
- Create: `internal/planner/reflection.go`, `internal/planner/reflection_test.go`
- Modify: `internal/planner/instructions.go`, `internal/planner/close.go` (`WrapUp`), `internal/planner/planner.go` (`Planner` struct and `New`), `internal/llm/fake/fake.go`, `internal/llm/fake/fake_test.go`, `internal/planner/fake_test.go`

**Interfaces:**
- Consumes: `SetDayReflection`, `Day.Reflection` (Task 1).
- Produces (all unexported, in package `planner`):
  ```go
  const reflectionInstructions string
  func (p *Planner) reflect(ctx context.Context, summary string) (string, error)
  func (p *Planner) writeReflection(ctx context.Context, day domain.Day) (string, error)
  type fills struct{ mu sync.Mutex; busy, gaveUp map[int64]bool }
  func newFills() *fills
  func (f *fills) hold(dayID int64)
  func (f *fills) claim(dayID int64) bool
  func (f *fills) release(dayID int64, gaveUp bool)
  // Planner gains: fills *fills
  ```
  `fake.Reflection = "A fake reflection, to you."`

- [ ] **Step 1: Write the failing planner tests**

`internal/planner/reflection_test.go`:

```go
package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWrapUpWritesASummaryThenAReflection(t *testing.T) {
	f := newFixture(t, "They shipped it.", "You shipped it.")
	f.start(t, 3, "")
	ctx := context.Background()

	_, wrapped, err := f.planner.WrapUp(ctx, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if wrapped.Summary != "They shipped it." || wrapped.Reflection != "You shipped it." {
		t.Errorf("wrapped = %+v", wrapped)
	}
	if stored, _ := f.svc.GetDay(ctx, today); stored.Reflection != "You shipped it." {
		t.Errorf("stored reflection = %q", stored.Reflection)
	}
	summary, reflection := f.model.sent[0], f.model.sent[1]
	if strings.Contains(system(summary), "dry humor") {
		t.Error("the summary prompt carries the user's style")
	}
	if !strings.Contains(system(reflection), "dry humor") {
		t.Error("the reflection prompt leaves out the user's style")
	}
	if reflection.User != "They shipped it." {
		t.Errorf("reflection is written from %q, want the summary", reflection.User)
	}
}

func TestCloseWritesNoReflection(t *testing.T) {
	f := newFixture(t, "They shipped it.")
	day := f.start(t, 3, "")
	if _, wrapped, err := f.planner.Close(context.Background(), day, nil, ""); err != nil || wrapped.Reflection != "" {
		t.Errorf("wrapped = %+v, err %v", wrapped, err)
	}
	if len(f.model.sent) != 1 {
		t.Errorf("Close made %d model calls, want 1", len(f.model.sent))
	}
}

func TestAFailedReflectionStillWrapsTheDay(t *testing.T) {
	f := newFixture(t, "They shipped it.", errors.New("overloaded"))
	f.start(t, 3, "")
	_, wrapped, err := f.planner.WrapUp(context.Background(), nil, "")
	if err != nil || wrapped.WrappedAt == nil || wrapped.Summary != "They shipped it." || wrapped.Reflection != "" {
		t.Errorf("wrapped = %+v, err %v", wrapped, err)
	}
}

func TestAFailedSummaryMakesNoReflectionCall(t *testing.T) {
	f := newFixture(t, errors.New("overloaded"))
	f.start(t, 3, "")
	if _, _, err := f.planner.WrapUp(context.Background(), nil, ""); err != nil {
		t.Fatal(err)
	}
	if len(f.model.sent) != 1 {
		t.Errorf("WrapUp made %d model calls, want 1", len(f.model.sent))
	}
}
```

Append to `internal/planner/fake_test.go`:

```go
func TestTheFakeReflectsOnTheSummary(t *testing.T) {
	f := newFixture(t)
	f.slow = fake.Client{}
	f.start(t, 3, "")
	_, wrapped, err := f.planner.WrapUp(context.Background(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if wrapped.Summary != fake.Sentence || wrapped.Reflection != fake.Reflection {
		t.Errorf("wrapped = %+v", wrapped)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/planner/ -run 'Reflect|CloseWritesNo|FailedSummaryMakes'`
Expected: FAIL to compile, `fake.Reflection` undefined (the new test in `fake_test.go` shares the package). That is the red step for this task; the behavior tests turn green in Step 7.

- [ ] **Step 3: Add the fake's reply**

In `internal/llm/fake/fake.go`, add to the constants:

```go
	Reflection = "A fake reflection, to you."
```

and above `Complete`:

```go
// reflectionMarker is a phrase from the planner's reflection instructions. TestTheFakeReflectsOnTheSummary
// in the planner fails if the two drift apart.
const reflectionMarker = "a short message to them about their day"
```

In `Complete`'s `default` case:

```go
	default:
		if strings.Contains(system(req.System), reflectionMarker) {
			return Reflection, nil
		}
		return Sentence, nil
```

Append to `internal/llm/fake/fake_test.go`:

```go
func TestAReflectionRequestGetsTheFixedReflection(t *testing.T) {
	req := llm.Request{System: []llm.Block{{Text: "Write a short message to them about their day."}}}
	reply, err := fake.Client{}.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if reply != fake.Reflection {
		t.Errorf("reply = %q, want %q", reply, fake.Reflection)
	}
}
```

- [ ] **Step 4: Add the instructions**

In `internal/planner/instructions.go`, after `summaryInstructions`:

```go
const reflectionInstructions = Persona + `

Below are your notes on how their day went. Write a short message to them about their day, in second person: two to four plain sentences. Be kind, not clinical. Notice what went well, and name what slipped without guilt. Keep to what the notes say. These rules hold whatever style you were given: no headings, no lists, no advice.`
```

The phrase `a short message to them about their day` must stay in it; the fake keys on it.

- [ ] **Step 5: Write `internal/planner/reflection.go`**

```go
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
```

- [ ] **Step 6: Wire it into the planner and `WrapUp`**

In `internal/planner/planner.go`, add to `Planner` after `mu`:

```go
	// fills keeps reflection writers to one per day.
	fills *fills
```

and in `New`: `return &Planner{svc: svc, hub: hub, client: client, loc: loc, now: now, fills: newFills()}`.

In `internal/planner/close.go`, replace `WrapUp`:

```go
// WrapUp closes today and writes the user's reflection from the summary. It returns ErrNoDay if
// today hasn't started. A reflection that fails is logged and left empty, like the summary.
func (p *Planner) WrapUp(ctx context.Context, triage []Triage, note string) (int64, domain.Day, error) {
	day, err := p.svc.GetDay(ctx, p.Date())
	if errors.Is(err, service.ErrNotFound) {
		return 0, domain.Day{}, ErrNoDay
	}
	if err != nil {
		return 0, domain.Day{}, err
	}
	// Held from before the summary is saved, so a lazy fill can't start a second reflection.
	p.fills.hold(day.ID)
	defer p.fills.release(day.ID, false)
	batch, wrapped, err := p.Close(ctx, day, triage, note)
	if err != nil || wrapped.Summary == "" {
		return batch, wrapped, err
	}
	// Written even if the user has left, like the summary.
	if r, err := p.writeReflection(context.WithoutCancel(ctx), wrapped); err != nil {
		fallbackNote(err)
	} else {
		wrapped.Reflection = r
	}
	return batch, wrapped, nil
}
```

Update `Close`'s doc comment: after "has the light model write the day's summary." add "It writes no reflection; WrapUp does, since only today's goodnight is seen."

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/planner/ ./internal/llm/fake/ ./internal/httpapi/`
Expected: PASS, including the existing close and wrap tests (the API tests run with no provider, so the summary fails and no reflection call happens).

- [ ] **Step 8: Commit**

```bash
git add internal/planner internal/llm/fake
git commit -m "Write a reflection for the user at wrap-up, in their assistant's voice"
```

---

### Task 3: Fill a missing reflection lazily

**Files:**
- Modify: `internal/planner/reflection.go`, `internal/planner/today.go`
- Test: `internal/planner/reflection_test.go`

**Interfaces:**
- Consumes: `fills`, `writeReflection`, `hold` (Task 2).
- Produces: `func (p *Planner) fillReflection(day domain.Day)`, called from `Today`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/planner/reflection_test.go` (extend its imports with `"sync/atomic"`, `"time"`, `"github.com/jameynakama/whatnow/internal/llm"`):

```go
// gate is a model that answers the first hold calls at once with the summary, and holds every
// later call until release closes, then answers with the reflection, or fails if fail is set.
type gate struct {
	noChat
	hold    int32
	fail    bool
	calls   atomic.Int32
	release chan struct{}
}

func (g *gate) Complete(context.Context, llm.Request) (string, error) {
	if g.calls.Add(1) <= g.hold {
		return "They shipped it.", nil
	}
	<-g.release
	if g.fail {
		return "", errors.New("bad key")
	}
	return "You shipped it.", nil
}

// eventually polls cond for up to two seconds.
func eventually(t *testing.T, cond func() bool, what string) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// wrapped closes today with the summary "They shipped it." and no reflection, as a day wrapped
// before reflections existed.
func (f *fixture) wrapped(t *testing.T) {
	t.Helper()
	f.model.replies = append(f.model.replies, "They shipped it.")
	day := f.start(t, 3, "")
	if _, _, err := f.planner.Close(context.Background(), day, nil, ""); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) reflection(t *testing.T) string {
	t.Helper()
	day, err := f.svc.GetDay(context.Background(), today)
	if err != nil {
		t.Fatal(err)
	}
	return day.Reflection
}

func TestTodayFillsAMissingReflectionOnce(t *testing.T) {
	f := newFixture(t)
	f.wrapped(t)
	g := &gate{release: make(chan struct{})}
	f.slow = g
	for range 3 {
		if _, err := f.planner.Today(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	close(g.release)
	eventually(t, func() bool { return f.reflection(t) == "You shipped it." }, "the reflection")
	if _, err := f.planner.Today(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := g.calls.Load(); n != 1 {
		t.Errorf("%d model calls, want 1", n)
	}
}

func TestAFailedFillIsNotRetried(t *testing.T) {
	f := newFixture(t)
	f.wrapped(t)
	g := &gate{fail: true, release: make(chan struct{})}
	close(g.release)
	f.slow = g
	if _, err := f.planner.Today(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return g.calls.Load() == 1 }, "the fill")
	time.Sleep(50 * time.Millisecond) // let the failed fill give the day up
	for range 3 {
		if _, err := f.planner.Today(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := g.calls.Load(); n != 1 {
		t.Errorf("%d model calls, want 1", n)
	}
}

func TestTodayDuringWrapUpStartsNoSecondReflection(t *testing.T) {
	f := newFixture(t)
	g := &gate{hold: 1, release: make(chan struct{})}
	f.slow = g
	f.start(t, 3, "")
	done := make(chan error)
	go func() {
		_, _, err := f.planner.WrapUp(context.Background(), nil, "")
		done <- err
	}()
	eventually(t, func() bool { return g.calls.Load() == 2 }, "wrap-up's reflection call")
	if _, err := f.planner.Today(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := g.calls.Load(); n != 2 {
		t.Errorf("%d model calls, want 2: one summary, one reflection", n)
	}
}

func TestAnEmptyReflectionIsTriedOnceMoreLazily(t *testing.T) {
	f := newFixture(t, "They shipped it.", "   ")
	f.start(t, 3, "")
	if _, wrapped, err := f.planner.WrapUp(context.Background(), nil, ""); err != nil || wrapped.Reflection != "" {
		t.Fatalf("wrapped = %+v, err %v", wrapped, err)
	}
	g := &gate{release: make(chan struct{})}
	close(g.release)
	f.slow = g
	if _, err := f.planner.Today(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return f.reflection(t) == "You shipped it." }, "the second try")
}

func TestADayWithNoSummaryGetsNoReflection(t *testing.T) {
	f := newFixture(t, errors.New("overloaded"))
	day := f.start(t, 3, "")
	if _, _, err := f.planner.Close(context.Background(), day, nil, ""); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := f.planner.Today(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if len(f.model.sent) != 1 {
		t.Errorf("%d model calls, want only the failed summary", len(f.model.sent))
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/planner/ -run 'Fill|Filled|DuringWrapUp|EmptyReflection|NoSummary'`
Expected: `TestTodayFillsAMissingReflectionOnce` and `TestAnEmptyReflectionIsTriedOnceMoreLazily` FAIL timing out on the reflection; the others pass (nothing calls the model yet) and must keep passing.

- [ ] **Step 3: Add the lazy fill**

Append to `internal/planner/reflection.go`:

```go
// fillReflection writes a wrapped day's missing reflection in the background. It covers a
// reflection that failed at wrap-up and days wrapped before reflections existed.
func (p *Planner) fillReflection(day domain.Day) {
	if !p.fills.claim(day.ID) {
		return
	}
	go func() {
		r, err := p.writeReflection(context.Background(), day)
		if err != nil {
			fallbackNote(err)
		}
		p.fills.release(day.ID, err != nil || r == "")
	}()
}
```

A lazy fill that comes back empty also gives the day up (`r == ""`). Wrap-up always releases with `gaveUp` false, so an empty reflection from wrap-up still gets one lazy try, which `TestAnEmptyReflectionIsTriedOnceMoreLazily` checks.

In `internal/planner/today.go`, in `Today`, right after `gather` succeeds:

```go
	if d := st.day; d != nil && d.WrappedAt != nil && d.Summary != "" && d.Reflection == "" {
		p.fillReflection(*d)
	}
```

- [ ] **Step 4: Run the planner tests, with the race detector**

Run: `go test -race ./internal/planner/`
Expected: PASS, no races.

- [ ] **Step 5: Run everything**

Run: `go test ./... && go vet ./...`
Expected: PASS. The TUI tests never wrap a day, so they make no extra calls.

- [ ] **Step 6: Commit**

```bash
git add internal/planner
git commit -m "Fill in a wrapped day's missing reflection, once per process"
```

---

### Task 4: Show the reflection on the web

**Files:**
- Modify: `web/src/lib/api.ts`, `web/src/lib/components/DaysDone.svelte`, `web/src/routes/(ritual)/wrap/+page.svelte`, `web/src/lib/plan.test.ts`, `web/src/lib/rituals.test.ts`, `web/src/lib/triage.test.ts`, `web/e2e/fixtures.ts`, `web/e2e/today.spec.ts`

**Interfaces:**
- Consumes: `"reflection"` in the day JSON (Task 1), `fake.Reflection` (Task 2).
- Produces: `Day.reflection: string` in `api.ts`; `fake.reflection` in `web/e2e/fixtures.ts`.

- [ ] **Step 1: Write the failing e2e check**

In `web/e2e/fixtures.ts`, add to `fake` after `sentence`:

```ts
	reflection: 'A fake reflection, to you.',
```

In `web/e2e/today.spec.ts`, in the wrap-up test, replace

```ts
	await expect(page.getByText(fake.sentence)).toBeVisible();
```

with

```ts
	// The goodnight screen shows the reflection; the summary is the planner's, not the user's.
	await expect(page.getByText(fake.reflection)).toBeVisible();
	await expect(page.getByText(fake.sentence)).toHaveCount(0);
```

- [ ] **Step 2: Run it to see it fail**

Run: `just e2e`
Expected: the wrap-up test FAILS: `fake.reflection` isn't on the page, and `fake.sentence` is.

- [ ] **Step 3: Show the reflection**

In `web/src/lib/api.ts`, add to `Day` after `summary: string;`:

```ts
	reflection: string;
```

Add `reflection: ''` after `summary: ''` in the `day` objects in `web/src/lib/plan.test.ts`, `rituals.test.ts`, and `triage.test.ts`.

In `web/src/lib/components/DaysDone.svelte`, replace `{#if day.summary}<p>{day.summary}</p>{/if}` with:

```svelte
	{#if day.reflection}<p>{day.reflection}</p>{/if}
```

In `web/src/routes/(ritual)/wrap/+page.svelte`, replace `{#if wrapped?.summary}<p class="sticker summary">{wrapped.summary}</p>{/if}` with:

```svelte
	{#if wrapped?.reflection}<p class="sticker summary">{wrapped.reflection}</p>{/if}
```

- [ ] **Step 4: Run the web checks and e2e**

Run: `cd web && npm run check && npm test && cd .. && just e2e`
Expected: `svelte-check` reports 0 errors, the unit tests pass, and all Playwright tests pass.

- [ ] **Step 5: Commit**

```bash
git add web
git commit -m "Show the reflection, not the summary, on the goodnight screen and the Day's-done card"
```
