# rss

A server-rendered RSS reader written in Go, using [chi](https://github.com/go-chi/chi), `html/template` (templates and static files are embedded in the binary), Postgres, and [scs](https://github.com/alexedwards/scs) sessions.

> **Work in progress** (pre-alpha). You can find websites and their feeds, subscribe, and read a timeline of their articles. See [Documentation](#documentation) for how it works and what's planned.

## Prerequisites

- Go (version in `go.mod`)
- Docker and Docker Compose
- [goose](https://github.com/pressly/goose), only for `make db/migrations/new` and `make db/migrations/down`: `go install github.com/pressly/goose/v3/cmd/goose@latest`. The app applies migrations itself (`www migrate`).
- Optional: `psql` (for `make db/psql`)

## Configuration

The `Makefile` includes and exports a gitignored `.env` file. It is not enforced by `make`, but `make run` and the database targets need `RSS_DB_DSN` from it. Copy the example, whose value matches `compose.yml`:

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

Instead of `make db/migrations/up` you can run `go run ./cmd/www migrate`.

## Command-line flags and environment variables

`cmd/www` accepts these flags. Each can also be set by its environment variable, so `go run ./cmd/www` (and `make run`) reads `RSS_DB_DSN` from the environment. A flag on the command line wins over its variable, which wins over the default; a variable set to the empty string counts as unset. `-help` lists them all.

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-env` | `RSS_ENV` | `development` | Environment (`development` or `production`; `production` logs JSON) |
| `-port` | `RSS_PORT` | `8080` | Server port |
| `-invite-code` | `RSS_INVITE_CODE` | _(empty: signup off)_ | Open signup to people with this code (at least 16 characters) |
| `-proxy-hops` | `RSS_PROXY_HOPS` | `0` | Reverse proxies in front of the app that append to `X-Forwarded-For` (0–3; 0 ignores the header). The client IP is the entry this many places from the right. Only safe when those proxies are the only way to reach the app |
| `-db-dsn` | `RSS_DB_DSN` | _(none, required)_ | PostgreSQL DSN |
| `-db-max-open-conns` | `RSS_DB_MAX_OPEN_CONNS` | `25` | PostgreSQL max open connections |
| `-db-max-idle-conns` | `RSS_DB_MAX_IDLE_CONNS` | `25` | PostgreSQL max idle connections |
| `-db-max-idle-time` | `RSS_DB_MAX_IDLE_TIME` | `15m` | PostgreSQL max idle time |
| `-lookup-workers` | `RSS_LOOKUP_WORKERS` | `2` | Background lookup workers (0–16; 0 disables them) |
| `-refresh-workers` | `RSS_REFRESH_WORKERS` | `2` | Background feed refresh workers (0–16; 0 disables them) |

Signup is off by default. To open it, run with `-invite-code $(openssl rand -hex 16)` and sign up at `/signup?invite=<code>`; no page links to `/signup`.

### `www migrate`

`www migrate [flags]` applies the migrations embedded in the binary and exits; hosts run it before new code goes live. While pre-alpha, it refuses to run if `migrations/*.sql` have changed since the database was migrated, instead of wiping it: locally, rebuild the database with `make db/reset`. It takes its own flags, also settable by environment variables:

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-env` | `RSS_ENV` | `development` | Environment (`development` or `production`; `production` logs JSON) |
| `-db-dsn` | `RSS_DB_DSN` | _(none, required)_ | PostgreSQL DSN |
| `-allow-reset-to` | `RSS_ALLOW_RESET_TO` | _(empty)_ | If the migrations have changed, wipe the database and migrate from scratch, but only if this is the new migrations' fingerprint |
| `-print-fingerprint` | `RSS_PRINT_FINGERPRINT` | `false` | Print the migrations' fingerprint and exit, without opening the database |

To wipe and rebuild a database other than the local one: `go run ./cmd/www migrate -allow-reset-to "$(go run ./cmd/www migrate -print-fingerprint)"`.

## Make targets

Run `make help` for the documented targets, among them `make docker/build`, which builds the container image (tagged `rss`, with the current commit as the version `/healthcheck` reports). Others: `make run`, `make build` (outputs `bin/www`), `make clean`.

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
- [`infra/README.md`](infra/README.md): production infrastructure on Render (Terraform) and how to run it.
- [`CLAUDE.md`](CLAUDE.md): code, testing and PR conventions (for people and coding agents), and which docs to update when.
