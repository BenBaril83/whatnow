# whatnow integrations: Linear

Linear is a ticket integration beside Jira, on the contract in
`2026-10-12-whatnow-integrations-design.md` and `2026-10-12-whatnow-jira-design.md`. Everything a
ticket integration gets there (the pick list, skip, dedupe by ref, Add, the TUI step) Linear gets
unchanged. This spec covers only what differs.

## Behavior

- **The search is your assigned, active issues, or a Linear view.** With no view, a pull offers
  issues assigned to you whose state is unstarted or started. A view's address replaces that with
  the view's own filter, so a new rule ("also unassigned bugs in BE") is an edit in Linear, not a
  code change, as JQL is for Jira.
- **Linear is always reachable,** so it never offers "From my computer". That stays Jira's, for a
  Jira behind a VPN.

## Linear

`internal/integrations/linear`, built like `jira`, knowing nothing of whatnow's storage.

- **Fields:** `api_key` (secret, required; label "API key"; help: "In Linear: Settings → Security
  & access → Personal API keys") and `view` (text, optional; label "View"; help: "Optional: a
  view's address, such as https://linear.app/acme/view/my-bugs-0a1b2c3d4e5f. Blank pulls your
  active assigned issues").
- **Auth is a personal API key** sent as `Authorization: <key>`, with no `Bearer`.
- **One request per pull:** `POST https://api.linear.app/graphql`, 30-second timeout. `New(client)`
  as Jira; the endpoint is a field on the struct, set by `NewAt(client, endpoint)` so tests and the
  e2e build can point it at a fake.
- **The query** asks twice in one request, under two aliases: `shown` with `first: 50` and the
  ticket fields, and `rest` with `first: 250` and only `id`. `More` is `len(rest) - len(shown)`,
  never below 0. Linear has no total count; past 250 the number undercounts, which is fine for
  "narrow the search".
  - No view: `viewer { assignedIssues(filter: {state: {type: {in: ["unstarted", "started"]}}}) }`.
  - A view: one query, `viewer { id } customView(id: $id) { issues(filter: $filter) }`, where `$id` is
    the address's last path segment whole when it is a UUID, else its final `-`-separated part (the
    slug ID). An address that isn't `https://linear.app/<ws>/view/…`
    is "that isn't a Linear view's address". A `filter` in the address (base64 JSON that Linear's UI
    adds, std or URL-safe, padded or not) is sent as the nullable `$filter: IssueFilter` on both aliases, and left out when there is none; one that
    isn't a JSON object is the same error.
- **Each issue becomes** `{Key: identifier, Title: "ENG-123 title", Notes: description, URL: url}`.
  A null description is empty notes; the Markdown is kept as is.
- **Your issues sort first**, by `assignee.id` matching the `viewer { id }` fetched in the same
  request, in Linear's order otherwise.
- **Errors never contain the key:** no response or a timeout is "Can't reach Linear"; `401`, or a
  GraphQL error whose `extensions.code` is `AUTHENTICATION_ERROR`, is "Linear rejected the API
  key"; other GraphQL errors are their `message`s joined, which is how a missing view explains
  itself; another non-200 is "Linear answered <status>"; a `200` that isn't the expected JSON is
  "couldn't read Linear's answer".

## The contract

- **Reaching an integration from the user's computer is opt-in.** An integration that can be pushed
  to implements `integrations.Pushable` (a marker method, `Pushable()`). Jira does; Linear and Review
  Queue don't. The view carries `pushable: bool`. `SetVia` and `Receive` refuse an integration that
  isn't pushable with `ErrInvalid`. The settings card offers "From my computer" only when hosted and
  `pushable`. The push key routes in `internal/host` and `web/src/lib/api.ts` stay Jira-specific
  (`/integrations/jira/key`), since Jira is the only pushable integration; a second one would need them
  generalized.

## Wiring

Linear is in both lists in `internal/app/app.go`: local as `linear.NewAt(nil, o.LinearURL)`, hosted as
`linear.NewAt(guarded, o.LinearURL)`, after Jira. A blank `LinearURL` is the real Linear.
`cmd/whatnow/main.go` sets it from its `linearURL` var, which only the e2e build (`cmd/whatnow/e2e.go`)
fills, from `WHATNOW_E2E_LINEAR_URL`.

## Testing

- **Go, `linear` over `httptest`:** mapping (key-first title, URL, null description), the header with
  no `Bearer`, the default query, a view's query with the slug ID or UUID sent, your issues first and
  the rest in order, `More`, a view address's filter (applied, empty, not an object), and each error:
  unreachable, timeout, `401` and `400`, `AUTHENTICATION_ERROR` in a `200`, other GraphQL errors, `500`,
  non-JSON, missing data, a bad view address. No error contains the key.
- **Go, `integrations`:** `runner_test.go` checks `pushable` in the view; `push_test.go` checks that
  `SetVia` and `Receive` refuse a non-pushable ticket integration with `ErrInvalid`. The card's
  one-line `hosted && pushable` check has no test of its own.
- **Vitest:** `integrations.test.ts` covers the shared ticket helpers (pick in order shown, drop a skip).
- **Playwright:** `linear.spec.ts` runs a fake Linear GraphQL server from Node that lists an unassigned
  issue before yours: set Linear up in settings, pull from the morning, see yours first, skip one, add
  one, pull again: neither comes back.
