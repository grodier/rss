# Architecture

How the app fits together.
[features.md](features.md) walks through each feature's code, [decisions.md](decisions.md) has the reasons behind it, and CLAUDE.md has the conventions to follow when changing it.

## What the app does

A reader finds websites by searching.
If a site isn't known yet, they *look it up*: the app finds the feeds the site publishes and shows them on a site page.
They subscribe to feeds, and the app fetches those feeds in the background.
Their home page is a timeline of the articles in their subscriptions, newest arrivals first; opening an article shows its text and marks it read.

## Glossary

- **Site:** a website, identified by its *site key*, the lowercase host without `www.` (`example.com` for `https://www.Example.com/blog`).
- **Feed:** an RSS, Atom or JSON Feed document.
  It belongs to the site it was first discovered from.
- **Lookup:** a background job that finds a site's feeds.
  Its row is also the cached result for that site key.
- **Article:** one item of a feed.
  The same story in two feeds is two articles, *copies* matched by `canonical_url` (the article's URL without tracking parameters).
- **Timeline position** (`timeline_at`): where an article sits in timelines, set once when it's first saved: for *news* (new items on a later save), its published date, but never below the later of the feed's previous fetch and 6 hours back (`rss.NewsWindow`), with news dated earlier spread above that floor and undated news at the save time; for *backlog* (older items, including every item on a feed's first save or on a save after two days without a fetch attempt, where undated ones get spaced-out positions at least a day back), its published date.
- **Reader's position** (`timeline_entries.timeline_at`): where an article sits in one reader's timelines instead of its timeline position, for news that arrived while they were looking and a newly followed feed's recent posts, raised above what they've seen and set once (see "The reader's position" in features.md).
- **Subscription:** a user following a feed.
  Subscriptions decide what's in the user's timeline and which feeds are refreshed in the background.
- **Read:** the user opened the article, or clicked through to the original, or did either for a copy of it.
- **Refresh request:** a feed page view asking the workers to fetch a stale feed soon.

## The process

There are two binaries, `www` (`cmd/www`), the reader, and `admin` (`cmd/admin`), the admin site (below), and one Postgres database.
Inside the `www` process run eleven long-lived components, all stopped by one context (the request metrics by their own, after the server has drained):

- The **HTTP server** (`internal/server`) handles requests and reads and writes the database through the `internal/psql` repositories.
  Its Refresh button calls `ingest.Refresher` directly.
- The **refresh runner** (`internal/refresh`) claims due feeds and hands each to `ingest.Refresher`, which fetches the feed and saves the outcome.
- The **lookup runner** (`internal/lookup`) claims pending lookups, runs `discovery.Discoverer` to find the site's feeds, and saves them with `psql.DiscoveryRepository`.
- The **import runner** (`internal/importer`) claims the feeds of uploaded OPML files one at a time, finds or discovers each like the lookup runner, subscribes the import's user and adds the row's tags.
- The **site icon runner** (`internal/siteicon`) checks sites' home pages for a usable icon and stores it in `site_icons`.
- The **enrich runner** (`internal/enrich`) fetches the web pages of new articles missing an image, title or excerpt, and stores their Open Graph values in the article's `page_*` columns.
- The **mail runner** (`internal/mail`, `mail.Runner`) sends the emails the server queues in the `emails` outbox.
- The **request metrics** (`metrics.Requests`) count the requests the server reports in memory and write them to `request_metrics` once a minute.
- The **job metrics** (`metrics.Jobs`) count the jobs the runners above report, by runner and outcome, in memory and write them to `job_metrics` once a minute.
- The **runtime sampler** (`metrics.Runtime`) writes the process's memory, goroutines, GC pauses and connection pool to `runtime_metrics` once a minute.
- The **server error log** (`metrics.ErrorLog`) writes the 500s and panics the server reports to `server_errors`, one at a time, from a buffer.
- The **metrics housekeeper** (`metrics.Housekeeper`) rolls up and deletes old metrics and real-user views, and deletes all but the newest 1,000 server errors, every hour.

`ingest`, `discovery`, `siteicon` and `enrich` make every outbound request through `internal/fetch`.

`cmd/www/main.go` (`run`) hands `www migrate` and `www seed` to their own functions (below).
Otherwise it parses the configuration (`parseConfig` in `config.go`: each flag, or else its `RSS_` environment variable, set by `cli.SetFlagsFromEnv`, or else its default) and builds the slog logger on stdout (`cli.NewLogger`: JSON in production, text otherwise).
`cmd/www/application.go` (`Application.Run`) wires everything: it opens the database, builds one `fetch.Client` (plus one capped at 256 KiB for icon downloads) and one `ingest.Refresher` (shared by the Refresh button and the workers), the repositories, the scs session manager and the server, then starts the runners next to `Server.Serve`.
On SIGINT or SIGTERM, or when any component fails, the context is canceled: the server drains requests for up to 30 seconds, the workers stop, and only then is the database closed.
Only `cmd/www` and `cmd/admin` handle signals.

`www migrate` (`cmd/www/migrate.go`) applies the migrations embedded in the binary (`migrations.FS`) with `psql.Migrate` and exits.
It runs before new code goes live, never at startup.
`Migrate` applies the migrations the database doesn't have yet, with goose, and never wipes anything; CI refuses a PR that changes a migration already on `main`.

`www seed` (`cmd/www/seed.go`) queues lookups of the seed list's sites (`internal/seed/sites.txt`, embedded) that haven't been found, behind readers', and exits; the app's lookup runner fetches them.
It only writes `lookups` rows, so it's cheap to run on every deploy: production runs it as `www migrate -seed`, its pre-deploy command, and locally it runs only by hand (`make db/seed-sites`), never on `make db/reset`.


`admin` (`cmd/admin/main.go`, `run`) is the admin site's server, `admin` or `admin serve` (`serve.go`), and its command-line tools; an unknown subcommand prints the usage and exits 2.
Its flags are read like `www`'s (`parseConfig` in `cmd/admin/config.go`, through `internal/cli`), and it opens its own small pool on the same database (`-db-max-open-conns`, 3 by default).
Inside it runs one component, the HTTP server (`internal/admin`), on port 4001; it runs none of `www`'s workers.
On SIGINT or SIGTERM the server drains requests for up to 30 seconds, then the database is closed.
It refuses to start in production without `-secret-key`, the key admins' TOTP secrets are sealed with; in development it makes one for the run, with a warning.
`admin enroll` (`cmd/admin/enroll.go`) enrolls an admin, the only way one is made (see "Admin" in features.md).
`admin create-user` (`cmd/admin/create_user.go`) creates a reader's account with the password on the first line of stdin, with the same checks as signup; it's for local development (`make db/seed`).
`admin invite create`, `list` and `revoke` (`cmd/admin/invite.go`) make, list and revoke signup invites; `create` prints the link, built from `-app-url`, the reader's URL (the admin's own `-base-url` is its own host).
`admin send-test-email` (`cmd/admin/send_test_email.go`) renders the `test` email and sends it with `www`'s mail flags (`cli.Mail`, shared with `www`'s configuration), without opening the database, to check the mail settings (locally against Mailpit, in production against Resend).

## Packages

| Package | Role | Depends on |
| --- | --- | --- |
| `cmd/www` | Flags, wiring, signal handling, the `migrate` and `seed` commands | everything below |
| `cmd/admin` | The admin's flags, wiring and signal handling, its `serve`, `enroll`, `create-user` and `send-test-email` commands, and the QR code `enroll` draws | `admin`, `cli`, `mail`, `psql`, `rss`, `ui`, `validator` |
| `internal/admin` | The admin site: router, middleware, handlers, templates, its login (TOTP, sealed secrets, recovery codes) and enrolling | `assets`, `chart`, `clientip`, `ratelimit`, `rss`, `password`, `token`, `validator`, `ui`, `web` |
| `internal/admin/chart` | The admin's charts, line charts and histograms, drawn as SVG on the server (the `chart` template function), and percentiles from histogram buckets | — |
| `internal/cli` | What the commands share: flags from `RSS_` environment variables, the logger, the mail flags and their sender | `mail` |
| `internal/server` | Router, middleware, handlers, templates, rate limits | `rss`, `ui`, `web`, `assets`, `clientip`, `validator`, `sanitize`, `mail`, `metrics`, `token`, `opml`, `discovery`, `ingest`; `feedparse`, `fetch` and `enrich` for the debug inspectors; `psql` for interface checks only |
| `internal/psql` | Repositories: plain SQL, driver errors mapped to `rss` errors; `Migrate` | `rss`, `mail`, `metrics` and `seed` (types), `password` |
| `internal/lookup` | Lookup queue workers | `discovery`, `ingest`, `rss` |
| `internal/seed` | The embedded seed list: parse it and queue its lookups | `discovery` |
| `internal/importer` | OPML import queue workers | `discovery`, `lookup`, `rss` |
| `internal/opml` | Read the feeds listed in an OPML file, with their folders | — |
| `internal/refresh` | Feed refresh workers | `ingest`, `rss` |
| `internal/siteicon` | Site icon workers | `discovery`, `fetch`, `rss` |
| `internal/enrich` | Article enrichment workers | `discovery`, `fetch`, `ingest`, `rss` |
| `internal/ingest` | Fetch a feed, turn items into articles, schedule the next fetch | `fetch`, `feedparse`, `rss` |
| `internal/discovery` | Normalize user input, find a site's feeds, read pages' tags | `fetch`, `feedparse` |
| `internal/feedparse` | Recognize and parse RSS, Atom and JSON Feed (via gofeed) | — |
| `internal/fetch` | The only outbound HTTP client for user-influenced URLs | — |
| `internal/mail` | Render emails, queue them in the outbox, send them over SMTP or to the log | `rss` |
| `internal/metrics` | Count requests and background jobs and sample the runtime once a minute; keep recent server errors; roll up and delete old metrics and real-user views (`RUMBounds`) | `rss` |
| `internal/sanitize` | Make article HTML safe to render (bluemonday) | — |
| `internal/password` | Argon2id hashing, after NFKC normalization | — |
| `internal/token` | Random tokens for share links (`Random`) and email links (`New`, `Hash`) | — |
| `internal/rss` | Domain types (`Feed`, `Article`, `Site`, …) and sentinel errors | — |
| `internal/ui` | Embedded page templates (the reader's and the admin's), email templates and the web app manifest | — |
| `internal/assets` | Read a frontend build's Vite manifest: page groups' tags, files' URLs, serving `/static` | — |
| `internal/clientip` | The client's address behind `-proxy-hops` reverse proxies, and its rate-limit key | — |
| `internal/ratelimit` | Both servers' in-memory fixed-window rate limiters, and the response to a denied request | — |
| `internal/validator` | Form validation helpers and the rules for new passwords | — |
| `migrations` | The goose SQL migrations, embedded (`FS`) | — |
| `web` | The frontend's npm project; its Go package embeds the builds (`App`, what the reader's `/static` serves, and `Admin`, the admin's) | — |

Consumers define the interfaces they need, and only `cmd/www` and `cmd/admin` build the concrete types.
The server's store interfaces (`FeedStore`, `ArticleStore`, …) and the `Services` struct that holds them are in `internal/server/server.go`, with compile-time checks that the `psql` repositories satisfy them.
`lookup`, `importer`, `refresh`, `siteicon`, `enrich`, `ingest` and `discovery` each declare their own small interfaces (`Store`, `Fetcher`, `Saver`, …).
That's what lets every package be tested with fakes (see Testing).

Third-party modules and what they're for (CLAUDE.md asks for a justification before adding another):

| Module | Used for |
| --- | --- |
| `go-chi/chi` | Router: route groups, URL parameters, `GetHead` |
| `alexedwards/scs` (+ `postgresstore`) | Sessions, stored in the `sessions` table |
| `lib/pq` | Postgres driver; `pq.Error` codes are mapped to `rss` errors |
| `pressly/goose` | Applying the migrations (`psql.Migrate`) |
| `go-playground/form` | Decoding POST forms into structs |
| `mmcdole/gofeed` | Parsing RSS, Atom and JSON Feed (`internal/feedparse`) |
| `microcosm-cc/bluemonday` | The HTML allowlist in `internal/sanitize` |
| `golang.org/x/net/html` | Reading HTML pages and rewriting article HTML; its `charset` package decodes OPML files in legacy encodings |
| `golang.org/x/crypto/argon2` | Password hashing |
| `golang.org/x/text/unicode/norm` | NFKC-normalizing passwords |
| `honnef.co/go/tools` | `staticcheck`, as a `go tool` |

The frontend's npm devDependencies (`web/package.json`) are Vite, which builds the JavaScript and CSS, TypeScript, which checks the modules' types (`tsc --noEmit`; Vite strips the types when it builds), Prettier, which formats the sources, and Vitest with happy-dom, which tests the modules; they run only at build time and in CI.
Its one dependency, pinned to an exact version, is the only npm code that reaches the browser: `web-vitals`, which measures the real-user metrics' Core Web Vitals (`rum.ts`, below; why under "Observability" in decisions.md).

## HTTP

**Middleware**, outermost first (`internal/server/router.go`):

1. `logRequest`: one log line per request with status, size and duration.
2. `recordRequest`: counts the request in the request metrics (`Services.Metrics`) by its chi route pattern, after the handlers return; a request that never reached a route is `(unmatched)`: 404s, 405s, and the responses of the middleware before routing (`CrossOriginProtection`'s 403s, canonical-host redirects).
3. `serverTiming`: when an HTML response's headers are written, adds `Server-Timing: app;dur=<ms>`, the server's time so far.
4. `recoverPanic`: a panic becomes the 500 page.
5. `redirectToCanonicalHost`: in production, a request for any host but `-base-url`'s gets a 308 to the same path and query there, except `/healthcheck`, which Render calls on the service's internal address.
6. `http.CrossOriginProtection`: rejects cross-origin non-GET browser requests (`Sec-Fetch-Site` / `Origin`) with the 403 page.
   There are no CSRF tokens.
7. `commonHeaders`: the CSP (see "Security measures"), `nosniff`, `X-Frame-Options: deny`, `Referrer-Policy`, and HSTS in production.
8. `middleware.GetHead`: HEAD is served by the GET handler, upkeep side effects included.

The admin's middleware (`internal/admin/router.go`) is the same chain without `recordRequest`, `serverTiming` and `redirectToCanonicalHost`, and with its own `commonHeaders` (see "Security measures"); `/static/*` and `/healthcheck` need no session; every other route loads it (scs's `LoadAndSave`, then `authenticate`), and all but the login's (`/login`, `/login/code`, `/logout`) need a logged-in admin (`requireAuthentication`), as do destructive actions' recent code (`requireRecentCode`).

Then the reader's routes are split into groups.
`/static/*`, `/manifest.webmanifest`, `/sites/{id}/icon`, `/healthcheck`, `/rum`, `/privacy`, `/terms` and the shared timeline routes have no session.
All other routes load the session (`sessionManager.LoadAndSave`) and `authenticate` (see "Accounts and sessions" in features.md).
Routes in the `requireAuthentication` group redirect anonymous users to `/login` and set `Cache-Control: no-store`.
Routes under `/debug/` are a group inside it with `requireDebug`, which answers 404 unless debug mode is on (see "Debug mode" in features.md).

**Routes** (the handler is the route's name in `router.go`; features.md has the flows):

| Route | Login | What it does |
| --- | --- | --- |
| `GET /healthcheck` | no | JSON status, database reachability, environment and build version, for health checks |
| `POST /rum` | no | The real-user beacon from `rum.ts`: one page view's metrics, stored in `rum_views` under its path's route pattern; always 204 |
| `GET /static/*` | no | The built frontend (`web.App`): hashed files under `assets/` and public files with their current `?v=` are cached for a year, others revalidated; an outdated entry's hashed name gets the current file |
| `GET /manifest.webmanifest` | no | The web app manifest (`manifestHandler`), cached for a day |
| `GET /sites/{id}/icon` | no | A site's stored icon; 404 when it has none |
| `GET /privacy`, `GET /terms` | no | Privacy policy, terms of use |
| `GET /shared/{token}` | no | A shared timeline's landing page |
| `GET /shared/{token}/feed.atom` | no | A shared timeline's Atom feed |
| `GET /` | no | Landing page, with the waitlist form; when logged in, the start tab (the visit's, from the `tab` cookie, else the first) under the tab row; never cached (`no-store`) |
| `GET, POST /signup` | no | Sign up with an invite (`?invite=<token>`); without a usable one, a page saying why |
| `GET, POST /login`, `POST /logout` | no | |
| `GET, POST /confirm-email` | no | A confirmation link's page, and confirming |
| `GET /confirm-email/done` | no | Static "email address confirmed" page for anyone but the confirmed account |
| `GET, POST /email-change` | no | A change-email link's page, and changing |
| `POST /waitlist`, `GET /waitlist/joined` | no | Join the waitlist (a logged-in user is sent to `/`), and "check your email" |
| `GET, POST /waitlist/confirm`, `GET, POST /waitlist/leave` | no | A waitlist link's page, and confirming or leaving |
| `GET, POST /password/forgot`, `GET /password/forgot/sent` | no | Ask for a reset link |
| `GET, POST /password/reset` | no | A reset link's page, and setting the password |
| `GET /.well-known/change-password` | no | 302 to `/settings/password`, for password managers |
| `GET /saved` | yes | Saved articles, with `?q=` search |
| `GET /feeds` | yes | My feeds |
| `GET /feeds/{id}` | yes | Feed page; queues a refresh if the feed is stale |
| `POST /feeds/{id}/refresh`, `/subscribe`, `/unsubscribe` | yes | Refresh now; subscribe; unsubscribe |
| `GET, POST /feeds/{id}/edit` | yes | Edit subscription: name and tags (and Unsubscribe) |
| `GET, POST /tags` | yes | Tags, and making one |
| `GET /tags/{id}`; `POST /tags/{id}/feeds`, `/rename`, `/delete` | yes | A tag's page, and changing it |
| `POST /timelines/order` | yes | The whole tab order, posted by `reorder.ts` after a drag or a move (204) |
| `GET, POST /timelines`, `GET /timelines/new` | yes | Timelines, and making one (`?preset=`, `?tag=`) |
| `GET /timelines/all` | yes | Home at its own address, since `/` may show another tab |
| `GET /timelines/{id}`, `GET, POST /timelines/{id}/edit` | yes | A custom timeline, and editing it |
| `POST /timelines/{id}/delete`, `/move` | yes | Delete; move up or down in tab order (`/timelines/all/move` moves Home) |
| `GET /timelines/{id}/share` | yes | A timeline's share page |
| `POST /timelines/{id}/share`, `/unshare`, `/share/new-link` | yes | Sharing (needs a confirmed email) |
| `POST /timeline/seen` | yes | "You were here" beacon: the newest position seen, and on which timeline (`all` or its ID); 204 |
| `GET /articles/{id}` | yes | Article page; records the read |
| `POST /articles/{id}/read` | yes | Click-through ping; 204 |
| `POST /articles/{id}/save`, `/unsave`, `/star`, `/unstar` | yes | Save and Star buttons |
| `GET /sites/{id}` | yes | A site and its feeds |
| `GET /search` | yes | Search sites and feeds in the database |
| `POST /lookups`, `GET /lookups/{id}` | yes | Queue a site lookup; its status page |
| `GET /settings` | yes | The Settings home: the account and the other categories |
| `GET /settings/account`, `/reading`, `/data`, `/help` | yes | Settings' category pages |
| `POST /settings/reading` | yes | Save the reading preferences (the theme) |
| `GET, POST /settings/name`, `/password`, `/email`, `/delete` | yes | Account's one-form pages |
| `POST /settings/log-out-others`, `/settings/debug` | yes | Log out other devices; turn debug mode on or off |
| `GET /settings/export`, `GET /settings/subscriptions.opml` | yes | OPML export |
| `GET, POST /settings/import`, `GET /settings/imports/{id}` | yes | OPML import, and an import's progress |
| `POST /confirm-email/resend` | yes | Email a new confirmation link |
| `POST /debug/feeds/{id}/inspect`, `/debug/articles/{id}/inspect-page`, `/debug/sites/{id}/discover` | yes | Debug inspectors; save nothing |

**Rendering** (`internal/server/html.go`): at startup every `templates/*.html` page is parsed together with all of `templates/partials/*.html` into one template per page.
`renderHTML` executes into a buffer first, so a template error becomes a clean 500 instead of half a page.
Template functions: `asset` (a static file's URL with a content hash), `avatar`, `avatarTone`, `monogram` and `debugPanel`.
Logged-in pages pass a `navData` (from `s.nav`) as their `Nav` field for the `app-nav` partial.
Errors are HTML pages via `errorHTML` (`errors.go`); JSON is only for `/healthcheck`.

**Timeouts:** the server's `ReadTimeout` is 5 s and `WriteTimeout` 10 s, so handlers that wait on slow work bound it below that: the Refresh button gives a fetch 8 s, and `POST /lookups` waits at most 3 s before redirecting to the status page.
Inspectors are the exception: `extendWriteDeadline` moves their response's write deadline to 35 s from when they start.

## Data model

The schema is `migrations/*.sql` applied in order (`00001_init.sql`, then one migration per change; see CLAUDE.md), and their comments describe the columns.
This section covers what the comments don't: how tables are used together.

```
users ──< subscriptions >── feeds ──< articles ──< reads >── users
                              │          │
                              │          ├──< saves >── users
                              │          ├──< stars >── users
                              │          └──< enrich_jobs
                              │
                            sites ──< lookups (site_id, once found)
                              └──< site_icons

users ──  timeline_marks (one row per user, once reported)
users ──< timeline_lines (one per timeline seen: Home's with no timeline_id) >── timelines
users ──< timeline_entries >── articles (feed_id, no foreign key)
users ──< tags
subscriptions ──< subscription_tags >── tags
users ──< timelines ──< timeline_tags (>── tags) / timeline_feeds (>── subscriptions)
users ──< imports ──< import_feeds (feed_id, once known)
users ──< user_sessions
users ──< user_tokens
waitlist (no foreign keys: an entry is an address, not an account)
waitlist ──< invites >── users   (both nullable: the entry it was sent to, the account made with it)

admins ──< admin_recovery_codes   (apart from users: an admin is never a reader)
admins ──< admin_audit   (admin_id nullable: none for a failed password or a command, and kept, with the email, when the admin is deleted)
admin_sessions (the admin's scs sessions, apart from the reader's sessions)

sessions (scs's table: session data keyed by token, no foreign keys)
emails   (the outbox: no foreign keys)
request_metrics, job_metrics, runtime_metrics, server_errors, rum_views, rum_daily (written by each www instance, read by the admin; no foreign keys)
```

- **Tables that are also queues** keep their claim state in their own columns, and a due time doubles as the claim's lease: `feeds.next_fetch_at`, `sites.icon_next_check_at`, `enrich_jobs.next_attempt_at`, `emails.next_attempt_at`; `lookups` and `import_feeds` use a `status` column instead (see "Background work").
- **Columns marked internal** (`feeds.last_error`, `lookups.error`, `import_feeds.error`, `enrich_jobs.last_error`, `emails.last_error`) are never shown to users, except in debug panels; `server_errors.message` is shown only in the admin.
- **`articles`:** `timeline_at` is set once on insert and never updated.
  The feed's own `image_url`, `title` and `excerpt` are overwritten by every changed fetch; the `page_*` columns are written only by enrichment, so the two never mix.
- **Copies:** reads, saves and stars belong to one article, but every copy (same non-empty `canonical_url`) counts, matched at query time.
  Ingest flags copies (`articles.has_copies`, set by `saveArticles` and never cleared), and timelines check for an earlier copy only on flagged rows.
- **`timeline_marks`** is written only by the timeline's beacon (`MarkSeen`) and read by every timeline page that isn't starred-only (`TimelineLine`), for whether a new visit has started, and at ingest and on subscribe, to set the reader's own positions (`timeline_entries`); its positions compare with the reader's positions (a row's, else `articles.timeline_at`), with no foreign key to an article.
- **`timeline_lines`** holds each timeline's own "You were here" line, one row per timeline the user has had on screen (Home's with a NULL `timeline_id`, unique with `NULLS NOT DISTINCT`): `MarkSeen` upserts the reported timeline's row in the transaction that updates `timeline_marks`, after checking the timeline is the user's, and a new visit moves every row's `divider_at` to its `seen_up_to`; `TimelineLine` reads it joined to the user's `timeline_marks` row.
  Only the line is per timeline: ingest and trimming use `timeline_marks`.
- **`timeline_entries`** holds each reader's own position for news that arrived in a feed they follow while they were looking, and for a feed's recent posts when they follow it: `saveArticles` writes rows for inserted articles, for each follower whose `timeline_marks.seen_at` is within `rss.NewsWindow`, and `SubscriptionRepository.Subscribe` for the feed's articles positioned within `rss.NewsWindow`; both raise the shared `timeline_at` above the reader's `seen_up_to` (`raisePositions`).
  `Subscribe` also writes a row for each of the new feed's copies that would otherwise take an entry of the reader's down to the copy's date (`keepCopiesInPlace`).
  Rows are inserted or deleted, never updated, and a row is never below the article's `timeline_at`, so deleting one is always safe; `feed_id` copies the article's feed so timelines can join a row to the reader's subscription to it.
  `Unsubscribe` keeps the reader's rows for the feed, so following it again finds its posts where they were.
  `MarkSeen` trims a reader's rows at the start of each visit, in the transaction that updates their `timeline_marks` row (see "You were here" in features.md).
  Every timeline that isn't starred-only or shared reads them (`ListTimeline`): an entry is at the reader's row's position when there is one, else at `timeline_at`, and rows of feeds the reader no longer follows are left out by a join with `subscriptions`.
- **Per-user names:** a subscription's `title` is the user's name for the feed (`''` uses the feed's); per-user list queries select it in place of `feeds.title` (`subscriptionTitle`).
- **Ownership by composite foreign keys:** `subscription_tags`, `timeline_tags` and `timeline_feeds` reference `(id, user_id)` pairs, so a subscription can only carry its own user's tags and a timeline can only name its own user's tags and subscriptions, and repositories need no ownership check for them.
  `tags.name` and `users.email` are `citext`, so unique ignoring case.
- **Caps** checked in the repositories: 100 tags (`MaxTags`) and 20 timelines (`MaxTimelines`) per user.
- **Metrics:** each instance writes its own `request_metrics` and `job_metrics` minute rows (named by host name) and `runtime_metrics` rows; the hourly rollup sums every instance's minute rows of an hour into one row per route, method and status class, or per runner and outcome, with `instance` `''`.
  The rollup starts after the newest hourly row and inserts with `ON CONFLICT DO NOTHING`, so every instance runs it and an hour is rolled up once; it only takes whole hours, since an hour that has rows is never rolled up again.
- **Real-user metrics:** `rum_views` gets a row per beacon, with no user, IP address or URL; the daily rollup counts each whole UTC day's views into one `rum_daily` row per route, device, browser and metric, with its views in the metric's `metrics.RUMBounds` buckets, so the 30 days of views and 13 months of days answer the same percentiles.
  Like the hourly rollup, it starts after the newest day rolled up, inserts with `ON CONFLICT DO NOTHING` and takes whole days only; a view counts for each metric it reported.
- **`server_errors`** is capped by count, not age: every instance's housekeeper keeps the newest 1,000 (`Trim`), so a quiet month keeps its errors and a burst can't grow the table past an hour's worth.
- **`waitlist`:** one row per address (`citext`), with one link token's hash that both confirms the entry and leaves the list; joining again while unconfirmed replaces the row's details and hash in the same `INSERT … ON CONFLICT`, and a confirmed row is left alone.
  An entry counts once `confirmed_at` is set; leaving deletes the row.
  Nothing joins it to `users`: the join handler checks for an account by address before inserting, and signing up deletes the account's entry.
- **`invites`:** one row per invite, with its token's hash; `email` is set for a bound invite and must be the signup's address.
  Redeeming is one `UPDATE … WHERE` the invite is unredeemed, unrevoked and unexpired, in the same transaction as the user's insert, so an invite makes at most one account and a failed signup leaves it usable.
  Deleting its waitlist entry or its account clears `waitlist_id` or `user_id` and keeps the row, for the admin's history.
- **No sweepers:** `authenticate` deletes an idle `user_sessions` row when it sees it and login deletes the user's idle rows; `TokenRepository.Create` deletes the user's expired tokens first; the mail runner deletes finished emails after 7 days; scs deletes expired sessions.

Deleting a site cascades to its feeds and their articles, subscriptions, reads, saves and enrich jobs, and to its `site_icons` row, and clears `lookups.site_id` and `import_feeds.feed_id`; deleting an article deletes its saves, stars, reads, enrich job and `timeline_entries` rows; deleting a user cascades to their subscriptions, reads, `timeline_marks` row, `timeline_lines` and `timeline_entries` rows, saves, stars, tags, timelines, imports (and their `import_feeds`), `user_sessions` and `user_tokens`, and leaves their `emails`.
Deleting a subscription (unsubscribing) deletes its name, its `subscription_tags` and its `timeline_feeds` rows, so subscribing again starts clean, except for the reader's `timeline_entries` rows for the feed, which stay until a visit trims them; deleting a tag removes it from subscriptions and timelines; deleting a timeline deletes its `timeline_tags`, `timeline_feeds` and `timeline_lines` rows.
Nothing deletes sites, feeds or articles yet: they stay even when no one subscribes to them anymore.

One table isn't in the migrations: goose's `goose_db_version`, the versions applied.

## Background work

The runners follow the same pattern: N worker goroutines (`-import-workers`, default 6; `-lookup-workers`, `-refresh-workers`, default 2; `-icon-workers`, `-enrich-workers`, `-mail-workers`, default 1; 0 turns a runner off) loop claiming one job at a time from a Postgres table with `FOR UPDATE SKIP LOCKED`, so several instances can run side by side.
When nothing is claimable a worker sleeps for a poll interval.
Each job gets a timeout; its outcome is recorded with a fresh short context, so a job that timed out still records its failure.
On shutdown, lookups, imports, refreshes, icon checks and enrichment jobs record nothing and are retried later; the mail runner instead finishes the email it's sending (within its timeout) and records the result, since an aborted send can still be delivered.
With `-mail-workers 0` the process queues emails without sending them (and logs that at startup); another instance's runner sends them.

| | Lookups (`internal/lookup`) | Imports (`internal/importer`) | Feed refresh (`internal/refresh`) | Site icons (`internal/siteicon`) | Enrichment (`internal/enrich`) | Email (`mail.Runner`) |
| --- | --- | --- | --- | --- | --- | --- |
| Queue | `lookups` rows with `status = 'pending'`, readers' first (`priority` 0), then the seed list's (1) | `import_feeds` rows with `status = 'pending'`, oldest import first, then by position | `feeds` with `next_fetch_at <= now()`, not gone, and subscribed or requested | `sites` with `icon_next_check_at <= now()` that are new (`icon_checked_at IS NULL`) or have a subscribed feed | `enrich_jobs` with `next_attempt_at <= now()` whose article's feed has a subscriber, by `priority` then `next_attempt_at` | `emails` that are pending and due (`next_attempt_at <= now()`) |
| Claim | `ClaimNext`: set `running` | `ClaimNext`: set `running` and count the attempt | `ClaimDue`: move `next_fetch_at` 5 min ahead (the lease); requested feeds first | `ClaimIconCheck`: move `icon_next_check_at` 5 min ahead (the lease) | `ClaimNext`: move `next_attempt_at` 2 min ahead (the lease) and count the attempt | `ClaimNext`: move `next_attempt_at` 2 min ahead (the lease) and count the attempt |
| Poll when idle | 500 ms | 1 s | 15 s | 5 s | 5 s | 10 s, and woken (`Runner.Wake`) when this process queues an email |
| Job timeout | 30 s | 30 s | 30 s | 30 s | 20 s | 30 s (SMTP: 10 s, `SMTPSender.Timeout`) |
| Crashed job | `ResetStale` every minute: running for over 2 min → pending, failed after 3 attempts | `ResetStale` every minute: running for over 2 min → pending, failed (`unreachable`) after 3 attempts | The lease runs out and the feed is due again | The lease runs out and the site is due again | The lease runs out and the job is due again | The lease runs out and the email is due again |
| Work | `discovery.Discoverer`, then `DiscoveryRepository.Save` | `FeedRepository.GetByURL`, else (or if gone) discovery and `DiscoveryRepository.Save`; then `SubscriptionRepository.Subscribe` | `ingest.Refresher.Refresh` | `Runner.check`: home page, `discovery.ParsePage` icons, downloads; then `SaveIcon`, `KeepIcon` or `RemoveIcon` | `Runner.attempt`: the article's page, `discovery.ParsePage`; then `SavePage`, `Retry` or `GiveUp` | `Sender.Send` |
| Metrics (`runner`: outcomes) | `lookup`: `found`, `none` (no feeds), `failed` | `import`: `subscribed`, `already_subscribed`, `failed` (the row's status) | `refresh`: `saved`, `not_modified` (304), `gone` (410), `failed` (a panic too) | `icon`: `saved`, `kept`, `removed`, `failed` (a panic, or an outcome that couldn't be recorded, too) | `enrich`: `saved`, `retry`, `gave_up`; a panic or an outcome that couldn't be recorded is a `retry`, as the lease retries it | `mail`: `sent`, `retry`, `gave_up` |

Retry schedules for feeds, icons and enrichment are in their sections of [features.md](features.md).
A failed email is retried after 1, 2, 4, … minutes (`retryDelay`, capped at an hour) and marked `failed` after 8 attempts.
Delivery is at least once: a crash between sending and recording the result sends the email again.
Every hour the mail runner deletes sent and failed emails older than 7 days (`DeleteFinished`).

Each runner reports every job it finishes to its `Metrics` (a `JobObserver`, `metrics.Jobs` in `www`; nil reports nothing) with its outcome, in the Metrics row above, and how long it took.
A job interrupted by shutdown isn't reported, as it's retried; the Refresh button's fetch isn't either: it's a request, counted with the others.

The metrics components (`internal/metrics`) claim nothing: every instance runs them.
`metrics.Requests` writes at the start of each minute the counts of the minutes that have ended, keyed by the minute each request finished in, and once more on shutdown, after the server has drained its requests and the runners have stopped; a failed write is logged and its counts dropped.
`metrics.Jobs` does the same with the runners' jobs, keyed by the minute each finished in, runner and outcome.
`metrics.Runtime` writes one sample at the end of each minute, stamped with that minute; its GC pause p99 covers the minute.
`metrics.ErrorLog.Record` never blocks a response: it queues the error in a buffer of 100, dropping it with a warning when the buffer is full, and `Run` writes each with a 2-second timeout, logging and dropping one that fails (the database may be why the request failed).
`metrics.Housekeeper` runs at startup and then hourly: it trims `server_errors` to the newest 1,000 (`Trim`), rolls request and job minute rows older than 2 hours into hourly rows (`RollUp`), then deletes minute and runtime rows older than 7 days and hourly rows older than 90 (`DeleteBefore`); if the rollup fails it deletes no metrics.
It does the same for the real-user views, with `psql.RUMRepository`: it rolls each UTC day that ended over an hour ago into daily rows, then deletes views older than 30 days and daily rows older than 13 months, and deletes no views by age if their rollup fails.
Whether or not the rollup worked, it then keeps only the newest 500,000 views (`Trim`), so a flood of beacons can't fill the disk.

## Outbound HTTP

All requests to URLs a user can influence (websites to look up, feeds and the pages they point to, articles' web pages) go through `fetch.Client` (`internal/fetch`):

- only `http` and `https`, no credentials in URLs, at most 5 redirects, each re-checked;
- a dialer check on the resolved IP of every connection, which blocks loopback, private, link-local, multicast and other special ranges and so also defeats DNS rebinding;
- 10 s for the whole request, bodies capped at 5 MiB, no environment proxy;
- a fixed `User-Agent` and `Accept`.

Site icons are downloaded through their own `fetch.Client`, with bodies capped at 256 KiB (`siteicon.MaxBytes`); the home page they're found on goes through the shared one.

Inspectors (debug mode) fetch through the shared `fetch.Client` too (`Services.Fetcher`), synchronously in the request, with a 30 s budget (`inspectTimeout`) and an extended write deadline; they save nothing.

`fetch.Options.AllowPrivate` turns the address check off for tests that use `httptest` servers; `cmd/www` never sets it.

Email goes over SMTP (`internal/mail`) to the configured provider, a fixed host from the configuration that no user can influence, so it doesn't go through `internal/fetch`.

## Security measures

Where each protection lives.
The reasons are in [decisions.md](decisions.md); the rules for writing code that keeps them (GET handlers, article HTML, inline scripts) are in CLAUDE.md.

- **Cross-site requests:** `CrossOriginProtection` in the middleware (see HTTP); there are no CSRF tokens.
- **HTTPS and one host:** in production, `redirectToCanonicalHost` sends every other host (the old `rss.georgerodier.com`, the `onrender.com` URL) to `-base-url`'s, and `commonHeaders` sends `Strict-Transport-Security: max-age=31536000` on the canonical host's responses only, without `includeSubDomains` or preload.
  Render redirects HTTP to HTTPS.
- **Untrusted HTML:** `sanitize.HTML` (see "Reading" in features.md).
  The CSP is `default-src 'self'; img-src 'self' https:; frame-src https://www.youtube-nocookie.com https://player.vimeo.com`: no inline scripts or styles, images from any `https:` host.
  `http:` image URLs would be blocked, so `CardImageURL` and `sanitize.HTML` rewrite them to `https:` when rendering; the database keeps the publisher's URLs.
  YouTube and Vimeo iframes become click-to-play previews; the players load only from the two `frame-src` hosts, sandboxed, with URLs `video-embeds.ts` builds from the validated video ID.
  Every other iframe becomes a link.
- **The admin** (`internal/admin`, `commonHeaders`) sends its own headers on every response: `Content-Security-Policy: default-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store` (static files set their own), `nosniff`, `X-Frame-Options: deny`, and HSTS in production.
  It's on its own origin (`admin.paperround.app`, #573), so `CrossOriginProtection` refuses form posts from the reader's pages, which are same-site but not same-origin.
- **The admin's login** (`internal/admin/login.go`): admins are their own accounts (`admins`), never readers, enrolled only from the command line (`admin enroll`), and log in with a password, then a second factor.
  Both steps give one generic error each ("That email and password don't match an admin.", "That code didn't work."), and the password is verified against a dummy hash for an unknown email, so neither the page nor its timing says which emails are admins'.
  The password step renews the session token and marks the admin pending for 5 minutes; the code step renews it again and logs in.
  Recovery codes (10, `xxxx-xxxx` from 40 random bits each) are stored as SHA-256 hashes and deleted when used.
  `admin_sessions` holds the admin's scs sessions, which end after 30 minutes idle and 12 hours at most; the cookie is `__Host-admin_session` (`Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/`) in production and `admin_session` without `Secure` locally.
  `admin enroll -reset` deletes every admin session, so one on a lost device ends.
  `requireRecentCode` asks for a code again before a destructive action when the session's last one is over 10 minutes old.
- **The admin's audit log** (`admin_audit`; `internal/admin/audit.go`, `internal/psql/audit.go`): every admin action that changes something, from the web or the command line, and every login, failed login and logout, with the admin, the client IP (web), the target and the fields it changed before and after.
  A change and its row are written in one transaction, inside the repository method that makes the change, which takes who did it (`rss.AuditActor`) and records what it changed, so a change is never made without its record; a login isn't completed without its row either.
  The app only inserts rows, and the page shows their values as escaped text.
  A destructive action also needs the target's name typed (`confirmDestructive`), on top of `requireRecentCode`.
- **The admin's second factor** (`internal/admin`): TOTP (RFC 6238, standard library only) in `totp.go`, whose `Verify` accepts one step of drift either way, compares in constant time and never accepts a step at or before the last one used, so each code works once.
  TOTP secrets are stored sealed (`seal.go`: AES-256-GCM, a random nonce prepended to each ciphertext) under a 32-byte key that only the admin service's environment holds, so a database leak alone gives no working secrets.
- **Site icons:** served from our own domain (`siteIconHandler`), so pages never load them from the site.
  Only the raster types the icon runner stores (PNG, JPEG, GIF, ICO) are served, with their stored `Content-Type`, `nosniff`, and `Content-Security-Policy: default-src 'none'; sandbox` in place of the page CSP.
- **Outbound requests:** `internal/fetch` (see Outbound HTTP).
- **Sessions:** scs, stored in Postgres; the token is renewed on login and logout; the reader's cookies are `SameSite=Lax`, and `Secure` with `-env production` (the admin's are above).
- **The `tab` cookie** remembers the tab a reader was on for a visit (`rss.VisitGap`), so `/` can return to it; it's a cookie rather than a session value because sessions are stored in Postgres and it's set on every timeline page. It's `HttpOnly`, `SameSite=Lax`, `Secure` like the session cookie, and checked against the reader's tabs on every use.
  Each login is a `user_sessions` row: it ends after 90 days unused (`sessionIdleTimeout`) and at most a year after login (scs's `Lifetime`), and logging out deletes it.
- **Passwords:** Argon2id with OWASP's minimum parameters (`internal/password`); at most `GOMAXPROCS` hashes run at once and the rest wait.
  `Hash` and `Verify` normalize the password to Unicode NFKC first.
  Every new password (`validator.CheckNewPassword`) is 8 to 256 characters, isn't the account's email and isn't on the common-password list (`internal/validator/common_passwords.txt`: the NCSC's 100,000 most common passwords from Have I Been Pwned, via SecLists, kept to 8 characters or more; lookups are case-insensitive).
- **Email:** `SMTPSender` (`internal/mail`) uses implicit TLS on port 465 and otherwise requires STARTTLS, except to `localhost` or `127.0.0.1` (`requireTLS`), and rejects a line break in the recipient or subject.
  Links in emails are built from `-base-url` (`App.URL`), never from a request's `Host` header.
  With `-env production`, the app refuses to start without its SMTP and mail settings, so one-time links never fall through to `LogSender`, which writes them to the logs.
  An email's bodies are cleared once it's sent or given up on (`MarkSent`, `GiveUp`), and the mail runner logs an email's `id`, never its address or body.
- **Email links:** tokens are 32 random bytes (`internal/token`); `user_tokens` stores only their SHA-256, so a database leak gives no working links.
  A token works once, until it expires, and (except `change_email`) only while the account's email is still the address it was sent to.
  Opening a link shows a page with a button and only the POST uses the token, so mail scanners that open links can't use it.
  The link's page is served with `Referrer-Policy: no-referrer` and `Cache-Control: no-store`, since the token is in its URL.
  Signup invites (`invites.token_hash`) are stored the same way, work once and expire, and their page (`/signup?invite=`) sends the same headers; the form posts the token back in a hidden field.
  Waitlist links follow the same pattern with one difference: the entry's token (`waitlist.token_hash`) doesn't expire and isn't used up, since the leave link must work in every later email; joining again while unconfirmed replaces it.
- **Waitlist bots:** the form has a honeypot field, hidden from people and the keyboard; a request that fills it gets the "check your email" page and nothing is stored or sent, and the rest are capped per IP (`waitlistLimiter`) and by the outbox's per-address cap.
- **Shared timelines:** their routes are public (no session, no login) and reachable only by the share token: 32 random bytes from `token.Random`, stored as is, so the owner's settings can show the link again.
  A malformed token is a 404 before any query.
  The page and feed send `X-Robots-Tag: noindex` (the page also has a robots meta tag) and name no owner; feed entries carry only titles, links, dates, excerpts and each feed's own title.
  Their absolute URLs are built from `-base-url`, never the `Host` header.
  Sharing and New link need a confirmed email.
  Discovery refuses addresses on `-base-url`'s site (`Discoverer.AppSiteKey`), and search and lookups don't offer them, so a shared link looked up or imported never becomes a feed every account can find.
- **Form bodies:** `decodePostForm` caps them at 64 KiB (`maxFormBytes`); over the cap is a 413 and an unparseable body a 400, both HTML error pages from `formErrorResponse`.
- **OPML uploads:** `settingsImportFormHandler` is a multipart form with its own cap: the body is limited to 1 MiB (`maxImportBytes`) plus 64 KiB for the multipart headers, and the file itself to 1 MiB; over either is a 413 that re-renders the form.
  A file may list up to 1,000 feeds (`maxImportFeeds`); the stored filename is its base name, at most 255 bytes.
- **Rate limits** (`internal/ratelimit`, with `clientip.RateLimitKey` for IPs): in-memory fixed windows, per process.

  | Limiter | Key | Limit |
  | --- | --- | --- |
  | `loginLimiter` | client IP (IPv6 by /64) | 20 per 10 min |
  | `loginFailureLimiter` | submitted email | 10 failed logins per 15 min |
  | `signupLimiter` | client IP (IPv6 by /64) | 10 per hour |
  | `waitlistLimiter` | client IP (IPv6 by /64) | 10 waitlist joins per hour |
  | `lookupLimiter` | user | 10 per 10 min |
  | `refreshLimiter` | user | 10 per 10 min |
  | `importLimiter` | user | 5 OPML imports per hour |
  | `passwordChangeLimiter` | user | 10 per 10 min |
  | `confirmResendLimiter` | user | 3 confirmation emails (Resend) per hour |
  | `resetIPLimiter` | client IP (IPv6 by /64) | 10 password reset requests per hour |
  | `resetEmailLimiter` | submitted email | 3 password reset requests per hour |
  | `deleteAccountLimiter` | user | 10 per 10 min |
  | `emailChangeLimiter` | user | 5 email change requests per hour |
  | `rumLimiter` | client IP (IPv6 by /64) | 120 real-user beacons per 10 min; the rest are dropped, still with a 204 |
  | `rumTotalLimiter` | every client together | 600 real-user beacons per minute, checked after `rumLimiter`; the rest are dropped, still with a 204 |
  | admin `loginLimiter` | client IP (IPv6 by /64) | 10 admin login attempts per 15 min, both steps together |
  | admin `codeFailureLimiter` | admin | 5 wrong codes per 15 min |

  The outbox also caps emails per address, across processes: `EmailRepository.Enqueue` queues about 20 to one address (ignoring case) per hour and returns `rss.ErrTooManyEmails` past that, which `sendEmail` logs as a warning.
  It counts, then inserts, so simultaneous requests can slightly exceed 20.

- **Client IP:** `clientip.FromRequest` (`internal/clientip`), which both servers' `clientIP` call, is the only code that reads `RemoteAddr`.
  With `-proxy-hops` N above 0 it reads `X-Forwarded-For` instead, taking the entry N places from the right, and falls back to `RemoteAddr` when the header is missing, has fewer than N entries, or that entry doesn't parse.
  If that entry isn't a public address, each server logs a warning once per process.
- **Internal errors** (the internal columns, Go errors) go to the logs, never to pages, except debug panels.
  `serverErrorHTML` and `serverErrorJSON` also store the errors behind 500s and panics in `server_errors`, by route pattern, never the URL, so no IDs or tokens are kept; only the admin shows them.
- **Real-user beacons** carry no identifiers (see "Observability" in decisions.md): `POST /rum` loads no session and sets no cookie, `rumHandler` keeps the route pattern its path resolves to (the router's `Find`, through the request's route context), dropping the path and any beacon whose path is no page, and of the User-Agent only the browser's family and major version.
  Every other field is a number, cut to its range (outside it, NULL), or one of a fixed set, a host name or a tag or `data-*` name, else empty, so a beacon can't carry free text; `CrossOriginProtection` lets only same-origin pages post it.
  It's capped per client IP (`rumLimiter`), for every client together (`rumTotalLimiter`), and in rows by the housekeeper (`Trim`); why under "Observability" in decisions.md.
- **Debug mode:** only accounts listed in `-debug-emails` can turn it on (empty, the default, turns it off for everyone), and both checks run on every request.
  Its panels show internal details to those accounts only, as escaped text, and only read.

## Frontend

Templates, CSS and JavaScript follow the conventions in CLAUDE.md.
Beyond those:

- **Static files** are built by Vite from the npm project in `web/` (`make assets`; `web/vite.config.ts`) into `web/dist/app`, embedded by package `web` and served at `/static/`.
  The admin's frontend (`web/admin`) is a second build, with the same rules, into `web/dist/admin` (`web/vite.admin.config.ts`; both configs come from `frontend` in `vite.config.ts`, and `npm run build` runs both), served at the admin's `/static/`; it imports `web/shared` and copies the reader's public files, for the logo and favicons.
  Vite bundles and minifies every entry in `app/entries/` and every sheet in `app/styles/` (with the partials it imports, inlined in its order), splits the modules two entries share into chunks (`assets/chunks/`), writes them and the fonts they link under `assets/` with a content hash in their names, rewrites the stylesheets' `url()`s to those names, and lists every built file in `.vite/manifest.json`, with each script's imported chunks and CSS.
  At startup each server reads its build's manifest and hashes every other file (`assets.Load` in `internal/assets`); pages link their stylesheets and scripts through their page group (below), and the `asset` template function links any other source path through the manifest (`shared/fonts/newsreader.woff2` → `/static/assets/newsreader-<hash>.woff2`) and a public file, copied as-is from `web/app/public`, with `?v=<hash>`, so a changed file gets a new URL and can be cached for a year.
  Every response carries an `ETag` of its content; a public file without its current `?v=` gets `Cache-Control: no-cache`, so it's revalidated rather than kept.
  During a deploy Render runs the old and new versions side by side, so a request for an outdated hashed name of an entry or a sheet (`assets/reading-<old hash>.js`, `assets/base-<old hash>.css`) gets the current file, uncached; any other missing file is a 404.
  That includes an outdated chunk (`assets/chunks/…`): only a page that loaded an old entry from its cache in that window misses it, and it loses its enhancements but still works without them.
  Besides the CSS and JavaScript, they're the logo and favicons (`web/app/public/img/`) and the self-hosted fonts (`web/shared/fonts/`: Bricolage Grotesque for headlines and Newsreader for reading, with their licences); every page links the favicons through `partials/head.html`.
  The fonts load with `font-display: swap`, so text first shows in a size-matched fallback face (`"Bricolage Grotesque Fallback"`, `"Newsreader Fallback"` in `web/shared/styles/fonts.css`: local Arial and Georgia scaled to the web fonts' widths and line metrics, named right after the web font in `--font-display` and `--font-reading`), and the swap barely moves it; the comment above them says how their numbers are computed.
  So the fonts don't wait for the stylesheet, `app-head` preloads Bricolage on every page and the `reading-font` partial preloads Newsreader (never the italic) on the pages with reading text on their first screen; both preload the font's built URL through `asset`, the one `base.css`'s `url()` names (`TestFontPreloadsMatchCSS`), as otherwise it downloads twice.
  The landing page and Log in draw the logo inline instead (`partials/logo.html`), so its sun can rise when they load; change it together with `img/logo.svg` (`TestInlineLogoMatchesFile` checks their shapes match).
  The install icons (`img/icon-192.png`, `img/icon-512.png`, `img/apple-touch-icon.png`) are rendered from `img/app-icon.svg`, whose comment says how to regenerate them.
- **The web app manifest** (`ui/manifest.webmanifest`) is embedded and served by its own handler, not from `/static`: Go's `mime` table has no `.webmanifest` type and the distroless image has no `/etc/mime.types`, so the file server would sniff it as `text/plain`.
  Pages are standalone, so each includes `app-head` (`partials/head.html`: icons, manifest link, `theme-color`); a test fails for a page without it.
  The manifest and the `theme-color` metas repeat `--bg`, as they can't read CSS custom properties: the manifest the light one, and `app-head` the user's theme's on logged-in pages (they pass it their navData), else a light and dark pair with `prefers-color-scheme` media queries.
- **Page groups** (#547): each page loads only the CSS and JavaScript of its group, named once in its head, `{{template "assets" "<group>"}}` (`partials/head.html`).
  A group is a sheet, `web/app/styles/<group>.css`, an entry, `web/app/entries/<group>.ts`, or both; an entry imports the modules its pages use and sets them up, and can import another group's sheet, which Vite lists as its CSS.
  The groups: `reading` (timelines, Saved, feeds, sites, articles, the debug inspectors), `search` (Search, a lookup), `settings` (the Settings pages with no enhancement of their own, My feeds, tags, a subscription's edit page, New timeline and a timeline's share page), `timelines` (the Timelines page and the timeline form, with `settings.css`), `account` (the Settings pages with password fields, Import and an import's progress, with `settings.css`), `solo` (logged-out pages, errors, the policies, a shared timeline) and `login` (Log in, Sign up and Reset password, with `solo.css`).
  Every group has an entry, since every page sends the real-user beacon: `settings` and `solo` only set up `rum.ts`, and every entry sets it up first.
  `assetTags` (`internal/server/static.go`, through `Assets.Tags` in `internal/assets`), built once per group at startup from the manifest, writes the head's tags in this order: stylesheets (`base.css`, the group's sheet, then the CSS the entry and its chunks list), a `<link rel="modulepreload">` for each chunk the entry imports, recursively (so they download alongside it rather than after it), then the entry's `<script type="module">`, each file once.
  Everything loads at once from the head: nothing is imported when a feature's markup is found, as an enhancement would then arrive a round trip late.
  An unknown group is an error, which fails the page's template; `TestPagesNameOneGroup` checks every page names one.
  `web/build_test.go` (`TestReadingBundleBudget`) holds the reading pages' JavaScript and CSS, gzipped, to a budget.
- **Stylesheets:** `web/app/styles/base.css`, on every page, imports the shared partials (`web/shared/styles`: the layer order, reset, fonts, tokens, and base elements, buttons, forms and messages), then the shell and avatars; each other sheet in `web/app/styles` imports the reader's partials (`web/app/styles/partials`, one per area) its pages use.
  Every partial wraps its rules in their cascade layer, and the partials have one order (in `base.css`'s comment), which every sheet imports them in, with `press.css` last: `base.css` followed by a group's sheet is the same cascade as one stylesheet of every partial would be, for the rules that match its pages, so a rule only moves with the partial it's in.
- **Press states** (#510): the last partial, `press.css`, holds the `:active` styles that give touch, mouse and keyboard presses feedback, since hover is limited to devices that hover.
  Colors apply always; the scales sit in the `prefers-reduced-motion: no-preference` block.
  The browser's tap highlight is turned off.
- **Themes:** `tokens.css` (`web/shared/styles`) defines the color tokens on `:root` (light), then the dark values twice with the same content: under `@media (prefers-color-scheme: dark)` for `:root:not([data-theme="light"])`, and for `:root[data-theme="dark"]`.
  A logged-in page's `<html>` gets `data-theme="light"` or `"dark"` from the user's choice in Settings (`users.theme`, loaded with the session): each page opens with `<html lang="en"{{template "html-theme" .Nav}}>`, which writes `navData.Theme()`, nothing for System, and includes `{{template "app-head" .Nav}}`; a test fails for a page with the nav but without them.
  The dark values are written twice (system and chosen), and `web/css_test.go` fails if the two differ or a color token has no dark value.
  Logged-out and error pages have no user, so they always follow the system.
- **Motion:** the easing curves and durations are tokens on `:root` in `tokens.css`, next to the colors, and every animation and moving transition is written inside `@media (prefers-reduced-motion: no-preference)`; `web/css_test.go` (`TestMotionIsOptIn`, over every partial) fails on motion outside it.
- **View transitions** (#513): navigating from page to page crossfades (`@view-transition` in `shell.css`, inside the reduced-motion opt-in), for links and form posts that redirect, but not reloads.
  The crossfade is the browser's own, which blends the two pages so that whatever is the same on both, the nav included, stays still; `shell.css` only sets its timing on `::view-transition-group(root)`, which the images inherit.
  Only the tab row's underline has a `view-transition-name` (see the tab row below): a named element is drawn from a snapshot during the transition, and the frosted bars' snapshots don't match them live, so they'd jump at the start and end.
  Back and Forward cut: `back-forward.ts` skips the transition for traverse navigations (see below), so it never plays after a browser's own swipe-back animation.
  Browsers without cross-document view transitions, and readers who ask for less motion, navigate with a plain cut; `html` reserves the scrollbar gutter so the column doesn't shift between short and long pages.
- **The nav** (`partials/nav.html`, `app-nav`) has four section links (Home, Saved, My feeds, Search) and an account entry (the user's letter avatar, labelled Settings).
  Wide screens put it in a rail, medium screens show icons only, phones put it in a top bar.
  Log out is a button on the Settings home.
- **Avatars:** sites and feeds have rounded-square avatars (`partials/avatar.html`): the site's icon on a white tile, else its letter on its tint.
  People's avatars (the nav, Settings) stay circles.
- **The tab row** (`partials/timeline_tabs.html`) heads Home and each custom timeline in place of the page's visible heading (the `<h1>` is visually hidden).
  It's a `nav` of links with `aria-current`, not ARIA tabs, and shows no counts.
  The current tab's underline is its own element, `.tab-indicator` (`aria-hidden`), named `tab-indicator` for view transitions (#521): between two tab pages the browser moves it from the old tab to the new one apart from the page's crossfade, and to or from a page without the row it fades with the page.
  `timeline-tabs.css` moves it on `--ease-out` and stretches and squashes the new snapshot about its middle (`tab-indicator-stretch`).
  When the script (`timeline-tabs.ts`) has loaded before the new page's first render (it's deferred, so on a slow device it may not have), its `pagereveal` listener replaces that with an elastic move: two springs, one per edge, as keyframes on `::view-transition-group(tab-indicator)`.
  The row starts scrolled just enough to show the current tab before the first render, with no script (`scroll-initial-target` and `scroll-snap-align: end` on it, the row's scroll padding keeping it clear of the fade), so a transition never captures the new tab out of view.
  `timeline-tabs.ts` puts the row back where the reader left it on the previous tab page (its `scrollLeft`, saved to `sessionStorage` in `pageswap` for the URL being opened), but only before the row is first seen (in `pagereveal` during a transition, else when nothing has been painted), and scrolls the current tab into view where `scroll-initial-target` isn't supported; on the page being left it unnames an underline scrolled out of the row (`data-unnamed`), whose unclipped snapshot would slide in from outside it.
- **The enhancements** are TypeScript modules, one per enhancement, set up in order by the entries of the page groups whose pages use them (`web/app/entries`, above); those another site could reuse are in `web/shared/lib`, the reader's in `web/app/features`.
  Each exports a setup function (`init`, or one that takes a root, like `localizeTimes`, for rows added later) and runs nothing on import.
  There are seventeen, each hooked onto server-rendered markup or page events: the real-user metrics (`rum.ts`, `setupRUM`, on every page: `web-vitals` reports every change to LCP, INP, CLS, FCP and TTFB, and the first time the page is hidden it sends them with the navigation entry's timings and coarse context to `POST /rum` with `navigator.sendBeacon`, once per view, a page restored from the back/forward cache being a new one; see "Observability" in features.md), local times (`local-times.ts`, `<time data-local>`), read pings (`read-pings.ts`, `<a ping>` → `navigator.sendBeacon`), Back and Forward without a view transition (`back-forward.ts`; the page being left skips it in `pageswap` when `activation.navigationType` is `traverse`, which runs whenever the script has loaded, so it needn't block rendering; where there's no activation, a page restored from the back/forward cache skips it in `pagereveal`), Save and Star buttons (`save-star.ts`, `form[data-toggle]`, posted with `fetch`; success is a `204`, and on any failure the form submits normally; `aria-disabled` while the request runs, dimmed by `data-busy` only after `--delay-busy`, and `data-just-toggled` on a button just turned on, which `rows.css` animates), Share buttons (`share.ts`, `button[data-share-url]`, rendered hidden and shown by the script; the share sheet, else the clipboard), busy forms (`busy-forms.ts`, `form[data-busy]`: the slow forms post normally, and their button is `aria-disabled` at once, takes its `data-busy-label` and ignores further submits, then shows a spinner and announces the label after `--delay-busy`; restored on `pageshow` from the back/forward cache), video embeds (`video-embeds.ts`, `figure.embed[data-embed]`), the "You were here" beacon (`seen-beacon.ts`, `.article-list[data-seen-up-to]` → `navigator.sendBeacon` when the page is first shown and each time it's hidden), "You were here" drawing itself in (`divider-draw.ts`, `.timeline-divider`: an `IntersectionObserver` sets `data-draw` from `pending` to `play` the first time half of it is in view, unless reduced motion is on), infinite scroll (`infinite-scroll.ts`, `.pager a[rel=next]`, appending rows and the `.timeline-divider`), live search (`live-search.ts`, `form[data-live-search]`: as the reader types, it fetches `/search?q=…` and swaps in its `[data-search-results]`), lookup and import status (`lookup-status.ts` and `import-status.ts`, `[data-lookup-poll]` and `[data-import-poll]`, sharing `pollSelf` from `poll-self.ts`: the page fetches itself every 2 s while visible, with the meta refresh in `<noscript>`; the lookup page follows a redirect to the site page with `location.replace`, and swaps in and announces a failure or "No feeds"; the import page inserts new rows and groups by `data-position` without replacing any, updates its progress line silently, and announces only the finish), the tab row (`timeline-tabs.ts`: its scroll, and the underline's move between tabs, above), the timeline form's live summary and feed filter boxes (`timeline-form.ts`, `form[data-timeline-form]`), reordering on `/timelines` (`reorder.ts`, `ul[data-reorder]`: drag handles and in-place move buttons, posting the order to `/timelines/order`; rows glide into place on springs with the Web Animations API, not a view transition, and a drag scrolls the page near its edges), and a show/hide toggle on every password input (`password-toggle.ts`).
  The polite live region they announce in is `announce.ts`'s, and `--delay-busy` is read once by `busy-delay.ts`.
  Each module's behavior is tested in its `*.test.ts` (see Testing); `internal/server/scroll_test.go` pins the markup the modules rely on, and `search_test.go` (`TestLiveSearchMarkup`) live search's.

## Testing

Server and DB test conventions are in CLAUDE.md.
The other packages test against fakes of their own interfaces, and use `httptest` servers with `fetch.Options{AllowPrivate: true}` where real HTTP matters (`fetch`, `discovery`, `ingest`).
The frontend's modules are tested with Vitest in happy-dom (`make test/web`), each in a `*.test.ts` next to it, through the markup hooks the server renders, with `fetch`, timers and the browser APIs happy-dom lacks (`sendBeacon`, the Clipboard API) faked; `isolate()` in `web/shared/test/setup.ts` gives each test fresh modules and removes the listeners it added.
happy-dom has no layout, view transitions, Navigation API or Web Animations, so the tab underline's move, dragging and the page transitions are checked by hand, and only their springs (`spring`, `at`) are tested.
Where a module mirrors Go, Go is the spec: `TestTimelineSummaryFixture` writes the timeline form's summary for a table of form states to `web/app/features/testdata/timeline-summary.json` (with `-update`; it fails when the file is stale), and `timeline-form.test.ts` checks `timeline-form.ts` gives the same.
`tools/loadseed` (`make db/loadseed`, dev only, not in the `www` binary) seeds a development database at realistic scale and times the `psql` reading methods themselves, printing their plans from `auto_explain`, on a connection set up as the app's (`psql.NewConnector`).

## Deployment

Production runs on Render since #214.
Why this setup is under "Deployment" in [decisions.md](decisions.md).
What runs where, and how to operate it, is in [`infra/README.md`](../infra/README.md) (the runbook) and the Terraform in `infra/`; this section is the overview.

- **Images.** The `Dockerfile` builds the frontend in a Node stage (the version in `web/.node-version`), then `www` and `admin` as static binaries, embedding it (the image's entrypoint is `/www`; the admin's service will run `/admin`, #573), into `gcr.io/distroless/static-debian12:nonroot`, with the commit SHA as `version` (reported by `/healthcheck`); the image has no Node.
  `deploy/backup` is the nightly backup's image: `pg_dump` streamed to the R2 bucket `rss-backups`.
  CI's `image` job builds both on every event, and on `main` pushes them to GHCR (`:sha-<sha>` and `:main`) with build provenance; PRs never push.
- **Infrastructure** (`infra/`, one Terraform root module, state in the R2 bucket `rss-tfstate`): a Postgres 17 database reachable only from Render's private network, the `rss` web service (pre-deploy `/www migrate -seed`, health check `/healthcheck`: Render reads its status code, which is always 200, and the uptime monitor reads its `status`), the `rss-backup` cron job, the backup bucket's lifecycle rules, and the app's DNS records in Cloudflare.
  Values that change outside Terraform (the images, the debug accounts, the SMTP password) are in `ignore_changes`.
- **Workflows** (`.github/workflows`): `ci.yml` (checks, images, then `deploy.yml` on `main`); `deploy.yml` deploys an image by digest and checks `/healthcheck` reports its SHA, then deploys the backup image to the `rss-backup` cron job by digest (also run by hand to roll back); `infra.yml` plans `infra/` changes on PRs and applies them on `main` after the owner approves.
  All Render, R2 and Cloudflare values are repository secrets, read only on `main`, by hand, or on PRs from branches in this repository; none uses `pull_request_target`.
- **Edge** (#541): Render's Cloudflare edge terminates TLS and serves HTTP/2 and HTTP/3 (`h3` seen in DevTools), compresses responses (Brotli, when the client accepts it), and caches `/static` files ("Common static files", set in Render's dashboard), purging them on every successful deploy.
  Pages and `/sites/{id}/icon` aren't cached there.
  Checks and the setting: "Edge caching" in [`infra/README.md`](../infra/README.md).
- **Dependabot** (`.github/dependabot.yml`) proposes weekly updates for Go modules, the frontend's npm packages (Vite and Vitest in one PR), GitHub Actions, the base images and the Terraform providers.
