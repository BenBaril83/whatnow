# whatnow web UI, part 1: Today

This first of three UI slices makes whatnow usable every day: the Today page, capture, What
now, the chat dock, undo, and a bare settings form for entering a key. The parent spec is
`2026-10-05-whatnow-design.md`, and its Web UI section lists every screen.

| Slice | Contents |
|---|---|
| **5a (this spec)** | Scaffold and embed, SSE store, Today, capture, What now, chat dock, undo toasts, bare settings |
| 5b | Good morning, wrap up, all tasks and the edit form |
| 5c | Full settings, delete everything, onboarding, nudges, the day strip fix |
| 5d | The `e2e` build tag, a fake provider, and Playwright |

## Visual direction: sticker

The look is chunky and playful, and it should demo loud.

- A cream background (`#fff8e7`) with ink-navy text (`#1f1a3d`).
- Cards and buttons have 2px ink outlines, flat offset shadows (`3px 3px 0`), and 12px corners.
- Tier fills: must `#ffd9d2`, should `#d8ecff`, could white. Chips are yellow (`#ffd84d`), the
  primary action is violet (`#7b5cff`), and the now line is red (`#ff4d6d`).
- Headings and labels are heavy weight, in a rounded sans (Avenir Next, falling back to the system
  sans).

All of these values are CSS custom properties on `:root` in `tokens.css`. Components use the
tokens and never raw colors.

## Stack

- SvelteKit 3 with Svelte 5 runes, TypeScript strict, and `adapter-static` with a `200.html` SPA
  fallback. Shared modules are imported through `#lib/...`, with the `.ts` extension.
- Styling is plain scoped Svelte CSS plus `tokens.css`. No Tailwind and no component library.
- API types are written by hand in `api.ts`.
- Vitest tests client logic. npm is the package manager, and `web/.nvmrc` pins the Node version.

## Layout

The Today page is a main column with the chat dock on its right.

The main column holds, from top to bottom:
1. The header.
2. The day strip.
3. The capture box.
4. The plan tiers.
5. The unplanned tray and Done today, side by side.

The chat dock is a full-height right panel. It collapses to a slim tab on the right edge, and
expands again from that tab. The tab shows a dot while a reply is streaming or unread. Whether the
dock is collapsed is kept in `localStorage`. With no saved choice, it starts open on a wide
screen and collapsed on a narrow one. Narrow screens get a bottom sheet instead.

## Structure

```
web/
  src/lib/api.ts             typed fetch wrappers; failures throw ApiError{status, message}
  src/lib/stream.svelte.ts   one EventSource on /api/v1/stream, a typed handler per event name
  src/lib/today.svelte.ts    state: today (day, plan, unplanned, blocked), events, tasks, blockers; refresh()
  src/lib/live.svelte.ts     the stream, settings, and minute clock, started once by the root layout
  src/lib/chat.svelte.ts     state: messages, the in-flight reply (text and chips), busy
  src/lib/toasts.svelte.ts   undo toasts keyed by batch ID
  src/lib/tokens.css         the sticker palette and shape tokens
  src/routes/(app)/+page.svelte      Today
  src/routes/settings/+page.svelte   settings
internal/web/web.go          //go:embed all:dist; the static handler with SPA fallback
```

`whatnow serve` keeps `/api` as it is and sends every other path to the `internal/web` handler.

## Data flow

The server is the single source of truth. When state changes, the client refetches instead of
patching local state.

- **On load:** fetch `/today`, `/events`, `/tasks`, and `/chat`, then open the stream.
- **`batch.applied`, `batch.undone`, `plan.updated`:** call `refresh()`, which refetches
  `/today`, `/events`, and `/tasks`. It is debounced by about 100ms, so a burst of events causes
  one refetch. A `batch.applied` that finishes a task also triggers confetti: a small burst, a
  bigger one for a must, and fireworks for the plan's last open task. Morning triage doesn't.
- **`work.started`, `work.finished`:** the server is running a slow model call, a `replan` or a
  `cleanup`. Today shows "Replanning…" and the capture bar shows "Tidying…" until it finishes.
- **`chat.delta`:** append to the in-flight reply. **`chat.tool`:** add a chip to it.
  **`chat.message`:** replace the in-flight reply with the stored message. Deltas are best-effort,
  and `chat.message` is authoritative, even when no deltas arrived before it.
- **Reconnect:** `EventSource` retries on its own. Once it is back, run `refresh()` and refetch
  `/chat`, and clear any work in progress, since its `work.finished` may have been lost. A "reconnecting…" pill shows in the header while it is down.

### Backend change: `source` on `batch.applied`

`BatchApplied` gains `source`: `user`, `chat`, `cleanup`, or `triage`. The UI needs it to decide
which undo affordance to show. The batch's stored actor can't tell chat from cleanup, since both
are `agent`. Inferring it doesn't work either: the event is published before the HTTP response
returns the batch ID, and other tabs make batches too. `source` lives only on the event, so the
schema doesn't change. Each `PublishBatch` caller passes its own source.

### Undo

| Batch | Where its undo appears |
|---|---|
| `user`, from this tab | A toast, using the `batch_id` in the HTTP response |
| `user`, from another tab | None |
| `cleanup` | A toast saying what cleanup did, such as "Split your note into 3 tasks and 1 event", from `batch.applied` |
| `chat` | An Undo button on that chat reply, shown only when its `batch_id` is nonzero |
| `triage` | None. The ritual's triage list is its own confirmation (see part 2) |

Undo calls `POST /batches/{id}/undo`. When `batch.undone` arrives, the toast dismisses itself and
the reply's button reads "undone". Toasts stack at the bottom left and dismiss after about 8
seconds.

## Today page

**Header:**
- The date.
- The pace chip. Clicking it opens the five levels. Picking one calls `PUT /today/pace` and shows
  an undo toast, and the replan arrives as `plan.updated`.
- A countdown to the next event: "Standup in 24m", or "Standup now" while it runs.
- The **What now?** button.

**Day strip:** today's events placed by time, with a red now line that moves along it, ticking each
minute. Part 3 (`2026-10-06-whatnow-web-ui-settings-design.md`) sets the window, the hour ticks, and
the end-of-day mark. An event with no end is drawn as 30 minutes, the same as `facts`. An event's
box grows to fit its title on one line, even past its end time, so it can overlap the next event.
Boxes held to their time with titles clamped to two lines were tried and lost: a half-hour event at
a normal width showed only the start time and an ellipsis.

**Capture box:** pressing Enter sends `POST /tasks {raw_text}` and clears the box at once. The raw
card appears in the tray after the refetch, and the cleanup batch replaces it later. If the save
fails, the text goes back in the box. Capture works with no provider set up.

**Plan tiers:**
- Must, should, and could, in plan order. A task finished, dropped, deleted, or pushed past
  today since the plan was made leaves its tier at once, without waiting for a replan.
- Each card shows the title, size and energy badges, the feeling note in italics, and the first
  line of its notes. The rationale shows on hover. A click expands the card to the full notes and
  the rationale, and a second click collapses it.
- A card linked by blockers shows "⛓ waits on N" and/or "unblocks N", counting open tasks only.
  Clicking one shows the linked titles under it and puts a dashed outline on the linked cards.
  Clicking again hides them. The ⛓ glyph stays as it is, small and odd in the sticker font.
- Every ⋯ and pace menu closes on a click outside it or on Escape.
- Each card has a checkbox, which completes it, and a ⋯ menu with **push to tomorrow** and
  **drop**. Every action shows an undo toast. If completing fails, the checkbox unticks.
- The ⋯ menu's **Move ›** opens the tiers and the tray in one column, the card's own greyed out.
  Picking one moves the card there. It saves a new plan version
  with no model call, so it has no undo. Later replans see each move by hand and weigh it, but
  can still move the task again.
- A fallback plan carries a small "rough plan, model was out" tag.

**Unplanned tray:** dashed cards with the same actions as plan cards.

**Done today:** collapsed by default. Its header carries the win, for example "✓ 6 things done, 3
meetings survived". Meetings survived counts today's events that have already ended.

**What now answer:** a large card that pops in above the tiers. It shows the picked task and the
reason, with these actions:
- **[done]** completes the task.
- **[not this one]** opens an optional reason box, then asks again with `exclude_task_id`.
- **✕** dismisses the card.

When nothing is open and eligible (`404`), a quiet "Nothing to do right now. Enjoy it." note
shows instead of an error.

**No plan yet:** Today redirects to `/morning` (part 2).

## Chat dock

- On load, the dock shows today's history from `GET /chat`. When the plan has an `opening`, it is
  shown as the first bubble. It isn't stored as a message.
- **Sending:**
  - The user's bubble appears at once, and the input locks while a reply is busy.
  - Tool chips are small pills: yellow with ✓ for a call that worked, red with ✕ for one that
    failed.
  - A `409` means another tab is mid-turn. The dock shows "one sec, still answering" and keeps the
    text in the input.
- **A failed reply** (`error` set) shows as a muted bubble with the error text and a link to
  Settings. This covers a bad key, rate limits, and outages. Sending again retries.

## Errors and the no-provider state

- A failed API call shows a red toast with the error message. It has no undo.
- When no provider is set up, the header shows a notice with a link to Settings. Nothing is
  disabled: capture works, What now and the morning's plan return the code fallback, and chat returns a
  failed reply that says so.

## Bare settings

- The fields are provider, key, main model, light model, and **Test connection**.
- The key field is write-only. It shows "saved", or "from env" when the key comes from the
  environment.
- The page writes settings back as the full object it got from GET, because `PUT /settings`
  rejects a body without `end_of_day` or `alert_lead_minutes`.
- Part 3 adds the rest of the page.

## Build

- `adapter-static` writes to `web/build`. `just web` builds it and copies the output into
  `internal/web/dist/`.
- `dist/` is gitignored, except for a committed `.gitkeep`. That way `go build` and `go test` work
  on a fresh clone without Node.
- If `dist` is empty, the handler serves a one-line page: "UI not built, run `just build`".
- `just build` runs `just web` and then builds the binary.
- `just dev` runs Vite, which proxies `/api` including the SSE stream, alongside `just serve`.

## Testing

**Go:**
- `internal/web` serves assets, and falls back to `200.html` for an unknown path outside `/api`.
- It serves the "not built" page when `dist` is empty.
- It never shadows `/api/*`.
- `batch.applied` carries the right `source` from each publisher: user writes, chat, cleanup,
  and triage.

**Vitest, for logic modules only:**
- The stream dispatcher routes each event to its handler by name.
- The chat store handles deltas, chips, and the `chat.message` swap, including a `chat.message`
  with no deltas before it.
- `refresh()` is debounced: a burst of events causes one fetch.
- The toast rules cover each source, and dismissal on `batch.undone`.
- Countdown and day-strip math covers no events, during an event, an event with no end, and a time
  past the end of day.
- The copy in the Done today header.

Components get no unit tests. The Playwright suite in `web/e2e/` (`just e2e`) covers them.
