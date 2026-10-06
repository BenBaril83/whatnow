# whatnow web UI, part 2: the morning and evening rituals, and All tasks

The second of three UI slices. It closes the daily loop: a good-morning check-in, a wrap-up, and an
All tasks page with an edit form. It builds on part 1, `2026-10-05-whatnow-web-ui-today-design.md`:
the same sticker look, client architecture, refetch-on-event data flow, and undo toasts. The parent
spec, `2026-10-05-whatnow-design.md`, describes both flows' server behavior.

## Why the rituals are their own pages

Morning is when overwhelm peaks. Seeing the plan, the day strip, and chat all at once is the noise
the check-in exists to cut through. So `/morning` and `/wrap` are calm pages that show one step at a
time. Two escape hatches stay: a slim capture bar, so a stray thought is never lost mid-ritual, and
the chat tab, collapsed by default, for things like "push the dentist to Thursday."

Inline cards on Today were rejected because they keep everything else in view. A modal over Today
was rejected because it blocks capture and chat.

## Routes and navigation

SvelteKit route groups give two shells:

| Group | Routes | Shell |
|---|---|---|
| `(app)` | `/`, `/tasks` | The Today header and the chat dock |
| `(ritual)` | `/morning`, `/wrap` | Progress dots, a slim capture bar at the bottom, and the chat tab collapsed on the right |

`/settings` stays where it is.

- **Header ⋯ menu:** All tasks, Wrap up early, and Settings. Settings moves into the menu.
- **Wrap up button:** shows in the header from an hour before the end-of-day setting until the day
  is wrapped.
- **Into the morning:** Today with no plan redirects to `/morning`. The Start my day card is
  deleted.
- **Out of the morning:** `/morning` on a day that has already started redirects to Today, so a
  stale tab or a double click can't start the day twice.
- **Out of wrap-up:** `/wrap` on a wrapped day redirects to Today.
- **Midnight rollover:** a tab open across midnight still shows the previous day. When the window regains
  focus, the page refetches `/today`. If the date changed and today has no plan, it goes to
  `/morning`.

## The triage list

Both rituals use one component for unfinished tasks.

- Each task has a roll over / push / drop control, prefilled from the model's suggestion, with the
  suggestion's one-line reason under it.
- A task without a suggestion rolls over, the same as the server's default.
- Choosing push shows a date field. It's prefilled with the suggested date, or with tomorrow when
  there is none.
- When `review.fallback` is set, a "rough suggestions" tag says code made them.
- The payload is one `{task_id, action, until}` per task. `until` is set only for a push.
- There is no undo for a ritual's triage. The list is the confirmation, and starting or wrapping a
  day can't be undone anyway.

## Good morning (`/morning`)

0. **Greeting:** "Good morning ☀︎" while `GET /morning` loads. That call runs the main model and
   takes several seconds, so the greeting covers the wait.
1. **Reconcile:** only when `review` is non-null and has unfinished tasks. It starts with the
   previous day's win, such as "Last time you got 4 things done," then shows the triage list.
2. **Brain dump:** "What's on your plate?", a big optional textarea, every morning. **Skip** clears
   it. Without it, a first morning has nothing to plan, and a task list typed into the mood box
   only reads as a mood.
3. **Pace and mood:** the five pace chips, and an optional "How are you feeling?"
4. **Plan reveal:** "Making your plan…" while `POST /today/start` runs with the pace, the mood
   note, the triage, and the dump. The server saves the dump as one captured card and runs cleanup
   on it right away, before planning. If cleanup fails, the raw card is planned as it is and goes to
   the background runner like any capture. A `409` comes after the dump is saved, so the page clears
   the dump before retrying. Then the plan's `opening` line, large, above a compact list of each tier's
   titles. **Let's go** opens Today.

## Wrap up (`/wrap`)

1. **Done:** the win line and the tasks finished, from `GET /today/wrap`.
2. **Unfinished:** the triage list. It is skipped when nothing is unfinished.
3. **Note for tomorrow:** optional. **Wrap up** sends `POST /today/wrap` with the triage and the
   note.
4. **Goodnight:** the day's summary from the response, if the model wrote one. **Goodnight** opens
   Today.

**After wrap-up,** Today's tiers give way to a Day's-done card: the win line, the summary, and the
note for tomorrow. What now? and the Wrap up button hide. Capture, chat, the unplanned tray, and
Done today keep working, so late thoughts land in the tray for the morning.

## Errors in the rituals

- **`409` on start or wrap:** a task in the triage changed elsewhere, for example it was finished
  in another tab. Refetch the review and say "Something changed, here's the fresh list." Choices
  for tasks still on the list are kept.
- **Any other failure:** a red toast, and the ritual stays on its step.
- **No model set up:** both review endpoints and start still work through the code fallback. The
  "rough suggestions" tag and the rough plan tag say so.

## All tasks (`/tasks`)

- A chip row filters the list. Exactly one chip is active:

  | Chip | Shows |
  |---|---|
  | **Open** (default) | Open tasks |
  | **Future** | Open tasks whose `not_before` is after today |
  | **Blocked** | Open tasks with an open blocker |
  | **Done** | Done tasks |
  | **Dropped** | Dropped tasks |
  | **All** | Every task |

- Sorted by due date, earliest first and undated last, then newest first.
- Cards match Today's, plus badges for the due date and the not-before date.
- A dropped card has a **Restore** button that sets it back to open, with an undo toast.
- Clicking a card opens the edit form. Today's ⋯ card menu gains **Edit**, which opens the same
  form.

## Edit form

A sheet over the current page. Escape and ✕ close it without saving.

- **Fields:** title (required), notes, size (S, M, L, or none), energy (low, med, high, or none),
  feeling, due date, and not-before date.
- **Blockers:** a "Blocked by" list, each with ✕ to remove it, and an "Add a blocker" picker of
  the other open tasks. Each change is its own write, with its own undo toast.
- **Raw text:** read-only, below the form.
- **Save:** `PUT /tasks/{id}` with every field. The task's existing refs are sent unchanged. It shows
  an undo toast and closes the sheet.
- Editing not-before here is an ordinary edit. Push counts count only the push action.
- **New task:** **+ Task**, beside Today's capture box and in the All tasks header, opens the same
  sheet empty. Save sends `POST /tasks` with the fields and no raw text, so the model never touches
  it. Blockers picked before saving are added once the task exists. It shows an "Added" undo toast,
  and the task lands in the tray until a replan.

## Toast countdown

A toast that vanishes on an unseen timer is stressful. Each toast shows a faint fill that sweeps
from left to right over its lifetime, and the toast leaves when the fill reaches the end. Hovering
or focusing a toast pauses both the fill and the timer, so reaching Undo is never a race. Leaving
it resumes from where it paused.

## Testing

**Vitest, for logic modules only:**
- `triage.ts`: choices prefilled from suggestions; a task with no suggestion rolls over; choices
  survive a refetched review for tasks still on it; the payload carries `until` only for a push.
- `tasks.ts`: each filter chip, and the sort order.
- `taskForm.ts`: a task round-trips through the form; empty fields map to `null`; a blank title is
  rejected before any request.
- `Toasts`: a paused toast outlives its lifetime; resuming finishes the remaining time only.
- The rollover rule: a changed date with no plan means go to `/morning`; the same date, or a day
  that has started, means stay.

Components get no unit tests. Playwright covers them in 5d. Until then, smoke-test by hand
against `whatnow serve` with a throwaway `-db`, including a skipped wrap-up followed by a morning
reconcile.
