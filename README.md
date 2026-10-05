# rss

A server-rendered RSS reader written in Go, using [chi](https://github.com/go-chi/chi), `html/template` (templates and static files are embedded in the binary), Postgres, and [scs](https://github.com/alexedwards/scs) sessions.

> **Work in progress.** Accounts (sign up, log in, log out), searching for sites and discovering their feeds (site lookups) work. Fetching articles and subscriptions are not built yet (see `docs/todo.md`).

## Prerequisites

- Go (version in `go.mod`)
- Docker and Docker Compose
- [goose](https://github.com/pressly/goose): `go install github.com/pressly/goose/v3/cmd/goose@latest`
- Optional: `psql` (for `make db/psql`)

## Configuration

The `Makefile` includes a gitignored `.env` file. It is not enforced by `make`, but `make run` and the database targets need `RSS_DB_DSN` from it. Copy the example, whose value matches `compose.yml`:

```sh
cp .env.example .env
```

```
RSS_DB_DSN=postgres://rssapp:dev-password@localhost:5432/rssapp_dev?sslmode=disable
```

## Quick start

```sh
cp .env.example .env
make db/start            # start Postgres in Docker
make db/migrations/up    # apply migrations (asks for confirmation)
make run                 # http://localhost:8080
```

Instead of `make db/migrations/up` you can run `goose -dir ./migrations postgres "$RSS_DB_DSN" up`.

## Command-line flags

`cmd/www` accepts these flags (`make run` passes `-db-dsn` from `RSS_DB_DSN`):

| Flag | Default | Description |
| --- | --- | --- |
| `-env` | `development` | Environment (`development` or `production`) |
| `-port` | `8080` | Server port |
| `-db-dsn` | _(none, required)_ | PostgreSQL DSN |
| `-db-max-open-conns` | `25` | PostgreSQL max open connections |
| `-db-max-idle-conns` | `25` | PostgreSQL max idle connections |
| `-db-max-idle-time` | `15m` | PostgreSQL max idle time |
| `-lookup-workers` | `2` | Background lookup workers (0–16; 0 disables them) |

## Make targets

Run `make help` for the documented targets. Others: `make run`, `make build` (outputs `bin/www`), `make clean`.

## Tests

```sh
make test      # go test ./... ; database tests are skipped
make test/db   # runs all tests, including database tests, against the local database
```

Database tests run when `RSS_TEST_DB_DSN` is set to a migrated database (`make test/db` uses `RSS_DB_DSN`). CI sets it and fails if it is missing.

## Project layout

- `cmd/www`: application entry point, flag parsing and config
- `internal/server`: HTTP server, router, middleware and handlers
- `internal/rss`: domain types and sentinel errors
- `internal/psql`: Postgres repositories (feeds, users, sites, lookups, search and discovery)
- `internal/discovery`: finds the feeds a website publishes
- `internal/feedparse`: recognizes RSS, Atom and JSON Feed documents and reads their metadata
- `internal/fetch`: the only way the app makes outbound HTTP requests to user-influenced URLs (blocks private addresses, limits redirects, size and time)
- `internal/lookup`: runs the site lookups queued in the `lookups` table
- `internal/password`: Argon2id password hashing and verification
- `internal/ui`: embedded HTML templates and static files
- `internal/validator`: form validation helpers
- `migrations`: goose SQL migrations
- `docs/todo.md`: planned work that has no issue yet

## Routes

| Route | Auth required | Description |
| --- | --- | --- |
| `GET /healthcheck` | No | Health check (JSON) |
| `GET /static/*` | No | Embedded static files |
| `GET /` | No | Home page |
| `GET, POST /signup` | No | Sign up form |
| `GET, POST /login` | No | Log in form |
| `POST /logout` | No | Log out |
| `POST /subscribe` | Yes | Placeholder; subscriptions are not built yet |
| `GET /feeds/{id}` | Yes | A single feed |
| `GET /feeds` | Yes | Latest feeds |
| `GET /sites/{id}` | Yes | A site and its discovered feeds |
| `GET /search` | Yes | Search the database for sites |
| `POST /lookups` | Yes | Queue a lookup of a site; redirects to the site if found in time, else to the lookup |
| `GET /lookups/{id}` | Yes | Status of a site lookup; redirects to the site when found |
