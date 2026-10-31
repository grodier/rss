# Features

How each feature works and where to start reading.
Read [architecture.md](architecture.md) first for the overall shape and the glossary.

Each section has the flow (what happens, and the parts that aren't obvious from the code), where to start reading (a few entry points, in the order a request or job reaches them; follow the calls from there), the test files that pin the behavior, and where the reasons are in [decisions.md](decisions.md).
User-facing strings, CSS classes and every helper's name live in the code and templates, and the tests check them; this file doesn't repeat them.

## Accounts and sessions

**Signup and login.** Signup exists only when the app runs with `-invite-code`, and no page links to it: people arrive through `/signup?invite=<code>`, which pre-fills the code.
`signupFormHandler` checks the per-IP limit, then the code (compared in constant time), then the name, email and password with `validator.CheckNewAccount`, and creates the user with an Argon2id hash.
A taken email gets an error on the email field with links to log in or reset the password.
Login checks the per-IP limit, then counts the attempt against the submitted email *before* checking the password (refunded unless the password was wrong), so parallel guesses can't slip past the limit.
`Authenticate` tells an unknown email (`rss.ErrNoAccount`) from a wrong password (`rss.ErrWrongPassword`), and each gets its own field error; the login page links to password reset, with the typed address.

On a successful login or signup, `startSession` renews the session token, creates a `user_sessions` row for this device (deleting the user's rows idle for 90 days) and stores its ID in the session.
The handler then redirects to `popReturnTo`: `requireAuthentication` remembers an anonymous GET or HEAD's URI as `returnTo` before sending it to `/login`, and `safeReturnPath` keeps it local.
Other methods aren't remembered, since replaying one as a GET would give a 405.
A logged-in user who opens `/login` or `/signup` is sent to the timeline.

**Sessions.** On every later request, `authenticate` loads the device's `user_sessions` row with its user in one query.
A missing row, or one unused for 90 days (`sessionIdleTimeout`; the row is deleted), makes the request anonymous; otherwise it updates `last_seen_at` if it's over a day old and puts the user and session in the request context (`s.authenticatedUserID(r)`, `s.authenticatedSession(r)`).
Logging out deletes the row and renews the token.
A login lasts at most a year (scs's `Lifetime`).

Both forms follow the field pattern in CLAUDE.md, and signup shows the name and password hints all the time.
The other way to make an account is `www create-user` (`runCreateUser`), for your local account: it reads the password from stdin and uses the same checks and `Create`; `make db/seed` runs it from `.env`, and `make db/reset` runs `db/seed` when `RSS_DEV_EMAIL` is set.

**Settings.** The nav's account entry opens `/settings`: the account's name and email, then rows that each open a small page with one form.
Name trims and checks the name (`CheckName`, also used by signup).
Theme ("Appearance") offers System, Light or Dark as radio buttons; `UserRepository.UpdateTheme` stores it in `users.theme`, which `authenticate` loads with the session, so every logged-in page then renders it as `data-theme` on `<html>` (see "Frontend" in [architecture.md](architecture.md)).
The Email row shows "Not confirmed" until the address is confirmed (see "Changing email").
Password asks for the current and the new password; `UserRepository.ChangePassword` verifies the current one before starting a transaction (Argon2 is slow), then in one transaction stores the new hash and deletes the user's other sessions, keeping this device's.
The handler queues the `password_changed` email, logging rather than failing if it can't.
`/.well-known/change-password` redirects there, for password managers.
"Log out of all other devices" needs no password, since it can only protect.
"Your data" has the subscriptions export and import (see "Subscriptions"); "Help" has the support address (`Config.SupportEmail`, left out when unset) and the policy pages.

**Deleting an account.** `/settings/delete` says what goes (the account, subscriptions, reading history) and what stays (feeds and articles, which are shared), offers the OPML export first, and asks for the password.
`settingsDeleteFormHandler` checks the per-user limit, then `UserRepository.Delete` verifies the password outside any transaction and deletes the `users` row, which cascades to the user's subscriptions, reads, sessions and tokens.
Deletion is immediate.
The handler queues the `account_deleted` email to the old address (the outbox has no foreign key to users), ends the session and redirects to the logged-out home page with a flash.

**Privacy policy and terms.** `/privacy` and `/terms` are logged-out pages whose text is HTML in `privacy.html` and `terms.html`, with a "Last updated" date to change with the text.
They're linked from signup, login, the logged-out home page, Settings and every email's footer.

**Email.** A handler that sends email calls `s.sendEmail(ctx, to, name, data)`, which renders `internal/ui/emails/<name>.tmpl` (links built with `s.absoluteURL` from the base URL, never from the request) and queues it with `mail.Queue.Enqueue`.
That inserts it into the `emails` outbox and wakes this process's `mail.Runner`, so the page doesn't wait on the provider.
Over the per-address cap, the email isn't queued and `sendEmail` logs a warning and returns nil.
A runner worker claims the email, sends it and records the outcome: sent, retried later (`retryDelay`), or given up.
The cap, retries, lease and retention are under "Background work" and "Security measures" in [architecture.md](architecture.md).

**Email confirmation.** Confirmation is soft: nothing requires it yet, except sharing a timeline.
After signup, `sendConfirmation` makes a token with `token.New` (only its SHA-256 is stored), saves it (purpose `confirm_email`, the address it's sent to, 7 days) and queues the link `<base URL>/confirm-email?token=<token>`.
If that fails, signup still succeeds and the error is logged.
Asking again doesn't cancel earlier links, since emails arrive out of order.
Until the address is confirmed, the timeline and Settings show a notice with a Resend button (`confirmResendHandler`, 3 per user per hour).

The link works in any browser, logged in or not.
`GET /confirm-email` sets `Referrer-Policy: no-referrer` and `Cache-Control: no-store`, and `TokenRepository.Get` only returns an unexpired token whose address is still the account's.
A valid token gets a page with a Confirm button that posts it back: the GET never writes, so a mail scanner that opens the link can't confirm.
`UserRepository.ConfirmEmail` then, in one transaction, deletes the token (checking the same conditions), sets `email_confirmed_at` (keeping an earlier time) and deletes the user's other confirmation tokens.
An unknown, expired, used or outdated token gets the expired-link page (410), built by `linkExpiredPage`, which the other email links reuse.

**Password reset.** `/password/forgot` asks for the account's email (prefilled from `?email=`), checks the per-IP and per-email limits, finds the account (an unknown email gets a field error), and `sendPasswordReset` queues a link with a 1-hour token.
The address is kept in the session as `resetSentTo` for the "sent" page, which can send the link again.
Both pages show the support address for people who can't get into their email.
`GET /password/reset` checks the token like confirmation (no-referrer, no-store, no writes) and shows a new-password form with the token's address read-only.
`UserRepository.ResetPassword` hashes the password, then in one transaction deletes the token, sets the hash, confirms the email (receiving the link proves the address), and deletes the user's other reset tokens and every session.
The handler then starts a session here and queues `password_changed`.

**Changing email.** `/settings/email` asks for the new address and the current password, checks the per-user limit, that the address is new and free, and the password (`CheckPassword`), then sends a 24-hour link to the **new** address.
Nothing changes until the link is used.
`GET /email-change` checks the token (for `change_email` the address check is skipped, since the address is the new one) and shows a Change button; `UserRepository.ChangeEmail` in one transaction deletes the token, sets the new address as confirmed and deletes the user's other change tokens.
If another account took the address in the meantime (`rss.ErrDuplicateEmail`), nothing changes and the page re-renders with 409.
On success the old address gets `email_changed`; sessions stay.
Links sent to the old address stop working by themselves, since tokens are only accepted while the account still has the address they were sent to.

**Start at:**

1. `internal/server/router.go`: the session group and the `requireAuthentication` group.
2. `internal/server/user.go` (signup, login, `startSession`, `popReturnTo`) and `internal/validator/account.go` (`CheckNewAccount`).
3. `internal/server/middleware.go` (`authenticate`, `requireAuthentication`) and `internal/psql/user_sessions.go`.
4. `internal/server/settings.go`, then `confirm.go`, `password_reset.go` and `email_change.go`, with `internal/psql/users.go` and `tokens.go`.
5. Email: `internal/server/email.go` (`sendEmail`), then `internal/mail` (`Queue`, `Runner`, `ParseTemplates`).
6. `internal/password/password.go`, `internal/token/token.go`, and `cmd/www/create_user.go`.

**Tests:** in `internal/server`, `user_test.go`, `settings_test.go`, `confirm_test.go`, `password_reset_test.go`, `email_change_test.go`, `email_test.go`, `policy_test.go`, `middleware_test.go`, `ratelimit_test.go`, `clientip_test.go`, `router_test.go` and `templates_test.go` (the field pattern and the nav); `internal/psql/users_test.go`, `user_sessions_test.go`, `tokens_test.go`, `emails_test.go`; `internal/validator`, `internal/password`, `internal/token` and `internal/mail`; `cmd/www/config_test.go` and `create_user_test.go`.

**Why:** "Security" and "Accounts" in decisions.md.

## Search, site lookups and discovery

**Flow.** `GET /search?q=…` only searches the database: sites whose title or host, or one of whose feeds' title or URL, contains `q`.
When `q` is a website address (`discovery.ParseInput`, which drops whitespace next to `.`, `/` or `:` so `npr. org` counts), the search also matches its site key exactly and ranks that site first, and the page offers a Look up button, which posts the cleaned-up address to `/lookups`.
That handler normalizes the input (`discovery.ParseInput`), applies the per-user limit, and calls `LookupRepository.Request`, which returns the site key's lookup, creating it or re-queuing it if its result is older than its TTL (24 h when it found a site, 1 h when it failed or found no feeds), or at once when the address differs from the one it ran on, unless it found a site and the address is that site's home page.
It then waits up to 3 s for the row and redirects to the site page if the lookup found one; otherwise to `/lookups/{id}`, which reloads itself every 2 s while the lookup runs, then redirects to the site or says what went wrong.

Meanwhile a lookup worker claims the row and runs `Discoverer.Discover`: fetch the URL; if it is itself a feed, that's the result.
Otherwise read the page's title, description and `<link rel="alternate">` feeds, or, if there are none, try `CommonFeedPaths`.
Then fetch up to 15 candidates, 4 at a time, and keep up to 10 that parse.
`Result.Checked` records every candidate and its outcome, for the debug inspector; if the deadline ends the checking, `Discover` returns what it has alongside the error, and the runners ignore the result on any error.
The worker turns each feed's items into articles (`ingest.Articles`) and saves the site, its feeds and their articles in one transaction (`DiscoveryRepository.Save`), so a looked-up feed has articles right away.
The site page lists the feeds with subscribe buttons and offers "Check for new feeds" once the last lookup's TTL has passed.
(`ParsePage` also reads a page's title, image and icon links, for the icon worker and enrichment.)

**Start at:**

1. `internal/server/search.go` and `internal/psql/search.go`.
2. `internal/server/lookups.go` (`lookupCreateHandler`, `lookupHandler`) and `internal/psql/lookups.go`.
3. `internal/lookup/lookup.go` (`Runner`).
4. `internal/discovery/discover.go` (`Discover`), `page.go` (`ParsePage`) and `normalize.go` (`ParseInput`, `SiteKey`, `CanonicalFeedURL`).
5. `internal/feedparse/feedparse.go` (`Sniff`, `Parse`), then `internal/psql/discoveries.go` (`Save`).
6. `internal/server/sites.go` (`siteHandler`).

**Tests:** `internal/server/search_test.go`, `lookups_test.go`, `sites_test.go`; `internal/lookup`; `internal/discovery`; `internal/feedparse` (fixtures in `testdata/`); `internal/psql/search_test.go`, `lookups_test.go`, `discoveries_test.go`, `sites_test.go`.

**Why:** "Feed discovery" in decisions.md.

## Feed ingestion and background refresh

**Flow.** Three things fetch a feed's articles: discovery (the first save, above), the Refresh button, and the refresh workers.
The last two share one path, `ingest.Refresher.Refresh`:

1. A conditional GET with the stored `ETag` / `Last-Modified`.
2. The outcome is recorded and the next fetch scheduled.
   After a success (2xx or 304) the feed is due again in an hour ±10% (`NextFetch`), or later if `Cache-Control` / `Expires` says it stays fresh longer (capped at 24 h).
   - **304:** `RecordNotModified`.
   - **410:** `MarkGone`; not fetched again until a lookup finds the feed again.
   - **Network error, other non-2xx, or not a feed:** `RecordFailure`, with backoff (`RetryAt`: 1 h, 2 h, 4 h, … up to 24 h), delayed further by a 429's or 503's `Retry-After`.
   - **2xx feed:** parse it, turn the items into articles, and `SaveFetch` in one transaction: the feed's metadata, validators and `next_fetch_at`, then the articles and the success.

Turning items into articles (`ingest.Articles`): each item's identity within its feed is its ID, else its URL, else a hash of its text; duplicates are dropped and at most 200 are kept.
Each article gets a plain-text excerpt, a `canonical_url` (tracking parameters removed) for matching copies across feeds, and the MIME type of its first audio or video enclosure (`media_type`).
Its `authors` are the names `feedparse.itemAuthors` finds: every `dc:creator` for RSS (gofeed keeps only the first), else gofeed's single pick (`<author>`, `itunes:author`, …), and every author for Atom and JSON Feed, with email addresses reduced to their name or dropped.
What `Parse` reads is also listed by hand in `usedChannelFields` and `usedItemFields` (`feedparse/inspect.go`) for the feed inspector, so a change to `Parse` updates them too.
Saving (`saveArticles`) locks the feed row, sets each new article's `timeline_at` (see "Reading"), and upserts by `(feed_id, external_id)`: unchanged articles aren't written, and an existing `published_at` is kept because some feeds bump it on every edit.
A newly inserted article missing an image, title or excerpt gets an enrichment job in the same transaction (see "Article enrichment").

`feeds.posts_per_week` is the feed's articles published in the last 30 days (undated ones count from when they were saved) × 7 ÷ 30 (`postsPerWeek`), recomputed at the end of every `saveArticles` and by `RecordNotModified`, so the window keeps sliding for feeds that stopped changing.

Which feeds the workers fetch: `ClaimDue` takes a due feed that has a subscriber or a pending refresh request, requested ones first.
A request is made when someone views a stale feed's page (`feedHandler` calls `RequestRefresh`).
The Refresh button instead fetches right away, within 8 s, and reports the result in a flash; it refuses gone feeds, feeds fetched successfully in the last 5 minutes, and users over the per-user limit.

**Start at:**

1. `internal/refresh/refresh.go` (`Runner`) and `ClaimDue` in `internal/psql/feeds.go`.
2. `internal/ingest/ingest.go` (`Refresher.Refresh`, `Articles`, `ExternalID`), `schedule.go` and `canonical.go`.
3. `internal/psql/feeds.go` (`SaveFetch`, `RecordFailure`, `RecordNotModified`, `MarkGone`) and `saveArticles` in `articles.go`.
4. `internal/server/feeds.go` (`feedRefreshHandler`, and `RequestRefresh` in `feedHandler`).

**Tests:** `internal/feedparse`; `internal/ingest`; `internal/refresh`; `internal/psql/feeds_test.go`, `articles_test.go`; `internal/server/feeds_test.go`.

**Why:** "Feed ingestion" in decisions.md.

## Subscriptions

**Flow.** Subscribe and Unsubscribe buttons on the feed and site pages (and Unsubscribe on My feeds) post to `/feeds/{id}/subscribe` or `/unsubscribe` with a `return_to` path, and redirect back with a flash.
Gone feeds can't be subscribed to but can be unsubscribed from.
My feeds (`/feeds`) lists the user's subscriptions grouped by site, with each feed's latest article date, posting rate (`postingRate`, from `posts_per_week`) and a warning after 3 failed fetches in a row.
Site, feed and search pages mark subscribed feeds with one extra query (`SubscribedFeedIDs`).
Subscriptions also decide which feeds are refreshed in the background and what's in the timeline.

**Names.** A subscription can have the user's own name for the feed (`subscriptions.title`, private to them; `""` means "use the feed's title", so a feed that changes its title still updates).
Edit subscription (`/feeds/{id}/edit`) has a Name field and the subscription's tags (see "Tags"); a blank name, or one equal to the feed's current title, is stored as `""`.
The name shows wherever the user's own reading shows the feed: per-user list queries (`ListTimeline`, `fillAlsoIn`, `ListSaved`) select `subscriptionTitle` as the feed's title, so rows and "Also in" show it without template changes, and the feed page, the article page and the OPML export use it too.
Catalog pages (site, search) and debug panels keep the feed's own title.
Unsubscribing drops the name and the subscription's tags.

**Export.** `/settings/export` asks whether to turn tags into folders (the default) or ignore them, then downloads `/settings/subscriptions.opml` (`exportOPMLHandler`): every subscription, gone feeds included, as OPML 2.0, one `<outline>` per feed with the user's name for it.
With folders, feeds are nested in a folder per tag (a feed with two tags in both), then the untagged feeds at the top level.
It's encoded into a buffer first, so an error is an HTML 500 instead of half a file.

**Import.** `/settings/import` uploads an OPML file, with a choice to turn folders into tags (the default), and lists the user's last 5 imports.
`settingsImportFormHandler` caps the body with `http.MaxBytesReader` (not `decodePostForm`), parses the file with `opml.Parse`, and re-renders with an error on the file field for no file (422), over 1 MiB (413), not OPML, no feeds or over 1,000 (422), or a sixth import within the hour (429).
Otherwise it turns each feed's folders into tags (`importTags`: normalized like typed tags, cut to 50 characters, repeats dropped), creates the import and redirects to `/settings/imports/{id}`, which refreshes itself while feeds are pending and then lists what was subscribed, already subscribed, or couldn't be added and why.

`opml.Parse` reads every `<outline>` with an `xmlUrl`, at any depth, in file order and without repeats; each feed's folders are those it's nested in, at any depth.
It parses non-strictly and decodes legacy charsets, since older readers' exports are sloppy.
`ImportRepository.Create` stores one pending `import_feeds` row per feed, with its tags.
The import runner (`importer.Runner`) claims one row at a time, oldest import first: it parses the URL, looks up its `CanonicalFeedURL`, runs discovery for an unknown or gone feed (saved exactly like a lookup), then subscribes or records `already_subscribed`, and adds the row's tags (`TagRepository.AddToSubscription`: reuses the user's tags by name, creates the rest up to the cap, never removes one).
A tag error is only logged: the feed stays subscribed.
Failures aren't retried; a crashed row is re-queued by `ResetStale`.
Subscribing bumps nothing onto the timeline: the backlog keeps its published dates.

**Start at:**

1. `internal/server/feeds.go`: subscribe and unsubscribe, `feedsHandler`, and the subscription edit handlers.
2. `internal/psql/subscriptions.go`.
3. Export: `exportOPMLHandler` in `internal/server/settings.go`.
4. Import: `internal/server/import.go`, `internal/opml/opml.go`, `internal/psql/imports.go`, then `internal/importer/importer.go`.

**Tests:** `internal/server/feeds_test.go`, `articles_test.go`, `sites_test.go`, `search_test.go`, `settings_test.go`, `import_test.go`; `internal/psql/subscriptions_test.go`, `articles_test.go`, `saves_test.go`, `imports_test.go`, `tags_test.go`; `internal/opml`; `internal/importer`.

**Why:** "Subscriptions" and "Accounts" in decisions.md.

## Tags

**Flow.** `/tags` (linked from My feeds) has a New tag form and the user's tags with their feed counts.
A name is normalized with `rss.TagName` (trimmed, inner whitespace collapsed) and must be non-blank, at most 50 runes and without commas (Edit subscription separates new tags with commas).
`Create` reports `ErrDuplicateTag` for a name the user already has, ignoring case (`citext`), and `ErrTooManyTags` at 100.

A tag's page (`/tags/{id}`; another user's tag is a 404) is one form with a checkbox per subscription.
Saving posts the ticked feeds; `tagFeedsHandler` drops any the user no longer subscribes to, and `SetFeeds` in one transaction removes the tag from the other subscriptions and adds it to these, leaving their other tags alone.
Rename uses the same checks; Delete (no confirmation) cascades to `subscription_tags` and `timeline_tags`.
All of it works without JavaScript.

**On a subscription.** Edit subscription has a checkbox per tag and a New tags field for comma-separated names.
The post keeps only the user's own tag IDs (a tag deleted in another tab is ignored), splits and normalizes the new names, and calls `SetForSubscription` before renaming, so a refusal changes nothing.
In one transaction that checks the subscription exists, refuses names that would pass the cap, creates new names with `INSERT … ON CONFLICT (user_id, name) DO UPDATE` (so an existing name, ignoring case, reuses that tag), and replaces the subscription's tags.
`ListByUser` and `Get` fill `Subscription.Tags` with one more query each (`tagsByFeed`), and My feeds shows each feed's tags as links.
Unsubscribing cascades to the subscription's tags; the tags themselves stay.

**Start at:**

1. `internal/server/tags.go`, and the subscription edit handlers in `internal/server/feeds.go`.
2. `internal/psql/tags.go`, and `tagsByFeed` in `subscriptions.go`.
3. `Tag`, `TagName` and the tag errors in `internal/rss`.

**Tests:** `internal/server/tags_test.go`, `feeds_test.go`; `internal/psql/tags_test.go`, `subscriptions_test.go`; `internal/rss/rss_test.go`.

**Why:** "Stars and tags" under "Filtering and timelines" in decisions.md.

## Timelines

**Flow.** Home and each custom timeline are headed by a tab row (from `TimelineRepository.List`): Home, the user's timelines in tab order, then New timeline (or Timelines when there are some); `app.js` scrolls the row so the current tab shows.
A timeline is a name, an optional description and an `rss.TimelineFilter` over the user's subscriptions:

- **Show:** all feeds, or (`OnlyIncluded`) only those with one of `IncludeTagIDs` or in `IncludeFeedIDs`.
- **Leave out** (`ExcludeTagIDs`, `ExcludeFeedIDs`) always wins.
- **Only show:** `StarredOnly` (the user's stars); `MaxPostsPerWeek` (feeds whose 30-day `posts_per_week` is at most that, so a feed that gets busier takes its older articles with it); `MediaOnly` (articles with an audio or video enclosure; embedded videos don't count); `IncludeWords` (articles mentioning one of its words or phrases); `ExcludeWords` (left out even when they also mention an include word).

Tag-based timelines stay current, since the filter reads `subscription_tags` on every page.

Words and phrases match whole words, ignoring case, in `concat_ws(' ', title, page_title, excerpt)` (never the full content).
`wordsPattern` turns a list into one Postgres regular expression for `~*`, `(?<![[:alnum:]])(?:…|…)(?![[:alnum:]])`, with each ASCII character that isn't a letter or digit escaped (so `C++`, `.NET` and `a|b` match literally) and each run of whitespace as `\s+`.
So `AI` matches "AI's" and "#AI" but not "said", and `C` matches "C++" too (the accepted trade-off).
`[[:alnum:]]` follows the database's `LC_CTYPE`, so in a UTF-8 database `café` doesn't match "cafés".

`/timelines` lists them in tab order with Edit links and Move up and Move down buttons; `TimelineRepository.Move` locks the user's timelines and renumbers them 0…n−1 with the timeline swapped with its neighbour, which also repairs ties and gaps from deletes.
Home is always first and can't move or be edited.
New timeline and Edit render one form, `timeline_form.html`, with Show, Leave out and Only show fieldsets.
New timeline offers presets (`?preset=starred`, `?preset=quiet` for at most 2 posts a week), and a tag's page links to it with `?tag={id}`.
`timelineForm.check` keeps only the user's own tags and feeds and validates the fields (limits are in the code and its tests); words are split on commas by `splitTerms`.
`ErrTooManyTimelines` (20) is a form error; another user's timeline is a 404 (`timelineFromURL`).

A timeline's page and Home (the zero `rss.Timeline`) share `renderTimeline` (see "Reading").
`ListTimeline` applies `timelineWhere`'s conditions to the earlier copy it looks for as well as to the entry, so copies collapse after filtering and a left-out feed's copy never stands in for an entry; "Also in" still lists every subscribed feed with a copy.
A starred-only timeline lists stars instead (`StarRepository.ListStarred`), newest starred first, paged by `starred_at`, without "Also in".
It LEFT JOINs `subscriptions`, so with Show all a star from a feed the user has since left still shows; `timelineWhere` handles the missing subscription (Show conditions fail, Leave out conditions pass).

**Sharing.** A custom timeline can be shared as an Atom feed, with a landing page at the same link; sharing needs a confirmed email.
Its edit page's Sharing section shares it (`TimelineRepository.Share`, a `token.Random` token), shows the feed and page links (`sharedURLs`, built from the base URL), and offers New link (`NewShareLink`) and Stop sharing (`Unshare`).

The public routes are outside the session group.
`sharedTimelineFromURL` 404s a malformed token or one `GetShared` doesn't know.
The page (`sharedTimelineHandler`) shows the name and description, how to follow it, and `<link rel="alternate">` to the feed; it reads no articles.
The feed (`sharedFeedHandler`) reads the newest 50 with `ArticleRepository.ListShared`: the same query as the timeline (or starred list), with `shared` set, so feeds keep their own titles (never the owner's names) and articles without a URL are left out.
`sharedAtomFeed` builds the feed: the page URL as its ID, each entry's ID its canonical URL (else `urn:uuid:{id}`), an `<author>` for each of the article's authors, and a `<source>` naming its feed.
It's sent with an `ETag` and `Last-Modified` (a matching `If-None-Match` is a 304), `X-Robots-Tag: noindex` and `Cache-Control: public`.

**Start at:**

1. `internal/server/home.go` (`homeHandler`, `renderTimeline`, `timelineRows`).
2. `internal/server/timelines.go` (the handlers and `timelineForm`).
3. `internal/psql/timelines.go`, then `timelineWhere`, `ListTimeline` and `ListShared` in `articles.go`, and `ListStarred` in `stars.go`.
4. `internal/server/shared.go`.

**Tests:** `internal/server/timelines_test.go`, `shared_test.go`, `home_test.go`, `policy_test.go`; `internal/psql/timelines_test.go`, `articles_test.go`, `stars_test.go`; `internal/token`.

**Why:** "Timelines" and "Sharing" under "Filtering and timelines" in decisions.md.

## Reading: timeline, article pages and read state

**Timeline.** `renderTimeline` asks `ListTimeline` for 31 entries to know whether there's a next page, and shows 30.
`ListTimeline` reads, for each of the user's subscriptions (those the timeline's filter shows), that feed's newest entries before the cursor from `articles_feed_timeline_idx`, merges them by `(timeline_at, id)` descending and keeps the page, which it pages with a keyset cursor, `?before=<time>_<id>` (`cursor.go`).
Copies of one article (same `canonical_url`) collapse into the earliest to arrive: an entry is skipped when an earlier copy is in one of the user's feeds, a check only articles flagged `has_copies` at ingest (`saveArticles`) get.
`fillAlsoIn` then lists the other subscribed feeds that carry each entry.
Feed pages work the same way with `ListByFeed`, but in publication order and without collapsing copies.
List queries never load `summary` or `content`.
Times are rendered in UTC for `app.js` to localize (the `local-time` and `local-date` partials).

**Card values.** Rows and the article page show an article through accessors on `rss.Article`, so every page agrees: `CardImageURL` (the feed's image, else the page's, over https), `DisplayTitle` (the title, else the page's title, else the start of the excerpt) and `DisplayExcerpt` (the excerpt, else the page's description).
`Byline` joins the article's authors ("A, B and C") for the article page's dateline; rows use `ShortByline` ("A and 4 others" past two, the full list in its `title`) before the source link.
The page's values come from enrichment and only fill gaps.

**Timeline position.** `timelineAt` sets `timeline_at` once, when an article is first saved.
*News* (on a later save: undated, or published since a day before the feed's previous successful fetch) goes at the save time, 1 µs apart, newest-published first.
*Backlog* (every item on a feed's first save, or older items appearing later) goes at its published date, or now if that's in the future.
A first save's undated items are spaced out at the feed's pace below the item before them in its document, the newest at least a day back (`placeUndated`).
So new items always land on top, and an imported archive, dated or not, doesn't.

**Article page.** `articleHandler` records the read and loads the user's subscription to the feed, if any, for its name.
The "Read on" button (`readOnLabel`) names the article's domain when it's on another site, else the feed.
The body is the content, else the summary, through `sanitize.HTML` (relative URLs resolved against the article's URL); with neither, the page's description as plain text.
`sanitize.HTML` also splits plain text into paragraphs and links bare URLs, turns YouTube and Vimeo iframes into a click-to-play preview (`app.js` swaps in the sandboxed player; without JavaScript the link opens the video), and turns any other iframe into a link, and makes `http:` images load over `https:`.
The card image shows above the text unless `sanitize.ShowsImage` finds the content already shows it.

**Read state.** An article is read when its page is opened, or when the reader clicks through to the original from a row: the link has `ping="/articles/{id}/read"`, which `app.js` replaces with `navigator.sendBeacon` because Firefox ignores `ping`.
List pages ask `ReadArticleIDs` which rows are read (matching any copy with the same `canonical_url`), and the row is shown quieter.

**"You were here."** A timeline's first page (not starred-only) carries its first row's `timeline_at` in `.article-list[data-seen-up-to]`, and `app.js` sends it to `POST /timeline/seen` with a beacon each time the page is hidden.
`MarkSeen` keeps the newest position reported (never later than now) and when; a report after `rss.VisitGap` without one starts a new visit, whose divider becomes where the previous visit ended (`divider_at`).
At render, `TimelineMark.Divider` picks the visit's divider, and `fillDivider` puts the line above the first row at or before it, only when something newer is above: an earlier row, or the previous page (the cursor).
A store error renders the page without the line.

**Infinite scroll.** `app.js` watches the "Older articles" link; when it nears the viewport it fetches that page, appends its rows (and the "You were here" line, in place) and swaps in the new pager.
It keeps the address bar on the topmost visible row (`history.replaceState`), so reload and Back land nearby.
Without JavaScript the link just works.

**Start at:**

1. `internal/server/home.go` (`renderTimeline`, `fillDivider`, `timelineSeenHandler`) and `cursor.go`.
2. `internal/psql/articles.go` (`ListTimeline`, `fillAlsoIn`, `ListByFeed`, `timelineAt`).
3. `internal/server/articles.go` (`articleHandler`, `articleRow`) and `internal/psql/reads.go` (including `MarkSeen`, `TimelineMark`), with `rss.TimelineMark`.
4. `internal/sanitize/sanitize.go` (`HTML`, `ShowsImage`).
5. `partials/article_row.html` and `internal/ui/static/js/app.js`.

**Tests:** `internal/server/home_test.go`, `articles_test.go`, `cursor_test.go`, `scroll_test.go`; `internal/psql/articles_test.go`, `reads_test.go`; `internal/sanitize`; `internal/rss/rss_test.go`.

**Why:** "Reading" and "Design" in decisions.md.

## Saved articles

**Flow.** Every article row and the article page has a Save button (the `save-button` partial): a form posting to `/articles/{id}/save`, or `/unsave`, with a `return_to` of the page.
The handlers (both `toggleAction`) redirect back with `#article-{id}`, so without JavaScript the page comes back at the same row; with JavaScript, `app.js` posts the form with `fetch`, which the handlers tell apart by its `Sec-Fetch-Mode` header and answer with a `204`, and flips the button in place (`aria-pressed` carries the state); on anything but a `204`, such as the redirect to Login when the session has ended, it submits the form normally.
Saving is quiet: no flash, and it doesn't mark the article read.

**Copies.** Saving covers every copy of an article (same non-empty `canonical_url`), like read state: `Save` inserts nothing when a copy is already saved, `Unsave` removes every copy's save, and `SavedArticleIDs` matches copies.

**Stars.** A star means "this is good" (Saved stays "keep").
The Star button sits next to Save and works the same way (`toggleAction`); `StarRepository` mirrors `SaveRepository`.
Stars are private, with no counts; starred-only timelines list them (see "Timelines").

**Share.** Between Star and Save, an icon-only Share button (the `share-button` partial) carries the article's `CanonicalURL` and `DisplayTitle`; an article without a canonical URL has none.
It's rendered `hidden` and "Share buttons" in `app.js` shows it, so it never appears without JavaScript.
A click opens the system share sheet (`navigator.share`) where there is one, else copies the link and announces "Link copied" in a polite live region.
It never talks to the server: nothing about a share is recorded.

**Saved page** (`/saved`). Modelled on the timeline: `ListSaved` returns the user's saves newest first, paged by `(saved_at, article_id)` (served by `saves_user_saved_at_idx`), and left-joins the user's subscriptions so a named feed shows its name.
Unsaving from the page keeps the row until reload when JavaScript is on, so an accidental unsave can be undone in place.
Search (`/saved?q=…`) trims `q` and cuts it to 200 runes, then matches it with `ILIKE` (`likePattern` escapes the wildcards) against the article's title, page title and excerpt, the feed's title and the user's name for it.
There's no count anywhere.

**Start at:**

1. `internal/server/articles.go` (`toggleAction` and the save and star handlers) and `saved.go`.
2. `internal/psql/saves.go` and `stars.go`.
3. `partials/save_button.html`, and "Save and Star buttons" and "Share buttons" in `app.js`.

**Tests:** `internal/server/articles_test.go`, `saved_test.go`, `home_test.go`, `feeds_test.go`, `scroll_test.go`, `templates_test.go`; `internal/psql/saves_test.go`, `stars_test.go`.

**Why:** "Reading" (its "Saved articles") and "Stars and tags" under "Filtering and timelines" in decisions.md.

## Site icons

**Flow.** The site icon runner (`siteicon.Runner`, `-icon-workers`) claims one site at a time with `SiteRepository.ClaimIconCheck`: a site whose `icon_next_check_at` has passed and that has never been checked (so a new site gets its first check soon, subscribed or not) or has a subscribed feed.
The claim moves `icon_next_check_at` 5 minutes ahead as a lease.

`Runner.check` fetches the site's home page; an error or a non-2xx keeps the icon and retries in a day.
Otherwise it builds up to four candidates (`candidates`): the icons the page declares, apple-touch first and largest first, skipping SVG and those declaring a size under 32, then `/apple-touch-icon.png` and `/favicon.ico`.
Each is downloaded with a second `fetch.Client` capped at 256 KiB, the current icon conditionally (a 304 keeps it).
`usable` recognizes PNG, JPEG, GIF and ICO from the bytes and reads their size; the first icon at least 32 px on its shorter side and at most twice as long as wide is stored (`SaveIcon`, with its SHA-256 `hash`).
If none is usable the icon is removed, unless a candidate failed with a network error, a 429 or a 5xx: then it's kept and retried in a day.
A check that reached the site schedules the next one in 30 days ±10%.

**Serving and showing.** `GET /sites/{id}/icon` (no session) serves the stored bytes with their `Content-Type`, an `ETag` of the hash and a CSP of `default-src 'none'; sandbox`; it's cached for a year when `?v=` is the current hash, else for an hour.
Pages get icon URLs with one `IconHashes` query for the page's sites (`siteIconURLs`), and templates call the `avatar` partial: the icon on a white tile, else the site's letter.
Icons are decoration: if `IconHashes` fails, the error is logged and the page shows letters.

**Start at:**

1. `internal/siteicon/siteicon.go` (`Runner`, `check`) and `ico.go`.
2. `internal/psql/sites.go` (the icon methods).
3. `internal/server/sites.go` (`siteIconHandler`, `siteIconURLs`) and `partials/avatar.html`.

**Tests:** `internal/siteicon` (an `httptest` site with generated fixtures); `internal/psql/sites_test.go`; `internal/server/sites_test.go`, the page tests built on `testSiteIcons`, `templates_test.go`; `cmd/www/config_test.go`.

**Why:** "Design" in decisions.md.

## Installing the app

**Flow.** Every page includes `app-head`, which links the favicons, the web app manifest and a `theme-color` (the user's theme's, else the system's; see "Themes" in [architecture.md](architecture.md)).
That is all the browser needs to offer installing: Safari's Share → Add to Home Screen on iOS, Chrome's Install app on Android and desktop.
The installed app opens `/` full screen (`display: standalone`): the timeline when logged in, the landing page when not.
There is no service worker, install button or banner, and no pull-to-refresh: tapping the current tab loads its page again.

**Start at:**

1. `internal/server/manifest.go` (`manifestHandler`) and `internal/ui/manifest.webmanifest`.
2. `internal/ui/templates/partials/head.html` (`app-head`).
3. `internal/ui/static/img/app-icon.svg` (the source of the install icons).

**Tests:** `internal/server/manifest_test.go`.

**Why:** "Mobile" in decisions.md.

## Article enrichment

**Flow.** Many feed items arrive without an image, a title or any text; enrichment fetches their web page in the background and stores what its meta tags say.

1. **Queuing.** `saveArticles` knows which upserted articles were inserted (`RETURNING id, (xmax = 0)`).
   A new article that `needsEnrichment` (it has a URL and lacks an image, a title or an excerpt) gets an `enrich_jobs` row if `enrichCandidates` allows it: on a feed's first save only the 20 with the latest `timeline_at`, at priority 1; on later saves every one, at priority 0.
   Updated articles aren't queued again, and older articles never.
2. **Claiming.** The enrich runner (`enrich.Runner`, `-enrich-workers`) claims a due job whose article's feed has a subscriber, priority 0 first, with a 2-minute lease, and counts the attempt.
   A looked-up site nobody subscribes to costs no page fetches.
3. **Fetching** (20 s per job):
   - **network error, 429 or 5xx:** retried after 1 hour, then 6 hours (or a later `Retry-After`, capped at 24 h), and given up after the third attempt;
   - **any other non-2xx, a body that isn't HTML, or a fetch the client refuses:** given up at once;
   - **an HTML page:** `discovery.ParsePage` reads its `og:image` (else `twitter:image`), `og:title` (else `<title>`) and description, `PageOf` turns them into an `rss.ArticlePage`, and `SavePage` stores them in `page_image_url`, `page_title` and `page_description` and deletes the job, in one transaction.
4. Outcomes are recorded with a fresh short context; on shutdown nothing is recorded and the lease retries the job.

The `page_*` columns are never the feed's values: `saveArticles` overwrites `image_url`, `title` and `excerpt` on every changed fetch and leaves `page_*` alone.
Pages read them only through the card-value accessors (see "Card values" under "Reading").

**Start at:**

1. `saveArticles`, `needsEnrichment` and `enrichCandidates` in `internal/psql/articles.go`.
2. `internal/enrich/enrich.go` (`Runner`, `attempt`, `PageOf`) and `internal/psql/enrich.go`.

**Tests:** `internal/psql/articles_test.go`, `enrich_test.go`; `internal/enrich` (an `httptest` server); `cmd/www/config_test.go`.

**Why:** "Enrichment" in decisions.md.

## Debug mode

**Flow.** Debug mode shows the stored data behind site, feed and article pages and article rows, and inspects feeds, article pages and site discovery live, for the accounts listed in `-debug-emails` only.

1. **Gating.** `-debug-emails` is lowercased and validated in `parseConfig`.
   For an allowed account (`debugAllowed`), Settings shows a Debug toggle that puts or removes `"debug"` in the scs session, so it's per device and logging out ends it; for anyone else `POST /settings/debug` is a 404.
   `debugEnabled` (allowed and toggled on) is checked on every request, so removing an account from the flag turns debug mode off at once; the nav shows a Debug badge.
2. **Panels.** When `debugEnabled`, the site and feed pages load `DebugStore.SiteDebug` and `FeedDebug` (icon bookkeeping, refresh state, article and enrich job counts) and render them in a collapsed panel after the header.
   The article page loads `ArticleDebug` (its enrich job, up to 20 copies by `canonical_url`, and this user's read and save rows) and shows every column, where each shown value came from, its timeline placement, and the raw `summary` and `content` (cut at 20 000 bytes); `?debug=1` opens the panel.
   Off, none of these queries run.
3. **Provenance.** `articleProvenance` is a pure function of the article that says where each shown value comes from (feed, page, excerpt or none; timeline `backlog` or `news`; external ID kind).
   Its rules mirror `DisplayTitle`, `DisplayExcerpt` and `CardImageURL`, whose comments point to it.
4. **List line.** List pages call `fillDebug` after building their rows; it adds a line per row from columns the lists already load, so no queries, with a link to the article's panel.
5. **Inspectors.** Routes under `/debug/` are POSTs behind `requireDebug` (404 unless `debugEnabled`), since they fetch URLs; each extends its write deadline past the server's `WriteTimeout`, fetches within 30 s, and saves nothing.
   - **Feed** (`feedInspectHandler`): fetches the feed (optionally with the stored validators), and `feedparse.Inspect` shows what `Parse` returns, every channel and item field marked used or ignored, and each item's raw source; `ingest.Articles` and `ArticlesByExternalID` give each item its outcome (new, unchanged, which columns differ, or dropped).
   - **Page** (`articlePageInspectHandler`): fetches the article's page and shows what enrichment would do, next to the stored `page_*` columns, then every meta tag (`discovery.InspectPage`, marked used or ignored from `usedPageKeys`) and its JSON-LD.
   - **Discovery** (`siteDiscoverHandler`): runs `discovery.Discoverer` on the site and shows every candidate checked and its outcome, the feeds found marked known or new, and stored feeds it didn't find.

   Untrusted text (bodies, tags, sources) is shown only as escaped text, and cut to a size.

**Start at:**

1. `internal/server/debug.go` (`debugAllowed`, `debugEnabled`, `requireDebug`, the panels and inspectors).
2. `internal/psql/debug.go`.
3. `internal/feedparse/inspect.go` and `internal/discovery/inspect.go`.
4. `internal/ui/templates/partials/debug.html`.

**Tests:** `internal/server/debug_test.go`; `internal/psql/debug_test.go`; `internal/feedparse/inspect_test.go`; `internal/discovery/inspect_test.go` and `discover_test.go`; `internal/enrich/enrich_test.go`; `cmd/www/config_test.go`.

**Why:** "Debug mode" in decisions.md.

## Adding a feature

The usual path for a new page or action, following the conventions in CLAUDE.md:

1. **Schema:** edit `migrations/00001_init.sql` and run `make db/reset`.
2. **Domain type:** add or extend a type in `internal/rss`, and a sentinel error in `errors.go` if handlers need to tell a case apart.
3. **Repository:** a method in `internal/psql` with a DB test.
4. **Interface:** add the method to the store interface in `internal/server/server.go` (or a new interface, a `Services` field, the compile-time check, and the wiring in `cmd/www/application.go`), and a function field to the fake in `fakes_test.go`.
5. **Handler and route:** a method on `*Server`, registered in `router.go` (see "Code conventions" in CLAUDE.md for forms, flash messages and error pages).
6. **Template:** a page in `internal/ui/templates`, with shared parts in `partials/`.
7. **Tests:** handler tests with fakes for the new behavior, and DB tests for the repository method.
8. **Docs:** see "Docs" in CLAUDE.md for which files to update.
