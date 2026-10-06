# whatnow web UI, part 1: Today

This first of three UI slices makes whatnow usable every day: the Today page, capture, What
now, the chat dock, undo, and a bare settings form for entering a key. The parent spec is
`2026-10-05-whatnow-design.md`, and its Web UI section lists every screen.

| Slice | Contents |
|---|---|
| **5a (this spec)** | Scaffold and embed, SSE store, Today, capture, What now, chat dock, undo toasts, bare settings |
| 5b | Good morning, wrap up, all tasks and the edit form |
| 5c | Onboarding, full settings, nudges, Playwright |

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
  src/lib/today.svelte.ts    state: today (day, plan, unplanned, blocked), events, tasks; refresh()
  src/lib/chat.svelte.ts     state: messages, the in-flight reply (text and chips), busy
  src/lib/toasts.svelte.ts   undo toasts keyed by batch ID
  src/lib/tokens.css         the sticker palette and shape tokens
  src/routes/+page.svelte            Today
  src/routes/settings/+page.svelte   bare settings
internal/web/web.go          //go:embed all:dist; the static handler with SPA fallback
```

`whatnow serve` keeps `/api` as it is and sends every other path to the `internal/web` handler.

## Data flow

The server is the single source of truth. When state changes, the client refetches instead of
patching local state.

- **On load:** fetch `/today`, `/events`, `/tasks`, and `/chat`, then open the stream.
- **`batch.applied`, `batch.undone`, `plan.updated`:** call `refresh()`, which refetches
  `/today`, `/events`, and `/tasks`. It is debounced by about 100ms, so a burst of events causes
  one refetch.
- **`chat.delta`:** append to the in-flight reply. **`chat.tool`:** add a chip to it.
  **`chat.message`:** replace the in-flight reply with the stored message. Deltas are best-effort,
  and `chat.message` is authoritative, even when no deltas arrived before it.
- **Reconnect:** `EventSource` retries on its own. Once it is back, run `refresh()` and refetch
  `/chat`. A "reconnecting…" pill shows in the header while it is down.

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
| `cleanup` | A toast reading "here's what I did", from `batch.applied` |
| `chat` | An Undo button on that chat reply, shown only when its `batch_id` is nonzero |
| `triage` | None in 5a. The wrap-up and good-morning flows in 5b own it |

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

**Day strip:** today's events placed by time. The strip runs from the earlier of now and the first
event, to the end-of-day setting. A red now line moves along it, ticking each minute. An event
with no end is drawn as 30 minutes, the same as `facts`.

**Capture box:** pressing Enter sends `POST /tasks {raw_text}` and clears the box at once. The raw
card appears in the tray after the refetch, and the cleanup batch replaces it later. If the save
fails, the text goes back in the box. Capture works with no provider set up.

**Plan tiers:**
- Must, should, and could, in plan order. A task finished, dropped, deleted, or pushed past
  today since the plan was made leaves its tier at once, without waiting for a replan.
- Each card shows the title, size and energy badges, the feeling note in italics, and ⛓ when the
  task is blocked. The rationale shows on hover or tap.
- Each card has a checkbox, which completes it, and a ⋯ menu with **push to tomorrow** and
  **drop**. Every action shows an undo toast. If completing fails, the checkbox unticks.
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

**Start my day:** good morning arrives in 5b. Until then, when today has no plan, the plan area
shows a card with a pace picker and an optional "anything on your mind?" box. It sends
`POST /today/start` with an empty triage, so an unwrapped yesterday's tasks roll over. 5b replaces
this card.

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
  disabled: capture works, What now and Start my day return the code fallback, and chat returns a
  failed reply that says so.

## Bare settings

- The fields are provider, key, main model, light model, and **Test connection**.
- The key field is write-only. It shows "saved", or "from env" when the key comes from the
  environment.
- The page writes settings back as the full object it got from GET, because `PUT /settings`
  rejects a body without `end_of_day`.
- Style, about me, nudges, end of day, and alert lead time come in 5c.

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

Components get no unit tests. Playwright covers them in 5c. Until then, verify 5a by hand against
`whatnow serve` with a throwaway `-db`.
