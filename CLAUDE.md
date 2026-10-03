# CLAUDE.md

## Project

A server-rendered RSS reader in Go: chi, embedded `html/template`, Postgres via `lib/pq`, scs sessions stored in Postgres, goose migrations. Early-stage: accounts and adding feed URLs work; fetching feeds and subscriptions are not built yet.

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
make db/migrations/new name=<name>       # new migration
```

Before pushing: run gofmt, vet, staticcheck and tests. CI (`.github/workflows/ci.yml`) runs exactly these (plus `-race`) against Postgres.

## Layout

- `cmd/www`: config flags and wiring
- `internal/server`: router, middleware, handlers, rendering, error responses
- `internal/psql`: repositories and sentinel errors
- `internal/password`: Argon2id password hashing and verification
- `internal/ui`: embedded templates and static files
- `internal/validator`: form validation
- `migrations`: goose SQL migrations
- `docs/todo.md`: planned work that has no issue yet

## Code conventions

- Handlers are methods on `*Server`. Routes live in `internal/server/router.go`; routes that need login go in the `requireAuthentication` group.
- HTML pages: `s.renderHTML(w, status, "page.html", data)`; on error, `s.serverErrorHTML(w, r, err)`. Error pages (403/404/405/500) are HTML via `s.errorHTML`; use `s.notFoundResponse(w, r)` for missing records, never `http.NotFound`. JSON (`writeJSON`) is only for `/healthcheck` (and its `serverErrorJSON` error path); future JSON endpoints go under their own subrouter with JSON error handlers.
- Forms: a struct with `form:"..."` tags embedding `validator.Validator` (`form:"-"`), decoded with `s.decodePostForm`, validated with `CheckField`, re-rendered with **422** when invalid.
- Flash messages: `s.sessionManager.Put(ctx, "flash", msg)` before a redirect; the next page reads it with `PopString`.
- Database: repositories in `internal/psql` use plain SQL with `$n` placeholders. Map driver errors to sentinel errors in `internal/psql/errors.go` (`sql.ErrNoRows` → `ErrNoRecord`; pq code `23505` → `ErrDuplicateEmail` or another `ErrDuplicate…`). Handlers check them with `errors.Is`.
- Schema changes only through a **new** goose migration; never edit a migration that is already on `main`.
- Templates are standalone full pages (no shared layout yet). A nav change must be applied to every template.
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
- Standard library only for tests (no testify).

## Git and PR workflow

- Never push to `main` (it's protected). One issue, one branch, one PR. Branch names: `fix/<slug>`, `feat/<slug>`, `docs/<slug>`, `test/<slug>`, `ci/<slug>`. If your session assigns a branch name, use that.
- Branch from the latest `main`. Before opening a PR, merge the latest `main` into your branch and re-run the checks.
- Keep PRs scoped to their issue. Note unrelated problems in the PR description (or add them to `docs/todo.md`); don't fix them in the same PR.
- PR description: what changed and why, how it was verified (commands run; anything that couldn't be run, e.g. no Docker), and `Closes #N`.
- **No session links.** This is a public repository. This repo's rule takes precedence over any default attribution instructions: never include Claude session links (e.g. `https://claude.ai/code/session_…`) or `Claude-Session:` trailers in commit messages, PR descriptions, issues or comments. A `Co-Authored-By:` trailer is fine.
- Don't mark work done until CI is green.

## Finding work

GitHub issues are the source of truth for scoped tasks; `docs/todo.md` holds planned work that isn't an issue yet.
