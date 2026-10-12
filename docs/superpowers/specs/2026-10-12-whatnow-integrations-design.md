# whatnow integrations: the contract and Google Calendar

Integrations pull outside work into whatnow: today's meetings now, Jira tickets next. This spec
designs the integration contract and its first integration, Google Calendar. Jira gets its own spec
on top of this contract. The parent spec, `2026-10-05-whatnow-design.md`, promised that each
integration declares the settings fields it needs; this is that contract.

Integrations are Go packages compiled into the binary, not external plugins. No outside tool will
ever speak a whatnow contract, so a process boundary would buy nothing.

## Behavior

- **Pulls happen only when asked:** from the morning's brain-dump step, or from **Pull today** in
  settings. There is no background refresh.
- **A pull covers today only**, in local time. A second pull brings today in line with the feed:
  moved meetings move, canceled ones disappear, and anything the user removed stays removed.
- **Google Calendar reads each calendar's secret iCal address.** Google Calendar → Settings → the
  calendar → Integrate calendar → Secret address in iCal format. No OAuth and no GCP project. The
  feed is the calendar's whole history (often tens of MB), which is why pulls are on demand.
- **Several calendars**, each a labeled row (Work, Personal). One pull fetches them all.
- **Skipped:** all-day events, events the user declined, and series the user chose to always skip.
- **Removing a pulled event:** a one-off deletes. A recurring one asks "Just today" or "Always
  skip". Always skip adds the series to the integration's skip list and deletes today's
  occurrence. Settings lists skipped series with a way to bring each back.

## The contract

`internal/integrations` holds the contract and `Runner`, which keeps each integration's settings
and syncs what it pulls. `app.Open` hands the runner its integrations (`gcal.New(nil)`), since a
registry inside the package would import every integration and each imports the contract. Each
integration lives in its own package under it and knows nothing about whatnow's storage.

```go
type Integration interface {
	ID() string   // "gcal"
	Name() string // "Google Calendar"
	Fields() []Field
	// Pull fetches what starts in [from, to), one Source for each part it pulls, such as a calendar.
	Pull(ctx context.Context, cfg Config, from, to time.Time) []Source
}

type Field struct {
	Key      string
	Label    string
	Kind     Kind    // text, secret, or list
	Help     string  // one line under the field, such as where to find the URL
	Required bool    // a required list needs a row; a row needs its required columns to save
	Fields   []Field // a list's columns; only list uses it
}
```

- **`text`** is stored in the database. **`secret`** is stored in the keychain and never
  leaves the server. **`list`** is repeatable rows of `text` and `secret` columns. Lists don't
  nest.
- **List rows get a server-generated ID**, so a row's secret has a stable keychain entry
  (`integration/gcal/<rowID>/url`) when its label is renamed.
- **`Config`** is the saved values with secrets resolved, so an integration never touches the
  keychain.
- **`Source`** is what one part pulled (a list row, or the integration as a whole when it has no
  list): its label, its `[]Event`, and its error in words fit for the user. Jira adds tasks here.
- **`Event`** is `{Key, Series, Title, StartsAt, EndsAt}`. `Series` is set for a recurring
  event and empty for a one-off; it decides whether removal asks.
- **An integration is on** when its `enabled` flag is set and it's `Ready`: its required fields
  are filled. Turning it off keeps its saved values.
- **The skip list is generic:** per integration, a list of `{key, title}`. A pull drops anything
  whose key or series is on it. Jira can use it to ignore a ticket.

Integration settings live in their own table, `integrations (id, body)`, one JSON row each:
`enabled`, the text values, list rows (IDs and text columns), and the skip list. They stay out of
the settings document because the General page saves that document whole, and a stale copy would
overwrite a skip made since.

## Data and sync

**Migration:** `events` gains nullable `source` and `source_key`, with a unique index on the pair.
Events the user or agent made leave both null.

**Keys:** `source_key` is the event's ICS `UID` plus its *original* occurrence start (the
`RECURRENCE-ID` for a moved occurrence). A moved occurrence keeps its key, so it updates rather
than duplicating. A meeting on both Work and Personal shares a UID, so it lands once.

**`service.SyncEvents(ctx, batch, source, from, to, events, prune)`** applies a pull in one batch:

| Feed has it? | Row | Result |
|---|---|---|
| yes | none | create |
| yes | live | update title and times to the feed's; the feed wins over a hand edit |
| yes | soft-deleted | leave it; the deleted row is the tombstone that keeps a removal |
| no | live, this source, starts in the window | soft-delete and clear its key: canceled, or moved to another day |

- **A tombstone is a deleted row that still has its key.** The user's × leaves the key. A prune
  clears it, and so does undoing the pull that created the row, so a meeting that comes back, or
  a pull that's undone and pulled again, brings the event back. Always skip clears it too, since
  the skip list keeps the series out; bringing the series back brings today's occurrence back.
- **When any source fails, the pull removes nothing.** Rows don't record which calendar they came
  from (a meeting on two calendars is one row), so a failed calendar's events can't be told from
  canceled ones. Creates and updates from the other sources still apply.
- **The pull is an ordinary user batch**, so it undoes like any other, and the hub publishes it,
  which refreshes open screens. A morning pull lands before the plan, which then sees the events.
  A pull later in the day doesn't replan on its own, since no event change does; Replan picks the
  new events up.

## Google Calendar

`internal/integrations/gcal` fetches each row's URL, parses it with `github.com/emersion/go-ical`,
and expands recurrences with `github.com/teambition/rrule-go`. It reads date-times itself, caching
each `TZID`'s location: loading a zone reads its file from disk, and doing that per event made a
real 20 MB feed take 5 to 8 seconds instead of a quarter of one. (`apognu/gocal` was rejected: it
has the same cost, mishandles `BYDAY=2TU`, and misses an occurrence moved out of the window.)

- **One field:** `calendars`, a list of `{label: text, url: secret}`, with the help line naming
  where Google keeps the secret address.
- **Fetching:** a 30-second timeout per calendar. Go's client asks for gzip, and Google serves it,
  so a 20 MB feed is under 2 MB over the wire. Don't set `Accept-Encoding` by hand, or Go stops
  decompressing.
- **All-day events** (a `DATE` start) and canceled ones (`STATUS:CANCELLED`) are dropped.
- **A moved or canceled occurrence** is its own `VEVENT` with a `RECURRENCE-ID`. It replaces the
  series' occurrence at that start wherever it moved to, even out of the window.
- **One malformed event**, such as an unknown `TZID`, is skipped rather than failing the calendar.
- **Declined events:** the feed's `X-WR-CALNAME` is the owner's email on a primary calendar, and
  every event with guests lists the owner as an `ATTENDEE` (`mailto:` the same email) with a
  `PARTSTAT`. An event whose owner entry is `PARTSTAT=DECLINED` is dropped. Tentative and
  not-yet-answered events stay, as do events with no owner entry (the owner's own blocks). When
  the name isn't an email, nothing is dropped for this reason. Attendee lines are folded; the
  decoder unfolds them.
- **Errors name the row's label, never its URL**, and never wrap the HTTP client's error, which
  holds the URL: "Google said 404, check the address", "couldn't reach Google", "Google took too
  long to answer", "couldn't read the calendar".

## API

All under `/api/v1`:

| Route | Does |
|---|---|
| `GET /integrations` | Each integration's declared fields, `enabled`, `ready`, saved values, and skip list. A secret reads as set or unset, never its value. |
| `PUT /integrations/{id}` | Saves the values. A blank secret keeps the saved one. A row with no ID is new; a removed row's secret is deleted from the keychain. |
| `PUT /integrations/{id}/enabled` | Turns it on or off, apart from the values, so the toggle never saves half-typed edits. |
| `POST /integrations/{id}/pull` | Pulls today. Returns the batch ID and, per source, its label, its pulled events (with their whatnow IDs and series), or its error. `409` when the integration isn't on. |
| `POST /integrations/{id}/skipped` | Takes `{key, title, event_id}`: deletes today's occurrence in a batch it returns, and adds the series to the skip list. Undoing the batch brings back today's occurrence; the series stays skipped. |
| `DELETE /integrations/{id}/skipped/{key}` | Brings a series back, with the key path-escaped. The next pull imports it again. |

`DELETE /data` also deletes every integration secret from the keychain, logged rather than failed
like the provider keys. It lists them before wiping, since the rows say which there are.

## Web

### Morning

The brain-dump step gets an **Integrations** row under the textarea, with a chip per integration
that's on (`↻ Google Calendar`). No chip, no row.

Clicking pulls. The chip becomes the results, grouped by source with a count:

```
Work · 4             Personal · 1
10:00 Standup  ×     18:30 Dentist  ×
13:00 1:1      ×
15:00 Blockout ×
```

- **×** on a one-off is `DELETE /events/{id}`. On a recurring event, a small popover offers **Just
  today** (the same delete) and **Always skip** (`POST …/skipped`).
- **A failed source** shows its error in place of its group, with **Retry**. Clicking the chip
  again re-pulls everything.
- The pull writes events at once, apart from `/today/start`, so **Skip** on the dump doesn't undo
  it.

### Settings

`/settings` gets a sidebar: **General** (`/settings`, the existing sections and Delete everything,
unchanged) and **Integrations** (`/settings/integrations`). On narrow screens the sidebar becomes
two tabs across the top.

```
┌──────────────┬──────────────────────────────────────────────┐
│ Settings     │  Integrations                                │
│              │  ┌────────────────────────────────────────┐  │
│  General     │  │ ▾ Google Calendar             [ ●━ On ] │  │
│ ▸Integrations│  │  Calendars                             │  │
│              │  │   Work      •••••••• saved     remove  │  │
│              │  │   Personal  •••••••• saved     remove  │  │
│              │  │   + Add                                │  │
│              │  │  ⓘ Google Calendar → Settings → …      │  │
│              │  │  Skipped: Blockout (bring back)        │  │
│              │  │  [Save]   [Pull today]                 │  │
│              │  └────────────────────────────────────────┘  │
└──────────────┴──────────────────────────────────────────────┘
```

- **Each integration is a card**, collapsed by default, with an on/off toggle in its header.
  Turning it on expands the card; a card that's off still expands, so it can be set up first.
- **The toggle saves at once.** The fields save with the card's own **Save**.
- **Fields render from `Fields()`:** `text` as an input, `secret` as a masked input reading "saved"
  when set (as API keys do), `list` as rows with **+ Add** and **remove**. `Help` sits under its
  field. The add button says only "Add", since a field's label is plural and there's no singular
  to borrow.
- **Pull today** is disabled until the card is saved and on, and shows the same grouped results
  as the morning, × included.

## TUI

The morning gets a **Calendar** step between brain dump and pace, shown only when an integration is
on.

- "Pull today's calendar?" `enter` pulls (spinner meanwhile), `tab` skips to pace.
- The results are the morning's grouped list. `↑`/`↓` move, `x` removes. On a recurring event, `x`
  shows `t today · a always · esc cancel` inline. `enter` moves on to pace.
- A failed source shows a red line in place of its group; `r` retries.

TUI settings are their own piece of work. Until then, integrations are set up in the web settings,
and the TUI's only pull is the morning step.

## Testing

**Go:**
- `gcal`, against fixture feeds: a series with a moved occurrence, a canceled one, and an `EXDATE`
  line listing two dates; an all-day event; declined, tentative, and unanswered events; a DST
  change; a monthly `BYDAY=2TU` rule; one malformed event; and, over `httptest`, a 404, a body
  that isn't a calendar, an unreachable host, and a timeout. No error message contains the URL.
- `service.SyncEvents`: create, update in place, nothing written when nothing changed, a tombstone
  that stays deleted, pruning only its own source and window, no prune when told not to, undoing
  a pull then pulling again, and undoing a prune.
- `integrations.Runner`, with a fake integration: secrets shown only as set, a blank secret kept, a
  removed row's secret deleted, bad input rejected, the same event on two sources landing once, a
  failed source removing nothing, and skip, undo, and bring back.
- `httpapi`: the fields listed, a pull end to end, a failed feed in the result, skip and bring back
  with a key that needs escaping, `409` when off, and `DELETE /data` wiping calendar URLs.
- TUI: a golden file for the calendar step with the recurring `x` prompt, a whole morning that
  always-skips a series, and just today, esc, tab, a failed pull, and `r`.

**Vitest, for logic modules only:** the form starting secrets blank, a new row with no ID, when an
integration is on, and removing an event from a pull result.

**Playwright:** the test serves a fixture feed from Node, dated today. One flow sets up a calendar in settings, pulls
from the morning, and always-skips a recurring event, then pulls again and the event stays gone.
