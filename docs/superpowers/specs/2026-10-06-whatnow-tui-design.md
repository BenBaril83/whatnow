# whatnow TUI: a Bubble Tea companion

`whatnow tui` is a terminal view of today, meant to stay open in a spare pane all day. It is a
companion, not a second client with parity: it shows the day, takes the quick actions, and
hosts chat. Setup and the morning and wrap-up rituals stay in the web UI.

It runs in-process and calls the Go services directly, without HTTP. It's built on Bubble Tea, Bubbles, and Lip Gloss v1 (`github.com/charmbracelet/...`);
teatest pins Bubble Tea v1, so don't mix in the v2 `charm.land` packages.

## Shared wiring (`internal/app`)

The wiring lives in `internal/app`, not `main.go`, so `serve` and `tui` get identical behavior: captures are cleaned up, changes trigger replans, and chat streams.

`app.Open(app.Options{DBPath, NewClient, Now, Loc})` resolves the default database path, opens
the store, and builds the service, hub, cleanup runner, planner, and agent. It starts the cleanup
runner and the replan watcher, and returns an `App` holding those parts with a `Close` that stops
the goroutines and closes the database. `main` passes its `newClient` as `Options.NewClient`, so
the `e2e` build's fake model reaches both subcommands. `Now` and `Loc` let tests fix the clock.

`serve` becomes `app.Open` plus the HTTP server. `tui` becomes `app.Open` plus the Bubble Tea
program. Both take the same `-db` flag.

### Running both at once

`serve` and `tui` may run at the same time on the same database:

- SQLite is already in WAL mode with a busy timeout.
- The cleanup runner cleans only tasks captured by its own process, so the two never clean the
  same card twice.
- Each process has its own hub, so neither sees the other's changes live. The TUI's timed
  refresh picks up web changes; the web UI picks up TUI changes on its next fetch.

## Screens

### No day yet

A centered card: the tagline ("whatnow · executive function for the rest of us"), "Good morning.
Start your day in the browser:", and the `serve` address. It
reloads every 5 seconds and switches to Today once the day has a plan.

### Goodnight

Once the day is wrapped, the list pane shows a card instead: the tagline, "Goodnight. Day's
done.", the win line (things done and meetings survived, as the web's Day's-done card counts
them), the day's reflection, the note for tomorrow, and "You're off the hook". Only `a`, `u`,
`c`, `?`, `r`, and `q` work; there's no list to act on, and no wrap-up banner. At midnight it gives
way to the good-morning card.

### Today

```
╭─ whatnow ── Tue Oct 6 ── 3 of 7 ────────────────────────────╮
│ 9a        12p     ▌     3p        6p              ┆ 5:30 PM │
│   [standup]       ▌   [1:1]                                 │
├──────────────────────────────────┬──────────────────────────┤
│ MUST                             │ chat                     │
│ ▸ ☐ buy milk                     │ > what next?             │
│   ☐ call mom  ⛓ 1                │ Call mom, the milk can   │
│ SHOULD                           │ wait.                    │
│   ☐ fix the gutter               │                          │
│ not planned                      │                          │
│   ☐ renew passport               │                          │
├──────────────────────────────────┴──────────────────────────┤
│ space done · w what now · a add · c chat · ? keys           │
╰─────────────────────────────────────────────────────────────╯
```

- **Header:** the date, the done count (tasks done today out of those plus the open tasks on the
  list), and the day strip.
- **Day strip:** a Go port of `strip()` in `web/src/lib/time.ts`, laid out in whole columns:
  events as green blocks, the now line as a hot-pink bar, and the end of day as a dashed rule
  whose label stays inside the right edge.
- **Task list:** grouped by plan tier (MUST, SHOULD, COULD), each tier header in its sticker
  color, then a dim "not planned" group for eligible tasks the plan leaves out. ⛓ marks a task
  waiting on an open blocker. A task marked done leaves the list.
- **Chat pane:** on the right, open from the start on a terminal 90 columns or wider. Below 90
  columns it is hidden until `c`, and then replaces the list. Narrowing the terminal below 90
  closes it unless chat has focus, so the list the keys act on never disappears.
- **Footer:** a one-line key hint; `?` shows the full list.
- **Wrap-up banner:** when `wrapDue` holds (within an hour of the end of day), a banner says to
  wrap up in the browser.
- **Toasts:** each change shows a toast for a few seconds at the bottom, such as
  "Done: buy milk · u to undo". Errors show in red.

## Keys

| Key | Action |
|---|---|
| `↑`/`↓`, `j`/`k` | Move the cursor |
| `space` | Mark done |
| `w` | What now: moves the cursor to the pick and shows its reason in a callout. `n` passes on it and asks again, with an optional reason. |
| `a` | Add: one line of text, captured and cleaned up in the background like the web's Capture |
| `p` | Push to tomorrow |
| `x` | Drop |
| `u` | Undo your last change, as the web's toast Undo does |
| `c` | Open and focus chat. `enter` sends, the reply streams in, `esc` returns to the list (and hides chat below 90 columns). |
| `r` | Refresh now |
| `?` | Show all keys |
| `q` | Quit |

Out of scope: editing a task's fields, picking blockers, All tasks, settings, onboarding, the
rituals, and nudges.

## Look

The web's sticker look, translated. The colors come from `web/src/lib/tokens.css`: tier fills
(must coral, should sky, could white), the yellow chip, the purple primary, the hot-pink now
line, and the green event fill. Panes have rounded borders with an offset shadow edge drawn in
ink. Lip Gloss adaptive colors keep it readable on light and dark terminals.

## Data flow

- **Snapshot:** one function reads what the screen shows: `planner.Today`, the tasks, today's
  events, and the blockers. It returns a single message, and the view renders from it.
- **Reloads:** on start, after each action, on any message from the hub, and on a 5-second
  tick. The tick catches changes from another process and moves the now line.
- **Hub:** the TUI subscribes to this process's hub. A command reads one message and returns
  it to the update loop, which starts the next read. `chat.delta` and `chat.message` drive the
  chat pane; the rest trigger a reload.
- **Model calls:** what now and chat run as commands with a spinner, so the keys keep working.
- **Midnight:** the next reload after the date changes finds no day and shows the no-day
  screen.

## Errors

- A failed action shows a red toast with the error.
- With no model set up, the planner and agent already answer: what now falls back to the first
  free task with a note saying why, and chat replies that no provider is set up.
- If a reload fails, the last snapshot stays on screen, marked "stale", until a later reload
  succeeds.
- The shared packages log with `log.Printf`. While the TUI runs, the log goes to `tui.log` next to
  the database, because stderr is the same terminal and lines would land on the screen.

## Testing

- **Day strip:** table tests at fixed widths, covering the same cases as `time.test.ts` plus
  the end-of-day label at the right edge.
- **Update loop:** tests send keys and messages to the model, backed by a real service on a temp
  database and the fake model from `internal/llm/fake`. They cover: space marks done and `u`
  restores it, `w` moves the cursor to the pick, `n` asks again excluding it, `a` captures and
  the cleaned tasks appear, and streamed deltas end in one chat message.
- **Golden views:** `teatest` snapshots of the rendered screen at 80 and 120 columns, with
  colors stripped, so layout changes produce readable diffs.
- **`internal/app`:** an open-and-close test on a temp database. `serve` keeps its existing
  coverage through the HTTP tests.
- **Coverage:** 100%, except code a test can't reach on its own: `tui.Run`, which needs a real
  terminal; the `tea.Tick` callbacks, which fire after seconds; and error returns that share one
  database with an earlier call (`load` after its first, `commit`'s activity read).
