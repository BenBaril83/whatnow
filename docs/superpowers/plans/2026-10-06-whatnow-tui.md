# whatnow TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `whatnow tui`, a Bubble Tea companion that shows today, takes the quick actions, and hosts chat, running in-process on the same parts as `whatnow serve`.

**Architecture:** The wiring in `cmd/whatnow/main.go` moves to `internal/app`, which both subcommands open. `internal/tui` is one Bubble Tea model: a snapshot loader feeds the view, actions run in user batches announced on the hub like the API's writes, and the hub plus a 5-second tick drive reloads.

**Tech Stack:** Go 1.27, Bubble Tea v1 (`github.com/charmbracelet/bubbletea`), Bubbles (`textinput`, `spinner`), Lip Gloss v1, `github.com/charmbracelet/x/ansi`, `github.com/charmbracelet/x/exp/teatest`, go-cmp, the fake model in `internal/llm/fake`.

**Spec:** `docs/superpowers/specs/2026-10-06-whatnow-tui-design.md`

## Global Constraints

- Stay on the v1 APIs named above: `tea.Model` with `View() string`, `tea.KeyMsg`, `lipgloss.AdaptiveColor`. Don't mix in v2 (`charm.land/...`) packages.
- Every write runs in a new `domain.ActorUser` batch and is announced with `Hub.PublishBatch(batch, stream.SourceUser, activity)`, exactly as `httpapi.Server.write` does.
- An undo is announced with `Hub.PublishUndone(batch)`.
- Colors come from `web/src/lib/tokens.css`. Fills (tiers, event, chip) keep dark ink `#1f1a3d` on them; plain ink is adaptive.
- The web address shown is `http://` plus `serve`'s default address, `127.0.0.1:7777`.
- Chat sits beside the list at 90 columns or wider; below that it replaces the list while open.
- Refresh every 5 seconds; toasts last 4 seconds.
- Go tests name behavior in the repo's style: `TestWhatNowPicksFromTheModel`, not `TestShould...`.
- Coverage goal 100% for `internal/app` and `internal/tui`. Accepted gaps, and only these: `tui.Run`, which starts a real terminal; the `tea.Tick` callbacks in `tick` and `show`, which fire only after 5 and 4 seconds; and `load`'s error returns after its first, which share one database and can't fail on their own.
- No dates, names, or history in code comments.

## Review Focus

1. **A tiny terminal** (say 20×6, or before the first `WindowSizeMsg`): the view must not panic on negative widths. Task 4 pins it.
2. **A task title wider than its pane:** it is cut with `…` on one line, not wrapped into the next row. Task 4 pins it.
3. **An empty list once everything is done:** the list says it's all clear, and `space`, `p`, `x` do nothing instead of acting on a stale ID. Task 5 pins it.
4. **The list reordering under the cursor** after a replan or another process's change: the cursor stays on the same task, and when its task leaves, it lands on the task now at the same position. Task 4 pins it.
5. **Chat sent while a reply is still being written** (`agent.ErrBusy`) and **what now with nothing free** (`planner.ErrNothingToDo`): each shows a plain-language toast, not a raw error. Task 5 pins it.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/app/app.go` | Opens the database and builds and runs the shared parts |
| `internal/app/app_test.go` | Default path, cleanup wiring, close |
| `cmd/whatnow/main.go` | `serve` and `tui` subcommands over `app.Open` |
| `internal/tui/strip.go` | Day strip layout in columns, and its rendering |
| `internal/tui/snapshot.go` | Loads what the screen shows; groups the list |
| `internal/tui/styles.go` | Palette, styles, the sticker frame |
| `internal/tui/model.go` | Model, messages, `Update`, key handling |
| `internal/tui/actions.go` | Commands: load, listen, tick, writes, undo, what now, chat |
| `internal/tui/view.go` | `View` and its parts |
| `internal/tui/*_test.go`, `internal/tui/testdata/*.golden` | Tests and golden views |
| `Justfile` | `just tui` |

---

### Task 1: Shared wiring in `internal/app`

**Files:**
- Create: `internal/app/app.go`, `internal/app/app_test.go`
- Modify: `cmd/whatnow/main.go`

**Interfaces:**
- Produces:
  ```go
  type Options struct {
      DBPath    string // defaults to <user config dir>/whatnow/whatnow.db
      NewClient func(settings.Settings) (llm.Client, settings.ProviderConfig, error)
      Now       func() time.Time
      Loc       *time.Location
  }
  type App struct {
      DB      *sql.DB
      DBPath  string
      Service *service.Service
      Hub     *stream.Hub
      Cleanup *cleanup.Runner
      Planner *planner.Planner
      Agent   *agent.Agent
      Now     func() time.Time
      Loc     *time.Location
  }
  func Open(o Options) (*App, error)
  func (a *App) Close() error
  ```

- [ ] **Step 1: Write the failing tests**

`internal/app/app_test.go`:

```go
package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/llm/fake"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
)

func fakeClient(settings.Settings) (llm.Client, settings.ProviderConfig, error) {
	return fake.Client{}, settings.ProviderConfig{}, nil
}

func open(t *testing.T, path string) *app.App {
	t.Helper()
	a, err := app.Open(app.Options{DBPath: path, NewClient: fakeClient, Now: time.Now, Loc: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func TestOpenPutsTheDatabaseInTheUserConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	a := open(t, "")
	want := filepath.Join(dir, "whatnow", "whatnow.db")
	if a.DBPath != want {
		t.Errorf("DBPath = %q, want %q", a.DBPath, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Error(err)
	}
}

func TestOpenCleansUpCapturesInTheBackground(t *testing.T) {
	a := open(t, filepath.Join(t.TempDir(), "test.db"))
	ctx := context.Background()
	applied, stop := a.Hub.Subscribe("batch.applied")
	defer stop()

	batch, err := a.Service.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Service.CreateTask(ctx, batch, service.TaskFields{RawText: "buy milk and call mom"})
	if err != nil {
		t.Fatal(err)
	}
	a.Cleanup.Enqueue(task)

	select {
	case <-applied:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup never applied a batch")
	}
	tasks, err := a.Service.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks after cleanup, want 2", len(tasks))
	}
}

func TestCloseClosesTheDatabase(t *testing.T) {
	a, err := app.Open(app.Options{DBPath: filepath.Join(t.TempDir(), "test.db"), NewClient: fakeClient, Now: time.Now, Loc: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.Ping(); err == nil {
		t.Error("the database still answers after Close")
	}
}

func TestOpenFailsWithNoUserConfigDir(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if _, err := app.Open(app.Options{NewClient: fakeClient, Now: time.Now, Loc: time.UTC}); err == nil {
		t.Error("Open succeeded with no home")
	}
}

func TestOpenFailsWhenTheDatabaseCantOpen(t *testing.T) {
	if _, err := app.Open(app.Options{DBPath: t.TempDir(), NewClient: fakeClient, Now: time.Now, Loc: time.UTC}); err == nil {
		t.Error("Open succeeded on a directory")
	}
}

func TestOpenFailsWhenTheDatabaseDirCantBeMade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Open(app.Options{DBPath: filepath.Join(file, "whatnow.db"), NewClient: fakeClient, Now: time.Now, Loc: time.UTC}); err == nil {
		t.Error("Open succeeded under a file")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/`
Expected: FAIL, package `app` does not exist.

- [ ] **Step 3: Write `internal/app/app.go`**

```go
// Package app builds whatnow's parts and runs their background work. The server and the TUI both
// start from it, so a change made in either is cleaned up and replanned the same way.
package app

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jameynakama/whatnow/internal/agent"
	"github.com/jameynakama/whatnow/internal/cleanup"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

// Options says where the database is and how to make the model client. Now and Loc are the
// clock, which tests fix.
type Options struct {
	// DBPath defaults to <user config dir>/whatnow/whatnow.db.
	DBPath    string
	NewClient func(settings.Settings) (llm.Client, settings.ProviderConfig, error)
	Now       func() time.Time
	Loc       *time.Location
}

// App is the running set of parts. Close stops its background work and closes the database.
type App struct {
	DB      *sql.DB
	DBPath  string
	Service *service.Service
	Hub     *stream.Hub
	Cleanup *cleanup.Runner
	Planner *planner.Planner
	Agent   *agent.Agent
	Now     func() time.Time
	Loc     *time.Location

	stop    context.CancelFunc
	running sync.WaitGroup
}

// Open opens the database and starts the cleanup runner and the replan watcher.
func Open(o Options) (*App, error) {
	path := o.DBPath
	if path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(dir, "whatnow", "whatnow.db")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := store.Open(path)
	if err != nil {
		return nil, err
	}

	svc := service.New(db, o.Now)
	hub := stream.NewHub()
	active := func(ctx context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
		s, err := settings.Load(ctx, db)
		if err != nil {
			return nil, settings.ProviderConfig{}, settings.Settings{}, err
		}
		client, cfg, err := o.NewClient(s)
		return client, cfg, s, err
	}
	plans := planner.New(svc, hub, active, o.Loc, o.Now)
	a := &App{
		DB:      db,
		DBPath:  path,
		Service: svc,
		Hub:     hub,
		Cleanup: cleanup.New(svc, hub, active, o.Loc, o.Now),
		Planner: plans,
		Agent:   agent.New(svc, hub, plans, o.Loc, o.Now),
		Now:     o.Now,
		Loc:     o.Loc,
	}

	ctx, stop := context.WithCancel(context.Background())
	a.stop = stop
	watched, unsubscribe := hub.Subscribe("batch.applied", "batch.undone")
	a.running.Add(2)
	go func() {
		defer a.running.Done()
		a.Cleanup.Run(ctx)
	}()
	go func() {
		defer a.running.Done()
		defer unsubscribe()
		plans.Run(ctx, watched)
	}()
	return a, nil
}

// Close stops the background work, waits for it, and closes the database.
func (a *App) Close() error {
	a.stop()
	a.running.Wait()
	return a.DB.Close()
}
```

- [ ] **Step 4: Run the app tests**

Run: `go test ./internal/app/`
Expected: PASS.

- [ ] **Step 5: Move `main.go` onto `app.Open`, and add the `tui` subcommand shell**

Replace `cmd/whatnow/main.go` with the version below. `tui.Run` arrives in Task 4, so until then leave the `case "tui"` branch out, and add it in Task 4 Step 6.

```go
// Command whatnow runs the whatnow server, or the terminal companion.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/web"
)

// newClient makes the model client. The e2e build swaps in the fake.
var newClient = settings.NewClient

const defaultAddr = "127.0.0.1:7777"

const usage = "usage: whatnow serve [-addr 127.0.0.1:7777] [-db PATH]\n       whatnow tui [-db PATH]"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	flags := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	dbPath := flags.String("db", "", "database path (default <user config dir>/whatnow/whatnow.db)")
	var err error
	switch os.Args[1] {
	case "serve":
		addr := flags.String("addr", defaultAddr, "listen address; keep it on loopback")
		flags.Parse(os.Args[2:])
		err = serve(*addr, *dbPath)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func open(dbPath string) (*app.App, error) {
	return app.Open(app.Options{DBPath: dbPath, NewClient: newClient, Now: time.Now, Loc: time.Local})
}

func serve(addr, dbPath string) error {
	a, err := open(dbPath)
	if err != nil {
		return err
	}
	defer a.Close()

	handler := httpapi.New(httpapi.Deps{
		Service:   a.Service,
		Hub:       a.Hub,
		DB:        a.DB,
		Loc:       a.Loc,
		OnCapture: a.Cleanup.Enqueue,
		Clean:     a.Cleanup.Clean,
		NewClient: newClient,
		Planner:   a.Planner,
		Agent:     a.Agent,
		Static:    web.Handler(),
	})
	// No WriteTimeout: it would cut off the long-lived SSE stream.
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("whatnow listening on http://%s (db %s)", addr, a.DBPath)
	return srv.ListenAndServe()
}
```

- [ ] **Step 6: Check nothing else moved**

Run: `go build ./... && go build -tags e2e ./cmd/whatnow && go vet ./... && go test ./...`
Expected: all PASS. Then `just e2e`: all Playwright tests pass, which proves `serve` still behaves the same through `app`.

- [ ] **Step 7: Commit**

```bash
git add internal/app cmd/whatnow/main.go
git commit -m "Move the server's wiring into internal/app, so a second front end can share it"
```

---

### Task 2: The day strip

**Files:**
- Create: `internal/tui/strip.go`, `internal/tui/styles.go`, `internal/tui/strip_test.go`, `internal/tui/main_test.go`

**Interfaces:**
- Produces:
  ```go
  func layoutStrip(events []domain.Event, now, endOfDay, startedAt time.Time, width int) stripLayout
  func (l stripLayout) render() string // two lines joined by "\n", each exactly width columns
  func hourLabel(h int) string          // "9a", "12p"
  func eventEnd(e domain.Event) time.Time
  // styles.go: ink, inkSoft, onFill, primary, nowRed, tierFill, eventFill, chipFill,
  // paneStyle, softStyle, cursorStyle, toastStyle, errorStyle, nowStyle, eventStyle, tickStyle,
  // chip(text string, fill lipgloss.Color) string, sticker(lines []string, w, h int) string
  ```

- [ ] **Step 1: Add the dependencies**

Run: `go get github.com/charmbracelet/bubbletea@v1 github.com/charmbracelet/lipgloss@v1 github.com/charmbracelet/bubbles github.com/charmbracelet/x/ansi github.com/charmbracelet/x/exp/teatest github.com/muesli/termenv`

Check `go.mod` lists bubbletea `v1.x` and lipgloss `v1.x`.

- [ ] **Step 2: Write `internal/tui/main_test.go`**

Rendering in tests must have no color, so goldens and string checks read plainly.

```go
package tui

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	os.Exit(m.Run())
}
```

- [ ] **Step 3: Write the failing strip tests**

`internal/tui/strip_test.go`. The cases mirror `strip` in `web/src/lib/time.test.ts`.

```go
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jameynakama/whatnow/internal/domain"
)

func at(clock string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", "2026-10-06 "+clock, time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func event(title, from, to string) domain.Event {
	e := domain.Event{Title: title, StartsAt: at(from)}
	if to != "" {
		end := at(to)
		e.EndsAt = &end
	}
	return e
}

func labels(l stripLayout) []string {
	var out []string
	for _, t := range l.ticks {
		out = append(out, t.label)
	}
	return out
}

func TestStripRunsFromTheStartOfDayToTheEndOfDayInWholeHours(t *testing.T) {
	l := layoutStrip(nil, at("13:00"), at("17:30"), at("08:40"), 100)
	if l.now != 50 { // 13:00 is halfway through 8:00 to 18:00
		t.Errorf("now = %d, want 50", l.now)
	}
	if l.endOfDay != 95 {
		t.Errorf("endOfDay = %d, want 95", l.endOfDay)
	}
}

func TestStripTicksEveryThirdHourOnALongStrip(t *testing.T) {
	got := strings.Join(labels(layoutStrip(nil, at("09:00"), at("17:30"), at("08:40"), 100)), " ")
	if got != "9a 12p 3p" {
		t.Errorf("ticks = %q", got)
	}
}

func TestStripTicksEveryHourOnAShortStrip(t *testing.T) {
	got := strings.Join(labels(layoutStrip(nil, at("13:10"), at("17:30"), at("13:05"), 100)), " ")
	if got != "2p 3p 4p 5p" {
		t.Errorf("ticks = %q", got)
	}
}

func TestStripStretchesPastTheEndOfDayForLateEvents(t *testing.T) {
	l := layoutStrip([]domain.Event{event("Late call", "18:00", "19:00")}, at("09:00"), at("17:00"), at("09:00"), 110)
	item := l.items[0]
	if item.col+item.width > 110 {
		t.Errorf("event runs off the strip: col %d width %d", item.col, item.width)
	}
	if l.endOfDay >= item.col {
		t.Errorf("end of day %d should sit left of the late event at %d", l.endOfDay, item.col)
	}
}

func TestStripStartsAtAnEarlierEvent(t *testing.T) {
	l := layoutStrip([]domain.Event{event("Standup", "07:30", "08:00")}, at("13:00"), at("17:30"), at("08:40"), 110)
	if l.items[0].col != 5 { // 30 of 660 minutes, from 7:00
		t.Errorf("col = %d, want 5", l.items[0].col)
	}
}

func TestStripDrawsAnEventWithNoEndAsHalfAnHour(t *testing.T) {
	l := layoutStrip([]domain.Event{event("Focus", "13:00", "")}, at("09:00"), at("17:00"), at("09:00"), 90)
	if l.items[0].width != 5 { // 30 of 540 minutes
		t.Errorf("width = %d, want 5", l.items[0].width)
	}
}

func TestStripRendersEventsOnTheSecondRow(t *testing.T) {
	rows := strings.Split(layoutStrip([]domain.Event{event("Standup", "09:00", "09:30")}, at("13:00"), at("17:30"), at("08:40"), 100).render(), "\n")
	if got := string([]rune(rows[1])[10:19]); got != " Standup " {
		t.Errorf("event row = %q", got)
	}
}

func TestStripKeepsEveryRowAtTheStripWidth(t *testing.T) {
	for _, row := range strings.Split(layoutStrip([]domain.Event{event("A very long meeting title", "16:00", "17:00")}, at("13:00"), at("17:30"), at("08:40"), 60).render(), "\n") {
		if w := lipgloss.Width(row); w != 60 {
			t.Errorf("row width %d, want 60: %q", w, row)
		}
	}
}

func TestStripKeepsTheEndOfDayLabelInsideTheRightEdge(t *testing.T) {
	rows := strings.Split(layoutStrip(nil, at("10:00"), at("23:59"), at("10:00"), 40).render(), "\n")
	if !strings.HasSuffix(rows[0], "11:59 PM") {
		t.Errorf("label row = %q", rows[0])
	}
}

func TestHourLabelCallsNoon12p(t *testing.T) {
	if got := hourLabel(12); got != "12p" {
		t.Errorf("hourLabel(12) = %q", got)
	}
	if got := hourLabel(0); got != "12a" {
		t.Errorf("hourLabel(0) = %q", got)
	}
}
```

- [ ] **Step 4: Run them to see them fail**

Run: `go test ./internal/tui/`
Expected: FAIL, `layoutStrip` undefined.

- [ ] **Step 5: Write `internal/tui/styles.go`**

```go
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/jameynakama/whatnow/internal/domain"
)

// The sticker look from web/src/lib/tokens.css. Fills keep dark ink on them, so they read on light
// and dark terminals alike; plain ink flips with the terminal's background.
var (
	ink     = lipgloss.AdaptiveColor{Light: "#1f1a3d", Dark: "#f1eadb"}
	inkSoft = lipgloss.AdaptiveColor{Light: "#6b6590", Dark: "#a59fc9"}
	onFill  = lipgloss.Color("#1f1a3d")
	primary = lipgloss.Color("#7b5cff")
	nowRed  = lipgloss.Color("#ff4d6d")

	tierFill = map[domain.Tier]lipgloss.Color{
		domain.TierMust:   "#ffd9d2",
		domain.TierShould: "#d8ecff",
		domain.TierCould:  "#ffffff",
	}
	eventFill = lipgloss.Color("#e3ffd8")
	chipFill  = lipgloss.Color("#ffd84d")

	paneStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ink).Padding(0, 1)
	softStyle   = lipgloss.NewStyle().Foreground(inkSoft)
	cursorStyle = lipgloss.NewStyle().Foreground(primary).Bold(true)
	toastStyle  = lipgloss.NewStyle().Background(chipFill).Foreground(onFill).Bold(true).Padding(0, 1)
	errorStyle  = lipgloss.NewStyle().Background(nowRed).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1)
	nowStyle    = lipgloss.NewStyle().Foreground(nowRed)
	eventStyle  = lipgloss.NewStyle().Background(eventFill).Foreground(onFill)
	tickStyle   = softStyle.Bold(true)
	shadeStyle  = lipgloss.NewStyle().Foreground(ink)
)

func chip(text string, fill lipgloss.Color) string {
	return lipgloss.NewStyle().Background(fill).Foreground(onFill).Bold(true).Padding(0, 1).Render(text)
}

// sticker frames lines in a rounded pane w columns wide and h rows tall, shadow included. The
// shadow is a flat ink edge offset one column right and one row down. Lines past the pane's
// height are dropped, and long lines are cut with an ellipsis rather than wrapped.
func sticker(lines []string, w, h int) string {
	inner, rows := max(w-5, 1), max(h-3, 1)
	fitted := make([]string, 0, rows)
	for _, l := range lines[:min(len(lines), rows)] {
		fitted = append(fitted, ansi.Truncate(l, inner, "…"))
	}
	box := strings.Split(paneStyle.Width(inner+2).Height(rows).Render(strings.Join(fitted, "\n")), "\n")
	for i := range box {
		if i == 0 {
			box[i] += " "
			continue
		}
		box[i] += shadeStyle.Render("█")
	}
	box = append(box, " "+shadeStyle.Render(strings.Repeat("▀", lipgloss.Width(box[0])-1)))
	return strings.Join(box, "\n")
}
```

- [ ] **Step 6: Write `internal/tui/strip.go`**

```go
package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/jameynakama/whatnow/internal/domain"
)

// stripLayout places the day's events on a strip of whole columns. It follows strip() in
// web/src/lib/time.ts: the strip runs from the earliest of when the day started, the first event,
// and now, to the end of day, rounded out to whole hours, and stretches to keep now and every
// event on it. Ticks mark every third clock hour, or every hour on a strip of 6 hours or less.
type stripLayout struct {
	width    int
	ticks    []stripTick
	items    []stripItem
	now      int
	endOfDay int
	eodLabel string
}

type stripTick struct {
	col   int
	label string
}

type stripItem struct {
	col, width int
	title      string
}

func layoutStrip(events []domain.Event, now, endOfDay, startedAt time.Time, width int) stripLayout {
	loc := now.Location()
	start, end := now, endOfDay.In(loc)
	if s := startedAt.In(loc); s.Before(start) {
		start = s
	}
	if now.After(end) {
		end = now
	}
	for _, e := range events {
		if s := e.StartsAt.In(loc); s.Before(start) {
			start = s
		}
		if e := eventEnd(e).In(loc); e.After(end) {
			end = e
		}
	}
	start, end = floorHour(start), floorHour(end).Add(time.Hour)
	col := func(t time.Time) int {
		c := int(float64(width) * float64(t.Sub(start)) / float64(end.Sub(start)))
		return min(max(c, 0), width-1)
	}
	step := 3
	if end.Sub(start) <= 6*time.Hour {
		step = 1
	}
	l := stripLayout{width: width, now: col(now), endOfDay: col(endOfDay), eodLabel: endOfDay.In(loc).Format("3:04 PM")}
	for h := start.Add(time.Hour); h.Before(end); h = h.Add(time.Hour) {
		if h.Hour()%step == 0 {
			l.ticks = append(l.ticks, stripTick{col: col(h), label: hourLabel(h.Hour())})
		}
	}
	for _, e := range events {
		c := col(e.StartsAt)
		l.items = append(l.items, stripItem{col: c, width: max(col(eventEnd(e))-c, 1), title: e.Title})
	}
	return l
}

func floorHour(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}

// eventEnd is when an event ends. One with no end is drawn as 30 minutes.
func eventEnd(e domain.Event) time.Time {
	if e.EndsAt != nil {
		return *e.EndsAt
	}
	return e.StartsAt.Add(30 * time.Minute)
}

func hourLabel(h int) string {
	suffix := "a"
	if h >= 12 {
		suffix = "p"
	}
	if h%12 == 0 {
		return fmt.Sprintf("12%s", suffix)
	}
	return fmt.Sprintf("%d%s", h%12, suffix)
}

type cellKind int

const (
	plainCell cellKind = iota
	tickCell
	eodCell
	eventCell
	nowCell
)

type cell struct {
	r    rune
	kind cellKind
}

var cellStyles = map[cellKind]lipgloss.Style{
	plainCell: lipgloss.NewStyle(),
	tickCell:  tickStyle,
	eodCell:   softStyle,
	eventCell: eventStyle,
	nowCell:   nowStyle,
}

// render draws the strip as two rows: tick and end-of-day labels, then events. The end-of-day rule
// and the now line cross both.
func (l stripLayout) render() string {
	var rows [2][]cell
	for i := range rows {
		rows[i] = make([]cell, l.width)
		for j := range rows[i] {
			rows[i][j] = cell{r: ' '}
		}
	}
	put := func(row, col int, s string, k cellKind) {
		for _, r := range s {
			if col >= 0 && col < l.width {
				rows[row][col] = cell{r: r, kind: k}
			}
			col++
		}
	}
	for _, t := range l.ticks {
		put(0, t.col, t.label, tickCell)
	}
	put(0, l.endOfDay, "┆", eodCell)
	put(1, l.endOfDay, "┆", eodCell)
	// The label sits right of its rule, sliding left to stay inside the strip.
	put(0, min(l.endOfDay+1, l.width-utf8.RuneCountInString(l.eodLabel)), l.eodLabel, eodCell)
	for _, it := range l.items {
		text := " " + it.title + " "
		if n := utf8.RuneCountInString(text); n < it.width {
			text += strings.Repeat(" ", it.width-n)
		}
		put(1, it.col, text, eventCell)
	}
	put(0, l.now, "▌", nowCell)
	put(1, l.now, "▌", nowCell)
	return paint(rows[0]) + "\n" + paint(rows[1])
}

// paint renders a row, styling each run of same-kind cells once.
func paint(cells []cell) string {
	var b strings.Builder
	for i := 0; i < len(cells); {
		j := i
		var run strings.Builder
		for j < len(cells) && cells[j].kind == cells[i].kind {
			run.WriteRune(cells[j].r)
			j++
		}
		b.WriteString(cellStyles[cells[i].kind].Render(run.String()))
		i = j
	}
	return b.String()
}
```

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/tui/`
Expected: PASS. If a column is off by one, check the expectation against `time.test.ts`'s percentages before changing the math: `col` floors `width × fraction`.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/tui
git commit -m "Add the TUI's day strip, laid out in whole columns"
```

---

### Task 3: The snapshot

**Files:**
- Create: `internal/tui/snapshot.go`, `internal/tui/helpers_test.go`, `internal/tui/snapshot_test.go`

**Interfaces:**
- Consumes: `*app.App` (Task 1).
- Produces:
  ```go
  type snapshot struct {
      now       time.Time
      today     planner.Today
      tasks     map[int64]domain.Task
      events    []domain.Event
      waits     map[int64]int // open blockers per task
      doneToday int
      endOfDay  time.Time
      chat      []domain.Message
  }
  type group struct {
      tier domain.Tier // "" for the tasks the plan leaves out
      ids  []int64
  }
  func load(ctx context.Context, a *app.App) (snapshot, error)
  func (s snapshot) groups() []group
  func (s snapshot) order() []int64
  // helpers_test.go: type clock; type fixture{app *app.App; clock *clock};
  // newFixture(t) *fixture; (f) task(t, title) domain.Task; (f) start(t)
  ```

- [ ] **Step 1: Write the test fixture**

`internal/tui/helpers_test.go`:

```go
package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/llm/fake"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type fixture struct {
	app   *app.App
	clock *clock
}

// newFixture opens a temp database at 10:00 on a fixed day, with the fake model.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	a, err := app.Open(app.Options{
		DBPath: filepath.Join(t.TempDir(), "test.db"),
		NewClient: func(settings.Settings) (llm.Client, settings.ProviderConfig, error) {
			return fake.Client{}, settings.ProviderConfig{}, nil
		},
		Now: c.now,
		Loc: time.UTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return &fixture{app: a, clock: c}
}

func (f *fixture) task(t *testing.T, title string) domain.Task {
	t.Helper()
	ctx := context.Background()
	batch, err := f.app.Service.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.app.Service.CreateTask(ctx, batch, service.TaskFields{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// start starts today. The fake plans eligible tasks in order: a must, a should, then coulds.
func (f *fixture) start(t *testing.T) {
	t.Helper()
	if _, _, err := f.app.Planner.StartDay(context.Background(), 3, "", nil); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) load(t *testing.T) snapshot {
	t.Helper()
	s, err := load(context.Background(), f.app)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
```

- [ ] **Step 2: Write the failing snapshot tests**

`internal/tui/snapshot_test.go`:

```go
package tui

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
)

func TestSnapshotGroupsThePlanByTierThenWhatItLeftOut(t *testing.T) {
	f := newFixture(t)
	a, b, c, d := f.task(t, "a"), f.task(t, "b"), f.task(t, "c"), f.task(t, "d")
	f.start(t)
	late := f.task(t, "late")

	got := f.load(t).groups()
	want := []group{
		{tier: domain.TierMust, ids: []int64{a.ID}},
		{tier: domain.TierShould, ids: []int64{b.ID}},
		{tier: domain.TierCould, ids: []int64{c.ID, d.ID}},
		{tier: "", ids: []int64{late.ID}},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(group{})); diff != "" {
		t.Errorf("groups (-want +got):\n%s", diff)
	}
}

func TestSnapshotLeavesOutDoneTasksAndCountsThem(t *testing.T) {
	f := newFixture(t)
	a, b := f.task(t, "a"), f.task(t, "b")
	f.start(t)
	ctx := context.Background()
	batch, _ := f.app.Service.NewBatch(ctx, domain.ActorUser)
	if _, err := f.app.Service.SetTaskStatus(ctx, batch, a.ID, domain.StatusDone); err != nil {
		t.Fatal(err)
	}

	s := f.load(t)
	if diff := cmp.Diff([]int64{b.ID}, s.order()); diff != "" {
		t.Errorf("order (-want +got):\n%s", diff)
	}
	if s.doneToday != 1 {
		t.Errorf("doneToday = %d, want 1", s.doneToday)
	}
}

func TestSnapshotCountsOnlyOpenBlockers(t *testing.T) {
	f := newFixture(t)
	first, waiting := f.task(t, "buy milk"), f.task(t, "pour cereal")
	ctx := context.Background()
	batch, _ := f.app.Service.NewBatch(ctx, domain.ActorUser)
	if err := f.app.Service.AddBlocker(ctx, batch, domain.Blocker{TaskID: waiting.ID, BlockedByID: first.ID}); err != nil {
		t.Fatal(err)
	}
	f.start(t)
	if n := f.load(t).waits[waiting.ID]; n != 1 {
		t.Fatalf("waits = %d, want 1", n)
	}

	batch, _ = f.app.Service.NewBatch(ctx, domain.ActorUser)
	if _, err := f.app.Service.SetTaskStatus(ctx, batch, first.ID, domain.StatusDone); err != nil {
		t.Fatal(err)
	}
	if n := f.load(t).waits[waiting.ID]; n != 0 {
		t.Errorf("waits after the blocker is done = %d, want 0", n)
	}
}

func TestSnapshotReadsTheEndOfDayOnToday(t *testing.T) {
	f := newFixture(t)
	want := time.Date(2026, 10, 6, 17, 30, 0, 0, time.UTC) // the default end of day
	if got := f.load(t).endOfDay; !got.Equal(want) {
		t.Errorf("endOfDay = %v, want %v", got, want)
	}
}

func TestSnapshotHasNoPlanBeforeTheDayStarts(t *testing.T) {
	f := newFixture(t)
	f.task(t, "a")
	s := f.load(t)
	if s.today.Plan != nil {
		t.Error("a plan before the day started")
	}
	if len(s.groups()) != 0 {
		t.Errorf("groups before the day started: %v", s.groups())
	}
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/tui/ -run Snapshot`
Expected: FAIL, `load` undefined.

- [ ] **Step 4: Write `internal/tui/snapshot.go`**

```go
package tui

import (
	"context"
	"time"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/settings"
)

// snapshot is everything the screen shows, read in one go.
type snapshot struct {
	now       time.Time
	today     planner.Today
	tasks     map[int64]domain.Task
	events    []domain.Event
	waits     map[int64]int // open blockers per task
	doneToday int
	endOfDay  time.Time
	chat      []domain.Message
}

func load(ctx context.Context, a *app.App) (snapshot, error) {
	now := a.Now().In(a.Loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, a.Loc)
	dayEnd := dayStart.AddDate(0, 0, 1)
	s := snapshot{now: now, tasks: map[int64]domain.Task{}, waits: map[int64]int{}}

	var err error
	if s.today, err = a.Planner.Today(ctx); err != nil {
		return snapshot{}, err
	}
	tasks, err := a.Service.ListTasks(ctx)
	if err != nil {
		return snapshot{}, err
	}
	for _, t := range tasks {
		s.tasks[t.ID] = t
	}
	if s.events, err = a.Service.ListEvents(ctx, dayStart, dayEnd); err != nil {
		return snapshot{}, err
	}
	blockers, err := a.Service.ListBlockers(ctx)
	if err != nil {
		return snapshot{}, err
	}
	for _, b := range blockers {
		if s.tasks[b.BlockedByID].Status == domain.StatusOpen {
			s.waits[b.TaskID]++
		}
	}
	done, err := a.Service.DoneBetween(ctx, dayStart, dayEnd)
	if err != nil {
		return snapshot{}, err
	}
	s.doneToday = len(done)
	if s.chat, err = a.Agent.History(ctx); err != nil {
		return snapshot{}, err
	}
	set, err := settings.Load(ctx, a.DB)
	if err != nil {
		return snapshot{}, err
	}
	// Settings are checked when saved, so the end of day always parses.
	clock, _ := time.Parse(settings.ClockLayout, set.EndOfDay)
	s.endOfDay = dayStart.Add(time.Duration(clock.Hour())*time.Hour + time.Duration(clock.Minute())*time.Minute)
	return s, nil
}

// group is one heading of the list: a plan tier, or the tasks the plan leaves out when tier is "".
type group struct {
	tier domain.Tier
	ids  []int64
}

// groups is the list as shown: open tasks by plan tier in plan order, then the rest. It's empty
// until the day has a plan.
func (s snapshot) groups() []group {
	if s.today.Plan == nil {
		return nil
	}
	var out []group
	for _, tier := range []domain.Tier{domain.TierMust, domain.TierShould, domain.TierCould} {
		g := group{tier: tier}
		for _, e := range s.today.Plan.Entries {
			if e.Tier == tier && s.open(e.TaskID) {
				g.ids = append(g.ids, e.TaskID)
			}
		}
		if len(g.ids) > 0 {
			out = append(out, g)
		}
	}
	rest := group{}
	for _, id := range s.today.Unplanned {
		if s.open(id) {
			rest.ids = append(rest.ids, id)
		}
	}
	if len(rest.ids) > 0 {
		out = append(out, rest)
	}
	return out
}

// order is every task on the list, top to bottom.
func (s snapshot) order() []int64 {
	var ids []int64
	for _, g := range s.groups() {
		ids = append(ids, g.ids...)
	}
	return ids
}

func (s snapshot) open(id int64) bool {
	t, ok := s.tasks[id]
	return ok && t.Status == domain.StatusOpen
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/tui/`
Expected: PASS. If the tier test fails, print the plan entries: the fake's plan must give a must, a should, then coulds (see `internal/llm/fake`'s `plan`), and `Today.Unplanned` must hold the task created after the plan.

- [ ] **Step 6: Commit**

```bash
git add internal/tui
git commit -m "Add the TUI's snapshot of today, grouped as the list shows it"
```

---

### Task 4: The model, the views, and `whatnow tui`

**Files:**
- Create: `internal/tui/model.go`, `internal/tui/actions.go`, `internal/tui/view.go`, `internal/tui/view_test.go`, `internal/tui/testdata/*.golden`
- Modify: `cmd/whatnow/main.go`, `Justfile`

**Interfaces:**
- Consumes: `load`, `snapshot`, `groups`, `order` (Task 3); `layoutStrip`, `sticker`, `chip`, styles (Task 2).
- Produces:
  ```go
  type Model struct { /* fields below */ }
  func New(a *app.App, webURL string) Model
  func Run(a *app.App, webURL string) error
  func (m Model) Init() tea.Cmd
  func (m Model) Update(tea.Msg) (tea.Model, tea.Cmd)
  func (m Model) View() string
  // messages: snapshotMsg{snap snapshot}, loadFailedMsg{err error}, tickMsg{}, hubMsg (stream.Message)
  // commands: (m Model) load() tea.Cmd, (m Model) listen() tea.Cmd, tick() tea.Cmd
  // test helper: (f *fixture) model(t, width, height int) Model; golden(t, name, got string)
  ```
  Tasks 5 to 7 add more fields, messages, and keys to `model.go`, `actions.go`, and `view.go`.

- [ ] **Step 1: Write the failing view tests**

`internal/tui/view_test.go`:

```go
package tui

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// golden compares got with testdata/<name>.golden. Run with -update to rewrite it after checking
// the change by eye.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("%s (-want +got):\n%s", name, diff)
	}
}

// model is a TUI sized to width × height with a fresh snapshot loaded.
func (f *fixture) model(t *testing.T, width, height int) Model {
	t.Helper()
	m := New(f.app, "http://127.0.0.1:7777")
	t.Cleanup(m.unsubscribe)
	return send(m, tea.WindowSizeMsg{Width: width, Height: height}, snapshotMsg{f.load(t)})
}

// send feeds msgs to m in turn and returns the result, dropping commands.
func send(m Model, msgs ...tea.Msg) Model {
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func (f *fixture) today(t *testing.T) {
	t.Helper()
	f.task(t, "buy milk")
	f.task(t, "call mom")
	f.task(t, "fix the gutter")
	f.start(t)
	f.task(t, "renew passport")
}

func TestTodayView(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 30}} {
		f := newFixture(t)
		f.today(t)
		golden(t, "today-"+strconv.Itoa(size.w), f.model(t, size.w, size.h).View())
	}
}

func TestNoDayViewPointsToTheBrowser(t *testing.T) {
	f := newFixture(t)
	golden(t, "no-day", f.model(t, 80, 24).View())
}

func TestTheDayRollsOverAtMidnight(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	f.clock.t = f.clock.t.Add(24 * time.Hour)
	if v := f.model(t, 80, 24).View(); !strings.Contains(v, "Start your day in the browser") {
		t.Errorf("after midnight the view is:\n%s", v)
	}
}

func TestAWrapBannerShowsWithinAnHourOfTheEndOfDay(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	f.clock.t = time.Date(2026, 10, 6, 16, 45, 0, 0, time.UTC)
	if v := f.model(t, 80, 24).View(); !strings.Contains(v, "Wrap up in the browser") {
		t.Errorf("no wrap banner at 16:45:\n%s", v)
	}
}

func TestAFailedReloadKeepsTheListAndMarksItStale(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	v := send(f.model(t, 80, 24), loadFailedMsg{errors.New("disk")}).View()
	if !strings.Contains(v, "stale") || !strings.Contains(v, "buy milk") {
		t.Errorf("view after a failed reload:\n%s", v)
	}
}

func TestAFailedFirstLoadSaysWhy(t *testing.T) {
	f := newFixture(t)
	m := New(f.app, "")
	t.Cleanup(m.unsubscribe)
	v := send(m, tea.WindowSizeMsg{Width: 80, Height: 24}, loadFailedMsg{errors.New("disk")}).View()
	if !strings.Contains(v, "disk") {
		t.Errorf("view = %q", v)
	}
}

func TestTheCursorStaysOnItsTaskWhenTheListReorders(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 80, 24)
	m = send(m, key("j"))
	held := m.cursor

	s := f.load(t)
	entries := s.today.Plan.Entries
	entries[0], entries[1] = entries[1], entries[0]
	entries[0].Tier, entries[1].Tier = entries[1].Tier, entries[0].Tier
	if got := send(m, snapshotMsg{s}).cursor; got != held {
		t.Errorf("cursor moved from %d to %d", held, got)
	}
}

func TestTheCursorTakesThePlaceOfATaskThatLeaves(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := send(f.model(t, 80, 24), key("j"))
	s := f.load(t)
	gone := m.cursor
	next := s.order()[2]
	task := s.tasks[gone]
	task.Status = domain.StatusDone
	s.tasks[gone] = task
	if got := send(m, snapshotMsg{s}).cursor; got != next {
		t.Errorf("cursor = %d, want %d", got, next)
	}
}

func TestALongTitleIsCutNotWrapped(t *testing.T) {
	f := newFixture(t)
	f.task(t, strings.Repeat("very long title ", 10))
	f.start(t)
	v := f.model(t, 60, 20).View()
	for _, line := range strings.Split(v, "\n") {
		if w := lipgloss.Width(line); w > 60 {
			t.Errorf("line is %d wide: %q", w, line)
		}
	}
	if !strings.Contains(v, "…") {
		t.Errorf("no ellipsis:\n%s", v)
	}
}

func TestATinyTerminalDoesNotPanic(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	_ = f.model(t, 20, 6).View()
	m := New(f.app, "")
	t.Cleanup(m.unsubscribe)
	if v := m.View(); v != "" {
		t.Errorf("view before the first size = %q", v)
	}
}

func TestHelpListsTheKeysAndAnyKeyCloses(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := send(f.model(t, 80, 24), key("?"))
	if v := m.View(); !strings.Contains(v, "what now") || !strings.Contains(v, "push to tomorrow") {
		t.Errorf("help:\n%s", v)
	}
	if send(m, key("z")).mode != modeList {
		t.Error("a key didn't close help")
	}
}

func TestMovingStopsAtTheEnds(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 80, 24)
	first := m.cursor
	if got := send(m, key("k")).cursor; got != first {
		t.Errorf("up from the top moved to %d", got)
	}
	m = send(m, key("j"), key("j"), key("j"), key("j"), key("j"))
	if got, last := m.cursor, f.load(t).order()[3]; got != last {
		t.Errorf("cursor = %d, want the last task %d", got, last)
	}
}

func TestListenStopsWhenTheSubscriptionEnds(t *testing.T) {
	f := newFixture(t)
	m := New(f.app, "")
	m.unsubscribe()
	if msg := m.listen()(); msg != nil {
		t.Errorf("listen after unsubscribe = %v", msg)
	}
}

func TestLoadReportsAFailure(t *testing.T) {
	f := newFixture(t)
	m := New(f.app, "")
	t.Cleanup(m.unsubscribe)
	f.app.DB.Close() // the fixture's Close closes it again, which database/sql allows
	if _, ok := m.load()().(loadFailedMsg); !ok {
		t.Error("load on a closed database didn't fail")
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}
```

Add `"strconv"` to the imports.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/tui/`
Expected: FAIL, `New` undefined.

- [ ] **Step 3: Write `internal/tui/model.go`**

```go
// Package tui is whatnow's terminal companion: today's list, the day strip, what now, and chat,
// over the same parts the server uses.
package tui

import (
	"context"
	"slices"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/stream"
)

type mode int

const (
	modeList mode = iota
	modeHelp
)

const (
	// wideEnough is the narrowest terminal that fits chat beside the list.
	wideEnough   = 90
	refreshEvery = 5 * time.Second
	toastFor     = 4 * time.Second
)

type (
	snapshotMsg   struct{ snap snapshot }
	loadFailedMsg struct{ err error }
	tickMsg       struct{}
	hubMsg        stream.Message
)

// Model is the TUI. Its snapshot is replaced on every reload; everything else is the user's place
// on the screen.
type Model struct {
	app         *app.App
	ctx         context.Context
	webURL      string
	hub         <-chan stream.Message
	unsubscribe func()

	snap          *snapshot
	stale         bool
	loadErr       error
	width, height int
	cursor        int64 // the task under the cursor; 0 when the list is empty
	mode          mode
	input         textinput.Model
	spin          spinner.Model
	chatOpen      bool
}

// New makes the TUI and subscribes it to the hub. webURL is where the user starts and wraps the day.
func New(a *app.App, webURL string) Model {
	hub, unsubscribe := a.Hub.Subscribe()
	in := textinput.New()
	in.Prompt = "› "
	return Model{
		app:         a,
		ctx:         context.Background(),
		webURL:      webURL,
		hub:         hub,
		unsubscribe: unsubscribe,
		input:       in,
		spin:        spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
}

// Run shows the TUI on the terminal until the user quits.
func Run(a *app.App, webURL string) error {
	m := New(a, webURL)
	defer m.unsubscribe()
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.load(), m.listen(), tick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if m.width == 0 {
			m.chatOpen = msg.Width >= wideEnough
		}
		m.width, m.height = msg.Width, msg.Height
	case snapshotMsg:
		m.replace(msg.snap)
	case loadFailedMsg:
		m.stale, m.loadErr = true, msg.err
	case tickMsg:
		return m, tea.Batch(m.load(), tick())
	case hubMsg:
		return m.onHub(stream.Message(msg))
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

// replace takes a new snapshot. The cursor stays on its task; if that task left the list, it
// takes the task now at the same place.
func (m *Model) replace(s snapshot) {
	at := 0
	if m.snap != nil {
		at = max(slices.Index(m.snap.order(), m.cursor), 0)
	}
	m.snap, m.stale, m.loadErr = &s, false, nil
	ids := s.order()
	if slices.Contains(ids, m.cursor) {
		return
	}
	m.cursor = 0
	if len(ids) > 0 {
		m.cursor = ids[min(at, len(ids)-1)]
	}
}

func (m Model) onHub(msg stream.Message) (tea.Model, tea.Cmd) {
	return m, tea.Batch(m.listen(), m.load())
}

func (m Model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.mode == modeHelp {
		m.mode = modeList
		return m, nil
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "r":
		return m, m.load()
	}
	if m.snap == nil || m.snap.today.Plan == nil {
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "?":
		m.mode = modeHelp
	}
	return m, nil
}

func (m *Model) move(by int) {
	ids := m.snap.order()
	if len(ids) == 0 {
		return
	}
	at := slices.Index(ids, m.cursor) + by
	m.cursor = ids[min(max(at, 0), len(ids)-1)]
}
```

- [ ] **Step 4: Write `internal/tui/actions.go`**

```go
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) load() tea.Cmd {
	return func() tea.Msg {
		s, err := load(m.ctx, m.app)
		if err != nil {
			return loadFailedMsg{err}
		}
		return snapshotMsg{s}
	}
}

// listen hands the next hub message to the update loop, which listens again.
func (m Model) listen() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-m.hub
		if !ok {
			return nil
		}
		return hubMsg(msg)
	}
}

// tick reloads on a timer, which catches changes made in another process and moves the now line.
func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}
```

- [ ] **Step 5: Write `internal/tui/view.go`**

```go
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/jameynakama/whatnow/internal/domain"
)

const keyHint = "space done · w what now · a add · p push · x drop · u undo · c chat · ? keys · q quit"

var helpKeys = [][2]string{
	{"↑ ↓  k j", "move"},
	{"space", "mark done"},
	{"w", "what now"},
	{"n", "not this one, ask again"},
	{"a", "add a task"},
	{"p", "push to tomorrow"},
	{"x", "drop"},
	{"u", "undo your last change"},
	{"c", "chat"},
	{"r", "refresh"},
	{"q", "quit"},
}

func (m Model) View() string {
	switch {
	case m.width == 0:
		return ""
	case m.snap == nil && m.loadErr != nil:
		return "Couldn't load today: " + m.loadErr.Error()
	case m.snap == nil:
		return "Loading…"
	case m.snap.today.Plan == nil:
		return m.noDayView()
	case m.mode == modeHelp:
		return m.helpView()
	}
	return m.todayView()
}

func (m Model) noDayView() string {
	card := sticker([]string{
		"☀  Good morning.",
		"",
		"Start your day in the browser:",
		cursorStyle.Render(m.webURL),
		"",
		softStyle.Render("I'll pick it up from there."),
	}, min(44, m.width), 9)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card)
}

func (m Model) helpView() string {
	lines := []string{cursorStyle.Render("keys"), ""}
	for _, k := range helpKeys {
		lines = append(lines, fmt.Sprintf("%-10s %s", k[0], k[1]))
	}
	lines = append(lines, "", softStyle.Render("any key to close"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, sticker(lines, min(40, m.width), len(lines)+3))
}

func (m Model) todayView() string {
	top := []string{m.header()}
	if wrapDue(m.snap.now, m.snap.endOfDay) {
		top = append(top, ansi.Truncate(toastStyle.Render("Nearly the end of the day. Wrap up in the browser: "+m.webURL), m.width, "…"))
	}
	head := strings.Join(top, "\n")
	body := m.body(max(m.height-lipgloss.Height(head)-2, 4))
	return strings.Join([]string{head, body, m.toastLine(), m.footer()}, "\n")
}

// wrapDue says it's time to wrap up: within an hour of the end of day, or past it.
func wrapDue(now, endOfDay time.Time) bool {
	return !now.Before(endOfDay.Add(-time.Hour))
}

func (m Model) header() string {
	s := m.snap
	line := fmt.Sprintf("%s  %s  %s",
		cursorStyle.Render("whatnow"),
		s.now.Format("Mon Jan 2"),
		softStyle.Render(fmt.Sprintf("%d of %d done", s.doneToday, s.doneToday+len(s.order()))))
	if m.stale {
		line += "  " + nowStyle.Render("stale")
	}
	strip := layoutStrip(s.events, s.now, s.endOfDay, s.today.Day.StartedAt, max(m.width-5, 1)).render()
	return sticker(append([]string{line}, strings.Split(strip, "\n")...), m.width, 6)
}

func (m Model) body(h int) string {
	return sticker(m.listLines(h-3), m.width, h)
}

// listLines is the list, scrolled so the cursor and the line below it are in view.
func (m Model) listLines(rows int) []string {
	groups := m.snap.groups()
	if len(groups) == 0 {
		return []string{softStyle.Render("All clear. Nothing open on today's list.")}
	}
	var lines []string
	at := 0
	for _, g := range groups {
		if g.tier == "" {
			lines = append(lines, softStyle.Render("not planned"))
		} else {
			lines = append(lines, chip(strings.ToUpper(string(g.tier)), tierFill[g.tier]))
		}
		for _, id := range g.ids {
			if id == m.cursor {
				at = len(lines)
			}
			lines = append(lines, m.row(m.snap.tasks[id]))
		}
	}
	if top := at - rows + 2; top > 0 {
		lines = lines[top:]
	}
	return lines
}

func (m Model) row(t domain.Task) string {
	mark, title := "  ", t.Title
	if t.ID == m.cursor {
		mark, title = cursorStyle.Render("▸ "), lipgloss.NewStyle().Bold(true).Render(title)
	}
	line := mark + "☐ " + title
	if n := m.snap.waits[t.ID]; n > 0 {
		line += softStyle.Render(fmt.Sprintf("  ⛓ %d", n))
	}
	return line
}

func (m Model) toastLine() string {
	return ""
}

func (m Model) footer() string {
	return softStyle.Render(ansi.Truncate(keyHint, m.width, "…"))
}
```

- [ ] **Step 6: Wire `whatnow tui` and `just tui`**

In `cmd/whatnow/main.go`, import `"github.com/jameynakama/whatnow/internal/tui"` and add a case after `serve`:

```go
	case "tui":
		flags.Parse(os.Args[2:])
		err = runTUI(*dbPath)
```

and the function:

```go
func runTUI(dbPath string) error {
	a, err := open(dbPath)
	if err != nil {
		return err
	}
	defer a.Close()
	return tui.Run(a, "http://"+defaultAddr)
}
```

In `Justfile`, after `serve`:

```just
# The terminal companion. Start and wrap up the day in the web UI.
[positional-arguments]
tui *args:
    go run ./cmd/whatnow tui "$@"
```

- [ ] **Step 7: Make the goldens and look at them**

Run: `go test ./internal/tui/ -run 'TodayView|NoDayView' -update`, then open `internal/tui/testdata/today-80.golden`, `today-120.golden`, `no-day.golden`.

Check by eye: the header frame holds the title line and two strip rows; MUST, SHOULD, COULD headers appear in that order with "buy milk" under MUST; "renew passport" sits under "not planned"; every frame has its shadow column and bottom row; no line is wider than the terminal.

- [ ] **Step 8: Run all TUI tests**

Run: `go test ./internal/tui/`
Expected: PASS.

- [ ] **Step 9: Try it**

Run `just dev` in one terminal, start the day in the browser, then `just tui` in another. Check the list matches the web's Today and quits on `q`.

- [ ] **Step 10: Commit**

```bash
git add internal/tui cmd/whatnow/main.go Justfile
git commit -m "Add whatnow tui: today's list and day strip in the terminal"
```

---

### Task 5: Actions, toasts, and undo

**Files:**
- Modify: `internal/tui/model.go`, `internal/tui/actions.go`, `internal/tui/view.go`
- Create: `internal/tui/actions_test.go`

**Interfaces:**
- Consumes: Task 4's `Model`, `send`, `key`, `fixture.model`.
- Produces:
  ```go
  const modeAdd mode // add to the mode list
  type toast struct { text string; err bool; id int }
  // Model gains: toast toast; toastSeq int; lastBatch int64; asking, sending bool
  type wroteMsg struct{ batch int64; toast string }
  type undoneMsg struct{}
  type failedMsg struct{ err error }
  type toastExpiredMsg struct{ id int }
  func (m Model) commit(op func(ctx context.Context, batch int64) error) (int64, error)
  func (m Model) write(toast string, op func(ctx context.Context, batch int64) error) tea.Cmd
  func (m Model) show(text string, isErr bool) (Model, tea.Cmd)
  func (m Model) prompt(md mode, placeholder string) (tea.Model, tea.Cmd)
  func (m Model) onInput(k tea.KeyMsg) (tea.Model, tea.Cmd)
  func (m Model) current() (domain.Task, bool)
  func friendly(err error) string
  // test helpers: (f *fixture) run(t) *teatest.TestModel; waitFor(t, tm, text); quit(t, tm) Model
  ```
  `asking` and `sending` are read by Tasks 6 and 7.

- [ ] **Step 1: Write the failing tests**

`internal/tui/actions_test.go`:

```go
package tui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/jameynakama/whatnow/internal/agent"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
)

// run starts the TUI in a test program, 120 × 30.
func (f *fixture) run(t *testing.T) *teatest.TestModel {
	t.Helper()
	m := New(f.app, "http://127.0.0.1:7777")
	t.Cleanup(m.unsubscribe)
	return teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 30))
}

func waitFor(t *testing.T, tm *teatest.TestModel, text string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte(text)) },
		teatest.WithDuration(5*time.Second))
}

// quit ends the program and returns its last model. It sends ctrl+c, which quits from any mode.
func quit(t *testing.T, tm *teatest.TestModel) Model {
	t.Helper()
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	return tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
}

func (f *fixture) status(t *testing.T, id int64) domain.Task {
	t.Helper()
	task, err := f.app.Service.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestSpaceMarksDoneAndUUndoesIt(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.start(t)
	tm := f.run(t)
	waitFor(t, tm, "buy milk")

	tm.Send(key(" "))
	waitFor(t, tm, "Done: buy milk · u to undo")
	if got := f.status(t, milk.ID).Status; got != domain.StatusDone {
		t.Errorf("status = %s, want done", got)
	}

	tm.Send(key("u"))
	waitFor(t, tm, "Undone")
	if got := f.status(t, milk.ID).Status; got != domain.StatusOpen {
		t.Errorf("status after undo = %s, want open", got)
	}
	quit(t, tm)
}

func TestPPushesToTomorrow(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.start(t)
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("p"))
	waitFor(t, tm, "Pushed to tomorrow: buy milk")
	if got := f.status(t, milk.ID).NotBefore; got == nil || *got != "2026-10-07" {
		t.Errorf("not_before = %v, want 2026-10-07", got)
	}
	quit(t, tm)
}

func TestXDrops(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.start(t)
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("x"))
	waitFor(t, tm, "Dropped: buy milk")
	if got := f.status(t, milk.ID).Status; got != domain.StatusDropped {
		t.Errorf("status = %s, want dropped", got)
	}
	quit(t, tm)
}

func TestAddCapturesAndCleanupSplitsIt(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("a"))
	tm.Type("pour cereal and wash bowl")
	tm.Send(key("enter"))
	waitFor(t, tm, "Added: pour cereal and wash bowl")
	waitFor(t, tm, "wash bowl") // the fake's cleanup split it into two tasks
	quit(t, tm)
}

func TestEscCancelsAnAdd(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	m := send(f.model(t, 80, 24), key("a"), key("h"), key("esc"))
	if m.mode != modeList {
		t.Errorf("mode = %d after esc", m.mode)
	}
	if _, cmd := send(m, key("a")).Update(key("enter")); cmd != nil {
		t.Error("an empty add made a command")
	}
}

func TestUndoWithNothingToUndoSaysSo(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	if v := send(f.model(t, 80, 24), key("u")).View(); !strings.Contains(v, "Nothing to undo") {
		t.Errorf("view:\n%s", v)
	}
}

func TestActionsDoNothingOnAnEmptyList(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.start(t)
	ctx := context.Background()
	batch, _ := f.app.Service.NewBatch(ctx, domain.ActorUser)
	if _, err := f.app.Service.SetTaskStatus(ctx, batch, milk.ID, domain.StatusDone); err != nil {
		t.Fatal(err)
	}
	m := f.model(t, 80, 24)
	if !strings.Contains(m.View(), "All clear") {
		t.Errorf("view:\n%s", m.View())
	}
	for _, k := range []string{" ", "p", "x"} {
		if _, cmd := m.Update(key(k)); cmd != nil {
			t.Errorf("%q on an empty list made a command", k)
		}
	}
}

func TestAFailedWriteShowsTheError(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 80, 24)
	msg := m.write("x", func(context.Context, int64) error { return errors.New("disk full") })()
	if v := send(m, msg).View(); !strings.Contains(v, "disk full") {
		t.Errorf("view:\n%s", v)
	}
}

func TestFriendlyErrors(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{planner.ErrNothingToDo, "Nothing is free to start right now."},
		{agent.ErrBusy, "A reply is still being written."},
		{errors.New("disk full"), "disk full"},
	} {
		if got := friendly(c.err); got != c.want {
			t.Errorf("friendly(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestAToastLeavesWhenItsTimeIsUp(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m, _ := f.model(t, 80, 24).show("hello", false)
	older := m.toast.id
	m, _ = m.show("again", false)
	if send(m, toastExpiredMsg{older}).toast.text != "again" {
		t.Error("an older toast's timer cleared a newer toast")
	}
	if send(m, toastExpiredMsg{m.toast.id}).toast.text != "" {
		t.Error("the toast stayed past its time")
	}
}

```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/tui/`
Expected: FAIL to compile: `show`, `write`, `friendly`, `toastExpiredMsg` undefined.

- [ ] **Step 3: Extend `model.go`**

Add `modeAdd` to the modes, after `modeList`:

```go
const (
	modeList mode = iota
	modeAdd
	modeHelp
)
```

Add the toast type and messages:

```go
type toast struct {
	text string
	err  bool
	id   int
}

type (
	wroteMsg        struct {
		batch int64
		toast string
	}
	undoneMsg       struct{}
	failedMsg       struct{ err error }
	toastExpiredMsg struct{ id int }
)
```

Add fields to `Model`, after `chatOpen`:

```go
	toast     toast
	toastSeq  int
	lastBatch int64 // the user's last change from this TUI, for u
	asking    bool  // what now is thinking
	sending   bool  // a chat reply is being written
```

Add cases to `Update`, before `case tea.KeyMsg`:

```go
	case wroteMsg:
		m.lastBatch = msg.batch
		next, cmd := m.show(msg.toast+" · u to undo", false)
		return next, tea.Batch(cmd, next.load())
	case undoneMsg:
		m.lastBatch = 0
		next, cmd := m.show("Undone", false)
		return next, tea.Batch(cmd, next.load())
	case failedMsg:
		m.asking, m.sending = false, false
		return m.show(friendly(msg.err), true)
	case toastExpiredMsg:
		if msg.id == m.toast.id {
			m.toast = toast{}
		}
	case spinner.TickMsg:
		if !m.asking && !m.sending {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
```

Replace `onKey` and add `onInput`, `prompt`, `current`, `show`:

```go
func (m Model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeHelp:
		m.mode = modeList
		return m, nil
	case modeAdd:
		return m.onInput(k)
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "r":
		return m, m.load()
	}
	if m.snap == nil || m.snap.today.Plan == nil {
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "?":
		m.mode = modeHelp
	case "a":
		return m.prompt(modeAdd, "What's on your mind?")
	case "u":
		if m.lastBatch == 0 {
			return m.show("Nothing to undo", false)
		}
		return m, m.undo(m.lastBatch)
	case " ", "p", "x":
		if t, ok := m.current(); ok {
			return m, m.act(k.String(), t)
		}
	}
	return m, nil
}

// onInput handles keys while the one-line input is open.
func (m Model) onInput(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		m.input.Reset()
		m.mode = modeList
		m.input.Blur()
		if text == "" {
			return m, nil
		}
		return m, m.capture(text)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

func (m Model) prompt(md mode, placeholder string) (tea.Model, tea.Cmd) {
	m.mode = md
	m.input.Reset()
	m.input.Placeholder = placeholder
	return m, m.input.Focus()
}

// current is the task under the cursor, if the list has one.
func (m Model) current() (domain.Task, bool) {
	t, ok := m.snap.tasks[m.cursor]
	return t, ok
}

func (m Model) show(text string, isErr bool) (Model, tea.Cmd) {
	m.toastSeq++
	m.toast = toast{text: text, err: isErr, id: m.toastSeq}
	id := m.toastSeq
	return m, tea.Tick(toastFor, func(time.Time) tea.Msg { return toastExpiredMsg{id} })
}
```

Add `"strings"` and `"github.com/jameynakama/whatnow/internal/domain"` to the imports.

- [ ] **Step 4: Extend `actions.go`**

```go
// commit runs op in a new user batch and announces the batch, as the API's writes do.
func (m Model) commit(op func(ctx context.Context, batch int64) error) (int64, error) {
	batch, err := m.app.Service.NewBatch(m.ctx, domain.ActorUser)
	if err != nil {
		return 0, err
	}
	if err := op(m.ctx, batch); err != nil {
		return 0, err
	}
	acts, err := m.app.Service.BatchActivity(m.ctx, batch)
	if err != nil {
		return 0, err
	}
	m.app.Hub.PublishBatch(batch, stream.SourceUser, acts)
	return batch, nil
}

// write commits op and toasts what it did, or why it failed.
func (m Model) write(toast string, op func(ctx context.Context, batch int64) error) tea.Cmd {
	return func() tea.Msg {
		batch, err := m.commit(op)
		if err != nil {
			return failedMsg{err}
		}
		return wroteMsg{batch: batch, toast: toast}
	}
}

// act runs one of the list's task keys on t.
func (m Model) act(key string, t domain.Task) tea.Cmd {
	svc := m.app.Service
	switch key {
	case "p":
		tomorrow := domain.Date(m.app.Now().In(m.app.Loc).AddDate(0, 0, 1).Format(time.DateOnly))
		return m.write("Pushed to tomorrow: "+t.Title, func(ctx context.Context, batch int64) error {
			_, err := svc.PushTask(ctx, batch, t.ID, tomorrow)
			return err
		})
	case "x":
		return m.write("Dropped: "+t.Title, func(ctx context.Context, batch int64) error {
			_, err := svc.SetTaskStatus(ctx, batch, t.ID, domain.StatusDropped)
			return err
		})
	}
	return m.write("Done: "+t.Title, func(ctx context.Context, batch int64) error {
		_, err := svc.SetTaskStatus(ctx, batch, t.ID, domain.StatusDone)
		return err
	})
}

// capture saves a line as a raw card and queues it for cleanup, like the web's Capture.
func (m Model) capture(text string) tea.Cmd {
	return m.write("Added: "+text, func(ctx context.Context, batch int64) error {
		task, err := m.app.Service.CreateTask(ctx, batch, service.TaskFields{RawText: text})
		if err == nil {
			m.app.Cleanup.Enqueue(task)
		}
		return err
	})
}

func (m Model) undo(batch int64) tea.Cmd {
	return func() tea.Msg {
		if err := m.app.Service.Undo(m.ctx, batch); err != nil {
			return failedMsg{err}
		}
		m.app.Hub.PublishUndone(batch)
		return undoneMsg{}
	}
}

// friendly says what went wrong in plain words, for the errors a user can expect.
func friendly(err error) string {
	switch {
	case errors.Is(err, planner.ErrNothingToDo):
		return "Nothing is free to start right now."
	case errors.Is(err, agent.ErrBusy):
		return "A reply is still being written."
	}
	return err.Error()
}
```

Imports: `"context"`, `"errors"`, and the `agent`, `domain`, `planner`, `service`, `stream` packages.

- [ ] **Step 5: Show the toast and the add input in `view.go`**

Replace `toastLine` and `footer`:

```go
func (m Model) toastLine() string {
	if m.toast.text == "" {
		return ""
	}
	style := toastStyle
	if m.toast.err {
		style = errorStyle
	}
	return ansi.Truncate(style.Render(m.toast.text), m.width, "…")
}

func (m Model) footer() string {
	if m.mode == modeAdd {
		return ansi.Truncate(m.input.View(), m.width, "…")
	}
	return softStyle.Render(ansi.Truncate(keyHint, m.width, "…"))
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/tui/`
Expected: PASS. If a `waitFor` times out, rerun with `-v` and print `tm.Output()`'s bytes: the renderer only writes lines that changed, so the text must be on a line that changes when it appears.

- [ ] **Step 7: Commit**

```bash
git add internal/tui
git commit -m "Let the TUI mark done, push, drop, add, and undo, with toasts"
```

---

### Task 6: What now

**Files:**
- Modify: `internal/tui/model.go`, `internal/tui/actions.go`, `internal/tui/view.go`
- Create: `internal/tui/whatnow_test.go`

**Interfaces:**
- Consumes: Task 5's `commit`, `prompt`, `onInput`, `show`, `asking`.
- Produces:
  ```go
  const modePass mode
  type pickedMsg struct{ pick planner.Pick }
  // Model gains: pick *planner.Pick; passing int64 (the task being passed on)
  func (m Model) whatNow(exclude int64, reason string) tea.Cmd
  ```

- [ ] **Step 1: Write the failing tests**

`internal/tui/whatnow_test.go`:

```go
package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/llm/fake"
)

func TestWMovesTheCursorToThePickAndShowsWhy(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.task(t, "call mom")
	f.start(t)
	want, err := f.app.Planner.WhatNow(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("w"))
	waitFor(t, tm, fake.Reason)
	if m := quit(t, tm); m.cursor != want.TaskID || m.pick == nil {
		t.Errorf("cursor = %d, want the pick %d", m.cursor, want.TaskID)
	}
}

func TestNPassesOnThePickWithAReason(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.task(t, "call mom")
	f.start(t)
	ctx := context.Background()
	first, err := f.app.Planner.WhatNow(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.app.Planner.WhatNow(ctx, first.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("w"))
	waitFor(t, tm, fake.Reason)
	tm.Send(key("n"))
	tm.Type("too tired")
	tm.Send(key("enter"))
	waitFor(t, tm, "▸ ☐ "+f.status(t, second.TaskID).Title)
	quit(t, tm)
	if got := f.status(t, first.TaskID).Feeling; !strings.Contains(got, "too tired") {
		t.Errorf("feeling = %q", got)
	}
}

func TestNDoesNothingWithoutAPick(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	if m := send(f.model(t, 80, 24), key("n")); m.mode != modeList {
		t.Errorf("mode = %d", m.mode)
	}
}

func TestThePickClearsWhenItsTaskLeaves(t *testing.T) {
	f := newFixture(t)
	milk := f.task(t, "buy milk")
	f.task(t, "call mom")
	f.start(t)
	m := send(f.model(t, 80, 24), pickedMsg{pick: planner.Pick{TaskID: milk.ID, Reason: "go"}})
	s := f.load(t)
	task := s.tasks[milk.ID]
	task.Status = domain.StatusDone
	s.tasks[milk.ID] = task
	if send(m, snapshotMsg{s}).pick != nil {
		t.Error("the pick outlived its task")
	}
}
```

Add `"github.com/jameynakama/whatnow/internal/domain"` and `"github.com/jameynakama/whatnow/internal/planner"` to the imports.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/tui/ -run 'Pick|Passes|WMoves|NDoes'`
Expected: FAIL to compile: `pickedMsg`, `m.pick` undefined.

- [ ] **Step 3: Extend `model.go`**

Add `modePass` after `modeAdd`. Add `pickedMsg struct{ pick planner.Pick }` to the messages. Add fields after `sending`:

```go
	pick    *planner.Pick
	passing int64 // the pick being passed on while its reason is typed
```

Add a case to `Update`:

```go
	case pickedMsg:
		m.asking = false
		p := msg.pick
		m.pick, m.cursor = &p, p.TaskID
		return m, m.load()
```

At the end of `replace`, before the cursor logic, drop a pick whose task left:

```go
	if m.pick != nil && !slices.Contains(s.order(), m.pick.TaskID) {
		m.pick = nil
	}
```

In `onKey`, route `modePass` to `onInput` alongside `modeAdd`, and add the keys:

```go
	case "w":
		if m.asking {
			return m, nil
		}
		m.asking = true
		return m, tea.Batch(m.whatNow(0, ""), m.spin.Tick)
	case "n":
		if m.pick == nil || m.asking {
			return m, nil
		}
		m.passing = m.pick.TaskID
		return m.prompt(modePass, "Why not? (optional)")
```

In `onInput`'s `enter` case, branch on the mode before resetting it:

```go
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		md := m.mode
		m.input.Reset()
		m.mode = modeList
		m.input.Blur()
		if md == modePass {
			m.asking = true
			return m, tea.Batch(m.whatNow(m.passing, text), m.spin.Tick)
		}
		if text == "" {
			return m, nil
		}
		return m, m.capture(text)
```

- [ ] **Step 4: Add `whatNow` to `actions.go`**

```go
// whatNow asks for one task to start now. Passing on a task leaves it out of this answer, and a
// reason is added to its feeling note first, as the API's "not this one" does.
func (m Model) whatNow(exclude int64, reason string) tea.Cmd {
	return func() tea.Msg {
		if exclude != 0 && reason != "" {
			if _, err := m.commit(func(ctx context.Context, batch int64) error {
				_, err := m.app.Service.AppendFeeling(ctx, batch, exclude, reason)
				return err
			}); err != nil {
				return failedMsg{err}
			}
		}
		pick, err := m.app.Planner.WhatNow(m.ctx, exclude)
		if err != nil {
			return failedMsg{err}
		}
		return pickedMsg{pick}
	}
}
```

- [ ] **Step 5: Show the pick and the pass input in `view.go`**

In `listLines`, after appending a task's row:

```go
			if m.pick != nil && m.pick.TaskID == id {
				lines = append(lines, softStyle.Italic(true).Render("    ↳ "+m.pick.Reason+"  ·  n not this one"))
			}
```

In `row`, before `return line`:

```go
	if m.pick != nil && m.pick.TaskID == t.ID {
		line += "  " + chip("what now", chipFill)
	}
```

In `header`, after the stale marker:

```go
	if m.asking || m.sending {
		line += "  " + m.spin.View()
	}
```

In `footer`, show the input for `modePass` too: `if m.mode == modeAdd || m.mode == modePass`.

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/tui/`
Expected: PASS. The goldens don't change: nothing is picked in them.

- [ ] **Step 7: Commit**

```bash
git add internal/tui
git commit -m "Add what now to the TUI, with not-this-one"
```

---

### Task 7: Chat

**Files:**
- Modify: `internal/tui/model.go`, `internal/tui/actions.go`, `internal/tui/view.go`
- Create: `internal/tui/chat_test.go`
- Regenerate: `internal/tui/testdata/today-120.golden`

**Interfaces:**
- Consumes: Task 5's `prompt`, `onInput`, `sending`; Task 4's `chatOpen`, `onHub`.
- Produces:
  ```go
  const modeChat mode
  type chatSentMsg struct{}
  // Model gains: streaming string
  func (m Model) send(text string) tea.Cmd
  func (m Model) chatLines(w, rows int) []string
  ```

- [ ] **Step 1: Write the failing tests**

`internal/tui/chat_test.go`:

```go
package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/agent"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm/fake"
	"github.com/jameynakama/whatnow/internal/stream"
)

func TestChatStreamsAReply(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("c"))
	tm.Type("hello")
	tm.Send(key("enter"))
	waitFor(t, tm, fake.ChatReply)
	quit(t, tm)

	history, err := f.app.Agent.History(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Text != "hello" || history[1].Text != fake.ChatReply {
		t.Errorf("history = %+v", history)
	}
}

func TestStreamedPiecesEndInOneMessage(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := send(f.model(t, 120, 30),
		hubMsg(stream.Message{Type: "chat.delta", Data: agent.Delta{Text: "A fake "}}),
		hubMsg(stream.Message{Type: "chat.delta", Data: agent.Delta{Text: "reply."}}))
	if m.streaming != "A fake reply." {
		t.Fatalf("streaming = %q", m.streaming)
	}
	if !strings.Contains(m.View(), "A fake reply.") {
		t.Errorf("the reply isn't shown while it streams:\n%s", m.View())
	}
	m = send(m, hubMsg(stream.Message{Type: "chat.message", Data: domain.Message{Role: domain.RoleAssistant, Text: "A fake reply."}}))
	if m.streaming != "" || len(m.snap.chat) != 1 {
		t.Errorf("streaming = %q, chat = %+v", m.streaming, m.snap.chat)
	}
}

func TestChatOpensBesideTheListOnAWideTerminal(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	if v := f.model(t, 120, 30).View(); !strings.Contains(v, "c to chat") || !strings.Contains(v, "buy milk") {
		t.Errorf("view:\n%s", v)
	}
}

func TestChatReplacesTheListOnANarrowTerminalUntilEsc(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	m := f.model(t, 80, 24)
	if m.chatOpen {
		t.Fatal("chat starts open on a narrow terminal")
	}
	m = send(m, key("c"))
	if v := m.View(); strings.Contains(v, "buy milk") {
		t.Errorf("the list shows beside chat at 80 columns:\n%s", v)
	}
	m = send(m, key("esc"))
	if m.chatOpen || m.mode != modeList {
		t.Errorf("esc left chat open (%v) or mode %d", m.chatOpen, m.mode)
	}
}

func TestEnterWhileAReplyIsBeingWrittenWaits(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := send(f.model(t, 120, 30), key("c"))
	m.sending = true
	m = send(m, key("h"), key("i"))
	if _, cmd := m.Update(key("enter")); cmd != nil {
		t.Error("a second message was sent while the first reply was being written")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/tui/ -run Chat`
Expected: FAIL to compile: `m.streaming` undefined.

- [ ] **Step 3: Extend `model.go`**

Add `modeChat` after `modePass`. Add `chatSentMsg struct{}` to the messages and `streaming string` to `Model`. Add an `Update` case:

```go
	case chatSentMsg:
		m.sending = false
		return m, m.load()
```

Replace `onHub`. Chat messages go straight onto the screen; the next reload replaces them with the stored history.

```go
func (m Model) onHub(msg stream.Message) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case "chat.delta":
		m.streaming += msg.Data.(agent.Delta).Text
		return m, m.listen()
	case "chat.message":
		said := msg.Data.(domain.Message)
		if said.Role == domain.RoleAssistant {
			m.streaming = ""
		}
		if m.snap != nil {
			m.snap.chat = append(m.snap.chat, said)
		}
		return m, m.listen()
	}
	return m, tea.Batch(m.listen(), m.load())
}
```

In `onKey`, route `modeChat` to `onInput` with the others, and add:

```go
	case "c":
		m.chatOpen = true
		return m.prompt(modeChat, "Message")
```

In `onInput`, `esc` hides chat on a narrow terminal:

```go
	case "esc":
		if m.mode == modeChat && m.width < wideEnough {
			m.chatOpen = false
		}
		m.mode = modeList
		m.input.Blur()
		return m, nil
```

and `enter` keeps chat focused after sending. Put this at the top of the `enter` case:

```go
		if m.mode == modeChat {
			text := strings.TrimSpace(m.input.Value())
			if text == "" || m.sending {
				return m, nil
			}
			m.input.Reset()
			m.sending = true
			return m, tea.Batch(m.send(text), m.spin.Tick)
		}
```

- [ ] **Step 4: Add `send` to `actions.go`**

```go
// send sends a chat message. Its reply streams in over the hub while the turn runs.
func (m Model) send(text string) tea.Cmd {
	return func() tea.Msg {
		if _, err := m.app.Agent.Send(m.ctx, text); err != nil {
			return failedMsg{err}
		}
		return chatSentMsg{}
	}
}
```

- [ ] **Step 5: Draw the chat pane in `view.go`**

Replace `body`:

```go
func (m Model) body(h int) string {
	switch {
	case !m.chatOpen:
		return sticker(m.listLines(h-3), m.width, h)
	case m.width < wideEnough:
		return sticker(m.chatLines(m.width-5, h-3), m.width, h)
	}
	listW := m.width * 3 / 5
	return lipgloss.JoinHorizontal(lipgloss.Top,
		sticker(m.listLines(h-3), listW, h),
		sticker(m.chatLines(m.width-listW-5, h-3), m.width-listW, h))
}

// chatLines is today's chat, newest at the bottom above the input, wrapped to w columns.
func (m Model) chatLines(w, rows int) []string {
	var lines []string
	say := func(who, text string, style lipgloss.Style) {
		wrapped := lipgloss.NewStyle().Width(max(w-5, 1)).Render(text)
		for i, l := range strings.Split(wrapped, "\n") {
			label := "     "
			if i == 0 {
				label = style.Render(fmt.Sprintf("%-5s", who))
			}
			lines = append(lines, label+l)
		}
	}
	for _, msg := range m.snap.chat {
		if msg.Role == domain.RoleUser {
			say("you", msg.Text, softStyle)
		} else {
			say("wn", msg.Text, cursorStyle)
		}
	}
	if m.streaming != "" {
		say("wn", m.streaming, cursorStyle)
	}
	input := softStyle.Render("c to chat")
	if m.mode == modeChat {
		input = m.input.View()
	}
	room := max(rows-2, 0)
	if len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	for len(lines) < room {
		lines = append([]string{""}, lines...)
	}
	return append(lines, "", input)
}
```

In `footer`, while chat is focused show its keys:

```go
	if m.mode == modeChat {
		return softStyle.Render(ansi.Truncate("enter send · esc back to the list", m.width, "…"))
	}
```

- [ ] **Step 6: Regenerate the wide golden and look at it**

Run: `go test ./internal/tui/ -run TodayView -update`, then open `testdata/today-120.golden`. Check the list takes about three fifths of the width, the chat pane sits right of it with "c to chat" at its bottom, and both have shadows. `today-80.golden` must not change: `git diff internal/tui/testdata/today-80.golden` is empty.

- [ ] **Step 7: Run everything**

Run: `go test ./... && go vet ./... && go test -coverprofile=/tmp/tui.out ./internal/tui/ ./internal/app/ && go tool cover -func=/tmp/tui.out | grep -v '100.0%'`
Expected: tests PASS. The coverage listing shows only the accepted gaps from Global Constraints (`Run`, `tick`, `show`, `load`) and the `total:` line. Cover any other gap with a behavior test, not a test of the implementation.

- [ ] **Step 8: Try it for real**

Run `just dev`, start the day in the browser, then `just tui` in a second terminal at full width and again in a narrow split. Check: chat replies stream in; `w` picks and `n` passes; a task marked done in the browser leaves the TUI's list within 5 seconds; a task added in the TUI shows up cleaned in the browser after a refresh.

- [ ] **Step 9: Commit**

```bash
git add internal/tui
git commit -m "Add chat to the TUI, beside the list or in its place on a narrow terminal"
```
