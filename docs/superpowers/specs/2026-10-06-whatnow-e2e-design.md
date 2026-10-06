# whatnow end-to-end tests: the fake provider, the `e2e` build tag, and Playwright

This slice adds browser tests that run against the real binary. They cover the parent spec's
three flows (`2026-10-05-whatnow-design.md`, Testing) and the components that so far had only
hand checks in the UI specs (`2026-10-0*-whatnow-web-ui-*-design.md`).

| Slice | Contents |
|---|---|
| **5d (this spec)** | A rule-based fake provider, the `e2e` build tag, and Playwright |

Tests never call a real model or the network, and never touch the real keychain.

## Fake provider (`internal/llm/fake`)

The fake is an ordinary package with no build tag, so it gets unit tests and counts toward
coverage. It implements `llm.Client` with fixed rules and no randomness, so tests can assert the
exact text it returns.

### Rules

`Complete` chooses its reply by `Request.SchemaName`:

| Schema | Reply |
|---|---|
| `cleanup` | Split the raw note on commas, " and ", and newlines. Trim each piece and drop empty ones. The first piece becomes the original task's title, with `keep: true`. The rest become `new_tasks` with refs `n1`, `n2`, and so on. All other fields are empty: no events, blockers, dates, sizes, or refs. |
| `plan` | Read the eligible `#<id>`s from the facts. The first is a must, the second a should, and the rest are coulds. Each entry gets a fixed rationale, and the plan gets a fixed opening. |
| `pick` | The first eligible task that is neither blocked nor excluded, with a fixed reason. |
| `suggestions` | `roll_over` for every task under review, with a fixed reason. |
| none | One fixed sentence. This serves the connection test, the wrap summary, and the chat fold. |

`Chat` streams a fixed reply through `onText` in a few chunks and returns it with no tool calls.

The fake reads task IDs from the facts' "Eligible tasks, as #<id> title:" list in the
prompt. That ties it to the prompt format on purpose. If the format changes so the fake
can't find the tasks, a test fails.

### Drift guard

Go tests run the real cleanup runner and planner through the fake against a temp database, and
assert that no fallback fires: the plan isn't `Fallback`, the pick isn't `Fallback`, and the
cleanup applies. A prompt change that breaks the fake fails `go test` before it reaches
Playwright.

## The `e2e` build tag

Only `cmd/whatnow/e2e.go` carries the tag. Its `init`:

- Calls go-keyring's `MockInit`, so keys live in memory for the life of the process.
- Unsets `ANTHROPIC_API_KEY` and `OPENAI_API_KEY`, so a key in the shell never reaches a client
  and onboarding always asks for one.
- Replaces the package-level `newClient` with a wrapper around the real `settings.NewClient`.
  The wrapper keeps its errors, so an unconfigured install still reads as unconfigured, and
  replaces the real client with the fake.

`main.go` gains that `newClient` variable, defaulting to `settings.NewClient`. Both the `active`
closure and `Deps.NewClient` use it, so `active` loads settings itself instead of calling
`settings.Active`. `settings.Active` loses its only caller and goes. Without the tag, the binary behaves the same as
before.

## Playwright (`web/e2e/`)

- `@playwright/test`, Chromium only, one worker, tests run serially.
- The config's `webServer` starts `whatnow-e2e serve` on a port other than the default, with a
  `-db` in a temp directory.
- `beforeEach` calls `DELETE /api/v1/data`, which wipes the database and keys like the real
  "delete everything". Every test except onboarding then saves settings and a dummy key through
  the API, so the app starts already set up.
- `just e2e` builds the web client, runs `go build -tags e2e -o whatnow-e2e ./cmd/whatnow`, and
  runs Playwright. `just test` doesn't run it, so it stays fast and needs no browser.
- `whatnow-e2e` is gitignored.

### Tests

| Test | Covers |
|---|---|
| Capture to split to undo | "buy milk and call mom" becomes two cards. Undo in the cleanup toast puts back the single raw task. |
| Good morning | Morning dump, pace, start. The plan shows in tiers with the fake's opening. Replan makes a new plan version. |
| What now | It shows the first free task. Marking that task done shows the done toast. |
| Onboarding | A fresh install redirects to `/welcome`. Entering a key, passing the connection test, and finishing lands on the morning check-in. |
| Settings | An edit survives a reload. Delete everything sends the app back to onboarding. |
| Menus | A task menu closes on an outside click and on Escape. |
| + Task and blocker link | The form creates a task that waits on another, and its card links to the blocker. |
| Wrap-up | The roll-over suggestions show, and wrapping closes the day. |
| Chat | A sent message gets the streamed canned reply. |

## Out of scope

- **A fake clock.** Tests avoid assertions that depend on the date or time. Reconciling an
  unwrapped previous day at the morning start needs a past date, so it stays untested here.
  Wrap-up exercises the same `suggestions` path on the same day.
- **Chat tool calls.** The canned chat reply covers streaming and the bubble, but not tool chips
  or chat Undo.
- **Visual and mobile checks.** Those come with the mobile layout pass.
