# Hosted whatnow: users, sign-in, and a server of its own

whatnow runs locally today: one Go binary, one person, SQLite on their machine, keys in their
keychain. This spec adds a hosted mode, the same app on a server where people sign in with Google
and use it from any device, phone included. The parent spec is `2026-10-05-whatnow-design.md`.

## Why

People asking about whatnow hit two walls. Installing a Go binary and pasting an API key into a
terminal loses the non-technical ones, and others won't use a planner they can't carry with them.
The hosted version removes both: open a URL, sign in, plan the day from a phone or a laptop, with
the whole experience from the morning dump to wrap-up. It starts as bring-your-own-model. A
subscription may come later to cover hosting.

## Goals

- Sign in with Google and use the full app in a browser on any device.
- Each user's data is isolated by construction, not by remembering a filter.
- Local mode is untouched: same default, same flags, no login, SQLite file, keychain keys. A local
  user upgrading notices nothing.
- One codebase. Everything hosted mode needs to run is in the public repo.
- It can't take the operator's server down: its own droplet, bounded resource use.

## Non-goals

- Billing or subscriptions. Later, and its own spec.
- Sign-in other than Google. Magic links and other providers come when people ask.
- Data shared between users: no shared days, no teams.
- Server-sent push notifications (see Nudges).
- The TUI in hosted mode. It stays a local tool.
- Review Queue in hosted mode. It reads a service on the user's own network.
- Changing the license. The public repo stays MIT.
- Importing a local install's data into a hosted account. Accepting an uploaded SQLite file is
  security work this doesn't need yet.

## Anti-goals

- Any request, background job, or bug that shows one user another user's data.
- The server's own API keys or environment leaking into a user's session.
- Hosted mode as a way to reach the server's private network (SSRF through a user-set URL).
- A local install that breaks, prompts for login, or changes behavior.

## Shape

**One App per signed-in user.** Everything in `internal/app` already assumes one person per
`App`: its database, stream hub, cleanup runner, planner, and agent. Hosted mode keeps a registry
of Apps keyed by user ID, opens a user's App on their first request, and closes it after it has
sat idle for a while. Each App gets its own `httpapi` handler, so the stream, undo, and every
endpoint are per user with no change to how handlers work. Local mode is the same code with
exactly one App and no registry.

**One SQLite file per user.** `<data dir>/users/<user id>.db`, opened and migrated through
`store.Open` like a local database, so a user's file is migrated on first open after a deploy. The
user ID is a random opaque ID, never the email. SQLite on both sides means one SQL dialect, and
hosted runs the queries local users run.

**A central accounts database.** `<data dir>/accounts.db`, also SQLite, with its own migrations:
users (ID, Google subject, email, name, created), and sessions. It holds nothing from
the planner. Billing state lands here later.

**Hosted mode is a subcommand in the public repo:** `whatnow host`. Go's `internal/` packages
can't be imported from another module, so a private repo can't wrap them. Configuration comes
from flags and environment, matching `serve`: listen address, data directory, public URL, Google
client ID and secret, the secrets master key, and the user cap. Secrets come from environment only,
never flags.

## Sign-in

- Google sign-in (OpenID Connect, authorization code flow). The account is keyed by Google's
  subject ID; email and name are kept for display and updated at each sign-in. No PKCE: the server
  is a confidential client with a client secret, and it checks state and nonce. Adding PKCE means
  passing a verifier through the `Provider` seam.
- First sign-in creates the account and the user's database. The browser's time zone
  (`Intl.DateTimeFormat().resolvedOptions().timeZone`) is sent with it and saved as a time zone
  setting in the user's own settings document, which hosted mode shows in settings. The user's App
  opens with that `*time.Location` in place of `time.Local`. The planner, cleanup, agent,
  integrations, and handler each take a copy at construction, so saving a new time zone has the
  registry close that user's App once the save is answered; the next request reopens it in the new
  zone. Only a changed zone does this. Closing an App cancels the user's other requests and drops
  the in-memory cleanup queue, and the theme picker saves settings on every click.
- Sessions are a random token in a cookie (`HttpOnly`, `Secure`, `SameSite=Lax`), stored hashed in
  the accounts database, with a long expiry so phones stay signed in. Sign out deletes the session.
- **User cap.** Once the number of accounts reaches the configured cap, new sign-ups get a page
  saying sign-ups are closed for now. Existing users sign in as usual. This is the main knob for
  keeping the server's load bounded. Launch is sized for tens of users, up to a low hundred: the cap
  starts at 100, on the smallest droplet that runs it comfortably. Each open App holds a hub, a
  cleanup goroutine and queue, and a planner watcher, so idle eviction is what keeps memory flat as
  accounts grow.

## Security in hosted mode

`localOnly` stays as it is for local mode. Hosted mode replaces it with:

- Host must be the configured public host.
- Every `/api/v1/` request needs a valid session, except the session check itself.
- Writes keep the JSON content-type check and also require the `Origin` header to match the public
  URL.

**API keys and integration secrets** move behind a small secrets interface with two
implementations: the OS keychain (local) and encrypted rows in the user's own database (hosted),
AES-GCM under a master key from the environment. The master key never sits on the volume, so a
copy of the backups alone can't read anyone's keys. `settings.Keys` and the integration runner's
secret calls go through it.

**No server environment in hosted users' requests.** Locally, keys fall back to `ANTHROPIC_API_KEY`
and `OPENAI_API_KEY`. In hosted mode that fallback is off, so nobody runs on the server's key. The
model SDKs also read other variables (auth tokens, base URLs, org IDs, custom headers) that would
travel to a user-chosen base URL, so `whatnow host` refuses to start while any `ANTHROPIC_*` or
`OPENAI_*` variable is set.

**Outbound requests can't reach private addresses.** In hosted mode, every request to a URL a user
controls refuses loopback, private, link-local, and metadata addresses, checked on the address
actually dialed, not just the hostname, so DNS rebinding can't get around it. That covers the
LLM base URL setting, Google Calendar iCal addresses, and Jira. The model clients' guarded client
has no overall timeout, since a streamed reply can run long. The guard can still reach the
droplet's own public address, so nothing else on it may listen beyond loopback.

**Settings that don't apply in hosted mode** aren't shown or accepted: allowed hosts, and Review
Queue (not registered with the runner). Base URLs stay, behind the guard above.

**Limits.** Request body size caps, and a per-user rate limit on the endpoints that call the model
(capture cleanup, plan, replan, what now, chat) and on integration pulls, which make the server
fetch user-set URLs. The model cost is the user's, but the request handling and goroutines are
the server's.

## Deleting an account

The settings danger zone's wipe (`DELETE /api/v1/data`) becomes **Delete my account** in hosted
mode: delete their account and sessions first, so no new request can reopen anything, then close
the user's App and delete their database file. A deleted user's ID is never reopened. Litestream
leaves a deleted database's replica and local state in place forever, so a daily guarded sweep in
`docs/hosting.md` removes both; older copies of `accounts.db` age out with the 30-day retention.
An R2 lifecycle rule can't do this: it can't tell a deleted database from an idle one. The
privacy page and the danger zone promise backups are gone within 30 days.

## The web client

The client asks the server what mode it's in and who is signed in (a session endpoint), and
everything below shows only in hosted mode:

- **Landing page** for signed-out visitors: what whatnow is, a screenshot or two, and Sign in with
  Google.
- **Privacy page**, linked from the landing page and the header. In plain words: what's stored
  (tasks, events, how you feel about them, your settings), that API keys are encrypted, that your
  model provider sees what you send it, how backups work and when they expire, and how to delete
  your account.
- **Header nav**: the existing header gains an account menu with the user's name, Privacy, and
  Sign out.
- **Home-screen banner**: a dismissable banner suggesting adding whatnow to the home screen, with a
  short how-to for iOS Safari and Android Chrome. Dismissal is remembered in the browser. A web app
  manifest and icons make the install work. The banner doesn't show once the app is running
  installed (`display-mode: standalone`).
- Onboarding stays as it is: a new user picks a provider and pastes their key.

The landing and privacy pages live in the SvelteKit app like any other route; the static adapter's
fallback already serves every path.

## Nudges

Nudges stay as they are: computed and shown by the open page (`web/src/lib/nudges.ts`). Installing
to the home screen gives an app-like window, not background nudges. Real push, delivered when the
app is closed, needs the server to work out each user's nudges and send them through Web Push, so
it's a follow-up with its own spec.

## Hosting

- Its own DigitalOcean droplet, the smallest that fits, separate from the operator's existing
  server.
- User data on a DigitalOcean Volume mounted on the droplet, so rebuilding the droplet doesn't touch
  it.
- Caddy in front for TLS. `whatnow host` runs under systemd.
- Backups to Cloudflare R2 with Litestream watching the users directory, continuously and covering
  every user's file, including users who sign up after the backup process starts. Retention: 30 days. A restore drill before launch: restore one
  user's file and the accounts database from R2 onto a scratch machine and open them.
- A Linux build recipe in the Justfile next to the Mac release.
- A setup guide, `docs/hosting.md`, written as step-by-step instructions for someone who has never
  made a Volume or an R2 bucket: create the droplet and Volume, mount it, install Caddy, set the
  environment, create the Google OAuth client, set up R2 and backups, run the restore drill, deploy
  an update.

**The private repo** holds only what's specific to this deployment: the real environment file
layout (not its secret values), the Caddyfile, the systemd unit and backup config as deployed, and
later billing work. Everything needed to run hosted mode is public.

## Testing

- The existing suites run unchanged and pass. Local mode stays the default path.
- Isolation: two users signed in, each creates tasks and events, each sees only their own through
  every list endpoint and the stream.
- A request with no session, an expired one, or the wrong `Origin` is refused.
- The environment key fallback is ignored in hosted mode.
- The outbound guard refuses loopback, private, and metadata addresses, including a hostname that
  resolves to one.
- Idle eviction closes an App, and the next request reopens it with its data intact.
- Delete my account removes the file, the account, and its sessions.
- The user cap blocks a new sign-up and lets an existing user in.
- Google sign-in is tested against a fake provider; nothing in the suite calls Google.
