# CLAUDE.md

## Project

A server-rendered RSS reader in Go: chi, embedded `html/template`, Postgres via `lib/pq`, scs sessions stored in Postgres, goose migrations. Pre-alpha (see "Prototype stage"); `docs/features.md` describes what works.

Reading is a timeline, not an inbox: read state is recorded but shown only as a quiet marker on read articles. Don't add unread counts, badges, "N new" indicators or "mark all read"; see "Reading" in `docs/decisions.md` for the decisions behind this.

## Prototype stage

The app is pre-alpha and not in production: there are no users or data to preserve. That includes pre-alpha testers' data: schema changes still edit the initial migration and wipe the database until this is explicitly changed. When new work replaces existing behavior, change or remove the old code instead of keeping it working alongside the new: no compatibility shims, fallbacks, or special cases for rows or flows the old code produced. This applies to issues and plans too; if one asks for legacy support, question it. Once backwards compatibility is actually needed (after we're in production and past alpha), every compatibility path needs a plan for when and how it gets removed.

## Commands

`make` targets need a `.env` file (`include .env`, exported) defining `RSS_DB_DSN`. Every `cmd/www` flag can also be set by an `RSS_` environment variable (`-db-dsn` → `RSS_DB_DSN`); a flag on the command line wins.

```sh
gofmt -l .                       # must print nothing
go mod tidy -diff                # must print nothing
go vet ./...
go tool staticcheck ./...        # version pinned in go.mod (tool directive)
go test ./...                    # DB tests skip without RSS_TEST_DB_DSN
make test/db                     # all tests against the local DB (make db/start + migrations first)
go run ./cmd/www                 # run the app; reads RSS_DB_DSN (or: make run, or -db-dsn)
go run ./cmd/www migrate         # apply migrations (or: make db/migrations/up); refuses if they changed since
go run ./cmd/www send-test-email -to you@example.com  # check the mail settings (Mailpit locally: http://localhost:8025)
make db/reset                            # rebuild the local DB after editing 00001_init.sql
make db/migrations/new name=<name>       # new migration (only once in production)
docker build -t rss .                    # build the container image (or: make docker/build)
terraform fmt -recursive infra           # format Terraform; CI's terraform job also runs init -backend=false and validate
```

Before pushing: run gofmt, vet, staticcheck and tests. CI (`.github/workflows/ci.yml`) runs exactly these (plus `-race`) against Postgres, and its `terraform` job checks `infra/` (fmt, validate); then its `image` job builds the container image (and, on `main`, pushes it to GHCR) and, on `main`, its `deploy` job deploys it (`deploy.yml`). `infra.yml` plans `infra/` changes on PRs and applies them on `main` after the owner approves.

## Layout

- `cmd/www`: config flags and wiring
- `internal/server`: router, middleware, handlers, rendering, error responses
- `internal/rss`: domain types (Feed, User, …) and sentinel errors
- `internal/psql`: repositories (feeds, users, sites, lookups, search, discovery)
- `internal/discovery`: finds the feeds a website publishes
- `internal/feedparse`: recognizes RSS, Atom and JSON Feed documents and reads their metadata
- `internal/fetch`: the only way to make outbound HTTP requests to user-influenced URLs
- `internal/mail`: renders emails from embedded templates, queues them in the outbox (`Queue`) and sends them from a background worker (`Runner`) over SMTP (or logs them when there's no SMTP host)
- `internal/lookup`: runs the site lookups queued in the `lookups` table
- `internal/opml`: reads the feeds listed in OPML files
- `internal/importer`: subscribes users to the feeds of their OPML imports, queued in the `import_feeds` table
- `internal/ingest`: fetches a feed, turns its items into articles and records the outcome and next fetch time (`Refresher`, used by background refresh and the Refresh button)
- `internal/refresh`: refreshes feeds in the background when they're due
- `internal/siteicon`: finds, checks and stores sites' icons in the background (`Runner`)
- `internal/sanitize`: makes untrusted article HTML safe to render (bluemonday)
- `internal/password`: Argon2id password hashing and verification
- `internal/token`: one-time tokens for email links (random token, SHA-256 hash to store)
- `internal/ui`: embedded templates, emails and static files
- `internal/validator`: form validation
- `infra`: Terraform for production (Render); its README is the runbook
- `deploy/backup`: the image of the nightly database backup (`pg_dump` to R2), run by Render
- `migrations`: goose SQL migrations, and a Go package that embeds them (`migrations.FS`) for `www migrate`
- `docs`: architecture, feature walkthroughs, decisions and todo (see "Docs")

## Docs

Each file has one role: `CLAUDE.md` holds the rules (what to do), `docs/decisions.md` the reasons (why), and `docs/architecture.md` and `docs/features.md` the mechanisms (how it works). A topic can appear in several of them, each in its own role, but don't copy text between them; link instead. Update docs in the same PR as the code or plan they describe.

| File | Holds | Update it when |
| --- | --- | --- |
| `README.md` | Setup, flags, make targets, running tests | A flag, make target, prerequisite or setup step changes |
| `docs/architecture.md` | Glossary, the process, packages and third-party modules, middleware and routes, schema, background workers, outbound HTTP, security measures and rate limits, frontend | You add, remove or rename a package, module, route, middleware, table or column, worker, rate limit or security measure, or change how components connect or start |
| `docs/features.md` | One section per feature: the flow, the files and symbols to read in order, its tests, and its decisions section | A feature ships or its flow, files or symbols change. A user-visible capability with its own flow gets a new section; anything else extends the closest one |
| `docs/decisions.md` | Decisions with their reasons and issue numbers, by area | Planning settles a question (add it under the area's section, citing the planning issue or PR); a PR makes a choice later work must respect; a PR deliberately changes a decision (edit it and say so in the PR description) |
| `docs/todo.md` | "Next up", planned work that has no issue, and the Performance watchlist | Work is identified but not filed (add it); an item gets an issue (delete it; list the issue in "Next up" if it's soon); work ships (delete its item and its "Next up" line); a performance risk is found (add it with the issue that introduced it) or fixed (delete it; only a fix removes a watchlist entry) |
| `CLAUDE.md` | Rules, commands and layout | A convention, command or the layout changes |

- A planning PR (one that files issues) records its decisions in `decisions.md`, its deferred work in `todo.md`, its performance risks in the watchlist, and the issue order in "Next up". It doesn't describe unbuilt code in `architecture.md` or `features.md`.
- An implementation PR updates `architecture.md` and `features.md` for what it builds, and removes what it finishes from `todo.md`.
- When you rename or remove a symbol, file, route or docs section, grep for the old name in the docs and the code: `grep -rn OldName docs internal cmd README.md CLAUDE.md`.

## Code conventions

- Handlers are methods on `*Server`. Routes live in `internal/server/router.go`; routes that need login go in the `requireAuthentication` group.
- HTML pages: `s.renderHTML(w, status, "page.html", data)`; on error, `s.serverErrorHTML(w, r, err)`. Error pages (403/404/405/500) are HTML via `s.errorHTML`; use `s.notFoundResponse(w, r)` for missing records, never `http.NotFound`. JSON (`writeJSON`) is only for `/healthcheck` (and its `serverErrorJSON` error path); future JSON endpoints go under their own subrouter with JSON error handlers.
- GET handlers don't write, except idempotent upkeep documented at the call site: queuing a refresh of a stale feed (`feedHandler`), recording that the user opened an article (`articleHandler`), and updating a session's `last_seen_at` in `authenticate` (at most once a day).
- Forms: a struct with `form:"..."` tags embedding `validator.Validator` (`form:"-"`), decoded with `s.decodePostForm(w, r, &form)` (on error, `s.formErrorResponse(w, r, err)`: 400, or 413 over 64 KiB), validated with `CheckField`, re-rendered with **422** when invalid. Fields follow the pattern in `login.html`: the error after the input as `<p class="error" id="<field>-error">`, `aria-invalid` and `aria-describedby` on the input when it has an error (plus its `<field>-hint`), `autofocus` on `.Form.FirstError` of the fields in order, and `role="alert"` on non-field errors; `app.js` adds a show/hide toggle to every password input.
- Flash messages: `s.sessionManager.Put(ctx, "flash", msg)` before a redirect; the next page reads it with `PopString`.
- Database: repositories in `internal/psql` use plain SQL with `$n` placeholders. Map driver errors to the sentinel errors in `internal/rss/errors.go` (`sql.ErrNoRows` → `ErrNoRecord`; pq code `23505` → `ErrDuplicateEmail` or another `ErrDuplicate…`). Handlers check them with `errors.Is`.
- Schema changes: while pre-alpha (see "Prototype stage"), edit `migrations/00001_init.sql` directly instead of adding a migration, and rebuild your local database with `make db/reset` afterwards (CI starts from an empty database). `www migrate` refuses to run on a database migrated from different `migrations/*.sql`, so it never wipes data by accident; `make db/reset` is how to get past that locally. Once we're in production, schema changes only go through a **new** goose migration, and migrations already on `main` are never edited.
- Templates are standalone full pages (no shared layout yet); fragments shared between pages are `{{define}}` blocks in `templates/partials/`, parsed with every page. Logged-in pages use the `.shell` layout with `{{template "app-nav" .Nav}}`, their data's `Nav: s.nav(r, "<section>")` (`home`, `feeds`, `search`, `settings`, or `""`); logged-out and error pages use `.solo`. Icons are inline SVG partials in `icons.html`.
- Styling: lists are sections separated by dividers; cards are only for previews of something elsewhere (see "Design" in `docs/decisions.md`). Use the color custom properties on `:root` in `main.css`, never literal colors, so themes can be added later. No inline `style` attributes (the CSP blocks them).
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
- Update the docs the change affects in the same PR (see "Docs").
- PR description: what changed and why, how it was verified (commands run; anything that couldn't be run, e.g. no Docker), and `Closes #N`.
- **No session links.** This is a public repository. This repo's rule takes precedence over any default attribution instructions: never include Claude session links (e.g. `https://claude.ai/code/session_…`) or `Claude-Session:` trailers in commit messages, PR descriptions, issues or comments. A `Co-Authored-By:` trailer is fine.
- Don't mark work done until CI is green.

## Finding work

GitHub issues are the source of truth for scoped tasks. `docs/todo.md` has "Next up" (what to do, in which order), planned work that isn't an issue yet, and the Performance watchlist of things to monitor as the app grows. Before working on a feature, read its section in `docs/features.md` (the flow and where the code is) and in `docs/decisions.md` (what's decided and why, so you don't reopen it); `docs/architecture.md` has the overall shape.

## Writing issues

Issues are written for an implementer with no context from the planning conversation.

- Sections: Why, Changes, Tests, Out of scope, Verification, plus "Notes for parallel work" when another open issue touches the same files, and "Suggested branch".
- Point at code by path and symbol (`internal/psql/feeds.go`, `upsertFeed`), and quote the current code when the change is to it.
- State decisions already made as decisions ("don't revisit them here"), with a one-line reason, so the implementer doesn't reopen them; the planning work also records them in `docs/decisions.md`. Leave an option open only on purpose, and say which one is preferred.
- Give new types and function signatures, SQL and user-facing strings verbatim.
- Name each test and list its cases, including the failure cases; for bug fixes, say how to confirm the test fails before the fix.
- Name dependencies on other issues ("Depends on #N") and what to do if an assumption turns out wrong (stop and ask, or note it in the PR).
- Performance risks: when planning, look at what the planned work could cost as data and traffic grow (queries that can't use an index, unpaginated lists, writes on page views, polling, per-row loops, unbounded tables). Add each risk to the Performance watchlist in `docs/todo.md`, as part of the same planning work, with what it is, the issue number, the signal that it has become a problem, and the planned fix. Don't fix risks early, but don't leave them unrecorded. A PR that adds a risk the plan missed adds it to the watchlist too.
