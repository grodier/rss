# Architecture

How the app fits together. For how each feature works, step by step, see
[features.md](features.md); for why things are the way they are, see
[decisions.md](decisions.md).

## The process

There is one binary, `www` (`cmd/www`), and one Postgres database. Inside the process
run three long-lived components, all stopped by one context:

- the **HTTP server** (`internal/server`), which renders HTML pages;
- the **lookup runner** (`internal/lookup`), whose workers find the feeds of websites
  that users look up;
- the **refresh runner** (`internal/refresh`), whose workers fetch feeds when they're due.

```
 browser ──HTTP──► server ──────────────► psql repositories ──► Postgres
                     │ Refresh button            ▲   ▲
                     ▼                           │   │ lookups and feeds
 refresh.Runner ─► ingest.Refresher ─────────────┘   │ tables are the queues
                     │                               │
 lookup.Runner ──► discovery.Discoverer ─────────────┘ (saves via psql)
                     │
                     ▼
                   fetch ──HTTP──► publishers' websites and feeds
```

`cmd/www/application.go` (`Application.Run`) wires everything: it parses the flags,
opens the database, builds one `fetch.Client` and one `ingest.Refresher` (shared by the
Refresh button and the background workers), the repositories, the scs session manager
(sessions in Postgres, 12-hour lifetime) and the server, then starts the runners next to
`Server.Serve`. On SIGINT or SIGTERM, or when any component fails, the context is
canceled: the server drains requests for up to 30 seconds, the workers stop, and only
then is the database closed. Only `cmd/www` handles signals.

## Packages

| Package | Role | Depends on |
| --- | --- | --- |
| `cmd/www` | Flags, wiring, signal handling | everything below |
| `internal/server` | Router, middleware, handlers, templates, rate limits | `rss`, `ui`, `validator`, `sanitize`, `discovery` (input parsing), `ingest` (errors), `psql` (interface checks only) |
| `internal/psql` | Repositories: plain SQL, driver errors mapped to `rss` errors | `rss`, `password` |
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

Consumers define the interfaces they need, and only `cmd/www` builds the concrete types.
The server's store interfaces (`FeedStore`, `ArticleStore`, …) and the `Services` struct
that holds them are in `internal/server/server.go`, with compile-time checks that the
`psql` repositories satisfy them. `lookup`, `refresh`, `ingest` and `discovery` each
declare their own small `Store` or `Fetcher` interface. That's what lets every package
be tested with fakes (see Testing).

## HTTP

**Middleware**, outermost first (`internal/server/router.go`):

1. `logRequest`: one log line per request with status, size and duration.
2. `recoverPanic`: a panic becomes the 500 page.
3. `http.CrossOriginProtection`: rejects cross-origin non-GET browser requests
   (`Sec-Fetch-Site` / `Origin`) with the 403 page. There are no CSRF tokens.
4. `commonHeaders`: CSP `default-src 'self'; img-src 'self' https:`, `nosniff`,
   `X-Frame-Options: deny`, `Referrer-Policy`.
5. `middleware.GetHead`: HEAD is served by the GET handler.

Then routes are split into groups. `/static/*` and `/healthcheck` have no session. All
other routes load the session (`sessionManager.LoadAndSave`) and `authenticate`, which
puts the logged-in user's ID in the request context if the session's user still exists.
Routes in the `requireAuthentication` group redirect anonymous users to `/login` and set
`Cache-Control: no-store`.

**Routes:**

| Route | Login | Handler | What it does |
| --- | --- | --- | --- |
| `GET /healthcheck` | no | `healthcheckHandler` | JSON status, for health checks |
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
users ─┬─< subscriptions >── feeds ──< articles ──< reads >── users
       │                       │
       │                     sites ──< lookups (site_id, once found)
       └── sessions (scs, keyed by token)
```

- **`sites`**: a website, unique by `host`, its *site key* (lowercase host without
  `www.`, `discovery.SiteKey`).
- **`feeds`**: unique by canonical `url`; `site_id` is the site it was first discovered
  from. Also the refresh queue: `next_fetch_at` (when it's due, and the claim lease),
  `consecutive_failures`, `last_error` (internal, never shown), `etag` / `last_modified`
  (for conditional GET), `gone_at` (410 Gone: never fetched again), and
  `refresh_requested_at` (a page view asked for a fetch).
- **`articles`**: unique by `(feed_id, external_id)`. `summary` and `content` are raw HTML
  from the feed and are only rendered through `sanitize.HTML`; `excerpt` is plain text;
  `canonical_url` matches copies of one article across feeds; `timeline_at` is set once
  on insert and never updated.
- **`subscriptions`** and **`reads`**: `(user_id, feed_id)` and `(user_id, article_id)`.
- **`lookups`**: one row per site key. It is both the lookup queue (`status` pending →
  running → done or failed) and the cache of the result (`site_id`).
- **`users`** (`email` is `citext`) and **`sessions`** (scs's table).

Deleting a site cascades to its feeds, their articles and reads; deleting a user cascades
to their subscriptions and reads. Nothing in the app deletes rows yet.

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

Where each protection lives; [decisions.md](decisions.md) has the reasons.

- **Cross-site requests:** `CrossOriginProtection` (see HTTP). Because it doesn't cover
  GET, GET handlers don't write, apart from two documented, idempotent exceptions
  (`feedHandler` queues a refresh of a stale feed; `articleHandler` records a read), and
  never fetch user-supplied URLs.
- **Untrusted HTML:** article HTML is only rendered through `sanitize.HTML`, which
  resolves relative URLs, replaces iframes with links and applies a bluemonday allowlist.
  The CSP blocks inline scripts and styles; images may load from any `https:` host.
- **Sessions:** scs, stored in Postgres; the token is renewed on login and logout; cookies
  are `SameSite=Lax`, and `Secure` with `-env production`.
- **Passwords:** Argon2id with OWASP's minimum parameters (`internal/password`). At most
  `GOMAXPROCS` hashes run at once; the rest wait. Login verifies a dummy hash for unknown
  emails, and a duplicate signup looks like a successful one.
- **Rate limits** (`internal/server/ratelimit.go`): in-memory fixed windows, per process.

  | Limiter | Key | Limit |
  | --- | --- | --- |
  | `loginLimiter` | client IP (IPv6 by /64) | 20 per 10 min |
  | `loginFailureLimiter` | submitted email | 10 failed logins per 15 min |
  | `signupLimiter` | client IP | 10 per hour |
  | `lookupLimiter` | user | 10 per 10 min |
  | `refreshLimiter` | user | 10 per 10 min |

- **Client IP:** `clientIP` (`internal/server/clientip.go`) is the only code that reads
  `RemoteAddr`.
- **Internal errors** (`lookups.error`, `feeds.last_error`, Go errors) go to the logs,
  never to pages.

## Frontend

- **Templates** (`internal/ui/templates`) are standalone pages. Shared fragments are
  `{{define}}` blocks in `partials/`: `app-nav` (the logged-in navigation), `article-row`
  (one row of the timeline or a feed page), avatars and icons (inline SVG). Logged-in
  pages use the `.shell` layout, logged-out and error pages `.solo`.
- **CSS** is one file, `static/css/main.css`. Colors are custom properties on `:root`.
- **JavaScript** is one file, `static/js/app.js`, loaded with `defer`. Each enhancement
  hooks onto server-rendered markup and the page works without it: local times
  (`<time data-local>`), read pings (`<a ping>` → `navigator.sendBeacon`), and infinite
  scroll (fetch the page behind `.pager a[rel=next]` and append its `.article-row`s).
  `internal/server/scroll_test.go` pins the markup the script relies on.
- **Static files** are embedded and served at `/static/…?v=<hash>`; the `asset` template
  function builds those URLs, so a changed file gets a new URL.

## Testing

- **Server:** `newTestServer(t)` or `newTestServerWith(t, Services{...})` with the
  function-field fakes in `internal/server/fakes_test.go`; no database. Tests go through
  `s.router()` with `httptest`, or call a handler directly.
- **Repositories:** DB tests in `internal/psql` use `psqltest.NewDB(t)` against a migrated
  database (`RSS_TEST_DB_DSN`); they skip locally without it and fail in CI.
- **Everything else** uses fakes for its own interfaces, and `httptest` servers with
  `fetch.Options{AllowPrivate: true}` where real HTTP matters (`fetch`, `discovery`,
  `ingest`).
- **CI** (`.github/workflows/ci.yml`) runs gofmt, `go mod tidy -diff`, vet, staticcheck,
  the migrations and `go test -race` against Postgres 17.

## Deployment

Not deployed yet. The plan (Render, Terraform, a container image, configuration from
environment variables, a `migrate` command) is under "Deployment" in
[decisions.md](decisions.md), and the work is #208–#217. Update this section as those
land.
