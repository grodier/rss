# Paper Round

[Paper Round](https://paperround.app) is a server-rendered RSS reader written in Go, using [chi](https://github.com/go-chi/chi), `html/template` (templates and the frontend's build are embedded in the binary), Postgres, and [scs](https://github.com/alexedwards/scs) sessions.

> **Work in progress** (pre-alpha). You can find websites and their feeds, subscribe, and read a timeline of their articles. See [Documentation](#documentation) for how it works and what's planned.

## Prerequisites

- Go (version in `go.mod`)
- Node, with the npm that ships with it (version in `web/.node-version`), to build the frontend with [Vite](https://vite.dev); only at build time, the image has no Node
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
make assets              # build the frontend into web/dist (Go doesn't compile without it)
make db/start            # start Postgres and Mailpit in Docker
make db/migrations/up    # apply migrations (asks for confirmation)
make run                 # http://localhost:8080
```

Instead of `make db/migrations/up` you can run `go run ./cmd/www migrate`.
The `make` targets that compile the app build the frontend first; with `go` directly, run `make assets` again after changing anything under `web/`.

## Command-line flags and environment variables

`cmd/www` accepts these flags.
Each can also be set by its environment variable, so `go run ./cmd/www` (and `make run`) reads `RSS_DB_DSN` from the environment.
A flag on the command line wins over its variable, which wins over the default; a variable set to the empty string counts as unset.
`-help` lists them all.

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-env` | `RSS_ENV` | `development` | Environment (`development` or `production`; `production` logs JSON) |
| `-port` | `RSS_PORT` | `8080` | Server port |
| `-debug-emails` | `RSS_DEBUG_EMAILS` | _(empty: debug mode off)_ | Accounts that may turn on debug mode, comma-separated (case doesn't matter); see "Debug mode" in [docs/features.md](docs/features.md) |
| `-proxy-hops` | `RSS_PROXY_HOPS` | `0` | Reverse proxies in front of the app that append to `X-Forwarded-For` (0–3; 0 ignores the header). The client IP is the entry this many places from the right. Only safe when those proxies are the only way to reach the app |
| `-db-dsn` | `RSS_DB_DSN` | _(none, required)_ | PostgreSQL DSN |
| `-db-max-open-conns` | `RSS_DB_MAX_OPEN_CONNS` | `25` | PostgreSQL max open connections |
| `-db-max-idle-conns` | `RSS_DB_MAX_IDLE_CONNS` | `25` | PostgreSQL max idle connections |
| `-db-max-idle-time` | `RSS_DB_MAX_IDLE_TIME` | `15m` | PostgreSQL max idle time |
| `-lookup-workers` | `RSS_LOOKUP_WORKERS` | `2` | Background lookup workers (0–16; 0 disables them) |
| `-refresh-workers` | `RSS_REFRESH_WORKERS` | `2` | Background feed refresh workers (0–16; 0 disables them) |
| `-import-workers` | `RSS_IMPORT_WORKERS` | `6` | Background OPML import workers (0–16; 0 disables them) |
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

Signup needs a single-use invite: make one with [`admin invite create`](#admin-invite) and sign up at the link it prints; no page links to `/signup`.
Locally, `make db/seed` makes your account without signing up (see [`admin create-user`](#admin-create-user)).

### `www migrate`

`www migrate [flags]` applies the migrations embedded in the binary and exits; hosts run it before new code goes live.
It applies only the migrations the database doesn't have yet, and keeps its data.
It takes its own flags, also settable by environment variables:

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-env` | `RSS_ENV` | `development` | Environment (`development` or `production`; `production` logs JSON) |
| `-db-dsn` | `RSS_DB_DSN` | _(none, required)_ | PostgreSQL DSN |
| `-seed` | `RSS_SEED` | `false` | After migrating, queue the seed list's lookups, as [`www seed`](#www-seed) does |

### `www seed`

`www seed [-env <env>] [-db-dsn <dsn>]` queues lookups of the sites in the seed list (`internal/seed/sites.txt`, embedded in the binary) that haven't been found yet, behind readers' own lookups, and exits; the app's lookup workers then find them.
It logs the sites whose lookups failed, and retries those that failed over an hour ago.
Production runs it on every deploy as `www migrate -seed`; locally, `make db/reset` doesn't, so run `make db/seed-sites` when you want the list looked up.

## The admin site

`cmd/admin` is the admin site, the owner's tools, a second binary built into the same image (see "Admin" in [docs/features.md](docs/features.md)).
Run it next to the app, against the same database:

```sh
make run/admin           # http://localhost:4001 (or: go run ./cmd/admin)
```

Every page needs a logged-in admin: a password, then a code from an authenticator app (or a recovery code).
To log in locally, give `.env` a key for admins' TOTP secrets, which both `admin enroll` and the site need (without one, the site makes a new key on every run, and no enrolled admin can log in):

```sh
echo "RSS_SECRET_KEY=$(openssl rand -hex 32)" >> .env
```

Then enroll yourself, typing the password on the first line (it's read from stdin, like [`admin create-user`](#admin-create-user)):

```sh
go run ./cmd/admin enroll -email you@example.com
```

It prints a QR code and the `otpauth://` URI it holds; add it to an authenticator app, type the code the app shows, and keep the 10 recovery codes it prints, each of which works once instead of a code, and which aren't shown again.
`admin enroll -email you@example.com -reset` replaces an admin's password, secret and recovery codes after the same steps, for a lost phone, and logs every admin out; there's no other way to make or change an admin.

`admin` and `admin serve` run the site; `admin -help` lists its flags, each also settable by its environment variable, as for `www`, so it reads `RSS_DB_DSN` from `.env` too:

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-env` | `RSS_ENV` | `development` | Environment (`development` or `production`) |
| `-port` | `RSS_PORT` | `4001` | Server port |
| `-base-url` | `RSS_BASE_URL` | `http://localhost:<port>` | The admin's own URL: `http` or `https`, no path, query or fragment |
| `-proxy-hops` | `RSS_PROXY_HOPS` | `0` | Reverse proxies in front of the admin that append to `X-Forwarded-For` (0–3), as for `www` |
| `-db-dsn` | `RSS_DB_DSN` | _(none, required)_ | PostgreSQL DSN |
| `-db-max-open-conns` | `RSS_DB_MAX_OPEN_CONNS` | `3` | PostgreSQL max open connections (1–10): the admin shares the database with `www` |
| `-secret-key` | `RSS_SECRET_KEY` | _(none; required in production)_ | Key that seals admins' TOTP secrets: 32 bytes, hex-encoded (`openssl rand -hex 32`); in development, a new one each run when unset |
| `-version` | `RSS_VERSION` | `false` | Print the build's version and exit |

Since the variables are shared, `RSS_PORT` or `RSS_BASE_URL` in `.env` apply to both binaries; set the admin's on its command line instead (`go run ./cmd/admin -port 4002`).

### `admin create-user`

`admin create-user -name <name> -email <email> [-db-dsn <dsn>]` creates an account and exits.
The password is the first line of stdin (a trailing `\n` or `\r\n` is dropped; other whitespace is kept); it is never a flag, so it stays out of shell history and process listings:

```sh
printf '%s\n' "$PASSWORD" | go run ./cmd/admin create-user -name You -email you@example.com
```

The name, email and password must pass signup's rules.
It makes a reader's account, not an admin: it's for local development, and in production accounts come from signing up with an invite.
Its flags, also settable by environment variables:

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-db-dsn` | `RSS_DB_DSN` | _(none, required)_ | PostgreSQL DSN |
| `-name` | `RSS_NAME` | _(none, required)_ | The account's name |
| `-email` | `RSS_EMAIL` | _(none, required)_ | The account's email |

It exits 0 when the account is created, 1 when it can't be (including when an account with that email already exists), and 2 for an empty DSN or invalid input, before opening the database.

### `admin invite`

`admin invite create [-email <email>] [-note <text>] [-expires 336h]` makes a single-use signup invite and prints its link, `<app-url>/signup?invite=<token>`.
With `-email`, the invite is bound to that address's waitlist entry, which must be confirmed: signup fills in the entry's name and the address, locks the address, and the account starts confirmed.
Without it, the invite is open: the person types their email and confirms it afterwards, and `-note` says who it's for.
To sign up locally, join the waitlist on `/` and confirm (the link is in Mailpit, or the app's log), then:

```sh
go run ./cmd/admin invite create -email you@example.com
```

`admin invite list` prints every invite, newest first, with its ID, when it was made, its email or note, its status (pending, redeemed, expired or revoked) and when it expires, in UTC.
`admin invite revoke <id>` stops an unused invite working.
Their flags, also settable by environment variables:

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-db-dsn` | `RSS_DB_DSN` | _(none, required)_ | PostgreSQL DSN (all three) |
| `-app-url` | `RSS_APP_URL` | `http://localhost:8080` | `create`: the reader's public URL, for the link (`http` or `https`, no path; a trailing `/` is dropped). The admin's own `-base-url` is its own host |
| `-email` | `RSS_EMAIL` | _(empty: an open invite)_ | `create`: bind the invite to this confirmed waitlist entry |
| `-note` | `RSS_NOTE` | _(empty)_ | `create`: who the invite is for, shown only in the admin |
| `-expires` | `RSS_EXPIRES` | `336h` | `create`: how long the invite works |

Each exits 0 on success, 1 when it fails (including `-email` without a confirmed waitlist entry, an unknown invite, or revoking one already used), and 2 for an empty DSN or invalid input, before opening the database.

### `admin send-test-email`

`admin send-test-email -to <address> [flags]` renders the test email and sends it with the mail settings, to check them; with no SMTP host, it logs the email instead.
It doesn't open the database:

```sh
go run ./cmd/admin send-test-email -to you@example.com
```

Locally, with Mailpit's settings from `.env.example`, the email shows up at http://localhost:8025.
Its `-base-url` is the app's URL, which the email links to, as for `www`: on the admin's service, whose `RSS_BASE_URL` is the admin's own URL, pass the app's on the command line (`-base-url https://paperround.app`).
Its flags are `-to` and the mail flags of `www` (see [Command-line flags and environment variables](#command-line-flags-and-environment-variables)), with the same environment variables: `-base-url`, `-support-email`, `-smtp-host`, `-smtp-port`, `-smtp-username`, `-smtp-password` and `-mail-from`.

| Flag | Variable | Default | Description |
| --- | --- | --- | --- |
| `-to` | `RSS_TO` | _(none, required)_ | Where to send the test email |

It prints `sent to <address>` and exits 0 when the email is sent (or exits 0 after logging it, with no SMTP host), exits 1 when sending fails, and exits 2 for a missing or invalid `-to` or invalid mail flags.

## Make targets

Run `make help` for the documented targets, among them `make docker/build`, which builds the container image (tagged `rss`, with the current commit as the version `/healthcheck` reports).
Among them: `make run` (the app), `make run/admin` (the admin site, locally), `make build` (outputs `bin/www` and `bin/admin`); also `make clean` (also removes `web/dist`).

`make assets` builds the frontend (`web/`) into `web/dist`, which package `web` embeds: it runs `npm ci` when `web/package-lock.json` changes and `npm run build` (the reader's build into `web/dist/app`, then the admin's into `web/dist/admin`) when a file under `web/app`, `web/admin` or `web/shared` (or a Vite config) does, so it's instant when nothing changed.
`make run`, `make run/admin`, `make build`, `make test`, `make test/db` and the database targets that run the app depend on it.
`make assets/watch` rebuilds on every change; restart `go run` to pick up a build, since it's embedded.
In `web/`, `npm run typecheck` checks the TypeScript (`tsc --noEmit`) and `npm run format:check` the formatting ([Prettier](https://prettier.io); `npm run format` fixes it); CI's `web` job runs both and the tests, then the build.

`make db/seed` creates your local account from `RSS_DEV_EMAIL`, `RSS_DEV_PASSWORD` and (optionally, default `Dev`) `RSS_DEV_NAME` in `.env` (see `.env.example`), with [`admin create-user`](#admin-create-user).
`make db/reset` runs it at the end when `RSS_DEV_EMAIL` is set.
`make db/seed-sites` queues lookups of the seed list's sites with [`www seed`](#www-seed); `make db/reset` doesn't, so a reset doesn't look up the whole list.

`make db/loadseed` seeds the local database at realistic scale (1,000 feeds, two years of articles, a user with 300 subscriptions) with `tools/loadseed`, then prints how long the timeline, starred, shared and feed queries take, with their plans.
`make db/loadseed/measure` prints the report again without reseeding.
Both need the local database (`make db/start` and migrations first); the tool refuses to run unless the database's name ends in `_dev` or `_test`.
Run `go run ./tools/loadseed -h` for its flags (sizes, prefix, random seed).

## Tests

```sh
make test      # go test ./... and the frontend tests; database tests are skipped
make test/db   # runs all tests, including database tests, against the local database
make test/web  # the frontend tests only (Vitest, in web/: npm test)
```

The frontend tests sit next to their modules in `web/` (`share.test.ts` beside `share.ts`) and run in happy-dom.
`web/app/features/testdata/timeline-summary.json` is written by a Go test: after changing the timeline form's summary in Go, run `go test ./internal/server -run TestTimelineSummaryFixture -update`, then make `timeline-form.ts` pass `make test/web`.

Database tests run when `RSS_TEST_DB_DSN` is set to a migrated database (`make test/db` uses `RSS_DB_DSN`).
CI sets it and fails if it is missing.

## Documentation

- [`docs/architecture.md`](docs/architecture.md): how the app fits together: the process, packages, routes, data model, background workers and security measures.
  Start here.
- [`docs/features.md`](docs/features.md): how each feature works and where to start reading its code.
- [`docs/decisions.md`](docs/decisions.md): decisions made while planning, and why.
- [Roadmap (#385)](https://github.com/grodier/rss/issues/385): the planned issues, in order.
- [`docs/backlog.md`](docs/backlog.md): work that has no issue yet, and the performance watchlist.
- [`infra/README.md`](infra/README.md): production infrastructure on Render (Terraform) and how to run it.
- [`CLAUDE.md`](CLAUDE.md): code, testing and PR conventions (for people and coding agents), and which docs to update when.
