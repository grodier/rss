# CLAUDE.md

## Project

Paper Round (paperround.app), a server-rendered RSS reader in Go: chi, embedded `html/template`, Postgres via `lib/pq`, scs sessions stored in Postgres, goose migrations.
Pre-alpha (see "Prototype stage"); `docs/features.md` describes what works.

Reading is a timeline, not an inbox: read state is recorded but shown only as a quiet marker on read articles.
Don't add unread counts, badges, "N new" indicators or "mark all read"; see "Reading" in `docs/decisions.md` for the decisions behind this.

## Prototype stage

The app is pre-alpha, but its production data is kept: schema changes are new migrations, never a reset (see "Pre-alpha testing" in `docs/decisions.md`).
In every other way it's still pre-alpha.
When new work replaces existing behavior, change or remove the old code instead of keeping it working alongside the new: no compatibility shims, fallbacks, or special cases for rows or flows the old code produced.
Data is kept, old code isn't: a schema change migrates the existing rows in its own migration, so code never handles rows the old code produced.
A migration must work with the version still running during a deploy, which overlaps the new one for a few seconds: add a column or table before the code uses it, and drop it only after the code has stopped using it.
This applies to issues and plans too; if one asks for legacy support, question it.
Once backwards compatibility is actually needed (after we're in production and past alpha), every compatibility path needs a plan for when and how it gets removed.

## Commands

`make` targets need a `.env` file (`include .env`, exported) defining `RSS_DB_DSN`.
Package `web` embeds the built frontend, so Go doesn't compile without it: on a fresh clone, run `make assets` before `go build`, `go vet`, `go test` or gopls work (the `make` targets that compile `cmd/www` run it first).
Every `cmd/www` and `cmd/admin` flag can also be set by an `RSS_` environment variable (`-db-dsn` → `RSS_DB_DSN`); a flag on the command line wins.

```sh
make assets                      # build the frontend into web/dist, the reader's and the admin's (npm ci, then npm run build; instant when nothing changed)
make assets/watch                # rebuild the frontend on every change (restart go run to pick it up)
npm --prefix web run typecheck   # tsc --noEmit over the TypeScript modules
npm --prefix web run format:check  # must pass (npm --prefix web run format fixes it); Prettier, for web/
gofmt -l $(git ls-files '*.go')  # must print nothing (gofmt -l . would walk web/node_modules)
go mod tidy -diff                # must print nothing
go vet ./...
go tool staticcheck ./...        # version pinned in go.mod (tool directive)
go test ./...                    # DB tests skip without RSS_TEST_DB_DSN
make test/web                    # the frontend tests (Vitest; npm --prefix web test); make test runs them too
make test/db                     # all tests against the local DB (make db/start + migrations first)
go run ./cmd/www                 # run the app; reads RSS_DB_DSN (or: make run, or -db-dsn)
go run ./cmd/admin               # run the admin site on :4001 (or: make run/admin)
go run ./cmd/admin enroll -email you@example.com  # enroll an admin (password on stdin; needs RSS_SECRET_KEY, see README)
go run ./cmd/www migrate         # apply migrations (or: make db/migrations/up)
go run ./cmd/www seed            # queue lookups of the seed list's sites (or: make db/seed-sites); production runs it on every deploy
go run ./cmd/admin send-test-email -to you@example.com  # check the mail settings (Mailpit locally: http://localhost:8025)
make db/reset                            # rebuild the local DB from scratch
make db/loadseed                         # seed the local DB at realistic scale and measure the reading queries (make db/loadseed/measure: measure only)
make db/migrations/new name=<name>       # new migration
docker build -t rss .                    # build the container image (or: make docker/build)
docker build --target admin -t rss-admin .  # build the admin's image (or: make docker/build/admin)
terraform fmt -recursive infra           # format Terraform; CI's terraform job also runs init -backend=false and validate
```

Before pushing: run gofmt, vet, staticcheck and tests, and, after changing anything under `web/`, the typecheck, the format check and the frontend tests.
CI (`.github/workflows/ci.yml`) builds the assets, then runs exactly these (plus `-race`) against Postgres, its `web` job runs the typecheck, the format check, the frontend tests and the build, and its `terraform` job checks `infra/` (fmt, validate); then its `image` job builds the container image (and, on `main`, pushes it to GHCR) and, on `main`, its `deploy` job deploys it (`deploy.yml`).
`infra.yml` plans `infra/` changes on PRs and applies them on `main` after the owner approves; `infra-unlock.yml` releases a stale state lock (see "A stale state lock" in `infra/README.md`).

## Layout

- `cmd/www`: config flags and wiring
- `cmd/admin`: the admin site's binary (its own service): config flags, wiring and its subcommands
- `internal/admin`: the admin site's server (router, middleware, handlers, rendering, its login and enrolling), shared by its pages and `cmd/admin`'s subcommands
- `internal/admin/chart`: the admin's charts (line charts, histograms), drawn as SVG on the server, and percentiles from histogram buckets
- `internal/cli`: what the commands share (`SetFlagsFromEnv`, `NewLogger`)
- `internal/server`: router, middleware, handlers, rendering, error responses
- `internal/rss`: domain types (Feed, User, …) and sentinel errors
- `internal/psql`: repositories (feeds, users, sites, lookups, search, discovery)
- `internal/discovery`: finds the feeds a website publishes
- `internal/feedparse`: recognizes RSS, Atom and JSON Feed documents and reads their metadata
- `internal/fetch`: the only way to make outbound HTTP requests to user-influenced URLs
- `internal/clientip`: the client's address behind `-proxy-hops` reverse proxies, and its rate-limit key, for both servers
- `internal/ratelimit`: in-memory fixed-window rate limiters, for both servers
- `internal/assets`: reads a frontend build's Vite manifest, for both servers: page groups' tags, files' URLs, and serving `/static`
- `internal/mail`: renders emails from embedded templates, queues them in the outbox (`Queue`) and sends them from a background worker (`Runner`) over SMTP (or logs them when there's no SMTP host)
- `internal/metrics`: counts `www`'s requests and background jobs and samples its runtime, writes them once a minute, keeps its recent server errors, and rolls up and deletes old metrics and real-user views in the background
- `internal/lookup`: runs the site lookups queued in the `lookups` table
- `internal/seed`: the seed list (`sites.txt`, embedded) of sites to look up before anyone searches for them; `www seed` queues their lookups
- `internal/opml`: reads the feeds listed in OPML files
- `internal/importer`: subscribes users to the feeds of their OPML imports, queued in the `import_feeds` table
- `internal/ingest`: fetches a feed, turns its items into articles and records the outcome and next fetch time (`Refresher`, used by background refresh and the Refresh button)
- `internal/refresh`: refreshes feeds in the background when they're due
- `internal/siteicon`: finds, checks and stores sites' icons in the background (`Runner`)
- `internal/enrich`: fetches the web pages of articles missing an image, title or excerpt and stores their Open Graph metadata in the background (`Runner`)
- `internal/sanitize`: makes untrusted article HTML safe to render (bluemonday)
- `internal/password`: Argon2id password hashing and verification
- `internal/token`: one-time tokens for email links (random token, SHA-256 hash to store)
- `internal/ui`: embedded templates (the reader's in `templates/`, the admin's in `admin/`), emails and the web app manifest
- `internal/validator`: form validation
- `web`: the frontend's npm project, built by Vite into `web/dist` (gitignored), and the Go package that embeds the builds (`web.App`, `web.Admin`); `app/` is the reader's code (`entries/`, one per page group, each setting up the enhancements in `features/` its pages use; `styles/`, `base.css` and one sheet per area, each importing the partials in `styles/partials/` its pages use; and `public/` for files that need a fixed URL), `admin/` the admin site's, built apart (`vite.admin.config.ts`, into `dist/admin`) with the same layout, `shared/` what another site could reuse (`lib/`'s generic enhancements, `styles/`'s layers, reset, fonts, tokens and base styles, the fonts, and `types/`, the declarations of browser APIs TypeScript lacks) and `test/`, the frontend tests' helpers
- `tools/loadseed`: seeds the local database at realistic scale and measures the reading queries (dev only)
- `infra`: Terraform for production (Render); its README is the runbook
- `deploy/backup`: the image of the nightly database backup (`pg_dump` to R2), run by Render
- `migrations`: goose SQL migrations, and a Go package that embeds them (`migrations.FS`) for `www migrate`
- `docs`: architecture, feature walkthroughs, decisions and backlog (see "Docs")

## Docs

Each file has one role: `CLAUDE.md` holds the rules (what to do), `docs/decisions.md` the reasons (why), and `docs/architecture.md` and `docs/features.md` the mechanisms (how it works).
A topic can appear in several of them, each in its own role, but don't copy text between them; link instead.
Update docs in the same PR as the code or plan they describe.

| File | Holds | Update it when |
| --- | --- | --- |
| `README.md` | Setup, flags, make targets, running tests | A flag, make target, prerequisite or setup step changes |
| `docs/architecture.md` | Glossary, the process, packages and third-party modules, middleware and routes, how tables are used together, background workers, outbound HTTP, security measures and rate limits, frontend, deployment overview | You add, remove or rename a package, module, route, middleware, table, worker, rate limit or security measure, or change how components connect or start |
| `docs/features.md` | One section per feature: the flow, a few entry points to start reading at, its test files, and its decisions section | A feature ships or its flow or entry points change. A user-visible capability with its own flow gets a new section; anything else extends the closest one |
| `docs/error-states.md` | Every user-visible error and empty state, with how to trigger it and its last review | A PR adds, removes or changes a user-visible failure or empty state |
| `docs/decisions.md` | Decisions with their reasons and issue numbers, by area | Planning settles a question (add it under the area's section, citing the planning issue or PR); a PR makes a choice later work must respect; a PR deliberately changes a decision (edit it and say so in the PR description) |
| `docs/backlog.md` | Work that has no issue yet, and the Performance watchlist | Work is identified but not filed (add it); an item gets an issue (delete it); a performance risk is found (add it with the issue that introduced it) or fixed (delete it; only a fix removes a watchlist entry) |
| `CLAUDE.md` | Rules, commands and layout | A convention, command or the layout changes |

The order of planned work isn't in a file: it's the [Roadmap issue](https://github.com/grodier/rss/issues/385), whose sub-issues are the planned issues, and, for the admin and its observability, the [Admin Roadmap issue](https://github.com/grodier/rss/issues/560) (see "Finding work").

- Docs say what the code can't: reasons, flows that cross packages, and invariants that aren't obvious.
  Don't restate what lives in the code, templates, tests, the schema's comments or Terraform: user-facing strings, CSS classes, every helper or test name, column lists, configuration values.
  Name a few entry points and let readers follow the calls.
- One sentence per line, with no hard wrapping, so a change touches only its own lines.
  Add new list items, table rows and watchlist entries next to related ones, not at the end.
- A planning PR (one that files issues) records its decisions in `decisions.md`, its deferred work in `backlog.md` and its performance risks in the watchlist, and deletes from `backlog.md` the items that now have issues.
  The issues themselves go on the Roadmap as sub-issues, in order; the PR doesn't list them in a file.
  It doesn't describe unbuilt code in `architecture.md` or `features.md`.
  Choices about how the app looks or reads (layout, style, copy) are the exception: the issue holds them as the current thinking, and the implementation PR records them in `decisions.md` once the owner has approved the result (see "Design" in `docs/decisions.md`).
- An implementation PR updates `architecture.md` and `features.md` for what it builds.
  It doesn't edit the Roadmap: `Closes #N` closes its sub-issue.
- When you rename or remove a symbol, file, route or docs section, grep for the old name in the docs and the code: `grep -rn OldName docs internal cmd README.md CLAUDE.md`.

## Code conventions

- Handlers are methods on `*Server`.
  Routes live in `internal/server/router.go`; routes that need login go in the `requireAuthentication` group.
- HTML pages: `s.renderHTML(w, status, "page.html", data)`; on error, `s.serverErrorHTML(w, r, err)`.
  Error pages (403/404/405/500) are HTML via `s.errorHTML`; use `s.notFoundResponse(w, r)` for missing records, never `http.NotFound`.
  JSON (`writeJSON`) is only for `/healthcheck` (and its `serverErrorJSON` error path); future JSON endpoints go under their own subrouter with JSON error handlers.
- GET handlers don't write, except idempotent upkeep documented at the call site: queuing a refresh of a stale feed (`feedHandler`), recording that the user opened an article (`articleHandler`), and updating a session's `last_seen_at` in `authenticate` (at most once a day).
- Forms: a struct with `form:"..."` tags embedding `validator.Validator` (`form:"-"`), decoded with `s.decodePostForm(w, r, &form)` (on error, `s.formErrorResponse(w, r, err)`: 400, or 413 over 64 KiB), validated with `CheckField`, re-rendered with **422** when invalid.
  Fields follow the pattern in `login.html`: the error after the input as `<p class="error" id="<field>-error">`, `aria-invalid` and `aria-describedby` on the input when it has an error (plus its `<field>-hint`), `autofocus` on `.Form.FirstError` of the fields in order, and `role="alert"` on non-field errors; `password-toggle.ts` adds a show/hide toggle to every password input.
- Flash messages: `s.sessionManager.Put(ctx, "flash", msg)` before a redirect; the next page reads it with `PopString`.
- Database: repositories in `internal/psql` use plain SQL with `$n` placeholders.
  Map driver errors to the sentinel errors in `internal/rss/errors.go` (`sql.ErrNoRows` → `ErrNoRecord`; pq code `23505` → `ErrDuplicateEmail` or another `ErrDuplicate…`).
  Handlers check them with `errors.Is`.
- Schema changes: every change is a **new** goose migration (`make db/migrations/new name=<name>`), applied locally with `go run ./cmd/www migrate`.
  Migrations already on `main` are never edited, renamed or deleted (CI fails a PR that does); fix a mistake with another migration.
  Each must work with the version still running during a deploy (see "Prototype stage"), and migrates the existing rows it affects.
  If two branches add the same migration number, the one that merges `main` second renumbers its file (goose refuses duplicate versions).
  `make db/reset` only rebuilds a local database from scratch.
- Templates are standalone full pages (no shared layout yet); fragments shared between pages are `{{define}}` blocks in `templates/partials/`, parsed with every page.
  Logged-in pages use the `.shell` layout with `{{template "app-nav" .Nav}}`, their data's `Nav: s.nav(r, "<section>")` (`home`, `saved`, `feeds`, `search`, `settings`, or `""`); logged-out and error pages use `.solo`.
  Icons are inline SVG partials in `icons.html`.
- The admin (`internal/admin`, `internal/ui/admin`, `web/admin`) follows these conventions with its own files: routes in `internal/admin/router.go`, pages in its layout with `{{template "admin-nav" .Nav}}` and `Nav: s.nav(r, "<section>")`, partials in `internal/ui/admin/partials`, and its stylesheet partials' order in `web/admin/styles/base.css`.
  Reuse a reader pattern where it fits, but not the reader's own code or styles: shared code goes in a package both use (`internal/assets`, `internal/clientip`), shared styles in `web/shared`.
- Styling: lists are sections separated by dividers; cards are only for previews of something elsewhere (see "Design" in `docs/decisions.md`).
  Use the color custom properties on `:root` in `web/shared/styles/tokens.css`, never literal colors, so themes can be added later.
  Rules go in the partial for their area (`web/app/styles/partials`, or `web/shared/styles` for what isn't about the reader), inside their cascade layer's block.
  The partials have one order, listed in `web/app/styles/base.css`: a sheet imports the partials its pages use in that order, with `press.css` last, and a new partial goes in the list and in the sheets of every page it styles.
  Where a property has a standard name, write only that and let the build add the vendor prefixes (see "Frontend assets" in `docs/decisions.md`).
  Motion uses the motion tokens in `tokens.css` and is written inside `@media (prefers-reduced-motion: no-preference)`; see "Motion and feedback" in `docs/decisions.md`.
  No inline `style` attributes (the CSP blocks them).
- Only the commands (`cmd/www`, `cmd/admin`) handle OS signals.
  Long-running components (the HTTP server, background workers) take a `context.Context` and shut down gracefully when it is canceled; they never call `signal.Notify`.
- JavaScript is progressive enhancement: every page must work without it.
  TypeScript (`strict`), one module per enhancement in `web/app/features` (those another site could reuse in `web/shared/lib`), each exporting a setup function that the entries of the page groups whose pages use it import and call in order (and `entries.test.ts` lists); nothing runs on import.
  Each page names its page group once in its head, `{{template "assets" "<group>"}}`, which links the group's stylesheets and scripts from Vite's manifest (`assetTags`): its sheet, `web/app/styles/<group>.css`, its entry, `web/app/entries/<group>.ts`, or both, and an entry can import another group's sheet.
  A new page names an existing group, or a new one, whose entry sets up `rum.ts` first, as every page sends the real-user beacon.
  No inline scripts or handlers (the CSP blocks them), and no runtime libraries unless a PR justifies one: the only one is `web-vitals` (see "Observability" in `docs/decisions.md`), pinned to an exact version under `dependencies`.
  An API TypeScript's DOM types lack gets a minimal declaration in `web/shared/types/dom.d.ts`, never `any`.
  A new npm devDependency gets a one-line justification in its PR, like a Go dependency.
  Enhancements hook onto server-rendered markup (`data-*` attributes, `rel` links).
- Article HTML (summary, content) is only ever rendered through `sanitize.HTML`; never convert it to `template.HTML` yourself.
- Debug panels and inspectors live in `internal/server/debug.go`, `internal/psql/debug.go` and `partials/debug.html`.
  They're read-only, and render untrusted text only as escaped text.
- Log with `s.logger` (slog); log request errors with `s.logError`.
- Prefer the standard library.
  Don't add a dependency without a one-line justification in the PR.

## Testing conventions

- Server tests: `newTestServer(t)` (no DB).
  Hit routes with `s.router()` + `httptest`, or call a handler directly.
  For chi URL params:
  ```go
  rctx := chi.NewRouteContext()
  rctx.URLParams.Add("id", "abc")
  req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
  ```
- `TestRoutesRequireAuthentication` (`router_test.go`) checks every route that isn't on its public list redirects anonymous requests to `/login`; add a new public route to that list rather than writing a per-route login test.
- `Services` holds the `FeedStore` / `UserStore` interfaces.
  For handler tests that need store results, build a server with `newTestServerWith(t, Services{...})` and the function-field fakes in `internal/server/fakes_test.go`.
  Keep DB tests for repository code in `internal/psql`.
- DB tests: `psqltest.NewDB(t)` (`internal/psql/psqltest`).
  They skip locally without `RSS_TEST_DB_DSN` and fail in CI without it.
  Create uniquely-named rows, delete them in `t.Cleanup`, never truncate.
- Bug fixes come with a test that fails before the fix, where feasible.
- Test behavior the app has, not behavior it no longer has.
  When code is removed, delete its tests; don't add tests asserting that a removed route, handler or function is gone (e.g. "`GET /old` returns 404").
  They only re-test the router's or compiler's defaults and become dead weight.
  The same goes for issues and plans that ask for such tests; question them.
- Standard library only for Go tests (no testify).
- Frontend tests use Vitest with happy-dom (`npm --prefix web test`), in a `*.test.ts` next to their module (`share.ts`, `share.test.ts`).
  Test through the markup hooks the server renders (`form[data-toggle]`, `button[data-share-url]`…), import the module in each test after `isolate()` (`web/shared/test/setup.ts`), and test pure functions directly.
  Where a module mirrors Go, Go stays the spec: a Go test writes a fixture (`-update`) that both sides check, like `TestTimelineSummaryFixture`.
  A changed module comes with tests.

## Git and PR workflow

- Never push to `main` (it's protected).
  One issue, one branch, one PR.
  Branch names: `fix/<slug>`, `feat/<slug>`, `docs/<slug>`, `test/<slug>`, `ci/<slug>`.
  If your session assigns a branch name, use that.
- Branch from the latest `main`.
  Before opening a PR, merge the latest `main` into your branch and re-run the checks.
- Keep PRs scoped to their issue.
  Note unrelated problems in the PR description (or add them to `docs/backlog.md`); don't fix them in the same PR.
- Keep PRs small enough to review in one sitting (see "Planning and docs" in `docs/decisions.md`).
  If, before or while building an issue, it looks like it will make a large PR, stop and suggest splitting the issue into smaller issues and PRs, saying where the seams are, before building further.
  Typical seams: a refactor the work needs first, a self-contained helper, a second flow, or an optional extra the issue allows leaving out.
  Suggest a split only if it makes sense: each part merges on its own, with its tests and docs, and leaves `main` working; don't cut one change into pieces that can't be reviewed apart.
- Update the docs the change affects in the same PR (see "Docs").
- A PR that changes how the app looks shows its options first, in a draft PR with screenshots at desktop and phone widths, and stops until the owner picks one; skip this only when its issue says the change needs no design checkpoint (see "Design" in `docs/decisions.md`).
  Every checkpoint shows several options that differ in approach, not variations of one idea, each with a justification, and recommends one.
- PR description: what changed and why, how it was verified (commands run; anything that couldn't be run, e.g. no Docker), and `Closes #N`.
- **No session links.** This is a public repository.
  This repo's rule takes precedence over any default attribution instructions: never include Claude session links (e.g. `https://claude.ai/code/session_…`) or `Claude-Session:` trailers in commit messages, PR descriptions, issues or comments.
  A `Co-Authored-By:` trailer is fine.
- Don't mark work done until CI is green.

## Finding work

GitHub issues are the source of truth for scoped tasks.
The [Roadmap issue (#385)](https://github.com/grodier/rss/issues/385) says what to do next and in which order: its sub-issues, grouped into waves that can be built in parallel.
The [Admin Roadmap issue (#560)](https://github.com/grodier/rss/issues/560) does the same for the admin site (`cmd/admin`) and its observability, with the same rules.
`docs/backlog.md` has work that isn't an issue yet, and the Performance watchlist of things to monitor as the app grows.
Before working on a feature, read its section in `docs/features.md` (the flow and where the code is) and in `docs/decisions.md` (what's decided and why, so you don't reopen it); `docs/architecture.md` has the overall shape.

## Writing issues

Issues are written for an implementer with no context from the planning conversation.

- Size each issue for one PR that can be reviewed in one sitting; plan a bigger change as several issues, in order, each of which stands on its own.
- Sections: Why, Changes, Tests, Out of scope, Verification, plus "Notes for parallel work" when another open issue touches the same files, and "Suggested branch".
  An issue that changes how the app looks adds "Design checkpoint": several starting options that differ in approach, the preferred one, and the pages and states the screenshots must show; or it says why the change needs no checkpoint (copy only, or an existing pattern reused exactly).
- Point at code by path and symbol (`internal/psql/feeds.go`, `upsertFeed`), and quote the current code when the change is to it.
- State decisions already made as decisions ("don't revisit them here"), with a one-line reason, so the implementer doesn't reopen them; the planning work also records them in `docs/decisions.md`.
  Look-and-feel choices not yet approved go under "Current thinking" instead, with the instruction to record the approved ones in `docs/decisions.md` in the implementation PR.
  Leave an option open only on purpose, and say which one is preferred.
- Give new types and function signatures, SQL and user-facing strings verbatim.
- Name each test and list its cases, including the failure cases; for bug fixes, say how to confirm the test fails before the fix.
- Name dependencies on other issues ("Depends on #N") and what to do if an assumption turns out wrong (stop and ask, or note it in the PR).
- File each issue as a sub-issue of the Roadmap (#385), or of the Admin Roadmap (#560) for the admin and its observability, and update its waves.
- Performance risks: when planning, look at what the planned work could cost as data and traffic grow (queries that can't use an index, unpaginated lists, writes on page views, polling, per-row loops, unbounded tables).
  Add each risk to the Performance watchlist in `docs/backlog.md`, as part of the same planning work, with what it is, the issue number, the signal that it has become a problem, and the planned fix.
  Don't fix risks early, but don't leave them unrecorded.
  A PR that adds a risk the plan missed adds it to the watchlist too.
