# A day with whatnow, from onboarding to wrap-up

One made-up Thursday, start to finish: what you tell whatnow, and what it does with it. It doubles
as a demo script. The setup below runs it on a clock you move by hand, so a whole day fits in ten
minutes.

## Setup

- `rm -rf .demo` for a fresh start, then `just demo`. It builds the web client and starts the TUI on
  its own database (`.demo/whatnow.db`) and port (7778), so your real whatnow is untouched. The web
  UI is at http://127.0.0.1:7778.
- Set the day first: `just clock 2026-10-08 08:40`. A Thursday makes "need it by friday" in the
  dump land as tomorrow.
- `just clock HH:MM` jumps the clock. The TUI catches up within 5 seconds, and the browser does when
  you click into it. Nudges for the hours skipped are dropped, so a jump doesn't set off a burst.
- After each act, checkpoint with `cp .demo/whatnow.db .demo/act1.db`. If the model goes off the
  rails, quit, copy the checkpoint back, and relaunch.
- Results are live model output. The `n` (pass) and "break it down" beats are where the demo is most
  likely to stumble, so rehearse them.

## Settings

**Bot prompt:** playful friendly voice, and i'm a buddhist, so give me relevant buddhist quotes or
koans to chew on sometimes.

**About me** (written to change the plan where people can see it):

> ADHD brain. My focus peaks before lunch, so deep or scary work goes in the morning. After 3pm
> I'm cooked: small admin tasks only. Meetings drain me, so I need something easy right after one.
> I always underestimate writing tasks. I have to leave by 5:15 for school pickup. That's a hard
> stop.

Each line shows up somewhere in the demo: the dreaded report lands in the morning, something
gentle follows the 1:1, the afternoon is lighter, and the 5:15 stop limits what's possible.

## The run

### Act 0: Onboarding (web, `/welcome`)

Pick the provider, which shows the key from the keychain or env. Paste in the bot prompt and About
me. Set end of day to 17:30. Then put the TUI next to the browser.

### Act 1: Good morning, 8:40 (TUI)

- The good-morning card shows the tagline. Press enter. It's the first morning, so there's no
  reconcile step.
- Brain dump (paste it, messy on purpose):

  ```
  standup 9:30
  1:1 with Priya at 2, 45 min
  Q3 report draft. dreading this, been avoiding it all week
  reply to Dana about the conference
  renew passport, need it by friday
  fix the flaky checkout test
  review Marco's API doc
  groceries
  learn banjo someday
  ```

- Pace: steady. Mood: "slept badly, a bit fried".
- The reveal: cleanup has turned the dump into events and tasks with feelings attached. The Q3
  report is a morning must because of About me. Banjo is a could. The opening line might come with
  a koan.

### Act 2: The morning (`just clock 9:19`)

- The strip and countdown read "standup in 11m". A minute later the web nudge fires. Jump to 9:19,
  not 9:20: a jump that lands exactly on a nudge's time skips it.
- Press `w` for what now. It picks something small before standup, and the reason appears in the
  callout.
- `space` marks the checkout test done, and the celebration plays.

### Act 3: Avoidance (`just clock 10:15`)

- `w` picks the Q3 report. Press `n` and say "can't face it". It should answer kindly and offer
  something smaller.
- `c` opens chat: "help me break the Q3 report into tiny steps". The agent creates the subtasks and
  replans. This is the money moment.
- `a` captures "Marco needs the API doc reviewed by EOD". It's cleaned up in the background,
  becomes a must, and triggers a replan.

### Act 4: After the 1:1 (`just clock 14:50`)

- `-` drops the pace: "that 1:1 drained me". The replan only offers light tasks (About me again).
- Chat: "push the passport to tomorrow morning". You can undo it with `u`.

### Act 5: Wrap up (`just clock 16:35`, web)

- The Wrap up button appears in the header, an hour before the 17:30 end of day.
- Done list with the win line. Triage the leftovers: banjo gets dropped, groceries roll over. Note
  for tomorrow: "start with report step 2".
- Reflection message and goodnight screen. The TUI switches to its goodnight screen too.

### Encore, optional (`just clock 2026-10-09 08:40`)

The TUI shows good morning. Reconcile opens with "Last time you got N things done", and the
planner has read last night's reflection.
