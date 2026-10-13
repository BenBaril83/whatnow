# whatnow

*Executive function for the rest of us.*

whatnow is a day planner for ADHD brains. Rather than an hourly schedule, it holds your day loosely:
what you must do, what you should do, and what you could do if there's room. Dump everything on
your plate in whatever words come out. An LLM sorts it into tasks and events, notices how you feel
about them, and plans a day that fits your energy. When you're stuck, ask it *what now?*

It runs locally: one Go binary with a web UI and a terminal UI, and your data in SQLite on your
machine. You bring your own model API key.

For a feel of a whole day, from the morning brain dump to the wrap-up, read [demo.md](demo.md).

## What it does

- **Good morning:** reconcile yesterday's leftovers, dump what's on your mind, say how much you've
  got in you today, and get a plan in three tiers.
- **What now?:** one pick for right now, with a reason. Pass on it and say why, and it picks again.
- **Chat:** "push the dentist to Thursday", "break this report into tiny steps". The assistant acts
  directly, and every change can be undone.
- **Pace:** five levels, from *running on fumes* to *go hard*. Change it any time and the plan
  follows.
- **Nudges:** before events, when they end, and as the day winds down, in the web UI.
- **Wrap up:** see what you got done, triage what's left, and leave a note for tomorrow.
- **Integrations:** pull today's meetings from Google Calendar (by each calendar's secret iCal
  address, no OAuth) and pick Jira tickets to add as tasks (by your own JQL search). Pulls happen
  only when you ask, from the morning brain dump or from settings.

You can shape it in settings. *Assistant style* sets its voice ("playful and friendly, with the
odd koan"). *About me* tells the planner how you work ("my focus peaks before lunch", "meetings
drain me"). *Theme* swaps the look: the default sticker style, plain 90s, Neon Nights, Terminal,
Paper, or Vaporwave.

## Install

whatnow builds from source for now. You need Go 1.27+, Node 24, and
[just](https://github.com/casey/just).

```sh
git clone https://github.com/jameynakama/whatnow
cd whatnow
just install   # builds the web UI and puts whatnow in your GOBIN
```

## Models

whatnow works with Anthropic (the default) or any OpenAI-compatible API, such as OpenAI,
OpenRouter, Ollama, or LM Studio. Set it up on the first run, or later in settings. Keys are
stored in your OS keychain, never in the database. `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` in the
environment works too.

Consumer subscriptions (Claude Pro or Max, ChatGPT plans) can't power whatnow. It needs an API key.

## Usage

```sh
whatnow          # the terminal UI, which also serves the web UI at http://127.0.0.1:7777
whatnow serve    # the web UI only
```

Both take `-addr` (default `127.0.0.1:7777`) and `-db` (default
`<user config dir>/whatnow/whatnow.db`, which on macOS is
`~/Library/Application Support/whatnow/whatnow.db`).

Open the web UI once to set up a model. After that, use either UI, or both at once, since they
stay in sync. In the terminal, press `?` for the keys.

### The terminal UI

The TUI is a companion meant to sit in a spare pane all day, not a full replacement. It does the
morning ritual (including calendar and Jira pulls), today's list, *what now?*, pace, undo, quick
add, and chat. These are web-only:

- first-run setup and all settings, including integrations and themes
- the wrap-up ritual (the TUI shows the day's-done card once you've wrapped)
- nudges
- editing a task's details, and the all-tasks list

## Development

```sh
just dev     # API plus the Vite dev server, at http://localhost:5173
just test    # Go and web unit tests
just lint
just e2e     # Playwright, against a fake model
just demo    # a throwaway database on a clock you move with `just clock 13:45`
```

## License

MIT. See [LICENSE](LICENSE).
