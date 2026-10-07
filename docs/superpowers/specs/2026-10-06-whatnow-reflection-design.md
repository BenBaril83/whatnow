# whatnow wrap-up reflection: a note for the planner, a message for the user

Wrap-up writes one third-person summary of the day (`days.summary`). The planner reads it on
later days as plain notes, and the web's goodnight screen and Day's-done card also show it to the
user, who gets "they had a steady day" about themselves.

This change gives each reader its own text. The summary stays the planner's brass-tacks notes. A new
**reflection** is the user's: the same day, told to them as "you", in the voice they set for
their assistant.

## Writing the reflection

The reflection is written from the finished summary, not from the day's raw facts. One source of
facts means the two never disagree, and any wrapped day with a summary can get a reflection
later.

`Planner.reflect(ctx, summary)` makes one call to the light model:

- **System:** `prompt.System(reflectionInstructions, settings.Style, settings.AboutMe, "")`, the
  same layers the planner and chat use. The persona applies only to the reflection. The summary's
  prompt stays as it is, with no style.
- **User:** the summary.
- **Instructions** (`reflectionInstructions` in `internal/planner/instructions.go`): rewrite the
  notes as a short message to the user about their day, in second person. Two to four plain
  sentences. Kind, not clinical, and no lists, headings, or advice. Keep to what the notes say.

## When it's written

- **Wrap-up** (`Planner.WrapUp`): after the summary is saved, `reflect` runs and its result is
  saved too, before WrapUp returns. Like the summary, it's written even if the user has left
  (`context.WithoutCancel`, bounded by `modelTimeout`). The goodnight screen gets both in the
  wrapped day.
- **The morning catch-up** (`Close` on an unwrapped earlier day, from `StartDay`) writes no
  reflection. Nobody sees that day's goodnight, so the call would be wasted.
- **A failed reflection** is logged as a fallback and leaves the reflection empty. Wrap-up still
  succeeds. So does a failed summary, which also skips the reflection, since there's nothing to
  write it from.

## Lazy fill

`Planner.Today` checks whether today is wrapped and has a summary but no reflection. If so, it
starts `reflect` in the background and saves the result. This retries a reflection that failed at
wrap time, and backfills days wrapped before this change.

- At most one fill runs per day at a time, guarded inside the planner. A second process (`serve`
  and `tui` both open) may write its own; the later write wins, which is harmless.
- `Today` returns at once without the reflection. The TUI's 5-second reload picks it up; the web
  picks it up on its next fetch of today.
- A fill that fails is logged, and the next `Today` call tries again.

## Storage and API

- Migration `0006_day_reflection.sql`: `ALTER TABLE days ADD COLUMN reflection TEXT NOT NULL DEFAULT '';`
- `domain.Day.Reflection` (`json:"reflection"`), read with the other day columns.
- `Service.SetDayReflection(ctx, dayID, reflection)`, like `SetDaySummary`.
- The web `Day` type gains `reflection: string`.

## Display

- The web goodnight step (`routes/(ritual)/wrap`) and `DaysDone.svelte` show `reflection` instead
  of `summary`, and nothing when it's empty. The summary is never shown to the user.
- The TUI's goodnight card, a follow-up slice, shows the reflection.

## Fake model

The fake (`internal/llm/fake`) answers every schema-less request with `Sentence`, so the
summary and reflection would match and a test couldn't tell them apart. It gains a constant
`Reflection` ("A fake reflection, to you.") and returns it when the system prompt starts with
`reflectionInstructions`. Reading the prompt to choose a reply is how the fake already works.

## Testing

- **Planner** (scripted model, as in `close_test.go`): wrap-up saves both texts. The reflection
  request's system prompt carries the user's style and the summary as its user text. The
  summary request carries no style.
- **Morning catch-up:** closing an unwrapped earlier day writes a summary and no reflection.
- **Failures:** a failed reflection leaves the wrap and the summary intact; a failed summary
  makes no reflection call.
- **Lazy fill:** `Today` on a wrapped day with no reflection writes one in the background, and
  concurrent `Today` calls make exactly one model call.
- **Service and store:** the migration applies, and `SetDayReflection` round-trips.
- **Playwright:** the wrap-up flow shows `fake.reflection` on the goodnight screen, and not
  `fake.sentence`.
