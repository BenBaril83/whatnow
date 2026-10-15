# whatnow integrations: Review Queue

Review Queue (`oreillymedia-ai-transformation/review-queue`) ranks the pull requests a person
should review. This integration offers its queue as tickets, on the contract in
`2026-10-12-whatnow-integrations-design.md` and the ticket flow in
`2026-10-12-whatnow-jira-design.md`. It needs one new endpoint in Review Queue, specified here and
contributed there as a pull request.

Review Queue may be converted to TypeScript. If its endpoint lands after that, it is rewritten in
TypeScript; the contract below is what both versions owe.

## Behavior

- **It behaves as Jira does:** pulls happen only when asked, a pull offers and the user picks,
  nothing starts ticked, × skips a PR for good, and a picked PR is a one-time copy. Nothing syncs
  back to GitHub.
- **The ranking is Review Queue's.** whatnow shows its order and never re-ranks. Dismissals,
  manual penalties, and age policies set in Review Queue apply, because the endpoint ranks with the
  saved settings.
- **A minimum score cuts the noise**, such as a Monday of maintenance-bot PRs. It's a raw score,
  not Review Queue's High/Medium/Low bands: with default weights a review request alone (100) is
  Medium, the same band as an assigned maintenance PR (60 and up), so a band cut would drop real
  requests with the bots. Scores depend on the user's weights, so each ticket's notes show its
  score to tune the cut by.
- **A re-requested review is offered again.** Jira's rule hides a ticket once any task, open or
  done, has its key as a ref. A PR comes back: the author pushes and asks again, and Review Queue
  ranks that above a first request. So a done or dropped task doesn't hide a PR whose review is re-requested;
  an open task still does. The re-request is a state, not a time: it holds while the user is a
  requested reviewer who has already reviewed, and ends when they submit the new review, since
  GitHub then drops them from the requested reviewers. A task ticked done before the review is
  submitted on GitHub is therefore offered again, which is right: GitHub still says it's waiting on
  them.
- **Review Queue must be running** for a pull to work, and the error says so.

## Review Queue: `GET /api/queue`

The ranked queue the browser builds today, built on the server: read the settings file,
`normalizePreferences`, `repositoryRequest`, `loadQueue`, then `rankQueue`. `rankQueue` and its
domain modules are pure and already run under Node.

- **Leaves out** dismissed PRs, manual or by age, as the queue view does. Penalties count toward
  the score.
- **Answers** `{pulls, fetchedAt}`, in `rankQueue`'s order:

  ```json
  {
    "pulls": [
      {
        "id": "acme/search#123",
        "url": "https://github.com/acme/search/pull/123",
        "title": "Cache facet counts",
        "repository": "acme/search",
        "author": "alexchen",
        "score": 170,
        "reasons": ["Review requested from you", "Small change"],
        "estimate": "5–10 min",
        "rereview": false
      }
    ],
    "fetchedAt": "2026-10-15T14:02:11.000Z"
  }
  ```

  `reasons` are the ranking reasons' text, highest points first. `estimate` is `estimate(pr).label`.
  `rereview` is the `rereview` signal.
- **Errors,** all `{error}`:
  - no settings file, or a `source` of `demo`: `409`, "Set Review Queue's source to GitHub first."
  - settings `validatePullRequest` rejects, such as organization scope with no organization: `409`
    with its message
  - an unreadable settings file: `500`, "Could not read <file>: …", as `GET /api/preferences` says
  - a GitHub failure: `502` with its message, as `POST /api/pulls` says
- **No origin change:** a request from whatnow carries no `Origin` header, which the existing
  cross-origin check already lets through.
- **Tests,** with the node test runner over an injected fetcher and a temporary settings file: the
  ranked order and fields, a dismissed PR left out, a penalty lowering a score, `rereview` set,
  the `409`s, the `500`, and the `502`.

## The contract

- **`Ticket` gains `Rereview bool`** (`rereview`, omitted when false): the source is asking for this
  again, so a done or dropped task with its key doesn't hide it. Jira never sets it. The web client
  sends pulled tickets back to Add whole, so the flag survives the round trip.
- **The runner's dedupe separates open from closed.** The keys on open tasks and skipped keys always
  hide a ticket; the keys on done or dropped tasks hide it unless it's `Rereview`. A dropped review
  is treated like a done one: the user let it go, and a re-request is a new ask. Pull and Add apply the same rule, so a double
  click still adds once. A deleted task still doesn't count.

## Review Queue integration

`internal/integrations/reviewqueue`, ID `reviewqueue`, name "Review Queue",, built like `jira`, knowing nothing of whatnow's storage.

- **Fields:** `url` (text, required; help: "Where Review Queue runs, such as
  http://localhost:5173") and `min_score` (text, optional; help: "Hide PRs scoring below this,
  such as 100"). No secret: the GitHub token stays in Review Queue's environment.
- **One request per pull:** `GET {url}/api/queue`, with a 60-second timeout, longer than Jira's
  because an organization-wide scan is slow.
- **Each PR becomes** `{Key: id, Title: "Review <id>: <title>", Notes: "Score <score> · <reasons,
  comma-joined> · <estimate>", URL: url, Rereview: rereview}`. A part Review Queue left empty is
  left out of the notes, so a PR with no reasons reads "Score 0 · 5–10 min". The title leads with a verb, since a
  task reads as something to do. The key is the task's ref, which links and dedupes it.
- **Filtering:** PRs scoring below `min_score` are dropped; blank keeps all. The rest keep
  Review Queue's order, capped at 50, and the count beyond the cap is the source's `More`.
- **Errors:**
  - no response: "Can't reach Review Queue — is it running?"
  - a timeout: "Review Queue took over a minute — GitHub may be slow, try again", since a slow
    organization scan behind a running Review Queue is the likelier cause
  - any non-`2xx` with an `error`: that text, which covers the demo `409` and GitHub's rate limits
  - a non-`2xx` without one: "Review Queue answered <status>"
  - a `200` that isn't JSON: "couldn't read Review Queue's answer — is the address right?"
  - an address with no scheme or host: "that address isn't a URL, such as http://localhost:5173"
  - a `min_score` that isn't a whole number: "minimum score must be a whole number"

**A picked PR's task:** as a Jira ticket's. `refs` is one `{label: id, url}`, size and energy stay
blank, and it skips the model's cleanup.

## Screens

- **Web:** the ticket picker is already generic; the Review Queue chip and settings card render
  from the integration's fields and `pulls`.
- **TUI:** the ticket step's offer names the ticket integrations that are on, such as "Pull from Jira?"
  or "Pull from Jira and Review Queue?", instead of naming Jira. Everything else is unchanged.

## Testing

**Go:**
- `reviewqueue`, over `httptest`: a PR's mapping (title, notes, ref key, `Rereview`), `min_score`
  blank, cutting, negative, and invalid, the cap and `More`, and each error.
- `integrations.Runner`, with a fake ticket integration: a done task hides a ticket but not a
  `Rereview` one, an open task hides both, a deleted task hides neither, and Add drops by the same
  rule.
- TUI: the offer with Jira and Review Queue both on.

**Review Queue:** the endpoint tests above.
