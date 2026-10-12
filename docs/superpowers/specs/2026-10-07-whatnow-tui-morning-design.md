# whatnow TUI: good morning

The TUI starts the day itself, instead of sending the user to the browser. It's the same ritual as
the web's `/morning` (`2026-10-06-whatnow-web-ui-rituals-design.md`): reconcile, brain dump, pace and
mood, plan reveal. When an integration is on, a calendar step sits between the dump and pace
(`2026-10-12-whatnow-integrations-design.md`). It shows one step at a time, in the TUI's sticker cards. Wrap-up stays in the
browser for now.

## Starting the day

The TUI starts the day the way `POST /api/v1/today/start` does, as it already does the API's other
writes through `commit`. It saves a non-blank dump as one captured card in a user batch and
announces that batch. Then it cleans the card up right away, and if cleanup fails it hands the raw
card to the background runner. Then it calls `Planner.StartDay`. The capture stays duplicated
rather than shared through `internal/app`, because the HTTP tests watch the fallback through
`Deps.OnCapture`, and the app's runner gives them no seam.

## Screens

The good-morning card, shown when today has no plan, holds the tagline,
"☀ Good morning.", and "enter to start your day". It reloads every 5 seconds. A day started
in the browser switches the TUI to Today from any step short of the reveal.

The ritual waits for **enter** rather than starting at midnight on its own, because `MorningReview`
is a main-model call and a TUI left open overnight shouldn't make one unasked.

0. **Greeting:** "Looking back…" with the spinner while `Planner.MorningReview` runs.
1. **Reconcile:** only when the review has unfinished tasks. It starts with the last day's win
   line ("Last time you got 4 things done."), then the triage list:

   ```
   ▸ fix the gutter     ‹ push → Sat ›
       rainy all week
     call the bank      ‹ roll over ›
       still due Friday
     learn banjo        ‹ drop ›
       untouched for 9 days
   ```

   - `↑`/`↓` move, and `←`/`→` cycle roll over, push, and drop. Each task starts on the model's
     suggestion, or on roll over when it has none.
   - The reason sits dim under each task. A push shows its date: the suggested one, or tomorrow
     when there's none. There's no date entry; for a different date, push it later in chat.
   - When `review.Fallback` is set, a "rough suggestions" tag says code made them.
   - `enter` moves on.
2. **Brain dump:** "What's on your plate?" in a multi-line box (a Bubbles textarea). `enter` adds a
   line, `tab` moves on, and `esc` clears the box and skips.
3. **Pace and mood:** the chosen pace by name, with a dot for each of the five levels
   (`‹ steady › ●●●○○`). All five names don't fit across the card. `←`/`→` picks, starting on
   steady (3). Below it, a one-line "How are you feeling? (optional)". `enter` starts the day.
4. **Plan reveal:** "Making your plan…" with the spinner while the start runs. Then the plan's
   opening line, and each tier's titles in its tier color. `enter` opens Today.

`ctrl+c` quits from any step. `q` doesn't quit, since it's a letter you might type in the dump or the
mood. There's no going back a step, and no capture or chat mid-ritual. The brain dump is where a
stray thought goes.

## Errors

- **Conflict on start:** a task in the triage changed elsewhere. The dump was already saved, so
  the box is cleared. The TUI fetches the review again and goes back to reconcile, keeping the
  choices for tasks still on the list, with "Something changed, here's the fresh list." If nothing
  is left to triage, it goes to pace with the same notice. A review that lands after the day was
  started elsewhere is dropped.
- **Any other failure:** a red toast, and the ritual stays on its step. A start that fails after
  the dump was saved clears the box, so a retry doesn't save it twice. A failed review returns to
  the good-morning card, so a start never sends an empty triage that rolls everything over.
- **No model set up:** the review and the start fall back to code, as on the web.

## Testing

- **Update loop:** tests drive the ritual with keys on a real service and the fake model. They
  cover these cases:
  - A day with an unwrapped yesterday reconciles and starts with the chosen triage.
  - A first morning skips reconcile.
  - The dump's tasks land in the plan.
  - `esc` skips the dump.
  - A conflict returns to reconcile with the choices kept.
  - A day started elsewhere mid-ritual switches to Today.
- **Triage choices:** prefill from suggestions, the roll-over default, cycling, the push date, and
  keeping choices across a fresh review.
- **Golden views:** the good-morning card, reconcile, and the reveal, at 80 columns.
- **The start:** a dump that cleanup fails on is planned raw.
