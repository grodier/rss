# rss

A server-rendered RSS reader written in Go, using [chi](https://github.com/go-chi/chi), `html/template` (templates and static files are embedded in the binary), Postgres, and [scs](https://github.com/alexedwards/scs) sessions.

> **Work in progress** (pre-alpha). You can find websites and their feeds, subscribe, and read a timeline of their articles. See [Documentation](#documentation) for how it works and what's planned.

## Prerequisites

- Go (version in `go.mod`)
- Docker and Docker Compose
- [goose](https://github.com/pressly/goose), only for `make db/migrations/new` and `make db/migrations/down`: `go install github.com/pressly/goose/v3/cmd/goose@latest`.
  The app applies migrations itself (`www migrate`).
- Optional: `psql` (for `make db/psql`)

## Configuration

The `Makefile` includes and exports a gitignored `.env` file.
It is not enforced by `make`, but `make run` and the database targets need `RSS_DB_DSN` from it.
Copy the example, whose value matches `compose.yml`:

```sh
cp .env.example .env
```

```
RSS_DB_DSN=postgres://rssapp:dev-password@localhost:5432/rssapp_dev?sslmode=disable
RSS_SMTP_HOST=localhost
RSS_SMTP_PORT=1025
```

`make db/start` also starts [Mailpit](https://mailpit.axllent.org), which catches the app's email: with the two `RSS_SMTP_` lines above, every email the app sends shows up at http://localhost:8025 instead of reaching anyone.
Remove them and the app logs each email instead, links included.

## Quick start

```sh
cp .env.example .env
make db/start            # start Postgres and Mailpit in Docker
make db/migrations/up    # apply migrations (asks for confirmation)
make run                 # http://localhost:8080
```

Instead of `make db/migrations/up` you can run `go run ./cmd/www migrate`.

## Command-line flags and environment variables

`cmd/www` accepts these flags.
Each can also be set by its environment variable, so `go run ./cmd/www` (and `make run`) reads `RSS_DB_DSN` from the environment.
A flag on the command line wins over its variable, which wins over the default; a variable set to the empty string counts as unset.
`-help` lists them all.

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-env` | `RSS_ENV` | `development` | Environment (`development` or `production`; `production` logs JSON) |
| `-port` | `RSS_PORT` | `8080` | Server port |
| `-invite-code` | `RSS_INVITE_CODE` | _(empty: signup off)_ | Open signup to people with this code (at least 16 characters) |
| `-debug-emails` | `RSS_DEBUG_EMAILS` | _(empty: debug mode off)_ | Accounts that may turn on debug mode, comma-separated (case doesn't matter); see "Debug mode" in [docs/features.md](docs/features.md) |
| `-proxy-hops` | `RSS_PROXY_HOPS` | `0` | Reverse proxies in front of the app that append to `X-Forwarded-For` (0–3; 0 ignores the header). The client IP is the entry this many places from the right. Only safe when those proxies are the only way to reach the app |
| `-db-dsn` | `RSS_DB_DSN` | _(none, required)_ | PostgreSQL DSN |
| `-db-max-open-conns` | `RSS_DB_MAX_OPEN_CONNS` | `25` | PostgreSQL max open connections |
| `-db-max-idle-conns` | `RSS_DB_MAX_IDLE_CONNS` | `25` | PostgreSQL max idle connections |
| `-db-max-idle-time` | `RSS_DB_MAX_IDLE_TIME` | `15m` | PostgreSQL max idle time |
| `-lookup-workers` | `RSS_LOOKUP_WORKERS` | `2` | Background lookup workers (0–16; 0 disables them) |
| `-refresh-workers` | `RSS_REFRESH_WORKERS` | `2` | Background feed refresh workers (0–16; 0 disables them) |
| `-import-workers` | `RSS_IMPORT_WORKERS` | `2` | Background OPML import workers (0–16; 0 disables them) |
| `-icon-workers` | `RSS_ICON_WORKERS` | `1` | Background workers that find and store site icons (0–4; 0 disables them) |
| `-enrich-workers` | `RSS_ENRICH_WORKERS` | `1` | Background workers that fetch articles' web pages for a missing image, title or excerpt (0–4; 0 disables them) |
| `-mail-workers` | `RSS_MAIL_WORKERS` | `1` | Background workers that send queued emails (0–4; with 0 this instance queues emails but doesn't send them) |
| `-base-url` | `RSS_BASE_URL` | `http://localhost:<port>` | Public URL of the app, for links in emails and shared timelines: `http` or `https`, no path (a trailing `/` is dropped), query or fragment. Required in production, where it must be `https` |
| `-support-email` | `RSS_SUPPORT_EMAIL` | _(empty)_ | Where people can get help: shown on pages and used as the Reply-To of emails (required in production) |
| `-smtp-host` | `RSS_SMTP_HOST` | _(empty: emails are logged, not sent)_ | SMTP server (required in production) |
| `-smtp-port` | `RSS_SMTP_PORT` | `587` | SMTP port: 465 for implicit TLS, otherwise STARTTLS (required unless the host is `localhost` or `127.0.0.1`) |
| `-smtp-username` | `RSS_SMTP_USERNAME` | _(empty)_ | SMTP username |
| `-smtp-password` | `RSS_SMTP_PASSWORD` | _(empty)_ | SMTP password |
| `-mail-from` | `RSS_MAIL_FROM` | `rss <hello@localhost>` | From address of every email (required in production) |

In production (`-env production`) the app refuses to start without `-base-url`, `-support-email`, `-smtp-host` and `-mail-from`: emails carry one-time links, which must never end up in the logs there.

Signup is off by default.
To open it, run with `-invite-code $(openssl rand -hex 16)` and sign up at `/signup?invite=<code>`; no page links to `/signup`.
Locally, `make db/seed` makes your account without signing up (see [`www create-user`](#www-create-user)).

### `www migrate`

`www migrate [flags]` applies the migrations embedded in the binary and exits; hosts run it before new code goes live.
While pre-alpha, it refuses to run if `migrations/*.sql` have changed since the database was migrated, instead of wiping it: locally, rebuild the database with `make db/reset`.
It takes its own flags, also settable by environment variables:

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-env` | `RSS_ENV` | `development` | Environment (`development` or `production`; `production` logs JSON) |
| `-db-dsn` | `RSS_DB_DSN` | _(none, required)_ | PostgreSQL DSN |
| `-allow-reset-to` | `RSS_ALLOW_RESET_TO` | _(empty)_ | If the migrations have changed, wipe the database and migrate from scratch, but only if this is the new migrations' fingerprint |
| `-print-fingerprint` | `RSS_PRINT_FINGERPRINT` | `false` | Print the migrations' fingerprint and exit, without opening the database |

To wipe and rebuild a database other than the local one: `go run ./cmd/www migrate -allow-reset-to "$(go run ./cmd/www migrate -print-fingerprint)"`.

### `www create-user`

`www create-user -name <name> -email <email> [-db-dsn <dsn>]` creates an account and exits.
The password is the first line of stdin (a trailing `\n` or `\r\n` is dropped; other whitespace is kept); it is never a flag, so it stays out of shell history and process listings:

```sh
printf '%s\n' "$PASSWORD" | go run ./cmd/www create-user -name You -email you@example.com
```

The name, email and password must pass signup's rules.
It's for local development: in production, accounts come from signing up with the invite code.
Its flags, also settable by environment variables:

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-db-dsn` | `RSS_DB_DSN` | _(none, required)_ | PostgreSQL DSN |
| `-name` | `RSS_NAME` | _(none, required)_ | The account's name |
| `-email` | `RSS_EMAIL` | _(none, required)_ | The account's email |

It exits 0 when the account is created, 1 when it can't be (including when an account with that email already exists), and 2 for an empty DSN or invalid input, before opening the database.

### `www send-test-email`

`www send-test-email -to <address> [flags]` renders the test email and sends it with the mail settings, to check them; with no SMTP host, it logs the email instead.
It doesn't open the database:

```sh
go run ./cmd/www send-test-email -to you@example.com
```

Locally, with Mailpit's settings from `.env.example`, the email shows up at http://localhost:8025.
Its flags are `-to` and the mail flags of `www` (above), with the same environment variables: `-base-url`, `-support-email`, `-smtp-host`, `-smtp-port`, `-smtp-username`, `-smtp-password` and `-mail-from`.

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-to` | `RSS_TO` | _(none, required)_ | Where to send the test email |

It prints `sent to <address>` and exits 0 when the email is sent (or exits 0 after logging it, with no SMTP host), exits 1 when sending fails, and exits 2 for a missing or invalid `-to` or invalid mail flags.

## Make targets

Run `make help` for the documented targets, among them `make docker/build`, which builds the container image (tagged `rss`, with the current commit as the version `/healthcheck` reports).
Others: `make run`, `make build` (outputs `bin/www`), `make clean`.

`make db/seed` creates your local account from `RSS_DEV_EMAIL`, `RSS_DEV_PASSWORD` and (optionally, default `Dev`) `RSS_DEV_NAME` in `.env` (see `.env.example`), with `www create-user`.
`make db/reset` runs it at the end when `RSS_DEV_EMAIL` is set.

## Tests

```sh
make test      # go test ./... ; database tests are skipped
make test/db   # runs all tests, including database tests, against the local database
```

Database tests run when `RSS_TEST_DB_DSN` is set to a migrated database (`make test/db` uses `RSS_DB_DSN`).
CI sets it and fails if it is missing.

## Documentation

- [`docs/architecture.md`](docs/architecture.md): how the app fits together: the process, packages, routes, data model, background workers and security measures.
  Start here.
- [`docs/features.md`](docs/features.md): how each feature works and which code to read, in order.
- [`docs/decisions.md`](docs/decisions.md): decisions made while planning, and why.
- [`docs/todo.md`](docs/todo.md): what's next, planned work that has no issue yet, and the performance watchlist.
- [`infra/README.md`](infra/README.md): production infrastructure on Render (Terraform) and how to run it.
- [`CLAUDE.md`](CLAUDE.md): code, testing and PR conventions (for people and coding agents), and which docs to update when.
