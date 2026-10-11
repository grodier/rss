# rss

A server-rendered RSS reader written in Go, using [chi](https://github.com/go-chi/chi), `html/template` (templates and static files are embedded in the binary), Postgres, and [scs](https://github.com/alexedwards/scs) sessions.

> **Work in progress** (pre-alpha). You can find websites and their feeds, subscribe, and read a timeline of their articles. See [Documentation](#documentation) for how it works and what's planned.

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
| `-signup` | `false` | Allow new accounts to sign up |
| `-db-dsn` | _(none, required)_ | PostgreSQL DSN |
| `-db-max-open-conns` | `25` | PostgreSQL max open connections |
| `-db-max-idle-conns` | `25` | PostgreSQL max idle connections |
| `-db-max-idle-time` | `15m` | PostgreSQL max idle time |
| `-lookup-workers` | `2` | Background lookup workers (0–16; 0 disables them) |
| `-refresh-workers` | `2` | Background feed refresh workers (0–16; 0 disables them) |

Signup is off by default. To create an account, run once with `-signup`, sign up, then restart without it.

## Make targets

Run `make help` for the documented targets. Others: `make run`, `make build` (outputs `bin/www`), `make clean`.

## Tests

```sh
make test      # go test ./... ; database tests are skipped
make test/db   # runs all tests, including database tests, against the local database
```

Database tests run when `RSS_TEST_DB_DSN` is set to a migrated database (`make test/db` uses `RSS_DB_DSN`). CI sets it and fails if it is missing.

## Documentation

- [`docs/architecture.md`](docs/architecture.md): how the app fits together: the process, packages, routes, data model, background workers and security measures. Start here.
- [`docs/features.md`](docs/features.md): how each feature works and which code to read, in order.
- [`docs/decisions.md`](docs/decisions.md): decisions made while planning, and why.
- [`docs/todo.md`](docs/todo.md): what's next, planned work that has no issue yet, and the performance watchlist.
- [`CLAUDE.md`](CLAUDE.md): code, testing and PR conventions (for people and coding agents), and which docs to update when.
