# whatnow: design

## Purpose

An executive-function helper for people with ADHD. It holds your day loosely, not as an hourly
schedule: what you must do, what you should do, and what you could do if you have the space. Its
core is an emotionally intelligent, compassionate assistant that reads how you feel about your work
and helps you get through the day.

Priorities, in order:

1. Useful daily for its author.
2. A strong live hackathon demo.
3. Easy for others to install and use.

## Scope

**MVP:** a locally run web app with no integrations. You paste or type your stuff, and the LLM turns
it into tasks and events.

**After the MVP, explicitly out of scope here:**
- A `whatnow add` CLI, as a client of the API.
- Integrations (calendar, Jira, PR review queue). Each declares the settings fields it needs; the
  contract is in `2026-10-12-whatnow-integrations-design.md`.
- Native OS notifications.

## Principles

- **Code establishes facts. The LLM makes every judgment.** Code computes free time, blocked tasks,
  not-before dates, due dates, and push counts. These reach the LLM as annotations, never as
  filters. Ordering, tiering, pacing, and reading emotion all belong to the LLM. Code rejects only
  impossibilities.
- **Low friction over confirmation.** The agent acts directly, and every change it makes can be undone.
- **A stable plan, with a live "what now?"** The plan changes only when something warrants a
  replan. "What now?" always reasons over the current state.
- **Capture never depends on the LLM.** Raw input is saved before any LLM call happens.
- **Single user, local only.** No accounts. The database is SQLite.

## Stack

A single Go binary: `whatnow serve`, plus bare `whatnow` (the terminal companion, which serves too), with a SvelteKit frontend built by `adapter-static` and
embedded with `go:embed`. SQLite through `modernc.org/sqlite`, which is pure Go with no cgo. In
development, the Vite dev server proxies `/api` to Go. Distribution is unsigned Mac binaries on
GitHub releases, built and uploaded by `just release`, which also points the Homebrew formula in
`jameynakama/homebrew-tap` at them. Binaries are never committed: each build would stay in git
history and every clone would download it. The formula is a formula rather than a cask because a
cask quarantines its download, which Gatekeeper then blocks for an unsigned binary, and only a
formula can offer `brew services`.

## LLM providers

The user supplies their own key. There are two providers behind one interface:

- **Anthropic** (the default), through `anthropic-sdk-go`.
- **OpenAI-compatible**, with a configurable base URL. This also covers Ollama, LM Studio,
  OpenRouter, and internal gateways.

Each provider has two model settings:

| Setting | Used for | Claude default | OpenAI default |
|---|---|---|---|
| Main | Agent, plans, "what now?" | `claude-sonnet-5-5` | `gpt-5.5` |
| Light | Cleanup, day summaries | `claude-haiku-4-5` | `gpt-5.4-mini` |

An OpenAI-compatible server other than OpenAI's needs its own models.

Structured replies use the provider's JSON-schema output (`output_config.format` on Claude,
strict `response_format` on OpenAI), never a forced tool call: Claude Sonnet 5.5 and Opus 5.5
reject forced `tool_choice`. Constrained decoding writes fields in schema order, so a schema
lists fields in the order the model should decide them.

Calls to Claude's main-tier models should opt into server-side refusal fallbacks. These need
the beta endpoint and do nothing on OpenAI.

Subscriptions (Claude Pro, Max, Team, or Enterprise, and ChatGPT plans) cannot power this app.
Anthropic forbids using consumer OAuth credentials in third-party tools, and ChatGPT plans don't
include API access. That rules out shelling out to `claude -p` or `codex exec` as a provider: it
would violate those terms once the app is distributed to others.

## Data model

- **Task**
  - Text: `title`, `notes`, `raw_text` (the user's original words, always kept)
  - Judgment: `size` (S/M/L), `energy` (low/med/high), `feeling` (a short extracted note, such as
    "dreading it, needs research first")
  - Timing: `due_on`, `not_before`
  - State: `status` (open/done/dropped), `refs` (links and keys such as `FLEX1-666`),
    `created_by` (user/agent), timestamps, soft delete
- **Blocker:** task A is blocked by task B.
- **Event:** `title`, `starts_at`, optional `ends_at`, `raw_text`, soft delete. Events are anything
  at a fixed time.
- **Day:** `date`, `pace`, optional `mood_note`, `started_at`, `wrapped_at`.
- **Plan:** belongs to a day and is versioned. It records its trigger (morning, pace, edit, agent,
  user, or move, a task moved by hand). It holds entries of (task, tier, one-line rationale) plus an opening message in the
  persona's voice.
- **Activity:** `actor`, `action`, `entity`, before and after snapshots, and `batch_id`. Writes made
  in one agent turn or one cleanup run share a batch.
- **Message:** chat history, keyed by date rather than day, so chat works before the day is started.
- **DaySummary:** a short summary of each day, written by the light model.
- **Settings:** provider, models, base URL, assistant style (500 characters at most), about me
  (1000 characters at most), nudge toggles, end-of-day time, and alert lead time. API keys go in
  the OS keychain through `go-keyring` and are never stored in SQLite. Hosted mode seals them in the
  user's own database instead (`2026-10-22-hosted-whatnow-design.md`).

Pace has five levels: *running on fumes / gentle / steady / full steam / go hard*. The labels can
change; there are always five levels.

## Architecture

| Package | Responsibility |
|---|---|
| `store` | SQLite access, embedded migrations, query functions |
| `service` | The only write path. Records activity, assigns batches, handles undo. REST handlers and agent tools both call it. |
| `facts` | Pure functions: free gaps, blocked set, push counts, annotations. No I/O. |
| `llm` | Provider interface, with Anthropic and OpenAI-compatible implementations. Messages, tool calls, streaming. |
| `prompt` | Assembles the layered persona and renders facts into context |
| `cleanup` | One structured LLM call per raw card. Applies its result as one batch. |
| `planner` | Structured calls for plans and "what now?". Validates, retries, falls back. |
| `agent` | The chat agent loop. Its tools wrap `service` and `planner`. |
| `httpapi` | REST under `/api/v1/`, and an SSE stream at `/api/v1/stream` |
| `web` | The SvelteKit app |

Cleanup and planning are one-shot structured calls. Only chat runs an open-ended agent loop.

### Prompt layers

Prompts are built in a fixed order. The order also keeps the stable prefix cacheable.

1. Core instructions: what the assistant does, its tools, and its rules. This layer takes precedence.
2. Assistant style, under a heading that limits it to tone and manner.
3. About me.
4. Live context: day summaries, today's chat, and current facts.

Style can change how the assistant sounds. It cannot change what the assistant does.

## Flows

### Capture and cleanup

1. Text from the capture box is saved at once as a raw task. Like every write, it lands in a batch,
   and SSE sends `batch.applied` with that batch's activity.
2. Cleanup runs in the background with the light model. From the text, it extracts tasks, events,
   blockers, refs, size, energy, and feeling, and it splits one input into several items when it
   should. For example, "do FLEX1-666 today, it feels difficult and needs pubsub research first;
   also review FLEX1-420" becomes three tasks, with the pubsub research blocking FLEX1-666.
   A task whose ticket key (such as `FLEX1-666`) an open task already has joins that task instead
   of being made again: its feeling is added there, blockers point there, and a note that joined
   is deleted. Only ticket keys join; a looser match would need the model's judgment, and a wrong
   guess would quietly swallow a note. A done task doesn't count, since its work came back.
3. The result is applied as one batch. SSE sends `batch.applied`, and the UI shows a "here's what I
   did [undo]" toast.
4. If there's no key, or the provider fails, the raw card stays as it is and remains fully usable.

### Good morning

1. **Reconcile since the last session.** If the last day has no `wrapped_at`, show everything still
   open since then, with roll over / push / drop actions and the agent's suggestions prefilled. A
   skipped week gets one pass, not seven.
2. Take an optional brain dump. It's captured and cleaned up before planning, so its tasks land in
   the plan rather than the tray.
3. Ask for pace, plus an optional note on how they're feeling.
4. Generate the plan and reveal it with the persona's opening message.

### Planning

`facts` computes free gaps from events, blocked tasks, not-before dates, due dates, push counts, and
annotations such as "too big for this gap," "blocking a must," and "pushed 4 times." The main model
assigns tiers and writes a rationale for each entry.

- Free time runs from now to the end-of-day setting. An event without an end counts as 30 minutes.
- "Too big" means an M task with no free gap of an hour, or an L task with none of three hours. It
  is never added once no free time is left.
- A push sets `not_before`. Push counts count pushes only; an ordinary edit to `not_before` isn't one.
- A plan may leave eligible tasks out. They wait in the unplanned tray along with new ones.

Blocked tasks can appear in tiers, shown with a blocker marker. A blocked must is still a must, and
it raises the urgency of whatever blocks it.

A task is **eligible** if it is open, not soft-deleted, and has no `not_before` in the future.
Validation rejects a plan that references any ineligible or unknown task. Plans have tiers, not
times, so there is nothing for them to collide with.

On rejection, the planner retries once with the errors attached. Only a rejected reply is retried:
a provider error, or a model call that runs past a minute, goes straight to the fallback. If the retry also fails, it builds
a **fallback plan** in code. This is the one deliberate exception to "the LLM makes every judgment,"
and it exists so a bad model day still produces a valid plan:
- due today or overdue: must
- due later: should, ordered by due date
- undated: could, ordered by creation time

**What triggers a replan:**
- a pace change,
- a task becoming due today, or being created that way,
- dropping a must,
- adding or removing a blocker that involves a must, or
- the agent calling `replan(reason)`.

Starting a day can't be undone, since its plans would be orphaned. A pace change can, and undoing
it replans. Completing a must just marks it done. Every other new task waits in the unplanned tray until the
next replan. The agent can suggest slotting one in, and Today's **Replan** button makes a fresh plan
on request. A wrapped day is never replanned, by any of these.

### What now?

Returns one pick, with a reason, from all eligible tasks in the current state, including tasks in
the unplanned tray. Validation rejects a pick that is unknown, ineligible, or blocked, and retries
once. The fallback is the first unblocked task in fallback-plan order.

The card offers **[done]** and **[not this one]**. "Not this one" asks again with that task excluded
for this answer only. Its optional reason is appended to the task's `feeling` note.

### Wrap up

1. Show what got done.
2. Decide what happens to each unfinished task.
3. Take an optional note for tomorrow.
4. Write the day summary for the planner, then the reflection for the user (see
   `2026-10-06-whatnow-reflection-design.md`).

If wrap-up is skipped, the next good morning reconciles and writes the summary instead, with no
reflection.

- Unfinished means in the day's latest plan, open, not deleted, and not pushed past today.
  Unplanned tasks aren't reviewed; they stay in the tray.
- The main model prefills roll over / push / drop. Anything it leaves out rolls over.
- The user's choices apply as one batch, so one undo takes them all back. A push without a date
  goes to tomorrow. A choice for a task that's no longer unfinished (finished in another tab, say)
  rejects the whole wrap-up before anything is written.
- Wrapping, like starting, isn't undoable.
- The summary is plain text from the light model. It's written even if the user leaves mid-call. If
  it fails, the day still wraps, with an empty summary.
- A wrapped day is never replanned.
- The next plan's context carries the last day's summary and note. A start that's retried after
  the last day was already closed ignores the triage it carries.

### Nudges

These run client-side, once the user grants notification permission. The tab receives event changes
over SSE and reschedules its timers. If permission is denied, nudges appear in-page only. Each nudge
type can be turned off in settings.

- **Before each event:** sent at the alert lead time. "Standup in 10, find a stopping point."
- **When an event ends:** "Standup's done. What now?" Clicking it opens a fresh "what now?" answer.
  The LLM runs only on that click.
- **Late in the day:** once, two hours before the end-of-day time, if an open must due today has no
  activity today.

Nudges only fire while the tab is open. Background tabs throttle timers to about once a minute.
Both limits are acceptable for the MVP.

## Agent

**Tools:**
- Tasks: `list_tasks`, `create_task`, `update_task`, `complete_task`, `drop_task`, `push_task`.
  Putting a task off goes through `push_task`, never an edit to `not_before`, so push counts see it.
- Blockers: `add_blocker`, `remove_blocker`
- Events: `create_event`, `update_event`, `delete_event`
- Planning: `replan(reason)`, `what_now`
- Pace: `set_pace(level, note)`
- Undo: `undo_last`, which reverts the most recent agent or cleanup batch, leaving out the current
  reply's own batch: "undo that" means the last reply. Undo buttons in the UI carry their own batch
  ID.

Tool schemas are strict on both providers, with no nullable fields: an empty string means unset.
The update tools take a list of `{field, value}` changes, and an empty value clears a field.

**Context:** the prompt layers, day summaries from the last 7 days, today's full chat, and current
facts. Facts are re-rendered every turn and never kept in the history. When today's chat outgrows
its budget, the oldest whole turns are folded into a running summary by the light model. If the
fold fails, the whole chat is sent. The context also lists the coming week's events with their IDs,
so the agent can change one.

History is text only. Tool calls and their results live only inside the turn that made them; the
facts show what they changed. Failed replies and replies with no text are never replayed: both
providers reject an empty message.

**Each turn:**
- Text streams to the UI over SSE, and tool calls appear as inline chips.
- All successful writes in the turn share one batch, so one turn gets one undo. If a later tool
  call fails, the earlier writes stay applied and stay in that batch.
- The loop stops after 10 tool rounds.
- One turn runs at a time; a second message meanwhile gets `409`. The turn finishes even if the
  client leaves.
- SSE carries `chat.delta` (text pieces), `chat.tool` (chips), and `chat.message` (each stored
  message). Deltas are best-effort, since the hub drops messages for a slow client; `chat.message`
  is authoritative.
- The turn's batch is published once, at the end. A reply whose writes all failed has no batch.
- The replan watcher subscribes to batch messages only, so chat traffic can't crowd one out of
  its buffer.

**Errors:**
- Tool and validation errors go back to the model as tool results.
- Provider errors (bad key, rate limit, outage) appear in chat as a plain message with a link to
  settings.

## Web UI

- **Today (home)**
  - Header: the date, a pace chip (changing it triggers a replan), and a countdown to the next event.
  - A "What now?" button.
  - The must / should / could plan. Cards show size and energy badges, the feeling note, and a
    blocker marker. The rationale shows on hover or tap.
  - The unplanned tray.
  - An events timeline with a "now" line.
  - "Done today," collapsed by default. Its header carries the win, such as "✓ 6 things done,
    3 meetings survived."
  - A capture box that is always visible.
  - A chat dock: a side panel on desktop, a bottom sheet on narrow screens.
- **Good morning** and **wrap up** flows, as described above.
- **All tasks:** every task that isn't soft-deleted, filterable by status, future, and blocked.
  Dropped tasks can be restored. Clicking a card
  opens the edit form, with the raw text shown below it. In the edit form, only the title is required.
- **Settings:** provider, key, models, Test connection; style and about me; nudge toggles,
  end-of-day time, alert lead time.
- **Onboarding** (first run, when no provider is set up):
  1. Pick a provider.
  2. Enter a key, or have it prefilled from `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`.
  3. Test the connection.
  4. Optionally add style and about me.
  5. Start the first good morning.

## Testing

**Seams:**
- An injected clock, used everywhere.
- The LLM provider interface, with a scripted fake.
- go-keyring's `MockInit`, standing in for the real keychain.

**Go:** stdlib `testing`, table-driven tests, and `go-cmp`.

| Package | Coverage |
|---|---|
| `facts` | Exhaustive: event gaps, back-to-back events, blocker chains, not-before boundaries, push counts, DST and midnight edges |
| `service` | A real SQLite database in a temp dir: activity logging, soft delete and restore, batch undo restoring everything exactly |
| `planner` | Each invalid-plan case, then one retry, then the fallback |
| `agent` | One batch per turn, the round cap, tool errors returned to the model |
| `cleanup` | Recorded real responses replayed as fixtures |
| `httpapi` | `httptest`, including the SSE events each action emits |

**Prompt evals (`just eval`):** opt-in, against the real API, and not run in CI. A small set of
real inputs with structural assertions. For example:
- The FLEX1-666 paste produces three tasks with the correct blocker.
- An "I'm fried" pace does not get the hardest task as its pick.

Run the evals whenever prompt templates, default models, or structured-output schemas change.

**Web:** Vitest for client logic (alert timers, countdowns, SSE store updates). Playwright for
three flows (capture to split to undo, good morning, what now?), run against the real binary built
with an `e2e` build tag that wires in the fake provider.

**Coverage:** a high ceiling, not a hard 100%. Don't contort code or tests to reach an awkward
line.
