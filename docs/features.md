# Features

How each feature works and which code to read, in order. Read
[architecture.md](architecture.md) first for the overall shape and the glossary.

Each section has the flow, a reading list (file and symbol, in the order a request or job
goes through them), the tests that pin the behavior, and where the reasons behind it are
in [decisions.md](decisions.md).

## Accounts and sessions

**Flow.** Signup exists only when the app runs with `-invite-code`, and no page links to
it: people arrive through `/signup?invite=<code>`, which `signupHandler` pre-fills into
the "Invite code" field. `signupFormHandler` checks the per-IP limit, validates the form
(the code, compared with `subtle.ConstantTimeCompare`, then `validator.CheckNewAccount`
for the name, email and password, after trimming the name), and creates the user with an Argon2id hash. A
duplicate email gets exactly the same response as a new
account. Login checks the per-IP limit, validates, then counts the attempt against the
submitted email *before* checking the password (refunded unless the password was
wrong), so parallel guesses can't slip past the limit. On success `startSession` renews
the session token, creates a `user_sessions` row for this device (deleting the user's
rows idle for 90 days), stores its ID in the session as `sessionID`, and the handler
redirects to the timeline (`/`). On every later request, `authenticate` loads that row
with its user in one query. A missing row, or one unused for 90 days
(`sessionIdleTimeout`, the row is deleted), makes the request anonymous; otherwise it
updates `last_seen_at` if it's over a day old (`sessionTouchInterval`) and puts the user's
ID and the session in the request context, where handlers read them with
`s.authenticatedUserID(r)` and `s.authenticatedSession(r)`. Logging out (the Log out
button on Settings) deletes the row, renews the token and removes `sessionID`. A login lasts at most a year (scs's
`Lifetime`).

Both forms show each field's error under it, tied to the input with `aria-describedby`,
and signup shows the name and password hints there all the time. After a failed submit
the server puts `autofocus` on the first invalid field (`Validator.FirstError`); nothing
is focused when a form first loads. `app.js` adds a Show/Hide toggle to the password
inputs.

The other way accounts are made is `www create-user` (`runCreateUser`), for your local
account: it reads the password from the first line of stdin, checks the account with the
same `CheckNewAccount`, and creates it with the same `Create`. Unlike signup, it says
when the email is taken. `make db/seed` runs it with `RSS_DEV_EMAIL` and
`RSS_DEV_PASSWORD` from `.env`, and `make db/reset` runs `db/seed` when `RSS_DEV_EMAIL`
is set.

**Settings.** The nav's account entry (the user's letter avatar, labelled Settings) opens
`/settings` (`settingsHandler`): a header with the avatar, name and email, then an
"Account" section of rows (`.settings-list`), each a label, a value and, on links, a
chevron, and a Log out button at the bottom. Each row that can be changed opens its own
small page with one form. Name (`/settings/name`) is the first:
`settingsNameFormHandler` trims the name, checks it with `CheckName` (required, at most
100 characters; `CheckNewAccount` uses it too), re-renders with 422 when it's invalid,
and otherwise saves it with `UpdateName`, flashes "Your name was updated." and redirects
to `/settings`. Email is shown as plain text for now.

**Read:**

1. `internal/server/router.go`: the session group and the `requireAuthentication` group.
2. `internal/server/user.go`: `signupHandler`, `signupFormHandler`, `loginFormHandler`,
   `startSession`, `logoutHandler`; `internal/validator/account.go`: `CheckNewAccount`, which checks the
   password with `CheckNewPassword` (`password.go`: length, not the email, not on the
   embedded common-password list, `isCommonPassword`); `internal/ui/templates/login.html`
   and `signup.html` (the field pattern), `Validator.FirstError`, and the password toggle
   in `internal/ui/static/js/app.js`.
3. `internal/server/ratelimit.go` (`rateLimiter`, `Refund`) and `clientip.go`
   (`Server.clientIP`, `ipRateLimitKey`).
4. `internal/psql/users.go`: `Create` (duplicate → `rss.ErrDuplicateEmail`),
   `Authenticate` (`dummyHash` for unknown emails).
5. `internal/password/password.go`: `Hash`, `Verify` (both NFKC-normalize first), the
   `slots` cap.
6. `internal/server/middleware.go` (`authenticate`, `sessionIdleTimeout`,
   `sessionTouchInterval`, `requireAuthentication`) and `helpers.go`
   (`authenticatedUserID`, `authenticatedSession`); `internal/psql/user_sessions.go`
   (`UserSessionRepository`: `Create`, `Get`, `Touch`, `Delete`).
7. `cmd/www/application.go`: `newSessionManager`, the session manager's settings
   (`sessionLifetime`, one year).
8. `cmd/www/create_user.go`: `runCreateUser`, `readPassword`; the `db/seed` target in
   `Makefile`.
9. Settings: `internal/server/html.go` (`navData`, `nav`) and
   `internal/ui/templates/partials/nav.html` (`app-nav`, the account entry);
   `internal/server/settings.go` (`settingsHandler`, `settingsNameHandler`,
   `settingsNameFormHandler`); `internal/validator/account.go` (`CheckName`);
   `internal/psql/users.go` (`UpdateName`); `settings.html`, `settings_name.html`.

**Tests:** `internal/server/user_test.go` (`TestSignupHandlerInvitePrefill`,
`TestSignupFormHandlerInviteCode`, `TestSignupFormHandlerTrimsName`), `router_test.go`
(`TestSignupRoutes`), `settings_test.go`, `templates_test.go` (`TestNoSignupLinks`,
`TestFormFieldErrorsDescribed`, `TestFormAutofocus`, `TestSignupHints`, `TestAppNav`), `middleware_test.go`, `ratelimit_test.go`,
`clientip_test.go`; `internal/validator/account_test.go` (`TestCheckNewAccount`,
`TestCheckName`),
`password_test.go` (`TestCheckNewPassword`, `TestCommonPasswordsFile`),
`validator_test.go` (`TestFirstError`);
`cmd/www/config_test.go` (`TestParseConfigInviteCode`), `create_user_test.go`;
`internal/psql/users_test.go`, `user_sessions_test.go`; `internal/password/password_test.go`.

**Why:** "Security" and "Accounts" in decisions.md.

## Search, site lookups and discovery

**Flow.** `GET /search?q=…` only searches the database: sites whose title or host, or
one of whose feeds' title or URL, contains `q`. When `q` looks like a website address,
the page also offers a "Look up example.com" button, which posts to `/lookups`. That handler
normalizes the input (`discovery.ParseInput`), applies the per-user limit, and calls
`LookupRepository.Request`, which returns the site key's lookup, creating it or
re-queuing it if its result is older than its TTL (24 h done, 1 h failed). The handler
then polls the row for up to 3 s and redirects to the site page if the lookup found one.
Otherwise it redirects to `/lookups/{id}`, which reloads itself every 2 s while the lookup
runs, then redirects to the site or says that no feeds were found or the site couldn't
be reached.

Meanwhile a lookup worker claims the row and runs `Discoverer.Discover`: fetch the URL;
if it is itself a feed, that's the result. Otherwise read the HTML page's title,
description and `<link rel="alternate">` feeds, or, if there are none, try
`CommonFeedPaths`. Then fetch up to 15 candidates, 4 at a time, and keep up to 10 that
parse. The worker turns each feed's items into articles (`ingest.Articles`) and saves the
site, its feeds and their articles in one transaction (`DiscoveryRepository.Save`), so
a looked-up feed has articles right away. The site page lists the feeds with subscribe
buttons and offers "Check for new feeds" once the last lookup's TTL has passed.

**Read:**

1. `internal/server/search.go` (`searchHandler`) and `internal/psql/search.go` (`Search`).
2. `internal/discovery/normalize.go`: `ParseInput`, `SiteKey`, `CanonicalFeedURL`.
3. `internal/server/lookups.go`: `lookupCreateHandler`, `waitForLookup`, `lookupHandler`.
4. `internal/psql/lookups.go`: `Request`, then the worker side: `ClaimNext`, `Finish`,
   `Fail`, `ResetStale`.
5. `internal/lookup/lookup.go`: `Runner.Run`, `work`, `process`, `run`.
6. `internal/discovery/discover.go` (`Discover`, `checkCandidates`) and `page.go`
   (`ParsePage`).
7. `internal/feedparse/feedparse.go`: `Sniff`, `Parse`.
8. `internal/psql/discoveries.go` (`Save`), with `upsertSite` in `sites.go` and
   `upsertFeed` in `feeds.go`.
9. `internal/server/sites.go`: `siteHandler`, `canRecheck`.

**Tests:** `internal/server/search_test.go`, `lookups_test.go`, `sites_test.go`;
`internal/lookup/lookup_test.go`; `internal/discovery/*_test.go`;
`internal/feedparse/feedparse_test.go` (fixtures in `testdata/`);
`internal/psql/search_test.go`, `lookups_test.go`, `discoveries_test.go`,
`sites_test.go`.

**Why:** "Feed discovery" in decisions.md.

## Feed ingestion and background refresh

**Flow.** Three things fetch a feed's articles: discovery (the first save, above), the
Refresh button, and the refresh workers. The last two share one path,
`ingest.Refresher.Refresh`:

1. A conditional GET with the stored `ETag` / `Last-Modified`.
2. The outcome is recorded and the next fetch scheduled. After a success (2xx or 304) the
   feed is due again in an hour ±10% (`NextFetch`), or later if the response's
   `Cache-Control` / `Expires` says it stays fresh longer (`freshness`, capped at 24 h).
   - **304:** `RecordNotModified`.
   - **410:** `MarkGone`; not fetched again until a lookup finds the feed again.
   - **Network error, other non-2xx, or not a feed:** `RecordFailure`, with backoff
     (`RetryAt`: 1 h, 2 h, 4 h, … up to 24 h), delayed further by a 429's or 503's
     `Retry-After`.
   - **2xx feed:** parse it, turn the items into articles (`Articles`), and `SaveFetch`
     in one transaction: update the feed's metadata, validators and `next_fetch_at`, then
     upsert the articles and record the success.

Turning items into articles (`ingest.Articles`): each item's identity within its feed is
its ID, else its URL, else a hash of its text; duplicates are dropped and at most 200 are
kept. Each article gets a plain-text excerpt and a `canonical_url` (tracking parameters
removed) for matching copies across feeds. Saving (`saveArticles`) locks the feed row,
computes each new article's `timeline_at` (see Reading), and upserts by
`(feed_id, external_id)`: unchanged articles aren't written, and an existing
`published_at` is kept because some feeds bump it on every edit.

Which feeds the workers fetch: `ClaimDue` takes a due feed that has a subscriber or a
pending refresh request, requested ones first. A request is made when someone views a
stale feed's page (`feedHandler` calls `RequestRefresh`, and the page says "Checking for
new articles…"). The Refresh button instead fetches right away, within 8 s, and reports
the result in a flash message. It refuses gone feeds, feeds fetched successfully (2xx or
304) in the last 5 minutes, and users over the per-user limit.

**Read:**

1. `internal/refresh/refresh.go`: `Runner.work`, `refresh`.
2. `internal/psql/feeds.go`: `ClaimDue`, `RequestRefresh`.
3. `internal/ingest/ingest.go`: `Refresher.Refresh`, `fetch`, `record`, `Articles`,
   `externalID`.
4. `internal/ingest/schedule.go` (`NextFetch`, `RetryAt`, `retryAfter`, `freshness`) and
   `canonical.go` (`CanonicalURL`).
5. `internal/psql/feeds.go`: `SaveFetch`, `RecordFailure`, `RecordNotModified`,
   `MarkGone`.
6. `internal/psql/articles.go`: `saveArticles`, `timelineAt`.
7. `internal/server/feeds.go`: `feedRefreshHandler`, `refreshFlash`, and the
   `RequestRefresh` call in `feedHandler`.

**Tests:** `internal/ingest/*_test.go`; `internal/refresh/refresh_test.go`;
`internal/psql/feeds_test.go`, `articles_test.go`; `internal/server/feeds_test.go`.

**Why:** "Feed ingestion" in decisions.md.

## Subscriptions

**Flow.** Subscribe and Unsubscribe buttons on the feed and site pages (and Unsubscribe on
My feeds) post to
`/feeds/{id}/subscribe` or `/unsubscribe` with a `return_to` path; the handler changes
the subscription, flashes a message, and redirects back (`safeReturnPath` keeps it
local). Gone feeds can't be subscribed to but can be unsubscribed from. "My feeds"
(`/feeds`) lists the user's subscriptions grouped by site, with each feed's latest
article date and a warning after 3 failed fetches in a row. Site, feed and search pages
mark the feeds the user subscribes to with one extra query (`SubscribedFeedIDs`).
Subscriptions also decide which feeds are refreshed in the background and what's in the
timeline.

**Read:**

1. `internal/server/feeds.go`: `feedSubscribeHandler`, `feedUnsubscribeHandler`,
   `subscriptionTarget`, `redirectWithFlash`, then `feedsHandler`.
2. `internal/server/helpers.go`: `safeReturnPath`.
3. `internal/psql/subscriptions.go`: `Subscribe`, `Unsubscribe`, `SubscribedFeedIDs`,
   `ListByUser`.
4. Templates: `feed.html`, `site.html`, `feeds.html`.

**Tests:** `internal/server/feeds_test.go`, `sites_test.go`, `search_test.go`;
`internal/psql/subscriptions_test.go`.

**Why:** "Subscriptions" in decisions.md.

## Reading: timeline, article pages and read state

**Timeline** (`/` when logged in). `homeHandler` asks `ListTimeline` for 31 entries to
know whether there's a next page, and shows 30. `ListTimeline` joins the user's
subscriptions to their feeds' articles, collapses copies of one article (same
`canonical_url`) into the earliest to arrive, orders by `(timeline_at, id)` descending,
and pages with a keyset cursor: `?before=<time>_<id>` (`encodeCursor`, `decodeCursor`).
`fillAlsoIn` then lists the other subscribed feeds that carry each entry. Feed pages work
the same way with `ListByFeed`, but in publication order and without collapsing copies.
List queries never load `summary` or `content`.

**Timeline position.** `timelineAt` sets `timeline_at` once, when an article is first
saved. *News* (undated, or published since a day before the feed's previous successful
fetch) goes at the save time, 1 µs apart, newest-published first. *Backlog* (every dated
item on a feed's first save, or older items appearing later) goes at its published date,
or now if that's in the future. So new items always land on top, and an imported archive
doesn't.

**Article page.** `articleHandler` records the read, then renders the content (else the
summary) through `sanitize.HTML`, with relative URLs resolved against the article's URL
(else the feed's).

**Read state.** An article is read when its page is opened, or when the reader clicks
through to the original from a row: the link has `ping="/articles/{id}/read"`, which
`app.js` replaces with `navigator.sendBeacon` because Firefox ignores `ping`. List pages
ask `ReadArticleIDs` which rows are read (matching any copy with the same
`canonical_url`) and the `article-row` partial dims them.

**Infinite scroll.** `app.js` watches the "Older articles" link; when it nears the
viewport it fetches that page, appends its rows, and swaps in the new pager. It keeps the
address bar on the topmost visible row (`history.replaceState`), so reload and Back land
nearby. Without JavaScript the link just works.

**Read:**

1. `internal/server/home.go`: `homeHandler`; `cursor.go`.
2. `internal/psql/articles.go`: `ListTimeline`, `fillAlsoIn`, `ListByFeed`,
   `articleListColumns`, then `timelineAt`.
3. `internal/server/articles.go`: `articleHandler`, `articleReadHandler`, `articleRow`,
   `fillRead`.
4. `internal/psql/reads.go`: `MarkRead`, `ReadArticleIDs`.
5. `internal/sanitize/sanitize.go`: `HTML`, `prepare`, `rewrite`, `newPolicy`.
6. Templates: `timeline.html`, `article.html`, `partials/article_row.html`.
7. `internal/ui/static/js/app.js`.
8. Also: `rss.Article.DisplayTitle` (untitled articles) and `FeedSortAt` in
   `internal/rss/rss.go`.

**Tests:** `internal/server/home_test.go`, `articles_test.go`, `cursor_test.go`,
`scroll_test.go`; `internal/psql/articles_test.go`, `reads_test.go`;
`internal/sanitize/sanitize_test.go`.

**Why:** "Reading" and "Design" in decisions.md.

## Adding a feature

The usual path for a new page or action, following the conventions in CLAUDE.md:

1. **Schema:** edit `migrations/00001_init.sql` and run `make db/reset`.
2. **Domain type:** add or extend a type in `internal/rss`, and a sentinel error in
   `errors.go` if handlers need to tell a case apart.
3. **Repository:** a method in `internal/psql` with a DB test.
4. **Interface:** add the method to the store interface in `internal/server/server.go`
   (or a new interface, a `Services` field, the compile-time check, and the wiring in
   `cmd/www/application.go`), and a function field to the fake in `fakes_test.go`.
5. **Handler and route:** a method on `*Server`, registered in `router.go` (see "Code
   conventions" in CLAUDE.md for forms, flash messages and error pages).
6. **Template:** a page in `internal/ui/templates`, with shared parts in `partials/`.
7. **Tests:** handler tests with fakes for the new behavior, and DB tests for the
   repository method.
8. **Docs:** see "Docs" in CLAUDE.md for which files to update.
