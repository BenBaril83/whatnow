# whatnow integrations: Jira

Jira is the second integration, on the contract in `2026-10-12-whatnow-integrations-design.md`.
The calendar writes what it pulls; Jira offers tickets and the user picks which become tasks.

## Behavior

- **Pulls happen only when asked**, as with the calendar: the morning's brain-dump step, or **Pull**
  in settings, which is also the way to pick up another ticket later in the day.
- **The search is the user's JQL.** One field, so a new rule ("also unassigned tickets labeled
  BE") is an edit, not a code change, and a bad search explains itself with Jira's own error. A
  team's search looks like:

  ```
  project = FLEX1
  AND sprint in openSprints()
  AND statusCategory != Done
  AND (assignee = currentUser() OR (assignee is EMPTY AND labels = BE))
  ```

  Nothing is prefilled: a default would bake one company's host and project into the app.
- **A pull offers; the user picks.** Nothing starts ticked. Only ticked tickets become tasks.
  Unassigned tickets are options, not commitments, and adding a sprint's worth at once would bury
  the day. Unpicked tickets come back on the next pull.
- **Tickets already on the list aren't offered:** any task, open or done, with the ticket's key
  as a ref, whether it came from a pull or was typed into a brain dump. A deleted task doesn't
  count, so its ticket can be offered again.
- **× skips a ticket for good**, through the generic skip list, for a ticket the user will never
  take. Settings lists it with "bring back", as for a calendar series.
- **A picked ticket is a one-time copy.** Nothing syncs back either way: finishing a task never
  touches Jira, and a ticket closing in Jira never closes the task. whatnow is managed by its
  user; finishing and tidying by hand is where the satisfaction and the habit come from. Done in
  whatnow also rarely means Done in Jira (a PR is up, review and QA lie between), so write-back
  would have to guess a column, in front of the whole team.

## The contract

- **An integration declares what it pulls:** `Pulls() Pulls`, either `PullsEvents` or
  `PullsTickets`, and the view carries it as `pulls: "events" | "tickets"`. Screens use it to pick
  the results UI and the button's words (**Pull today** or **Pull**), and the TUI to decide which
  morning step an integration belongs to.
- **`Source` gains `Tickets []Ticket` and `More int`** beside `Events`; a source fills one or the
  other. `Ticket` is `{Key, Title, Notes, URL}`. `More` is how many matched beyond those returned,
  so a capped list can say so.
- **The runner never writes tickets on pull.** It drops those whose key is a ref on a live task
  or is on the skip list, and returns the rest. A ticket marked `Rereview` gets past a done or
  dropped task (`2026-10-15-whatnow-review-queue-design.md`); Jira never marks one. A pull with no
  events writes nothing and opens no batch.
- **`Runner.Add(ctx, id, tickets)`** creates one task per ticket in one user batch, so one undo
  removes them all, and the hub's `batch.applied` refreshes open screens. It drops tickets already
  on the list again, so a double click adds nothing twice. It requires the integration to be on.

## Jira

`internal/integrations/jira`, built like `gcal`, knowing nothing of whatnow's storage.

- **Fields,** all required: `url` (text; help: "Your Jira's address, such as
  https://jira.example.com"), `token` (secret; help: "Profile → Personal Access Tokens"), and
  `jql` (text; help: an example search).
- **Auth is a Personal Access Token** sent as `Authorization: Bearer <token>`, for Jira Data
  Center and Server 8.14 or later. Jira Cloud's email-plus-API-token isn't supported until
  someone needs it.
- **One request per pull:** `GET {url}/rest/api/2/search` with `jql`,
  `fields=summary,description,assignee`, and `maxResults=50`, with a 30-second timeout. Jira's
  `total` past the 50 becomes the source's `More`, and the pick list ends with "N more matched —
  narrow the search".
- **Each issue becomes** `{Key, Title: "KEY summary", Notes: description, URL: {url}/browse/KEY}`.
  The title keeps the key first so it reads as in Jira; the key is also the task's ref, which links
  it and dedupes it. A null description is empty notes. The description is kept as Jira's wiki
  markup: it reads fine as text, and links in notes are already clickable.
- **Assigned tickets sort before unassigned ones**, in Jira's order otherwise. A search like the
  one above only lets through assigned tickets that are the user's own, so this lists theirs first.
- **Errors never contain the token:** no response or a timeout is "Can't reach Jira — on the
  VPN?"; `401` is "Jira rejected the token"; `400` is Jira's `errorMessages` joined, which is how a
  bad search explains itself; anything else is "Jira answered <status>". An address with no scheme
  or host is "that address isn't a URL, such as https://jira.example.com", since it would otherwise
  read as a network failure. A `200` that isn't JSON, such as a sign-in page in front of the API,
  is "couldn't read Jira's answer — is the address right?" rather than an empty list.
- **A skip has no batch,** so its toast offers no Undo; bringing a ticket back is in settings.

**A picked ticket's task:** `title` is the ticket's title, `notes` its description, `refs` one
`{label: key, url}`, `created_by` the user. Size and energy stay blank, so the planner leaves it
in unplanned until the user says more. It skips the model's cleanup: a ticket is already one clean
item, and cleanup might split it.

## API

| Route | Does |
|---|---|
| `POST /integrations/{id}/pull` | As before; each source also carries its `tickets`. |
| `POST /integrations/{id}/add` | Takes `{tickets: [{key, title, notes, url}]}`, creates the tasks, and returns the batch ID and the tasks. The client sends back what the pull returned, so Jira isn't asked twice. `409` when the integration isn't on. |
| `POST /integrations/{id}/skipped` | As before; `event_id` is optional, and a ticket leaves it out. |

## Web

**Morning:** the Jira chip pulls and opens a pick list in place of the calendar's grouped events:

```
Jira · 7
[ ] FLEX1-4033 [asessment-quiz-service] Subscribe to existing PubSub…   ×
[ ] FLEX1-4051 Fix flaky consumer test                                  ×
[ Add 2 ]
```

- Titles link to the ticket in a new tab. **Add** is disabled until something is ticked; after
  it, the list gives way to "Added 2".
- "Nothing new in Jira" when the pull offers nothing; a failed pull shows its error with **Retry**.

**Settings:** the Jira card renders from its fields like the calendar's. **Pull** opens the same
pick list, Add included.

## TUI

A **Jira** step follows the calendar step, shown only when a ticket integration is on. The calendar
step today shows when any integration is on and pulls them all; it narrows to event integrations.

- "Pull from Jira?" (naming every ticket integration that's on) `enter` pulls (spinner meanwhile), `tab` moves on to pace.
- The pick list: `↑`/`↓` move, `space` ticks, `x` skips a ticket for good, `enter` adds the
  ticked tickets and moves on, `tab` moves on without adding. A long list scrolls to the cursor.
- A failed pull shows a red line; `r` retries.

## Testing

**Go:**
- `jira`, over `httptest`: an issue's fields mapped (key-first title, browse URL, null
  description), the Bearer header, the assigned-first order, `More`, and each error:
  unreachable, timeout, `401`, `400` with `errorMessages`, `500`. No error contains the token.
- `integrations.Runner`, with a fake ticket integration: tickets whose key is a ref on a live
  task (open or done) are dropped, a deleted task's ticket is offered, skipped tickets are dropped,
  a ticket-only pull opens no batch, Add creates the tasks in one batch that one undo removes, and
  Add twice adds once.
- `httpapi`: a pull with tickets, add end to end, `409` when off, and skip without `event_id`.
- TUI: a golden file for the pick list, then tick and add, `x`, `tab`, a failed pull with `r`, and
  the calendar step hidden when only Jira is on.

**Vitest:** which tickets Add sends, and dropping a skipped ticket.

**Playwright:** the test serves a fake Jira search from Node. One flow sets Jira up in settings,
pulls from the morning, skips one ticket, adds another, and pulls again: neither comes back.
