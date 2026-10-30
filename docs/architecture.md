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
- **Timeline position** (`timeline_at`): where an article sits in timelines, set once when it's first saved: the save time for *news*, its published date for *backlog* (older items, including every dated item on a feed's first save).
- **Subscription:** a user following a feed.
  Subscriptions decide what's in the user's timeline and which feeds are refreshed in the background.
- **Read:** the user opened the article, or clicked through to the original, or did either for a copy of it.
- **Refresh request:** a feed page view asking the workers to fetch a stale feed soon.

## The process

There is one binary, `www` (`cmd/www`), and one Postgres database.
Inside the process run seven long-lived components, all stopped by one context:

- The **HTTP server** (`internal/server`) handles requests and reads and writes the database through the `internal/psql` repositories.
  Its Refresh button calls `ingest.Refresher` directly.
- The **refresh runner** (`internal/refresh`) claims due feeds and hands each to `ingest.Refresher`, which fetches the feed and saves the outcome.
- The **lookup runner** (`internal/lookup`) claims pending lookups, runs `discovery.Discoverer` to find the site's feeds, and saves them with `psql.DiscoveryRepository`.
- The **import runner** (`internal/importer`) claims the feeds of uploaded OPML files one at a time, finds or discovers each like the lookup runner, subscribes the import's user and adds the row's tags.
- The **site icon runner** (`internal/siteicon`) checks sites' home pages for a usable icon and stores it in `site_icons`.
- The **enrich runner** (`internal/enrich`) fetches the web pages of new articles missing an image, title or excerpt, and stores their Open Graph values in the article's `page_*` columns.
- The **mail runner** (`internal/mail`, `mail.Runner`) sends the emails the server queues in the `emails` outbox.

`ingest`, `discovery`, `siteicon` and `enrich` make every outbound request through `internal/fetch`.

`cmd/www/main.go` (`run`) hands `www migrate`, `www create-user` and `www send-test-email` to their own functions (below).
Otherwise it parses the configuration (`parseConfig` in `config.go`: each flag, or else its `RSS_` environment variable, or else its default) and builds the slog logger on stdout (JSON in production, text otherwise).
`cmd/www/application.go` (`Application.Run`) wires everything: it opens the database, builds one `fetch.Client` (plus one capped at 256 KiB for icon downloads) and one `ingest.Refresher` (shared by the Refresh button and the workers), the repositories, the scs session manager and the server, then starts the runners next to `Server.Serve`.
On SIGINT or SIGTERM, or when any component fails, the context is canceled: the server drains requests for up to 30 seconds, the workers stop, and only then is the database closed.
Only `cmd/www` handles signals.

`www migrate` (`cmd/www/migrate.go`) applies the migrations embedded in the binary (`migrations.FS`) with `psql.Migrate` and exits.
It runs before new code goes live, never at startup.
While pre-alpha, `Migrate` compares `psql.Fingerprint` (a SHA-256 of the names and contents of `migrations/*.sql`) with the one stored in `schema_fingerprint` when it last ran: if migrations were applied from different files, it refuses, unless `-allow-reset-to` is the new fingerprint, in which case it drops and recreates the current schema first.
So any edit to a migration, comments included, needs a reset.

`www create-user` (`cmd/www/create_user.go`) creates an account with the password on the first line of stdin, with the same checks as signup; it's for local development (`make db/seed`).
`www send-test-email` (`cmd/www/send_test_email.go`) renders the `test` email and sends it with the configured sender, without opening the database, to check the mail settings (locally against Mailpit, in production against Resend).

## Packages

| Package | Role | Depends on |
| --- | --- | --- |
| `cmd/www` | Flags, wiring, signal handling, the `migrate`, `create-user` and `send-test-email` commands | everything below |
| `internal/server` | Router, middleware, handlers, templates, rate limits | `rss`, `ui`, `validator`, `sanitize`, `mail`, `token`, `opml`, `discovery`, `ingest`; `feedparse`, `fetch` and `enrich` for the debug inspectors; `psql` for interface checks only |
| `internal/psql` | Repositories: plain SQL, driver errors mapped to `rss` errors; `Migrate` | `rss`, `mail` (types), `password` |
| `internal/lookup` | Lookup queue workers | `discovery`, `ingest`, `rss` |
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
| `internal/sanitize` | Make article HTML safe to render (bluemonday) | — |
| `internal/password` | Argon2id hashing, after NFKC normalization | — |
| `internal/token` | Random tokens for share links (`Random`) and email links (`New`, `Hash`) | — |
| `internal/rss` | Domain types (`Feed`, `Article`, `Site`, …) and sentinel errors | — |
| `internal/ui` | Embedded page templates, email templates and static files | — |
| `internal/validator` | Form validation helpers and the rules for new passwords | — |
| `migrations` | The goose SQL migrations, embedded (`FS`) | — |

Consumers define the interfaces they need, and only `cmd/www` builds the concrete types.
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

## HTTP

**Middleware**, outermost first (`internal/server/router.go`):

1. `logRequest`: one log line per request with status, size and duration.
2. `recoverPanic`: a panic becomes the 500 page.
3. `redirectToCanonicalHost`: in production, a request for any host but `-base-url`'s gets a 308 to the same path and query there, except `/healthcheck`, which Render calls on the service's internal address.
4. `http.CrossOriginProtection`: rejects cross-origin non-GET browser requests (`Sec-Fetch-Site` / `Origin`) with the 403 page.
   There are no CSRF tokens.
5. `commonHeaders`: the CSP (see "Security measures"), `nosniff`, `X-Frame-Options: deny`, `Referrer-Policy`, and HSTS in production.
6. `middleware.GetHead`: HEAD is served by the GET handler, upkeep side effects included.

Then routes are split into groups.
`/static/*`, `/manifest.webmanifest`, `/sites/{id}/icon`, `/healthcheck`, `/privacy`, `/terms` and the shared timeline routes have no session.
All other routes load the session (`sessionManager.LoadAndSave`) and `authenticate` (see "Accounts and sessions" in features.md).
Routes in the `requireAuthentication` group redirect anonymous users to `/login` and set `Cache-Control: no-store`.
Routes under `/debug/` are a group inside it with `requireDebug`, which answers 404 unless debug mode is on (see "Debug mode" in features.md).

**Routes** (the handler is the route's name in `router.go`; features.md has the flows):

| Route | Login | What it does |
| --- | --- | --- |
| `GET /healthcheck` | no | JSON status, database reachability, environment and build version, for health checks |
| `GET /static/*` | no | Embedded CSS, JS, images and fonts; `?v=<hash>` URLs are cached for a year |
| `GET /manifest.webmanifest` | no | The web app manifest (`manifestHandler`), cached for a day |
| `GET /sites/{id}/icon` | no | A site's stored icon; 404 when it has none |
| `GET /privacy`, `GET /terms` | no | Privacy policy, terms of use |
| `GET /shared/{token}` | no | A shared timeline's landing page |
| `GET /shared/{token}/feed.atom` | no | A shared timeline's Atom feed |
| `GET /` | no | Landing page; when logged in, Home under the tab row |
| `GET, POST /signup` | no | Only registered with `-invite-code` |
| `GET, POST /login`, `POST /logout` | no | |
| `GET, POST /confirm-email` | no | A confirmation link's page, and confirming |
| `GET, POST /email-change` | no | A change-email link's page, and changing |
| `GET, POST /password/forgot`, `GET /password/forgot/sent` | no | Ask for a reset link |
| `GET, POST /password/reset` | no | A reset link's page, and setting the password |
| `GET /.well-known/change-password` | no | 302 to `/settings/password`, for password managers |
| `GET /saved` | yes | Saved articles, with `?q=` search |
| `GET /feeds` | yes | My feeds |
| `GET /feeds/{id}` | yes | Feed page; queues a refresh if the feed is stale |
| `POST /feeds/{id}/refresh`, `/subscribe`, `/unsubscribe` | yes | Refresh now; subscribe; unsubscribe |
| `GET, POST /feeds/{id}/edit` | yes | Edit subscription: name and tags |
| `GET, POST /tags` | yes | Tags, and making one |
| `GET /tags/{id}`; `POST /tags/{id}/feeds`, `/rename`, `/delete` | yes | A tag's page, and changing it |
| `GET, POST /timelines`, `GET /timelines/new` | yes | Timelines, and making one (`?preset=`, `?tag=`) |
| `GET /timelines/{id}`, `GET, POST /timelines/{id}/edit` | yes | A custom timeline, and editing it |
| `POST /timelines/{id}/delete`, `/move` | yes | Delete; move up or down in tab order |
| `POST /timelines/{id}/share`, `/unshare`, `/share/new-link` | yes | Sharing (needs a confirmed email) |
| `POST /timeline/seen` | yes | "You were here" beacon: the newest timeline position seen; 204 |
| `GET /articles/{id}` | yes | Article page; records the read |
| `POST /articles/{id}/read` | yes | Click-through ping; 204 |
| `POST /articles/{id}/save`, `/unsave`, `/star`, `/unstar` | yes | Save and Star buttons |
| `GET /sites/{id}` | yes | A site and its feeds |
| `GET /search` | yes | Search sites and feeds in the database |
| `POST /lookups`, `GET /lookups/{id}` | yes | Queue a site lookup; its status page |
| `GET /settings` and its pages (`/name`, `/theme`, `/password`, `/email`, `/delete`) | yes | Settings |
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

The whole schema is `migrations/00001_init.sql` (edited in place while pre-alpha; see CLAUDE.md), and its comments describe the columns.
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
users ──< tags
subscriptions ──< subscription_tags >── tags
users ──< timelines ──< timeline_tags (>── tags) / timeline_feeds (>── subscriptions)
users ──< imports ──< import_feeds (feed_id, once known)
users ──< user_sessions
users ──< user_tokens

sessions (scs's table: session data keyed by token, no foreign keys)
emails   (the outbox: no foreign keys)
```

- **Tables that are also queues** keep their claim state in their own columns, and a due time doubles as the claim's lease: `feeds.next_fetch_at`, `sites.icon_next_check_at`, `enrich_jobs.next_attempt_at`, `emails.next_attempt_at`; `lookups` and `import_feeds` use a `status` column instead (see "Background work").
- **Columns marked internal** (`feeds.last_error`, `lookups.error`, `import_feeds.error`, `enrich_jobs.last_error`, `emails.last_error`) are never shown to users, except in debug panels.
- **`articles`:** `timeline_at` is set once on insert and never updated.
  The feed's own `image_url`, `title` and `excerpt` are overwritten by every changed fetch; the `page_*` columns are written only by enrichment, so the two never mix.
- **Copies:** reads, saves and stars belong to one article, but every copy (same non-empty `canonical_url`) counts, matched at query time.
  Ingest flags copies (`articles.has_copies`, set by `saveArticles` and never cleared), and timelines check for an earlier copy only on flagged rows.
- **`timeline_marks`** is written only by the timeline's beacon (`MarkSeen`) and read by every timeline page that isn't starred-only (`TimelineMark`), to place the "You were here" divider; its positions compare with `articles.timeline_at`, with no foreign key to an article.
- **Per-user names:** a subscription's `title` is the user's name for the feed (`''` uses the feed's); per-user list queries select it in place of `feeds.title` (`subscriptionTitle`).
- **Ownership by composite foreign keys:** `subscription_tags`, `timeline_tags` and `timeline_feeds` reference `(id, user_id)` pairs, so a subscription can only carry its own user's tags and a timeline can only name its own user's tags and subscriptions, and repositories need no ownership check for them.
  `tags.name` and `users.email` are `citext`, so unique ignoring case.
- **Caps** checked in the repositories: 100 tags (`MaxTags`) and 20 timelines (`MaxTimelines`) per user.
- **No sweepers:** `authenticate` deletes an idle `user_sessions` row when it sees it and login deletes the user's idle rows; `TokenRepository.Create` deletes the user's expired tokens first; the mail runner deletes finished emails after 7 days; scs deletes expired sessions.

Deleting a site cascades to its feeds and their articles, subscriptions, reads, saves and enrich jobs, and to its `site_icons` row, and clears `lookups.site_id` and `import_feeds.feed_id`; deleting an article deletes its saves, stars, reads and enrich job; deleting a user cascades to their subscriptions, reads, `timeline_marks` row, saves, stars, tags, timelines, imports (and their `import_feeds`), `user_sessions` and `user_tokens`, and leaves their `emails`.
Deleting a subscription (unsubscribing) deletes its name, its `subscription_tags` and its `timeline_feeds` rows, so subscribing again starts clean; deleting a tag removes it from subscriptions and timelines.
Nothing deletes sites, feeds or articles yet: they stay even when no one subscribes to them anymore.

Two tables aren't in `00001_init.sql`: goose's `goose_db_version`, and `schema_fingerprint` (one row: the fingerprint of the migrations last applied), which `psql.Migrate` creates for the pre-alpha check (see "The process").

## Background work

The runners follow the same pattern: N worker goroutines (`-lookup-workers`, `-import-workers`, `-refresh-workers`, default 2; `-icon-workers`, `-enrich-workers`, `-mail-workers`, default 1; 0 turns a runner off) loop claiming one job at a time from a Postgres table with `FOR UPDATE SKIP LOCKED`, so several instances can run side by side.
When nothing is claimable a worker sleeps for a poll interval.
Each job gets a timeout; its outcome is recorded with a fresh short context, so a job that timed out still records its failure.
On shutdown, lookups, imports, refreshes, icon checks and enrichment jobs record nothing and are retried later; the mail runner instead finishes the email it's sending (within its timeout) and records the result, since an aborted send can still be delivered.
With `-mail-workers 0` the process queues emails without sending them (and logs that at startup); another instance's runner sends them.

| | Lookups (`internal/lookup`) | Imports (`internal/importer`) | Feed refresh (`internal/refresh`) | Site icons (`internal/siteicon`) | Enrichment (`internal/enrich`) | Email (`mail.Runner`) |
| --- | --- | --- | --- | --- | --- | --- |
| Queue | `lookups` rows with `status = 'pending'` | `import_feeds` rows with `status = 'pending'`, oldest import first, then by position | `feeds` with `next_fetch_at <= now()`, not gone, and subscribed or requested | `sites` with `icon_next_check_at <= now()` that are new (`icon_checked_at IS NULL`) or have a subscribed feed | `enrich_jobs` with `next_attempt_at <= now()` whose article's feed has a subscriber, by `priority` then `next_attempt_at` | `emails` that are pending and due (`next_attempt_at <= now()`) |
| Claim | `ClaimNext`: set `running` | `ClaimNext`: set `running` and count the attempt | `ClaimDue`: move `next_fetch_at` 5 min ahead (the lease); requested feeds first | `ClaimIconCheck`: move `icon_next_check_at` 5 min ahead (the lease) | `ClaimNext`: move `next_attempt_at` 2 min ahead (the lease) and count the attempt | `ClaimNext`: move `next_attempt_at` 2 min ahead (the lease) and count the attempt |
| Poll when idle | 500 ms | 1 s | 15 s | 5 s | 5 s | 10 s, and woken (`Runner.Wake`) when this process queues an email |
| Job timeout | 30 s | 30 s | 30 s | 30 s | 20 s | 30 s (SMTP: 10 s, `SMTPSender.Timeout`) |
| Crashed job | `ResetStale` every minute: running for over 2 min → pending, failed after 3 attempts | `ResetStale` every minute: running for over 2 min → pending, failed (`unreachable`) after 3 attempts | The lease runs out and the feed is due again | The lease runs out and the site is due again | The lease runs out and the job is due again | The lease runs out and the email is due again |
| Work | `discovery.Discoverer`, then `DiscoveryRepository.Save` | `FeedRepository.GetByURL`, else (or if gone) discovery and `DiscoveryRepository.Save`; then `SubscriptionRepository.Subscribe` | `ingest.Refresher.Refresh` | `Runner.check`: home page, `discovery.ParsePage` icons, downloads; then `SaveIcon`, `KeepIcon` or `RemoveIcon` | `Runner.attempt`: the article's page, `discovery.ParsePage`; then `SavePage`, `Retry` or `GiveUp` | `Sender.Send` |

Retry schedules for feeds, icons and enrichment are in their sections of [features.md](features.md).
A failed email is retried after 1, 2, 4, … minutes (`retryDelay`, capped at an hour) and marked `failed` after 8 attempts.
Delivery is at least once: a crash between sending and recording the result sends the email again.
Every hour the mail runner deletes sent and failed emails older than 7 days (`DeleteFinished`).

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
  YouTube and Vimeo iframes become click-to-play previews; the players load only from the two `frame-src` hosts, sandboxed, with URLs `app.js` builds from the validated video ID.
  Every other iframe becomes a link.
- **Site icons:** served from our own domain (`siteIconHandler`), so pages never load them from the site.
  Only the raster types the icon runner stores (PNG, JPEG, GIF, ICO) are served, with their stored `Content-Type`, `nosniff`, and `Content-Security-Policy: default-src 'none'; sandbox` in place of the page CSP.
- **Outbound requests:** `internal/fetch` (see Outbound HTTP).
- **Sessions:** scs, stored in Postgres; the token is renewed on login and logout; cookies are `SameSite=Lax`, and `Secure` with `-env production`.
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
- **Shared timelines:** their routes are public (no session, no login) and reachable only by the share token: 32 random bytes from `token.Random`, stored as is, so the owner's settings can show the link again.
  A malformed token is a 404 before any query.
  The page and feed send `X-Robots-Tag: noindex` (the page also has a robots meta tag) and name no owner; feed entries carry only titles, links, dates, excerpts and each feed's own title.
  Their absolute URLs are built from `-base-url`, never the `Host` header.
  Sharing and New link need a confirmed email.
- **Form bodies:** `decodePostForm` caps them at 64 KiB (`maxFormBytes`); over the cap is a 413 and an unparseable body a 400, both HTML error pages from `formErrorResponse`.
- **OPML uploads:** `settingsImportFormHandler` is a multipart form with its own cap: the body is limited to 1 MiB (`maxImportBytes`) plus 64 KiB for the multipart headers, and the file itself to 1 MiB; over either is a 413 that re-renders the form.
  A file may list up to 1,000 feeds (`maxImportFeeds`); the stored filename is its base name, at most 255 bytes.
- **Rate limits** (`internal/server/ratelimit.go`): in-memory fixed windows, per process.

  | Limiter | Key | Limit |
  | --- | --- | --- |
  | `loginLimiter` | client IP (IPv6 by /64) | 20 per 10 min |
  | `loginFailureLimiter` | submitted email | 10 failed logins per 15 min |
  | `signupLimiter` | client IP (IPv6 by /64) | 10 per hour |
  | `lookupLimiter` | user | 10 per 10 min |
  | `refreshLimiter` | user | 10 per 10 min |
  | `importLimiter` | user | 5 OPML imports per hour |
  | `passwordChangeLimiter` | user | 10 per 10 min |
  | `confirmResendLimiter` | user | 3 confirmation emails (Resend) per hour |
  | `resetIPLimiter` | client IP (IPv6 by /64) | 10 password reset requests per hour |
  | `resetEmailLimiter` | submitted email | 3 password reset requests per hour |
  | `deleteAccountLimiter` | user | 10 per 10 min |
  | `emailChangeLimiter` | user | 5 email change requests per hour |

  The outbox also caps emails per address, across processes: `EmailRepository.Enqueue` queues about 20 to one address (ignoring case) per hour and returns `rss.ErrTooManyEmails` past that, which `sendEmail` logs as a warning.
  It counts, then inserts, so simultaneous requests can slightly exceed 20.

- **Client IP:** `Server.clientIP` (`internal/server/clientip.go`) is the only code that reads `RemoteAddr`.
  With `-proxy-hops` N above 0 it reads `X-Forwarded-For` instead, taking the entry N places from the right, and falls back to `RemoteAddr` when the header is missing, has fewer than N entries, or that entry doesn't parse.
  If that entry isn't a public address, it logs a warning once per process.
- **Internal errors** (the internal columns, Go errors) go to the logs, never to pages, except debug panels.
- **Debug mode:** only accounts listed in `-debug-emails` can turn it on (empty, the default, turns it off for everyone), and both checks run on every request.
  Its panels show internal details to those accounts only, as escaped text, and only read.

## Frontend

Templates, CSS and JavaScript follow the conventions in CLAUDE.md.
Beyond those:

- **Static files** are embedded and served at `/static/…?v=<hash>`.
  The `asset` template function builds those URLs, so a changed file gets a new URL and can be cached for a year.
  Besides the CSS and JavaScript, they're the logo and favicons (`img/`) and the self-hosted fonts (`fonts/`: Bricolage Grotesque for headlines and Newsreader for reading, with their licences); every page links the favicons through `partials/head.html`.
  The install icons (`img/icon-192.png`, `img/icon-512.png`) are rendered from `img/app-icon.svg`, whose comment says how to regenerate them.
- **The web app manifest** (`ui/manifest.webmanifest`) is embedded and served by its own handler, not from `/static`: Go's `mime` table has no `.webmanifest` type and the distroless image has no `/etc/mime.types`, so the file server would sniff it as `text/plain`.
  Pages are standalone, so each includes `app-head` (`partials/head.html`: icons, manifest link, `theme-color`); a test fails for a page without it.
  The manifest and the `theme-color` metas repeat `--bg`, as they can't read CSS custom properties: the manifest the light one, and `app-head` the user's theme's on logged-in pages (they pass it their navData), else a light and dark pair with `prefers-color-scheme` media queries.
- **Themes:** `main.css` defines the color tokens on `:root` (light), then the dark values twice with the same content: under `@media (prefers-color-scheme: dark)` for `:root:not([data-theme="light"])`, and for `:root[data-theme="dark"]`.
  A logged-in page's `<html>` gets `data-theme="light"` or `"dark"` from the user's choice in Settings (`users.theme`, loaded with the session): each page opens with `<html lang="en"{{template "html-theme" .Nav}}>`, which writes `navData.Theme()`, nothing for System, and includes `{{template "app-head" .Nav}}`; a test fails for a page with the nav but without them.
  The dark values are written twice (system and chosen), and `internal/ui/css_test.go` fails if the two differ or a color token has no dark value.
  Logged-out and error pages have no user, so they always follow the system.
- **The nav** (`partials/nav.html`, `app-nav`) has four section links (Home, Saved, My feeds, Search) and an account entry (the user's letter avatar, labelled Settings).
  Wide screens put it in a rail, medium screens show icons only, phones put it in a top bar.
  Log out is a button on the Settings page.
- **Avatars:** sites and feeds have rounded-square avatars (`partials/avatar.html`): the site's icon on a white tile, else its letter on its tint.
  People's avatars (the nav, Settings) stay circles.
- **The tab row** (`partials/timeline_tabs.html`) heads Home and each custom timeline in place of the page's visible heading (the `<h1>` is visually hidden).
  It's a `nav` of links with `aria-current`, not ARIA tabs, and shows no counts.
- **`app.js`** has nine enhancements, each hooked onto server-rendered markup: local times (`<time data-local>`), read pings (`<a ping>` → `navigator.sendBeacon`), Save and Star buttons (`form[data-toggle]`, posted with `fetch`; success is a `204`, and on any failure the form submits normally), Share buttons (`button[data-share-url]`, rendered hidden and shown by `app.js`; the share sheet, else the clipboard), video embeds (`figure.embed[data-embed]`), the "You were here" beacon (`.article-list[data-seen-up-to]` → `navigator.sendBeacon` when the page is hidden), infinite scroll (`.pager a[rel=next]`, appending rows and the `.timeline-divider`), scrolling the tab row to its current tab, and a show/hide toggle on every password input.
  `internal/server/scroll_test.go` pins the markup the script relies on.

## Testing

Server and DB test conventions are in CLAUDE.md.
The other packages test against fakes of their own interfaces, and use `httptest` servers with `fetch.Options{AllowPrivate: true}` where real HTTP matters (`fetch`, `discovery`, `ingest`).
`tools/loadseed` (`make db/loadseed`, dev only, not in the `www` binary) seeds a development database at realistic scale and times the `psql` reading methods themselves, printing their plans from `auto_explain`, on a connection set up as the app's (`psql.NewConnector`).

## Deployment

Production runs on Render since #214.
Why this setup is under "Deployment" in [decisions.md](decisions.md).
What runs where, and how to operate it, is in [`infra/README.md`](../infra/README.md) (the runbook) and the Terraform in `infra/`; this section is the overview.

- **Images.** The `Dockerfile` builds `www` as a static binary into `gcr.io/distroless/static-debian12:nonroot`, with the commit SHA as `version` (reported by `/healthcheck`).
  `deploy/backup` is the nightly backup's image: `pg_dump` streamed to the R2 bucket `rss-backups`.
  CI's `image` job builds both on every event, and on `main` pushes them to GHCR (`:sha-<sha>` and `:main`) with build provenance; PRs never push.
- **Infrastructure** (`infra/`, one Terraform root module, state in the R2 bucket `rss-tfstate`): a Postgres 17 database reachable only from Render's private network, the `rss` web service (pre-deploy `/www migrate`, health check `/healthcheck`: Render reads its status code, which is always 200, and the uptime monitor reads its `status`), the `rss-backup` cron job, the backup bucket's lifecycle rules, and the app's DNS records in Cloudflare.
  Values that change outside Terraform (the images, the invite code, the SMTP password, `RSS_ALLOW_RESET_TO`) are in `ignore_changes`.
- **Workflows** (`.github/workflows`): `ci.yml` (checks, images, then `deploy.yml` on `main`); `deploy.yml` deploys an image by digest and checks `/healthcheck` reports its SHA, then deploys the backup image to the `rss-backup` cron job by digest (also run by hand to roll back); `reset-db.yml` allows a pre-alpha schema reset, then deploys; `infra.yml` plans `infra/` changes on PRs and applies them on `main` after the owner approves.
  All Render, R2 and Cloudflare values are repository secrets, read only on `main`, by hand, or on PRs from branches in this repository; none uses `pull_request_target`.
- **Dependabot** (`.github/dependabot.yml`) proposes weekly updates for Go modules, GitHub Actions, the base images and the Terraform providers.
