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

`internal/integrations` holds the contract and a fixed registry
(`[]Integration{gcal.New()}`). Each integration lives in its own package under it and knows nothing
about whatnow's storage.

```go
type Integration interface {
	ID() string   // "gcal"
	Name() string // "Google Calendar"
	Fields() []Field
	Pull(ctx context.Context, cfg Config, from, to time.Time) (Pulled, error)
}

type Field struct {
	Key    string
	Label  string
	Kind   Kind    // text, secret, or list
	Help   string  // one line under the field, such as where to find the URL
	Fields []Field // a list's columns; only list uses it
}
```

- **`text`** is stored in the settings document. **`secret`** is stored in the keychain and never
  leaves the server. **`list`** is repeatable rows of `text` and `secret` columns. Lists don't
  nest.
- **List rows get a server-generated ID**, so a row's secret has a stable keychain entry
  (`gcal/<rowID>/url`) when its label is renamed.
- **`Config`** is the saved values with secrets resolved, so an integration never touches the
  keychain.
- **`Pulled`** holds a result per source (a list row, or the integration as a whole when it has no
  list): its label, its `[]PulledEvent`, and its error. Jira adds tasks here.
- **`PulledEvent`** is `{Key, Series, Title, StartsAt, EndsAt}`. `Series` is set for a recurring
  event and empty for a one-off; it decides whether removal asks.
- **An integration is on** when its `enabled` flag is set and its required fields are filled.
  Turning it off keeps its saved values.
- **The skip list is generic:** per integration, a list of `{key, title}`. Sync drops anything
  whose series key is on it. Jira can use it to ignore a ticket.

Integration settings live in the settings document under `integrations.<id>`: `enabled`, the text
values, list rows (IDs, labels, which secrets are set), and the skip list.

## Data and sync

**Migration:** `events` gains nullable `source` and `source_key`, with a unique index on the pair.
Events the user or agent made leave both null.

**Keys:** `source_key` is the event's ICS `UID` plus its *original* occurrence start (the
`RECURRENCE-ID` for a moved occurrence). A moved occurrence keeps its key, so it updates rather
than duplicating. A meeting on both Work and Personal shares a UID, so it lands once.

**`service.SyncEvents(ctx, batch, source, from, to, events)`** applies a pull in one batch:

| Feed has it? | Row | Result |
|---|---|---|
| yes | none | create |
| yes | live | update title and times to the feed's; the feed wins over a hand edit |
| yes | soft-deleted | leave it; the deleted row is the tombstone that keeps a removal |
| no | live, this source, starts in the window | soft-delete: canceled, or moved to another day |

- **A source that failed deletes nothing.** Its rows are left as they were; the others apply.
- **The pull is an ordinary user batch**, so it undoes like any other, and the hub publishes it.
  A pull after the day's plan exists replans through the existing edit trigger. A morning pull
  lands before the plan, which then sees the events.

## Google Calendar

`internal/integrations/gcal` fetches each row's URL and parses it with `github.com/apognu/gocal`,
which expands recurrences inside `[from, to)`.

- **One field:** `calendars`, a list of `{label: text, url: secret}`, with the help line naming
  where Google keeps the secret address.
- **Fetching:** a 30-second timeout per calendar. Go's client asks for gzip, and Google serves it,
  so a 20 MB feed is under 2 MB over the wire. Don't set `Accept-Encoding` by hand, or Go stops
  decompressing.
- **All-day events** (a `DATE` start) are dropped.
- **Declined events:** the feed's `X-WR-CALNAME` is the owner's email on a primary calendar, and
  every event with guests lists the owner as an `ATTENDEE` (`mailto:` the same email) with a
  `PARTSTAT`. An event whose owner entry is `PARTSTAT=DECLINED` is dropped. Tentative and
  not-yet-answered events stay, as do events with no owner entry (the owner's own blocks). When
  the name isn't an email, nothing is dropped for this reason. Attendee lines are folded, so match
  after unfolding (gocal does).
- **Errors name the row's label, never its URL**, and the URL is never logged: "Personal: Google
  said 404, check the URL", "Personal: couldn't read the calendar".

## API

All under `/api/v1`:

| Route | Does |
|---|---|
| `GET /integrations` | Each integration's declared fields, `enabled`, saved values, and skip list. A secret reads as set or unset, never its value. |
| `PUT /integrations/{id}` | Saves `enabled` and the values. A blank secret keeps the saved one. A removed list row's secret is deleted from the keychain. |
| `POST /integrations/{id}/pull` | Pulls today. Returns the batch ID and, per source, its label, its pulled events (with their whatnow IDs), or its error. `409` when the integration is off. |
| `POST /integrations/{id}/skipped` | Adds `{key, title}` to the skip list and deletes today's occurrences of that series, in one batch. |
| `DELETE /integrations/{id}/skipped/{key}` | Brings a series back. The next pull imports it again. |

`DELETE /data` also deletes every integration secret from the keychain, logged rather than failed
like the provider keys.

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
│              │  │   + Add calendar                       │  │
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
  field.
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
- `gcal`, against fixture feeds served by `httptest`: a recurring event with a moved occurrence, an
  `EXDATE`, an all-day event, a declined event, a DST change inside the window, the same UID on two
  calendars, a 404, and an unparseable body. No error message contains the URL.
- `service.SyncEvents`, table-driven: create, update, a tombstone that stays deleted, cancellation
  by absence, a skipped series, a failed source deleting nothing, and undoing the pull batch.
- `httpapi`: secrets masked on read, a blank secret keeping the saved one, a removed row's secret
  deleted, pull when off is `409`, skip deleting today's occurrences in one batch, bring back, and
  `DELETE /data` wiping integration secrets.
- TUI: golden files for the calendar step's results and the recurring `x` prompt.

**Vitest, for logic modules only:** grouping pull results by source, and building the `PUT` body
from field values (blank secrets left out, row IDs kept).

**Playwright:** the e2e build serves a fixture feed. One flow sets up a calendar in settings, pulls
from the morning, and always-skips a recurring event, then pulls again and the event stays gone.
