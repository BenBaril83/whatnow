# Sending Jira tickets from your computer: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A hosted user whose Jira is behind a VPN runs `whatnow jira push` on their computer, and the hosted morning pull offers those tickets.

**Architecture:** The integrations runner learns a per-integration `via` mode and stores a pushed ticket list in the integration's saved body; in "computer" mode a pull answers from that list instead of calling the integration. The hosted front door gains per-user push keys in the accounts database and one bearer-authenticated route that forwards to the user's App. A new `whatnow jira` subcommand reuses the `jira` package to search and posts the result.

**Tech Stack:** Go (stdlib `net/http`, SQLite via `database/sql`, `go-keyring` through `internal/secrets`), SvelteKit + Svelte 5 runes, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-23-whatnow-jira-push-design.md`

## Global Constraints

- Modes are exactly `"direct"` and `"computer"`; an empty saved `via` means `"direct"`.
- At most 50 tickets per push (`maxResults` in `internal/integrations/jira`). Each needs a non-empty `key` and `title`; `url`, when set, must be `http` or `https`.
- Keys: 32 random bytes, base64url, prefixed `wn_`, stored as SHA-256 (reuse `hashToken`), one per user, shown once.
- Neither the Jira token nor the push key appears in any output, error, or log.
- Copy, verbatim:
  - Mode line: "Useful when your Jira is behind a VPN or on an intranet."
  - Install line: "On a Mac: `brew install jameynakama/tap/whatnow`, then `whatnow jira setup`."
  - Nothing sent: "Nothing sent from your computer yet — run `whatnow jira push`"
  - Hint: "Behind a VPN or on an intranet? Send tickets from your computer instead"
  - CLI success: "Sent 7 tickets to whatnow.day" (count and host vary), then "12 more matched — narrow the search" when `more > 0`.
  - CLI `401`: "whatnow didn't accept the key — create a new one in Jira settings"
  - CLI `409`: "Jira on whatnow.day is set to reach Jira directly — switch it to From my computer"
- The mode choice and hint show only in hosted mode; local behavior is unchanged.
- Tests: Go `testing` with the repo's existing helpers; web logic in Vitest. The spec's hosted Playwright flow becomes a Go flow test in `internal/host` (Task 3), because the Playwright harness only runs `whatnow serve`.

## Review Focus

- Saving the Jira card's fields after a push: `Runner.Save` rebuilds `saved` from scratch and would drop `via` and `pushed`; they must survive (Task 1).
- A pushed ticket whose `url` is `javascript:…`: the pick list renders it as a link, so the push is refused with `400` (Task 1).
- Switching back to Directly: the next pull goes to Jira, never the stale pushed list (Task 1).
- `whatnow jira push` pointed at a wrong whatnow address that answers HTML: reads "couldn't read whatnow's answer — is the address right?", not a JSON decode error (Task 4).
- A browser session posting to the forwarded `/received` route: refused, so only a key can push (Task 3).

---

### Task 1: Runner modes and the pushed list

**Files:**
- Modify: `internal/integrations/saved.go` (add fields to `saved`)
- Modify: `internal/integrations/integrations.go` (`Source.Hint`)
- Modify: `internal/integrations/runner.go`
- Test: `internal/integrations/runner_test.go`

**Interfaces:**
- Produces:
  - `const ViaDirect = "direct"`, `const ViaComputer = "computer"` in `integrations`.
  - `saved` gains `Via string \`json:"via,omitempty"\`` and `Pushed *Pushed \`json:"pushed,omitempty"\``; `type Pushed struct { At time.Time; Tickets []Ticket; More int }` (json `at`, `tickets`, `more`).
  - `Source` gains `Hint string` (set by an integration when another way in might help; `"computer"` is the only value).
  - `View` gains `Via string \`json:"via"\`` (never empty: `"direct"` when unset) and `SentAt *time.Time \`json:"sent_at"\``.
  - `SourceResult` gains `Hint string \`json:"hint,omitempty"\`` and `SentAt *time.Time \`json:"sent_at,omitempty"\``.
  - `func (r *Runner) SetVia(ctx context.Context, id, via string) (View, error)` — `ErrInvalid` for an unknown mode or an integration whose `Pulls()` isn't `PullsTickets`.
  - `func (r *Runner) Receive(ctx context.Context, id string, tickets []Ticket, more int) error` — `ErrConflict` unless enabled and `via == "computer"`; `ErrInvalid` per Global Constraints; replaces `Pushed` with `{At: r.now(), …}`.

- [ ] **Step 1: Write the failing tests** in `runner_test.go`, using its existing fake ticket integration:
  - `TestReceiveThenPullOffersPushedList`: enable, `SetVia(…, "computer")`, `Receive` two tickets with `more: 3`; `Pull` returns one source labeled with the integration's name, both tickets, `More == 3`, `SentAt` equal to the runner's frozen now; the fake's `Pull` was not called.
  - `TestPushedListIsFiltered`: a received ticket whose key is a ref on an open task, and one on the skip list, are dropped from the pull.
  - `TestComputerModeNothingSent`: computer mode, no push; `Pull` returns one source with `Error == "Nothing sent from your computer yet — run `+"`whatnow jira push`"+`"` and no tickets.
  - `TestComputerModeIsReady`: with none of the fake's required fields filled, `SetVia(computer)` + enable gives `View.Ready == true`; in direct mode the same view is not ready.
  - `TestReceiveRefused`: table — off → `ErrConflict`; direct → `ErrConflict`; 51 tickets → `ErrInvalid`; blank key → `ErrInvalid`; blank title → `ErrInvalid`; url `javascript:alert(1)` → `ErrInvalid`.
  - `TestSaveKeepsViaAndPushed`: computer mode, receive, `Save` fields; view still `Via == "computer"` and the next pull still offers the pushed tickets.
  - `TestDirectModeIgnoresPushed`: receive in computer mode, `SetVia(direct)`; pull calls the fake's `Pull` and returns its tickets, `SentAt == nil`.
  - `TestSetViaRefused`: `"elsewhere"` → `ErrInvalid`; an events integration → `ErrInvalid`.
  - `TestHintCarried`: the fake returns a source with `Err` and `Hint: "computer"`; the result's source has `Hint == "computer"`.

- [ ] **Step 2: Run** `go test ./internal/integrations/ -run 'Receive|Pushed|ComputerMode|SaveKeeps|DirectMode|SetVia|Hint'` — expect compile failures on the new names.

- [ ] **Step 3: Implement.** In `view`, compute `Ready` as today unless `s.Via == ViaComputer`, which is ready. In `Pull`, after the `On()` check, branch on `s.Via == ViaComputer` before calling `in.Pull`: build the one source from `s.Pushed` (or the nothing-sent error) and hand it to `offer`, setting `SentAt`. `offer` copies `src.Hint`. `Save` carries `Via` and `Pushed` into `next`. Ticket URL check: `url.Parse` and scheme in `{http, https}` when non-empty.

- [ ] **Step 4: Run** `go test ./internal/integrations/...` — PASS.

- [ ] **Step 5: Commit** `feat: offer a pushed ticket list when an integration is reached from your computer`

### Task 2: API routes and the Jira hint

**Files:**
- Modify: `internal/integrations/jira/jira.go` (set `Hint`)
- Modify: `internal/httpapi/integrations.go`, `internal/httpapi/server.go:100-108`
- Test: `internal/integrations/jira/jira_test.go`, `internal/httpapi/integrations_test.go`

**Interfaces:**
- Consumes: `Runner.SetVia`, `Runner.Receive`, `Source.Hint`, `SourceResult.Hint` (Task 1).
- Produces:
  - `PUT /api/v1/integrations/{id}/via` body `{via}` → the `View`.
  - `POST /api/v1/integrations/{id}/received` body `{tickets, more}` → `204`; registered only when `Deps.Hosted`. Task 3 forwards key-authenticated pushes here.
  - Pull responses carry `hint` only when `Deps.Hosted`.

- [ ] **Step 1: Write the failing tests.**
  - `jira_test.go`: extend the error table so unreachable and `401` sources have `Hint == "computer"`, and `400`/`500`/bad-URL sources have `Hint == ""`.
  - `integrations_test.go`:
    - `TestSetVia`: `PUT …/jira/via {"via":"computer"}` → 200 with `"via":"computer"`; `{"via":"x"}` → 400.
    - `TestReceivedHostedOnly`: on a local server the route is 404 (falls to static or no match — assert not 204); on a hosted server, in computer mode, `{tickets:[…], more:0}` → 204, then pull offers them.
    - `TestHintOnlyWhenHosted`: a fake ticket integration returning `Hint: "computer"`; the local pull body has no `hint`, the hosted one has `"hint":"computer"`.

- [ ] **Step 2: Run** `go test ./internal/integrations/jira/ ./internal/httpapi/ -run 'Error|SetVia|Received|Hint'` — FAIL.

- [ ] **Step 3: Implement.** `jira.Pull` sets `src.Hint = "computer"` when `search` returns `errUnreachable` or the 401 error (make the 401 a package `var` like `errUnreachable` so it can be compared). Handlers `setVia` and `receiveTickets` follow `enableIntegration`'s shape. `pullIntegration` blanks each source's `Hint` when `!s.Hosted`.

- [ ] **Step 4: Run** `go test ./internal/...` — PASS.

- [ ] **Step 5: Commit** `feat: add routes to choose how Jira is reached and to receive pushed tickets`

### Task 3: Push keys and the push route in hosted mode

**Files:**
- Create: `internal/host/migrations/0002_push_keys.sql`
- Create: `internal/host/pushkeys.go` (accounts methods and the three handlers plus `push`)
- Modify: `internal/host/server.go` (routes, guard exemption, dispatch refusal)
- Test: `internal/host/pushkeys_test.go`

**Interfaces:**
- Consumes: `POST /api/v1/integrations/{id}/received` (Task 2); `Registry.Serve`; `hashToken`; `frontDoor.limiter`.
- Produces:
  - Table `push_keys (user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, key_hash TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL, last_used_at TEXT)`.
  - `func (a *Accounts) CreatePushKey(ctx context.Context, userID string) (string, error)` — replaces any existing key.
  - `type PushKey struct { CreatedAt time.Time; LastUsedAt *time.Time }` (json `created_at`, `last_used_at`); `func (a *Accounts) PushKey(ctx context.Context, userID string) (*PushKey, error)` — nil when none.
  - `func (a *Accounts) DeletePushKey(ctx context.Context, userID string) error`
  - `var ErrNoPushKey`; `func (a *Accounts) UserForPushKey(ctx context.Context, key string) (User, error)` — stamps `last_used_at`.
  - Routes: `GET /api/v1/integrations/jira/key` → `{key: PushKey|null}`; `POST …/key` → `{key: "wn_…"}`; `DELETE …/key` → 204 (all `withUser`). `POST /api/v1/integrations/jira/push` (bearer).

- [ ] **Step 1: Write the failing tests** with `newFrontTest`:
  - `TestPushKeyLifecycle`: GET → `null`; POST returns a `wn_` key; GET shows `created_at`, `last_used_at` null; a second POST returns a different key and the first no longer pushes (401); DELETE then push → 401.
  - `TestPushFlow` (the spec's end-to-end flow): sign in, enable Jira, `PUT …/jira/via computer`, create key, push two tickets with `Authorization: Bearer <key>`, no cookie, no `Origin` → 204; pull with the cookie offers both with `sent_at`; add one, skip the other; push the same two again; pull offers neither. `last_used_at` is now set.
  - `TestPushRefused`: no header → 401; unknown key → 401; direct mode → 409; a cookie-only request to `/push` → 401; a cookie request to `/api/v1/integrations/jira/received` → 404; wrong Host → 403; non-JSON content type → 403; a valid key as bearer on `GET /api/v1/tasks` with no cookie → 401.
  - `TestPushRateLimited`: with the front test's frozen clock, the 31st push in a burst → 429.
  - `TestDeleteAccountDeletesPushKey`: create key, delete account, push → 401.

- [ ] **Step 2: Run** `go test ./internal/host/ -run Push` — FAIL.

- [ ] **Step 3: Implement.** Register `POST /api/v1/integrations/jira/push` and the key routes before the `/api/v1/` catch-all. In `guard`, skip only the `Origin` comparison for that exact path (content-type and body cap still apply). `push`: read `Bearer` → `UserForPushKey` → limiter → clone the request with path `/api/v1/integrations/jira/received` and `registry.Serve`. `dispatch` answers 404 for any path ending `/received`. The migration runner already applies files in `migrations/` in order.

- [ ] **Step 4: Run** `go test ./internal/host/...` — PASS.

- [ ] **Step 5: Commit** `feat: let a per-user key push Jira tickets to a hosted account`

### Task 4: `whatnow jira setup` and `whatnow jira push`

**Files:**
- Create: `cmd/whatnow/jira.go`
- Modify: `cmd/whatnow/main.go` (`case "jira"`, usage string)
- Modify: `README.md` (a short "Jira behind a VPN" paragraph beside the Jira mention near line 29, using the install line from Global Constraints)
- Test: `cmd/whatnow/jira_test.go`

**Interfaces:**
- Consumes: `jira.New(client).Pull(ctx, integrations.Config{Values: {url, token, jql}}, time.Time{}, time.Time{})`; `secrets.Store` (`secrets.Keychain{}` in `main`); the push route (Task 3).
- Produces:
  - `type pushConfig struct { Whatnow, Jira, JQL string }` (json `whatnow`, `jira`, `jql`), stored at `<os.UserConfigDir()>/whatnow/jira-push.json`.
  - Keychain names `jira-push/key` and `jira-push/token`.
  - `func runJira(ctx context.Context, args []string, in io.Reader, out io.Writer, dir string, vault secrets.Store, client *http.Client) error` — dispatches `setup` and `push`; unknown subcommand prints usage and errors.
  - `func push(ctx context.Context, cfg pushConfig, key, token string, client *http.Client, out io.Writer) error`

- [ ] **Step 1: Write the failing tests**, with `httptest` servers for Jira and whatnow and an in-memory `secrets.Store` fake:
  - `TestPushSends`: fake Jira returns 2 issues with `total: 14`; fake whatnow asserts `POST /api/v1/integrations/jira/push`, `Authorization: Bearer wn_k`, JSON `{tickets:[2], more:12}`, answers 204; output is `"Sent 2 tickets to <host>\n12 more matched — narrow the search\n"`.
  - `TestPushErrors` (table on output/error text): Jira unreachable → the jira package's message; whatnow 401 → the Global Constraints `401` copy; whatnow 409 → the `409` copy; whatnow answering 200 HTML or a 404 HTML page → "couldn't read whatnow's answer — is the address right?". For every case, the output and error contain neither `wn_k` nor the Jira token.
  - `TestSetupSavesAndPushes`: feed five lines on `in`; config file has the three text values, the store has key and token, and a push was made. Run again with five empty lines: values kept as defaults, secrets kept, prompts show "saved; enter to keep".
  - `TestSetupDefaultsWhatnowAddress`: an empty first line saves `https://whatnow.day`.

- [ ] **Step 2: Run** `go test ./cmd/whatnow/ -run 'Push|Setup'` — FAIL.

- [ ] **Step 3: Implement** with `bufio.Scanner` prompts, one per line. `push` succeeds only on `204`; `401` and `409` get their copy; any other status gets "couldn't read whatnow's answer — is the address right?". The success line names `cfg.Whatnow`'s host.

- [ ] **Step 4: Run** `go test ./cmd/whatnow/...` and `go vet ./...` — PASS.

- [ ] **Step 5: Commit** `feat: add whatnow jira setup and push to send tickets from your computer`

### Task 5: The Jira card's mode choice and the hint

**Files:**
- Modify: `web/src/lib/api.ts` (types and calls)
- Modify: `web/src/lib/integrations.ts` (`sentLabel`)
- Modify: `web/src/lib/components/IntegrationCard.svelte`
- Modify: `web/src/lib/components/TicketPicker.svelte`
- Test: `web/src/lib/integrations.test.ts`

**Interfaces:**
- Consumes: Task 1's `via`/`sent_at` on the view and `hint`/`sent_at` on sources; Task 2's `via` route; Task 3's key routes; `session` from `#lib/session.svelte.ts` for `hosted`.
- Produces:
  - `Integration` gains `via: 'direct' | 'computer'`, `sent_at: string | null`; `PullSource` gains `hint?: 'computer'`, `sent_at?: string`.
  - `api.setVia(id, via)`, `api.pushKey()`, `api.createPushKey()`, `api.revokePushKey()`.
  - `export function sentLabel(sentAt: string, now: Date): string` — `"today 8:02am"` on the same local day, otherwise `"Oct 21, 8:02am"`.
  - `TicketPicker` gains optional prop `onHint?: () => void`.

- [ ] **Step 1: Write the failing Vitest cases** in `integrations.test.ts`: `sentLabel` for same day, previous day, and just after midnight (a 11:59pm send seen at 12:01am reads as the date, not "today").

- [ ] **Step 2: Run** `cd web && npx vitest run src/lib/integrations.test.ts` — FAIL.

- [ ] **Step 3: Implement.**
  - Card, when `session.value?.hosted && integration.pulls === 'tickets'`: two radio buttons, **Directly** / **From my computer**, at the top of the open card; a change calls `setVia` at once (like `toggle`) and replaces `integration`. In computer mode, hide the field inputs and **Save**, and show the mode line, the install line, the key block (Create key / the once-shown key / Revoke / "last used …"), the "Last sent from your computer: {sentLabel}" or nothing-sent line, and **Pull**.
  - `TicketPicker`: under a source's error, when `source.hint === 'computer'`, show the hint copy as a button calling `onHint` if given, else a link to `/settings/integrations`. Beside the header, when `source.sent_at`, show `sent {sentLabel}`.
  - Card passes `onHint={() => setVia('computer')}`; `MorningIntegrations` passes nothing.

- [ ] **Step 4: Run** `just test` and `just lint` — PASS. Then `just e2e` — the existing local Jira flow still passes (the choice is hidden locally).

- [ ] **Step 5: Commit** `feat: choose how the Jira card reaches Jira, and hint at it when a direct pull can't`
