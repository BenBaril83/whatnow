# whatnow Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A running `whatnow serve` binary with SQLite persistence, a single write path that logs every change into undoable batches, a REST API for tasks, blockers, and events, and an SSE stream that announces every write.

**Architecture:** `store` opens SQLite and applies embedded migrations. `service` is the only write path: every write belongs to a batch and records an activity row with before and after snapshots, which is what undo replays. `httpapi` wraps `service` in JSON handlers and publishes each batch to a `stream.Hub`, which fans out to SSE clients. No LLM code in this plan.

**Tech Stack:** Go 1.27, `modernc.org/sqlite` (pure Go), `net/http` ServeMux patterns, stdlib `testing`, `github.com/google/go-cmp`.

**Spec:** `docs/superpowers/specs/2026-10-05-whatnow-design.md`

**Where this sits:** this is plan 1 of the spec. Later plans, each written when the previous one lands:
1. Core (this plan)
2. LLM foundation: providers, settings and keychain, prompt layers, `facts`, cleanup
3. Days and planning: good morning, planner, what now, wrap up, day summaries
4. Agent chat
5. Web UI (can start against this plan's API as soon as it lands)
6. Packaging and prompt evals

## Global Constraints

- Module path: `github.com/jameynakama/whatnow`. Go directive: `go 1.27`.
- SQLite through `modernc.org/sqlite` only. No cgo.
- The server binds `127.0.0.1` by default and never `0.0.0.0`.
- All API routes live under `/api/v1/`.
- Timestamps are stored as UTC text in the fixed-width layout `2006-01-02T15:04:05.000000000Z07:00`, so string comparison orders them correctly.
- Dates (`due_on`, `not_before`) are `YYYY-MM-DD` strings.
- Tests use stdlib `testing`, table-driven where there are cases, and `go-cmp` for struct diffs. No assertion libraries.
- Commit messages: plain imperative subject, no `Co-Authored-By` trailer.

## Review Focus

1. **A web page on another origin sends requests to `localhost:7777`** (a cross-site form post or DNS rebinding). Expected: rejected before any handler runs. Pinned in Task 6.
2. **The default database path contains a space** (`~/Library/Application Support/whatnow`). Expected: opens normally. Pinned in Task 1.
3. **Undoing a batch after a later batch edited the same task.** Expected: refused with a conflict, and nothing changes. Pinned in Task 5.
4. **Raw text whose first line is long and multibyte (emoji, CJK).** Expected: the derived title is cut on a rune boundary and stays valid UTF-8. Pinned in Task 2.
5. **Blocker cycles, direct or transitive.** Expected: rejected, since a cycle would leave every task in it blocked forever. Pinned in Task 3.

---

## File Structure

| File | Responsibility |
|---|---|
| `go.mod`, `go.sum` | Module definition |
| `Justfile` | `test`, `cover`, `lint`, `serve` recipes |
| `.gitignore` | Build and coverage output |
| `cmd/whatnow/main.go` | CLI entry point: `whatnow serve` |
| `internal/store/store.go` | Open SQLite, apply migrations |
| `internal/store/migrations/0001_init.sql` | Schema for tasks, blockers, events, batches, activity |
| `internal/domain/domain.go` | Shared types and their validity checks |
| `internal/service/service.go` | `Service`, errors, batches, activity, transaction and time helpers |
| `internal/service/tasks.go` | Task writes and reads |
| `internal/service/blockers.go` | Blocker writes and reads, cycle check |
| `internal/service/events.go` | Event writes and reads |
| `internal/service/undo.go` | Undo and last-undoable-batch lookup |
| `internal/stream/hub.go` | Fan-out of messages to SSE subscribers |
| `internal/httpapi/server.go` | Router, local-only guard, JSON and error helpers, write-and-publish helper |
| `internal/httpapi/tasks.go` | Task, blocker, and undo handlers |
| `internal/httpapi/events.go` | Event handlers |
| `internal/httpapi/stream.go` | SSE handler |

Tests sit next to the code they cover, as `*_test.go` in external `_test` packages.

---

### Task 1: Scaffold and store

**Files:**
- Create: `go.mod`, `Justfile`, `.gitignore`
- Create: `internal/store/store.go`
- Create: `internal/store/migrations/0001_init.sql`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: `store.Open(path string) (*sql.DB, error)`. Creates the file if missing and applies pending migrations. The caller creates the parent directory.

- [ ] **Step 1: Initialize the module and tooling**

```bash
go mod init github.com/jameynakama/whatnow
go mod edit -go=1.27
go get modernc.org/sqlite github.com/google/go-cmp/cmp
```

Create `Justfile`:

```just
default:
    @just --list

test:
    go test ./...

cover:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out | tail -1

lint:
    go vet ./...

# Positional arguments keep quoted paths with spaces intact.
[positional-arguments]
serve *args:
    go run ./cmd/whatnow serve "$@"
```

Create `.gitignore`:

```
coverage.out
/whatnow
```

- [ ] **Step 2: Write the failing tests**

Create `internal/store/store_test.go`:

```go
package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jameynakama/whatnow/internal/store"
)

func TestOpenCreatesSchema(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, table := range []string{"tasks", "blockers", "events", "batches", "activity"} {
		var n int
		err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %q missing", table)
		}
	}
}

func TestOpenTwiceKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO batches (actor, created_at) VALUES ('user', 'x')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM batches`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("batches = %d after reopen, want 1", n)
	}
}

func TestOpenPathWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "w.db"))
	if err != nil {
		t.Fatalf("open path with spaces: %v", err)
	}
	db.Close()
}

func TestForeignKeysEnforced(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO blockers (task_id, blocked_by_id) VALUES (1, 2)`)
	if err == nil {
		t.Fatal("expected a foreign key error for blockers on missing tasks")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/store/`
Expected: FAIL, `undefined: store.Open`

- [ ] **Step 4: Write the migration**

Create `internal/store/migrations/0001_init.sql`:

```sql
CREATE TABLE tasks (
    id INTEGER PRIMARY KEY,
    title TEXT NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    raw_text TEXT NOT NULL DEFAULT '',
    size TEXT,
    energy TEXT,
    feeling TEXT NOT NULL DEFAULT '',
    due_on TEXT,
    not_before TEXT,
    status TEXT NOT NULL DEFAULT 'open',
    refs TEXT NOT NULL DEFAULT '[]',
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE TABLE blockers (
    task_id INTEGER NOT NULL REFERENCES tasks(id),
    blocked_by_id INTEGER NOT NULL REFERENCES tasks(id),
    PRIMARY KEY (task_id, blocked_by_id)
);

CREATE TABLE events (
    id INTEGER PRIMARY KEY,
    title TEXT NOT NULL,
    starts_at TEXT NOT NULL,
    ends_at TEXT,
    raw_text TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);
CREATE INDEX events_starts_at ON events(starts_at);

CREATE TABLE batches (
    id INTEGER PRIMARY KEY,
    actor TEXT NOT NULL,
    created_at TEXT NOT NULL,
    undone_at TEXT
);

CREATE TABLE activity (
    id INTEGER PRIMARY KEY,
    batch_id INTEGER NOT NULL REFERENCES batches(id),
    action TEXT NOT NULL,
    entity TEXT NOT NULL,
    entity_id INTEGER NOT NULL,
    before_json TEXT,
    after_json TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX activity_batch ON activity(batch_id);
CREATE INDEX activity_entity ON activity(entity, entity_id);
```

- [ ] **Step 5: Write `store.Open`**

Create `internal/store/store.go`:

```go
// Package store opens the SQLite database and applies migrations.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open opens the database at path, creating the file if needed, and applies pending migrations.
// The parent directory must exist.
func Open(path string) (*sql.DB, error) {
	// A plain path, not a file: URI, so spaces in the path need no escaping.
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer, and a single-user app never needs more than one connection.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		if err := apply(db, name); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func apply(db *sql.DB, name string) error {
	var done int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&done); err != nil {
		return err
	}
	if done > 0 {
		return nil
	}
	body, err := migrations.ReadFile(name)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(string(body)); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
		return err
	}
	return tx.Commit()
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/store/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum Justfile .gitignore internal/store
git commit -m "Add module scaffold and SQLite store with migrations"
```

---

### Task 2: Domain types and task writes

**Files:**
- Create: `internal/domain/domain.go`
- Create: `internal/service/service.go`
- Create: `internal/service/tasks.go`
- Test: `internal/service/helpers_test.go`, `internal/service/tasks_test.go`

**Interfaces:**
- Consumes: `store.Open`.
- Produces:
  - `domain.Actor` (`ActorUser`, `ActorAgent`), `domain.Status` (`StatusOpen`, `StatusDone`, `StatusDropped`), `domain.Size` (`SizeS`, `SizeM`, `SizeL`), `domain.Energy` (`EnergyLow`, `EnergyMed`, `EnergyHigh`), `domain.Date`, each with `Valid() bool`.
  - `domain.Ref{Label, URL string}`, `domain.Task`, `domain.Blocker{TaskID, BlockedByID int64}`, `domain.Event`, `domain.Activity`.
  - `service.ErrInvalid`, `service.ErrNotFound`, `service.ErrConflict`. Errors wrap these, so callers use `errors.Is`.
  - `service.New(db *sql.DB, now func() time.Time) *Service`
  - `(*Service).NewBatch(ctx, actor domain.Actor) (int64, error)`
  - `(*Service).BatchActivity(ctx, batch int64) ([]domain.Activity, error)`
  - `service.TaskFields{Title, Notes, RawText string; Size *domain.Size; Energy *domain.Energy; Feeling string; DueOn, NotBefore *domain.Date; Refs []domain.Ref}`
  - `(*Service).CreateTask(ctx, batch int64, f TaskFields) (domain.Task, error)`
  - `(*Service).UpdateTask(ctx, batch, id int64, f TaskFields) (domain.Task, error)`
  - `(*Service).SetTaskStatus(ctx, batch, id int64, status domain.Status) (domain.Task, error)`
  - `(*Service).DeleteTask(ctx, batch, id int64) error`
  - `(*Service).GetTask(ctx, id int64) (domain.Task, error)`
  - `(*Service).ListTasks(ctx) ([]domain.Task, error)`. Never nil.
  - Package-internal helpers later tasks in `service` use: `querier`, `scanner`, `inTx`, `batchActor`, `record`, `batchActivity`, `formatTime`, `parseTime`, `formatNullTime`, `parseNullTime`, `toNull`, `fromNull`, `liveTask`, `writeTask`, and the `action*` and `entity*` constants.

- [ ] **Step 1: Write the domain types**

These are plain types, and the tests in Step 3 exercise them through the service. Create `internal/domain/domain.go`:

```go
// Package domain holds the types shared by the service and API layers.
package domain

import (
	"encoding/json"
	"time"
)

// Actor is who made a change.
type Actor string

const (
	ActorUser  Actor = "user"
	ActorAgent Actor = "agent"
)

func (a Actor) Valid() bool { return a == ActorUser || a == ActorAgent }

type Status string

const (
	StatusOpen    Status = "open"
	StatusDone    Status = "done"
	StatusDropped Status = "dropped"
)

func (s Status) Valid() bool { return s == StatusOpen || s == StatusDone || s == StatusDropped }

type Size string

const (
	SizeS Size = "S"
	SizeM Size = "M"
	SizeL Size = "L"
)

func (s Size) Valid() bool { return s == SizeS || s == SizeM || s == SizeL }

type Energy string

const (
	EnergyLow  Energy = "low"
	EnergyMed  Energy = "med"
	EnergyHigh Energy = "high"
)

func (e Energy) Valid() bool { return e == EnergyLow || e == EnergyMed || e == EnergyHigh }

// Date is a calendar date in YYYY-MM-DD form.
type Date string

func (d Date) Valid() bool {
	_, err := time.Parse(time.DateOnly, string(d))
	return err == nil
}

// Ref is a link or key pulled out of a task's text, such as a ticket key or a URL.
type Ref struct {
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}

type Task struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	Notes     string     `json:"notes"`
	RawText   string     `json:"raw_text"`
	Size      *Size      `json:"size"`
	Energy    *Energy    `json:"energy"`
	Feeling   string     `json:"feeling"`
	DueOn     *Date      `json:"due_on"`
	NotBefore *Date      `json:"not_before"`
	Status    Status     `json:"status"`
	Refs      []Ref      `json:"refs"`
	CreatedBy Actor      `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at"`
}

// Blocker says TaskID cannot start until BlockedByID is finished.
type Blocker struct {
	TaskID      int64 `json:"task_id"`
	BlockedByID int64 `json:"blocked_by_id"`
}

// Event is anything at a fixed time.
type Event struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	RawText   string     `json:"raw_text"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at"`
}

// Activity is one recorded change. Before is null for a creation and After is null for a removal.
type Activity struct {
	ID        int64           `json:"id"`
	BatchID   int64           `json:"batch_id"`
	Action    string          `json:"action"`
	Entity    string          `json:"entity"`
	EntityID  int64           `json:"entity_id"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	CreatedAt time.Time       `json:"created_at"`
}
```

- [ ] **Step 2: Write the shared test helpers**

Create `internal/service/helpers_test.go`:

```go
package service_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/store"
)

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time          { return c.t }
func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newService(t *testing.T) (*service.Service, *testClock) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := &testClock{t: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)}
	return service.New(db, clock.now), clock
}

func newBatch(t *testing.T, svc *service.Service, actor domain.Actor) int64 {
	t.Helper()
	batch, err := svc.NewBatch(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func createTask(t *testing.T, svc *service.Service, title string) domain.Task {
	t.Helper()
	task, err := svc.CreateTask(context.Background(), newBatch(t, svc, domain.ActorUser), service.TaskFields{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func ptr[T any](v T) *T { return &v }
```

- [ ] **Step 3: Write the failing task tests**

Create `internal/service/tasks_test.go`:

```go
package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestCreateTaskFromRawText(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	raw := "  do FLEX1-666 today\nit feels difficult, needs pubsub research first"

	got, err := svc.CreateTask(ctx, newBatch(t, svc, domain.ActorAgent), service.TaskFields{RawText: raw})
	if err != nil {
		t.Fatal(err)
	}

	if got.Title != "do FLEX1-666 today" {
		t.Errorf("title = %q, want the raw text's first line", got.Title)
	}
	if got.RawText != raw {
		t.Errorf("raw text not kept verbatim: %q", got.RawText)
	}
	if got.Status != domain.StatusOpen || got.CreatedBy != domain.ActorAgent {
		t.Errorf("status %q, created_by %q; want open, agent", got.Status, got.CreatedBy)
	}

	listed, err := svc.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]domain.Task{got}, listed); diff != "" {
		t.Errorf("stored task differs from returned task (-returned +stored):\n%s", diff)
	}
}

func TestTitleFromLongMultibyteRawText(t *testing.T) {
	svc, _ := newService(t)
	raw := strings.Repeat("🔥", 200)

	got, err := svc.CreateTask(context.Background(), newBatch(t, svc, domain.ActorUser), service.TaskFields{RawText: raw})
	if err != nil {
		t.Fatal(err)
	}

	if !utf8.ValidString(got.Title) {
		t.Fatalf("title is not valid UTF-8: %q", got.Title)
	}
	if n := utf8.RuneCountInString(got.Title); n != 120 {
		t.Errorf("title is %d runes, want 120", n)
	}
	if !strings.HasSuffix(got.Title, "…") {
		t.Errorf("truncated title should end with an ellipsis: %q", got.Title)
	}
}

func TestCreateTaskRejectsInvalidFields(t *testing.T) {
	cases := map[string]service.TaskFields{
		"no title or raw text": {Title: "   "},
		"bad size":             {Title: "x", Size: ptr(domain.Size("XL"))},
		"bad energy":           {Title: "x", Energy: ptr(domain.Energy("frantic"))},
		"bad due_on":           {Title: "x", DueOn: ptr(domain.Date("tomorrow"))},
		"bad not_before":       {Title: "x", NotBefore: ptr(domain.Date("2026-13-01"))},
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _ := newService(t)
			_, err := svc.CreateTask(context.Background(), newBatch(t, svc, domain.ActorUser), fields)
			if !errors.Is(err, service.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCreateTaskUnknownBatch(t *testing.T) {
	svc, _ := newService(t)
	_, err := svc.CreateTask(context.Background(), 999, service.TaskFields{Title: "x"})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateTaskKeepsRawTextAndRecordsActivity(t *testing.T) {
	svc, clock := newService(t)
	ctx := context.Background()
	created, err := svc.CreateTask(ctx, newBatch(t, svc, domain.ActorUser), service.TaskFields{RawText: "review 420"})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Hour)

	batch := newBatch(t, svc, domain.ActorAgent)
	updated, err := svc.UpdateTask(ctx, batch, created.ID, service.TaskFields{
		Title:   "Review FLEX1-420",
		Size:    ptr(domain.SizeS),
		DueOn:   ptr(domain.Date("2026-10-05")),
		Refs:    []domain.Ref{{Label: "FLEX1-420"}},
		RawText: "this should be ignored",
	})
	if err != nil {
		t.Fatal(err)
	}

	if updated.RawText != "review 420" {
		t.Errorf("raw text changed on update: %q", updated.RawText)
	}
	if !updated.UpdatedAt.Equal(clock.now()) {
		t.Errorf("updated_at = %v, want %v", updated.UpdatedAt, clock.now())
	}

	acts, err := svc.BatchActivity(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 {
		t.Fatalf("got %d activity rows, want 1", len(acts))
	}
	var before, after domain.Task
	if err := json.Unmarshal(acts[0].Before, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(acts[0].After, &after); err != nil {
		t.Fatal(err)
	}
	if before.Title != "review 420" || after.Title != "Review FLEX1-420" {
		t.Errorf("snapshots: before %q, after %q", before.Title, after.Title)
	}
}

func TestStatusChangesAndRestore(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "write the doc")

	dropped, err := svc.SetTaskStatus(ctx, newBatch(t, svc, domain.ActorUser), task.ID, domain.StatusDropped)
	if err != nil {
		t.Fatal(err)
	}
	if dropped.Status != domain.StatusDropped {
		t.Fatalf("status = %q, want dropped", dropped.Status)
	}
	restored, err := svc.SetTaskStatus(ctx, newBatch(t, svc, domain.ActorUser), task.ID, domain.StatusOpen)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != domain.StatusOpen {
		t.Errorf("status = %q, want open", restored.Status)
	}

	_, err = svc.SetTaskStatus(ctx, newBatch(t, svc, domain.ActorUser), task.ID, domain.Status("paused"))
	if !errors.Is(err, service.ErrInvalid) {
		t.Errorf("bad status: err = %v, want ErrInvalid", err)
	}
}

func TestDeletedTaskIsGone(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "temp")

	if err := svc.DeleteTask(ctx, newBatch(t, svc, domain.ActorUser), task.ID); err != nil {
		t.Fatal(err)
	}

	tasks, err := svc.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("deleted task still listed: %+v", tasks)
	}
	if _, err := svc.GetTask(ctx, task.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("GetTask err = %v, want ErrNotFound", err)
	}
	_, err = svc.UpdateTask(ctx, newBatch(t, svc, domain.ActorUser), task.ID, service.TaskFields{Title: "back?"})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("UpdateTask err = %v, want ErrNotFound", err)
	}
}

func TestListTasksEmptyIsNotNil(t *testing.T) {
	svc, _ := newService(t)
	tasks, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tasks == nil {
		t.Error("ListTasks returned nil; the API would encode it as null")
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/service/`
Expected: FAIL to compile, `undefined: service.New` and friends.

- [ ] **Step 5: Write the service core**

Create `internal/service/service.go`:

```go
// Package service is the only write path for tasks, blockers, and events. Every write belongs to a
// batch and records an activity row with before and after snapshots, which is what undo replays.
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
)

var (
	ErrInvalid  = errors.New("invalid")
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

const (
	entityTask    = "task"
	entityBlocker = "blocker"
	entityEvent   = "event"

	actionCreate = "create"
	actionUpdate = "update"
	actionStatus = "status"
	actionDelete = "delete"
	actionAdd    = "add"
	actionRemove = "remove"
)

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB, now func() time.Time) *Service {
	return &Service{db: db, now: now}
}

// NewBatch starts a batch. Every write names one, and undo reverts a whole batch at once.
func (s *Service) NewBatch(ctx context.Context, actor domain.Actor) (int64, error) {
	if !actor.Valid() {
		return 0, fmt.Errorf("%w: actor %q", ErrInvalid, actor)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO batches (actor, created_at) VALUES (?, ?)`, actor, formatTime(s.now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// BatchActivity returns a batch's changes in the order they happened.
func (s *Service) BatchActivity(ctx context.Context, batch int64) ([]domain.Activity, error) {
	return batchActivity(ctx, s.db, batch)
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type scanner interface {
	Scan(dest ...any) error
}

func (s *Service) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// batchActor returns the actor of a batch that can still take writes.
func batchActor(ctx context.Context, tx *sql.Tx, batch int64) (domain.Actor, error) {
	var actor domain.Actor
	var undone sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT actor, undone_at FROM batches WHERE id = ?`, batch).Scan(&actor, &undone)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: batch %d", ErrNotFound, batch)
	}
	if err != nil {
		return "", err
	}
	if undone.Valid {
		return "", fmt.Errorf("%w: batch %d was undone", ErrConflict, batch)
	}
	return actor, nil
}

// record writes one activity row. Pass a nil before for a creation and a nil after for a removal.
func record(ctx context.Context, tx *sql.Tx, batch int64, action, entity string, entityID int64, before, after any) error {
	b, err := snapshot(before)
	if err != nil {
		return err
	}
	a, err := snapshot(after)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO activity (batch_id, action, entity, entity_id, before_json, after_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, (SELECT created_at FROM batches WHERE id = ?))`,
		batch, action, entity, entityID, b, a, batch)
	return err
}

func snapshot(v any) (sql.NullString, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func batchActivity(ctx context.Context, q querier, batch int64) ([]domain.Activity, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, batch_id, action, entity, entity_id, before_json, after_json, created_at
		 FROM activity WHERE batch_id = ? ORDER BY id`, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acts := []domain.Activity{}
	for rows.Next() {
		var a domain.Activity
		var before, after sql.NullString
		var createdAt string
		if err := rows.Scan(&a.ID, &a.BatchID, &a.Action, &a.Entity, &a.EntityID, &before, &after, &createdAt); err != nil {
			return nil, err
		}
		if before.Valid {
			a.Before = json.RawMessage(before.String)
		}
		if after.Valid {
			a.After = json.RawMessage(after.String)
		}
		if a.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		acts = append(acts, a)
	}
	return acts, rows.Err()
}

// timeLayout is fixed width, so stored timestamps sort correctly as strings.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }

func formatNullTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*t), Valid: true}
}

func parseNullTime(n sql.NullString) (*time.Time, error) {
	if !n.Valid {
		return nil, nil
	}
	t, err := parseTime(n.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func toNull[T ~string](p *T) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*p), Valid: true}
}

func fromNull[T ~string](n sql.NullString) *T {
	if !n.Valid {
		return nil
	}
	v := T(n.String)
	return &v
}
```

- [ ] **Step 6: Write the task functions**

Create `internal/service/tasks.go`:

```go
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jameynakama/whatnow/internal/domain"
)

// TaskFields are a task's editable fields. RawText is read only on create: it is the user's
// original words, and edits never change it. An empty Title on create defaults to the raw text's
// first line.
type TaskFields struct {
	Title     string         `json:"title"`
	Notes     string         `json:"notes"`
	RawText   string         `json:"raw_text"`
	Size      *domain.Size   `json:"size"`
	Energy    *domain.Energy `json:"energy"`
	Feeling   string         `json:"feeling"`
	DueOn     *domain.Date   `json:"due_on"`
	NotBefore *domain.Date   `json:"not_before"`
	Refs      []domain.Ref   `json:"refs"`
}

const maxTitleRunes = 120

func (f *TaskFields) normalize() error {
	f.Title = strings.TrimSpace(f.Title)
	if f.Title == "" {
		f.Title = titleFromRaw(f.RawText)
	}
	if f.Title == "" {
		return fmt.Errorf("%w: title or raw_text is required", ErrInvalid)
	}
	if f.Size != nil && !f.Size.Valid() {
		return fmt.Errorf("%w: size %q", ErrInvalid, *f.Size)
	}
	if f.Energy != nil && !f.Energy.Valid() {
		return fmt.Errorf("%w: energy %q", ErrInvalid, *f.Energy)
	}
	if f.DueOn != nil && !f.DueOn.Valid() {
		return fmt.Errorf("%w: due_on %q", ErrInvalid, *f.DueOn)
	}
	if f.NotBefore != nil && !f.NotBefore.Valid() {
		return fmt.Errorf("%w: not_before %q", ErrInvalid, *f.NotBefore)
	}
	if f.Refs == nil {
		f.Refs = []domain.Ref{}
	}
	return nil
}

func (f TaskFields) applyTo(t *domain.Task) {
	t.Title = f.Title
	t.Notes = f.Notes
	t.Size = f.Size
	t.Energy = f.Energy
	t.Feeling = f.Feeling
	t.DueOn = f.DueOn
	t.NotBefore = f.NotBefore
	t.Refs = f.Refs
}

// titleFromRaw returns the raw text's first line, cut on a rune boundary.
func titleFromRaw(raw string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(raw), "\n")
	runes := []rune(strings.TrimSpace(line))
	if len(runes) > maxTitleRunes {
		return string(runes[:maxTitleRunes-1]) + "…"
	}
	return string(runes)
}

func (s *Service) CreateTask(ctx context.Context, batch int64, f TaskFields) (domain.Task, error) {
	if err := f.normalize(); err != nil {
		return domain.Task{}, err
	}
	var t domain.Task
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		actor, err := batchActor(ctx, tx, batch)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		t = domain.Task{RawText: f.RawText, Status: domain.StatusOpen, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
		f.applyTo(&t)
		if err := insertTask(ctx, tx, &t); err != nil {
			return err
		}
		return record(ctx, tx, batch, actionCreate, entityTask, t.ID, nil, t)
	})
	return t, err
}

func (s *Service) UpdateTask(ctx context.Context, batch, id int64, f TaskFields) (domain.Task, error) {
	f.RawText = ""
	if err := f.normalize(); err != nil {
		return domain.Task{}, err
	}
	return s.changeTask(ctx, batch, id, actionUpdate, f.applyTo)
}

func (s *Service) SetTaskStatus(ctx context.Context, batch, id int64, status domain.Status) (domain.Task, error) {
	if !status.Valid() {
		return domain.Task{}, fmt.Errorf("%w: status %q", ErrInvalid, status)
	}
	return s.changeTask(ctx, batch, id, actionStatus, func(t *domain.Task) { t.Status = status })
}

// DeleteTask soft-deletes a task. Dropping (a status) means "not doing it"; deleting means
// "this shouldn't exist", and deleted tasks are hidden everywhere.
func (s *Service) DeleteTask(ctx context.Context, batch, id int64) error {
	_, err := s.changeTask(ctx, batch, id, actionDelete, func(t *domain.Task) {
		deleted := s.now().UTC()
		t.DeletedAt = &deleted
	})
	return err
}

func (s *Service) changeTask(ctx context.Context, batch, id int64, action string, change func(*domain.Task)) (domain.Task, error) {
	var t domain.Task
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		before, err := liveTask(ctx, tx, id)
		if err != nil {
			return err
		}
		t = before
		change(&t)
		t.UpdatedAt = s.now().UTC()
		if err := writeTask(ctx, tx, t); err != nil {
			return err
		}
		return record(ctx, tx, batch, action, entityTask, id, before, t)
	})
	return t, err
}

func (s *Service) GetTask(ctx context.Context, id int64) (domain.Task, error) {
	return liveTask(ctx, s.db, id)
}

// ListTasks returns every task that isn't deleted, oldest first.
func (s *Service) ListTasks(ctx context.Context) ([]domain.Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []domain.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

const taskColumns = `id, title, notes, raw_text, size, energy, feeling, due_on, not_before, status, refs,
	created_by, created_at, updated_at, deleted_at`

func liveTask(ctx context.Context, q querier, id int64) (domain.Task, error) {
	t, err := scanTask(q.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ? AND deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("%w: task %d", ErrNotFound, id)
	}
	return t, err
}

func scanTask(row scanner) (domain.Task, error) {
	var t domain.Task
	var size, energy, dueOn, notBefore, deletedAt sql.NullString
	var refs, createdAt, updatedAt string
	err := row.Scan(&t.ID, &t.Title, &t.Notes, &t.RawText, &size, &energy, &t.Feeling, &dueOn, &notBefore,
		&t.Status, &refs, &t.CreatedBy, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return t, err
	}
	t.Size = fromNull[domain.Size](size)
	t.Energy = fromNull[domain.Energy](energy)
	t.DueOn = fromNull[domain.Date](dueOn)
	t.NotBefore = fromNull[domain.Date](notBefore)
	if err := json.Unmarshal([]byte(refs), &t.Refs); err != nil {
		return t, err
	}
	if t.CreatedAt, err = parseTime(createdAt); err != nil {
		return t, err
	}
	if t.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return t, err
	}
	t.DeletedAt, err = parseNullTime(deletedAt)
	return t, err
}

func insertTask(ctx context.Context, tx *sql.Tx, t *domain.Task) error {
	refs, err := json.Marshal(t.Refs)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO tasks (title, notes, raw_text, size, energy, feeling, due_on, not_before, status, refs,
		 created_by, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Title, t.Notes, t.RawText, toNull(t.Size), toNull(t.Energy), t.Feeling, toNull(t.DueOn), toNull(t.NotBefore),
		string(t.Status), string(refs), string(t.CreatedBy), formatTime(t.CreatedAt), formatTime(t.UpdatedAt),
		formatNullTime(t.DeletedAt))
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// writeTask overwrites every column of an existing task. Undo uses it to restore a snapshot.
func writeTask(ctx context.Context, tx *sql.Tx, t domain.Task) error {
	refs, err := json.Marshal(t.Refs)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE tasks SET title = ?, notes = ?, raw_text = ?, size = ?, energy = ?, feeling = ?, due_on = ?,
		 not_before = ?, status = ?, refs = ?, created_by = ?, created_at = ?, updated_at = ?, deleted_at = ?
		 WHERE id = ?`,
		t.Title, t.Notes, t.RawText, toNull(t.Size), toNull(t.Energy), t.Feeling, toNull(t.DueOn), toNull(t.NotBefore),
		string(t.Status), string(refs), string(t.CreatedBy), formatTime(t.CreatedAt), formatTime(t.UpdatedAt),
		formatNullTime(t.DeletedAt), t.ID)
	return err
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/service/`
Expected: PASS. Then `go vet ./...` with no output.

- [ ] **Step 8: Commit**

```bash
git add internal/domain internal/service
git commit -m "Add domain types and task writes with activity log"
```

---

### Task 3: Blockers

**Files:**
- Create: `internal/service/blockers.go`
- Test: `internal/service/blockers_test.go`

**Interfaces:**
- Consumes: `inTx`, `batchActor`, `liveTask`, `record`, `entityBlocker`, `actionAdd`, `actionRemove` from Task 2.
- Produces:
  - `(*Service).AddBlocker(ctx, batch int64, b domain.Blocker) error`. Adding an existing blocker is a no-op that records nothing.
  - `(*Service).RemoveBlocker(ctx, batch int64, b domain.Blocker) error`
  - `(*Service).ListBlockers(ctx) ([]domain.Blocker, error)`. Only blockers whose two tasks both exist and aren't deleted. Never nil.

- [ ] **Step 1: Write the failing tests**

Create `internal/service/blockers_test.go`:

```go
package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestAddAndRemoveBlocker(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	work := createTask(t, svc, "do FLEX1-666")
	research := createTask(t, svc, "research pubsub")
	b := domain.Blocker{TaskID: work.ID, BlockedByID: research.ID}

	if err := svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorAgent), b); err != nil {
		t.Fatal(err)
	}
	got, err := svc.ListBlockers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]domain.Blocker{b}, got); diff != "" {
		t.Errorf("blockers (-want +got):\n%s", diff)
	}

	if err := svc.RemoveBlocker(ctx, newBatch(t, svc, domain.ActorUser), b); err != nil {
		t.Fatal(err)
	}
	got, err = svc.ListBlockers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("blocker still listed after removal: %+v", got)
	}
}

func TestAddBlockerTwiceRecordsOnce(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	a, b := createTask(t, svc, "a"), createTask(t, svc, "b")
	blocker := domain.Blocker{TaskID: a.ID, BlockedByID: b.ID}
	if err := svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorUser), blocker); err != nil {
		t.Fatal(err)
	}

	batch := newBatch(t, svc, domain.ActorUser)
	if err := svc.AddBlocker(ctx, batch, blocker); err != nil {
		t.Fatalf("second add: %v", err)
	}
	acts, err := svc.BatchActivity(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 0 {
		t.Errorf("a duplicate add recorded %d activity rows, want 0", len(acts))
	}
}

func TestAddBlockerRejectsCycles(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	a, b, c := createTask(t, svc, "a"), createTask(t, svc, "b"), createTask(t, svc, "c")
	add := func(task, by int64) error {
		return svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorUser), domain.Blocker{TaskID: task, BlockedByID: by})
	}
	if err := add(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := add(b.ID, c.ID); err != nil {
		t.Fatal(err)
	}

	cases := map[string][2]int64{
		"self":       {a.ID, a.ID},
		"direct":     {b.ID, a.ID},
		"transitive": {c.ID, a.ID},
	}
	for name, pair := range cases {
		t.Run(name, func(t *testing.T) {
			if err := add(pair[0], pair[1]); !errors.Is(err, service.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestBlockerErrors(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	a := createTask(t, svc, "a")

	err := svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorUser), domain.Blocker{TaskID: a.ID, BlockedByID: 999})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown task: err = %v, want ErrNotFound", err)
	}
	err = svc.RemoveBlocker(ctx, newBatch(t, svc, domain.ActorUser), domain.Blocker{TaskID: a.ID, BlockedByID: 999})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("missing blocker: err = %v, want ErrNotFound", err)
	}
}

func TestListBlockersHidesDeletedTasks(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	a, b := createTask(t, svc, "a"), createTask(t, svc, "b")
	if err := svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorUser), domain.Blocker{TaskID: a.ID, BlockedByID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteTask(ctx, newBatch(t, svc, domain.ActorUser), b.ID); err != nil {
		t.Fatal(err)
	}

	got, err := svc.ListBlockers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("blocker on a deleted task still listed: %+v", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/service/ -run Blocker`
Expected: FAIL to compile, `svc.AddBlocker undefined`

- [ ] **Step 3: Write the blocker functions**

Create `internal/service/blockers.go`:

```go
package service

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jameynakama/whatnow/internal/domain"
)

// AddBlocker records that b.TaskID waits on b.BlockedByID. It rejects any blocker that would form
// a cycle, since every task in a cycle would stay blocked forever.
func (s *Service) AddBlocker(ctx context.Context, batch int64, b domain.Blocker) error {
	if b.TaskID == b.BlockedByID {
		return fmt.Errorf("%w: a task cannot block itself", ErrInvalid)
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		for _, id := range []int64{b.TaskID, b.BlockedByID} {
			if _, err := liveTask(ctx, tx, id); err != nil {
				return err
			}
		}
		cycle, err := waitsOn(ctx, tx, b.BlockedByID, b.TaskID)
		if err != nil {
			return err
		}
		if cycle {
			return fmt.Errorf("%w: task %d already waits on task %d", ErrInvalid, b.BlockedByID, b.TaskID)
		}
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO blockers (task_id, blocked_by_id) VALUES (?, ?)`,
			b.TaskID, b.BlockedByID)
		if err != nil {
			return err
		}
		added, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if added == 0 {
			return nil
		}
		return record(ctx, tx, batch, actionAdd, entityBlocker, b.TaskID, nil, b)
	})
}

func (s *Service) RemoveBlocker(ctx context.Context, batch int64, b domain.Blocker) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM blockers WHERE task_id = ? AND blocked_by_id = ?`,
			b.TaskID, b.BlockedByID)
		if err != nil {
			return err
		}
		removed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if removed == 0 {
			return fmt.Errorf("%w: task %d is not blocked by task %d", ErrNotFound, b.TaskID, b.BlockedByID)
		}
		return record(ctx, tx, batch, actionRemove, entityBlocker, b.TaskID, b, nil)
	})
}

func (s *Service) ListBlockers(ctx context.Context) ([]domain.Blocker, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT b.task_id, b.blocked_by_id FROM blockers b
		 JOIN tasks t ON t.id = b.task_id AND t.deleted_at IS NULL
		 JOIN tasks blocking ON blocking.id = b.blocked_by_id AND blocking.deleted_at IS NULL
		 ORDER BY b.task_id, b.blocked_by_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blockers := []domain.Blocker{}
	for rows.Next() {
		var b domain.Blocker
		if err := rows.Scan(&b.TaskID, &b.BlockedByID); err != nil {
			return nil, err
		}
		blockers = append(blockers, b)
	}
	return blockers, rows.Err()
}

// waitsOn reports whether task from already waits on task to, directly or through other tasks.
func waitsOn(ctx context.Context, tx *sql.Tx, from, to int64) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT task_id, blocked_by_id FROM blockers`)
	if err != nil {
		return false, err
	}
	edges := map[int64][]int64{}
	for rows.Next() {
		var task, by int64
		if err := rows.Scan(&task, &by); err != nil {
			rows.Close()
			return false, err
		}
		edges[task] = append(edges[task], by)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	seen := map[int64]bool{}
	stack := []int64{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == to {
			return true, nil
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		stack = append(stack, edges[n]...)
	}
	return false, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/service/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/blockers.go internal/service/blockers_test.go
git commit -m "Add blockers with cycle rejection"
```

---

### Task 4: Events

**Files:**
- Create: `internal/service/events.go`
- Test: `internal/service/events_test.go`

**Interfaces:**
- Consumes: Task 2's helpers.
- Produces:
  - `service.EventFields{Title string; StartsAt time.Time; EndsAt *time.Time; RawText string}` with JSON tags `title`, `starts_at`, `ends_at`, `raw_text`. RawText is read only on create.
  - `(*Service).CreateEvent(ctx, batch int64, f EventFields) (domain.Event, error)`
  - `(*Service).UpdateEvent(ctx, batch, id int64, f EventFields) (domain.Event, error)`
  - `(*Service).DeleteEvent(ctx, batch, id int64) error`
  - `(*Service).ListEvents(ctx, from, to time.Time) ([]domain.Event, error)`. Events starting in `[from, to)`, ordered by start. Never nil.
  - Package-internal: `writeEvent(ctx, tx, domain.Event) error`, used by undo.

- [ ] **Step 1: Write the failing tests**

Create `internal/service/events_test.go`:

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

func TestListEventsForALocalDay(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	loc := time.FixedZone("UTC-5", -5*3600)
	at := func(day, hour, min int) time.Time { return time.Date(2026, 10, day, hour, min, 0, 0, loc) }
	create := func(title string, start time.Time) {
		t.Helper()
		_, err := svc.CreateEvent(ctx, newBatch(t, svc, domain.ActorUser), service.EventFields{Title: title, StartsAt: start})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Created out of order, and straddling midnight in UTC: 23:30 local is 04:30 UTC the next day.
	create("late call", at(5, 23, 30))
	create("standup", at(5, 10, 0))
	create("yesterday", at(4, 23, 59))
	create("tomorrow", at(6, 0, 0))

	from := at(5, 0, 0)
	got, err := svc.ListEvents(ctx, from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}

	var titles []string
	for _, e := range got {
		titles = append(titles, e.Title)
	}
	if len(titles) != 2 || titles[0] != "standup" || titles[1] != "late call" {
		t.Errorf("titles = %v, want [standup late call]", titles)
	}
}

func TestEventValidation(t *testing.T) {
	start := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	cases := map[string]service.EventFields{
		"no title":          {StartsAt: start},
		"no start":          {Title: "1:1"},
		"ends before start": {Title: "1:1", StartsAt: start, EndsAt: ptr(start.Add(-time.Minute))},
		"ends at start":     {Title: "1:1", StartsAt: start, EndsAt: ptr(start)},
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _ := newService(t)
			_, err := svc.CreateEvent(context.Background(), newBatch(t, svc, domain.ActorUser), fields)
			if !errors.Is(err, service.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestUpdateAndDeleteEvent(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	e, err := svc.CreateEvent(ctx, newBatch(t, svc, domain.ActorUser),
		service.EventFields{Title: "1:1", StartsAt: start, RawText: "1:1 at 2"})
	if err != nil {
		t.Fatal(err)
	}

	end := start.Add(30 * time.Minute)
	updated, err := svc.UpdateEvent(ctx, newBatch(t, svc, domain.ActorUser), e.ID,
		service.EventFields{Title: "1:1 with Sam", StartsAt: start, EndsAt: &end})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "1:1 with Sam" || updated.EndsAt == nil || !updated.EndsAt.Equal(end) {
		t.Errorf("update not applied: %+v", updated)
	}
	if updated.RawText != "1:1 at 2" {
		t.Errorf("raw text changed on update: %q", updated.RawText)
	}

	if err := svc.DeleteEvent(ctx, newBatch(t, svc, domain.ActorUser), e.ID); err != nil {
		t.Fatal(err)
	}
	got, err := svc.ListEvents(ctx, start.Add(-time.Hour), start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("deleted event still listed: %+v", got)
	}
	_, err = svc.UpdateEvent(ctx, newBatch(t, svc, domain.ActorUser), e.ID, service.EventFields{Title: "x", StartsAt: start})
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("update deleted event: err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/service/ -run Event`
Expected: FAIL to compile, `svc.CreateEvent undefined`

- [ ] **Step 3: Write the event functions**

Create `internal/service/events.go`:

```go
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
)

// EventFields are an event's editable fields. RawText is read only on create.
type EventFields struct {
	Title    string     `json:"title"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`
	RawText  string     `json:"raw_text"`
}

func (f *EventFields) normalize() error {
	f.Title = strings.TrimSpace(f.Title)
	if f.Title == "" {
		return fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if f.StartsAt.IsZero() {
		return fmt.Errorf("%w: starts_at is required", ErrInvalid)
	}
	if f.EndsAt != nil && !f.EndsAt.After(f.StartsAt) {
		return fmt.Errorf("%w: ends_at must be after starts_at", ErrInvalid)
	}
	return nil
}

func (f EventFields) applyTo(e *domain.Event) {
	e.Title = f.Title
	e.StartsAt = f.StartsAt.UTC()
	e.EndsAt = nil
	if f.EndsAt != nil {
		end := f.EndsAt.UTC()
		e.EndsAt = &end
	}
}

func (s *Service) CreateEvent(ctx context.Context, batch int64, f EventFields) (domain.Event, error) {
	if err := f.normalize(); err != nil {
		return domain.Event{}, err
	}
	var e domain.Event
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		now := s.now().UTC()
		e = domain.Event{RawText: f.RawText, CreatedAt: now, UpdatedAt: now}
		f.applyTo(&e)
		res, err := tx.ExecContext(ctx,
			`INSERT INTO events (title, starts_at, ends_at, raw_text, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			e.Title, formatTime(e.StartsAt), formatNullTime(e.EndsAt), e.RawText, formatTime(e.CreatedAt), formatTime(e.UpdatedAt))
		if err != nil {
			return err
		}
		if e.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return record(ctx, tx, batch, actionCreate, entityEvent, e.ID, nil, e)
	})
	return e, err
}

func (s *Service) UpdateEvent(ctx context.Context, batch, id int64, f EventFields) (domain.Event, error) {
	f.RawText = ""
	if err := f.normalize(); err != nil {
		return domain.Event{}, err
	}
	return s.changeEvent(ctx, batch, id, actionUpdate, f.applyTo)
}

func (s *Service) DeleteEvent(ctx context.Context, batch, id int64) error {
	_, err := s.changeEvent(ctx, batch, id, actionDelete, func(e *domain.Event) {
		deleted := s.now().UTC()
		e.DeletedAt = &deleted
	})
	return err
}

func (s *Service) changeEvent(ctx context.Context, batch, id int64, action string, change func(*domain.Event)) (domain.Event, error) {
	var e domain.Event
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		before, err := scanEvent(tx.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE id = ? AND deleted_at IS NULL`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: event %d", ErrNotFound, id)
		}
		if err != nil {
			return err
		}
		e = before
		change(&e)
		e.UpdatedAt = s.now().UTC()
		if err := writeEvent(ctx, tx, e); err != nil {
			return err
		}
		return record(ctx, tx, batch, action, entityEvent, id, before, e)
	})
	return e, err
}

// ListEvents returns events that start in [from, to), earliest first.
func (s *Service) ListEvents(ctx context.Context, from, to time.Time) ([]domain.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM events
		 WHERE deleted_at IS NULL AND starts_at >= ? AND starts_at < ? ORDER BY starts_at`,
		formatTime(from), formatTime(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []domain.Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

const eventColumns = `id, title, starts_at, ends_at, raw_text, created_at, updated_at, deleted_at`

func scanEvent(row scanner) (domain.Event, error) {
	var e domain.Event
	var startsAt, createdAt, updatedAt string
	var endsAt, deletedAt sql.NullString
	if err := row.Scan(&e.ID, &e.Title, &startsAt, &endsAt, &e.RawText, &createdAt, &updatedAt, &deletedAt); err != nil {
		return e, err
	}
	var err error
	if e.StartsAt, err = parseTime(startsAt); err != nil {
		return e, err
	}
	if e.EndsAt, err = parseNullTime(endsAt); err != nil {
		return e, err
	}
	if e.CreatedAt, err = parseTime(createdAt); err != nil {
		return e, err
	}
	if e.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return e, err
	}
	e.DeletedAt, err = parseNullTime(deletedAt)
	return e, err
}

// writeEvent overwrites every column of an existing event. Undo uses it to restore a snapshot.
func writeEvent(ctx context.Context, tx *sql.Tx, e domain.Event) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE events SET title = ?, starts_at = ?, ends_at = ?, raw_text = ?, created_at = ?, updated_at = ?,
		 deleted_at = ? WHERE id = ?`,
		e.Title, formatTime(e.StartsAt), formatNullTime(e.EndsAt), e.RawText, formatTime(e.CreatedAt),
		formatTime(e.UpdatedAt), formatNullTime(e.DeletedAt), e.ID)
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/service/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/events.go internal/service/events_test.go
git commit -m "Add events"
```

---

### Task 5: Undo

**Files:**
- Create: `internal/service/undo.go`
- Test: `internal/service/undo_test.go`

**Interfaces:**
- Consumes: `batchActor`, `batchActivity`, `writeTask`, `writeEvent`, the entity constants.
- Produces:
  - `(*Service).Undo(ctx, batch int64) error`. Reverts every change in the batch, newest first, then marks it undone. Returns `ErrConflict` if the batch is already undone or a later batch that hasn't been undone touched the same entity. Returns `ErrNotFound` for an unknown batch.
  - `(*Service).LastUndoableBatch(ctx, actor domain.Actor) (int64, error)`. The newest batch by that actor that has activity and isn't undone. `ErrNotFound` if there is none. The agent's `undo_last` tool (plan 4) calls this with `ActorAgent`.

- [ ] **Step 1: Write the failing tests**

Create `internal/service/undo_test.go`:

```go
package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestUndoRevertsAWholeBatch(t *testing.T) {
	svc, clock := newService(t)
	ctx := context.Background()
	existing := createTask(t, svc, "do FLEX1-666")
	gone := createTask(t, svc, "duplicate")
	start := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	event, err := svc.CreateEvent(ctx, newBatch(t, svc, domain.ActorUser), service.EventFields{Title: "1:1", StartsAt: start})
	if err != nil {
		t.Fatal(err)
	}
	tasksBefore, _ := svc.ListTasks(ctx)
	clock.advance(time.Minute)

	// The kind of batch cleanup produces: rename, add, block, delete, and an event change.
	batch := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.UpdateTask(ctx, batch, existing.ID, service.TaskFields{Title: "Do FLEX1-666", Size: ptr(domain.SizeL)}); err != nil {
		t.Fatal(err)
	}
	research, err := svc.CreateTask(ctx, batch, service.TaskFields{Title: "research pubsub"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddBlocker(ctx, batch, domain.Blocker{TaskID: existing.ID, BlockedByID: research.ID}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteTask(ctx, batch, gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteEvent(ctx, batch, event.ID); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}

	tasksAfter, err := svc.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(tasksBefore, tasksAfter); diff != "" {
		t.Errorf("tasks not restored exactly (-before +after undo):\n%s", diff)
	}
	blockers, _ := svc.ListBlockers(ctx)
	if len(blockers) != 0 {
		t.Errorf("blocker survived undo: %+v", blockers)
	}
	events, _ := svc.ListEvents(ctx, start.Add(-time.Hour), start.Add(time.Hour))
	if len(events) != 1 {
		t.Errorf("deleted event not restored: %+v", events)
	}
}

func TestUndoRestoresARemovedBlocker(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	a, b := createTask(t, svc, "a"), createTask(t, svc, "b")
	blocker := domain.Blocker{TaskID: a.ID, BlockedByID: b.ID}
	if err := svc.AddBlocker(ctx, newBatch(t, svc, domain.ActorUser), blocker); err != nil {
		t.Fatal(err)
	}
	batch := newBatch(t, svc, domain.ActorUser)
	if err := svc.RemoveBlocker(ctx, batch, blocker); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.ListBlockers(ctx)
	if diff := cmp.Diff([]domain.Blocker{blocker}, got); diff != "" {
		t.Errorf("blockers (-want +got):\n%s", diff)
	}
}

func TestUndoRefusesWhenALaterBatchTouchedTheSameTask(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "original")
	agentBatch := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.UpdateTask(ctx, agentBatch, task.ID, service.TaskFields{Title: "agent edit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateTask(ctx, newBatch(t, svc, domain.ActorUser), task.ID, service.TaskFields{Title: "user edit"}); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, agentBatch); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	got, err := svc.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "user edit" {
		t.Errorf("refused undo still changed the task: title %q", got.Title)
	}
}

func TestUndoAllowedOnceTheLaterBatchIsUndone(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	task := createTask(t, svc, "original")
	first := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.UpdateTask(ctx, first, task.ID, service.TaskFields{Title: "first"}); err != nil {
		t.Fatal(err)
	}
	second := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.UpdateTask(ctx, second, task.ID, service.TaskFields{Title: "second"}); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, first); err != nil {
		t.Fatalf("undo first after second: %v", err)
	}
	got, _ := svc.GetTask(ctx, task.ID)
	if got.Title != "original" {
		t.Errorf("title = %q, want original", got.Title)
	}
}

func TestUndoneBatchIsClosed(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.CreateTask(ctx, batch, service.TaskFields{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}

	if err := svc.Undo(ctx, batch); !errors.Is(err, service.ErrConflict) {
		t.Errorf("second undo: err = %v, want ErrConflict", err)
	}
	if _, err := svc.CreateTask(ctx, batch, service.TaskFields{Title: "y"}); !errors.Is(err, service.ErrConflict) {
		t.Errorf("write to undone batch: err = %v, want ErrConflict", err)
	}
	if err := svc.Undo(ctx, 999); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown batch: err = %v, want ErrNotFound", err)
	}
}

func TestLastUndoableBatch(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.LastUndoableBatch(ctx, domain.ActorAgent); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("no batches: err = %v, want ErrNotFound", err)
	}

	older := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.CreateTask(ctx, older, service.TaskFields{Title: "a"}); err != nil {
		t.Fatal(err)
	}
	undone := newBatch(t, svc, domain.ActorAgent)
	if _, err := svc.CreateTask(ctx, undone, service.TaskFields{Title: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, undone); err != nil {
		t.Fatal(err)
	}
	newBatch(t, svc, domain.ActorAgent) // empty: nothing to undo
	createTask(t, svc, "user's own change")

	got, err := svc.LastUndoableBatch(ctx, domain.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	if got != older {
		t.Errorf("got batch %d, want %d", got, older)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/service/ -run Undo`
Expected: FAIL to compile, `svc.Undo undefined`

- [ ] **Step 3: Write undo**

Create `internal/service/undo.go`:

```go
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
)

// Undo reverts every change in a batch, newest first. It refuses with ErrConflict when a later
// batch that hasn't been undone touched the same entity, so an undo never silently discards a
// newer edit.
func (s *Service) Undo(ctx context.Context, batch int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		acts, err := batchActivity(ctx, tx, batch)
		if err != nil {
			return err
		}
		for _, a := range acts {
			var later int
			err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM activity a JOIN batches b ON b.id = a.batch_id
				 WHERE a.entity = ? AND a.entity_id = ? AND a.batch_id > ? AND b.undone_at IS NULL`,
				a.Entity, a.EntityID, batch).Scan(&later)
			if err != nil {
				return err
			}
			if later > 0 {
				return fmt.Errorf("%w: %s %d changed after batch %d", ErrConflict, a.Entity, a.EntityID, batch)
			}
		}
		now := s.now().UTC()
		for i := len(acts) - 1; i >= 0; i-- {
			if err := revert(ctx, tx, acts[i], now); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE batches SET undone_at = ? WHERE id = ?`, formatTime(now), batch)
		return err
	})
}

func revert(ctx context.Context, tx *sql.Tx, a domain.Activity, now time.Time) error {
	switch a.Entity {
	case entityTask:
		if a.Before == nil {
			_, err := tx.ExecContext(ctx, `UPDATE tasks SET deleted_at = ? WHERE id = ?`, formatTime(now), a.EntityID)
			return err
		}
		var t domain.Task
		if err := json.Unmarshal(a.Before, &t); err != nil {
			return err
		}
		return writeTask(ctx, tx, t)
	case entityEvent:
		if a.Before == nil {
			_, err := tx.ExecContext(ctx, `UPDATE events SET deleted_at = ? WHERE id = ?`, formatTime(now), a.EntityID)
			return err
		}
		var e domain.Event
		if err := json.Unmarshal(a.Before, &e); err != nil {
			return err
		}
		return writeEvent(ctx, tx, e)
	case entityBlocker:
		if a.Before == nil {
			var b domain.Blocker
			if err := json.Unmarshal(a.After, &b); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM blockers WHERE task_id = ? AND blocked_by_id = ?`, b.TaskID, b.BlockedByID)
			return err
		}
		var b domain.Blocker
		if err := json.Unmarshal(a.Before, &b); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO blockers (task_id, blocked_by_id) VALUES (?, ?)`, b.TaskID, b.BlockedByID)
		return err
	}
	return fmt.Errorf("revert: unknown entity %q", a.Entity)
}

// LastUndoableBatch returns the newest batch by actor that changed something and isn't undone.
func (s *Service) LastUndoableBatch(ctx context.Context, actor domain.Actor) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM batches
		 WHERE actor = ? AND undone_at IS NULL AND EXISTS (SELECT 1 FROM activity WHERE activity.batch_id = batches.id)
		 ORDER BY id DESC LIMIT 1`, actor).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: no undoable %s batch", ErrNotFound, actor)
	}
	return id, err
}
```

A conflict check on a blocker matches on `TaskID`, so a later change to *any* blocker on the same task also blocks the undo. That's deliberately conservative.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/service/`
Expected: PASS. If `TestUndoRevertsAWholeBatch` shows a time diff, a snapshot lost precision or zone: check that every time assigned in `service` goes through `.UTC()`.

- [ ] **Step 5: Commit**

```bash
git add internal/service/undo.go internal/service/undo_test.go
git commit -m "Add batch undo with conflict detection"
```

---

### Task 6: Stream hub and the task API

**Files:**
- Create: `internal/stream/hub.go`
- Create: `internal/httpapi/server.go`
- Create: `internal/httpapi/tasks.go`
- Test: `internal/stream/hub_test.go`, `internal/httpapi/helpers_test.go`, `internal/httpapi/tasks_test.go`

**Interfaces:**
- Consumes: everything `service` produces in Tasks 2 to 5.
- Produces:
  - `stream.Message{Type string; Data any}`, `stream.NewHub() *Hub`, `(*Hub).Subscribe() (<-chan Message, func())`, `(*Hub).Publish(Message)`
  - `httpapi.New(svc *service.Service, hub *stream.Hub, loc *time.Location) http.Handler`
  - `httpapi.Mutation{BatchID int64; Data any}`: the response body of every write.
  - `httpapi.BatchApplied{BatchID int64; Activity []domain.Activity}`: the `batch.applied` SSE payload.
  - Routes (all under `/api/v1`):
    - `GET /tasks` → `[]domain.Task`
    - `POST /tasks` with `service.TaskFields` → 201 `Mutation{Data: domain.Task}`
    - `PUT /tasks/{id}` with `service.TaskFields` → 200 `Mutation{Data: domain.Task}`
    - `POST /tasks/{id}/status` with `{"status": "..."}` → 200 `Mutation{Data: domain.Task}`
    - `DELETE /tasks/{id}` → 200 `Mutation`
    - `GET /blockers` → `[]domain.Blocker`
    - `POST /tasks/{id}/blockers` with `{"blocked_by_id": n}` → 200 `Mutation`
    - `DELETE /tasks/{id}/blockers/{blockedBy}` → 200 `Mutation`
    - `POST /batches/{id}/undo` → 204, publishes `batch.undone` with `{"batch_id": n}`
  - Errors: `{"error": "..."}` with 400 for `ErrInvalid` and bad JSON, 404 for `ErrNotFound`, 409 for `ErrConflict`, 500 otherwise. 403 for a non-loopback Host, 415 for a non-GET request without `Content-Type: application/json`.
  - Package-internal for Task 7 and 8: `(*Server).write`, `writeJSON`, `writeError`, `decode`, `pathID`.

- [ ] **Step 1: Write the failing hub tests**

Create `internal/stream/hub_test.go`:

```go
package stream_test

import (
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/stream"
)

func TestPublishReachesEverySubscriber(t *testing.T) {
	hub := stream.NewHub()
	a, unsubA := hub.Subscribe()
	defer unsubA()
	b, unsubB := hub.Subscribe()
	defer unsubB()

	hub.Publish(stream.Message{Type: "batch.applied", Data: 1})

	for name, ch := range map[string]<-chan stream.Message{"a": a, "b": b} {
		select {
		case m := <-ch:
			if m.Type != "batch.applied" {
				t.Errorf("%s got %q", name, m.Type)
			}
		case <-time.After(time.Second):
			t.Errorf("%s got nothing", name)
		}
	}
}

func TestUnsubscribeClosesAndIsSafeTwice(t *testing.T) {
	hub := stream.NewHub()
	ch, unsub := hub.Subscribe()
	unsub()
	unsub()

	if _, open := <-ch; open {
		t.Error("channel still open after unsubscribe")
	}
	hub.Publish(stream.Message{Type: "after"}) // must not panic on the closed channel
}

func TestSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	hub := stream.NewHub()
	_, unsub := hub.Subscribe() // never read
	defer unsub()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			hub.Publish(stream.Message{Type: "spam"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a subscriber that isn't reading")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/stream/`
Expected: FAIL to compile, `undefined: stream.NewHub`

- [ ] **Step 3: Write the hub**

Create `internal/stream/hub.go`:

```go
// Package stream fans out messages to every connected SSE client.
package stream

import "sync"

type Message struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type Hub struct {
	mu   sync.Mutex
	subs map[chan Message]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[chan Message]struct{}{}}
}

const buffer = 64

// Subscribe returns a channel of messages and a function that ends the subscription and closes
// the channel. The function is safe to call more than once.
func (h *Hub) Subscribe() (<-chan Message, func()) {
	ch := make(chan Message, buffer)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
			close(ch)
		})
	}
}

// Publish sends m to every subscriber. A subscriber whose buffer is full misses the message
// instead of stalling every write; the web client refetches when it reconnects.
func (h *Hub) Publish(m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- m:
		default:
		}
	}
}
```

- [ ] **Step 4: Run the hub tests to verify they pass**

Run: `go test ./internal/stream/`
Expected: PASS

- [ ] **Step 5: Write the API test helpers**

Create `internal/httpapi/helpers_test.go`:

```go
package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

var testLoc = time.FixedZone("UTC-5", -5*3600)

type testAPI struct {
	t       *testing.T
	handler http.Handler
	hub     *stream.Hub
}

func newAPI(t *testing.T) *testAPI {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
	svc := service.New(db, func() time.Time { return now })
	hub := stream.NewHub()
	return &testAPI{t: t, handler: httpapi.New(svc, hub, testLoc), hub: hub}
}

// do sends a request the way the web client will: from a loopback host, with a JSON body type on
// every write.
func (a *testAPI) do(method, path string, body any) *httptest.ResponseRecorder {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = "127.0.0.1:7777" // httptest defaults to example.com, which the guard rejects
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func decodeAs[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
}
```

- [ ] **Step 6: Write the failing API tests**

Create `internal/httpapi/tasks_test.go`:

```go
package httpapi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/httpapi"
)

type taskMutation struct {
	BatchID int64       `json:"batch_id"`
	Data    domain.Task `json:"data"`
}

func TestCaptureCreatesTaskAndPublishes(t *testing.T) {
	api := newAPI(t)
	msgs, unsub := api.hub.Subscribe()
	defer unsub()

	rec := api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"raw_text": "review FLEX1-420"})
	wantStatus(t, rec, http.StatusCreated)
	got := decodeAs[taskMutation](t, rec)
	if got.BatchID == 0 || got.Data.Title != "review FLEX1-420" || got.Data.CreatedBy != domain.ActorUser {
		t.Errorf("unexpected mutation: %+v", got)
	}

	select {
	case m := <-msgs:
		applied, ok := m.Data.(httpapi.BatchApplied)
		if m.Type != "batch.applied" || !ok || applied.BatchID != got.BatchID || len(applied.Activity) != 1 {
			t.Errorf("unexpected message: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no batch.applied published")
	}

	list := decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil))
	if len(list) != 1 {
		t.Errorf("listed %d tasks, want 1", len(list))
	}
}

func TestTaskErrors(t *testing.T) {
	api := newAPI(t)
	cases := []struct {
		name, method, path string
		body               any
		status             int
	}{
		{"empty capture", http.MethodPost, "/api/v1/tasks", map[string]string{"raw_text": "  "}, http.StatusBadRequest},
		{"unknown field", http.MethodPost, "/api/v1/tasks", map[string]string{"titel": "typo"}, http.StatusBadRequest},
		{"non-numeric id", http.MethodPut, "/api/v1/tasks/abc", map[string]string{"title": "x"}, http.StatusBadRequest},
		{"missing task", http.MethodPut, "/api/v1/tasks/999", map[string]string{"title": "x"}, http.StatusNotFound},
		{"bad status", http.MethodPost, "/api/v1/tasks/999/status", map[string]string{"status": "paused"}, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := api.do(c.method, c.path, c.body)
			wantStatus(t, rec, c.status)
			if body := decodeAs[map[string]string](t, rec); body["error"] == "" {
				t.Errorf("error body missing message: %s", rec.Body.String())
			}
		})
	}
}

func TestEditStatusDeleteAndUndo(t *testing.T) {
	api := newAPI(t)
	created := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "draft"}))
	path := fmt.Sprintf("/api/v1/tasks/%d", created.Data.ID)

	edit := decodeAs[taskMutation](t, api.do(http.MethodPut, path, map[string]any{"title": "Draft the RFC", "size": "M"}))
	if edit.Data.Title != "Draft the RFC" {
		t.Fatalf("edit not applied: %+v", edit.Data)
	}
	done := decodeAs[taskMutation](t, api.do(http.MethodPost, path+"/status", map[string]string{"status": "done"}))
	if done.Data.Status != domain.StatusDone {
		t.Fatalf("status = %q", done.Data.Status)
	}

	msgs, unsub := api.hub.Subscribe()
	defer unsub()
	rec := api.do(http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/undo", done.BatchID), nil)
	wantStatus(t, rec, http.StatusNoContent)
	if m := <-msgs; m.Type != "batch.undone" {
		t.Errorf("published %q, want batch.undone", m.Type)
	}

	// The later status batch is undone, so nothing blocks undoing the edit now.
	wantStatus(t, api.do(http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/undo", edit.BatchID), nil), http.StatusNoContent)
	list := decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil))
	if len(list) != 1 || list[0].Title != "draft" || list[0].Status != domain.StatusOpen {
		t.Errorf("after undos: %+v", list)
	}

	wantStatus(t, api.do(http.MethodDelete, path, nil), http.StatusOK)
	if list := decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil)); len(list) != 0 {
		t.Errorf("deleted task still listed")
	}
}

func TestUndoConflictIs409(t *testing.T) {
	api := newAPI(t)
	created := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "a"}))
	path := fmt.Sprintf("/api/v1/tasks/%d", created.Data.ID)
	api.do(http.MethodPut, path, map[string]string{"title": "b"})

	rec := api.do(http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/undo", created.BatchID), nil)
	wantStatus(t, rec, http.StatusConflict)
}

func TestBlockerRoutes(t *testing.T) {
	api := newAPI(t)
	a := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "a"})).Data
	b := decodeAs[taskMutation](t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "b"})).Data

	wantStatus(t, api.do(http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/blockers", a.ID), map[string]int64{"blocked_by_id": b.ID}), http.StatusOK)
	got := decodeAs[[]domain.Blocker](t, api.do(http.MethodGet, "/api/v1/blockers", nil))
	if len(got) != 1 || got[0] != (domain.Blocker{TaskID: a.ID, BlockedByID: b.ID}) {
		t.Fatalf("blockers = %+v", got)
	}

	cycle := api.do(http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/blockers", b.ID), map[string]int64{"blocked_by_id": a.ID})
	wantStatus(t, cycle, http.StatusBadRequest)

	wantStatus(t, api.do(http.MethodDelete, fmt.Sprintf("/api/v1/tasks/%d/blockers/%d", a.ID, b.ID), nil), http.StatusOK)
	if got := decodeAs[[]domain.Blocker](t, api.do(http.MethodGet, "/api/v1/blockers", nil)); len(got) != 0 {
		t.Errorf("blocker still listed: %+v", got)
	}
}

func TestRejectsRequestsFromOtherSites(t *testing.T) {
	api := newAPI(t)
	send := func(host, contentType string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"title":"pwned"}`))
		req.Host = host
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		api.handler.ServeHTTP(rec, req)
		return rec.Code
	}

	cases := []struct {
		name, host, contentType string
		status                  int
	}{
		{"rebound DNS name", "evil.example:7777", "application/json", http.StatusForbidden},
		{"cross-site form post", "127.0.0.1:7777", "text/plain", http.StatusUnsupportedMediaType},
		{"no content type", "127.0.0.1:7777", "", http.StatusUnsupportedMediaType},
		{"localhost", "localhost:7777", "application/json", http.StatusCreated},
		{"ipv6 loopback", "[::1]:7777", "application/json; charset=utf-8", http.StatusCreated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := send(c.host, c.contentType); got != c.status {
				t.Errorf("status = %d, want %d", got, c.status)
			}
		})
	}
	if list := decodeAs[[]domain.Task](t, api.do(http.MethodGet, "/api/v1/tasks", nil)); len(list) != 2 {
		t.Errorf("tasks = %d, want only the 2 legitimate ones", len(list))
	}
}
```

- [ ] **Step 7: Run them to verify they fail**

Run: `go test ./internal/httpapi/`
Expected: FAIL to compile, `undefined: httpapi.New`

- [ ] **Step 8: Write the server core**

Create `internal/httpapi/server.go`:

```go
// Package httpapi serves the REST API under /api/v1/ and the SSE stream.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

type Server struct {
	svc *service.Service
	hub *stream.Hub
	loc *time.Location
}

// Mutation is the response to every write. It carries the batch ID so the UI can offer undo.
type Mutation struct {
	BatchID int64 `json:"batch_id"`
	Data    any   `json:"data,omitempty"`
}

// BatchApplied is the batch.applied SSE payload, sent after every write.
type BatchApplied struct {
	BatchID  int64             `json:"batch_id"`
	Activity []domain.Activity `json:"activity"`
}

// New returns the API handler. loc is the user's time zone, which turns a date into a day of events.
func New(svc *service.Service, hub *stream.Hub, loc *time.Location) http.Handler {
	s := &Server{svc: svc, hub: hub, loc: loc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", s.listTasks)
	mux.HandleFunc("POST /api/v1/tasks", s.createTask)
	mux.HandleFunc("PUT /api/v1/tasks/{id}", s.updateTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/status", s.setTaskStatus)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}", s.deleteTask)
	mux.HandleFunc("GET /api/v1/blockers", s.listBlockers)
	mux.HandleFunc("POST /api/v1/tasks/{id}/blockers", s.addBlocker)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}/blockers/{blockedBy}", s.removeBlocker)
	mux.HandleFunc("POST /api/v1/batches/{id}/undo", s.undo)
	return localOnly(mux)
}

// localOnly rejects requests another site could make through the user's browser. A Host that
// isn't loopback means DNS rebinding. A write that isn't JSON means a cross-site form post, which
// skips the CORS preflight a JSON request triggers and this server never answers.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.Host) {
			writeJSON(w, http.StatusForbidden, errorBody{"requests must come from this machine"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				writeJSON(w, http.StatusUnsupportedMediaType, errorBody{"writes must be application/json"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// write runs op in a new user batch, publishes the batch, and responds with its ID.
func (s *Server) write(w http.ResponseWriter, r *http.Request, status int, op func(batch int64) (any, error)) {
	ctx := r.Context()
	batch, err := s.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := op(batch)
	if err != nil {
		writeError(w, err)
		return
	}
	s.publishBatch(ctx, batch)
	writeJSON(w, status, Mutation{BatchID: batch, Data: data})
}

func (s *Server) publishBatch(ctx context.Context, batch int64) {
	acts, err := s.svc.BatchActivity(ctx, batch)
	if err != nil {
		log.Printf("publish batch %d: %v", batch, err)
		return
	}
	s.hub.Publish(stream.Message{Type: "batch.applied", Data: BatchApplied{BatchID: batch, Activity: acts}})
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, errorBody{err.Error()})
	case errors.Is(err, service.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody{err.Error()})
	case errors.Is(err, service.ErrConflict):
		writeJSON(w, http.StatusConflict, errorBody{err.Error()})
	default:
		log.Printf("internal error: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{"internal error"})
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", service.ErrInvalid, err)
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", service.ErrInvalid, name)
	}
	return id, nil
}
```

- [ ] **Step 9: Write the task handlers**

Create `internal/httpapi/tasks.go`:

```go
package httpapi

import (
	"net/http"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.svc.ListTasks(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var f service.TaskFields
	if err := decode(r, &f); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusCreated, func(batch int64) (any, error) {
		return s.svc.CreateTask(r.Context(), batch, f)
	})
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var f service.TaskFields
	if err := decode(r, &f); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return s.svc.UpdateTask(r.Context(), batch, id, f)
	})
}

func (s *Server) setTaskStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		Status domain.Status `json:"status"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return s.svc.SetTaskStatus(r.Context(), batch, id, body.Status)
	})
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return nil, s.svc.DeleteTask(r.Context(), batch, id)
	})
}

func (s *Server) listBlockers(w http.ResponseWriter, r *http.Request) {
	blockers, err := s.svc.ListBlockers(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, blockers)
}

func (s *Server) addBlocker(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		BlockedByID int64 `json:"blocked_by_id"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return nil, s.svc.AddBlocker(r.Context(), batch, domain.Blocker{TaskID: id, BlockedByID: body.BlockedByID})
	})
}

func (s *Server) removeBlocker(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	blockedBy, err := pathID(r, "blockedBy")
	if err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return nil, s.svc.RemoveBlocker(r.Context(), batch, domain.Blocker{TaskID: id, BlockedByID: blockedBy})
	})
}

func (s *Server) undo(w http.ResponseWriter, r *http.Request) {
	batch, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.svc.Undo(r.Context(), batch); err != nil {
		writeError(w, err)
		return
	}
	s.hub.Publish(stream.Message{Type: "batch.undone", Data: map[string]int64{"batch_id": batch}})
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 10: Run all tests to verify they pass**

Run: `go test ./...`
Expected: PASS. Then `go vet ./...` with no output.

- [ ] **Step 11: Commit**

```bash
git add internal/stream internal/httpapi
git commit -m "Add stream hub and task, blocker, and undo API"
```

---

### Task 7: Event API

**Files:**
- Create: `internal/httpapi/events.go`
- Modify: `internal/httpapi/server.go` (register routes in `New`)
- Test: `internal/httpapi/events_test.go`

**Interfaces:**
- Consumes: `(*Server).write`, `writeJSON`, `writeError`, `decode`, `pathID` from Task 6; the event functions from Task 4.
- Produces routes:
  - `GET /api/v1/events?date=YYYY-MM-DD` → `[]domain.Event` starting that day in the server's time zone. 400 if `date` is missing or malformed.
  - `POST /api/v1/events` with `service.EventFields` → 201 `Mutation{Data: domain.Event}`
  - `PUT /api/v1/events/{id}` → 200 `Mutation{Data: domain.Event}`
  - `DELETE /api/v1/events/{id}` → 200 `Mutation`

- [ ] **Step 1: Write the failing tests**

Create `internal/httpapi/events_test.go`:

```go
package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
)

type eventMutation struct {
	BatchID int64        `json:"batch_id"`
	Data    domain.Event `json:"data"`
}

func TestEventsByLocalDate(t *testing.T) {
	api := newAPI(t)
	create := func(title string, start time.Time) domain.Event {
		t.Helper()
		rec := api.do(http.MethodPost, "/api/v1/events", map[string]any{"title": title, "starts_at": start})
		wantStatus(t, rec, http.StatusCreated)
		return decodeAs[eventMutation](t, rec).Data
	}
	// testLoc is UTC-5, so 23:00 local on the 5th is 04:00 UTC on the 6th.
	create("standup", time.Date(2026, 10, 5, 10, 0, 0, 0, testLoc))
	late := create("late call", time.Date(2026, 10, 5, 23, 0, 0, 0, testLoc))
	create("tomorrow", time.Date(2026, 10, 6, 9, 0, 0, 0, testLoc))

	got := decodeAs[[]domain.Event](t, api.do(http.MethodGet, "/api/v1/events?date=2026-10-05", nil))
	if len(got) != 2 || got[1].ID != late.ID {
		t.Fatalf("events for the 5th = %+v", got)
	}

	path := fmt.Sprintf("/api/v1/events/%d", late.ID)
	moved := api.do(http.MethodPut, path, map[string]any{"title": "late call", "starts_at": time.Date(2026, 10, 6, 8, 0, 0, 0, testLoc)})
	wantStatus(t, moved, http.StatusOK)
	wantStatus(t, api.do(http.MethodDelete, path, nil), http.StatusOK)
	if got := decodeAs[[]domain.Event](t, api.do(http.MethodGet, "/api/v1/events?date=2026-10-06", nil)); len(got) != 1 {
		t.Errorf("events for the 6th = %+v, want only tomorrow", got)
	}
}

func TestEventErrors(t *testing.T) {
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodGet, "/api/v1/events", nil), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodGet, "/api/v1/events?date=today", nil), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/events", map[string]any{"title": "no start"}), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodDelete, "/api/v1/events/999", nil), http.StatusNotFound)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/httpapi/ -run Event`
Expected: FAIL. The routes 404 or 405, so the first `wantStatus` fails.

- [ ] **Step 3: Write the handlers**

Create `internal/httpapi/events.go`:

```go
package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/jameynakama/whatnow/internal/service"
)

// listEvents returns the events that start on ?date= in the user's time zone.
func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	day, err := time.ParseInLocation(time.DateOnly, r.URL.Query().Get("date"), s.loc)
	if err != nil {
		writeError(w, fmt.Errorf("%w: date must be YYYY-MM-DD", service.ErrInvalid))
		return
	}
	events, err := s.svc.ListEvents(r.Context(), day, day.AddDate(0, 0, 1))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) createEvent(w http.ResponseWriter, r *http.Request) {
	var f service.EventFields
	if err := decode(r, &f); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusCreated, func(batch int64) (any, error) {
		return s.svc.CreateEvent(r.Context(), batch, f)
	})
}

func (s *Server) updateEvent(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	var f service.EventFields
	if err := decode(r, &f); err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return s.svc.UpdateEvent(r.Context(), batch, id, f)
	})
}

func (s *Server) deleteEvent(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, err)
		return
	}
	s.write(w, r, http.StatusOK, func(batch int64) (any, error) {
		return nil, s.svc.DeleteEvent(r.Context(), batch, id)
	})
}
```

- [ ] **Step 4: Register the routes**

In `internal/httpapi/server.go`, inside `New`, after the `undo` route:

```go
	mux.HandleFunc("GET /api/v1/events", s.listEvents)
	mux.HandleFunc("POST /api/v1/events", s.createEvent)
	mux.HandleFunc("PUT /api/v1/events/{id}", s.updateEvent)
	mux.HandleFunc("DELETE /api/v1/events/{id}", s.deleteEvent)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi
git commit -m "Add event API"
```

---

### Task 8: SSE endpoint and `whatnow serve`

**Files:**
- Create: `internal/httpapi/stream.go`
- Modify: `internal/httpapi/server.go` (register the stream route)
- Create: `cmd/whatnow/main.go`
- Test: `internal/httpapi/stream_test.go`

**Interfaces:**
- Consumes: `stream.Hub`, `httpapi.New`, `store.Open`, `service.New`.
- Produces:
  - `GET /api/v1/stream`: `text/event-stream`. Sends `: connected` on open, then one SSE event per hub message (`event: <type>` and `data: <json of Data>`).
  - `whatnow serve [-addr 127.0.0.1:7777] [-db PATH]`. The default database is `<os.UserConfigDir()>/whatnow/whatnow.db`.

- [ ] **Step 1: Write the failing SSE test**

Create `internal/httpapi/stream_test.go`:

```go
package httpapi_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamDeliversWrites(t *testing.T) {
	api := newAPI(t)
	srv := httptest.NewServer(api.handler) // listens on 127.0.0.1, so the guard lets it through
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	lines := bufio.NewReader(resp.Body)
	if first, _ := lines.ReadString('\n'); first != ": connected\n" {
		t.Fatalf("first line = %q", first)
	}

	post, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tasks", strings.NewReader(`{"raw_text":"buy oat milk"}`))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("Content-Type", "application/json")
	postResp, err := http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()

	var event, data string
	for event == "" || data == "" {
		line, err := lines.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before the event arrived: %v", err)
		}
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			data = line
		}
	}
	if event != "batch.applied" || !strings.Contains(data, "buy oat milk") {
		t.Errorf("event %q, data %q", event, data)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/httpapi/ -run Stream`
Expected: FAIL, content type is not `text/event-stream` (the route doesn't exist yet).

- [ ] **Step 3: Write the SSE handler and register it**

Create `internal/httpapi/stream.go`:

```go
package httpapi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// stream sends every hub message to the client as a server-sent event until the client leaves.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	msgs, unsubscribe := s.hub.Subscribe()
	defer unsubscribe()

	rc := http.NewResponseController(w)
	fmt.Fprint(w, ": connected\n\n")
	if err := rc.Flush(); err != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-msgs:
			data, err := json.Marshal(m.Data)
			if err != nil {
				log.Printf("stream %s: %v", m.Type, err)
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.Type, data)
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
```

In `internal/httpapi/server.go`, inside `New`, after the event routes:

```go
	mux.HandleFunc("GET /api/v1/stream", s.stream)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Write the CLI entry point**

`main` is wiring only, so it has no unit test. Step 6 checks it by hand. Create `cmd/whatnow/main.go`:

```go
// Command whatnow runs the whatnow server.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: whatnow serve [-addr 127.0.0.1:7777] [-db PATH]")
		os.Exit(2)
	}
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := flags.String("addr", "127.0.0.1:7777", "listen address; keep it on loopback")
	dbPath := flags.String("db", "", "database path (default <user config dir>/whatnow/whatnow.db)")
	flags.Parse(os.Args[2:])

	if err := serve(*addr, *dbPath); err != nil {
		log.Fatal(err)
	}
}

func serve(addr, dbPath string) error {
	if dbPath == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		dbPath = filepath.Join(dir, "whatnow", "whatnow.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	handler := httpapi.New(service.New(db, time.Now), stream.NewHub(), time.Local)
	// No WriteTimeout: it would cut off the long-lived SSE stream.
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("whatnow listening on http://%s (db %s)", addr, dbPath)
	return srv.ListenAndServe()
}
```

- [ ] **Step 6: Smoke-test the binary**

In one terminal:

```bash
just serve -db "$(mktemp -d)/smoke test.db"
```

Expected log: `whatnow listening on http://127.0.0.1:7777 (db .../smoke test.db)`

In a second terminal:

```bash
curl -sN http://127.0.0.1:7777/api/v1/stream &
curl -s -X POST http://127.0.0.1:7777/api/v1/tasks -H 'Content-Type: application/json' -d '{"raw_text":"do FLEX1-666 today"}'
curl -s -X POST http://127.0.0.1:7777/api/v1/tasks -H 'Content-Type: text/plain' -d '{"raw_text":"nope"}'
curl -s http://127.0.0.1:7777/api/v1/tasks
kill %1
```

Expected:
- The stream prints `: connected`, then an `event: batch.applied` whose data contains `do FLEX1-666 today`.
- The first POST returns `{"batch_id":1,"data":{...}}`.
- The `text/plain` POST returns `{"error":"writes must be application/json"}`.
- The list contains exactly one task.

Stop the server with Ctrl-C.

- [ ] **Step 7: Check coverage**

Run: `just cover`
Expected: roughly 80% total. Open `go tool cover -html=coverage.out` and scan the red lines. Pass-through `if err != nil { return err }` branches for driver and encoding failures don't need tests. Any uncovered line that is a *behavior* (a validation rule, a status code mapping, a branch of `revert`) gets a test.

- [ ] **Step 8: Commit**

```bash
git add internal/httpapi cmd
git commit -m "Add SSE stream and whatnow serve"
```
