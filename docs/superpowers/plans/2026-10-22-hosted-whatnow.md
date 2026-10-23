# Hosted whatnow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `whatnow host` command that serves whatnow to many Google-signed-in users, each with their own App and SQLite file, while local mode behaves exactly as it does today.

**Architecture:** Secrets, model clients, and integrations get seams (a secrets store, an HTTP client) that local mode fills with today's behavior. A new `internal/host` package owns accounts, sessions, Google sign-in, a registry of per-user Apps, and the hosted HTTP front door, which authenticates and then hands each request to that user's ordinary `httpapi` handler. The web client asks `/api/v1/session` which mode it's in and shows the landing page, account menu, and install banner only when hosted.

**Tech Stack:** Go 1.27, `modernc.org/sqlite`, `golang.org/x/oauth2`, `github.com/coreos/go-oidc/v3`, `golang.org/x/time/rate`; SvelteKit with the static adapter, Vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-22-hosted-whatnow-design.md`

## Global Constraints

- Local mode is untouched: same default command, flags, no login, SQLite file, keychain keys. Every existing Go, Vitest, and Playwright test passes unchanged except where a task names the change.
- Keychain service stays `whatnow` and account names stay `anthropic`, `openai`, `integration/<id>/[<row>/]<key>`, so existing local keys keep working with no migration.
- Hosted user data: `<data dir>/users/<user id>.db`; accounts: `<data dir>/accounts.db`. User IDs are random opaque IDs, never the email.
- Hosted secrets (Google client ID and secret, seal key) come from environment only, never flags.
- Session cookie: `HttpOnly`, `Secure`, `SameSite=Lax`; stored hashed.
- User cap defaults to 100.
- Backup retention: 30 days.
- New Go dependencies are limited to the three named in Tech Stack.
- Commits: conventional prefix (`feat:`, `docs:`), no `Co-Authored-By` trailer.

## Review Focus

- **An SSE stream open when a user's App is evicted** (idle or time zone change): the stream ends, the browser's EventSource reconnects, and it lands on a fresh App. It must not keep a closed App's hub alive. Test in Task 7.
- **A valid session cookie for an account deleted on another device:** 401 and a cleared cookie, never a recreated empty account. Test in Task 9.
- **A browser time zone that's empty or unknown at sign-up:** the account is created in `UTC`, not refused. Test in Task 8.
- **Two first requests for the same user at once:** one App is opened, not two cleanup runners on one file. Test in Task 7.
- **A user-set URL that redirects to a private address** (an iCal address answering 302 to `http://169.254.169.254/`): refused like a direct request. Test in Task 4.

---

### Task 1: The secrets store

**Files:**
- Create: `internal/secrets/secrets.go`, `internal/secrets/secrets_test.go`
- Create: `internal/store/migrations/0009_secrets.sql`

**Interfaces:**
- Produces:
  - `type Store interface { Get(ctx context.Context, name string) (string, error); Set(ctx context.Context, name, value string) error; Delete(ctx context.Context, name string) error }`. `Get` returns `""` for a name that isn't set; `Delete` of a missing name is not an error.
  - `type Keychain struct{}`, implementing `Store` over `go-keyring` with service `"whatnow"`.
  - `func NewSealed(db *sql.DB, key [32]byte) *Sealed`, implementing `Store` with AES-GCM rows in `secrets`.
  - `func ParseKey(s string) ([32]byte, error)`: standard base64 of exactly 32 bytes.

- [ ] **Step 1: Write the failing tests**

  `TestSealedRoundTrip`: Set `"anthropic"` to `"sk-1"`, Get returns `"sk-1"`; Set again to `"sk-2"`, Get returns `"sk-2"`; Delete, Get returns `""`; Delete again returns nil.
  `TestSealedIsEncrypted`: after Set, `SELECT sealed FROM secrets` doesn't contain the bytes `sk-1`.
  `TestSealedWrongKey`: a `Sealed` over the same db with a different key returns an error from Get, not the value.
  `TestKeychainMissing`: with `keyring.MockInit()`, Get of an unset name returns `""`, nil; Delete of it returns nil.
  `TestParseKey`: 32 random bytes, base64 encoded, parse back equal; 31 bytes and non-base64 return errors.

- [ ] **Step 2: Run them to see them fail**

  Run: `go test ./internal/secrets/`
  Expected: FAIL, package doesn't compile.

- [ ] **Step 3: Implement**

  Migration: `CREATE TABLE secrets (name TEXT PRIMARY KEY, sealed BLOB NOT NULL);`. Local databases get the table too and never use it. `Sealed` stores nonce || ciphertext, with the name as additional data so a row can't be swapped onto another name.

- [ ] **Step 4: Run the tests**

  Run: `go test ./internal/secrets/ ./internal/store/`
  Expected: PASS

- [ ] **Step 5: Commit**: `feat: add a secrets store, over the keychain or sealed in the database`

### Task 2: Keys and model clients go through the store

**Files:**
- Modify: `internal/settings/keys.go`, `internal/settings/settings_test.go` (or a new `keys_test.go`)
- Modify: `internal/llm/anthropic.go`, `internal/llm/openai.go`
- Modify: `internal/httpapi/server.go`, `internal/httpapi/settings.go`, `internal/httpapi/helpers_test.go`, `internal/httpapi/settings_test.go`
- Modify: `internal/app/app.go`, `cmd/whatnow/main.go`, `cmd/whatnow/e2e.go`
- Modify: `web/src/lib/api.ts`

**Interfaces:**
- Consumes: `secrets.Store`, `secrets.Keychain` (Task 1).
- Produces:
  - `const KeyFromSaved KeySource = "saved"`.
  - `type Keys struct` (unexported fields) with `func LocalKeys() Keys` (keychain, environment fallback on) and `func SealedKeys(s secrets.Store) Keys` (that store, no environment fallback, reports `KeyFromSaved`).
  - `func (k Keys) Key(ctx context.Context, p Provider) (string, KeySource, error)`, `func (k Keys) Set(ctx context.Context, p Provider, key string) error`, `func (k Keys) Delete(ctx context.Context, p Provider) error`, replacing the package funcs `Key`, `SetKey`, `DeleteKey` with the same validation and the same keychain-error logging.
  - `type Clients struct { Keys Keys; HTTP *http.Client }`: `HTTP` nil means the SDK default.
  - `func NewClient(ctx context.Context, c Clients, s Settings) (llm.Client, ProviderConfig, error)`.
  - `func NewAnthropic(apiKey, baseURL string, hc *http.Client) Client`, same for `NewOpenAI`; nil `hc` adds no option.
  - `app.Options.NewClient func(context.Context, settings.Clients, settings.Settings) (llm.Client, settings.ProviderConfig, error)`; `App.Clients settings.Clients`; `func (a *App) NewClient(ctx context.Context, s settings.Settings) (llm.Client, settings.ProviderConfig, error)`.
  - `httpapi.Deps.NewClient func(context.Context, settings.Settings) (llm.Client, settings.ProviderConfig, error)` (main passes `a.NewClient`) and `httpapi.Deps.Keys settings.Keys`.

- [ ] **Step 1: Write the failing tests**

  `TestSealedKeysIgnoreEnv`: with `t.Setenv("ANTHROPIC_API_KEY", "from-env")`, `SealedKeys(store).Key(ctx, Anthropic)` returns `""`, `""`; after `Set`, it returns the key and `KeyFromSaved`.
  `TestLocalKeysFallBackToEnv`: with the mock keychain empty and the env set, `LocalKeys().Key` returns `"from-env"`, `KeyFromEnv`.
  `TestNewClientUsesHTTPClient`: an `httptest` server standing in for Anthropic at `BaseURL`; `NewClient` with `Clients{HTTP: client}` whose transport counts requests; one `Complete` call reaches the transport once.

- [ ] **Step 2: Run them to see them fail**

  Run: `go test ./internal/settings/`
  Expected: FAIL, `SealedKeys` undefined.

- [ ] **Step 3: Implement, then move every caller to the new signatures**

  The httpapi settings handlers use `s.Keys`; `testConnection` calls `s.NewClient(r.Context(), saved)`. `app.Open` builds `Clients{Keys: settings.LocalKeys()}` for now (Task 5 adds hosted). `e2e.go` wraps `settings.NewClient` with the new signature. In `api.ts`, `KeySource` gains `'saved'`.

- [ ] **Step 4: Run everything**

  Run: `just test && just lint`
  Expected: PASS

- [ ] **Step 5: Commit**: `feat: route API keys and model clients through a secrets store and an HTTP client`

### Task 3: Integration secrets go through the store

**Files:**
- Modify: `internal/integrations/runner.go`, `internal/integrations/saved.go`, `internal/integrations/runner_test.go`
- Modify: `internal/httpapi/settings.go` (`deleteData`), `internal/httpapi/helpers_test.go`, `internal/app/app.go`

**Interfaces:**
- Consumes: `secrets.Store` (Task 1).
- Produces: `func NewRunner(db *sql.DB, svc *service.Service, hub *stream.Hub, loc *time.Location, now func() time.Time, vault secrets.Store, all ...Integration) *Runner`; `func (r *Runner) DeleteSecrets(ctx context.Context, accounts []string) error` replacing the package func. Account names are unchanged. The parameter is `vault` because `saved.go` already has a func named `store`, and `secrets` would shadow the package.

- [ ] **Step 1: Write the failing test**

  `TestRunnerKeepsSecretsInItsStore`: a runner over a `secrets.NewSealed` store saves a Jira token; the store's `Get(ctx, "integration/jira/token")` (use the real field key from `jira.go`) returns it, and the mock keychain doesn't have it.

- [ ] **Step 2: Run it to see it fail**

  Run: `go test ./internal/integrations/ -run TestRunnerKeepsSecretsInItsStore`
  Expected: FAIL, too many arguments to `NewRunner`.

- [ ] **Step 3: Implement**

  `secret(acct)` and the `keyring` calls become `r.vault` calls with the method's context. `app.Open` and the test helpers pass `secrets.Keychain{}`.

- [ ] **Step 4: Run everything**

  Run: `go test ./...`
  Expected: PASS

- [ ] **Step 5: Commit**: `feat: keep integration secrets in the runner's secrets store`

### Task 4: The outbound guard

**Files:**
- Create: `internal/outbound/outbound.go`, `internal/outbound/outbound_test.go`

**Interfaces:**
- Produces: `func Client(timeout time.Duration) *http.Client`: dials only public unicast addresses. `var ErrBlocked = errors.New("address not allowed")`.

- [ ] **Step 1: Write the failing tests**

  `TestClientRefuses`, parametrized over `127.0.0.1`, `[::1]`, `10.0.0.1`, `192.168.1.1`, `172.16.0.1`, `169.254.169.254`, `100.64.0.1`, `[fc00::1]`, `0.0.0.0`: a GET to `http://<addr>/` returns an error that `errors.Is(err, ErrBlocked)`.
  `TestClientRefusesRedirectToPrivate`: an `httptest` server (on loopback, so the test dials it through a client built by an unexported `newClient(allow func(netip.Addr) bool)` that allows that one address) answers 302 to `http://169.254.169.254/`; the GET fails with `ErrBlocked`.
  `TestClientRefusesHostnameResolvingPrivate`: `http://localhost:<port>/` fails with `ErrBlocked`.

- [ ] **Step 2: Run them to see them fail**

  Run: `go test ./internal/outbound/`
  Expected: FAIL, package doesn't compile.

- [ ] **Step 3: Implement**

  A `net.Dialer` whose `Control` parses the address being dialed and refuses anything that isn't global unicast, or is private, loopback, link-local, unspecified, or in `100.64.0.0/10`. Checking in `Control` covers DNS rebinding and every redirect hop. Transport `Proxy` is nil so an environment proxy can't route around it.

- [ ] **Step 4: Run the tests**

  Run: `go test ./internal/outbound/`
  Expected: PASS

- [ ] **Step 5: Commit**: `feat: add an HTTP client that can't reach private addresses`

### Task 5: Hosted options for an App and its handler

**Files:**
- Modify: `internal/settings/settings.go`, `internal/settings/settings_test.go`
- Modify: `internal/app/app.go`, `internal/app/app_test.go`
- Modify: `internal/httpapi/server.go`, `internal/httpapi/settings.go`, `internal/httpapi/settings_test.go`
- Modify: `web/src/lib/api.ts` (the `time_zone` field)

**Interfaces:**
- Consumes: `secrets.NewSealed` (Task 1), `settings.SealedKeys`, `settings.Clients` (Task 2), `NewRunner` (Task 3), `outbound.Client` (Task 4).
- Produces:
  - `Settings.TimeZone string` (`json:"time_zone"`): an IANA name, empty in local mode. `Validate` rejects a name `time.LoadLocation` can't load.
  - `type Hosted struct { SealKey [32]byte }` in `app`, and `Options.Hosted *Hosted`. With it set, `app.Open` seals secrets in the user's database, uses `SealedKeys`, gives the model clients and integrations `outbound.Client(30 * time.Second)`, leaves out Review Queue, and sets `Loc` from `TimeZone` (falling back to `UTC`), ignoring `Options.Loc`.
  - `httpapi.Deps.Hosted bool`: when true, `New` returns the mux without `localOnly`, and `putSettings` saves `AllowedHosts` as empty whatever is sent.
  - `GET /api/v1/session` in `httpapi`, answering `{"hosted": false}`. The hosted front door answers it itself (Task 9).

- [ ] **Step 1: Write the failing tests**

  `TestOpenHostedUsesUserTimeZone`: a db whose settings say `America/Chicago`, opened with `Hosted`; `a.Loc.String()` is `America/Chicago`.
  `TestOpenHostedLeavesOutReviewQueue`: `a.Integrations.Views` has no `reviewqueue` view; local has one.
  `TestOpenHostedSealsKeys`: `a.Clients.Keys.Set(ctx, Anthropic, "k")`; the user's `secrets` table has a row.
  `TestValidateTimeZone`: `"America/Chicago"` and `""` pass; `"Mars/Olympus"` fails with `ErrInvalid`.
  `TestHostedHandlerDropsAllowedHosts`: PUT settings with `allowed_hosts: ["box.ts.net"]` to a `Hosted: true` handler, then GET returns `[]`.
  `TestHostedHandlerSkipsLocalOnly`: a GET with `Host: whatnow.example.com` succeeds on a `Hosted: true` handler and is 403 on a local one.
  `TestSessionLocal`: GET `/api/v1/session` returns `{"hosted": false}`.

- [ ] **Step 2: Run them to see them fail**

  Run: `go test ./internal/app/ ./internal/settings/ ./internal/httpapi/`
  Expected: FAIL, `Hosted` undefined.

- [ ] **Step 3: Implement**

  `main.go`'s `newServer` passes `a.Clients.Keys` and `a.NewClient`; local mode leaves `Hosted` nil and false.

- [ ] **Step 4: Run everything**

  Run: `just test && just lint`
  Expected: PASS

- [ ] **Step 5: Commit**: `feat: open an App in hosted mode, with sealed keys, a guarded client, and the user's time zone`

### Task 6: The accounts database

**Files:**
- Modify: `internal/store/store.go`
- Create: `internal/host/accounts.go`, `internal/host/accounts_test.go`, `internal/host/migrations/0001_accounts.sql`

**Interfaces:**
- Produces:
  - `func store.OpenMigrated(path string, migrations fs.FS) (*sql.DB, error)`; `store.Open` calls it with its own FS.
  - `type User struct { ID, GoogleSub, Email, Name string; CreatedAt time.Time }`.
  - `func OpenAccounts(path string, now func() time.Time) (*Accounts, error)`, `(*Accounts) Close() error`.
  - `func (a *Accounts) SignIn(ctx context.Context, id Identity, maxUsers int) (User, bool, error)`: finds by Google subject, refreshing email and name, or creates the account and returns `true`. Returns `ErrFull` when creating would exceed `maxUsers`.
  - `func (a *Accounts) CreateSession(ctx context.Context, userID string) (string, error)`: a random 32-byte token, base64url; only its SHA-256 is stored; expires in 90 days.
  - `func (a *Accounts) UserForSession(ctx context.Context, token string) (User, error)`: `ErrNoSession` when it's unknown or expired.
  - `func (a *Accounts) DeleteSession(ctx context.Context, token string) error`, `func (a *Accounts) DeleteUser(ctx context.Context, id string) error` (sessions cascade).
  - `type Identity struct { Subject, Email, Name string }`, shared with Task 8.

- [ ] **Step 1: Write the failing tests**

  `TestSignInCreatesThenFinds`: the first `SignIn` returns created `true` and a 32-hex-character ID; the second, with a new name, returns the same ID, created `false`, and the new name.
  `TestSignInFull`: with `maxUsers` 1 and one account, a new subject gets `ErrFull` and the existing one still signs in.
  `TestSessionRoundTrip`: `CreateSession` then `UserForSession` returns the user; the `sessions` table doesn't contain the token.
  `TestSessionExpires`: with `now` moved 91 days on, `UserForSession` returns `ErrNoSession`.
  `TestDeleteUserEndsSessions`: after `DeleteUser`, `UserForSession` returns `ErrNoSession`.

- [ ] **Step 2: Run them to see them fail**

  Run: `go test ./internal/host/`
  Expected: FAIL, package doesn't compile.

- [ ] **Step 3: Implement**

  Schema: `users(id TEXT PRIMARY KEY, google_sub TEXT NOT NULL UNIQUE, email TEXT NOT NULL, name TEXT NOT NULL, created_at TEXT NOT NULL)` and `sessions(token_hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, expires_at TEXT NOT NULL)`. Counting and inserting happen in one transaction.

- [ ] **Step 4: Run the tests**

  Run: `go test ./internal/host/ ./internal/store/`
  Expected: PASS

- [ ] **Step 5: Commit**: `feat: add the hosted accounts database, with sessions and a user cap`

### Task 7: The App registry

**Files:**
- Create: `internal/host/registry.go`, `internal/host/registry_test.go`

**Interfaces:**
- Consumes: `app.Open`, `app.Options.Hosted` (Task 5).
- Produces:
  - `func NewRegistry(dir string, open func(userID, dbPath string) (*app.App, http.Handler, error), idle time.Duration, now func() time.Time) *Registry`. The host passes an `open` that builds the App and its `httpapi` handler with `Hosted: true`.
  - `func (r *Registry) Serve(w http.ResponseWriter, req *http.Request, userID string)`: opens the user's App if needed, holds it for the request, and runs the request with a context that is also cancelled when the App is evicted.
  - `func (r *Registry) Evict(userID string)`: cancels the user's in-flight requests, waits for them, and closes the App. A later `Serve` reopens it.
  - `func (r *Registry) DBPath(userID string) string`: `<dir>/users/<userID>.db`.
  - `func (r *Registry) Run(ctx context.Context)`: closes Apps that have had no request in flight for `idle`, checking every minute. `func (r *Registry) Close()` closes every App.
  - The host uses an idle timeout of 15 minutes.

- [ ] **Step 1: Write the failing tests**

  `TestServeOpensOnce`: 20 concurrent `Serve` calls for one user call `open` once.
  `TestIdleCloses`: after a request finishes and the clock moves past `idle`, a reaper pass closes the App (`open` is called again on the next `Serve`), and the user's data is still there.
  `TestOpenRequestKeepsAppAlive`: a request blocked inside the handler stops the reaper from closing the App past `idle`.
  `TestEvictEndsStream`: a handler blocked on `<-req.Context().Done()`, as the SSE stream is, returns once `Evict` runs, and `Evict` returns after it.
  `TestUsersAreSeparate`: two user IDs get two different DB paths and two `open` calls.

- [ ] **Step 2: Run them to see them fail**

  Run: `go test ./internal/host/ -run 'Serve|Idle|Evict|Users|OpenRequest'`
  Expected: FAIL, `NewRegistry` undefined.

- [ ] **Step 3: Implement**

  One mutex over a map of entries. Each entry holds the App, its handler, an in-flight count, the time it went idle, and a cancellable context. A request's context is cancelled by either its own context or the entry's (`context.AfterFunc`). Expose the reaper's single pass as an unexported method so tests don't wait on a ticker.

- [ ] **Step 4: Run the tests with the race detector**

  Run: `go test -race ./internal/host/`
  Expected: PASS

- [ ] **Step 5: Commit**: `feat: keep one App per hosted user, opened on demand and closed when idle`

### Task 8: Google sign-in

**Files:**
- Create: `internal/host/google.go`, `internal/host/signin.go`, `internal/host/signin_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `Accounts.SignIn`, `CreateSession`, `Identity` (Task 6); `Registry.DBPath` (Task 7); `settings.Save`, `store.Open`.
- Produces:
  - `type Provider interface { AuthURL(state, nonce string) string; Exchange(ctx context.Context, code, nonce string) (Identity, error) }`. It's the seam for the fake in tests.
  - `func NewGoogle(ctx context.Context, clientID, clientSecret, redirectURL string) (*Google, error)` with `golang.org/x/oauth2` and `go-oidc` against `https://accounts.google.com`, scopes `openid email profile`. `Exchange` verifies the ID token and nonce, and refuses `email_verified` false.
  - Handlers on the host's mux (Task 9 mounts them): `GET /auth/google?tz=<IANA>` sets a 10-minute `whatnow_oauth` cookie (state, nonce, time zone) and redirects to `AuthURL`. `GET /auth/google/callback` checks state, exchanges, and signs in. A new account gets its database created with `settings.Defaults()` plus `TimeZone` (unknown or empty means `UTC`) before any App opens. Then it sets `whatnow_session` and redirects to `/`. `ErrFull` redirects to `/?signups=closed`.

- [ ] **Step 1: Write the failing tests** (a fake `Provider` that returns a fixed `Identity` and records the nonce)

  `TestSignInFlow`: GET `/auth/google?tz=America/Chicago` redirects to the fake's URL and sets `whatnow_oauth`; the callback with that cookie and the state redirects to `/`, sets an `HttpOnly`, `Secure`, `SameSite=Lax` `whatnow_session`, and the new user's database has `time_zone` `America/Chicago`.
  `TestSignInBadState`: a callback whose state doesn't match the cookie gets 400 and no session.
  `TestSignInUnknownTimeZone`: `tz=Nowhere/Special` and a missing `tz` both create the account with `UTC`.
  `TestSignInFull`: at the cap, a new identity redirects to `/?signups=closed` with no session, and no database file is created.

- [ ] **Step 2: Run them to see them fail**

  Run: `go test ./internal/host/ -run SignIn`
  Expected: FAIL, undefined.

- [ ] **Step 3: Implement**

  Run: `go get golang.org/x/oauth2 github.com/coreos/go-oidc/v3`. `NewGoogle` isn't unit tested, since it talks to Google. Keep it thin, and leave every decision in `signin.go`.

- [ ] **Step 4: Run the tests**

  Run: `go test ./internal/host/`
  Expected: PASS

- [ ] **Step 5: Commit**: `feat: sign in to hosted whatnow with Google`

### Task 9: The hosted front door

**Files:**
- Create: `internal/host/server.go`, `internal/host/server_test.go`
- Modify: `go.mod`, `go.sum` (`golang.org/x/time/rate`)

**Interfaces:**
- Consumes: Tasks 6–8.
- Produces: `type Config struct { PublicURL *url.URL; MaxUsers int; Provider Provider; Static http.Handler }` and `func New(cfg Config, accounts *Accounts, registry *Registry) http.Handler`, routing:
  - Every request: `Host` must equal `PublicURL.Host`, or 403. Request bodies are capped at 1 MiB.
  - Writes (not GET or HEAD): `Origin` must equal `PublicURL`'s origin and `Content-Type` must be `application/json`, or 403. The OAuth callback is a GET, so it's unaffected.
  - `GET /api/v1/session`: `{"hosted": true, "user": {"name", "email"}}`, or `"user": null` when signed out. No session needed.
  - `POST /auth/logout`: deletes the session, clears the cookie, 204.
  - `DELETE /api/v1/data`: `Evict`, remove the user's `.db`, `-wal`, and `-shm` files, `DeleteUser`, clear the cookie, 204.
  - `PUT /api/v1/settings`: served by the user's handler; when it answers 200 and `time_zone` changed, `Evict` the user afterward.
  - Other `/api/v1/` requests: a session is required (401 otherwise; a session for a deleted user also clears the cookie). Model-calling endpoints go through a per-user `rate.Limiter` of 120 per minute with a burst of 30, answering 429. Those are POST `/api/v1/tasks`, `/today/start`, `/today/replan`, `/today/wrap`, `/whatnow`, `/chat`, `/settings/test`, and PUT `/today/pace`. Then `registry.Serve`.
  - Everything else: `Static`.

- [ ] **Step 1: Write the failing tests** (real `Accounts`, a real `Registry` opening real hosted Apps in `t.TempDir()`, sessions made through `Accounts` directly)

  `TestUsersSeeOnlyTheirOwn`: users A and B each POST a task and an event. Each one's GET `/api/v1/tasks`, `/api/v1/events?date=…`, and `/api/v1/today` show only their own. A's open `/api/v1/stream` receives no message when B writes.
  `TestNeedsSession`: GET `/api/v1/tasks` with no cookie, an unknown token, or an expired one is 401.
  `TestDeletedUserSession`: after `DeleteUser`, the old cookie gets 401 with a cleared `whatnow_session`, and no new account or database file appears.
  `TestWrongOriginRefused`: a POST with `Origin: https://evil.example` is 403; a GET with the wrong `Host` is 403.
  `TestSessionEndpoint`: signed in, it returns the name and email; signed out, `user` is null.
  `TestDeleteAccount`: afterward the database file is gone, the account is gone, and the cookie is cleared.
  `TestTimeZoneChangeReopens`: PUT settings with a new `time_zone`, then GET `/api/v1/today` uses the new zone (the App was reopened: `open` count is 2).
  `TestRateLimited`: the 31st immediate POST `/api/v1/whatnow` gets 429; GET `/api/v1/tasks` is never limited.

- [ ] **Step 2: Run them to see them fail**

  Run: `go test ./internal/host/ -run 'Users|Session|Origin|Delete|TimeZone|Rate'`
  Expected: FAIL, `New` undefined.

- [ ] **Step 3: Implement**

  Run: `go get golang.org/x/time/rate`. Keep limiters in a map keyed by user ID, dropped when the user's App is evicted.

- [ ] **Step 4: Run with the race detector**

  Run: `go test -race ./internal/host/`
  Expected: PASS

- [ ] **Step 5: Commit**: `feat: serve hosted users behind sign-in, each through their own App`

### Task 10: The `whatnow host` command

**Files:**
- Modify: `cmd/whatnow/main.go`, `cmd/whatnow/main_test.go`, `Justfile`

**Interfaces:**
- Consumes: `host.OpenAccounts`, `host.NewRegistry`, `host.NewGoogle`, `host.New`, `secrets.ParseKey`, `app.Hosted`.
- Produces: `whatnow host -addr 127.0.0.1:8080 -data DIR -public-url URL [-max-users 100]`. Environment: `WHATNOW_GOOGLE_CLIENT_ID`, `WHATNOW_GOOGLE_CLIENT_SECRET`, `WHATNOW_SEAL_KEY`. The usage text gains the line. A Justfile recipe `build-linux` writes `dist/whatnow-linux-amd64`, built like the Mac release (`-trimpath`, `-s -w`, version stamped from `git describe --tags --always`) after `web`.

- [ ] **Step 1: Write the failing test**

  `TestHostConfigErrors`: a table of missing `-data`, missing `-public-url`, a non-https `-public-url`, a missing environment variable, and a seal key of the wrong length. Each makes the unexported `hostConfig(args []string, getenv func(string) string)` return an error naming the problem.

- [ ] **Step 2: Run it to see it fail**

  Run: `go test ./cmd/whatnow/ -run TestHostConfigErrors`
  Expected: FAIL, undefined.

- [ ] **Step 3: Implement**

  `host` creates `<data>/users`, opens accounts, and builds the registry with an `open` that calls `app.Open` with `Hosted` and the same `newClient` the local commands use, plus `httpapi.New` with `Hosted: true` and `web.Handler()`. It runs `registry.Run`, and logs the listen address and data directory. `WHATNOW_DEMO_CLOCK` is ignored here.

- [ ] **Step 4: Run everything, and build**

  Run: `just test && just lint && just build-linux`
  Expected: PASS, and `dist/whatnow-linux-amd64` exists.

- [ ] **Step 5: Commit**: `feat: add the whatnow host command`

### Task 11: Signed-out pages and the account menu

**Files:**
- Create: `web/src/lib/session.svelte.ts`, `web/src/lib/session.test.ts`
- Create: `web/src/lib/components/Landing.svelte`, `web/src/routes/privacy/+page.svelte`
- Modify: `web/src/lib/api.ts`, `web/src/routes/+layout.svelte`, `web/src/lib/components/Header.svelte`

**Interfaces:**
- Consumes: `GET /api/v1/session`, `GET /auth/google?tz=`, `POST /auth/logout` (Tasks 5, 8, 9).
- Produces:
  - `api.session(): Promise<Session>` with `type Session = { hosted: boolean; user: { name: string; email: string } | null }`, and `api.logout(): Promise<void>`.
  - `signInURL(tz: string): string` returning `/auth/google?tz=<encoded>`, and `showLanding(s: Session, path: string): boolean`, which is true when hosted, signed out, and not on `/privacy`.

- [ ] **Step 1: Write the failing tests**

  `showLanding`: false for `{hosted: false}`; true for hosted with a null user on `/` and `/settings`; false on `/privacy`; false when signed in.
  `signInURL('America/New_York')` is `/auth/google?tz=America%2FNew_York`.

- [ ] **Step 2: Run them to see them fail**

  Run: `cd web && npx vitest run src/lib/session.test.ts`
  Expected: FAIL

- [ ] **Step 3: Implement**

  The root layout loads the session before `live.start()`. When `showLanding` is true, it renders `Landing` instead of the route and never starts the stream. `Landing` covers what whatnow is, with screenshots from `docs/screenshots` copied into `web/static`, a **Sign in with Google** button, a Privacy link, and a "sign-ups are closed for now" note when the URL has `signups=closed`. The privacy page covers, in plain words: what's stored (tasks, events, how you feel about them, settings); that API keys are encrypted; that your model provider sees what you send it; that backups are kept 30 days; and how to delete your account. The header gains an account menu (name, Privacy, Sign out) only when hosted and signed in.

- [ ] **Step 4: Run the suites**

  Run: `just test && just lint && just e2e`
  Expected: PASS. Local mode never shows the landing page or account menu.

- [ ] **Step 5: Commit**: `feat: show a landing page, privacy page, and account menu in hosted mode`

### Task 12: Hosted settings

**Files:**
- Modify: `web/src/routes/settings/+page.svelte`, `web/src/routes/welcome/+page.svelte`, `web/src/lib/components/DangerZone.svelte`

**Interfaces:**
- Consumes: `Session` (Task 11), `time_zone` and `'saved'` (Tasks 2, 5).

- [ ] **Step 1: Implement**

  When hosted:
  - **Settings:** hide allowed hosts. Show a time zone select from `Intl.supportedValuesOf('timeZone')`. Show a `'saved'` key as "Saved, encrypted on the server".
  - **Welcome:** replace "It stays in your system keychain, never in whatnow's database." with "It's stored encrypted on the server, and only used to talk to your model provider."
  - **Danger zone:** title the button **Delete my account**, with the phrase `delete my account` and copy saying it deletes the account and everything in it, and that backups are gone within 30 days.

  Local copy is unchanged. Review Queue disappears on its own, since the server doesn't register it.

- [ ] **Step 2: Run the suites**

  Run: `just test && just lint && just e2e`
  Expected: PASS

- [ ] **Step 3: Commit**: `feat: adapt settings, onboarding, and the danger zone to hosted mode`

### Task 13: Installable, with a home-screen banner

**Files:**
- Create: `web/static/manifest.webmanifest`, `web/static/icon-192.png`, `web/static/icon-512.png`, `web/static/apple-touch-icon.png`
- Create: `web/src/lib/install.ts`, `web/src/lib/install.test.ts`, `web/src/lib/components/InstallBanner.svelte`
- Modify: `web/src/app.html`, `web/src/routes/(app)/+layout.svelte`

**Interfaces:**
- Produces: `installBannerDue(hosted: boolean, standalone: boolean, dismissed: boolean): boolean` and `installPlatform(userAgent: string): 'ios' | 'android' | 'other'`.

- [ ] **Step 1: Write the failing tests**

  `installBannerDue` is true only for `(true, false, false)`.
  `installPlatform`: an iPhone Safari user agent is `ios`, a Pixel Chrome one is `android`, and desktop Firefox is `other`.

- [ ] **Step 2: Run them to see them fail**

  Run: `cd web && npx vitest run src/lib/install.test.ts`
  Expected: FAIL

- [ ] **Step 3: Implement**

  Render the PNGs from `web/src/lib/assets/favicon.svg` with any rasterizer and commit them. The manifest has `name` `whatnow`, `display` `standalone`, `start_url` `/`, and the two icons. `app.html` links the manifest and the apple-touch-icon in both modes, which is harmless locally. The banner sits at the top of the main app and uses `matchMedia('(display-mode: standalone)')`. Dismissal is stored in `localStorage` under `whatnow.install-dismissed`, wrapped in try/catch. It shows two or three steps for the user's platform: iOS is Share, then Add to Home Screen; Android is the menu, then Install app; other platforms get both.

- [ ] **Step 4: Run the suites**

  Run: `just test && just lint`
  Expected: PASS

- [ ] **Step 5: Commit**: `feat: make whatnow installable, with a home-screen banner in hosted mode`

### Task 14: The hosting guide and backups

**Files:**
- Create: `docs/hosting.md`
- Modify: `README.md` (one line pointing at it)

- [ ] **Step 1: Settle the backup tool**

  Check Litestream's current docs for replicating a directory whose files keep appearing. If it can, write that config. If it can't, choose a setup that backs up every user's file continuously, including users added after it starts. If nothing meets that bar, stop and report back before settling for less.

- [ ] **Step 2: Write `docs/hosting.md`**

  Step by step, for someone who has never made a Volume or an R2 bucket:
  - Create the droplet and a Volume, mount it at `/mnt/whatnow`, and add it to `fstab`.
  - Create a `whatnow` system user.
  - Install Caddy with a two-line Caddyfile reverse-proxying the domain to `127.0.0.1:8080`.
  - Create the Google OAuth client, with the redirect URI `https://<domain>/auth/google/callback`.
  - Generate the seal key (`openssl rand -base64 32`), keep a copy off the server, and write the environment file (mode 600) and the systemd unit.
  - Create the R2 bucket, its API token, and the backup config with 30-day retention.
  - Run the restore drill: restore `accounts.db` and one user's file onto another machine and open them with `sqlite3`.
  - Deploy an update: `just build-linux`, copy the binary, `systemctl restart whatnow`.

  Explain why the seal key is kept off the Volume: without it, the backups can't decrypt anyone's keys.

- [ ] **Step 3: Commit**: `docs: add a guide to hosting whatnow`
