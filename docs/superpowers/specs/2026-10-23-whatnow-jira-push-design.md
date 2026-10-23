# whatnow: sending Jira tickets from your computer

Hosted whatnow fetches Jira from the server (`2026-10-12-whatnow-jira-design.md`,
`2026-10-22-hosted-whatnow-design.md`). Many companies keep Jira on an intranet or behind a VPN,
where the server can't reach it: the request either fails or is answered by the network's edge,
often with a `401` that whatnow reports as "Jira rejected the token". This spec adds a second way
in: a command on the user's own computer, which can reach Jira, sends the tickets to their hosted
account, and the hosted app offers them exactly as a direct pull would.

## Why

whatnow's point is that work and life share one list: a prescription to pick up sits beside a demo
to give and a ticket to finish, and the plan weighs them together. Keeping work in a local install
and the rest in hosted splits that list, and the hosted app is the one on the phone. A user whose
Jira is behind a VPN has to keep Jira in the hosted list without the server ever reaching Jira.

The user this serves does the good-morning ritual at their work computer, on the VPN; their phone
has no VPN. They manage tickets by hand and are glad to run a command when they want fresh ones.

## Goals

- `whatnow jira push`, run on a computer that can reach Jira, sends the user's JQL results to their
  hosted account.
- The hosted morning pull and the settings **Pull** offer those tickets in the usual pick list,
  with the usual picking, skipping, and dedupe.
- Users whose Jira the server can reach see the Jira card as it is today, plus one hint when a
  direct pull fails in a way a VPN would explain.

## Non-goals

- Scheduled or background pushing. The user runs the command when they want fresh tickets.
- Pushing Google Calendar, or a generic push for every integration. Jira is the one that lives
  behind VPNs; generalize when a second one needs it.
- Jira Cloud auth, and writing anything back to Jira; both stay as the Jira spec has them.
- Local mode changes. A local install already runs on the user's machine and reaches Jira itself;
  the choice below is hosted-only.
- The browser fetching Jira itself, or the page calling a helper on `localhost`. Jira's CORS
  won't allow the first, and browsers' private-network rules make the second fragile.

## Anti-goals

- A key that does more than send tickets. If it leaks, the worst it can do is replace the
  offered ticket list.
- Anything that makes the Jira card confusing for users who reach Jira directly.
- The Jira token or JQL leaving the user's computer in this mode.

## Behavior

**The Jira card (hosted only)** gets a choice at its top: how whatnow reaches Jira.

- **Directly** (the default): today's card, unchanged: address, token, JQL, Pull.
- **From my computer**, with the line "Useful when your Jira is behind a VPN or on an intranet."
  The address, token, and JQL fields are hidden (their saved values are kept, as when an
  integration is turned off). In their place:
  - "On a Mac: `brew install jameynakama/tap/whatnow`, then `whatnow jira setup`." The command
    is Mac-only, as releases are; Windows and Linux wait until someone asks, since they'd need
    release targets and a keychain fallback.
  - **Create key** shows a new key once, with the setup command to paste
    (`whatnow jira setup`). Creating a key replaces the previous one; **Revoke** deletes it.
    The card shows whether a key exists and when it was last used.
  - "Last sent from your computer: today 8:02am" (or the date, when not today), or "Nothing sent
    yet: run `whatnow jira push`".
  - **Pull** as usual.

**Pull in "from my computer" mode** offers the last list the computer sent, run through the same
filters as a direct pull (tickets already a task's ref, and skipped tickets, are dropped). The pick
list's header carries "sent 8:02am" so a stale list reads as one. Nothing sent yet is the source's
error: "Nothing sent from your computer yet — run `whatnow jira push`". The morning chip and the TUI
are otherwise unchanged.

**Choosing the mode saves it at once**, like the on/off toggle, so the sequence "pick From my
computer, create a key, run setup" works without a **Save** in between. (Builder's call.)

**The hint for direct pulls (hosted only):** when a direct pull fails with "Can't reach Jira" or a
`401`, the source result carries `hint: "computer"` beside its error. The web shows "Behind a VPN
or on an intranet? Send tickets from your computer instead" under the error, as a link. On the
settings card the link switches the mode (saving it, as above); on the morning chip it opens
settings at the Jira card, where the user switches. The server sets the hint only in hosted mode,
so local never shows it. (Builder's call.)

**The integration is on** in "from my computer" mode when it's enabled; none of the direct fields
are required.

## The command

`whatnow jira setup` asks, one prompt each:

1. Your whatnow address (default `https://whatnow.day`).
2. The key from the Jira card.
3. Your Jira's address.
4. Your Jira Personal Access Token.
5. The JQL search.

It writes the two addresses and the JQL to `<user config dir>/whatnow/jira-push.json` and the key
and Jira token to the OS keychain through `internal/secrets`. Run again, it shows the saved values
as defaults (secrets as "saved; enter to keep"). It ends by running a push, so a typo shows up
right away.

`whatnow jira push` runs the search with the `jira` package's existing request and error mapping,
sends the result, and prints one line: "Sent 7 tickets to whatnow.day" (plus "12 more matched —
narrow the search" when Jira's total exceeds 50), or the error. Errors read like the hosted
card's: Jira's errors as the Jira spec words them, and for whatnow, "whatnow didn't accept the key —
create a new one in Jira settings" on `401`, and "Jira on whatnow.day is set to reach Jira
directly — switch it to From my computer" on `409`. Neither the Jira token nor the key appears in
any output.

## Server

**Keys** live in the accounts database: a new migration adds `push_keys (user_id primary key,
hash, created_at, last_used_at)`. One key per user. The key is random (32 bytes, shown once,
prefixed `wn_`), stored as its SHA-256 like sessions. Deleting the account deletes its key.

**`POST /api/v1/integrations/jira/push`** takes `Authorization: Bearer <key>` and
`{tickets: [{key, title, notes, url}], more}`. It is the only route a key opens, and it is the
only `/api/v1/` route that authenticates without the session cookie. A bearer header is never
attached by a browser on its own, so the guard's `Origin` check doesn't apply to it; the JSON
content-type check and the body cap still do. It is rate-limited per user like pulls. It answers
`401` for an unknown key, `409` when Jira isn't on or isn't in "from my computer" mode, and
`400` for more than 50 tickets or a ticket with no key or title.

A push replaces the stored list whole and stamps it with the server's time. It does not create
tasks and opens no batch; the user still picks. Since the hub only publishes batches, an open
settings card learns of the new list on its next load or pull.

**Where the list lives:** the integration's saved body gains `via` (`"direct"` or `"computer"`)
and `pushed: {at, tickets, more}`. The runner, not the `jira` package, answers a pull in
"computer" mode from `pushed`, since the `jira` package knows nothing of storage.

**The key endpoints** for the card: `GET`, `POST` (create, returns the key once), and `DELETE`
on `/api/v1/integrations/jira/key`, all session-authenticated like the rest of the API. `via` is
saved with the card's other values through the existing `PUT /integrations/{id}`.

## Testing

**Go:**
- Push with a good key stores the list and stamps it; with an unknown or revoked key, `401`;
  with Jira off or in direct mode, `409`; over 50 tickets, `400`. A push with a cookie but no
  key is refused. A key can't reach any other route.
- A pull in "computer" mode returns the stored list, drops a ticket already a task's ref and a
  skipped one, and with nothing sent yet returns the source error.
- Creating a key replaces the old one; deleting the account deletes the key; `last_used_at` moves
  on a push.
- `whatnow jira push` against a fake Jira and a fake whatnow, over `httptest`: what it sends,
  its one-line summaries, and each error. No output contains the token or the key.
- A direct pull's unreachable or `401` error carries `hint: "computer"` in hosted mode, and not
  locally.

**Vitest:** the card shows the right fields for each mode, and the "sent" line's today/date wording.

**A Go flow test in `internal/host`** (the Playwright harness only runs `whatnow serve`): switch Jira to "from my computer", create a key, push a list with the key
from the test, pull from the morning, add one ticket, skip another, push again: neither comes
back.

## Left to the builder

- Where the push route is registered so it sits outside the cookie check but inside the host check
  and body cap.
- How `Ready` learns that "computer" mode needs no fields; a mode-aware check in the runner is
  enough, without changing the `Field` contract.
- The `jira-push.json` shape and the prompt library (or plain `bufio`) for setup.
