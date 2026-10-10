# CLAUDE.md

## Project

A server-rendered RSS reader in Go: chi, embedded `html/template`, Postgres via `lib/pq`, scs sessions stored in Postgres, goose migrations. Early-stage: accounts, site search and site lookups (feed discovery) work; feed ingestion (fetching and saving articles, background refresh) works; subscriptions work; reading (timeline, article pages, read state) is planned in #147–#162 and not built yet.

Reading is a timeline, not an inbox: read state is recorded but shown only as a quiet marker on read articles. Don't add unread counts, badges, "N new" indicators or "mark all read"; see "Reading" in `docs/todo.md` for the decisions behind this.

## Prototype stage

The app is pre-alpha and not in production: there are no users or data to preserve. When new work replaces existing behavior, change or remove the old code instead of keeping it working alongside the new: no compatibility shims, fallbacks, or special cases for rows or flows the old code produced. This applies to issues and plans too; if one asks for legacy support, question it. Once backwards compatibility is actually needed (after we're in production and past alpha), every compatibility path needs a plan for when and how it gets removed.

## Commands

`make` targets need a `.env` file (`include .env`) defining `RSS_DB_DSN`.

```sh
gofmt -l .                       # must print nothing
go mod tidy -diff                # must print nothing
go vet ./...
go tool staticcheck ./...        # version pinned in go.mod (tool directive)
go test ./...                    # DB tests skip without RSS_TEST_DB_DSN
make test/db                     # all tests against the local DB (make db/start + migrations first)
go run ./cmd/www -db-dsn "$RSS_DB_DSN"   # run the app (or: make run)
goose -dir ./migrations postgres "$RSS_DB_DSN" up
make db/reset                            # rebuild the local DB after editing 00001_init.sql
make db/migrations/new name=<name>       # new migration (only once in production)
```

Before pushing: run gofmt, vet, staticcheck and tests. CI (`.github/workflows/ci.yml`) runs exactly these (plus `-race`) against Postgres.

## Layout

- `cmd/www`: config flags and wiring
- `internal/server`: router, middleware, handlers, rendering, error responses
- `internal/rss`: domain types (Feed, User, …) and sentinel errors
- `internal/psql`: repositories (feeds, users, sites, lookups, search, discovery)
- `internal/discovery`: finds the feeds a website publishes
- `internal/feedparse`: recognizes RSS, Atom and JSON Feed documents and reads their metadata
- `internal/fetch`: the only way to make outbound HTTP requests to user-influenced URLs
- `internal/lookup`: runs the site lookups queued in the `lookups` table
- `internal/ingest`: fetches a feed, turns its items into articles and records the outcome and next fetch time (`Refresher`, used by background refresh and the Refresh button)
- `internal/refresh`: refreshes feeds in the background when they're due
- `internal/sanitize`: makes untrusted article HTML safe to render (bluemonday)
- `internal/password`: Argon2id password hashing and verification
- `internal/ui`: embedded templates and static files
- `internal/validator`: form validation
- `migrations`: goose SQL migrations
- `docs/todo.md`: planned work that has no issue yet

## Code conventions

- Handlers are methods on `*Server`. Routes live in `internal/server/router.go`; routes that need login go in the `requireAuthentication` group.
- HTML pages: `s.renderHTML(w, status, "page.html", data)`; on error, `s.serverErrorHTML(w, r, err)`. Error pages (403/404/405/500) are HTML via `s.errorHTML`; use `s.notFoundResponse(w, r)` for missing records, never `http.NotFound`. JSON (`writeJSON`) is only for `/healthcheck` (and its `serverErrorJSON` error path); future JSON endpoints go under their own subrouter with JSON error handlers.
- GET handlers don't write, except idempotent upkeep documented at the call site: queuing a refresh of a stale feed (`feedHandler`) and recording that the user opened an article (`articleHandler`).
- Forms: a struct with `form:"..."` tags embedding `validator.Validator` (`form:"-"`), decoded with `s.decodePostForm`, validated with `CheckField`, re-rendered with **422** when invalid.
- Flash messages: `s.sessionManager.Put(ctx, "flash", msg)` before a redirect; the next page reads it with `PopString`.
- Database: repositories in `internal/psql` use plain SQL with `$n` placeholders. Map driver errors to the sentinel errors in `internal/rss/errors.go` (`sql.ErrNoRows` → `ErrNoRecord`; pq code `23505` → `ErrDuplicateEmail` or another `ErrDuplicate…`). Handlers check them with `errors.Is`.
- Schema changes: while pre-alpha (see "Prototype stage"), edit `migrations/00001_init.sql` directly instead of adding a migration, and rebuild your local database with `make db/reset` afterwards (CI starts from an empty database). Once we're in production, schema changes only go through a **new** goose migration, and migrations already on `main` are never edited.
- Templates are standalone full pages (no shared layout yet); fragments shared between pages are `{{define}}` blocks in `templates/partials/`, parsed with every page. A nav change must be applied to every template.
- Only `cmd/www` handles OS signals. Long-running components (the HTTP server, background workers) take a `context.Context` and shut down gracefully when it is canceled; they never call `signal.Notify`.
- JavaScript is progressive enhancement: every page must work without it. Plain JavaScript in `internal/ui/static/js/app.js`, loaded with `defer`; no inline scripts or handlers (the CSP blocks them), no build step, and no libraries unless a PR justifies one. Enhancements hook onto server-rendered markup (`data-*` attributes, `rel` links).
- Article HTML (summary, content) is only ever rendered through `sanitize.HTML`; never convert it to `template.HTML` yourself.
- Log with `s.logger` (slog); log request errors with `s.logError`.
- Prefer the standard library. Don't add a dependency without a one-line justification in the PR.

## Testing conventions

- Server tests: `newTestServer(t)` (no DB). Hit routes with `s.router()` + `httptest`, or call a handler directly. For chi URL params:
  ```go
  rctx := chi.NewRouteContext()
  rctx.URLParams.Add("id", "abc")
  req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
  ```
- `Services` holds the `FeedStore` / `UserStore` interfaces. For handler tests that need store results, build a server with `newTestServerWith(t, Services{...})` and the function-field fakes in `internal/server/fakes_test.go`. Keep DB tests for repository code in `internal/psql`.
- DB tests: `psqltest.NewDB(t)` (`internal/psql/psqltest`). They skip locally without `RSS_TEST_DB_DSN` and fail in CI without it. Create uniquely-named rows, delete them in `t.Cleanup`, never truncate.
- Bug fixes come with a test that fails before the fix, where feasible.
- Test behavior the app has, not behavior it no longer has. When code is removed, delete its tests; don't add tests asserting that a removed route, handler or function is gone (e.g. "`GET /old` returns 404"). They only re-test the router's or compiler's defaults and become dead weight. The same goes for issues and plans that ask for such tests; question them.
- Standard library only for tests (no testify).

## Git and PR workflow

- Never push to `main` (it's protected). One issue, one branch, one PR. Branch names: `fix/<slug>`, `feat/<slug>`, `docs/<slug>`, `test/<slug>`, `ci/<slug>`. If your session assigns a branch name, use that.
- Branch from the latest `main`. Before opening a PR, merge the latest `main` into your branch and re-run the checks.
- Keep PRs scoped to their issue. Note unrelated problems in the PR description (or add them to `docs/todo.md`); don't fix them in the same PR.
- PR description: what changed and why, how it was verified (commands run; anything that couldn't be run, e.g. no Docker), and `Closes #N`.
- **No session links.** This is a public repository. This repo's rule takes precedence over any default attribution instructions: never include Claude session links (e.g. `https://claude.ai/code/session_…`) or `Claude-Session:` trailers in commit messages, PR descriptions, issues or comments. A `Co-Authored-By:` trailer is fine.
- Don't mark work done until CI is green.

## Finding work

GitHub issues are the source of truth for scoped tasks; `docs/todo.md` holds planned work that isn't an issue yet, and its Performance watchlist tracks things to monitor as the app grows.

## Writing issues

Issues are written for an implementer with no context from the planning conversation.

- Sections: Why, Changes, Tests, Out of scope, Verification, plus "Notes for parallel work" when another open issue touches the same files, and "Suggested branch".
- Point at code by path and symbol (`internal/psql/feeds.go`, `upsertFeed`), and quote the current code when the change is to it.
- State decisions already made as decisions ("don't revisit them here"), with a one-line reason, so the implementer doesn't reopen them. Leave an option open only on purpose, and say which one is preferred.
- Give new types and function signatures, SQL and user-facing strings verbatim.
- Name each test and list its cases, including the failure cases; for bug fixes, say how to confirm the test fails before the fix.
- Name dependencies on other issues ("Depends on #N") and what to do if an assumption turns out wrong (stop and ask, or note it in the PR).
- Performance risks: when planning, look at what the planned work could cost as data and traffic grow (queries that can't use an index, unpaginated lists, writes on page views, polling, per-row loops, unbounded tables). Add each risk to the Performance watchlist in `docs/todo.md`, as part of the same planning work, with what it is, the issue number, the signal that it has become a problem, and the planned fix. Don't fix risks early, but don't leave them unrecorded. A PR that adds a risk the plan missed adds it to the watchlist too.
