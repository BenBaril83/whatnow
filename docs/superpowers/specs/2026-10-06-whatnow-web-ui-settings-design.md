# whatnow web UI, part 3: settings, onboarding, nudges, and the day strip

This third UI slice gives whatnow a first-run path, the remaining settings, nudges, and a day
strip that shows the time. It builds on part 1 (`2026-10-05-whatnow-web-ui-today-design.md`) and
part 2 (`2026-10-06-whatnow-web-ui-rituals-design.md`): the same sticker look, client
architecture, and refetch-on-event data flow. The parent spec, `2026-10-05-whatnow-design.md`,
describes settings, onboarding, and nudges at the product level.

| Slice | Contents |
|---|---|
| **5c (this spec)** | Full settings, delete everything, onboarding, nudges, the day strip fix |
| 5d | The `e2e` build tag, a fake provider, and Playwright for the parent spec's three flows |

Playwright is its own slice because the fake provider has to give cleanup, plans, and What
now believable structured replies, which is as much work as the rest of this slice.

## Settings

### Backend

`settings.Settings` gains three fields:

| Field | JSON | Default | Rule |
|---|---|---|---|
| Nudge toggles | `nudges: {before_event, event_end, late_day}` | all `true` | |
| Alert lead time | `alert_lead_minutes` | `10` | 1 to 60 |
| Onboarded | `onboarded` | `false` | set when onboarding finishes or is skipped |

`Load` already unmarshals the saved document over the defaults, so settings saved before these
fields existed pick up the defaults.

### The page

`/settings` is one page with four sections and one **Save**:

- **Connection:** provider, key, main and light models, base URL, and **Test connection**, as
  they are now.
- **You:** style and about me, as textareas with live character counters showing their limits (500 and
  1000 characters). Each has one line on what the model does with it.
- **Day:** end-of-day time and alert lead time in minutes.
- **Nudges:** the three toggles, and a permission row:

  | `Notification.permission` | Row shows |
  |---|---|
  | `default` | **Turn on notifications**, which calls `Notification.requestPermission()` |
  | `granted` | "On" |
  | `denied` | "Blocked in your browser. Nudges show in the page instead." |

Save writes the key first when one was typed, then `PUT /settings`. If the second write fails, the
page refetches the key sources, so the key-status note stays accurate and typed edits survive.

### Delete everything

A red-bordered **Danger zone** at the bottom of `/settings`.

- One line lists what will be deleted: every task, event, day, plan, chat, and summary, the settings, and the
  saved keys.
- The **Delete everything** button stays disabled until the field next to it reads
  `delete everything`.
- `DELETE /api/v1/data`:
  1. In one transaction, deletes all rows in every table except the migration record. The
     table list comes from `sqlite_master`, so a table added later is wiped without anyone
     remembering to add it.
  2. Removes both keychain keys. Keys from the environment can't be removed; onboarding finds
     them again.
  3. Publishes `data.reset`.
  4. Answers `204`.
- Every open tab handles `data.reset` with a full reload of `/`, which lands on `/welcome`.

## Onboarding (`/welcome`)

### Getting there

The root layout redirects to `/welcome` after settings load, when all of these hold:

- `onboarded` is false.
- No provider is saved.
- The path is not `/welcome` or `/settings`.

An install that already has a provider never sees onboarding. "Skipped" lives in settings rather
than `localStorage`, so a new browser or cleared storage doesn't bring the welcome back.

`/welcome` has its own bare shell: progress dots, no capture bar, and no chat. Chat can't answer
without a provider.

### Steps

Every step has **Back** and **Next**, and a quiet **Set up later** that saves `onboarded` and opens
Today.

1. **Provider:** a one-line description of what whatnow does, then two sticker cards, Anthropic and
   OpenAI-compatible.
   - Anthropic shows its default models, editable.
   - OpenAI-compatible asks for an optional base URL and the main and light models. Both model
     fields are required and have no defaults, since they depend on the server.
2. **Key:**
   - When the provider's key comes from the environment, the step says "Found your key in
     `ANTHROPIC_API_KEY`" (or `OPENAI_API_KEY`). A "use a different key" link opens the field.
   - Otherwise a password field.
   - **Next** saves the key, then the settings.
3. **Test:** runs the connection test on arrival.
   - Success turns **Next** on.
   - Failure shows the error. **Back** and **Set up later** are the ways out; there is no
     continuing on a broken connection.
4. **About you** (optional): style and about me, with the same counters and explanations as
   Settings. **Skip** or **Next**, which saves them.
5. **Nudges:** one line on each of the three kinds, then **Turn on nudges**, which asks for
   permission, or **Not now**. The toggles stay on either way, so without permission nudges show
   in the page.
6. **Ready:** saves `onboarded` and shows **Start my day**, which opens `/morning`.

Each step saves as it goes, so leaving partway keeps what was entered. A reload after step 2 has
saved a provider lands on Today rather than back in onboarding; Settings has the rest. This avoids
building resume-where-you-left-off.

## Nudges

### What's due

`nudges.ts` is pure. From the day's events and tasks, the settings, and the date, it returns
`{key, at, kind, text}` for each nudge:

| Kind | At | Text | Skipped when |
|---|---|---|---|
| Before an event | `starts_at` minus the lead time | "Standup in 10 min, find a stopping point." | that time is before the event was created |
| When an event ends | `ends_at` | "Standup's done. What now?" | the event has no end |
| Late in the day | end of day minus 2 hours | names the first such must | no open must due today has `updated_at` before today |

Kinds toggled off in settings are left out. `key` is the kind plus the event ID and time, or the
date for the late-day nudge.

The late-day rule reads "no activity today" as "not updated today." Every edit, push, and tick
touches `updated_at`, and the server has no per-task activity query. An agent's edit counts as
activity, which is close enough.

### Firing

`live` keeps `lastTick`, set when the page loads. On each minute tick and on window focus, it
fires every due nudge with `lastTick < at <= now`, then sets `lastTick` to now. So a nudge fires
once, a reload never re-fires old ones, and a throttled background tab still fires a nudge that
fell inside a long gap between ticks. A nudge can land up to a minute late.

### Delivery

- **Permission granted:** an OS `Notification` with `tag` set to the key, so two open tabs show
  one notification.
- **Otherwise:** an in-page nudge, a yellow toast that lives longer than an undo toast and pauses
  on hover like the others.
- **Clicking an event-end nudge,** whether an OS notification or an in-page nudge, focuses the tab and opens What now on Today,
  navigating there first from a ritual page. The model runs only on that click.

Nudges fire on every page that runs `live`, rituals included.

## Day strip

A window anchored at now pins the now line to the left edge. So the window stays fixed and the now
line moves across it:

- **Window:** from the earliest of the day's `started_at`, the first event, and now, rounded down
  to the hour, to the hour after the end of day, so the end-of-day mark sits inside the strip. It
  stretches further to keep now and every event on it.
- **Hour ticks:** faint marks labeled like "9a", "12p", and "3p" at every clock hour divisible by
  three, or at every hour when the window spans 6 hours or less.
- **End of day:** a dashed mark labeled with its time, such as "5:30".
- **Event blocks:** show their start time before the title. A block already grows to fit its
  title, so neither gets cut off. Hover still shows both.

`strip()` returns the ticks and the end-of-day position along with the events and the now line.

## Testing

**Go:**
- Settings: the new defaults; settings saved without the new fields load with them; lead time
  outside 1 to 60 is rejected.
- `DELETE /data`: every table is empty afterward, the keys are gone, `data.reset` is published,
  and settings read back as the defaults.

**Vitest, for logic modules only:**
- The welcome redirect rule: not onboarded with no provider redirects; onboarded, a saved
  provider, or being on `/settings` or `/welcome` stays.
- `nudges.ts`: each kind's time and text, the lead-time skip, an event with no end, the late-day
  rule (due, not due, and a must touched today), and the toggles.
- The firing window: fires once, never on load, and catches a nudge inside a 60-second gap.
- `strip()`: each window anchor, both tick steps, and the end-of-day mark.

Components get no unit tests; 5d's Playwright covers them. Until then, smoke-test by hand against
`whatnow serve` with a throwaway `-db`, including a complete onboarding, a skipped onboarding, and delete
everything with a second tab open.
