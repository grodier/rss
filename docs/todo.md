# TODO

Work that has been identified but does **not** yet have a GitHub issue. Items marked
"needs planning" are too large or too open-ended to hand to an agent as-is; plan them
first and split them into issues. When an item gets an issue, replace it here with a link
or delete it.

Already tracked as issues: #1–#13 (bug fixes, security hardening, README).

## Feed ingestion (needs planning)

The core missing feature: feeds are stored by URL only and never fetched.

- [ ] **Fetch and parse feeds.** When a feed is added, fetch and parse it (e.g. with
  `gofeed`), fill in title, site URL and description, and save its articles. The
  `articles` table needs a unique `(feed_id, external_id)` so re-fetching doesn't create
  duplicates.
- [ ] **Background refresh.** Re-fetch feeds on a schedule using `last_fetched_at`, skip
  unchanged feeds via ETag / Last-Modified, and back off on errors.
- [ ] **Safe fetching.** The server will fetch user-supplied URLs, so: block private and
  internal addresses (SSRF), set timeouts, cap response size and redirects, and allow only
  http/https.

## Subscriptions and reading (needs planning)

- [ ] **Subscriptions.** Replace the placeholder `POST /subscribe`, show each user only
  their own feeds, and support unsubscribing. Adding a feed that already exists should
  subscribe the user to it (follow-on to #7).
- [ ] **Reading experience.** List articles on the feed page, add an "all my feeds"
  timeline, and track read/unread per user (needs a new table).

## Code structure

- [ ] Put the logged-in user's ID in the request context instead of a true/false flag
  (subscriptions need it).
- [ ] Make the server depend on interfaces instead of the concrete `*psql` repository
  types, so handlers can be tested without a database.
- [ ] Pass request contexts through to database calls (`QueryRowContext` and similar).
- [ ] Give templates a shared base layout instead of repeating the full HTML and nav on
  every page.
- [ ] Return HTML instead of JSON for 404 and 405 responses (deliberately left out of #9).
- [ ] Clean-up: the commented-out feed in `createFeedHandler`, the placeholder
  `internal/ui/static/js/main.js`, the unused `readJSON` helper, the unused `ctx`
  parameter in `Application.Run`, and leftover TODO comments.

## Security

- [ ] Rate-limit login and signup.

## Testing and tooling

- [ ] **CI.** GitHub Actions workflow running `go vet`, `go test` and `staticcheck`, with a
  Postgres service so database tests run in CI. Add "require status checks" to the `main`
  ruleset once it exists.
- [ ] **Shared database test helper** and a documented way to run migrations before tests
  (#7 and #8 will each start one; consolidate).
- [ ] **`CLAUDE.md`** with project conventions and commands so agent sessions behave
  consistently: never push to `main`, one branch per issue named `fix/<slug>`, run
  `go vet ./... && go test ./...` before pushing, PR body includes `Closes #N`.

## Repository and workflow setup (manual, on GitHub)

- [ ] Add a ruleset on `main`: require a pull request before merging, block force pushes
  and branch deletion, empty bypass list, 0 required approvals (sole maintainer).

## Suggested order

1. **Foundation, merged first:** `CLAUDE.md`, shared test helper, CI, and README (#3).
2. **Wave 1 (parallel, mostly separate files):** #1, #2, #6, #9, #12.
3. **Wave 2 (in order, shared files):** #4 then #5; #10 then #13; #7 then #8; #11.
4. **Then plan** feed ingestion and subscriptions in their own sessions, and turn them
   into issues.

Merge one PR at a time; each branch should pull in the latest `main` before opening its PR.
