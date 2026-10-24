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
run seven long-lived components, all stopped by one context:

- The **HTTP server** (`internal/server`) handles requests and reads and writes the
  database through the `internal/psql` repositories. Its Refresh button calls
  `ingest.Refresher` directly.
- The **refresh runner** (`internal/refresh`) claims due feeds from the `feeds` table and
  hands each to `ingest.Refresher`, which fetches the feed and saves the outcome.
- The **lookup runner** (`internal/lookup`) claims pending rows from the `lookups` table,
  runs `discovery.Discoverer` to find the site's feeds, and saves them with
  `psql.DiscoveryRepository`.
- The **import runner** (`internal/importer`) claims pending rows from the `import_feeds`
  table (one per feed of an uploaded OPML file), finds the feed by URL or discovers and
  saves it as the lookup runner does, and subscribes the import's user to it.
- The **site icon runner** (`internal/siteicon`) claims sites whose icon check is due
  from the `sites` table, fetches each site's home page, downloads the icons it declares
  (or `/apple-touch-icon.png` and `/favicon.ico`) until one is usable, and stores it in
  `site_icons`, keeps the current one, or removes it.
- The **enrich runner** (`internal/enrich`) claims due jobs from the `enrich_jobs` table
  (queued by `saveArticles` for new articles missing an image, title or excerpt), fetches
  each article's web page, and stores its Open Graph image, title and description in
  the article's `page_*` columns.
- The **mail runner** (`internal/mail`, `mail.Runner`) claims due emails from the
  `emails` table (the outbox, filled by the server's `sendEmail` through `mail.Queue`) and
  sends them with the sender `newMailSender` builds.

`ingest`, `discovery`, `siteicon` and `enrich` make every outbound request through `internal/fetch`.

`cmd/www/main.go` (`run`) hands `www migrate` to `runMigrate`, `www create-user` to
`runCreateUser` and `www send-test-email` to `runSendTestEmail` (below). Otherwise it
parses the configuration (`parseConfig` in `config.go`: each flag,
or else its `RSS_` environment variable via `setFlagsFromEnv`, or else its default;
errors go to stderr) and builds the slog logger on stdout (`newLogger`: JSON in
production, text otherwise), which also reports the error `Run` returns.
`cmd/www/application.go` (`Application.Run`) wires everything: it opens the database, builds one `fetch.Client` (plus one capped at 256 KiB for icon downloads) and one `ingest.Refresher` (shared by the
Refresh button and the background workers), the repositories, the scs session manager
(sessions in Postgres, one-year lifetime) and the server, then starts the runners next to
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

`www create-user` (`runCreateUser` in `cmd/www/create_user.go`, with its own flags)
creates an account with the password on the first line of stdin, validated with
`validator.CheckNewAccount` like signup, through `psql.UserRepository.Create`, and exits.
It's for local development (`make db/seed`); there's nowhere to run it in production.

`www send-test-email` (`runSendTestEmail` in `cmd/www/send_test_email.go`) takes the
mail flags (`registerMailFlags` in `config.go`) and `-to`, renders the `test` email with
`mail.ParseTemplates` and `Templates.Render`, and sends it with the sender
`newMailSender` builds: `mail.SMTPSender`, or `mail.LogSender` when there's no SMTP
host. It doesn't open the database. It checks the mail settings, locally against
Mailpit (`compose.yml`) and in production against Resend.

## Packages

| Package | Role | Depends on |
| --- | --- | --- |
| `cmd/www` | Flags, wiring, signal handling, the `migrate`, `create-user` and `send-test-email` commands | `server`, `psql`, `lookup`, `importer`, `refresh`, `siteicon`, `enrich`, `ingest`, `discovery`, `fetch`, `mail`, `ui`, `migrations`, `validator`, `rss` |
| `internal/server` | Router, middleware, handlers, templates, rate limits | `rss`, `ui`, `validator`, `sanitize`, `mail`, `token`, `discovery` (input parsing; `InspectPage` and `SiteDiscoverer` for the debug inspectors), `ingest` (errors; `Articles` and `ExternalID` for the feed inspector), `feedparse` and `fetch` (the feed inspector), `enrich` (`PageOf` and `IsHTML` for the page inspector), `opml` (import uploads), `psql` (interface checks only) |
| `internal/psql` | Repositories: plain SQL, driver errors mapped to `rss` errors; `Migrate` | `rss`, `mail` (types), `password` |
| `internal/lookup` | Lookup queue workers | `discovery`, `ingest`, `rss` |
| `internal/importer` | OPML import queue workers: find or discover each feed, then subscribe | `discovery`, `lookup` (`ToSave`), `rss` |
| `internal/opml` | Read the feeds listed in an OPML file (`Parse`), folders flattened | — |
| `internal/refresh` | Feed refresh workers | `ingest`, `rss` |
| `internal/siteicon` | Site icon workers: find a site's icon on its home page, check its type and size, store it | `discovery`, `fetch`, `rss` |
| `internal/enrich` | Article enrichment workers: fetch an article's web page and store its image, title and description (`PageOf`, shared with the page inspector) | `discovery`, `fetch`, `ingest` (`ExcerptLen`, `RetryAfter`), `rss` |
| `internal/ingest` | Fetch a feed, turn items into articles, schedule the next fetch | `fetch`, `feedparse`, `rss` |
| `internal/discovery` | Normalize user input, find a site's feeds; `Result.Checked` records every candidate and its outcome; `InspectPage` lists a page's `<title>`, `<base>`, `<meta>` and `<link>` tags (used or ignored by `ParsePage`) and its JSON-LD, for the debug inspectors | `fetch`, `feedparse` |
| `internal/feedparse` | Recognize and parse RSS, Atom and JSON Feed (via gofeed); `Inspect` lists a document's fields (used or ignored by `Parse`) and items' source, for the feed inspector | — |
| `internal/fetch` | The only outbound HTTP client for user-influenced URLs | — |
| `internal/mail` | Render emails from embedded templates (`ParseTemplates`, `Render`); queue them in the outbox (`Queue`) and send them from it (`Runner`) over SMTP (`SMTPSender`) or to the log (`LogSender`) | `rss` |
| `internal/sanitize` | Make article HTML safe to render (bluemonday) | — |
| `internal/password` | Argon2id hashing, after NFKC normalization | — |
| `internal/token` | One-time tokens for email links: `New` (32 random bytes, base64url, and its SHA-256) and `Hash` | — |
| `internal/rss` | Domain types (`Feed`, `Article`, `Site`, …) and sentinel errors | — |
| `internal/ui` | Embedded page templates, email templates (`Emails`) and static files | — |
| `internal/validator` | Form validation helpers; the rules for new passwords, with the embedded common-password list | — |
| `migrations` | The goose SQL migrations, embedded (`FS`) | — |

Consumers define the interfaces they need, and only `cmd/www` builds the concrete types.
The server's store interfaces (`FeedStore`, `ArticleStore`, …) and the `Services` struct
that holds them are in `internal/server/server.go`, with compile-time checks that the
`psql` repositories satisfy them. `lookup`, `importer`, `refresh`, `siteicon`, `enrich`, `ingest` and
`discovery` each declare their own small interfaces (`Store`, `Fetcher`, `Saver`, …).
That's what lets every package be tested with fakes (see Testing).

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
| `golang.org/x/net/html` | Reading HTML pages (`discovery.ParsePage`) and rewriting article HTML; its `charset` package decodes OPML files in legacy encodings (`opml.Parse`) |
| `golang.org/x/crypto/argon2` | Password hashing |
| `golang.org/x/text/unicode/norm` | NFKC-normalizing passwords before hashing and checking them |
| `honnef.co/go/tools` | `staticcheck`, as a `go tool` |

## HTTP

**Middleware**, outermost first (`internal/server/router.go`):

1. `logRequest`: one log line per request with status, size and duration.
2. `recoverPanic`: a panic becomes the 500 page.
3. `http.CrossOriginProtection`: rejects cross-origin non-GET browser requests
   (`Sec-Fetch-Site` / `Origin`) with the 403 page. There are no CSRF tokens.
4. `commonHeaders`: CSP `default-src 'self'; img-src 'self' https:; frame-src
   https://www.youtube-nocookie.com https://player.vimeo.com`, `nosniff`,
   `X-Frame-Options: deny`, `Referrer-Policy`.
5. `middleware.GetHead`: HEAD is served by the GET handler, upkeep side effects included.

Then routes are split into groups. `/static/*`, `/sites/{id}/icon`, `/healthcheck`,
`/privacy` and `/terms` have no session. All
other routes load the session (`sessionManager.LoadAndSave`) and `authenticate`, which
loads the device's `user_sessions` row with its user and puts both in the request
context, unless the row is gone or idle for 90 days.
Routes in the `requireAuthentication` group redirect anonymous users to `/login`
(remembering a GET or HEAD's URI as `returnTo`, for after login or signup) and set
`Cache-Control: no-store`.

**Debug mode** (`internal/server/debug.go`) is gated in two steps, both checked on every
request: `debugAllowed` (the logged-in user's email is in `-debug-emails`, ignoring case)
and `debugEnabled` (allowed, and `"debug"` is set in the device's scs session).
`POST /settings/debug` is a 404 unless `debugAllowed`. Pages only query
`DebugStore` (`psql.DebugRepository`) when `debugEnabled`; article rows' debug lines
(`fillDebug`) make no queries. Routes under `/debug/` are a
group inside `requireAuthentication` with `requireDebug`, which answers 404 unless
`debugEnabled`; they're POSTs, since they fetch URLs.

**Routes:**

| Route | Login | Handler | What it does |
| --- | --- | --- | --- |
| `GET /healthcheck` | no | `healthcheckHandler` | JSON status, environment and build version, for health checks |
| `GET /static/*` | no | file server | Embedded CSS and JS; `?v=<hash>` URLs are cached for a year |
| `GET /sites/{id}/icon` | no | `siteIconHandler` | A site's stored icon; `?v=<hash>` URLs are cached for a year; 404 when it has none |
| `GET /privacy` | no | `privacyHandler` | Privacy policy |
| `GET /terms` | no | `termsHandler` | Terms of use |
| `GET /` | no | `homeHandler` | Landing page (shows a flash); the timeline when logged in |
| `GET, POST /signup` | no | `signupHandler`, `signupFormHandler` | Only registered with `-invite-code` |
| `GET, POST /login` | no | `loginHandler`, `loginFormHandler` | |
| `POST /logout` | no | `logoutHandler` | |
| `GET /confirm-email` | no | `confirmEmailHandler` | A confirmation link: a page with a Confirm button, or the expired-link page (410) |
| `POST /confirm-email` | no | `confirmEmailFormHandler` | Confirm the address; flash, then the timeline or `/login` |
| `GET /email-change` | no | `emailChangeHandler` | A change-email link: a page with a Change button, or the expired-link page (410) |
| `POST /email-change` | no | `emailChangeFormHandler` | Switch to the new address (confirmed); notice to the old one; flash, then `/settings` or `/login` (409 if the address was taken) |
| `GET, POST /password/forgot` | no | `forgotPasswordHandler`, `forgotPasswordFormHandler` | Ask for a reset link (`?email=` prefills); emails it, then the sent page |
| `GET /password/forgot/sent` | no | `forgotPasswordSentHandler` | "Check your email", with a button to send another link; else 303 to `/password/forgot` |
| `GET /password/reset` | no | `resetPasswordHandler` | A reset link: the new-password form, or the expired-link page (410) |
| `POST /password/reset` | no | `resetPasswordFormHandler` | Set the new password, log out everywhere, log in here; flash, then the timeline |
| `GET /.well-known/change-password` | no | `changePasswordWellKnownHandler` | 302 to `/settings/password`, for password managers |
| `GET /saved` | yes | `savedHandler` | Saved: the user's saved articles, newest saved first, paged with `?before=`, filtered by `?q=` |
| `GET /feeds` | yes | `feedsHandler` | My feeds: the user's subscriptions by site |
| `GET /feeds/{id}` | yes | `feedHandler` | Feed page; queues a refresh if the feed is stale |
| `POST /feeds/{id}/refresh` | yes | `feedRefreshHandler` | Refresh button: fetches now |
| `POST /feeds/{id}/subscribe` | yes | `feedSubscribeHandler` | |
| `POST /feeds/{id}/unsubscribe` | yes | `feedUnsubscribeHandler` | |
| `GET /feeds/{id}/edit` | yes | `subscriptionEditHandler` | Edit subscription: the user's name for the feed; 404 if not subscribed; `?return_to=` |
| `POST /feeds/{id}/edit` | yes | `subscriptionEditPostHandler` | Saves the name; 303 to `return_to` (else `/feeds`) |
| `GET /articles/{id}` | yes | `articleHandler` | Article page; records the read |
| `POST /articles/{id}/read` | yes | `articleReadHandler` | Click-through ping; 204 |
| `POST /articles/{id}/save` | yes | `articleSaveHandler` | Save button: saves the article (and its copies); 303 to `return_to#article-{id}` |
| `POST /articles/{id}/unsave` | yes | `articleUnsaveHandler` | Unsaves the article and its copies; 303 to `return_to#article-{id}` |
| `GET /sites/{id}` | yes | `siteHandler` | A site and its feeds |
| `GET /search` | yes | `searchHandler` | Search sites and feeds in the database |
| `POST /lookups` | yes | `lookupCreateHandler` | Queue a site lookup; wait up to 3 s for it |
| `GET /lookups/{id}` | yes | `lookupHandler` | Lookup status page |
| `GET /settings` | yes | `settingsHandler` | Settings: the account's rows, Devices, Log out and the Delete account link |
| `GET, POST /settings/name` | yes | `settingsNameHandler`, `settingsNameFormHandler` | Change your name |
| `GET, POST /settings/password` | yes | `settingsPasswordHandler`, `settingsPasswordFormHandler` | Change your password; logs out other devices and emails a notice |
| `GET, POST /settings/email` | yes | `settingsEmailHandler`, `settingsEmailFormHandler` | Change your email (with your password); emails a link to the new address |
| `POST /settings/log-out-others` | yes | `logOutOthersHandler` | Log out of all other devices |
| `POST /settings/debug` | yes | `settingsDebugFormHandler` | Turn debug mode on (`on=1`) or off for this device; 404 unless the account is on `-debug-emails` |
| `POST /debug/feeds/{id}/inspect` | yes | `feedInspectHandler` | Debug mode only (`requireDebug`): fetch the feed live and show the response, its fields and what a refresh would do with each item; saves nothing |
| `POST /debug/articles/{id}/inspect-page` | yes | `articlePageInspectHandler` | Debug mode only: fetch the article's web page live and show its tags, its JSON-LD and what enrichment would store; 404 for an article without a URL; saves nothing |
| `POST /debug/sites/{id}/discover` | yes | `siteDiscoverHandler` | Debug mode only: fetch the site's home page and run discovery live, and show its tags, every candidate's outcome, and the feeds found next to the stored ones; saves nothing |
| `GET /settings/subscriptions.opml` | yes | `exportOPMLHandler` | Download your subscriptions as OPML |
| `GET, POST /settings/import` | yes | `settingsImportHandler`, `settingsImportFormHandler` | Upload an OPML file to import (multipart), and your last 5 imports; 303 to the import's results |
| `GET /settings/imports/{id}` | yes | `importHandler` | An import's progress (refreshes itself every 2 s), then its results; 404 for another user's |
| `GET, POST /settings/delete` | yes | `settingsDeleteHandler`, `settingsDeleteFormHandler` | Delete your account (with your password); emails a notice, logs out, flash on `/` |
| `POST /confirm-email/resend` | yes | `confirmResendHandler` | Email a new confirmation link; back to `return_to` (else `/settings`) |

**Rendering** (`internal/server/html.go`): at startup every `templates/*.html` page is
parsed together with all of `templates/partials/*.html` into one template per page.
`renderHTML` executes into a buffer first, so a template error becomes a clean 500
instead of half a page. Template functions: `asset` (a static file's URL with a content
hash, from `static.go`), `avatar` (a site's or feed's `avatarView` for the `avatar`
partial: its icon URL, else its letter and tint), `avatarTone`, `monogram` and
`debugPanel` (a `debugPanel` for the `debug-panel` partial). Pages with
site avatars pass the icon URLs from `siteIconURLs` (rows carry theirs in
`articleRow.Icon`). Logged-in pages pass a `navData`
(the current section, the logged-in user and whether debug mode is on, from `s.nav`) as their `Nav` field for the
`app-nav` partial. Errors are HTML pages via `errorHTML` (`errors.go`); JSON is only for
`/healthcheck`.

**Timeouts:** the server's `ReadTimeout` is 5 s and `WriteTimeout` 10 s, so handlers that
wait on slow work bound it below that: the Refresh button gives a fetch 8 s, and
`POST /lookups` waits at most 3 s before redirecting to the status page. Inspectors are
the exception: `extendWriteDeadline` moves their response's write deadline to 35 s from
when they start.

## Data model

The whole schema is `migrations/00001_init.sql` (edited in place while pre-alpha; see
CLAUDE.md).

```
users ──< subscriptions >── feeds ──< articles ──< reads >── users
                              │          │
                              │          ├──< saves >── users
                              │          ├──< stars >── users
                              │          └──< enrich_jobs
                              │
                            sites ──< lookups (site_id, once found)
                              └──< site_icons

users ──< tags
subscriptions ──< subscription_tags >── tags
users ──< timelines ──< timeline_tags (>── tags) / timeline_feeds (>── subscriptions)
users ──< imports ──< import_feeds (feed_id, once known)
users ──< user_sessions
users ──< user_tokens

sessions (scs's table: session data keyed by token, no foreign keys)
emails   (the outbox: no foreign keys)
```

- **`sites`**: a website, unique by `host`, its *site key* (lowercase host without
  `www.`, `discovery.SiteKey`). `icon_checked_at` is when the icon worker last looked
  for its icon, and `icon_next_check_at` when it should look next (also the claim
  lease).
- **`site_icons`**: at most one per site, the icon downloaded from `source_url`: its
  `content_type` (PNG, JPEG, GIF or ICO), `width` / `height`, the bytes in `data` (at
  most 256 KiB), `hash` (hex SHA-256 of `data`, the `?v=` of icon URLs) and `etag` /
  `last_modified` for conditional GET. No row: the site shows its letter avatar. Written by
  the site icon runner, served by `GET /sites/{id}/icon`.
- **`feeds`**: unique by canonical `url`; `site_id` is the site it was first discovered
  from. Also the refresh queue: `next_fetch_at` (when it's due, and the claim lease),
  `consecutive_failures`, `last_error` (internal, never shown), `etag` / `last_modified`
  (for conditional GET), `gone_at` (410 Gone: not fetched again until a lookup finds the
  feed again), and `refresh_requested_at` (a page view asked for a fetch).
  `posts_per_week` is how many articles it published in the last 30 days, per week
  (not written or read yet).
- **`articles`**: unique by `(feed_id, external_id)`. `summary` and `content` are raw HTML
  from the feed; `excerpt` is plain text; `canonical_url` matches copies of one article
  across feeds; `timeline_at` is set once on insert and never updated. `page_image_url`,
  `page_title` and `page_description` hold what enrichment reads from the article's web
  page, kept apart from the feed's own `image_url`, `title` and `excerpt`, which
  `saveArticles` overwrites; the enrich runner writes them (`EnrichRepository.SavePage`).
  `media_type` is the MIME type
  of the item's first audio or video enclosure (`""` if none), written by `saveArticles`
  and not displayed yet.
- **`subscriptions`** and **`reads`**: `(user_id, feed_id)` and `(user_id, article_id)`.
  A subscription's `title` is the user's name for the feed; `''` uses the feed's own
  title (not written or read yet).
- **`saves`**: `(user_id, article_id)` with `saved_at`; an article a user saved to keep.
  Like `reads`, every copy of the article (same `canonical_url`) counts as saved, matched
  at query time.
- **`stars`**: `(user_id, article_id)` with `starred_at`; an article a user starred
  ("this is good"). Like `saves`, every copy of the article (same `canonical_url`)
  counts as starred, matched at query time. Not used yet.
- **`tags`** and **`subscription_tags`**: a user's labels (`name`, `citext`: trimmed,
  inner spaces collapsed, unique per user ignoring case) and the subscriptions that
  carry them, several per subscription. Composite foreign keys (`(user_id, feed_id)` to
  `subscriptions`, `(tag_id, user_id)` to `tags`) keep a subscription to its own user's
  tags, so repositories need no ownership check for it. Not used yet.
- **`timelines`**, **`timeline_tags`** and **`timeline_feeds`**: a user's timelines.
  Home is the row with `home` set (at most one per user, created when the user first
  mutes something on Home; no row: nothing is muted), and only its leave-outs are used;
  the others are custom timelines with a `name`, `description` and tab `position`. Their
  conditions are columns: `all_feeds` (false: only the feeds of its included tags and
  feeds), `starred_only`, `max_posts_per_week` (0: any rate), `include_words` (an article
  must mention one), `exclude_words` (an article mentioning one is left out),
  `media_only` (audio or video enclosures only) and `share_token` (NULL: not shared;
  else the public link's token, stored as is). Its sources are `timeline_tags` and
  `timeline_feeds` rows, each with `exclude` (false: show; true: leave out); composite
  foreign keys keep them to the timeline's own user's tags and subscriptions. Not used
  yet.
- **`enrich_jobs`**: the enrichment queue, one row per article whose web page should be
  fetched for a missing image, title or excerpt: `priority` (0 new arrivals, 1 backlog;
  lower first), `next_attempt_at` (when it's due, and the claim lease), `attempts`,
  `last_error` (internal, never shown). A row is deleted once its work is done or given
  up on. `saveArticles` queues jobs; the enrich runner claims them, and only for
  articles of feeds with a subscriber.
- **`imports`** and **`import_feeds`**: an OPML file a user uploaded (`filename`) and its
  feeds, keyed by `(import_id, position)` in file order, with the outline's `url` and
  `title`, and `tags`, the names of the folders the outline is in, to apply as tags on
  subscribe (not written or read yet). `import_feeds` is also the import queue: `status` pending → running →
  subscribed, already_subscribed or failed, with `failure` (`invalid_url`,
  `unreachable`, `not_feed`) and `error` (internal, never shown); `feed_id` is the feed
  subscribed to, once known.
- **`lookups`**: one row per site key. It is both the lookup queue (`status` pending →
  running → done or failed) and the cache of the result (`site_id`).
- **`users`**: `email` is `citext`, so unique regardless of case. `email_confirmed_at`
  is NULL until the address is confirmed (`ConfirmEmail`); while it is, logged-in pages
  with the confirm notice show it.
- **`user_sessions`**: one row per logged-in device, created at login, its `id` kept in
  the scs session under `sessionID`; deleting the row logs that device out.
  `last_seen_at` is written at most once a day. There's no sweeper: `authenticate`
  deletes an idle row when it sees it, and login deletes the user's idle rows. A
  password reset deletes all of the user's rows.
- **`user_tokens`**: one-time tokens for email links, keyed by the SHA-256 `hash` of the
  token, with their `purpose`, the `email` they were sent to and `expires_at`
  (`TokenRepository`). Using a token deletes it and the user's other tokens of its kind.
  There's no sweeper: `TokenRepository.Create` deletes the user's expired tokens first.
- **`emails`**: the outbox. `status` pending → sent or failed, `next_attempt_at` (when
  it's due, and the claim lease), `attempts`, `last_error` (internal, never shown); the
  bodies are cleared once sent or given up on. `to_address` isn't a foreign key, so an
  email outlives its user. The mail runner deletes sent and failed rows after 7 days.

Deleting a site cascades to its feeds and their articles, subscriptions, reads, saves
and enrich jobs, and to its `site_icons` row, and clears `lookups.site_id` and
`import_feeds.feed_id`; deleting an article deletes its saves, stars, reads and enrich
job; deleting a user cascades to their subscriptions, reads, saves, stars, tags,
timelines, imports (and their `import_feeds`), `user_sessions` and `user_tokens`, and
leaves their `emails`. Deleting a subscription (unsubscribing) deletes its name, its
`subscription_tags` and its `timeline_feeds` rows, so subscribing again starts clean;
deleting a tag removes it from subscriptions and timelines.
Deleting an account (`UserRepository.Delete`) deletes the user, with that cascade;
feeds and articles stay, even when no one subscribes to them anymore. Nothing deletes
sites, feeds or articles yet; unsubscribing deletes the subscription row, logging out deletes the `user_sessions` row, confirming an address
deletes the user's `confirm_email` tokens, and scs deletes expired sessions.

Two tables aren't in `00001_init.sql`: goose's `goose_db_version`, and
`schema_fingerprint` (one row: the fingerprint of the migrations last applied), which
`psql.Migrate` creates for the pre-alpha check (see "The process").

## Background work

The runners follow the same pattern: N worker goroutines (`-lookup-workers`,
`-import-workers`, `-refresh-workers`, default 2; `-icon-workers`, `-enrich-workers`, `-mail-workers`, default 1; 0 turns a
runner off) loop claiming one job at a time from a Postgres table with `FOR UPDATE SKIP
LOCKED`, so several instances can run side by side. When nothing is claimable a worker
sleeps for a poll interval. Each job gets a timeout; its outcome is recorded with a fresh
short context, so a job that timed out still records its failure. On shutdown, lookups,
imports, refreshes, icon checks and enrichment jobs record nothing and are retried later; the mail runner instead
finishes the email it's sending (within its timeout) and records the result, since an
aborted send can still be delivered. With `-mail-workers 0` the process queues emails
without sending them (and logs that at startup); another instance's runner sends them.

| | Lookups (`internal/lookup`) | Imports (`internal/importer`) | Feed refresh (`internal/refresh`) | Site icons (`internal/siteicon`) | Enrichment (`internal/enrich`) | Email (`mail.Runner`) |
| --- | --- | --- | --- | --- | --- | --- |
| Queue | `lookups` rows with `status = 'pending'` | `import_feeds` rows with `status = 'pending'`, oldest import first, then by position | `feeds` with `next_fetch_at <= now()`, not gone, and subscribed or requested | `sites` with `icon_next_check_at <= now()` that are new (`icon_checked_at IS NULL`) or have a subscribed feed | `enrich_jobs` with `next_attempt_at <= now()` whose article's feed has a subscriber, by `priority` then `next_attempt_at` | `emails` that are pending and due (`next_attempt_at <= now()`) |
| Claim | `ClaimNext`: set `running` | `ClaimNext`: set `running` and count the attempt | `ClaimDue`: move `next_fetch_at` 5 min ahead (the lease); requested feeds first | `ClaimIconCheck`: move `icon_next_check_at` 5 min ahead (the lease) | `ClaimNext`: move `next_attempt_at` 2 min ahead (the lease) and count the attempt | `ClaimNext`: move `next_attempt_at` 2 min ahead (the lease) and count the attempt |
| Poll when idle | 500 ms | 1 s | 15 s | 5 s | 5 s | 10 s, and woken (`Runner.Wake`) when this process queues an email |
| Job timeout | 30 s | 30 s | 30 s | 30 s | 20 s | 30 s (SMTP: 10 s, `SMTPSender.Timeout`) |
| Crashed job | `ResetStale` every minute: running for over 2 min → pending, failed after 3 attempts | `ResetStale` every minute: running for over 2 min → pending, failed (`unreachable`) after 3 attempts | The lease runs out and the feed is due again | The lease runs out and the site is due again | The lease runs out and the job is due again | The lease runs out and the email is due again |
| Work | `discovery.Discoverer`, then `DiscoveryRepository.Save` | `FeedRepository.GetByURL`, else (or if gone) discovery and `DiscoveryRepository.Save`; then `SubscriptionRepository.Subscribe` | `ingest.Refresher.Refresh` | `Runner.check`: home page, `discovery.ParsePage` icons, downloads; then `SaveIcon`, `KeepIcon` or `RemoveIcon` | `Runner.attempt`: the article's page, `discovery.ParsePage`; then `SavePage`, `Retry` or `GiveUp` | `Sender.Send` |

A site's icon is checked again about 30 days (±10%) after a check that reached the site,
and a day after one that didn't (see "Site icons" in [features.md](features.md)).

An enrichment job that failed with a network error, 429 or 5xx is retried 1 hour, then 6
hours later (or later if a 429's or 503's `Retry-After` says so) and given up after 3
attempts; any other failure gives it up at once (see "Article enrichment" in
[features.md](features.md)).

A failed email is retried after 1, 2, 4, … minutes (`retryDelay`, capped at an hour) and
marked `failed` after 8 attempts. Delivery is at least once: a crash between sending and
recording the result sends the email again. Every hour the mail runner deletes sent and
failed emails older than 7 days (`DeleteFinished`).

## Outbound HTTP

All requests to URLs a user can influence (websites to look up, feeds and the pages they
point to, articles' web pages) go through `fetch.Client` (`internal/fetch`):

- only `http` and `https`, no credentials in URLs, at most 5 redirects, each re-checked;
- a dialer check on the resolved IP of every connection, which blocks loopback, private,
  link-local, multicast and other special ranges and so also defeats DNS rebinding;
- 10 s for the whole request, bodies capped at 5 MiB, no environment proxy;
- a fixed `User-Agent` and `Accept`.

Site icons are downloaded through their own `fetch.Client`, with bodies capped at
256 KiB (`siteicon.MaxBytes`); the home page they're found on goes through the shared one.

Inspectors (debug mode) fetch through the shared `fetch.Client` too (`Services.Fetcher`),
synchronously in the request, with a 30 s budget (`inspectTimeout`) and an extended write
deadline; they save nothing.

`fetch.Options.AllowPrivate` turns the address check off for tests that use `httptest`
servers; `cmd/www` never sets it.

Email goes over SMTP (`internal/mail`) to the configured provider, a fixed host from the
configuration that no user can influence, so it doesn't go through `internal/fetch`.

## Security measures

Where each protection lives. The reasons are in [decisions.md](decisions.md); the rules
for writing code that keeps them (GET handlers, article HTML, inline scripts) are in
CLAUDE.md.

- **Cross-site requests:** `CrossOriginProtection` in the middleware (see HTTP); there
  are no CSRF tokens.
- **Untrusted HTML:** `sanitize.HTML` (see Reading in features.md). The CSP blocks inline
  scripts and styles; images may load from any `https:` host. YouTube and Vimeo iframes
  become click-to-play previews; the players load only from the two `frame-src` hosts
  (`www.youtube-nocookie.com`, `player.vimeo.com`), sandboxed, with URLs `app.js` builds
  from the validated video ID. Every other iframe becomes a link.
- **Site icons:** served from our own domain (`siteIconHandler`), so pages never load
  them from the site. Only the raster types the icon runner stores (PNG, JPEG, GIF, ICO)
  are served, with their stored `Content-Type`, `nosniff`, and
  `Content-Security-Policy: default-src 'none'; sandbox` in place of the page CSP.
- **Outbound requests:** `internal/fetch` (see Outbound HTTP).
- **Sessions:** scs, stored in Postgres; the token is renewed on login and logout; cookies
  are `SameSite=Lax`, and `Secure` with `-env production`. Each login is a
  `user_sessions` row: it ends after 90 days unused (`sessionIdleTimeout`) and at most a
  year after login (scs's `Lifetime`), and logging out deletes it.
- **Passwords:** Argon2id with OWASP's minimum parameters (`internal/password`); at most
  `GOMAXPROCS` hashes run at once and the rest wait. `Hash` and `Verify` normalize the
  password to Unicode NFKC first. Every new password (`validator.CheckNewPassword`) is 8
  to 256 characters, isn't the account's email and isn't on the common-password list
  (`internal/validator/common_passwords.txt`: the NCSC's 100,000 most common passwords
  from Have I Been Pwned, via SecLists, kept to 8 characters or more; lookups are
  case-insensitive).
- **Email:** `SMTPSender` (`internal/mail`) uses implicit TLS on port 465 and otherwise
  requires STARTTLS, except to `localhost` or `127.0.0.1` (`requireTLS`), and rejects a
  line break in the recipient or subject. Links in emails are built from `-base-url`
  (`App.URL`), never from a request's `Host` header. With `-env production`, the app
  refuses to start without its SMTP and mail settings, so one-time links never fall
  through to `LogSender`, which writes them to the logs. An email's bodies are cleared
  once it's sent or given up on (`MarkSent`, `GiveUp`), and the mail runner logs an
  email's `id`, never its address or body.
- **Email links:** tokens are 32 random bytes (`internal/token`); `user_tokens` stores
  only their SHA-256, so a database leak gives no working links. A token works once, until
  it expires, and (except `change_email`) only while the account's email is still the
  address it was sent to. Opening a link shows a page with a button and only the POST
  uses the token, so mail scanners that open links can't use it. The link's page is served
  with `Referrer-Policy: no-referrer` and `Cache-Control: no-store`, since the token is in
  its URL.
- **Form bodies:** `decodePostForm` caps them at 64 KiB (`maxFormBytes`); over the cap is a 413 and an unparseable body a 400, both HTML error pages from `formErrorResponse`.
- **OPML uploads:** `settingsImportFormHandler` is a multipart form with its own cap: the body is limited to 1 MiB (`maxImportBytes`) plus 64 KiB for the multipart headers, and the file itself to 1 MiB; over either is a 413 that re-renders the form. A file may list up to 1,000 feeds (`maxImportFeeds`); the stored filename is its base name, at most 255 bytes.
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

  The outbox also caps emails per address, across processes: `EmailRepository.Enqueue`
  queues about 20 to one address (ignoring case) per hour and returns
  `rss.ErrTooManyEmails` past that, which `sendEmail` logs as a warning. It counts, then
  inserts, so simultaneous requests can slightly exceed 20.

- **Client IP:** `Server.clientIP` (`internal/server/clientip.go`) is the only code that
  reads `RemoteAddr`. With `-proxy-hops` N above 0 it reads `X-Forwarded-For` instead,
  taking the entry N places from the right, and falls back to `RemoteAddr` when the header
  is missing, has fewer than N entries, or that entry doesn't parse. If that entry isn't a
  public address, it logs a warning once per process.
- **Internal errors** (`lookups.error`, `feeds.last_error`, `emails.last_error`, Go
  errors) go to the logs, never to pages, except debug panels.
- **Debug mode:** only accounts listed in `-debug-emails` can turn it on (empty, the
  default, turns it off for everyone), and both checks run on every request (see HTTP).
  Its panels show internal details (`feeds.last_error`, `lookups.error`) to those
  accounts only, as escaped text, and only read.

## Frontend

Templates, CSS and JavaScript follow the conventions in CLAUDE.md. Beyond those:

- **Static files** are embedded and served at `/static/…?v=<hash>`. The `asset` template
  function builds those URLs, so a changed file gets a new URL and can be cached for a
  year.
- **The nav** (`partials/nav.html`, `app-nav`) has four section links (Home, Saved, My feeds, Search) and an
  account entry: the user's letter avatar (`monogram` of the name on an `avatarTone` tint
  of the user ID, in a circle: `.avatar-person`), labelled Settings and linking to
  `/settings`. Wide screens put it, labelled, at the bottom of the rail; medium screens
  show the avatar only; phones put it at the right of the top bar. Log out is a button on the Settings page.
- **Avatars:** sites and feeds have rounded-square avatars (`partials/avatar.html`): the
  site's icon on a white tile (`--avatar-icon-bg`, `.avatar-icon`), else its letter on
  its tint. People's avatars (the nav, Settings) stay circles.
- **`app.js`** has six enhancements, each hooked onto server-rendered markup: local
  times (`<time data-local>`, from the `local-time` and `local-date` partials; `data-local="date"` shows the date only), read pings (`<a ping>` → `navigator.sendBeacon`),
  Save buttons (a delegated `submit` listener posts `form[data-save]` with `fetch`, then
  flips the button's `aria-pressed` and the form's `/save` ↔ `/unsave` action; on any
  failure it submits the form normally), video embeds (a delegated `click` listener
  swaps the link in `figure.embed[data-embed]` for a sandboxed player built from
  `data-embed-id`; modified clicks follow the link), infinite scroll (fetch the page behind
  `.pager a[rel=next]` and append its `.article-row`s), and a show/hide toggle on every
  `input[type=password]` (set back to `password` before its form submits). `internal/server/scroll_test.go` pins the markup the script relies on.

## Testing

Server and DB test conventions are in CLAUDE.md. The other packages test against fakes of
their own interfaces, and use `httptest` servers with `fetch.Options{AllowPrivate: true}`
where real HTTP matters (`fetch`, `discovery`, `ingest`).

## Deployment

Production runs on Render since #214. Why this setup is under "Deployment" in
[decisions.md](decisions.md); the remaining work is in "Next up" in [todo.md](todo.md).
Update this section as it lands.

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

The same job builds the backup image from `deploy/backup`: `postgres:17-alpine` (its
`pg_dump` must be at least the database's major version) plus the Alpine `aws-cli`
package, with `backup.sh` as the entrypoint. The script streams `pg_dump --format=custom
--no-owner "$DATABASE_URL"` into `aws s3 cp - s3://$R2_BUCKET/rss-<UTC
YYYY-MM-DDTHHMMZ>.dump --endpoint-url "$R2_ENDPOINT"`, and deletes the object if either
side fails, since `aws` completes the upload with whatever `pg_dump` wrote. On pushes to
`main`, CI pushes it as `ghcr.io/grodier/rss-backup:main` and `:sha-<sha>` and attests
it like the app image.

**Infrastructure** (`infra/`, one Terraform root module; [`infra/README.md`](../infra/README.md)
is the runbook). The `render-oss/render` provider creates `render_postgres.rss_db`
(Postgres 17, `basic_256mb`, Virginia, an empty IP allow list so only Render's private
network connects, `prevent_destroy`) and `render_web_service.rss` (Starter, Virginia,
image `ghcr.io/grodier/rss:main`, health check `/healthcheck`, pre-deploy command
`/www migrate`, 30-second maximum shutdown delay). The service's environment sets
`RSS_ENV=production`, `RSS_DB_DSN` (the database's internal connection string),
`RSS_DB_MAX_OPEN_CONNS=10`, `RSS_PROXY_HOPS=2`, `RSS_PORT`/`PORT=8080`, and the email
settings `RSS_BASE_URL=https://rss.georgerodier.com`, `RSS_SMTP_HOST=smtp.resend.com`,
`RSS_SMTP_PORT=465`, `RSS_SMTP_USERNAME=resend`, `RSS_MAIL_FROM` and
`RSS_SUPPORT_EMAIL=rss-support@georgerodier.com`. Its custom domain is
`rss.georgerodier.com`. `ignore_changes` leaves the image (set by deploys),
`RSS_ALLOW_RESET_TO` (set by the reset workflow), and `RSS_INVITE_CODE` and
`RSS_SMTP_PASSWORD` (set in Render's dashboard) alone.
`render_cron_job.backup` (`rss-backup`, Starter, Virginia, schedule `0 8 * * *`, image
`ghcr.io/grodier/rss-backup:main`) gets `DATABASE_URL` (the internal connection string),
`PGSSLMODE=require`, `R2_BUCKET=rss-backups`, `R2_ENDPOINT` (from variable
`cloudflare_account_id`), the `rss-backups` token as
`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` (sensitive variables
`backup_r2_access_key_id`/`backup_r2_secret_access_key`), `AWS_DEFAULT_REGION=auto`, and
`AWS_REQUEST_CHECKSUM_CALCULATION`/`AWS_RESPONSE_CHECKSUM_VALIDATION=when_required`. The
`cloudflare/cloudflare` provider (token from `CLOUDFLARE_API_TOKEN`) sets
`cloudflare_r2_bucket_lifecycle.backups` on the hand-made bucket `rss-backups`: delete
objects after 35 days, and abort multipart uploads after 7 (R2's default rule, which the
resource would otherwise replace). `domain.tf` holds the `cloudflare_dns_record`s in the
`georgerodier.com` zone (variable `cloudflare_zone_id`): the DNS-only CNAME `rss` to the
service's `onrender.com` host, and Resend's records (CNAMEs `send.rss` and `rsend.rss`,
and the DKIM TXT `resend._domainkey.rss`); see "Domain and email" in the runbook. State
is in the R2 bucket `rss-tfstate` (S3 backend, `use_lockfile`); credentials come only
from the environment. CI's `terraform` job runs `fmt -check`, `init -backend=false` and
`validate`.

**Workflows** (`.github/workflows`; how to use them is under "Deploys" in
[`infra/README.md`](../infra/README.md)). All Render, R2 and Cloudflare values are
repository secrets. Every job that reads them runs only on `main`, by hand (restricted to
`main` by its environment) or on a pull request from a branch in this repository; none
uses `pull_request_target`.

- `deploy.yml` (`workflow_call` from CI's `deploy` job on pushes to `main`, after
  `image`, and `workflow_dispatch` for rollbacks; input `sha`; environment `production`;
  concurrency group `deploy`): resolves `ghcr.io/grodier/rss:sha-<sha>` to its digest
  with `docker buildx imagetools inspect`, `POST /v1/services/{id}/deploys` with
  `imageUrl` = `ghcr.io/grodier/rss@<digest>` (fails on a 202, which queues the deploy
  without returning its ID), polls the deploy until `live` (fails on
  `pre_deploy_failed`, `build_failed`, `update_failed`, `canceled` or `deactivated`),
  then checks that the service's `/healthcheck` reports `<sha>`.
- `reset-db.yml` ("Reset production database", `workflow_dispatch` with `sha` and
  `confirm` = `wipe production`; environment `production`): runs `www migrate
  -print-fingerprint` at `sha`, sets `RSS_ALLOW_RESET_TO` to it with `PUT
  /v1/services/{id}/env-vars/RSS_ALLOW_RESET_TO`, then calls `deploy.yml`.
- `infra.yml` (pull requests and pushes to `main` touching `infra/**`; concurrency group
  `terraform`): `plan -no-color` into the job summary on pull requests (not from forks
  or Dependabot), `apply -auto-approve` on `main` in the `infrastructure` environment,
  which waits for the owner's approval.

Dependabot (`.github/dependabot.yml`) proposes weekly updates for Go modules, GitHub
Actions, the base images of the `Dockerfile` and `deploy/backup/Dockerfile`, and the
Terraform providers.
