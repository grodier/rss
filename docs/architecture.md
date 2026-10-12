# Architecture

How the app fits together. [features.md](features.md) walks through each feature's
code, [decisions.md](decisions.md) has the reasons behind it, and CLAUDE.md has the
conventions to follow when changing it.

## What the app does

A reader finds websites by searching. If a site isn't known yet, they *look it up*: the
app finds the feeds the site publishes and shows them on a site page. They subscribe to
feeds, and the app fetches those feeds in the background. Their home page is a timeline
of the articles in their subscriptions, newest arrivals first; opening an article shows
its text and marks it read.

## Glossary

- **Site:** a website, identified by its *site key*, the lowercase host without `www.`
  (`example.com` for `https://www.Example.com/blog`).
- **Feed:** an RSS, Atom or JSON Feed document. It belongs to the site it was first
  discovered from.
- **Lookup:** a background job that finds a site's feeds. Its row is also the cached
  result for that site key.
- **Article:** one item of a feed. The same story in two feeds is two articles, *copies*
  matched by `canonical_url` (the article's URL without tracking parameters).
- **Timeline position** (`timeline_at`): where an article sits in timelines, set once when
  it's first saved: the save time for *news*, its published date for *backlog* (older
  items, including every dated item on a feed's first save).
- **Subscription:** a user following a feed. Subscriptions decide what's in the user's
  timeline and which feeds are refreshed in the background.
- **Read:** the user opened the article, or clicked through to the original, or did either
  for a copy of it.
- **Refresh request:** a feed page view asking the workers to fetch a stale feed soon.

## The process

There is one binary, `www` (`cmd/www`), and one Postgres database. Inside the process
run three long-lived components, all stopped by one context:

- The **HTTP server** (`internal/server`) handles requests and reads and writes the
  database through the `internal/psql` repositories. Its Refresh button calls
  `ingest.Refresher` directly.
- The **refresh runner** (`internal/refresh`) claims due feeds from the `feeds` table and
  hands each to `ingest.Refresher`, which fetches the feed and saves the outcome.
- The **lookup runner** (`internal/lookup`) claims pending rows from the `lookups` table,
  runs `discovery.Discoverer` to find the site's feeds, and saves them with
  `psql.DiscoveryRepository`.

`ingest` and `discovery` make every outbound request through `internal/fetch`.

`cmd/www/main.go` (`run`) hands `www migrate` to `runMigrate` (below). Otherwise it
parses the configuration (`parseConfig` in `config.go`: each flag,
or else its `RSS_` environment variable via `setFlagsFromEnv`, or else its default;
errors go to stderr) and builds the slog logger on stdout (`newLogger`: JSON in
production, text otherwise), which also reports the error `Run` returns.
`cmd/www/application.go` (`Application.Run`) wires everything: it opens the database, builds one `fetch.Client` and one `ingest.Refresher` (shared by the
Refresh button and the background workers), the repositories, the scs session manager
(sessions in Postgres, 12-hour lifetime) and the server, then starts the runners next to
`Server.Serve`. On SIGINT or SIGTERM, or when any component fails, the context is
canceled: the server drains requests for up to 30 seconds, the workers stop, and only
then is the database closed. Only `cmd/www` handles signals.

`www migrate` (`runMigrate` in `cmd/www/migrate.go`, with its own flags) applies the
migrations embedded in the binary (`migrations.FS`) with `psql.Migrate` and exits. It
runs before new code goes live, never at startup. While pre-alpha, `Migrate` compares
`psql.Fingerprint` (a SHA-256 of the names and contents of `migrations/*.sql`) with the
one stored in `schema_fingerprint` when it last ran: if migrations were applied from
different files, it refuses, unless `-allow-reset-to` is the new fingerprint, in which
case it drops and recreates the current schema first. Then it runs goose's `Up` and
stores the new fingerprint.

## Packages

| Package | Role | Depends on |
| --- | --- | --- |
| `cmd/www` | Flags, wiring, signal handling, the `migrate` command | `server`, `psql`, `lookup`, `refresh`, `ingest`, `discovery`, `fetch`, `migrations` |
| `internal/server` | Router, middleware, handlers, templates, rate limits | `rss`, `ui`, `validator`, `sanitize`, `discovery` (input parsing), `ingest` (errors), `psql` (interface checks only) |
| `internal/psql` | Repositories: plain SQL, driver errors mapped to `rss` errors; `Migrate` | `rss`, `password` |
| `internal/lookup` | Lookup queue workers | `discovery`, `ingest`, `rss` |
| `internal/refresh` | Feed refresh workers | `ingest`, `rss` |
| `internal/ingest` | Fetch a feed, turn items into articles, schedule the next fetch | `fetch`, `feedparse`, `rss` |
| `internal/discovery` | Normalize user input, find a site's feeds | `fetch`, `feedparse` |
| `internal/feedparse` | Recognize and parse RSS, Atom and JSON Feed (via gofeed) | — |
| `internal/fetch` | The only outbound HTTP client for user-influenced URLs | — |
| `internal/sanitize` | Make article HTML safe to render (bluemonday) | — |
| `internal/password` | Argon2id hashing | — |
| `internal/rss` | Domain types (`Feed`, `Article`, `Site`, …) and sentinel errors | — |
| `internal/ui` | Embedded templates and static files | — |
| `internal/validator` | Form validation helpers | — |
| `migrations` | The goose SQL migrations, embedded (`FS`) | — |

Consumers define the interfaces they need, and only `cmd/www` builds the concrete types.
The server's store interfaces (`FeedStore`, `ArticleStore`, …) and the `Services` struct
that holds them are in `internal/server/server.go`, with compile-time checks that the
`psql` repositories satisfy them. `lookup`, `refresh`, `ingest` and `discovery` each
declare their own small interfaces (`Store`, `Fetcher`, `Saver`, …). That's what lets
every package be tested with fakes (see Testing).

Third-party modules and what they're for (CLAUDE.md asks for a justification before
adding another):

| Module | Used for |
| --- | --- |
| `go-chi/chi` | Router: route groups, URL parameters, `GetHead` |
| `alexedwards/scs` (+ `postgresstore`) | Sessions, stored in the `sessions` table |
| `lib/pq` | Postgres driver; `pq.Error` codes are mapped to `rss` errors |
| `pressly/goose` | Applying the migrations (`psql.Migrate`); its CLI only creates and rolls back migrations |
| `go-playground/form` | Decoding POST forms into structs |
| `mmcdole/gofeed` | Parsing RSS, Atom and JSON Feed (`internal/feedparse`) |
| `microcosm-cc/bluemonday` | The HTML allowlist in `internal/sanitize` |
| `golang.org/x/net/html` | Reading HTML pages (`discovery.ParsePage`) and rewriting article HTML |
| `golang.org/x/crypto/argon2` | Password hashing |
| `honnef.co/go/tools` | `staticcheck`, as a `go tool` |

## HTTP

**Middleware**, outermost first (`internal/server/router.go`):

1. `logRequest`: one log line per request with status, size and duration.
2. `recoverPanic`: a panic becomes the 500 page.
3. `http.CrossOriginProtection`: rejects cross-origin non-GET browser requests
   (`Sec-Fetch-Site` / `Origin`) with the 403 page. There are no CSRF tokens.
4. `commonHeaders`: CSP `default-src 'self'; img-src 'self' https:`, `nosniff`,
   `X-Frame-Options: deny`, `Referrer-Policy`.
5. `middleware.GetHead`: HEAD is served by the GET handler, upkeep side effects included.

Then routes are split into groups. `/static/*` and `/healthcheck` have no session. All
other routes load the session (`sessionManager.LoadAndSave`) and `authenticate`, which
puts the logged-in user's ID in the request context if the session's user still exists.
Routes in the `requireAuthentication` group redirect anonymous users to `/login` and set
`Cache-Control: no-store`.

**Routes:**

| Route | Login | Handler | What it does |
| --- | --- | --- | --- |
| `GET /healthcheck` | no | `healthcheckHandler` | JSON status, environment and build version, for health checks |
| `GET /static/*` | no | file server | Embedded CSS and JS; `?v=<hash>` URLs are cached for a year |
| `GET /` | no | `homeHandler` | Landing page; the timeline when logged in |
| `GET, POST /signup` | no | `signupHandler`, `signupFormHandler` | Only registered with `-signup` |
| `GET, POST /login` | no | `loginHandler`, `loginFormHandler` | |
| `POST /logout` | no | `logoutHandler` | |
| `GET /feeds` | yes | `feedsHandler` | My feeds: the user's subscriptions by site |
| `GET /feeds/{id}` | yes | `feedHandler` | Feed page; queues a refresh if the feed is stale |
| `POST /feeds/{id}/refresh` | yes | `feedRefreshHandler` | Refresh button: fetches now |
| `POST /feeds/{id}/subscribe` | yes | `feedSubscribeHandler` | |
| `POST /feeds/{id}/unsubscribe` | yes | `feedUnsubscribeHandler` | |
| `GET /articles/{id}` | yes | `articleHandler` | Article page; records the read |
| `POST /articles/{id}/read` | yes | `articleReadHandler` | Click-through ping; 204 |
| `GET /sites/{id}` | yes | `siteHandler` | A site and its feeds |
| `GET /search` | yes | `searchHandler` | Search sites and feeds in the database |
| `POST /lookups` | yes | `lookupCreateHandler` | Queue a site lookup; wait up to 3 s for it |
| `GET /lookups/{id}` | yes | `lookupHandler` | Lookup status page |

**Rendering** (`internal/server/html.go`): at startup every `templates/*.html` page is
parsed together with all of `templates/partials/*.html` into one template per page.
`renderHTML` executes into a buffer first, so a template error becomes a clean 500
instead of half a page. Template functions: `asset` (a static file's URL with a content
hash, from `static.go`), `avatarTone` and `monogram`. Errors are HTML pages via
`errorHTML` (`errors.go`); JSON is only for `/healthcheck`.

**Timeouts:** the server's `ReadTimeout` is 5 s and `WriteTimeout` 10 s, so handlers that
wait on slow work bound it below that: the Refresh button gives a fetch 8 s, and
`POST /lookups` waits at most 3 s before redirecting to the status page.

## Data model

The whole schema is `migrations/00001_init.sql` (edited in place while pre-alpha; see
CLAUDE.md).

```
users ──< subscriptions >── feeds ──< articles ──< reads >── users
                              │
                            sites ──< lookups (site_id, once found)

sessions (scs's table: session data keyed by token, no foreign keys)
```

- **`sites`**: a website, unique by `host`, its *site key* (lowercase host without
  `www.`, `discovery.SiteKey`).
- **`feeds`**: unique by canonical `url`; `site_id` is the site it was first discovered
  from. Also the refresh queue: `next_fetch_at` (when it's due, and the claim lease),
  `consecutive_failures`, `last_error` (internal, never shown), `etag` / `last_modified`
  (for conditional GET), `gone_at` (410 Gone: not fetched again until a lookup finds the
  feed again), and `refresh_requested_at` (a page view asked for a fetch).
- **`articles`**: unique by `(feed_id, external_id)`. `summary` and `content` are raw HTML
  from the feed; `excerpt` is plain text; `canonical_url` matches copies of one article
  across feeds; `timeline_at` is set once on insert and never updated.
- **`subscriptions`** and **`reads`**: `(user_id, feed_id)` and `(user_id, article_id)`.
- **`lookups`**: one row per site key. It is both the lookup queue (`status` pending →
  running → done or failed) and the cache of the result (`site_id`).
- **`users`**: `email` is `citext`, so unique regardless of case.

Deleting a site cascades to its feeds and their articles, subscriptions and reads, and
clears `lookups.site_id`; deleting a user cascades to their subscriptions and reads.
Nothing deletes users, sites, feeds or articles yet; unsubscribing deletes the
subscription row, and scs deletes expired sessions.

Two tables aren't in `00001_init.sql`: goose's `goose_db_version`, and
`schema_fingerprint` (one row: the fingerprint of the migrations last applied), which
`psql.Migrate` creates for the pre-alpha check (see "The process").

## Background work

Both runners follow the same pattern: N worker goroutines (`-lookup-workers`,
`-refresh-workers`, default 2, 0 turns a runner off) loop claiming one job at a time from
a Postgres table with `FOR UPDATE SKIP LOCKED`, so several instances can run side by side.
When nothing is claimable a worker sleeps for a poll interval. Each job gets a timeout;
its outcome is recorded with a fresh short context, so a job that timed out still records
its failure. On shutdown nothing is recorded and the job is retried later.

| | Lookups (`internal/lookup`) | Feed refresh (`internal/refresh`) |
| --- | --- | --- |
| Queue | `lookups` rows with `status = 'pending'` | `feeds` with `next_fetch_at <= now()`, not gone, and subscribed or requested |
| Claim | `ClaimNext`: set `running` | `ClaimDue`: move `next_fetch_at` 5 min ahead (the lease); requested feeds first |
| Poll when idle | 500 ms | 15 s |
| Job timeout | 30 s | 30 s |
| Crashed job | `ResetStale` every minute: running for over 2 min → pending, failed after 3 attempts | The lease runs out and the feed is due again |
| Work | `discovery.Discoverer`, then `DiscoveryRepository.Save` | `ingest.Refresher.Refresh` |

## Outbound HTTP

All requests to URLs a user can influence (websites to look up, feeds and the pages they
point to) go through `fetch.Client` (`internal/fetch`):

- only `http` and `https`, no credentials in URLs, at most 5 redirects, each re-checked;
- a dialer check on the resolved IP of every connection, which blocks loopback, private,
  link-local, multicast and other special ranges and so also defeats DNS rebinding;
- 10 s for the whole request, bodies capped at 5 MiB, no environment proxy;
- a fixed `User-Agent` and `Accept`.

`fetch.Options.AllowPrivate` turns the address check off for tests that use `httptest`
servers; `cmd/www` never sets it.

## Security measures

Where each protection lives. The reasons are in [decisions.md](decisions.md); the rules
for writing code that keeps them (GET handlers, article HTML, inline scripts) are in
CLAUDE.md.

- **Cross-site requests:** `CrossOriginProtection` in the middleware (see HTTP); there
  are no CSRF tokens.
- **Untrusted HTML:** `sanitize.HTML` (see Reading in features.md). The CSP blocks inline
  scripts and styles; images may load from any `https:` host.
- **Outbound requests:** `internal/fetch` (see Outbound HTTP).
- **Sessions:** scs, stored in Postgres; the token is renewed on login and logout; cookies
  are `SameSite=Lax`, and `Secure` with `-env production`.
- **Passwords:** Argon2id with OWASP's minimum parameters (`internal/password`); at most
  `GOMAXPROCS` hashes run at once and the rest wait. How login and signup avoid revealing
  which emails have accounts is under "Accounts and sessions" in features.md.
- **Rate limits** (`internal/server/ratelimit.go`): in-memory fixed windows, per process.

  | Limiter | Key | Limit |
  | --- | --- | --- |
  | `loginLimiter` | client IP (IPv6 by /64) | 20 per 10 min |
  | `loginFailureLimiter` | submitted email | 10 failed logins per 15 min |
  | `signupLimiter` | client IP (IPv6 by /64) | 10 per hour |
  | `lookupLimiter` | user | 10 per 10 min |
  | `refreshLimiter` | user | 10 per 10 min |

- **Client IP:** `clientIP` (`internal/server/clientip.go`) is the only code that reads
  `RemoteAddr`.
- **Internal errors** (`lookups.error`, `feeds.last_error`, Go errors) go to the logs,
  never to pages.

## Frontend

Templates, CSS and JavaScript follow the conventions in CLAUDE.md. Beyond those:

- **Static files** are embedded and served at `/static/…?v=<hash>`. The `asset` template
  function builds those URLs, so a changed file gets a new URL and can be cached for a
  year.
- **`app.js`** has three enhancements, each hooked onto server-rendered markup: local
  times (`<time data-local>`), read pings (`<a ping>` → `navigator.sendBeacon`), and
  infinite scroll (fetch the page behind `.pager a[rel=next]` and append its
  `.article-row`s). `internal/server/scroll_test.go` pins the markup the script relies on.

## Testing

Server and DB test conventions are in CLAUDE.md. The other packages test against fakes of
their own interfaces, and use `httptest` servers with `fetch.Options{AllowPrivate: true}`
where real HTTP matters (`fetch`, `discovery`, `ingest`).

## Deployment

Not deployed yet. The plan (Render, Terraform, a container image, configuration from
`RSS_*` environment variables and a `migrate` command (both done, see "The process")) is under "Deployment" in
[decisions.md](decisions.md), and the work is #208–#217. Update this section as those
land.

**Container image.** The `Dockerfile` builds `www` in a `golang` stage (the version in
`go.mod`) as a static binary (`CGO_ENABLED=0`) and copies it into
`gcr.io/distroless/static-debian12:nonroot`, which has the CA certificates feed fetching
needs and runs as a non-root user. The build takes no secrets; `.dockerignore` keeps
`.env`, `.git` and `docs` out of the context. `-ldflags "-X main.version=..."` sets
`version` in `cmd/www`, which reaches `server.Config.Version` and is reported by
`/healthcheck` as `system_info.version` (`dev` for a plain `go build`).

CI's `image` job (`.github/workflows/ci.yml`, after `test`) builds the image for
`linux/amd64` on every event, with the commit SHA as the version. On pushes to `main` it
also pushes `ghcr.io/grodier/rss:sha-<sha>` and `ghcr.io/grodier/rss:main`, attests the
pushed digest with `actions/attest-build-provenance`, and exposes the digest as the job
output `digest`. PRs never push.
