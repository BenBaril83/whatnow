# Integrations and Google Calendar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pull today's Google Calendar events into whatnow on demand, from the morning (web and TUI) or settings, through an integration contract Jira can reuse.

**Architecture:** `internal/integrations` holds the contract and a `Runner` that keeps each integration's settings (an `integrations` table, secrets in the keychain) and syncs pulled events into the existing `events` table through `service.SyncEvents`, keyed by `source` and `source_key`. `internal/integrations/gcal` reads each calendar's secret iCal address. The API, the web settings and morning, and the TUI morning all go through the runner.

**Tech Stack:** Go, SQLite migrations, `emersion/go-ical`, `teambition/rrule-go`, `zalando/go-keyring`, SvelteKit, Vitest, Playwright, Bubble Tea.

**Spec:** `docs/superpowers/specs/2026-10-12-whatnow-integrations-design.md`

Every code block below was built and tested in a scratch worktree (branch `integrations-scratch`) before this plan was written: `go test ./...`, `go vet`, Vitest, `svelte-check`, and the whole Playwright suite pass on it, and the parser was checked against a real 20 MB Google feed. Delete that branch once this lands.

## Global Constraints

- Libraries: `github.com/emersion/go-ical@v0.0.0-20250609112844-439c63cef608` and `github.com/teambition/rrule-go@v1.8.2`. Not `apognu/gocal`.
- A secret never leaves the server: views say only whether it's set. Error messages and logs never contain a calendar URL, so `gcal` never wraps the HTTP client's error.
- Keychain service `whatnow`; accounts `integration/<id>/<key>` and `integration/<id>/<rowID>/<key>`. Tests call `keyring.MockInit()`.
- Never set `Accept-Encoding` by hand; Go's client asks for gzip and decompresses it only then.
- Integration settings live in the `integrations` table, never the settings document.
- Copy: "Pull today's calendar?", "Removed: <title>", "Skipping <title> from now on", "Google said <code>, check the address", "couldn't reach Google", "Google took too long to answer", "couldn't read the calendar".
- Go test names read as sentences in the repo's style (`TestSyncEventsKeepsARemovalAsATombstone`); Vitest names start with "Should".
- Commits carry no `Co-Authored-By` trailer.

## Review Focus

1. **Undoing a pull that pruned a meeting, after a later pull brought it back:** the undo is refused with a conflict (409), not a database error. Pinned in Task 1 (`TestUndoingAPruneRefusesOnceTheEventWasPulledAgain`).
2. **A pull at noon, after the plan exists:** the events land and open screens refresh from the published batch, but nothing replans until Replan is pressed, the same as a hand-added event. Check Today's strip picks up the new events without a reload.
3. **The TUI and a separate `whatnow serve` pulling at the same moment:** SQLite's busy timeout should serialize them; the worst case is one side's pull failing with an error toast, and a retry working. Not pinned; check by reasoning about `SyncEvents`' transaction.
4. **A keychain that's locked or missing once a calendar is saved:** the Integrations page shows an error toast, and the morning goes on without its Integrations row. Not pinned.
5. **More events today than the TUI card has rows:** the calendar step doesn't scroll the way the triage list does, so a packed day pushes the hint off the card. Not pinned; fine for a handful of meetings, and a candidate for the final review.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/store/migrations/0007_event_source.sql` | `events.source`, `events.source_key`, and their unique index |
| `internal/store/migrations/0008_integrations.sql` | The `integrations` settings table |
| `internal/domain/domain.go` | `Event.Source`, `Event.SourceKey` |
| `internal/service/sync.go` | `SyncEvents`: create, update, tombstones, prune |
| `internal/service/events.go`, `service.go`, `undo.go` | Shared insert and change helpers; undo clears a created row's key and refuses a taken key |
| `internal/integrations/integrations.go` | The contract |
| `internal/integrations/saved.go` | Stored settings and keychain secrets |
| `internal/integrations/runner.go` | Views, saving, enabling, pulling, skipping |
| `internal/integrations/gcal/gcal.go` | Fetching and parsing a Google feed |
| `internal/httpapi/integrations.go`, `server.go`, `settings.go` | Routes, and wiping secrets on delete everything |
| `internal/app/app.go`, `cmd/whatnow/main.go` | Building the runner and handing it to the API |
| `web/src/lib/api.ts`, `integrations.ts` | Types, calls, and form logic |
| `web/src/lib/components/PullResults.svelte` | A pull's events by source, with × and the skip question |
| `web/src/lib/components/MorningIntegrations.svelte` | The morning's Integrations row |
| `web/src/lib/components/IntegrationCard.svelte` | A settings card rendered from declared fields |
| `web/src/routes/settings/+layout.svelte`, `integrations/+page.svelte` | The sidebar and the Integrations page |
| `internal/tui/calendar.go` | The TUI morning's calendar step |

---

### Task 1: Sync pulled events into the events table

**Files:**
- Create: `internal/store/migrations/0007_event_source.sql`
- Modify: `internal/domain/domain.go`
- Modify: `internal/service/service.go`
- Modify: `internal/service/events.go`
- Create: `internal/service/sync.go`
- Modify: `internal/service/undo.go`
- Create: `internal/service/sync_test.go`

**Interfaces:**
- Produces: `domain.Event.Source string` (`json:"source"`) and `SourceKey string` (`json:"source_key"`), empty for events made in whatnow.
- Produces: `service.PulledEvent{Key, Title string; StartsAt time.Time; EndsAt *time.Time}`.
- Produces: `func (s *Service) SyncEvents(ctx context.Context, batch int64, source string, from, to time.Time, pulled []PulledEvent, prune bool) (map[string]domain.Event, error)`, returning the live pulled events by key.
- Produces (unexported, same package): `insertEvent(ctx, tx, batch, *domain.Event) error` and `writeChange(ctx, tx, batch, before domain.Event, action string, change func(*domain.Event)) (domain.Event, error)`, which `CreateEvent` and `changeEvent` now call.
- Changes: undoing an event's creation clears its `source_key`; undoing a prune whose key a newer row holds returns `ErrConflict`.

- [ ] **Step 1: Write the failing tests**

The tests sync into a fixed day, `2026-10-05` in UTC, as source `gcal`.

`internal/service/sync_test.go` (new):

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

var syncDay = time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)

func pulled(key, title string, hour int) service.PulledEvent {
	end := syncDay.Add(time.Duration(hour)*time.Hour + 30*time.Minute)
	return service.PulledEvent{Key: key, Title: title, StartsAt: syncDay.Add(time.Duration(hour) * time.Hour), EndsAt: &end}
}

// sync pulls events into today as gcal, returning the live events by key.
func sync(t *testing.T, svc *service.Service, prune bool, events ...service.PulledEvent) map[string]domain.Event {
	t.Helper()
	live, err := svc.SyncEvents(context.Background(), newBatch(t, svc, domain.ActorUser), "gcal",
		syncDay, syncDay.AddDate(0, 0, 1), events, prune)
	if err != nil {
		t.Fatal(err)
	}
	return live
}

// titles lists today's live events as "title@hour".
func titles(t *testing.T, svc *service.Service) []string {
	t.Helper()
	events, err := svc.ListEvents(context.Background(), syncDay, syncDay.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Title+"@"+e.StartsAt.Format("15"))
	}
	return out
}

func TestSyncEventsCreatesThenUpdatesInPlace(t *testing.T) {
	svc, _ := newService(t)
	first := sync(t, svc, true, pulled("a", "Standup", 9), pulled("b", "1:1", 13))
	if first["a"].Source != "gcal" || first["a"].SourceKey != "a" {
		t.Errorf("created event = %+v", first["a"])
	}

	// The 1:1 moved and was renamed: same key, so the same row.
	second := sync(t, svc, true, pulled("a", "Standup", 9), pulled("b", "1:1 with Sam", 15))
	if second["b"].ID != first["b"].ID {
		t.Errorf("moved event got a new row: %d, was %d", second["b"].ID, first["b"].ID)
	}
	if diff := cmp.Diff([]string{"Standup@09", "1:1 with Sam@15"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestSyncEventsWritesNothingWhenNothingChanged(t *testing.T) {
	svc, _ := newService(t)
	sync(t, svc, true, pulled("a", "Standup", 9))
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SyncEvents(context.Background(), batch, "gcal", syncDay, syncDay.AddDate(0, 0, 1),
		[]service.PulledEvent{pulled("a", "Standup", 9)}, true); err != nil {
		t.Fatal(err)
	}
	if acts, _ := svc.BatchActivity(context.Background(), batch); len(acts) != 0 {
		t.Errorf("activity = %+v, want none", acts)
	}
}

func TestSyncEventsKeepsARemovalAsATombstone(t *testing.T) {
	svc, _ := newService(t)
	live := sync(t, svc, true, pulled("a", "Blockout", 15), pulled("b", "Standup", 9))
	if err := svc.DeleteEvent(context.Background(), newBatch(t, svc, domain.ActorUser), live["a"].ID); err != nil {
		t.Fatal(err)
	}

	again := sync(t, svc, true, pulled("a", "Blockout", 15), pulled("b", "Standup", 9))
	if _, ok := again["a"]; ok {
		t.Error("a removed event came back as live")
	}
	if diff := cmp.Diff([]string{"Standup@09"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestSyncEventsPrunesWhatTheFeedDropped(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	sync(t, svc, true, pulled("a", "Standup", 9), pulled("b", "Canceled", 13))
	mine, err := svc.CreateEvent(ctx, newBatch(t, svc, domain.ActorUser), service.EventFields{Title: "Mine", StartsAt: syncDay.Add(11 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	sync(t, svc, true, pulled("a", "Standup", 9))
	if diff := cmp.Diff([]string{"Standup@09", "Mine@11"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}

	// The pruned row isn't a tombstone: if the meeting comes back, so does the event.
	sync(t, svc, true, pulled("a", "Standup", 9), pulled("b", "Canceled", 13))
	if diff := cmp.Diff([]string{"Standup@09", "Mine@11", "Canceled@13"}, titles(t, svc)); diff != "" {
		t.Errorf("after it came back (-want +got):\n%s", diff)
	}
	if got, err := svc.GetEvent(ctx, mine.ID); err != nil || got.Source != "" {
		t.Errorf("own event = %+v, %v", got, err)
	}
}

func TestSyncEventsWithoutPruneDeletesNothing(t *testing.T) {
	svc, _ := newService(t)
	sync(t, svc, true, pulled("a", "Standup", 9), pulled("b", "1:1", 13))
	sync(t, svc, false, pulled("a", "Standup", 9))
	if diff := cmp.Diff([]string{"Standup@09", "1:1@13"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestSyncEventsOnlyPrunesItsSourceAndWindow(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	tomorrow := pulled("t", "Tomorrow", 9)
	tomorrow.StartsAt, tomorrow.EndsAt = tomorrow.StartsAt.AddDate(0, 0, 1), nil
	if _, err := svc.SyncEvents(ctx, newBatch(t, svc, domain.ActorUser), "gcal", syncDay.AddDate(0, 0, 1), syncDay.AddDate(0, 0, 2),
		[]service.PulledEvent{tomorrow}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SyncEvents(ctx, newBatch(t, svc, domain.ActorUser), "other", syncDay, syncDay.AddDate(0, 0, 1),
		[]service.PulledEvent{pulled("o", "Other", 10)}, true); err != nil {
		t.Fatal(err)
	}

	sync(t, svc, true)

	if diff := cmp.Diff([]string{"Other@10"}, titles(t, svc)); diff != "" {
		t.Errorf("today (-want +got):\n%s", diff)
	}
	next, err := svc.ListEvents(ctx, syncDay.AddDate(0, 0, 1), syncDay.AddDate(0, 0, 2))
	if err != nil || len(next) != 1 {
		t.Errorf("tomorrow = %+v, %v", next, err)
	}
}

func TestUndoingAPullLetsTheNextPullBringItBack(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SyncEvents(ctx, batch, "gcal", syncDay, syncDay.AddDate(0, 0, 1),
		[]service.PulledEvent{pulled("a", "Standup", 9)}, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, svc); len(got) != 0 {
		t.Fatalf("after undo = %v", got)
	}

	sync(t, svc, true, pulled("a", "Standup", 9))
	if diff := cmp.Diff([]string{"Standup@09"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestUndoingAPruneRestoresTheEventAndItsKey(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	sync(t, svc, true, pulled("a", "Standup", 9))
	batch := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SyncEvents(ctx, batch, "gcal", syncDay, syncDay.AddDate(0, 0, 1), nil, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}

	live := sync(t, svc, true, pulled("a", "Standup", 9))
	if diff := cmp.Diff([]string{"Standup@09"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if live["a"].SourceKey != "a" {
		t.Errorf("restored event = %+v", live["a"])
	}
}

func TestSyncEventsRejectsAnInvalidEvent(t *testing.T) {
	svc, _ := newService(t)
	bad := pulled("a", "  ", 9)
	if _, err := svc.SyncEvents(context.Background(), newBatch(t, svc, domain.ActorUser), "gcal", syncDay, syncDay.AddDate(0, 0, 1),
		[]service.PulledEvent{bad}, true); err == nil {
		t.Error("a blank title was accepted")
	}
}

func TestUndoingAPruneRefusesOnceTheEventWasPulledAgain(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	sync(t, svc, true, pulled("a", "Standup", 9))
	prune := newBatch(t, svc, domain.ActorUser)
	if _, err := svc.SyncEvents(ctx, prune, "gcal", syncDay, syncDay.AddDate(0, 0, 1), nil, true); err != nil {
		t.Fatal(err)
	}
	sync(t, svc, true, pulled("a", "Standup", 9))

	if err := svc.Undo(ctx, prune); !errors.Is(err, service.ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
	if diff := cmp.Diff([]string{"Standup@09"}, titles(t, svc)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/service/ -run 'Sync|Undoing'`
Expected: FAIL to compile: `service.PulledEvent` and `SyncEvents` are undefined.

- [ ] **Step 3: Implement**

The migration adds the columns and their unique index. `insertEvent` and `writeChange` are pulled out of `CreateEvent` and `changeEvent` so `SyncEvents` can create and change events inside its own transaction. `domain.Event`'s new fields go after `DeletedAt`, so gofmt leaves the existing alignment alone.

`internal/store/migrations/0007_event_source.sql` (new):

```sql
-- An event pulled from an integration names it, and its identity there. A deleted row that keeps
-- its key is the tombstone that stops the next pull bringing the event back.
ALTER TABLE events ADD COLUMN source TEXT;
ALTER TABLE events ADD COLUMN source_key TEXT;
CREATE UNIQUE INDEX events_source_key ON events(source, source_key);
```

`internal/domain/domain.go`, apply:

```diff
@@ -94,6 +94,10 @@ type Event struct {
 	CreatedAt time.Time  `json:"created_at"`
 	UpdatedAt time.Time  `json:"updated_at"`
 	DeletedAt *time.Time `json:"deleted_at"`
+	// Source is the integration the event was pulled from, and SourceKey its identity there. Both
+	// are empty for an event made in whatnow.
+	Source    string `json:"source"`
+	SourceKey string `json:"source_key"`
 }
 
 // Activity is one recorded change. Before is null for a creation and After is null for a removal.
```

`internal/service/service.go`, apply:

```diff
@@ -181,6 +181,9 @@ func parseNullTime(n sql.NullString) (*time.Time, error) {
 	return &t, nil
 }
 
+// nullString stores an empty string as NULL.
+func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
+
 func toNull[T ~string](p *T) sql.NullString {
 	if p == nil {
 		return sql.NullString{}
```

`internal/service/events.go`, apply:

```diff
@@ -52,23 +52,31 @@ func (s *Service) CreateEvent(ctx context.Context, batch int64, f EventFields) (
 		if _, err := batchActor(ctx, tx, batch); err != nil {
 			return err
 		}
-		now := s.now().UTC()
-		e = domain.Event{RawText: f.RawText, CreatedAt: now, UpdatedAt: now}
+		e = domain.Event{RawText: f.RawText}
 		f.applyTo(&e)
-		res, err := tx.ExecContext(ctx,
-			`INSERT INTO events (title, starts_at, ends_at, raw_text, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
-			e.Title, formatTime(e.StartsAt), formatNullTime(e.EndsAt), e.RawText, formatTime(e.CreatedAt), formatTime(e.UpdatedAt))
-		if err != nil {
-			return err
-		}
-		if e.ID, err = res.LastInsertId(); err != nil {
-			return err
-		}
-		return record(ctx, tx, batch, actionCreate, entityEvent, e.ID, nil, e)
+		return s.insertEvent(ctx, tx, batch, &e)
 	})
 	return e, err
 }
 
+// insertEvent adds e and records its creation, setting its ID and timestamps.
+func (s *Service) insertEvent(ctx context.Context, tx *sql.Tx, batch int64, e *domain.Event) error {
+	now := s.now().UTC()
+	e.CreatedAt, e.UpdatedAt = now, now
+	res, err := tx.ExecContext(ctx,
+		`INSERT INTO events (title, starts_at, ends_at, raw_text, source, source_key, created_at, updated_at)
+		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
+		e.Title, formatTime(e.StartsAt), formatNullTime(e.EndsAt), e.RawText, nullString(e.Source),
+		nullString(e.SourceKey), formatTime(e.CreatedAt), formatTime(e.UpdatedAt))
+	if err != nil {
+		return err
+	}
+	if e.ID, err = res.LastInsertId(); err != nil {
+		return err
+	}
+	return record(ctx, tx, batch, actionCreate, entityEvent, e.ID, nil, *e)
+}
+
 func (s *Service) UpdateEvent(ctx context.Context, batch, id int64, f EventFields) (domain.Event, error) {
 	f.RawText = ""
 	if err := f.normalize(); err != nil {
@@ -95,17 +103,23 @@ func (s *Service) changeEvent(ctx context.Context, batch, id int64, action strin
 		if err != nil {
 			return err
 		}
-		e = before
-		change(&e)
-		e.UpdatedAt = s.now().UTC()
-		if err := writeEvent(ctx, tx, e); err != nil {
-			return err
-		}
-		return record(ctx, tx, batch, action, entityEvent, id, before, e)
+		e, err = s.writeChange(ctx, tx, batch, before, action, change)
+		return err
 	})
 	return e, err
 }
 
+// writeChange applies change to a copy of before, saves it, and records it.
+func (s *Service) writeChange(ctx context.Context, tx *sql.Tx, batch int64, before domain.Event, action string, change func(*domain.Event)) (domain.Event, error) {
+	e := before
+	change(&e)
+	e.UpdatedAt = s.now().UTC()
+	if err := writeEvent(ctx, tx, e); err != nil {
+		return e, err
+	}
+	return e, record(ctx, tx, batch, action, entityEvent, e.ID, before, e)
+}
+
 func (s *Service) GetEvent(ctx context.Context, id int64) (domain.Event, error) {
 	return liveEvent(ctx, s.db, id)
 }
@@ -139,15 +153,16 @@ func liveEvent(ctx context.Context, q querier, id int64) (domain.Event, error) {
 	return e, err
 }
 
-const eventColumns = `id, title, starts_at, ends_at, raw_text, created_at, updated_at, deleted_at`
+const eventColumns = `id, title, starts_at, ends_at, raw_text, source, source_key, created_at, updated_at, deleted_at`
 
 func scanEvent(row scanner) (domain.Event, error) {
 	var e domain.Event
 	var startsAt, createdAt, updatedAt string
-	var endsAt, deletedAt sql.NullString
-	if err := row.Scan(&e.ID, &e.Title, &startsAt, &endsAt, &e.RawText, &createdAt, &updatedAt, &deletedAt); err != nil {
+	var endsAt, source, sourceKey, deletedAt sql.NullString
+	if err := row.Scan(&e.ID, &e.Title, &startsAt, &endsAt, &e.RawText, &source, &sourceKey, &createdAt, &updatedAt, &deletedAt); err != nil {
 		return e, err
 	}
+	e.Source, e.SourceKey = source.String, sourceKey.String
 	var err error
 	if e.StartsAt, err = parseTime(startsAt); err != nil {
 		return e, err
@@ -168,9 +183,9 @@ func scanEvent(row scanner) (domain.Event, error) {
 // writeEvent overwrites every column of an existing event. Undo uses it to restore a snapshot.
 func writeEvent(ctx context.Context, tx *sql.Tx, e domain.Event) error {
 	_, err := tx.ExecContext(ctx,
-		`UPDATE events SET title = ?, starts_at = ?, ends_at = ?, raw_text = ?, created_at = ?, updated_at = ?,
-		 deleted_at = ? WHERE id = ?`,
-		e.Title, formatTime(e.StartsAt), formatNullTime(e.EndsAt), e.RawText, formatTime(e.CreatedAt),
-		formatTime(e.UpdatedAt), formatNullTime(e.DeletedAt), e.ID)
+		`UPDATE events SET title = ?, starts_at = ?, ends_at = ?, raw_text = ?, source = ?, source_key = ?,
+		 created_at = ?, updated_at = ?, deleted_at = ? WHERE id = ?`,
+		e.Title, formatTime(e.StartsAt), formatNullTime(e.EndsAt), e.RawText, nullString(e.Source),
+		nullString(e.SourceKey), formatTime(e.CreatedAt), formatTime(e.UpdatedAt), formatNullTime(e.DeletedAt), e.ID)
 	return err
 }
```

`internal/service/sync.go` (new):

```go
package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
)

// PulledEvent is one event from an integration's feed. Key is its identity in the feed, which
// stays the same when the event moves.
type PulledEvent struct {
	Key      string
	Title    string
	StartsAt time.Time
	EndsAt   *time.Time
}

// SyncEvents makes source's events that start in [from, to) match pulled, in one batch:
//   - a pulled event with no row is created;
//   - one with a live row is updated to the feed's title and times, over any hand edit;
//   - one whose row was deleted stays deleted, since that row is the tombstone of a removal.
//
// With prune, a live row the feed no longer has is deleted, and its key cleared so it's no
// tombstone. Pass prune false when part of the feed couldn't be fetched. It returns the live
// pulled events by key.
func (s *Service) SyncEvents(ctx context.Context, batch int64, source string, from, to time.Time, pulled []PulledEvent, prune bool) (map[string]domain.Event, error) {
	live := map[string]domain.Event{}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := batchActor(ctx, tx, batch); err != nil {
			return err
		}
		for _, p := range pulled {
			f := EventFields{Title: p.Title, StartsAt: p.StartsAt, EndsAt: p.EndsAt}
			if err := f.normalize(); err != nil {
				return err
			}
			row, err := scanEvent(tx.QueryRowContext(ctx,
				`SELECT `+eventColumns+` FROM events WHERE source = ? AND source_key = ?`, source, p.Key))
			switch {
			case errors.Is(err, sql.ErrNoRows):
				e := domain.Event{Source: source, SourceKey: p.Key}
				f.applyTo(&e)
				if err := s.insertEvent(ctx, tx, batch, &e); err != nil {
					return err
				}
				live[p.Key] = e
			case err != nil:
				return err
			case row.DeletedAt != nil:
			case sameTimes(row, f):
				live[p.Key] = row
			default:
				e, err := s.writeChange(ctx, tx, batch, row, actionUpdate, f.applyTo)
				if err != nil {
					return err
				}
				live[p.Key] = e
			}
		}
		if !prune {
			return nil
		}
		gone, err := sourceEvents(ctx, tx, source, from, to)
		if err != nil {
			return err
		}
		for _, e := range gone {
			if _, ok := live[e.SourceKey]; ok {
				continue
			}
			_, err := s.writeChange(ctx, tx, batch, e, actionDelete, func(e *domain.Event) {
				deleted := s.now().UTC()
				e.DeletedAt, e.SourceKey = &deleted, ""
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return live, err
}

// sameTimes says whether e already has f's title and times.
func sameTimes(e domain.Event, f EventFields) bool {
	if e.Title != f.Title || !e.StartsAt.Equal(f.StartsAt) || (e.EndsAt == nil) != (f.EndsAt == nil) {
		return false
	}
	return e.EndsAt == nil || e.EndsAt.Equal(*f.EndsAt)
}

// sourceEvents returns source's live events that start in [from, to).
func sourceEvents(ctx context.Context, tx *sql.Tx, source string, from, to time.Time) ([]domain.Event, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM events
		 WHERE source = ? AND deleted_at IS NULL AND starts_at >= ? AND starts_at < ?`,
		source, formatTime(from), formatTime(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []domain.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
```

`internal/service/undo.go`, apply:

```diff
@@ -61,14 +61,28 @@ func revert(ctx context.Context, tx *sql.Tx, a domain.Activity, now time.Time) e
 		}
 		return writeTask(ctx, tx, t)
 	case entityEvent:
+		// A pulled event's key goes too, so the deleted row isn't a tombstone and the next pull
+		// brings the event back.
 		if a.Before == nil {
-			_, err := tx.ExecContext(ctx, `UPDATE events SET deleted_at = ? WHERE id = ?`, formatTime(now), a.EntityID)
+			_, err := tx.ExecContext(ctx, `UPDATE events SET deleted_at = ?, source_key = NULL WHERE id = ?`, formatTime(now), a.EntityID)
 			return err
 		}
 		var e domain.Event
 		if err := json.Unmarshal(a.Before, &e); err != nil {
 			return err
 		}
+		// A pruned event's key may belong to a row a later pull made for the same meeting.
+		if e.SourceKey != "" {
+			var taken int
+			err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE source = ? AND source_key = ? AND id != ?`,
+				e.Source, e.SourceKey, e.ID).Scan(&taken)
+			if err != nil {
+				return err
+			}
+			if taken > 0 {
+				return fmt.Errorf("%w: %s was pulled again since", ErrConflict, e.Title)
+			}
+		}
 		return writeEvent(ctx, tx, e)
 	case entityDay:
 		var d domain.Day
```

- [ ] **Step 4: Run the checks**

Run: `go test ./internal/service/ ./internal/store/ && go vet ./...`
Expected: PASS. Then break the undo change by hand (drop `, source_key = NULL`) and check `TestUndoingAPullLetsTheNextPullBringItBack` fails, and put it back.

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations/0007_event_source.sql internal/domain/domain.go internal/service/service.go internal/service/events.go internal/service/sync.go internal/service/undo.go internal/service/sync_test.go
git commit -m "Sync pulled events into the events table"
```

---

### Task 2: The integration contract and Google Calendar

**Files:**
- Create: `internal/integrations/integrations.go`
- Create: `internal/integrations/gcal/gcal.go`
- Create: `internal/integrations/gcal/gcal_test.go`

**Interfaces:**
- Produces, in `internal/integrations`: `type Integration interface { ID() string; Name() string; Fields() []Field; Pull(ctx context.Context, cfg Config, from, to time.Time) []Source }`; `Kind` with `Text`, `Secret`, `List`; `Field{Key, Label string; Kind Kind; Help string; Required bool; Fields []Field}` with lowercase JSON tags; `Config{Values map[string]string; Lists map[string][]Row}`; `Row{ID string; Values map[string]string}`; `Source{Label string; Events []Event; Err error}`; `Event{Key, Series, Title string; StartsAt time.Time; EndsAt *time.Time}`.
- Produces, in `internal/integrations/gcal`: `func New(client *http.Client) *Calendar` (nil gives a 30-second client), ID `gcal`, one list field `calendars` with columns `label` (text) and `url` (secret).
- Keys: a one-off's key is its UID; an occurrence's is `UID|<original start, UTC, 20060102T150405Z>` with `Series` set to the UID.

- [ ] **Step 1: Add the iCal and recurrence libraries**

Run: `go get github.com/emersion/go-ical@v0.0.0-20250609112844-439c63cef608 github.com/teambition/rrule-go@v1.8.2`
Expected: both added to `go.mod`. Don't use `apognu/gocal`; the spec says why.

- [ ] **Step 2: Write the failing tests**

`parse` is tested directly against small feeds written the way Google writes them; `Pull` is tested over `httptest`. The window is a day in America/Chicago.

`internal/integrations/gcal/gcal_test.go` (new):

```go
package gcal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/integrations"
)

var chicago = mustZone("America/Chicago")

func mustZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// feed wraps events in a calendar the way Google writes one, owned by me@example.com.
func feed(events ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Google Inc//Google Calendar 70.9054//EN\r\n" +
		"X-WR-CALNAME:me@example.com\r\nX-WR-TIMEZONE:America/Chicago\r\n" +
		strings.Join(events, "") + "END:VCALENDAR\r\n"
}

// vevent is one VEVENT from its property lines, with the DTSTAMP every event needs.
func vevent(lines ...string) string {
	return "BEGIN:VEVENT\r\nDTSTAMP:20261001T000000Z\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

// day returns the window for a date in Chicago.
func day(date string) (time.Time, time.Time) {
	from, err := time.ParseInLocation(time.DateOnly, date, chicago)
	if err != nil {
		panic(err)
	}
	return from, from.AddDate(0, 0, 1)
}

// summary lists events as "title key series start-end" in Chicago time, for compact diffs.
func summary(events []integrations.Event) []string {
	var out []string
	for _, e := range events {
		s := e.Title + " " + e.Key + " " + e.Series + " " + e.StartsAt.In(chicago).Format("15:04")
		if e.EndsAt != nil {
			s += "-" + e.EndsAt.In(chicago).Format("15:04")
		}
		out = append(out, s)
	}
	return out
}

func mustParse(t *testing.T, body, date string) []string {
	t.Helper()
	from, to := day(date)
	events, err := parse([]byte(body), from, to)
	if err != nil {
		t.Fatal(err)
	}
	return summary(events)
}

func TestParseKeepsTimedEventsThatStartInTheWindow(t *testing.T) {
	body := feed(
		vevent("UID:one", "SUMMARY:Dentist", "DTSTART;TZID=America/Chicago:20261005T090000", "DTEND;TZID=America/Chicago:20261005T100000"),
		vevent("UID:utc", "SUMMARY:Call", "DTSTART:20261005T190000Z", "DURATION:PT45M"),
		vevent("UID:midnight", "SUMMARY:Early", "DTSTART;TZID=America/Chicago:20261005T000000"),
		vevent("UID:allday", "SUMMARY:OOO", "DTSTART;VALUE=DATE:20261005", "DTEND;VALUE=DATE:20261006"),
		vevent("UID:tomorrow", "SUMMARY:Next", "DTSTART;TZID=America/Chicago:20261006T000000"),
		vevent("UID:yesterday", "SUMMARY:Late", "DTSTART;TZID=America/Chicago:20261004T233000", "DTEND;TZID=America/Chicago:20261005T003000"),
		vevent("UID:canceled", "SUMMARY:Off", "STATUS:CANCELLED", "DTSTART;TZID=America/Chicago:20261005T110000"),
		vevent("UID:untitled", "DTSTART;TZID=America/Chicago:20261005T120000"),
	)
	want := []string{
		"Early midnight  00:00",
		"Dentist one  09:00-10:00",
		"(no title) untitled  12:00",
		"Call utc  14:00-14:45",
	}
	if diff := cmp.Diff(want, mustParse(t, body, "2026-10-05")); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestParseDropsWhatTheOwnerDeclined(t *testing.T) {
	attend := func(uid, status string) string {
		return vevent("UID:"+uid, "SUMMARY:"+uid, "DTSTART:20261005T150000Z",
			"ATTENDEE;CN=Someone;PARTSTAT=DECLINED:mailto:someone@example.com",
			"ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT="+status+";CN=Me;X-NUM-GUESTS=0:mai\r\n lto:Me@Example.com")
	}
	body := feed(attend("declined", "DECLINED"), attend("tentative", "TENTATIVE"), attend("unanswered", "NEEDS-ACTION"),
		vevent("UID:mine", "SUMMARY:mine", "DTSTART:20261005T160000Z"))
	got := mustParse(t, body, "2026-10-05")
	want := []string{"tentative tentative  10:00", "unanswered unanswered  10:00", "mine mine  11:00"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}

	// A calendar not named after an email has no owner to match, so nothing counts as declined.
	shared := strings.Replace(body, "X-WR-CALNAME:me@example.com", "X-WR-CALNAME:Family", 1)
	if got := mustParse(t, shared, "2026-10-05"); len(got) != 4 {
		t.Errorf("shared calendar = %v, want all four", got)
	}
}

func TestParseExpandsASeriesAroundItsExceptions(t *testing.T) {
	series := vevent("UID:standup", "SUMMARY:Standup", "RRULE:FREQ=DAILY",
		"DTSTART;TZID=America/Chicago:20260901T093000", "DTEND;TZID=America/Chicago:20260901T094500",
		"EXDATE;TZID=America/Chicago:20261006T093000,20261007T093000")
	moved := vevent("UID:standup", "SUMMARY:Standup (moved)",
		"RECURRENCE-ID;TZID=America/Chicago:20261008T093000",
		"DTSTART;TZID=America/Chicago:20261009T140000", "DTEND;TZID=America/Chicago:20261009T141500")
	canceled := vevent("UID:standup", "SUMMARY:Standup", "STATUS:CANCELLED",
		"RECURRENCE-ID;TZID=America/Chicago:20261010T093000", "DTSTART;TZID=America/Chicago:20261010T093000")
	body := feed(series, moved, canceled)

	cases := map[string][]string{
		"2026-10-05": {"Standup standup|20261005T143000Z standup 09:30-09:45"},
		// Both dates on one EXDATE line are skipped.
		"2026-10-06": nil,
		"2026-10-07": nil,
		// Moved off the 8th, so it's gone from the 8th...
		"2026-10-08": nil,
		// ...and on the 9th twice: the usual occurrence, and the moved one under its original key.
		"2026-10-09": {
			"Standup standup|20261009T143000Z standup 09:30-09:45",
			"Standup (moved) standup|20261008T143000Z standup 14:00-14:15",
		},
		"2026-10-10": nil,
	}
	for date, want := range cases {
		if diff := cmp.Diff(want, mustParse(t, body, date)); diff != "" {
			t.Errorf("%s (-want +got):\n%s", date, diff)
		}
	}
}

func TestParseKeepsWallTimeAcrossDST(t *testing.T) {
	// Chicago leaves daylight time on November 1st, 2026.
	body := feed(vevent("UID:weekly", "SUMMARY:Weekly", "RRULE:FREQ=WEEKLY;BYDAY=MO",
		"DTSTART;TZID=America/Chicago:20261026T090000", "DTEND;TZID=America/Chicago:20261026T100000"))
	if diff := cmp.Diff([]string{"Weekly weekly|20261102T150000Z weekly 09:00-10:00"}, mustParse(t, body, "2026-11-02")); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestParseFollowsMonthlyByDayRules(t *testing.T) {
	// The second Tuesday of October 2026 is the 13th.
	body := feed(vevent("UID:monthly", "SUMMARY:Review", "RRULE:FREQ=MONTHLY;BYDAY=2TU",
		"DTSTART;TZID=America/Chicago:20260908T130000"))
	if got := mustParse(t, body, "2026-10-06"); len(got) != 0 {
		t.Errorf("first Tuesday = %v", got)
	}
	if diff := cmp.Diff([]string{"Review monthly|20261013T180000Z monthly 13:00"}, mustParse(t, body, "2026-10-13")); diff != "" {
		t.Errorf("second Tuesday (-want +got):\n%s", diff)
	}
}

func TestParseSkipsOneBadEvent(t *testing.T) {
	body := feed(
		vevent("UID:bad", "SUMMARY:Bad", "DTSTART;TZID=Not/AZone:20261005T090000"),
		vevent("UID:good", "SUMMARY:Good", "DTSTART:20261005T150000Z"),
	)
	if diff := cmp.Diff([]string{"Good good  10:00"}, mustParse(t, body, "2026-10-05")); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestParseRejectsWhatIsntACalendar(t *testing.T) {
	from, to := day("2026-10-05")
	if _, err := parse([]byte("<html>Sign in</html>"), from, to); err == nil {
		t.Error("a web page parsed as a calendar")
	}
}

func TestPullReportsEachCalendarWithoutItsURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/work.ics":
			w.Write([]byte(feed(vevent("UID:one", "SUMMARY:Standup", "DTSTART:20261005T143000Z"))))
		case "/junk.ics":
			w.Write([]byte("not a calendar"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := integrations.Config{Lists: map[string][]integrations.Row{"calendars": {
		{ID: "1", Values: map[string]string{"label": "Work", "url": srv.URL + "/work.ics"}},
		{ID: "2", Values: map[string]string{"label": "Personal", "url": srv.URL + "/secret-token.ics"}},
		{ID: "3", Values: map[string]string{"label": "Junk", "url": srv.URL + "/junk.ics"}},
		{ID: "4", Values: map[string]string{"label": "Gone", "url": "http://127.0.0.1:1/secret-token.ics"}},
	}}}

	from, to := day("2026-10-05")
	sources := New(nil).Pull(context.Background(), cfg, from, to)

	if len(sources) != 4 {
		t.Fatalf("sources = %+v", sources)
	}
	if s := sources[0]; s.Label != "Work" || s.Err != nil || len(s.Events) != 1 {
		t.Errorf("work = %+v", s)
	}
	wantErrs := map[string]string{
		"Personal": "Google said 404, check the address",
		"Junk":     "couldn't read the calendar",
		"Gone":     "couldn't reach Google",
	}
	for _, s := range sources[1:] {
		if s.Err == nil || s.Err.Error() != wantErrs[s.Label] {
			t.Errorf("%s: err = %v, want %q", s.Label, s.Err, wantErrs[s.Label])
		}
		if s.Err != nil && strings.Contains(s.Err.Error(), "secret-token") {
			t.Errorf("%s: error names the URL: %v", s.Label, s.Err)
		}
	}
}

func TestPullGivesUpOnASlowCalendar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	cfg := integrations.Config{Lists: map[string][]integrations.Row{"calendars": {
		{ID: "1", Values: map[string]string{"label": "Slow", "url": srv.URL}},
	}}}
	from, to := day("2026-10-05")
	sources := New(&http.Client{Timeout: 50 * time.Millisecond}).Pull(context.Background(), cfg, from, to)
	if err := sources[0].Err; err == nil || err.Error() != "Google took too long to answer" {
		t.Errorf("err = %v", err)
	}
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/integrations/...`
Expected: FAIL to compile: `parse`, `New`, and the contract types are undefined.

- [ ] **Step 4: Implement**

`internal/integrations/integrations.go` (new):

```go
// Package integrations pulls outside work into whatnow. Each integration declares the settings
// fields it needs and pulls a window of time; Runner keeps their settings and syncs what they pull.
package integrations

import (
	"context"
	"time"
)

type Integration interface {
	// ID names the integration in URLs, storage, and the events it pulls, such as "gcal".
	ID() string
	Name() string
	Fields() []Field
	// Pull fetches what starts in [from, to), one Source for each part it pulls, such as a calendar.
	Pull(ctx context.Context, cfg Config, from, to time.Time) []Source
}

type Kind string

const (
	// Text is kept in the database.
	Text Kind = "text"
	// Secret is kept in the keychain and never leaves the server.
	Secret Kind = "secret"
	// List is rows of Text and Secret columns, given by Fields. Lists don't nest.
	List Kind = "list"
)

type Field struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  Kind   `json:"kind"`
	// Help is one line under the field, such as where to find a URL.
	Help string `json:"help,omitempty"`
	// Required means the integration can't pull without it. A required list needs a row, and a
	// row needs its required columns before it can be saved.
	Required bool    `json:"required"`
	Fields   []Field `json:"fields,omitempty"`
}

// Config is an integration's saved values with its secrets filled in.
type Config struct {
	Values map[string]string
	Lists  map[string][]Row
}

// Row is one row of a list. Its ID is made by the server and never changes.
type Row struct {
	ID     string            `json:"id"`
	Values map[string]string `json:"values"`
}

// Source is what one part of an integration pulled. Err is set when it couldn't be pulled, in
// words fit for the user, and never holds a secret.
type Source struct {
	Label  string
	Events []Event
	Err    error
}

// Event is one pulled event. Key is its identity in the feed and stays the same when it moves.
// Series is set on an occurrence of a recurring event; skipping a series matches it.
type Event struct {
	Key      string
	Series   string
	Title    string
	StartsAt time.Time
	EndsAt   *time.Time
}
```

`internal/integrations/gcal/gcal.go` (new):

```go
// Package gcal pulls events from Google Calendar through each calendar's secret iCal address.
package gcal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"

	"github.com/jameynakama/whatnow/internal/integrations"
)

// Calendar is the Google Calendar integration.
type Calendar struct {
	client *http.Client
}

// New returns the integration. A nil client means one that gives up on a calendar after 30
// seconds. Go's client asks for gzip, which Google serves, so a 20 MB feed is under 2 MB over the
// wire; setting Accept-Encoding by hand would stop it decompressing.
func New(client *http.Client) *Calendar {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Calendar{client: client}
}

func (*Calendar) ID() string   { return "gcal" }
func (*Calendar) Name() string { return "Google Calendar" }

func (*Calendar) Fields() []integrations.Field {
	return []integrations.Field{{
		Key:      "calendars",
		Label:    "Calendars",
		Kind:     integrations.List,
		Required: true,
		Help:     "In Google Calendar: Settings → your calendar → Integrate calendar → Secret address in iCal format.",
		Fields: []integrations.Field{
			{Key: "label", Label: "Name", Kind: integrations.Text, Required: true},
			{Key: "url", Label: "Secret address", Kind: integrations.Secret, Required: true},
		},
	}}
}

func (c *Calendar) Pull(ctx context.Context, cfg integrations.Config, from, to time.Time) []integrations.Source {
	var sources []integrations.Source
	for _, row := range cfg.Lists["calendars"] {
		src := integrations.Source{Label: row.Values["label"]}
		body, err := c.fetch(ctx, row.Values["url"])
		if err == nil {
			src.Events, err = parse(body, from, to)
		}
		src.Err = err
		sources = append(sources, src)
	}
	return sources
}

// fetch downloads a feed. Its errors never wrap the client's, which name the secret URL.
func (c *Calendar) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.New("that address isn't a URL")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, errors.New("Google took too long to answer")
		}
		return nil, errors.New("couldn't reach Google")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Google said %d, check the address", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.New("the download was cut off")
	}
	return body, nil
}

var errUnreadable = errors.New("couldn't read the calendar")

// parse returns the timed events that start in [from, to), earliest first. It drops all-day
// events, canceled ones, and ones the calendar's owner declined.
func parse(body []byte, from, to time.Time) ([]integrations.Event, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(body)).Decode()
	if err != nil {
		return nil, errUnreadable
	}
	p := parser{zones: map[string]*time.Location{}, local: from.Location(), owner: owner(cal)}
	events := cal.Events()

	// A moved or canceled occurrence is its own VEVENT, with a RECURRENCE-ID naming the start it
	// replaces. It replaces it wherever it moved to, even out of the window.
	replaced := map[string]bool{}
	for _, e := range events {
		if rid := e.Props.Get(ical.PropRecurrenceID); rid != nil {
			if t, err := p.at(rid); err == nil {
				replaced[occurrenceKey(uid(e), t)] = true
			}
		}
	}

	var out []integrations.Event
	for _, e := range events {
		start := e.Props.Get(ical.PropDateTimeStart)
		if start == nil || isDate(start) || p.skip(e) {
			continue
		}
		got, err := p.event(e, start, replaced, from, to)
		if err != nil {
			// One event Google wrote oddly shouldn't cost the whole calendar.
			continue
		}
		out = append(out, got...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartsAt.Before(out[j].StartsAt) })
	return out, nil
}

type parser struct {
	// zones caches each TZID's location: loading one reads the zone file from disk, and a busy
	// calendar names one on every event.
	zones map[string]*time.Location
	// local is where a floating time, with no zone, is read.
	local *time.Location
	// owner is the calendar owner's email, or empty when the feed doesn't say.
	owner string
}

// event returns e's occurrences that start in [from, to): one for a single event, or each
// occurrence of a recurring one that hasn't been moved or canceled.
func (p parser) event(e ical.Event, startProp *ical.Prop, replaced map[string]bool, from, to time.Time) ([]integrations.Event, error) {
	start, err := p.at(startProp)
	if err != nil {
		return nil, err
	}
	length, err := p.length(e, start)
	if err != nil {
		return nil, err
	}
	id := uid(e)
	title, _ := e.Props.Text(ical.PropSummary)
	title = strings.TrimSpace(title)
	if title == "" {
		title = "(no title)"
	}
	one := func(key, series string, at time.Time) integrations.Event {
		ev := integrations.Event{Key: key, Series: series, Title: title, StartsAt: at}
		if length > 0 {
			end := at.Add(length)
			ev.EndsAt = &end
		}
		return ev
	}

	if rid := e.Props.Get(ical.PropRecurrenceID); rid != nil {
		original, err := p.at(rid)
		if err != nil || !inWindow(start, from, to) {
			return nil, err
		}
		return []integrations.Event{one(occurrenceKey(id, original), id, start)}, nil
	}

	rule, err := e.Props.RecurrenceRule()
	if err != nil {
		return nil, err
	}
	if rule == nil {
		if !inWindow(start, from, to) {
			return nil, nil
		}
		return []integrations.Event{one(id, "", start)}, nil
	}
	rule.Dtstart = start
	r, err := rrule.NewRRule(*rule)
	if err != nil {
		return nil, err
	}
	set := rrule.Set{}
	set.RRule(r)
	for _, ex := range e.Props.Values(ical.PropExceptionDates) {
		// One EXDATE line can list several dates.
		for _, v := range strings.Split(ex.Value, ",") {
			prop := ex
			prop.Value = v
			if t, err := p.at(&prop); err == nil {
				set.ExDate(t)
			}
		}
	}
	var out []integrations.Event
	for _, at := range set.Between(from, to, true) {
		key := occurrenceKey(id, at)
		if !inWindow(at, from, to) || replaced[key] {
			continue
		}
		out = append(out, one(key, id, at))
	}
	return out, nil
}

// skip says whether e is canceled or declined by the calendar's owner.
func (p parser) skip(e ical.Event) bool {
	if status, _ := e.Props.Text(ical.PropStatus); strings.EqualFold(status, "CANCELLED") {
		return true
	}
	if p.owner == "" {
		return false
	}
	for _, a := range e.Props.Values(ical.PropAttendee) {
		if email(a.Value) == p.owner {
			return strings.EqualFold(a.Params.Get(ical.ParamParticipationStatus), "DECLINED")
		}
	}
	return false
}

// length is how long e lasts, from DTEND or DURATION, or 0 when it gives no end.
func (p parser) length(e ical.Event, start time.Time) (time.Duration, error) {
	if end := e.Props.Get(ical.PropDateTimeEnd); end != nil {
		t, err := p.at(end)
		if err != nil {
			return 0, err
		}
		return max(t.Sub(start), 0), nil
	}
	if dur := e.Props.Get(ical.PropDuration); dur != nil {
		d, err := dur.Duration()
		return max(d, 0), err
	}
	return 0, nil
}

// at reads a date-time: UTC when it ends in Z, else in its TZID's zone, else floating.
func (p parser) at(prop *ical.Prop) (time.Time, error) {
	v := prop.Value
	if strings.HasSuffix(v, "Z") {
		return time.ParseInLocation("20060102T150405Z", v, time.UTC)
	}
	loc := p.local
	if tzid := prop.Params.Get(ical.PropTimezoneID); tzid != "" {
		z, ok := p.zones[tzid]
		if !ok {
			var err error
			if z, err = time.LoadLocation(tzid); err != nil {
				return time.Time{}, err
			}
			p.zones[tzid] = z
		}
		loc = z
	}
	return time.ParseInLocation("20060102T150405", v, loc)
}

// owner is the email Google names a primary calendar after, or empty when the name isn't one.
func owner(cal *ical.Calendar) string {
	name, _ := cal.Props.Text("X-WR-CALNAME")
	if !strings.Contains(name, "@") {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(name))
}

func email(attendee string) string {
	v := strings.ToLower(strings.TrimSpace(attendee))
	return strings.TrimPrefix(v, "mailto:")
}

func isDate(prop *ical.Prop) bool {
	return prop.ValueType() == ical.ValueDate || len(prop.Value) == len("20060102")
}

func uid(e ical.Event) string {
	id, _ := e.Props.Text(ical.PropUID)
	return id
}

// occurrenceKey names one occurrence of a series by its original start, which a moved
// occurrence keeps as its RECURRENCE-ID.
func occurrenceKey(uid string, start time.Time) string {
	return uid + "|" + start.UTC().Format("20060102T150405Z")
}

func inWindow(t, from, to time.Time) bool { return !t.Before(from) && t.Before(to) }
```

- [ ] **Step 5: Run the checks**

Run: `go test ./internal/integrations/... && go vet ./...`
Expected: PASS. Then check the parser against a real feed if one is at hand (Jamey's is in the session scratchpad as `work.ics`): parse today and confirm the timed events match Google's view, in well under a second.

- [ ] **Step 6: Commit**

```bash
git add internal/integrations/integrations.go internal/integrations/gcal/gcal.go internal/integrations/gcal/gcal_test.go go.mod go.sum
git commit -m "Add the integration contract and Google Calendar"
```

---

### Task 3: Keep integration settings and pull through a runner

**Files:**
- Create: `internal/store/migrations/0008_integrations.sql`
- Create: `internal/integrations/saved.go`
- Create: `internal/integrations/runner.go`
- Create: `internal/integrations/runner_test.go`

**Interfaces:**
- Consumes: Task 1's `SyncEvents`, Task 2's contract.
- Produces: `func NewRunner(db *sql.DB, svc *service.Service, hub *stream.Hub, loc *time.Location, now func() time.Time, all ...Integration) *Runner`.
- Produces, on `*Runner`: `Views(ctx) ([]View, error)`, `View(ctx, id) (View, error)`, `SetEnabled(ctx, id, on bool) (View, error)`, `Save(ctx, id, Update) (View, error)`, `Pull(ctx, id) (PullResult, error)`, `Skip(ctx, id, Skip, eventID int64) (batch int64, error)`, `Unskip(ctx, id, key) (View, error)`, `Secrets(ctx) ([]string, error)`.
- Produces: `func DeleteSecrets(accounts []string) error`; `View{ID, Name, Fields, Enabled, Ready, Values, Secrets, Lists map[string][]RowView, Skipped []Skip}` with `On() bool`; `RowView{ID, Values, Secrets}`; `Update{Values; Lists map[string][]RowUpdate}`; `RowUpdate{ID, Values}`; `Skip{Key, Title}`; `PullResult{BatchID, Sources []SourceResult}`; `SourceResult{Label, Error, Events []PulledEvent}`; `PulledEvent{ID, Title, StartsAt, EndsAt, Series}`.
- Errors wrap `service.ErrInvalid`, `ErrNotFound`, and `ErrConflict`, so the API maps them to 400, 404, and 409.
- Keychain accounts: `integration/<id>/<key>`, or `integration/<id>/<rowID>/<key>` for a row's column, under the service `whatnow`.

- [ ] **Step 1: Write the failing tests**

The tests use a fake integration with a text field, a secret, and a list of calendars, each a name and a secret feed; a feed it doesn't know fails. `keyring.MockInit()` keeps them off the real keychain.

`internal/integrations/runner_test.go` (new):

```go
package integrations_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/integrations"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

var loc = time.FixedZone("UTC-5", -5*3600)

// now is 09:00 local on October 5th.
var now = time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)

// fake is an integration with a list of calendars, each a name and a secret feed, and a secret
// token. Its sources come from feeds, by feed value; a missing feed fails.
type fake struct {
	feeds map[string][]integrations.Event
	// got is the config and window of the last pull.
	got      integrations.Config
	from, to time.Time
}

func (*fake) ID() string   { return "fake" }
func (*fake) Name() string { return "Fake" }
func (*fake) Fields() []integrations.Field {
	return []integrations.Field{
		{Key: "team", Label: "Team", Kind: integrations.Text},
		{Key: "token", Label: "Token", Kind: integrations.Secret},
		{Key: "calendars", Label: "Calendars", Kind: integrations.List, Required: true, Fields: []integrations.Field{
			{Key: "label", Label: "Name", Kind: integrations.Text, Required: true},
			{Key: "feed", Label: "Feed", Kind: integrations.Secret, Required: true},
		}},
	}
}

func (f *fake) Pull(_ context.Context, cfg integrations.Config, from, to time.Time) []integrations.Source {
	f.got, f.from, f.to = cfg, from, to
	var out []integrations.Source
	for _, row := range cfg.Lists["calendars"] {
		src := integrations.Source{Label: row.Values["label"]}
		events, ok := f.feeds[row.Values["feed"]]
		if ok {
			src.Events = events
		} else {
			src.Err = errors.New("couldn't reach it")
		}
		out = append(out, src)
	}
	return out
}

type env struct {
	runner *integrations.Runner
	svc    *service.Service
	hub    *stream.Hub
	fake   *fake
}

func newEnv(t *testing.T) env {
	t.Helper()
	keyring.MockInit()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := func() time.Time { return now }
	svc := service.New(db, clock)
	hub := stream.NewHub()
	f := &fake{feeds: map[string][]integrations.Event{}}
	return env{runner: integrations.NewRunner(db, svc, hub, loc, clock, f), svc: svc, hub: hub, fake: f}
}

func at(hour int) time.Time { return time.Date(2026, 10, 5, hour, 0, 0, 0, loc) }

func event(key, series, title string, hour int) integrations.Event {
	return integrations.Event{Key: key, Series: series, Title: title, StartsAt: at(hour)}
}

// setUp saves calendars as name → feed, and turns the integration on.
func (e env) setUp(t *testing.T, calendars ...[2]string) integrations.View {
	t.Helper()
	var rows []integrations.RowUpdate
	for _, c := range calendars {
		rows = append(rows, integrations.RowUpdate{Values: map[string]string{"label": c[0], "feed": c[1]}})
	}
	if _, err := e.runner.Save(context.Background(), "fake", integrations.Update{Lists: map[string][]integrations.RowUpdate{"calendars": rows}}); err != nil {
		t.Fatal(err)
	}
	v, err := e.runner.SetEnabled(context.Background(), "fake", true)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (e env) pull(t *testing.T) integrations.PullResult {
	t.Helper()
	res, err := e.runner.Pull(context.Background(), "fake")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// pulled lists a result as "source: title, title" lines, or "source: error".
func pulled(res integrations.PullResult) []string {
	var out []string
	for _, s := range res.Sources {
		line := s.Label + ":"
		if s.Error != "" {
			line += " " + s.Error
		}
		for _, e := range s.Events {
			line += " " + e.Title
		}
		out = append(out, line)
	}
	return out
}

func (e env) today(t *testing.T) []string {
	t.Helper()
	events, err := e.svc.ListEvents(context.Background(), at(0), at(24))
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, ev := range events {
		titles = append(titles, ev.Title)
	}
	return titles
}

func TestViewShowsSecretsOnlyAsSet(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v, err := e.runner.View(ctx, "fake")
	if err != nil {
		t.Fatal(err)
	}
	if v.Enabled || v.Ready || v.Secrets["token"] || len(v.Lists["calendars"]) != 0 {
		t.Errorf("fresh view = %+v", v)
	}

	v, err = e.runner.Save(ctx, "fake", integrations.Update{
		Values: map[string]string{"team": " Platform ", "token": "s3cret"},
		Lists: map[string][]integrations.RowUpdate{"calendars": {
			{Values: map[string]string{"label": "Work", "feed": "https://example.com/private.ics"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	row := v.Lists["calendars"][0]
	if v.Values["team"] != "Platform" || !v.Secrets["token"] || !v.Ready || v.Enabled {
		t.Errorf("view = %+v", v)
	}
	if row.ID == "" || row.Values["label"] != "Work" || !row.Secrets["feed"] {
		t.Errorf("row = %+v", row)
	}
	if _, ok := row.Values["feed"]; ok {
		t.Errorf("row values hold the secret: %+v", row.Values)
	}
}

func TestSaveKeepsABlankSecretAndRemovesARowsSecret(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v := e.setUp(t, [2]string{"Work", "work-feed"}, [2]string{"Personal", "home-feed"})
	work, home := v.Lists["calendars"][0], v.Lists["calendars"][1]

	// Renaming Work with a blank feed keeps its feed. Personal is left out, so it goes.
	v, err := e.runner.Save(ctx, "fake", integrations.Update{Lists: map[string][]integrations.RowUpdate{"calendars": {
		{ID: work.ID, Values: map[string]string{"label": "Job"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Lists["calendars"]; len(got) != 1 || got[0].ID != work.ID || got[0].Values["label"] != "Job" || !got[0].Secrets["feed"] {
		t.Errorf("rows = %+v", got)
	}
	e.pull(t)
	if diff := cmp.Diff("work-feed", e.fake.got.Lists["calendars"][0].Values["feed"]); diff != "" {
		t.Errorf("feed (-want +got):\n%s", diff)
	}
	accts, err := e.runner.Secrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accts {
		if v, _ := keyring.Get("whatnow", a); v == "home-feed" {
			t.Errorf("removed row's feed is still in the keychain as %s", a)
		}
	}
	if _, err := keyring.Get("whatnow", "integration/fake/"+home.ID+"/feed"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("removed row's feed: err = %v", err)
	}
}

func TestSaveRejectsBadInput(t *testing.T) {
	e := newEnv(t)
	cases := map[string]integrations.Update{
		"unknown field":      {Values: map[string]string{"nope": "x"}},
		"list as a value":    {Values: map[string]string{"calendars": "x"}},
		"unknown list":       {Lists: map[string][]integrations.RowUpdate{"team": {}}},
		"unknown row":        {Lists: map[string][]integrations.RowUpdate{"calendars": {{ID: "abc", Values: map[string]string{"label": "x", "feed": "y"}}}}},
		"unknown column":     {Lists: map[string][]integrations.RowUpdate{"calendars": {{Values: map[string]string{"label": "x", "feed": "y", "z": "1"}}}}},
		"new row, no secret": {Lists: map[string][]integrations.RowUpdate{"calendars": {{Values: map[string]string{"label": "Work"}}}}},
		"no name":            {Lists: map[string][]integrations.RowUpdate{"calendars": {{Values: map[string]string{"label": " ", "feed": "y"}}}}},
	}
	for name, u := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := e.runner.Save(context.Background(), "fake", u); !errors.Is(err, service.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
	if _, err := e.runner.Save(context.Background(), "nope", integrations.Update{}); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown integration: err = %v", err)
	}
}

func TestSaveKeepsEnabledAndSkipped(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.fake.feeds["f"] = []integrations.Event{event("a|1", "a", "Blockout", 15)}
	v := e.setUp(t, [2]string{"Work", "f"})
	res := e.pull(t)
	if _, err := e.runner.Skip(ctx, "fake", integrations.Skip{Key: "a", Title: "Blockout"}, res.Sources[0].Events[0].ID); err != nil {
		t.Fatal(err)
	}

	v, err := e.runner.Save(ctx, "fake", integrations.Update{Lists: map[string][]integrations.RowUpdate{"calendars": {
		{ID: v.Lists["calendars"][0].ID, Values: map[string]string{"label": "Job"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Enabled || len(v.Skipped) != 1 {
		t.Errorf("view = %+v", v)
	}
}

func TestPullNeedsTheIntegrationOn(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.runner.Pull(ctx, "fake"); !errors.Is(err, service.ErrConflict) {
		t.Errorf("not set up: err = %v", err)
	}
	e.setUp(t, [2]string{"Work", "f"})
	if _, err := e.runner.SetEnabled(ctx, "fake", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.runner.Pull(ctx, "fake"); !errors.Is(err, service.ErrConflict) {
		t.Errorf("off: err = %v", err)
	}
	if _, err := e.runner.Pull(ctx, "nope"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown: err = %v", err)
	}
}

func TestPullBringsInTodayBySource(t *testing.T) {
	e := newEnv(t)
	e.fake.feeds["work"] = []integrations.Event{event("s|9", "s", "Standup", 9), event("shared", "", "All hands", 11)}
	e.fake.feeds["home"] = []integrations.Event{event("shared", "", "All hands", 11), event("d", "", "Dentist", 17)}
	e.setUp(t, [2]string{"Work", "work"}, [2]string{"Personal", "home"})
	msgs, unsubscribe := e.hub.Subscribe("batch.applied")
	defer unsubscribe()

	res := e.pull(t)

	if diff := cmp.Diff([]string{"Work: Standup All hands", "Personal: Dentist"}, pulled(res)); diff != "" {
		t.Errorf("result (-want +got):\n%s", diff)
	}
	if got := res.Sources[0].Events[0]; got.ID == 0 || got.Series != "s" {
		t.Errorf("standup = %+v", got)
	}
	if !e.fake.from.Equal(at(0)) || !e.fake.to.Equal(at(24)) {
		t.Errorf("window = %v to %v", e.fake.from, e.fake.to)
	}
	if diff := cmp.Diff([]string{"Standup", "All hands", "Dentist"}, e.today(t)); diff != "" {
		t.Errorf("today (-want +got):\n%s", diff)
	}
	select {
	case m := <-msgs:
		if b := m.Data.(stream.BatchApplied); b.BatchID != res.BatchID || len(b.Activity) != 3 {
			t.Errorf("published = %+v", b)
		}
	default:
		t.Error("the pull wasn't announced")
	}
}

func TestAFailedSourceRemovesNothing(t *testing.T) {
	e := newEnv(t)
	e.fake.feeds["work"] = []integrations.Event{event("s|9", "s", "Standup", 9)}
	e.fake.feeds["home"] = []integrations.Event{event("d", "", "Dentist", 17)}
	e.setUp(t, [2]string{"Work", "work"}, [2]string{"Personal", "home"})
	e.pull(t)

	delete(e.fake.feeds, "home")
	res := e.pull(t)

	if diff := cmp.Diff([]string{"Work: Standup", "Personal: couldn't reach it"}, pulled(res)); diff != "" {
		t.Errorf("result (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"Standup", "Dentist"}, e.today(t)); diff != "" {
		t.Errorf("today (-want +got):\n%s", diff)
	}
}

func TestSkipRemovesTodayAndKeepsTheSeriesOut(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.fake.feeds["work"] = []integrations.Event{event("b|15", "b", "Blockout", 15), event("s|9", "s", "Standup", 9)}
	e.setUp(t, [2]string{"Work", "work"})
	res := e.pull(t)
	blockout := res.Sources[0].Events[0]

	batch, err := e.runner.Skip(ctx, "fake", integrations.Skip{Key: blockout.Series, Title: " Blockout "}, blockout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"Standup"}, e.today(t)); diff != "" {
		t.Errorf("today after skip (-want +got):\n%s", diff)
	}

	// Tomorrow's occurrence has a new key but the same series, so it stays out too.
	e.fake.feeds["work"] = []integrations.Event{event("b|15-2", "b", "Blockout", 16), event("s|9", "s", "Standup", 9)}
	if diff := cmp.Diff([]string{"Work: Standup"}, pulled(e.pull(t))); diff != "" {
		t.Errorf("pull after skip (-want +got):\n%s", diff)
	}

	// Undoing the skip brings back today's occurrence; the series stays skipped.
	if err := e.svc.Undo(ctx, batch); err != nil {
		t.Fatal(err)
	}
	v, err := e.runner.View(ctx, "fake")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]integrations.Skip{{Key: "b", Title: "Blockout"}}, v.Skipped); diff != "" {
		t.Errorf("skipped (-want +got):\n%s", diff)
	}

	v, err = e.runner.Unskip(ctx, "fake", "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Skipped) != 0 {
		t.Errorf("after unskip = %+v", v.Skipped)
	}
	if diff := cmp.Diff([]string{"Work: Blockout Standup"}, pulled(e.pull(t))); diff != "" {
		t.Errorf("pull after unskip (-want +got):\n%s", diff)
	}
}

func TestSkipNeedsAKeyAndALiveEvent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.runner.Skip(ctx, "fake", integrations.Skip{Key: " "}, 1); !errors.Is(err, service.ErrInvalid) {
		t.Errorf("blank key: err = %v", err)
	}
	if _, err := e.runner.Skip(ctx, "fake", integrations.Skip{Key: "b"}, 99); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("missing event: err = %v", err)
	}
	if v, _ := e.runner.View(ctx, "fake"); len(v.Skipped) != 0 {
		t.Errorf("a failed skip was saved: %+v", v.Skipped)
	}
}

func TestSecretsNamesEverySavedSecret(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	v := e.setUp(t, [2]string{"Work", "f"})
	accts, err := e.runner.Secrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"integration/fake/token", "integration/fake/" + v.Lists["calendars"][0].ID + "/feed"}
	if diff := cmp.Diff(want, accts); diff != "" {
		t.Errorf("secrets (-want +got):\n%s", diff)
	}
	if err := integrations.DeleteSecrets(accts); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.runner.View(ctx, "fake"); v.Lists["calendars"][0].Secrets["feed"] {
		t.Error("the feed is still set")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/integrations/`
Expected: FAIL to compile: `NewRunner` and the view types are undefined.

- [ ] **Step 3: Implement**

`internal/store/migrations/0008_integrations.sql` (new):

```sql
-- One row per integration that has been set up, holding its settings as JSON. It's apart from the
-- settings document, which the General page saves whole. Secrets live in the keychain.
CREATE TABLE integrations (
    id TEXT PRIMARY KEY,
    body TEXT NOT NULL
);
```

`internal/integrations/saved.go` (new):

```go
package integrations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/zalando/go-keyring"
)

// saved is an integration's settings as stored. Values and row values hold text fields only;
// secrets are in the keychain.
type saved struct {
	Enabled bool              `json:"enabled"`
	Values  map[string]string `json:"values"`
	Lists   map[string][]Row  `json:"lists"`
	Skipped []Skip            `json:"skipped"`
}

// Skip is a series, or any pulled item, the user chose never to see. Title is only for showing.
type Skip struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

func load(ctx context.Context, db *sql.DB, id string) (saved, error) {
	var s saved
	var body string
	err := db.QueryRowContext(ctx, `SELECT body FROM integrations WHERE id = ?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal([]byte(body), &s)
}

func store(ctx context.Context, db *sql.DB, id string, s saved) error {
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO integrations (id, body) VALUES (?, ?) ON CONFLICT (id) DO UPDATE SET body = excluded.body`,
		id, string(body))
	return err
}

func newRowID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

const keychainService = "whatnow"

// account names a secret in the keychain: one of the integration's own fields, or a row's.
func account(id, rowID, key string) string {
	if rowID == "" {
		return "integration/" + id + "/" + key
	}
	return "integration/" + id + "/" + rowID + "/" + key
}

// secret reads a secret, returning "" when it isn't set.
func secret(acct string) (string, error) {
	v, err := keyring.Get(keychainService, acct)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return v, err
}

// DeleteSecrets removes secrets from the keychain. One that isn't there is fine.
func DeleteSecrets(accounts []string) error {
	var errs []error
	for _, acct := range accounts {
		if err := keyring.Delete(keychainService, acct); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
```

`internal/integrations/runner.go` (new):

```go
package integrations

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/stream"
)

// Runner keeps each integration's settings and syncs what it pulls into today's events.
type Runner struct {
	db  *sql.DB
	svc *service.Service
	hub *stream.Hub
	loc *time.Location
	now func() time.Time
	all []Integration
}

func NewRunner(db *sql.DB, svc *service.Service, hub *stream.Hub, loc *time.Location, now func() time.Time, all ...Integration) *Runner {
	return &Runner{db: db, svc: svc, hub: hub, loc: loc, now: now, all: all}
}

// View is an integration as the settings screen sees it. Secrets show only whether they're set.
type View struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Fields  []Field `json:"fields"`
	Enabled bool    `json:"enabled"`
	// Ready means every required field is filled, so it can pull once it's enabled.
	Ready   bool                 `json:"ready"`
	Values  map[string]string    `json:"values"`
	Secrets map[string]bool      `json:"secrets"`
	Lists   map[string][]RowView `json:"lists"`
	Skipped []Skip               `json:"skipped"`
}

type RowView struct {
	ID      string            `json:"id"`
	Values  map[string]string `json:"values"`
	Secrets map[string]bool   `json:"secrets"`
}

// On says whether the integration can pull.
func (v View) On() bool { return v.Enabled && v.Ready }

// Update is what saving an integration's fields sends. A blank secret keeps the saved one. A row
// with no ID is new; a saved row left out is removed, with its secrets.
type Update struct {
	Values map[string]string      `json:"values"`
	Lists  map[string][]RowUpdate `json:"lists"`
}

type RowUpdate struct {
	ID     string            `json:"id"`
	Values map[string]string `json:"values"`
}

func (r *Runner) find(id string) (Integration, error) {
	for _, in := range r.all {
		if in.ID() == id {
			return in, nil
		}
	}
	return nil, fmt.Errorf("%w: integration %q", service.ErrNotFound, id)
}

func (r *Runner) Views(ctx context.Context) ([]View, error) {
	views := []View{}
	for _, in := range r.all {
		v, err := r.view(ctx, in)
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

func (r *Runner) View(ctx context.Context, id string) (View, error) {
	in, err := r.find(id)
	if err != nil {
		return View{}, err
	}
	return r.view(ctx, in)
}

func (r *Runner) view(ctx context.Context, in Integration) (View, error) {
	s, err := load(ctx, r.db, in.ID())
	if err != nil {
		return View{}, err
	}
	v := View{
		ID: in.ID(), Name: in.Name(), Fields: in.Fields(), Enabled: s.Enabled, Ready: true,
		Values: map[string]string{}, Secrets: map[string]bool{}, Lists: map[string][]RowView{}, Skipped: s.Skipped,
	}
	if v.Skipped == nil {
		v.Skipped = []Skip{}
	}
	for _, f := range v.Fields {
		switch f.Kind {
		case Text:
			v.Values[f.Key] = s.Values[f.Key]
			v.Ready = v.Ready && (!f.Required || s.Values[f.Key] != "")
		case Secret:
			val, err := secret(account(in.ID(), "", f.Key))
			if err != nil {
				return View{}, err
			}
			v.Secrets[f.Key] = val != ""
			v.Ready = v.Ready && (!f.Required || val != "")
		case List:
			rows := []RowView{}
			for _, row := range s.Lists[f.Key] {
				rv := RowView{ID: row.ID, Values: map[string]string{}, Secrets: map[string]bool{}}
				for _, c := range f.Fields {
					if c.Kind == Secret {
						val, err := secret(account(in.ID(), row.ID, c.Key))
						if err != nil {
							return View{}, err
						}
						rv.Secrets[c.Key] = val != ""
						continue
					}
					rv.Values[c.Key] = row.Values[c.Key]
				}
				rows = append(rows, rv)
			}
			v.Lists[f.Key] = rows
			v.Ready = v.Ready && (!f.Required || len(rows) > 0)
		}
	}
	return v, nil
}

func (r *Runner) SetEnabled(ctx context.Context, id string, on bool) (View, error) {
	in, err := r.find(id)
	if err != nil {
		return View{}, err
	}
	s, err := load(ctx, r.db, id)
	if err != nil {
		return View{}, err
	}
	s.Enabled = on
	if err := store(ctx, r.db, id, s); err != nil {
		return View{}, err
	}
	return r.view(ctx, in)
}

// Save replaces the integration's fields with u. It writes new secrets before the settings, and
// removes the secrets of removed rows after, so a failure never leaves a row without its secret.
func (r *Runner) Save(ctx context.Context, id string, u Update) (View, error) {
	in, err := r.find(id)
	if err != nil {
		return View{}, err
	}
	s, err := load(ctx, r.db, id)
	if err != nil {
		return View{}, err
	}
	next := saved{Enabled: s.Enabled, Values: map[string]string{}, Lists: map[string][]Row{}, Skipped: s.Skipped}
	writes := map[string]string{}
	fields := in.Fields()
	for key := range u.Values {
		if f := field(fields, key); f == nil || f.Kind == List {
			return View{}, fmt.Errorf("%w: %s has no field %q", service.ErrInvalid, in.Name(), key)
		}
	}
	for key := range u.Lists {
		if f := field(fields, key); f == nil || f.Kind != List {
			return View{}, fmt.Errorf("%w: %s has no list %q", service.ErrInvalid, in.Name(), key)
		}
	}
	for _, f := range fields {
		switch f.Kind {
		case Text:
			next.Values[f.Key] = strings.TrimSpace(u.Values[f.Key])
		case Secret:
			if v := strings.TrimSpace(u.Values[f.Key]); v != "" {
				writes[account(id, "", f.Key)] = v
			}
		case List:
			rows, err := listRows(id, f, s.Lists[f.Key], u.Lists[f.Key], writes)
			if err != nil {
				return View{}, err
			}
			next.Lists[f.Key] = rows
		}
	}
	for acct, v := range writes {
		if err := keyring.Set(keychainService, acct, v); err != nil {
			return View{}, err
		}
	}
	if err := store(ctx, r.db, id, next); err != nil {
		return View{}, err
	}
	gone := slices.DeleteFunc(rowSecrets(in, s), func(acct string) bool { return slices.Contains(rowSecrets(in, next), acct) })
	if err := DeleteSecrets(gone); err != nil {
		return View{}, err
	}
	return r.view(ctx, in)
}

// listRows checks a list's updated rows against what's saved, giving each new row an ID and
// adding the secrets to write.
func listRows(id string, f Field, saved []Row, updates []RowUpdate, writes map[string]string) ([]Row, error) {
	rows := []Row{}
	for i, ru := range updates {
		rowID := ru.ID
		if rowID == "" {
			rowID = newRowID()
		} else if !slices.ContainsFunc(saved, func(r Row) bool { return r.ID == rowID }) {
			return nil, fmt.Errorf("%w: %s has no row %q", service.ErrInvalid, f.Label, rowID)
		}
		for key := range ru.Values {
			if field(f.Fields, key) == nil {
				return nil, fmt.Errorf("%w: %s has no column %q", service.ErrInvalid, f.Label, key)
			}
		}
		row := Row{ID: rowID, Values: map[string]string{}}
		for _, c := range f.Fields {
			v := strings.TrimSpace(ru.Values[c.Key])
			missing := v == ""
			if c.Kind == Secret {
				acct := account(id, rowID, c.Key)
				if v != "" {
					writes[acct] = v
				} else if ru.ID != "" {
					old, err := secret(acct)
					if err != nil {
						return nil, err
					}
					missing = old == ""
				}
			} else {
				row.Values[c.Key] = v
			}
			if c.Required && missing {
				return nil, fmt.Errorf("%w: %s row %d needs a %s", service.ErrInvalid, f.Label, i+1, strings.ToLower(c.Label))
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func field(fields []Field, key string) *Field {
	for i := range fields {
		if fields[i].Key == key {
			return &fields[i]
		}
	}
	return nil
}

// rowSecrets names the keychain entries of every secret column in s's list rows.
func rowSecrets(in Integration, s saved) []string {
	var accts []string
	for _, f := range in.Fields() {
		for _, row := range s.Lists[f.Key] {
			for _, c := range f.Fields {
				if c.Kind == Secret {
					accts = append(accts, account(in.ID(), row.ID, c.Key))
				}
			}
		}
	}
	return accts
}

// Secrets names every secret the integrations could have in the keychain, for wiping.
func (r *Runner) Secrets(ctx context.Context) ([]string, error) {
	var accts []string
	for _, in := range r.all {
		s, err := load(ctx, r.db, in.ID())
		if err != nil {
			return nil, err
		}
		for _, f := range in.Fields() {
			if f.Kind == Secret {
				accts = append(accts, account(in.ID(), "", f.Key))
			}
		}
		accts = append(accts, rowSecrets(in, s)...)
	}
	return accts, nil
}

// config fills in the saved settings' secrets.
func config(in Integration, s saved) (Config, error) {
	cfg := Config{Values: map[string]string{}, Lists: map[string][]Row{}}
	for _, f := range in.Fields() {
		switch f.Kind {
		case Text:
			cfg.Values[f.Key] = s.Values[f.Key]
		case Secret:
			v, err := secret(account(in.ID(), "", f.Key))
			if err != nil {
				return cfg, err
			}
			cfg.Values[f.Key] = v
		case List:
			for _, row := range s.Lists[f.Key] {
				full := Row{ID: row.ID, Values: map[string]string{}}
				for _, c := range f.Fields {
					v := row.Values[c.Key]
					if c.Kind == Secret {
						var err error
						if v, err = secret(account(in.ID(), row.ID, c.Key)); err != nil {
							return cfg, err
						}
					}
					full.Values[c.Key] = v
				}
				cfg.Lists[f.Key] = append(cfg.Lists[f.Key], full)
			}
		}
	}
	return cfg, nil
}

// PullResult is what a pull brought in, by source, with each event's ID in whatnow. A source that
// failed has Error and no events.
type PullResult struct {
	BatchID int64          `json:"batch_id"`
	Sources []SourceResult `json:"sources"`
}

type SourceResult struct {
	Label  string        `json:"label"`
	Error  string        `json:"error,omitempty"`
	Events []PulledEvent `json:"events"`
}

type PulledEvent struct {
	ID       int64      `json:"id"`
	Title    string     `json:"title"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`
	// Series is set on an occurrence of a recurring event, which removing offers to skip for good.
	Series string `json:"series"`
}

// Pull brings in today's events from the integration, in one user batch, and announces it. An
// event in more than one source lands once, under the first. When any source fails, nothing is
// removed, since the missing source's events can't be told from canceled ones.
func (r *Runner) Pull(ctx context.Context, id string) (PullResult, error) {
	in, err := r.find(id)
	if err != nil {
		return PullResult{}, err
	}
	v, err := r.view(ctx, in)
	if err != nil {
		return PullResult{}, err
	}
	if !v.On() {
		return PullResult{}, fmt.Errorf("%w: %s is off or not set up", service.ErrConflict, in.Name())
	}
	s, err := load(ctx, r.db, id)
	if err != nil {
		return PullResult{}, err
	}
	cfg, err := config(in, s)
	if err != nil {
		return PullResult{}, err
	}
	now := r.now().In(r.loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, r.loc)
	to := from.AddDate(0, 0, 1)
	sources := in.Pull(ctx, cfg, from, to)

	skipped := map[string]bool{}
	for _, sk := range s.Skipped {
		skipped[sk.Key] = true
	}
	var pulled []service.PulledEvent
	type origin struct {
		source int
		event  Event
	}
	seen := map[string]origin{}
	prune := true
	for i, src := range sources {
		if src.Err != nil {
			prune = false
			continue
		}
		for _, e := range src.Events {
			if _, dup := seen[e.Key]; dup || skipped[e.Key] || (e.Series != "" && skipped[e.Series]) {
				continue
			}
			seen[e.Key] = origin{i, e}
			pulled = append(pulled, service.PulledEvent{Key: e.Key, Title: e.Title, StartsAt: e.StartsAt, EndsAt: e.EndsAt})
		}
	}

	batch, err := r.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		return PullResult{}, err
	}
	live, err := r.svc.SyncEvents(ctx, batch, id, from, to, pulled, prune)
	if err != nil {
		return PullResult{}, err
	}
	if err := r.publish(ctx, batch); err != nil {
		return PullResult{}, err
	}

	res := PullResult{BatchID: batch, Sources: make([]SourceResult, len(sources))}
	for i, src := range sources {
		res.Sources[i] = SourceResult{Label: src.Label, Events: []PulledEvent{}}
		if src.Err != nil {
			res.Sources[i].Error = src.Err.Error()
		}
	}
	for _, p := range pulled {
		e, ok := live[p.Key]
		if !ok {
			continue
		}
		o := seen[p.Key]
		res.Sources[o.source].Events = append(res.Sources[o.source].Events, PulledEvent{
			ID: e.ID, Title: e.Title, StartsAt: e.StartsAt, EndsAt: e.EndsAt, Series: o.event.Series,
		})
	}
	return res, nil
}

// Skip stops the integration bringing in what sk names, and removes eventID, today's occurrence,
// in a user batch it announces and returns. The skip itself isn't in the batch: undoing it brings
// back today's occurrence, and settings bring back the series.
func (r *Runner) Skip(ctx context.Context, id string, sk Skip, eventID int64) (int64, error) {
	if _, err := r.find(id); err != nil {
		return 0, err
	}
	sk.Key, sk.Title = strings.TrimSpace(sk.Key), strings.TrimSpace(sk.Title)
	if sk.Key == "" {
		return 0, fmt.Errorf("%w: key is required", service.ErrInvalid)
	}
	batch, err := r.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		return 0, err
	}
	if err := r.svc.DeleteEvent(ctx, batch, eventID); err != nil {
		return 0, err
	}
	s, err := load(ctx, r.db, id)
	if err != nil {
		return 0, err
	}
	if !slices.ContainsFunc(s.Skipped, func(old Skip) bool { return old.Key == sk.Key }) {
		s.Skipped = append(s.Skipped, sk)
	}
	if err := store(ctx, r.db, id, s); err != nil {
		return 0, err
	}
	return batch, r.publish(ctx, batch)
}

// Unskip brings back what key names. The next pull brings it in again.
func (r *Runner) Unskip(ctx context.Context, id, key string) (View, error) {
	in, err := r.find(id)
	if err != nil {
		return View{}, err
	}
	s, err := load(ctx, r.db, id)
	if err != nil {
		return View{}, err
	}
	s.Skipped = slices.DeleteFunc(s.Skipped, func(sk Skip) bool { return sk.Key == key })
	if err := store(ctx, r.db, id, s); err != nil {
		return View{}, err
	}
	return r.view(ctx, in)
}

func (r *Runner) publish(ctx context.Context, batch int64) error {
	acts, err := r.svc.BatchActivity(ctx, batch)
	if err != nil {
		return err
	}
	r.hub.PublishBatch(batch, stream.SourceUser, acts)
	return nil
}
```

- [ ] **Step 4: Run the checks**

Run: `go test ./internal/integrations/... ./internal/store/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations/0008_integrations.sql internal/integrations/saved.go internal/integrations/runner.go internal/integrations/runner_test.go
git commit -m "Keep integration settings and pull through a runner"
```

---

### Task 4: Serve integrations over the API, and wire them into the app

**Files:**
- Create: `internal/httpapi/integrations.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/settings.go`
- Modify: `internal/app/app.go`
- Modify: `cmd/whatnow/main.go`
- Modify: `internal/httpapi/helpers_test.go`
- Create: `internal/httpapi/integrations_test.go`

**Interfaces:**
- Consumes: Task 3's `Runner`.
- Produces: `httpapi.Deps.Integrations *integrations.Runner` and `app.App.Integrations *integrations.Runner` (built in `app.Open` with `gcal.New(nil)`), which the TUI uses in Task 6.
- Produces the routes `GET /api/v1/integrations`, `PUT /api/v1/integrations/{id}`, `PUT /api/v1/integrations/{id}/enabled` (`{enabled}`), `POST /api/v1/integrations/{id}/pull`, `POST /api/v1/integrations/{id}/skipped` (`{key, title, event_id}`, answering a `Mutation`), and `DELETE /api/v1/integrations/{id}/skipped/{key}`.
- `DELETE /api/v1/data` now also deletes integration secrets.

- [ ] **Step 1: Write the failing tests**

`newAPI` gains a feed server: tests set `api.feed` to the calendar body, and `api.feedURL` is its address, with `private-token` in the path so a test can check it never leaks.

`internal/httpapi/helpers_test.go`, apply:

```diff
@@ -15,6 +15,8 @@ import (
 	"github.com/jameynakama/whatnow/internal/cleanup"
 	"github.com/jameynakama/whatnow/internal/domain"
 	"github.com/jameynakama/whatnow/internal/httpapi"
+	"github.com/jameynakama/whatnow/internal/integrations"
+	"github.com/jameynakama/whatnow/internal/integrations/gcal"
 	"github.com/jameynakama/whatnow/internal/llm"
 	"github.com/jameynakama/whatnow/internal/planner"
 	"github.com/jameynakama/whatnow/internal/service"
@@ -36,6 +38,9 @@ type testAPI struct {
 	model llm.Client
 	// models are the ones the connection test checks.
 	models settings.ProviderConfig
+	// feed is the calendar served at feedURL, for the Google Calendar integration.
+	feed    string
+	feedURL string
 }
 
 func newAPI(t *testing.T) *testAPI {
@@ -56,6 +61,9 @@ func newAPI(t *testing.T) *testAPI {
 	}
 	plans := planner.New(svc, api.hub, active, testLoc, now)
 	runner := cleanup.New(svc, api.hub, active, testLoc, now)
+	feeds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, api.feed) }))
+	t.Cleanup(feeds.Close)
+	api.feedURL = feeds.URL + "/private-token/basic.ics"
 	api.handler = httpapi.New(httpapi.Deps{
 		Service:   svc,
 		Hub:       api.hub,
@@ -73,6 +81,9 @@ func newAPI(t *testing.T) *testAPI {
 		Planner: plans,
 		Agent:   agent.New(svc, api.hub, plans, testLoc, now),
 		Static:  http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "static") }),
+		Integrations: integrations.NewRunner(db, svc, api.hub, testLoc, now,
+			gcal.New(nil),
+		),
 	})
 	return api
 }
```

`internal/httpapi/integrations_test.go` (new):

```go
package httpapi_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/integrations"
)

// testFeed has today's standup, a daily blockout, and a one-off, at 09:00 local (UTC-5).
const testFeed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nX-WR-CALNAME:me@example.com\r\n" +
	"BEGIN:VEVENT\r\nUID:standup@google.com\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Standup\r\nDTSTART:20261005T140000Z\r\nDTEND:20261005T141500Z\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:block/out@google.com\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Blockout\r\nRRULE:FREQ=DAILY\r\nDTSTART:20261001T200000Z\r\nDTEND:20261001T210000Z\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

// setUpCalendar saves one calendar named Work, turns Google Calendar on, and returns the view.
func setUpCalendar(t *testing.T, api *testAPI) integrations.View {
	t.Helper()
	update := integrations.Update{Lists: map[string][]integrations.RowUpdate{"calendars": {
		{Values: map[string]string{"label": "Work", "url": api.feedURL}},
	}}}
	rec := api.do(http.MethodPut, "/api/v1/integrations/gcal", update)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "private-token") {
		t.Fatal("the saved view contains the calendar's URL")
	}
	rec = api.do(http.MethodPut, "/api/v1/integrations/gcal/enabled", map[string]bool{"enabled": true})
	wantStatus(t, rec, http.StatusOK)
	return decodeAs[integrations.View](t, rec)
}

func TestIntegrationsListGoogleCalendarWithItsFields(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	views := decodeAs[[]integrations.View](t, api.do(http.MethodGet, "/api/v1/integrations", nil))
	if len(views) != 1 || views[0].ID != "gcal" || views[0].Name != "Google Calendar" || views[0].Enabled {
		t.Fatalf("views = %+v", views)
	}
	cal := views[0].Fields[0]
	if cal.Key != "calendars" || cal.Kind != integrations.List || len(cal.Fields) != 2 || cal.Fields[1].Kind != integrations.Secret {
		t.Errorf("fields = %+v", views[0].Fields)
	}
}

func TestPullingGoogleCalendar(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	api.feed = testFeed
	wantStatus(t, api.do(http.MethodPost, "/api/v1/integrations/gcal/pull", nil), http.StatusConflict)
	v := setUpCalendar(t, api)
	if !v.Enabled || !v.Ready || !v.Lists["calendars"][0].Secrets["url"] {
		t.Fatalf("view = %+v", v)
	}

	rec := api.do(http.MethodPost, "/api/v1/integrations/gcal/pull", nil)
	wantStatus(t, rec, http.StatusOK)
	res := decodeAs[integrations.PullResult](t, rec)
	if len(res.Sources) != 1 || res.Sources[0].Label != "Work" || res.BatchID == 0 {
		t.Fatalf("result = %+v", res)
	}
	var got []string
	for _, e := range res.Sources[0].Events {
		got = append(got, e.Title+" "+e.Series)
	}
	if diff := cmp.Diff([]string{"Standup ", "Blockout block/out@google.com"}, got); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}

	api.feed = "not a calendar"
	res = decodeAs[integrations.PullResult](t, api.do(http.MethodPost, "/api/v1/integrations/gcal/pull", nil))
	if res.Sources[0].Error != "couldn't read the calendar" {
		t.Errorf("failed pull = %+v", res)
	}
}

func TestSkippingASeriesAndBringingItBack(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	api.feed = testFeed
	setUpCalendar(t, api)
	res := decodeAs[integrations.PullResult](t, api.do(http.MethodPost, "/api/v1/integrations/gcal/pull", nil))
	blockout := res.Sources[0].Events[1]

	rec := api.do(http.MethodPost, "/api/v1/integrations/gcal/skipped",
		map[string]any{"key": blockout.Series, "title": blockout.Title, "event_id": blockout.ID})
	wantStatus(t, rec, http.StatusOK)
	if decodeAs[httpapi.Mutation](t, rec).BatchID == 0 {
		t.Error("no batch to undo")
	}
	res = decodeAs[integrations.PullResult](t, api.do(http.MethodPost, "/api/v1/integrations/gcal/pull", nil))
	if n := len(res.Sources[0].Events); n != 1 {
		t.Errorf("after skip: %d events", n)
	}

	// The key has a slash in it, so it's escaped in the path.
	rec = api.do(http.MethodDelete, "/api/v1/integrations/gcal/skipped/"+url.PathEscape(blockout.Series), nil)
	wantStatus(t, rec, http.StatusOK)
	if v := decodeAs[integrations.View](t, rec); len(v.Skipped) != 0 {
		t.Errorf("still skipped: %+v", v.Skipped)
	}
}

func TestSavingIntegrationsRejectsBadInput(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/integrations/gcal",
		map[string]any{"lists": map[string]any{"calendars": []any{map[string]any{"values": map[string]string{"label": "Work"}}}}}),
		http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/integrations/nope", map[string]any{}), http.StatusNotFound)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/integrations/gcal/skipped", map[string]any{"key": "x", "event_id": 99}), http.StatusNotFound)
}

func TestDeleteEverythingForgetsCalendarURLs(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	v := setUpCalendar(t, api)
	acct := "integration/gcal/" + v.Lists["calendars"][0].ID + "/url"
	if _, err := keyring.Get("whatnow", acct); err != nil {
		t.Fatalf("the URL wasn't saved: %v", err)
	}

	wantStatus(t, api.do(http.MethodDelete, "/api/v1/data", nil), http.StatusNoContent)

	if _, err := keyring.Get("whatnow", acct); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("the URL survived: err = %v", err)
	}
	views := decodeAs[[]integrations.View](t, api.do(http.MethodGet, "/api/v1/integrations", nil))
	if views[0].Enabled || len(views[0].Lists["calendars"]) != 0 {
		t.Errorf("after wipe = %+v", views[0])
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/httpapi/`
Expected: FAIL to compile: `httpapi.Deps` has no field `Integrations`.

- [ ] **Step 3: Implement**

gofmt realigns `main.go`'s `Deps` literal for the longer key; that churn is expected. In `Deps` and `App` the new field goes after the existing block, behind its own comment, so the rest keeps its alignment.

`internal/httpapi/integrations.go` (new):

```go
package httpapi

import (
	"net/http"

	"github.com/jameynakama/whatnow/internal/integrations"
)

func (s *Server) listIntegrations(w http.ResponseWriter, r *http.Request) {
	views, err := s.Integrations.Views(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) saveIntegration(w http.ResponseWriter, r *http.Request) {
	var u integrations.Update
	if err := decode(r, &u); err != nil {
		writeError(w, err)
		return
	}
	v, err := s.Integrations.Save(r.Context(), r.PathValue("id"), u)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) enableIntegration(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	v, err := s.Integrations.SetEnabled(r.Context(), r.PathValue("id"), body.Enabled)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// pullIntegration brings in today's events. A source that failed is part of a normal result, so
// it comes back as 200 with that source's error.
func (s *Server) pullIntegration(w http.ResponseWriter, r *http.Request) {
	res, err := s.Integrations.Pull(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) skipIntegration(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key     string `json:"key"`
		Title   string `json:"title"`
		EventID int64  `json:"event_id"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	batch, err := s.Integrations.Skip(r.Context(), r.PathValue("id"), integrations.Skip{Key: body.Key, Title: body.Title}, body.EventID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Mutation{BatchID: batch})
}

func (s *Server) unskipIntegration(w http.ResponseWriter, r *http.Request) {
	v, err := s.Integrations.Unskip(r.Context(), r.PathValue("id"), r.PathValue("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
```

`internal/httpapi/server.go`, apply:

```diff
@@ -17,6 +17,7 @@ import (
 
 	"github.com/jameynakama/whatnow/internal/agent"
 	"github.com/jameynakama/whatnow/internal/domain"
+	"github.com/jameynakama/whatnow/internal/integrations"
 	"github.com/jameynakama/whatnow/internal/llm"
 	"github.com/jameynakama/whatnow/internal/planner"
 	"github.com/jameynakama/whatnow/internal/service"
@@ -42,7 +43,8 @@ type Deps struct {
 	Planner   *planner.Planner
 	Agent     *agent.Agent
 	// Static serves the web client for every GET outside the API.
-	Static http.Handler
+	Static       http.Handler
+	Integrations *integrations.Runner
 }
 
 type Server struct {
@@ -88,6 +90,12 @@ func New(d Deps) http.Handler {
 	mux.HandleFunc("PUT /api/v1/settings/keys/{provider}", s.putKey)
 	mux.HandleFunc("DELETE /api/v1/settings/keys/{provider}", s.deleteKey)
 	mux.HandleFunc("POST /api/v1/settings/test", s.testConnection)
+	mux.HandleFunc("GET /api/v1/integrations", s.listIntegrations)
+	mux.HandleFunc("PUT /api/v1/integrations/{id}", s.saveIntegration)
+	mux.HandleFunc("PUT /api/v1/integrations/{id}/enabled", s.enableIntegration)
+	mux.HandleFunc("POST /api/v1/integrations/{id}/pull", s.pullIntegration)
+	mux.HandleFunc("POST /api/v1/integrations/{id}/skipped", s.skipIntegration)
+	mux.HandleFunc("DELETE /api/v1/integrations/{id}/skipped/{key}", s.unskipIntegration)
 	mux.HandleFunc("DELETE /api/v1/data", s.deleteData)
 	mux.HandleFunc("GET /api/v1/clock", s.clock)
 	mux.Handle("GET /", d.Static)
```

`internal/httpapi/settings.go`, apply:

```diff
@@ -6,6 +6,7 @@ import (
 	"net/http"
 	"slices"
 
+	"github.com/jameynakama/whatnow/internal/integrations"
 	"github.com/jameynakama/whatnow/internal/llm"
 	"github.com/jameynakama/whatnow/internal/settings"
 	"github.com/jameynakama/whatnow/internal/store"
@@ -111,10 +112,16 @@ func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
 	writeJSON(w, http.StatusOK, ConnectionResult{OK: true})
 }
 
-// deleteData wipes every row and both saved keys, then tells every open tab to start over.
-// Environment keys can't be removed and are found again on the next setup. An unreachable
-// keychain is logged rather than failed, as in settings.Key: the rows are already gone.
+// deleteData wipes every row, both saved keys, and the integrations' secrets, then tells every
+// open tab to start over. Environment keys can't be removed and are found again on the next setup.
+// An unreachable keychain is logged rather than failed, as in settings.Key: the rows are already
+// gone. The secrets are listed first, since the rows say which there are.
 func (s *Server) deleteData(w http.ResponseWriter, r *http.Request) {
+	secrets, err := s.Integrations.Secrets(r.Context())
+	if err != nil {
+		writeError(w, err)
+		return
+	}
 	if err := store.Wipe(r.Context(), s.DB); err != nil {
 		writeError(w, err)
 		return
@@ -124,6 +131,9 @@ func (s *Server) deleteData(w http.ResponseWriter, r *http.Request) {
 			log.Printf("delete %s key: %v", p, err)
 		}
 	}
+	if err := integrations.DeleteSecrets(secrets); err != nil {
+		log.Printf("delete integration secrets: %v", err)
+	}
 	s.Hub.Publish(stream.Message{Type: "data.reset"})
 	w.WriteHeader(http.StatusNoContent)
 }
```

`internal/app/app.go`, apply:

```diff
@@ -12,6 +12,8 @@ import (
 
 	"github.com/jameynakama/whatnow/internal/agent"
 	"github.com/jameynakama/whatnow/internal/cleanup"
+	"github.com/jameynakama/whatnow/internal/integrations"
+	"github.com/jameynakama/whatnow/internal/integrations/gcal"
 	"github.com/jameynakama/whatnow/internal/llm"
 	"github.com/jameynakama/whatnow/internal/planner"
 	"github.com/jameynakama/whatnow/internal/service"
@@ -41,6 +43,8 @@ type App struct {
 	Agent   *agent.Agent
 	Now     func() time.Time
 	Loc     *time.Location
+	// Integrations pull outside work in, such as calendar events, when the user asks.
+	Integrations *integrations.Runner
 
 	stop    context.CancelFunc
 	running sync.WaitGroup
@@ -85,6 +89,9 @@ func Open(o Options) (*App, error) {
 		Agent:   agent.New(svc, hub, plans, o.Loc, o.Now),
 		Now:     o.Now,
 		Loc:     o.Loc,
+		Integrations: integrations.NewRunner(db, svc, hub, o.Loc, o.Now,
+			gcal.New(nil),
+		),
 	}
 
 	ctx, stop := context.WithCancel(context.Background())
```

`cmd/whatnow/main.go`, apply:

```diff
@@ -81,17 +81,18 @@ func serve(addr, dbPath string) error {
 
 func newServer(a *app.App, addr string) *http.Server {
 	handler := httpapi.New(httpapi.Deps{
-		Service:   a.Service,
-		Hub:       a.Hub,
-		DB:        a.DB,
-		Loc:       a.Loc,
-		Now:       a.Now,
-		OnCapture: a.Cleanup.Enqueue,
-		Clean:     a.Cleanup.Clean,
-		NewClient: newClient,
-		Planner:   a.Planner,
-		Agent:     a.Agent,
-		Static:    web.Handler(),
+		Service:      a.Service,
+		Hub:          a.Hub,
+		DB:           a.DB,
+		Loc:          a.Loc,
+		Now:          a.Now,
+		OnCapture:    a.Cleanup.Enqueue,
+		Clean:        a.Cleanup.Clean,
+		NewClient:    newClient,
+		Planner:      a.Planner,
+		Agent:        a.Agent,
+		Integrations: a.Integrations,
+		Static:       web.Handler(),
 	})
 	// No WriteTimeout: it would cut off the long-lived SSE stream.
 	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
```

- [ ] **Step 4: Run the checks**

Run: `go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/integrations.go internal/httpapi/server.go internal/httpapi/settings.go internal/app/app.go cmd/whatnow/main.go internal/httpapi/helpers_test.go internal/httpapi/integrations_test.go
git commit -m "Serve integrations over the API"
```

---

### Task 5: Set up integrations in settings, and pull from the morning

**Files:**
- Modify: `web/src/lib/api.ts`
- Create: `web/src/lib/integrations.ts`
- Modify: `web/src/lib/welcome.ts`
- Create: `web/src/lib/components/PullResults.svelte`
- Create: `web/src/lib/components/MorningIntegrations.svelte`
- Create: `web/src/lib/components/IntegrationCard.svelte`
- Modify: `web/src/lib/components/DangerZone.svelte`
- Create: `web/src/routes/settings/+layout.svelte`
- Modify: `web/src/routes/settings/+page.svelte`
- Create: `web/src/routes/settings/integrations/+page.svelte`
- Modify: `web/src/routes/(ritual)/morning/+page.svelte`
- Create: `web/src/lib/integrations.test.ts`
- Modify: `web/src/lib/welcome.test.ts`
- Create: `web/e2e/integrations.spec.ts`

**Interfaces:**
- Consumes: Task 4's routes.
- Produces, in `api.ts`: the `Field`, `Integration`, `IntegrationRow`, `IntegrationUpdate`, `Skip`, `PullResult`, `PullSource`, and `PulledEvent` types, and `api.integrations`, `saveIntegration`, `enableIntegration`, `pull`, `skip`, `unskip`.
- Produces, in `integrations.ts`: `formFrom(i): IntegrationUpdate`, `newRow(list): {id, values}`, `isOn(i): boolean`, `withoutEvent(result, id): PullResult`.
- Produces: `PullResults.svelte` (props `id`, bindable `result`, `retry`), used by the morning row and the settings card.

- [ ] **Step 1: Write the failing tests**

Vitest covers the logic module. The Playwright flow serves a feed dated today from Node and drives settings, the morning, and settings again.

`web/src/lib/integrations.test.ts` (new):

```ts
import { describe, expect, it } from 'vitest';
import type { Field, Integration, PullResult } from './api';
import { formFrom, isOn, newRow, withoutEvent } from './integrations';

const calendars: Field = {
	key: 'calendars',
	label: 'Calendars',
	kind: 'list',
	required: true,
	fields: [
		{ key: 'label', label: 'Name', kind: 'text', required: true },
		{ key: 'url', label: 'Secret address', kind: 'secret', required: true }
	]
};

const gcal: Integration = {
	id: 'gcal',
	name: 'Google Calendar',
	fields: [{ key: 'team', label: 'Team', kind: 'text', required: false }, { key: 'token', label: 'Token', kind: 'secret', required: false }, calendars],
	enabled: true,
	ready: true,
	values: { team: 'Platform' },
	secrets: { token: true },
	lists: { calendars: [{ id: 'r1', values: { label: 'Work' }, secrets: { url: true } }] },
	skipped: []
};

describe('formFrom', () => {
	it('Should copy text and leave every secret blank, so saving keeps them', () => {
		expect(formFrom(gcal)).toEqual({
			values: { team: 'Platform', token: '' },
			lists: { calendars: [{ id: 'r1', values: { label: 'Work', url: '' } }] }
		});
	});

	it('Should start a never-saved integration empty', () => {
		expect(formFrom({ ...gcal, values: {}, lists: {} })).toEqual({
			values: { team: '', token: '' },
			lists: { calendars: [] }
		});
	});
});

describe('newRow', () => {
	it('Should have no id and a blank value for every column', () => {
		expect(newRow(calendars)).toEqual({ id: '', values: { label: '', url: '' } });
	});
});

describe('isOn', () => {
	it.each([
		[true, true, true],
		[true, false, false],
		[false, true, false]
	])('Should be on only when enabled (%s) and ready (%s)', (enabled, ready, want) => {
		expect(isOn({ ...gcal, enabled, ready })).toBe(want);
	});
});

describe('withoutEvent', () => {
	it('Should drop the event from whichever source has it, keeping the rest', () => {
		const ev = (id: number) => ({ id, title: `e${id}`, starts_at: '', ends_at: null, series: '' });
		const result: PullResult = {
			batch_id: 1,
			sources: [
				{ label: 'Work', events: [ev(1), ev(2)] },
				{ label: 'Personal', error: "couldn't reach Google", events: [] }
			]
		};
		expect(withoutEvent(result, 2).sources).toEqual([
			{ label: 'Work', events: [ev(1)] },
			{ label: 'Personal', error: "couldn't reach Google", events: [] }
		]);
	});
});
```

`web/src/lib/welcome.test.ts`, apply:

```diff
@@ -21,7 +21,8 @@ describe('welcomeDue', () => {
 		['setup was skipped', { ...fresh, onboarded: true }, '/', false],
 		['a provider is saved', { ...fresh, provider: 'anthropic' }, '/', false],
 		['already in onboarding', fresh, '/welcome', false],
-		['in Settings', fresh, '/settings', false]
+		['in Settings', fresh, '/settings', false],
+		["in Settings' integrations", fresh, '/settings/integrations', false]
 	] as const)('Should know whether to onboard when %s', (_, s, path, want) => {
 		expect(welcomeDue(s, path)).toBe(want);
 	});
```

`web/e2e/integrations.spec.ts` (new):

```ts
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { expect, setUp, test } from './fixtures';

/** A date as an iCal local time, such as 20261005T150000. */
function icalTime(d: Date, hour: number): string {
	const pad = (n: number) => String(n).padStart(2, '0');
	return `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}T${pad(hour)}0000`;
}

/** Today's standup, and a daily blockout that started last week, in floating local time. */
function feed(): string {
	const today = new Date();
	const lastWeek = new Date(today.getTime() - 7 * 86_400_000);
	const event = (lines: string[]) => ['BEGIN:VEVENT', 'DTSTAMP:20260101T000000Z', ...lines, 'END:VEVENT'];
	return [
		'BEGIN:VCALENDAR',
		'VERSION:2.0',
		'X-WR-CALNAME:me@example.com',
		...event(['UID:standup', 'SUMMARY:Standup', `DTSTART:${icalTime(today, 10)}`, `DTEND:${icalTime(today, 11)}`]),
		...event(['UID:blockout', 'SUMMARY:Blockout', 'RRULE:FREQ=DAILY', `DTSTART:${icalTime(lastWeek, 15)}`, `DTEND:${icalTime(lastWeek, 16)}`]),
		'END:VCALENDAR',
		''
	].join('\r\n');
}

test('a calendar pulls into the morning, and always skip keeps a series out', async ({ page, request }) => {
	const server = createServer((_, res) => res.end(feed()));
	await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
	const url = `http://127.0.0.1:${(server.address() as AddressInfo).port}/basic.ics`;
	try {
		await setUp(request);

		await page.goto('/settings/integrations');
		await page.getByRole('button', { name: /Google Calendar/ }).click();
		await page.getByRole('button', { name: '+ Add' }).click();
		await page.getByLabel('Name').fill('Work');
		await page.getByLabel('Secret address').fill(url);
		await page.getByRole('button', { name: 'Save' }).click();
		await expect(page.getByPlaceholder('•••••••• saved')).toBeVisible();
		await page.getByRole('switch').check();
		await expect(page.getByRole('button', { name: 'Pull today' })).toBeEnabled();

		await page.goto('/morning');
		const chip = page.getByRole('button', { name: '↻ Google Calendar' });
		await chip.click();
		await expect(page.getByText('Work · 2')).toBeVisible();
		await page.getByRole('button', { name: 'Remove Blockout' }).click();
		await page.getByRole('button', { name: 'Always skip' }).click();
		await expect(page.getByText('Work · 1')).toBeVisible();

		await chip.click();
		await expect(page.getByText('Standup', { exact: true })).toBeVisible();
		await expect(page.getByText('Work · 1')).toBeVisible();

		await page.goto('/settings/integrations');
		await page.getByRole('button', { name: /Google Calendar/ }).click();
		await expect(page.getByRole('button', { name: 'bring back' })).toBeVisible();
		await expect(page.getByText('Blockout')).toBeVisible();
	} finally {
		server.close();
	}
});
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/lib/integrations.test.ts src/lib/welcome.test.ts`
Expected: FAIL: `./integrations` can't be resolved, and `/settings/integrations` onboards a fresh install.

- [ ] **Step 3: Implement**

The settings route gets a layout with the sidebar; the General page loses its own `<main>`, back link, and heading, which move to the layout. The morning's brain-dump step gets `<MorningIntegrations />` under the textarea. Watch two layout traps the screenshots caught: the card's generic `label { flex-direction: column }` rule stacks the switch's text under it unless `.switch` sets `row`, and the skip question needs its own line (`flex-basis: 100%`) or it covers the title.

`web/src/lib/api.ts`, apply:

```diff
@@ -193,6 +193,71 @@ export interface ConnectionResult {
 	error?: string;
 }
 
+export type FieldKind = 'text' | 'secret' | 'list';
+
+/** A settings field an integration declares. Only a list has fields, its columns. */
+export interface Field {
+	key: string;
+	label: string;
+	kind: FieldKind;
+	help?: string;
+	required: boolean;
+	fields?: Field[];
+}
+
+/** A list row. Secrets show only whether they're set. */
+export interface IntegrationRow {
+	id: string;
+	values: Record<string, string>;
+	secrets: Record<string, boolean>;
+}
+
+/** A series, or any pulled item, the user chose never to see. */
+export interface Skip {
+	key: string;
+	title: string;
+}
+
+export interface Integration {
+	id: string;
+	name: string;
+	fields: Field[];
+	enabled: boolean;
+	/** Every required field is filled, so it can pull once enabled. */
+	ready: boolean;
+	values: Record<string, string>;
+	secrets: Record<string, boolean>;
+	lists: Record<string, IntegrationRow[]>;
+	skipped: Skip[];
+}
+
+/** What saving an integration sends. A blank secret keeps the saved one; a row with no id is new. */
+export interface IntegrationUpdate {
+	values: Record<string, string>;
+	lists: Record<string, { id: string; values: Record<string, string> }[]>;
+}
+
+export interface PulledEvent {
+	id: number;
+	title: string;
+	starts_at: string;
+	ends_at: string | null;
+	/** Set on an occurrence of a recurring event, which removing offers to skip for good. */
+	series: string;
+}
+
+/** What one source, such as a calendar, pulled. A failed source has an error and no events. */
+export interface PullSource {
+	label: string;
+	error?: string;
+	events: PulledEvent[];
+}
+
+export interface PullResult {
+	batch_id: number;
+	sources: PullSource[];
+}
+
 export class ApiError extends Error {
 	constructor(
 		readonly status: number,
@@ -253,6 +318,15 @@ export const api = {
 		request<void>('PUT', `/settings/keys/${provider}`, { key }),
 	testConnection: () => request<ConnectionResult>('POST', '/settings/test'),
 	deleteData: () => request<void>('DELETE', '/data'),
+	integrations: () => request<Integration[]>('GET', '/integrations'),
+	saveIntegration: (id: string, u: IntegrationUpdate) => request<Integration>('PUT', `/integrations/${id}`, u),
+	enableIntegration: (id: string, enabled: boolean) =>
+		request<Integration>('PUT', `/integrations/${id}/enabled`, { enabled }),
+	pull: (id: string) => request<PullResult>('POST', `/integrations/${id}/pull`),
+	skip: (id: string, key: string, title: string, event_id: number) =>
+		request<Mutation>('POST', `/integrations/${id}/skipped`, { key, title, event_id }),
+	unskip: (id: string, key: string) =>
+		request<Integration>('DELETE', `/integrations/${id}/skipped/${encodeURIComponent(key)}`),
 	clock: () => request<{ now: string }>('GET', '/clock')
 };
```

`web/src/lib/integrations.ts` (new):

```ts
import type { Field, Integration, IntegrationUpdate, PullResult } from './api';

/**
 * A settings form's copy of an integration's fields, in the shape saving sends. Secrets start
 * blank, so saving without typing one keeps it.
 */
export function formFrom(i: Integration): IntegrationUpdate {
	const values: Record<string, string> = {};
	const lists: IntegrationUpdate['lists'] = {};
	for (const f of i.fields) {
		if (f.kind === 'text') values[f.key] = i.values[f.key] ?? '';
		if (f.kind === 'secret') values[f.key] = '';
		if (f.kind === 'list') {
			lists[f.key] = (i.lists[f.key] ?? []).map((row) => ({ id: row.id, values: blankSecrets(f, row.values) }));
		}
	}
	return { values, lists };
}

/** A new row for a list field: every column blank and no id, which the server gives it. */
export function newRow(list: Field): { id: string; values: Record<string, string> } {
	return { id: '', values: blankSecrets(list, {}) };
}

function blankSecrets(list: Field, saved: Record<string, string>): Record<string, string> {
	const values: Record<string, string> = {};
	for (const c of list.fields ?? []) values[c.key] = c.kind === 'secret' ? '' : (saved[c.key] ?? '');
	return values;
}

/** Whether an integration can pull: it's on, and its required fields are filled. */
export function isOn(i: Integration): boolean {
	return i.enabled && i.ready;
}

/** The result without one event, after it was removed or its series skipped. */
export function withoutEvent(result: PullResult, id: number): PullResult {
	return {
		...result,
		sources: result.sources.map((s) => ({ ...s, events: s.events.filter((e) => e.id !== id) }))
	};
}
```

`web/src/lib/welcome.ts`, apply:

```diff
@@ -5,5 +5,5 @@ import type { Settings } from './api';
  * nothing is set up. Onboarding and Settings are where setup happens, so they never redirect.
  */
 export function welcomeDue(s: Settings, path: string): boolean {
-	return !s.onboarded && s.provider === '' && path !== '/welcome' && path !== '/settings';
+	return !s.onboarded && s.provider === '' && path !== '/welcome' && !path.startsWith('/settings');
 }
```

`web/src/lib/components/PullResults.svelte` (new):

```svelte
<script lang="ts">
	import { api, type PullResult, type PulledEvent } from '#lib/api.ts';
	import { withoutEvent } from '#lib/integrations.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	interface Props {
		/** The integration the events came from. */
		id: string;
		result: PullResult;
		/** Pulls again, for a source that failed. */
		retry: () => void;
	}
	let { id, result = $bindable(), retry }: Props = $props();

	/** The recurring event whose × is asking whether to skip just today or the whole series. */
	let asking = $state<number | null>(null);

	const time = (d: string) => new Date(d).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });

	async function removeToday(e: PulledEvent) {
		asking = null;
		if (await toasts.track(`Removed: ${e.title}`, () => api.deleteEvent(e.id))) result = withoutEvent(result, e.id);
	}

	async function skip(e: PulledEvent) {
		asking = null;
		if (await toasts.track(`Skipping ${e.title} from now on`, () => api.skip(id, e.series, e.title, e.id))) {
			result = withoutEvent(result, e.id);
		}
	}
</script>

<div class="sources">
	{#each result.sources as source, i (i)}
		<section>
			<h3 class="label">{source.error ? source.label : `${source.label} · ${source.events.length}`}</h3>
			{#if source.error}
				<p class="bad">{source.error} <button class="link" onclick={retry}>Retry</button></p>
			{:else if source.events.length === 0}
				<p class="muted">Nothing today.</p>
			{:else}
				<ul>
					{#each source.events as e (e.id)}
						<li>
							<span class="at">{time(e.starts_at)}</span>
							<span class="title">{e.title}</span>
							{#if asking === e.id}
								<span class="ask">
									<button class="link" onclick={() => removeToday(e)}>Just today</button>
									<button class="link" onclick={() => skip(e)}>Always skip</button>
									<button class="link muted" onclick={() => (asking = null)}>Cancel</button>
								</span>
							{:else}
								<button
									class="remove"
									aria-label="Remove {e.title}"
									onclick={() => (e.series ? (asking = e.id) : removeToday(e))}>×</button
								>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	{/each}
</div>

<style>
	.sources {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
		gap: 12px;
	}
	h3 {
		margin: 0 0 4px;
	}
	ul {
		list-style: none;
		margin: 0;
		padding: 0;
	}
	li {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0 8px;
		padding: 2px 0;
	}
	.at {
		font-weight: var(--heavy);
		white-space: nowrap;
	}
	.title {
		flex: 1;
		min-width: 0;
	}
	/* Its own line under the event, so the title stays readable. */
	.ask {
		flex-basis: 100%;
		display: flex;
		gap: 12px;
		flex-wrap: wrap;
	}
	.remove {
		background: none;
		border: none;
		padding: 0 4px;
		font-weight: var(--heavy);
		color: var(--ink-soft);
	}
	.link {
		background: none;
		border: none;
		padding: 0;
		color: var(--primary);
		font-weight: var(--heavy);
	}
	.link.muted {
		color: var(--ink-soft);
	}
	p {
		margin: 0;
	}
	.bad {
		color: var(--danger);
		font-weight: var(--heavy);
	}
</style>
```

`web/src/lib/components/MorningIntegrations.svelte` (new):

```svelte
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Integration, type PullResult } from '#lib/api.ts';
	import { isOn } from '#lib/integrations.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import PullResults from './PullResults.svelte';

	/** The integrations that can pull. With none, the row doesn't show. */
	let on = $state<Integration[]>([]);
	let results = $state<Record<string, PullResult>>({});
	let pulling = $state<Record<string, boolean>>({});

	onMount(() => {
		api.integrations().then(
			(all) => (on = all.filter(isOn)),
			(err) => toasts.error(err)
		);
	});

	async function pull(id: string) {
		pulling[id] = true;
		try {
			results[id] = await api.pull(id);
		} catch (err) {
			toasts.error(err);
		} finally {
			pulling[id] = false;
		}
	}
</script>

{#if on.length}
	<section class="integrations" aria-label="Integrations">
		<h2 class="label">Integrations</h2>
		{#each on as i (i.id)}
			<button class="chip" disabled={pulling[i.id]} onclick={() => pull(i.id)}>
				{pulling[i.id] ? 'Pulling…' : `↻ ${i.name}`}
			</button>
			{#if results[i.id]}
				<PullResults id={i.id} bind:result={results[i.id]} retry={() => pull(i.id)} />
			{/if}
		{/each}
	</section>
{/if}

<style>
	.integrations {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 8px;
	}
	h2 {
		margin: 0;
	}
	.integrations :global(.sources) {
		width: 100%;
	}
	.chip {
		background: var(--card);
		cursor: pointer;
		font-size: 15px;
		padding: 4px 14px;
	}
</style>
```

`web/src/lib/components/IntegrationCard.svelte` (new):

```svelte
<script lang="ts">
	import { api, type Integration, type PullResult } from '#lib/api.ts';
	import { formFrom, isOn, newRow } from '#lib/integrations.ts';
	import { toasts } from '#lib/toasts.svelte.ts';
	import PullResults from './PullResults.svelte';

	interface Props {
		integration: Integration;
	}
	let { integration = $bindable() }: Props = $props();

	let open = $state(false);
	let form = $state(formFrom(integration));
	let saving = $state(false);
	let pulling = $state(false);
	let result = $state<PullResult | null>(null);

	/** Whether a list row's secret column is saved, so a blank input keeps it. */
	const savedSecret = (list: string, rowID: string, column: string) =>
		integration.lists[list]?.find((r) => r.id === rowID)?.secrets[column] ?? false;

	// The toggle saves at once, apart from the fields, so it never saves half-typed edits.
	async function toggle(e: Event & { currentTarget: HTMLInputElement }) {
		const input = e.currentTarget;
		try {
			integration = await api.enableIntegration(integration.id, input.checked);
			if (integration.enabled) open = true;
		} catch (err) {
			toasts.error(err);
			input.checked = integration.enabled;
		}
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		try {
			integration = await api.saveIntegration(integration.id, form);
			form = formFrom(integration);
		} catch (err) {
			toasts.error(err);
		} finally {
			saving = false;
		}
	}

	async function pull() {
		pulling = true;
		try {
			result = await api.pull(integration.id);
		} catch (err) {
			toasts.error(err);
		} finally {
			pulling = false;
		}
	}

	async function unskip(key: string) {
		try {
			integration = await api.unskip(integration.id, key);
		} catch (err) {
			toasts.error(err);
		}
	}
</script>

<section class="sticker card">
	<header>
		<button class="disclose" aria-expanded={open} onclick={() => (open = !open)}>
			{open ? '▾' : '▸'}
			{integration.name}
		</button>
		<label class="switch">
			<input type="checkbox" role="switch" checked={integration.enabled} onchange={toggle} />
			{integration.enabled ? 'On' : 'Off'}
		</label>
	</header>
	{#if open}
		<form onsubmit={save}>
			{#each integration.fields as f (f.key)}
				{#if f.kind === 'list'}
					<fieldset>
						<legend class="label">{f.label}</legend>
						{#each form.lists[f.key] as row, r (r)}
							<div class="row">
								{#each f.fields ?? [] as c (c.key)}
									{@const saved = c.kind === 'secret' && savedSecret(f.key, row.id, c.key)}
									<input
										class="field"
										class:secret={c.kind === 'secret'}
										type={c.kind === 'secret' ? 'password' : 'text'}
										autocomplete="off"
										aria-label={c.label}
										placeholder={saved ? '•••••••• saved' : c.label}
										bind:value={row.values[c.key]}
									/>
								{/each}
								<button type="button" class="link" onclick={() => form.lists[f.key].splice(r, 1)}>remove</button>
							</div>
						{/each}
						<button type="button" class="link" onclick={() => form.lists[f.key].push(newRow(f))}>+ Add</button>
						{#if f.help}<p class="muted hint">{f.help}</p>{/if}
					</fieldset>
				{:else}
					<label>
						<span class="label">
							{f.label}
							{#if f.kind === 'secret'}<span class="muted">({integration.secrets[f.key] ? 'saved' : 'not set'})</span>{/if}
						</span>
						<input
							class="field"
							type={f.kind === 'secret' ? 'password' : 'text'}
							autocomplete="off"
							placeholder={f.kind === 'secret' ? 'Leave blank to keep' : ''}
							bind:value={form.values[f.key]}
						/>
						{#if f.help}<span class="muted hint">{f.help}</span>{/if}
					</label>
				{/if}
			{/each}
			{#if integration.skipped.length}
				<div>
					<span class="label">Skipped</span>
					<ul class="skipped">
						{#each integration.skipped as s (s.key)}
							<li>{s.title} <button type="button" class="link" onclick={() => unskip(s.key)}>bring back</button></li>
						{/each}
					</ul>
				</div>
			{/if}
			<div class="actions">
				<button class="btn primary" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
				<button type="button" class="btn" disabled={!isOn(integration) || pulling} onclick={pull}>
					{pulling ? 'Pulling…' : 'Pull today'}
				</button>
			</div>
		</form>
		{#if result}<PullResults id={integration.id} bind:result retry={pull} />{/if}
	{/if}
</section>

<style>
	.card {
		padding: 14px 20px;
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
	header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
	}
	.disclose {
		background: none;
		border: none;
		padding: 0;
		font-size: 17px;
		font-weight: var(--heavy);
	}
	.switch {
		display: flex;
		flex-direction: row;
		align-items: center;
		gap: 6px;
		font-weight: var(--heavy);
	}
	.switch input {
		appearance: none;
		width: 38px;
		height: 22px;
		border: var(--line);
		border-radius: var(--pill);
		background: var(--paper-deep);
		position: relative;
		cursor: pointer;
		margin: 0;
	}
	.switch input::after {
		content: '';
		position: absolute;
		top: 2px;
		left: 2px;
		width: 14px;
		height: 14px;
		border-radius: 50%;
		background: var(--ink);
		transition: left 0.15s;
	}
	.switch input:checked {
		background: var(--primary);
	}
	.switch input:checked::after {
		left: 18px;
		background: var(--on-primary);
	}
	form {
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
	fieldset {
		border: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 8px;
	}
	legend {
		padding: 0;
		margin-bottom: 6px;
	}
	.row {
		display: grid;
		grid-template-columns: minmax(80px, 1fr) 2fr auto;
		align-items: center;
		gap: 8px;
		width: 100%;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.hint {
		font-size: 13px;
		margin: 0;
	}
	.link {
		background: none;
		border: none;
		padding: 0;
		color: var(--primary);
		font-weight: var(--heavy);
	}
	.skipped {
		margin: 4px 0 0;
		padding-left: 18px;
	}
	.actions {
		display: flex;
		gap: 10px;
		flex-wrap: wrap;
	}
</style>
```

`web/src/lib/components/DangerZone.svelte`, apply:

```diff
@@ -21,8 +21,8 @@
 <section class="sticker danger">
 	<h2 class="label">Danger zone</h2>
 	<p>
-		Deletes every task, event, day, plan, chat, and summary, your settings, and the saved keys. There's
-		no undo.
+		Deletes every task, event, day, plan, chat, and summary, your settings, the saved keys, and your
+		integrations' saved addresses. There's no undo.
 	</p>
 	<label>
 		<span class="muted">Type <strong>{phrase}</strong> to confirm</span>
```

`web/src/routes/settings/+layout.svelte` (new):

```svelte
<script lang="ts">
	import { page } from '$app/state';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	const sections = [
		{ href: '/settings', label: 'General' },
		{ href: '/settings/integrations', label: 'Integrations' }
	];
</script>

<main>
	<a href="/">← Today</a>
	<h1>Settings</h1>
	<div class="split">
		<nav aria-label="Settings sections">
			{#each sections as s (s.href)}
				<a href={s.href} class:on={page.url.pathname === s.href} aria-current={page.url.pathname === s.href ? 'page' : undefined}>
					{s.label}
				</a>
			{/each}
		</nav>
		<div class="content">
			{@render children()}
		</div>
	</div>
</main>

<style>
	main {
		max-width: 780px;
		margin: 0 auto;
		padding: 16px;
		display: flex;
		flex-direction: column;
		gap: 16px;
	}
	h1 {
		margin: 0;
		font-weight: var(--heavy);
	}
	.split {
		display: grid;
		grid-template-columns: 150px minmax(0, 1fr);
		gap: 20px;
		align-items: start;
	}
	nav {
		display: flex;
		flex-direction: column;
		gap: 4px;
		position: sticky;
		top: 16px;
	}
	nav a {
		color: var(--ink);
		text-decoration: none;
		padding: 6px 10px;
		border: 2px solid transparent;
		border-radius: var(--radius-sm);
	}
	nav a.on {
		background: var(--card);
		border: var(--line);
		box-shadow: var(--shadow-sm);
	}
	.content {
		display: flex;
		flex-direction: column;
		gap: 16px;
	}
	/* On a phone the sidebar becomes tabs across the top. */
	@media (max-width: 640px) {
		.split {
			grid-template-columns: minmax(0, 1fr);
		}
		nav {
			flex-direction: row;
			position: static;
		}
	}
</style>
```

`web/src/routes/settings/+page.svelte`, apply:

```diff
@@ -69,10 +69,7 @@
 	}
 </script>
 
-<main>
-	<a href="/">← Today</a>
-	<h1>Settings</h1>
-	{#if view}
+{#if view}
 		<form class="sticker" onsubmit={save}>
 			<h2 class="label">Connection</h2>
 			<label>
@@ -149,21 +146,8 @@
 	{:else}
 		<p class="muted">Loading…</p>
 	{/if}
-</main>
 
 <style>
-	main {
-		max-width: 560px;
-		margin: 0 auto;
-		padding: 16px;
-		display: flex;
-		flex-direction: column;
-		gap: 16px;
-	}
-	h1 {
-		margin: 0;
-		font-weight: var(--heavy);
-	}
 	h2 {
 		margin: 10px 0 0;
 		font-size: 14px;
```

`web/src/routes/settings/integrations/+page.svelte` (new):

```svelte
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Integration } from '#lib/api.ts';
	import IntegrationCard from '#lib/components/IntegrationCard.svelte';
	import { toasts } from '#lib/toasts.svelte.ts';

	let list = $state<Integration[] | null>(null);

	onMount(() => {
		api.integrations().then(
			(l) => (list = l),
			(err) => toasts.error(err)
		);
	});
</script>

{#if list}
	{#each list as integration, i (integration.id)}
		<IntegrationCard bind:integration={list[i]} />
	{/each}
{:else}
	<p class="muted">Loading…</p>
{/if}
```

`web/src/routes/(ritual)/morning/+page.svelte`, apply:

```diff
@@ -3,6 +3,7 @@
 	import { goto } from '$app/navigation';
 	import { api, ApiError, paceLabels, type Review, type Tier } from '#lib/api.ts';
 	import Dots from '#lib/components/Dots.svelte';
+	import MorningIntegrations from '#lib/components/MorningIntegrations.svelte';
 	import Working from '#lib/components/Working.svelte';
 	import Tagline from '#lib/components/Tagline.svelte';
 	import TriageList from '#lib/components/TriageList.svelte';
@@ -98,6 +99,7 @@
 	<h1>What's on your plate?</h1>
 	<p class="muted center">Dump it all here, messy is fine. I'll sort it into tasks before planning.</p>
 	<textarea class="field" rows="8" bind:value={dump} aria-label="What's on your plate"></textarea>
+	<MorningIntegrations />
 	<div class="row">
 		{#if reconciling}<button class="btn" onclick={() => (step = 'reconcile')}>← Back</button>{/if}
 		<button class="link" onclick={() => ((dump = ''), (step = 'pace'))}>Skip</button>
```

- [ ] **Step 4: Run the checks**

Run: `cd web && npm test && npm run check`, then `just e2e`.
Expected: everything passes, the new flow included. Then look at `/settings/integrations` and `/morning` at desktop and phone widths with a calendar set up, including a failed calendar and the skip question open (playwright-core screenshots, as in the environment notes).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/api.ts web/src/lib/integrations.ts web/src/lib/welcome.ts web/src/lib/components/PullResults.svelte web/src/lib/components/MorningIntegrations.svelte web/src/lib/components/IntegrationCard.svelte web/src/lib/components/DangerZone.svelte web/src/routes/settings/+layout.svelte web/src/routes/settings/+page.svelte web/src/routes/settings/integrations/+page.svelte 'web/src/routes/(ritual)/morning/+page.svelte' web/src/lib/integrations.test.ts web/src/lib/welcome.test.ts web/e2e/integrations.spec.ts
git commit -m "Set up integrations in settings and pull from the morning"
```

---

### Task 6: Pull the calendar in the TUI morning

**Files:**
- Create: `internal/tui/calendar.go`
- Modify: `internal/tui/morning.go`
- Modify: `internal/tui/model.go`
- Create: `internal/tui/calendar_test.go`
- Create: `internal/tui/testdata/morning-calendar-80.golden`

**Interfaces:**
- Consumes: `app.App.Integrations` from Task 4.
- Produces: `stepCalendar` between `stepDump` and `stepPace`; `morning.calendar`; the messages `integrationsMsg`, `pulledMsg`, `pullFailedMsg`, and `removedMsg`, routed to `onCalendarMsg`; `afterDump()`, which the dump step's `tab` and `esc` now call instead of `toPace()`.

- [ ] **Step 1: Write the failing tests**

`fixture.calendars` sets up a Work calendar serving a standup and a daily blockout, and a Personal one that's gone. The golden file is written with `-update` once the view looks right.

`internal/tui/calendar_test.go` (new):

```go
package tui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/go-cmp/cmp"
	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/integrations"
)

// calendarFeed has the fixture day's standup at 14:00 and a daily blockout at 15:00, both UTC.
const calendarFeed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" +
	"BEGIN:VEVENT\r\nUID:standup\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Standup\r\nDTSTART:20261006T140000Z\r\nDTEND:20261006T141500Z\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:blockout\r\nDTSTAMP:20261001T000000Z\r\nSUMMARY:Blockout\r\nRRULE:FREQ=DAILY\r\nDTSTART:20261001T150000Z\r\nDTEND:20261001T160000Z\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

// calendars sets up Google Calendar with a Work calendar serving calendarFeed, and a Personal
// one that's gone, and turns it on.
func (f *fixture) calendars(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "gone") {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, calendarFeed)
	}))
	t.Cleanup(feed.Close)
	ctx := context.Background()
	_, err := f.app.Integrations.Save(ctx, "gcal", integrations.Update{Lists: map[string][]integrations.RowUpdate{"calendars": {
		{Values: map[string]string{"label": "Work", "url": feed.URL + "/work.ics"}},
		{Values: map[string]string{"label": "Personal", "url": feed.URL + "/gone.ics"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Integrations.SetEnabled(ctx, "gcal", true); err != nil {
		t.Fatal(err)
	}
}

// todaysEvents lists the fixture day's live event titles.
func (f *fixture) todaysEvents(t *testing.T) []string {
	t.Helper()
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	events, err := f.app.Service.ListEvents(context.Background(), day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, e := range events {
		titles = append(titles, e.Title)
	}
	return titles
}

// calendarAt is a morning on the calendar step with today pulled.
func (f *fixture) calendarAt(t *testing.T) Model {
	t.Helper()
	m := f.morningAt(t)
	m = send(m, m.loadIntegrations()(), key("esc"))
	if m.morning.step != stepCalendar {
		t.Fatalf("step = %d, want the calendar", m.morning.step)
	}
	return send(m, m.pullAll()())
}

func TestTheMorningPullsTheCalendarAndSkipsASeries(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	tm := f.run(t)
	waitFor(t, tm, "to start your day")
	tm.Send(key("enter"))
	waitFor(t, tm, "What's on your plate?")
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	waitFor(t, tm, "Pull today's calendar?")
	tm.Send(key("enter"))
	waitFor(t, tm, "Work · 2 events")
	tm.Send(key("down"))
	tm.Send(key("x"))
	waitFor(t, tm, "t today · a always")
	tm.Send(key("a"))
	waitFor(t, tm, "Skipping Blockout from now on")
	tm.Send(key("enter"))
	waitFor(t, tm, "How hard")
	quit(t, tm)

	if diff := cmp.Diff([]string{"Standup"}, f.todaysEvents(t)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	v, err := f.app.Integrations.View(context.Background(), "gcal")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]integrations.Skip{{Key: "blockout", Title: "Blockout"}}, v.Skipped); diff != "" {
		t.Errorf("skipped (-want +got):\n%s", diff)
	}
}

func TestCalendarView(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := send(f.calendarAt(t), key("down"), key("x"))
	golden(t, "morning-calendar-80", m.View())
}

func TestXOnAOneOffRemovesItWithoutAsking(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := f.calendarAt(t)
	next, cmd := m.Update(key("x"))
	m = next.(Model)
	if m.morning.calendar.asking || cmd == nil {
		t.Fatalf("asking %v, cmd %v; want the standup removed", m.morning.calendar.asking, cmd)
	}
	m = send(m, cmd())
	if diff := cmp.Diff([]string{"Blockout"}, f.todaysEvents(t)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if got := m.morning.calendar.events(); len(got) != 1 || got[0].event.Title != "Blockout" {
		t.Errorf("listed = %+v", got)
	}
}

func TestJustTodayLeavesTheSeries(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := send(f.calendarAt(t), key("down"), key("x"))
	_, cmd := m.Update(key("t"))
	send(m, cmd())
	if diff := cmp.Diff([]string{"Standup"}, f.todaysEvents(t)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if v, _ := f.app.Integrations.View(context.Background(), "gcal"); len(v.Skipped) != 0 {
		t.Errorf("skipped = %+v", v.Skipped)
	}
}

func TestEscCancelsTheSkipQuestion(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := send(f.calendarAt(t), key("down"), key("x"))
	next, cmd := m.Update(key("esc"))
	if m = next.(Model); m.morning.calendar.asking || cmd != nil {
		t.Errorf("asking %v, cmd %v; want nothing removed", m.morning.calendar.asking, cmd)
	}
}

func TestTabSkipsThePull(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := f.morningAt(t)
	m = send(m, m.loadIntegrations()(), key("esc"), tea.KeyMsg{Type: tea.KeyTab})
	if m.morning.step != stepPace || len(f.todaysEvents(t)) != 0 {
		t.Errorf("step %d, events %v; want pace with nothing pulled", m.morning.step, f.todaysEvents(t))
	}
}

func TestAFailedPullStaysOnTheStep(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := f.morningAt(t)
	m = send(m, m.loadIntegrations()(), key("esc"), key("enter"))
	if !m.morning.calendar.pulling {
		t.Fatal("enter didn't start a pull")
	}
	m = send(m, pullFailedMsg{context.DeadlineExceeded})
	if m.morning.step != stepCalendar || m.morning.calendar.pulling || !strings.Contains(m.View(), "Pull today's calendar?") {
		t.Errorf("step %d, pulling %v:\n%s", m.morning.step, m.morning.calendar.pulling, m.View())
	}
}

func TestTheCursorStaysOnTheListAndRPullsAgain(t *testing.T) {
	f := newFixture(t)
	f.calendars(t)
	m := send(f.calendarAt(t), key("up"), key("down"), key("down"), key("down"))
	if m.morning.calendar.at != 1 {
		t.Errorf("cursor = %d, want the last event", m.morning.calendar.at)
	}
	next, cmd := m.Update(key("r"))
	if m = next.(Model); !m.morning.calendar.pulling || cmd == nil {
		t.Fatal("r didn't pull again")
	}
	if m = send(m, key("enter")); m.morning.step != stepCalendar {
		t.Error("a key mid-pull moved on")
	}
}
```

`internal/tui/testdata/morning-calendar-80.golden` (new):

```text
                                                                                
                                                                                
                                                                                
                                                                                
                                                                                
                                                                                
                                                                                
                                                                                
            ╭─────────────────────────────────────────────────────╮             
            │ whatnow · executive function for the rest of us     │█            
            │                                                     │█            
            │ Today's calendar                                    │█            
            │                                                     │█            
            │ Work · 2 events                                     │█            
            │   14:00 Standup                                     │█            
            │ ▸ 15:00 Blockout                                    │█            
            │     t today · a always · esc cancel                 │█            
            │ Personal: Google said 404, check the address        │█            
            │                                                     │█            
            │ ↑↓ move · x remove · r pull again · enter next      │█            
            ╰─────────────────────────────────────────────────────╯█            
             ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/tui/ -run 'Calendar|OneOff|JustToday|Esc|TabSkips|FailedPull|Cursor'`
Expected: FAIL to compile: `stepCalendar`, `loadIntegrations`, and `pullAll` are undefined.

- [ ] **Step 3: Implement**

`internal/tui/calendar.go` (new):

```go
package tui

import (
	"context"
	"log"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jameynakama/whatnow/internal/integrations"
)

// calendar is the morning's step that pulls today's events from the integrations that are on.
type calendar struct {
	on      []integrations.View
	pulling bool
	pulled  []pulledFrom // nil until the first pull
	at      int          // the event under the cursor, counted across every source
	asking  bool         // x on a recurring event is asking whether to skip today or the series
}

type pulledFrom struct {
	integration string
	result      integrations.PullResult
}

// calEvent is a pulled event and the integration it came from.
type calEvent struct {
	integration string
	event       integrations.PulledEvent
}

type (
	integrationsMsg struct{ on []integrations.View }
	pulledMsg       struct{ pulled []pulledFrom }
	pullFailedMsg   struct{ err error }
	removedMsg      struct {
		eventID int64
		toast   string
	}
)

// events lists every pulled event in the order shown.
func (c calendar) events() []calEvent {
	var out []calEvent
	for _, p := range c.pulled {
		for _, s := range p.result.Sources {
			for _, e := range s.Events {
				out = append(out, calEvent{p.integration, e})
			}
		}
	}
	return out
}

// loadIntegrations finds the integrations that can pull. A failure only costs the step, so it's
// logged and the ritual goes on without it.
func (m Model) loadIntegrations() tea.Cmd {
	return func() tea.Msg {
		views, err := m.app.Integrations.Views(m.ctx)
		if err != nil {
			log.Printf("load integrations: %v", err)
		}
		return integrationsMsg{slices.DeleteFunc(views, func(v integrations.View) bool { return !v.On() })}
	}
}

func (m Model) pullAll() tea.Cmd {
	on := m.morning.calendar.on
	return func() tea.Msg {
		var pulled []pulledFrom
		for _, v := range on {
			res, err := m.app.Integrations.Pull(m.ctx, v.ID)
			if err != nil {
				return pullFailedMsg{err}
			}
			pulled = append(pulled, pulledFrom{v.ID, res})
		}
		return pulledMsg{pulled}
	}
}

// removePulled deletes today's occurrence of e, and with skip, keeps its series out for good.
func (m Model) removePulled(e calEvent, skip bool) tea.Cmd {
	return func() tea.Msg {
		var err error
		toast := "Removed: " + e.event.Title
		if skip {
			toast = "Skipping " + e.event.Title + " from now on"
			_, err = m.app.Integrations.Skip(m.ctx, e.integration, integrations.Skip{Key: e.event.Series, Title: e.event.Title}, e.event.ID)
		} else {
			_, err = m.commit(func(ctx context.Context, batch int64) error {
				return m.app.Service.DeleteEvent(ctx, batch, e.event.ID)
			})
		}
		if err != nil {
			return failedMsg{err}
		}
		return removedMsg{eventID: e.event.ID, toast: toast}
	}
}

func (m Model) onCalendarMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	c := &m.morning.calendar
	switch msg := msg.(type) {
	case integrationsMsg:
		c.on = msg.on
	case pulledMsg:
		c.pulling, c.pulled, c.at, c.asking = false, msg.pulled, 0, false
	case pullFailedMsg:
		c.pulling = false
		return m.show(friendly(msg.err), true)
	case removedMsg:
		for i := range c.pulled {
			for j := range c.pulled[i].result.Sources {
				s := &c.pulled[i].result.Sources[j]
				s.Events = slices.DeleteFunc(s.Events, func(e integrations.PulledEvent) bool { return e.ID == msg.eventID })
			}
		}
		c.at = max(min(c.at, len(c.events())-1), 0)
		return m.show(msg.toast, false)
	}
	return m, nil
}

// afterDump goes to the calendar step when an integration can pull, or straight to pace.
func (m Model) afterDump() (tea.Model, tea.Cmd) {
	if len(m.morning.calendar.on) == 0 {
		return m.toPace()
	}
	m.morning.dump.Blur()
	m.morning.step = stepCalendar
	return m, nil
}

func (m Model) onCalendarKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := &m.morning.calendar
	if c.pulling {
		return m, nil
	}
	if c.pulled == nil {
		switch k.String() {
		case "enter":
			c.pulling = true
			return m, tea.Batch(m.pullAll(), m.spin.Tick)
		case "tab":
			return m.toPace()
		}
		return m, nil
	}
	events := c.events()
	if c.asking {
		c.asking = false
		switch k.String() {
		case "t":
			return m, m.removePulled(events[c.at], false)
		case "a":
			return m, m.removePulled(events[c.at], true)
		}
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		c.at = max(c.at-1, 0)
	case "down", "j":
		c.at = max(min(c.at+1, len(events)-1), 0)
	case "x":
		if len(events) == 0 {
			break
		}
		if events[c.at].event.Series != "" {
			c.asking = true
			break
		}
		return m, m.removePulled(events[c.at], false)
	case "r":
		c.pulling = true
		return m, tea.Batch(m.pullAll(), m.spin.Tick)
	case "enter", "tab":
		return m.toPace()
	}
	return m, nil
}

// calendarLines is the calendar step: the offer to pull, then each source's events or error.
func (m Model) calendarLines(w int) (lines []string, hint string) {
	c := m.morning.calendar
	if c.pulling {
		return []string{m.spin.View() + softStyle.Render(" Pulling today's calendar…")}, ""
	}
	if c.pulled == nil {
		return []string{"Pull today's calendar?"}, "enter pull · tab skip"
	}
	lines = []string{"Today's calendar", ""}
	i := 0
	for _, p := range c.pulled {
		for _, s := range p.result.Sources {
			if s.Error != "" {
				lines = append(lines, nowStyle.Render(s.Label+": "+s.Error))
				continue
			}
			lines = append(lines, lipgloss.NewStyle().Bold(true).Render(s.Label+" · "+count(len(s.Events), "event", "events")))
			for _, e := range s.Events {
				lines = append(lines, eventLine(e, i == c.at, m.app.Loc, w))
				if i == c.at && c.asking {
					lines = append(lines, cursorStyle.Render("    t today · a always · esc cancel"))
				}
				i++
			}
		}
	}
	return lines, "↑↓ move · x remove · r pull again · enter next"
}

func eventLine(e integrations.PulledEvent, current bool, loc *time.Location, w int) string {
	mark, title := "  ", e.Title
	if current {
		mark, title = cursorStyle.Render("▸ "), lipgloss.NewStyle().Bold(true).Render(title)
	}
	at := e.StartsAt.In(loc).Format("15:04")
	room := max(w-len(at)-3, 1)
	return mark + at + " " + lipgloss.NewStyle().MaxWidth(room).Render(title)
}
```

`internal/tui/morning.go`, apply:

```diff
@@ -26,6 +26,7 @@ const (
 	stepGreet
 	stepReconcile
 	stepDump
+	stepCalendar
 	stepPace
 	stepMaking
 	stepReveal
@@ -46,6 +47,7 @@ type morning struct {
 	dump     textarea.Model
 	pace     domain.Pace
 	rowsKept []triageRow // the choices to keep when the review is fetched again after a conflict
+	calendar calendar
 }
 
 // triageRow is one unfinished task from the last day and what happens to it.
@@ -127,7 +129,7 @@ func newDump() textarea.Model {
 // beginMorning starts the ritual from the good-morning card by looking back at the last day.
 func (m Model) beginMorning() (tea.Model, tea.Cmd) {
 	m.morning = morning{step: stepGreet, dump: newDump(), pace: 3}
-	return m, tea.Batch(m.reviewMorning(), m.spin.Tick)
+	return m, tea.Batch(m.reviewMorning(), m.loadIntegrations(), m.spin.Tick)
 }
 
 func (m Model) reviewMorning() tea.Cmd {
@@ -254,14 +256,16 @@ func (m Model) onMorningKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
 		mo.notice = ""
 		switch k.String() {
 		case "tab":
-			return m.toPace()
+			return m.afterDump()
 		case "esc":
 			mo.dump.Reset()
-			return m.toPace()
+			return m.afterDump()
 		}
 		var cmd tea.Cmd
 		mo.dump, cmd = mo.dump.Update(k)
 		return m, cmd
+	case stepCalendar:
+		return m.onCalendarKey(k)
 	case stepPace:
 		switch k.String() {
 		case "left":
@@ -314,6 +318,10 @@ func (m Model) morningView() string {
 	case stepDump:
 		lines = append(lines, "What's on your plate?", "", mo.dump.View())
 		hint = "enter new line · tab next · esc skip"
+	case stepCalendar:
+		var more []string
+		more, hint = m.calendarLines(inner)
+		lines = append(lines, more...)
 	case stepPace:
 		if mo.notice != "" {
 			lines = append(lines, toastStyle.Render(mo.notice), "")
```

`internal/tui/model.go`, apply:

```diff
@@ -174,6 +174,8 @@ func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
 		return m, m.load()
 	case reviewedMsg, reviewFailedMsg, startedMsg, startFailedMsg, conflictMsg:
 		return m.onMorningMsg(msg)
+	case integrationsMsg, pulledMsg, pullFailedMsg, removedMsg:
+		return m.onCalendarMsg(msg)
 	case cheerTickMsg:
 		return m.onCheerTick(msg)
 	case spinner.TickMsg:
@@ -245,7 +247,7 @@ func (m Model) onHub(msg stream.Message) (tea.Model, tea.Cmd) {
 
 // spinning says something is in flight: what now, a chat reply, or the server's own model work.
 func (m Model) spinning() bool {
-	return m.asking || m.sending || m.morning.step == stepGreet || m.morning.step == stepMaking || m.working[stream.WorkReplan] > 0 || m.working[stream.WorkCleanup] > 0
+	return m.asking || m.sending || m.morning.step == stepGreet || m.morning.step == stepMaking || m.morning.calendar.pulling || m.working[stream.WorkReplan] > 0 || m.working[stream.WorkCleanup] > 0
 }
 
 func (m Model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
```

- [ ] **Step 4: Run the checks**

Run: `go test ./internal/tui/ -run CalendarView -update`, read `testdata/morning-calendar-80.golden` by eye, then `go test ./... && go vet ./...`.
Expected: PASS. Then smoke-test `whatnow tui` with `expect` on a throwaway `-db` (sized pty, as in the environment notes): tab past the dump, pull, and x a recurring event.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/calendar.go internal/tui/morning.go internal/tui/model.go internal/tui/calendar_test.go internal/tui/testdata/morning-calendar-80.golden
git commit -m "Pull the calendar in the TUI morning"
```

---

