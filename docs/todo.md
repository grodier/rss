# TODO

Open work only: what's next, work that has no GitHub issue yet, and the Performance
watchlist. Items marked "needs planning" are too large or open-ended to hand to an agent
as-is; plan them first and split them into issues. How to keep this file up to date is
under "Docs" in CLAUDE.md; decisions already made are in [decisions.md](decisions.md).

## Next up

1. #342 Rename a subscription.
2. #343 Show the reader's name for a feed everywhere they read it. After #342.
3. Filtering and timelines (planned in #365; decisions under "Filtering and timelines" in
   decisions.md):
   1. #348 The batch's tables (one production reset). Everything below needs it.
   2. #349 Star articles, #350 posting rate, #351 Tags page: independent of each other.
   3. #352 Tag subscriptions on Edit subscription (after #342 and #351), then #353 bulk
      tagging, #354 OPML folders to tags, and #355 OPML export by tag (after #343).
   4. #356 Custom timelines (after #351), then #357 reorder, #358 starred only (after
      #349), #359 quiet posters (after #350), #360 keywords and #361 audio or video.
   5. #362 Mute on Home (after #352, #353 and #356).
   6. #363 Share a timeline (after #356), then #364 its Atom feed.
4. **Plan** the error-state walkthrough and run the first pass; then repeat it
   periodically.
5. Before production or a few thousand feeds, **plan** load and performance testing.

Merge one PR at a time; each branch merges the latest `main` before its PR.

## Pre-alpha testing

What's needed, after deployment, before other people use the app on a public URL.
Running it locally for yourself needs none of this.

- [ ] **Single-use invites.** A table of invites with expiry and a CLI command to make
  them. Only once people we don't know personally are invited. When the code comes in
  the link, show "Invited" instead of the text field.
- [ ] **Review the privacy policy and terms** (`privacy.html`, `terms.html`, #269). They
  were drafted without legal review and name George Rodier, in the United States, as
  who runs rss. Before launch: put in the product's name, and a business if one runs it;
  name a state for governing law (the terms only say "the United States"); check each
  section against what the app then does (processors, retention, the Settings features
  it mentions); have them reviewed; and bump "Last updated".

## Accounts

Deferred while planning account management (#270). The decisions are under "Accounts" in
decisions.md.

- [ ] **Passkeys.** Log in with Face ID, a fingerprint or a security key, with no
  password to make up at signup, and phishing-proof. Offered alongside the password form,
  since WebAuthn needs JavaScript. Needs a dependency (`go-webauthn/webauthn`) and a
  `passkeys` table (one user, many passkeys), and makes `users.hashed_password` nullable
  for passkey-only accounts. The best login upgrade once #254–#269 are in.
- [ ] **Sign in with Google or Apple (decide before production).** One-tap signup, but a
  third party learns who uses the app, linking accounts that share an email must only
  happen with confirmed addresses, and Apple charges a developer fee. If it's planned,
  revisit whether credentials move to their own table (the password hash stays on
  `users` for now).
- [ ] **Devices list.** Settings lists each logged-in device ("Firefox on macOS · active
  3 days ago") with its own Log out, from `user_sessions` plus a device label taken from
  the User-Agent at login. No IP addresses or locations, which aren't kept outside the
  logs. Settings only has "Log out of all other devices" so far.
- [ ] **Email typo suggestions.** "Did you mean gmail.com?" under the signup email field,
  for near-misses of common domains. Until then, the confirmation notice shows the
  address you typed.
- [ ] **Rehash passwords when the Argon2 parameters change.** On a successful login,
  hash the password again if its stored parameters are older than `password`'s current
  ones. Only matters once the parameters change.
- [ ] **Have I Been Pwned check.** Reject passwords found in breaches, with the
  k-anonymity range API (only the first 5 characters of the password's SHA-1 leave the
  server), on top of the common-password list (#256). A third-party call per new
  password; allow the password if the API is down.
- [ ] **Bounces and complaints.** Record hard bounces and spam complaints from the email
  provider's webhooks in a suppression table we own, skip those addresses in the outbox,
  and tell the user in the app that we couldn't deliver to their address. Needed before
  the move to SES.
- [ ] **Move email to Amazon SES.** When Resend's free tier (100 emails a day, 3,000 a
  month) gets tight or costs more than SES. Sending is SMTP (#259), so the app only
  changes configuration. The rest: request SES production access (it asks how bounces
  and complaints are handled; see above), verify the domain with DKIM, set a custom MAIL
  FROM domain, move DMARC from `p=none` to `quarantine` once its reports are clean (the
  app has no `_dmarc.rss` record yet: add one, or `sp=` on `georgerodier.com`'s), and
  check the result with mail-tester.com.
- [ ] **Product name and domain.** `rss.georgerodier.com` (#254) until the app has a name.
  Moving means verifying the new domain with the email provider, new DNS records,
  changing `RSS_BASE_URL`, `RSS_MAIL_FROM` and `RSS_SUPPORT_EMAIL`, and redirecting the
  old domain. Sessions on the old domain end.
- [ ] **One canonical host.** The app also answers on its `onrender.com` URL, so sessions
  and links can be split between two hosts. Redirect requests for any other host to
  `-base-url`'s, except `/healthcheck`, which Render's health checks call.
- [ ] **Full data export.** A JSON file with your account, subscriptions, reading
  history and saved articles (#299), next to the OPML export.
- [ ] **Clean up abandoned unconfirmed accounts**, e.g. delete accounts never confirmed
  and unused for 30 days after signup. Only if they pile up.
- [ ] **Support tools.** `www` commands can't run in production, so the self-service flows
  are the support path. If support requests need more (looking up an account, resending
  a confirmation for someone), plan an owner-only admin page. It would build on debug
  mode's allowlist (#327), and is where cross-user data such as subscriber counts
  belongs.
- [ ] **First-run experience (needs planning).** What a new account sees after signup:
  today, an empty timeline pointing to Search and to OPML import. Perhaps
  suggested sites or a short tour.

## Deployment

- [ ] **HSTS.** Send `Strict-Transport-Security` in production once the app's domain is
  settled (see "Product name and domain" under Accounts), without `includeSubDomains`
  so other `georgerodier.com` hosts are unaffected. #254 sets up the custom domain.
- [ ] **Deploy the backup job with its image.** Render pulls `rss-backup:main` only when
  the cron job is deployed, so a change to `deploy/backup` runs only after a manual deploy
  in Render's dashboard (see "Backups and restore" in `infra/README.md`). Have CI deploy
  it by digest like the app, which needs its ID as a secret.
- [ ] **External uptime check.** Deferred from #217 while pre-alpha. A free UptimeRobot
  (or Better Stack) keyword monitor on `<url>/healthcheck` for `available`, every 5
  minutes, alerting by email; note the monitor (not its credentials) in
  `infra/README.md`. Before other people rely on the app.
- [ ] **Stop the lifecycle rules from changing on every apply.** Cloudflare returns the
  `rss-backups` lifecycle rules in the opposite order from `infra/backup.tf`, so every
  apply plans an in-place update of `cloudflare_r2_bucket_lifecycle.backups` that only
  swaps the two rules. Harmless, but a plan that always shows a change hides real ones.
  List the rules in the order Cloudflare returns them (the multipart-upload rule first)
  and check that the next plan shows no change for it.
- [ ] **Upgrade `render-oss/render` once its digest fix ships.** Provider v1.9.1 can't
  update a service pinned by digest, which deploys always leave `rss` on: it sends
  `ghcr.io/grodier/rss:<hex>` and Render refuses with "unable to fetch image with provided
  input" ([render-oss/terraform-provider-render#106](https://github.com/render-oss/terraform-provider-render/issues/106),
  fix in PR #109). The #254 apply hit it. When a release includes #109 (Dependabot opens
  the upgrade PR; check its changelog), upgrade, check that a plan changing the service
  applies without the workaround, and remove "Changing the web service" from
  `infra/README.md`.
- [ ] **PR preview environments.** Render preview environments need the Professional
  workspace (about $19 per user a month plus compute). Only from same-repo branches: never
  `pull_request_target` with fork code, since this repository is public.
- [ ] **Leaving pre-alpha.** Remove the fingerprint check, `-allow-reset-to` and the
  "Reset production database" workflow, and make schema changes new migrations (see
  CLAUDE.md). From then on every migration must also work with the version still
  running during a deploy ("expand/contract": add, deploy code using both, backfill,
  deploy code using the new, then drop the old). Give migrations their own Postgres role
  that owns the schema, and the app a role that can only read and write data.

## Security

- [ ] **Per-email login lockout.** #183 lets anyone keep a known email locked out of
  login for 15 minutes at a time by failing logins for it. That's accepted while users
  are a handful of known people, and from #265 a locked-out owner can still get in by
  resetting their password. Revisit before signup is open to strangers (e.g. key the
  limit on IP and email together, or slow down with backoff instead of refusing).
- [ ] **Bot protection on signup.** Before signup opens beyond invite codes, add a
  challenge (e.g. Cloudflare Turnstile, which needs a script and a CSP change) or another
  bot check.
- [ ] **Rate limiter approach.** Evaluate whether the in-memory rate limiter
  (`internal/server/ratelimit.go`) is right long term. It resets on restart and isn't
  shared across instances. Before running more than one instance, or if limits need to
  survive restarts, compare options (e.g. Postgres-backed counters, a reverse-proxy/edge
  limit, Redis) and decide. No decision yet.

## Feed discovery

- [ ] **Live updates for pending work (lookups and feed refreshes).** Replace the lookup
  status page's `<meta http-equiv="refresh">` with JavaScript polling, server-sent events
  or websockets, and use the same mechanism to update a feed page after the background
  refresh it requested (#137) finishes: the request is pending while
  `feeds.refresh_requested_at` is set. Progressive enhancement: pages keep working without
  JavaScript.
- [ ] **Logged-out access.** Let logged-out visitors search and view site and feed pages, to
  help people find the app. Keep lookups login-only.
- [ ] **Name → website suggestions.** For free-text queries ("the verge"), suggest likely
  websites ("Try theverge.com?") the user can click to look up. Don't fetch them
  automatically.
- [ ] **Platform-specific discovery rules.** YouTube channels, Reddit, Substack, Medium,
  GitHub releases, etc. Includes sites that share a host and are identified by path
  (`medium.com/@user`), which the host-based site key can't tell apart.
- [ ] **`<a href>` heuristics.** When a page has no `<link rel="alternate">`, look for links
  whose URL or text mentions RSS/Atom/feed before probing common paths.
- [ ] **Search autosuggest** showing matching sites and their related feeds as you type.
- [ ] **Better search ranking.** `pg_trgm` or full-text search instead of `ILIKE`
  substring matching, with an index.

## Feed ingestion

Each with when it pays off:

- [ ] **Adaptive refresh interval.** Fetch busy feeds often and dormant ones rarely, from
  posting frequency: count articles published in the last 14 days (`n`), interval =
  clamp(14 days ÷ 4n, 15 min, 24 h), 24 h when `n = 0`, then apply the `Retry-After` /
  `Cache-Control` floors (#133). Worth it at a few thousand feeds, or when most fetches
  are 304s or find no new articles. `feeds.posts_per_week` (#350) is a 30-day version of
  `n` the schedule could use.
- [ ] **Permanent redirects (301/308).** Update `feeds.url`. Needs `fetch` to report the
  redirect chain, and a decision for the unique-URL collision when two feeds end up at
  the same URL (merge, or keep the old one). Redirects are already followed, so this can
  wait until moved feeds are a visible problem.
- [ ] **Give up on long-dead feeds.** E.g. stop after 30 days of consecutive failures and
  say so on the feed page. When dead feeds are a noticeable share of fetches.
- [ ] **Per-host politeness.** At most N concurrent fetches per host. When one host has
  many feeds (category feeds, platform hosts like Substack or Medium).
- [ ] **Separate worker process or `-once` mode.** Run the background workers in their
  own process when fetching measurably slows web requests or should scale separately
  from web instances. On Render, that's the same image as a background worker service
  with the web service's workers set to 0 (`RSS_LOOKUP_WORKERS=0`,
  `RSS_REFRESH_WORKERS=0`), for about $7 more a month; a `cmd/worker` or `-role` flag is
  the alternative. On a scale-to-zero host, add a `-once` command that drains the due
  feeds, run by the platform's cron, rather than an HTTP endpoint.
- [ ] **WebSub (push).** Subscribe to hubs that feeds advertise for near-instant updates.
  Only when freshness matters more than it does now; needs a public callback URL.
- [ ] **Article retention (needs planning before launch).** Whether to delete old
  articles, and when. Read state doesn't drive it (the timeline isn't an inbox), so a
  time- or count-based rule works (e.g. keep 6 months, or the newest N per feed), with
  `reads` rows deleted along with their articles (`ON DELETE CASCADE`). Before launch,
  weigh deleting against keeping: storage and backup cost and the "Articles grow without
  bound" watchlist entry on one side; the value of a long article archive (search,
  history, business uses of the data) and its copyright and privacy implications on the
  other. Saved articles (#299) must survive whatever rule is chosen: deleting an article
  cascades to its `saves` rows.
- [ ] **Operational visibility.** An admin page or metrics (due backlog, failure rate,
  304 rate). When the app runs somewhere the logs aren't at hand. An admin page would
  build on debug mode's allowlist (#327).
- [ ] **Fetch history.** Keep the last N fetch attempts per feed, with status, bytes,
  items, new and updated counts, and duration, but no bodies. Show them in the feed's
  debug panel. Debug mode (#327–#330) deliberately stores nothing and inspects live.
  Add this only if a problem comes and goes, so that a live fetch can't show it. Cap it
  per feed, since it's a table that grows with every fetch.

## Subscriptions

- [ ] **Feed tabs on the site page.** Show a site's articles one feed at a time, as tabs
  that are sub-routes of the site page (e.g. `/sites/{id}/feeds/{feedID}`). This is for
  browsing a site's feeds in context. It isn't a way to organise subscriptions (no
  folders or pinned feeds). Open question: how it relates to the standalone feed page.
- [ ] **Names from OPML imports.** Other readers' exports carry each feed's name as the
  outline's `text`/`title`; an import could set it as the subscription's name (#342)
  when it differs from the feed's title. Deferred from #344.

## Reading

- [ ] **Enrich older articles (low priority).** #306 only enriches a feed's 20 newest
  articles on its first save, then new arrivals. A second, lowest-priority queue could
  work back through older articles that are missing an image, title or excerpt, when
  nothing else is waiting. Worth it once something reads the old archive: searching all
  articles, related articles, or saved articles from far back that look bare.
- [ ] **Load full article (on demand).** A "Load full article" button on the article page
  when the feed only sends a summary: a POST that fetches the page once, extracts the
  text readability-style (a dependency, e.g. `go-shiori/go-readability`), sanitizes it
  like feed HTML and keeps it on the article for every reader. Never automatic: it costs
  storage, extraction fails on paywalls and cookie walls, and storing publishers' full
  text is a copyright question for a public product. Needs a failure story ("Couldn't
  load the article; read it on {site}").
- [ ] **AI-assisted titles and summaries.** For untitled items, or alongside enrichment:
  heuristics over headings, URL path, images and text, or a model. Deferred from #306.
- [ ] **Generic `og:image` detection.** Some sites use one fallback `og:image` (their
  logo) for every page, so enrichment (#306) would give every card the same picture.
  If that shows up, ignore a page image that a feed's recent articles share.
- [ ] **Measure embeds and media.** Before building more embeds or a player, see what
  articles carry. Embedded iframes by host:
  ```sql
  SELECT substring(m[1] from '^(?:https?:)?//([^/?#]+)') AS host, count(DISTINCT id)
  FROM articles, regexp_matches(content || summary, '<iframe[^>]*\ssrc=["'']([^"'']+)', 'gi') AS m
  GROUP BY host ORDER BY 2 DESC;
  ```
  Embedded social posts (their markup arrives as a blockquote with a class):
  ```sql
  SELECT count(*) FILTER (WHERE content || summary ~* 'class="twitter-tweet') AS x,
         count(*) FILTER (WHERE content || summary ~* 'bluesky-embed') AS bluesky,
         count(*) FILTER (WHERE content || summary ~* 'mastodon-embed') AS mastodon,
         count(*) FILTER (WHERE content || summary ~* 'instagram-media') AS instagram
  FROM articles;
  ```
  Audio and video items:
  ```sql
  SELECT split_part(media_type, '/', 1) AS kind, count(*), count(DISTINCT feed_id) AS feeds
  FROM articles WHERE media_type <> '' GROUP BY 1;
  ```
  Use the numbers to order the next three items.
- [ ] **Podcast and video player.** Keep the enclosure's URL, type and length (#297 only
  keeps the type), show the browser's own `<audio>` / `<video>` player on the article
  page, and an "Audio" or "Video" label on the card. Media loads from the publisher, so
  the CSP needs `media-src https:`.
- [ ] **Music embeds.** Spotify, SoundCloud and Bandcamp players as click-to-play
  previews, like YouTube and Vimeo (#296).
- [ ] **Embedded social posts.** Style the blockquote that X, Bluesky and Mastodon embeds
  arrive as into a quote card with the author and a link, without loading their scripts.
- [ ] **Share button.** In the row's action bar, to Save's left (#299): the Web Share API on
  phones, else copy the article's link. Its own small issue.
- [ ] **Measure untitled articles.** Before investing in better titles, query the share of
  articles with an empty title (`SELECT count(*) FILTER (WHERE title = '') * 100.0 /
  count(*) FROM articles`).
- [ ] **Image proxy.** Serve article and card images from our domain so publishers and
  trackers don't see readers' IPs, browsers and read times (tracking pixels work like
  email open tracking). Why it's its own project:
  - SSRF: the proxy fetches stranger-chosen URLs; must go through `internal/fetch`
    (already blocks private addresses).
  - Open-proxy abuse: only URLs we emitted may be fetched, so image URLs need an HMAC
    signature.
  - Content-type safety: serving an attacker's SVG or HTML from our origin is XSS; allow
    only raster types, send `nosniff` and a sandboxing CSP, or use a separate domain.
  - Decoding untrusted images (if we resize for thumbnails) is attack surface, including
    decompression bombs.
  - Bandwidth, caching, size limits and timeouts for every image.
  - Our server may get rate-limited or blocked by hosts; caching publishers' images raises
    copyright questions.

  Site icons (#304, #305) are already served from our domain: they're small, one per
  site, and checked and stored once a month.
- [ ] **Keyboard shortcuts.** E.g. `j`/`k` to move between rows, `o` to open, `v` to open
  the original.
- [ ] **"You were here" divider.** A subtle marker in the timeline at your last visit
  (possible because positions never change). No counts.
- [ ] **Unread filter.** Maybe never; it pulls toward the inbox model.
- [ ] **Timezone setting and day headers.** Only if local times via JavaScript aren't
  enough (e.g. "Today"/"Yesterday" groups rendered server-side).
- [ ] **Site names from `og:site_name`.** For articles on another site than their
  feed's, enrichment could store the page's `og:site_name` ("The New York Times") so
  "Read on" shows it instead of the domain (#341). Enrichment doesn't run for every
  article, so the domain stays the fallback. Deferred from #344.
- [ ] **"Discussion on Hacker News".** For aggregator items, a second link on the article
  page to the item's own page (the feed's `comments` link), next to "Read on {domain}".
  Deferred from #344.

## Filtering and timelines

Deferred while planning filters (#365). The decisions are under "Filtering and timelines"
in decisions.md.

- [ ] **Related articles (needs planning).** "More like this" on the article page, and
  smarter topic matching than keyword filters (#360). Two approaches to compare:
  - *Content similarity:* article embeddings, in Postgres with `pgvector` or in a
    separate vector store, which means a model call per article and its cost; or
    Postgres full-text search (`tsvector` similarity) as a cheap baseline first.
  - *Signals across users:* articles starred together ("people who starred this also
    starred…", within a similar category), and a feed's category from the tags readers
    give it.

  The second needs real usage first, and the conditions under "Cross-user use" in
  decisions.md: anonymous totals above a minimum count, and a privacy policy update.
- [ ] **Shared timelines in Search (needs planning).** An opt-in "List in search" per
  shared timeline (#363), and a Timelines section in search results. Plan moderation
  first: names and descriptions are the first user-written text strangers would see
  (spam, impersonation, reporting), and whether to show an owner name or profile.
- [ ] **More posting-frequency conditions.** Beyond "at most N posts a week" (#359): posts
  a day, "posted in the last N days", "hasn't posted for N months". When someone asks.
- [ ] **Mute from a timeline row.** A per-row menu with "Leave out of Home", if muting
  from Edit subscription and tag pages (#362) proves too far away.
- [ ] **Images on shared pages.** #363 shows text only. Card images would load from
  publishers for strangers too; decide with the image proxy.
- [ ] **Embedded videos for the audio/video condition.** #361 only counts enclosures. A
  flag set at ingest for YouTube or Vimeo iframes would add blog posts with videos;
  decide with "Measure embeds and media".

## Design

- [ ] **Generated avatars for sites without a usable icon.** Sites whose only icon is a
  small favicon keep their letter (#304). Explore generating something better, possibly
  with AI: the site's colors on the tile, an upscaled or redrawn logo. Ideas only so far.
- [ ] **Icons a feed declares.** RSS `<image>`, Atom `<icon>` / `<logo>` and JSON Feed
  `icon` as extra candidates for #304, if many sites end up without an icon.
- [ ] **Themes, including dark mode (needs planning).** A theme choice (light, dark,
  follow the system) on the Settings page (#261), applied as a `data-theme` attribute
  on `<html>` that redefines the color tokens. Stored as a typed column on `users`, not
  a key-value table ("Accounts" in decisions.md). The avatar tints (`.tone-1` to
  `.tone-6`) and the icon tile (#305) need dark variants too. Reading preferences go
  here as well: text size, serif or sans, and line width on the article page (deferred
  from #295).
- [ ] **Asset build step (needs planning).** Bundle and minify the CSS and JavaScript
  for production, writing content-hashed file names. Templates link assets through
  `asset` (#199), so only `asset` changes: it looks names up in the build's manifest
  instead of hashing files at startup, and every file under `/static` gets the immutable
  `Cache-Control`. This reverses the "no build step" decision ("Overall" in
  decisions.md, and CLAUDE.md), so planning it updates both. Pages must still work
  without JavaScript.

## Error-state walkthrough (needs planning)

A way to see every error state an end user can hit and judge the experience (wording,
what the user can do next, tone, layout) without having to provoke each one by hand. Do it
once after the initial feature set is built, then repeat periodically and whenever a
feature adds a failure mode. Nothing here is built yet.

- [ ] **Catalog.** `docs/error-states.md`: one row per user-visible error state with
  *trigger* (how to reproduce it), *where it shows* (page / status code), *current
  message*, *can the user recover?*, and a *reviewed on* date and verdict. Seed it by
  grepping handlers for `CheckField`, `notFoundResponse`, `serverErrorHTML`, `errorHTML`,
  `sessionManager.Put(ctx, "flash"` and the sentinel errors in `internal/rss/errors.go`, and
  every template that renders an error or status. Rule for contributors: a PR that adds a
  user-visible failure adds a row.
- [ ] **Walkthrough tool.** Something that renders every state in one place so they can
  be clicked through in a browser, preferably a dev-only route group (e.g. `/dev/states`,
  mounted only with a flag, never in production) that lists each state and renders it with
  fake data. A test table (`name`, `setup`, `request`, `want status`) can drive the same
  list, so the catalog can't drift: a test fails if a state is in the code but not the
  catalog. Prefer this over screenshots, which go stale.
- [ ] **States to cover.**
  - Forms: signup (invalid email, short, common or email-as-password password (#256), a
    duplicate email (#262), signup turned off (#186), rate limited (#182)), login
    (unknown email, wrong password (#262), rate limited per IP (#182) or per email
    (#183)), empty or oversized input, a form posted from another site (403), a
    malformed form body.
  - Auth and sessions: visiting a login-only page while logged out (redirect and
    message), an expired or idle session mid-action (#258), logging out, a logged-in
    user on `/login`.
  - Account emails and settings (#254–#269): a confirmation, reset or email-change link
    that's expired, used, or for an address the account no longer has; reset for an
    unknown email; the rate limits on reset, resend, password change, email change and
    deletion; an address over the outbox's hourly cap; a wrong current password in
    Settings; an email change to a taken address; the confirmation notice; deleting an
    account.
  - HTTP errors: 403, 404 (unknown feed, site or lookup id; malformed id), 405, 413, 429
    (the lookup rate limit; login and signup re-render their form instead), and 500,
    including a database outage.
  - Lookups and discovery: invalid URL, a URL that is blocked (private address, bad
    scheme), a site that is unreachable, times out, redirects too often, returns a non-HTML
    or huge body, has no feeds, or has a feed that fails to parse; a lookup that is
    pending, running, failed or done; the status page after the lookup row is gone.
  - OPML import: an upload with no file, over 1 MiB, that isn't OPML, with no feeds or
    over 1,000, or over the hourly limit; an import still running; an import with
    failures (invalid URL, unreachable, no feed); another user's import.
  - Feeds: a refresh that fails (and the backoff / failure display from #129), a feed
    that has never been fetched, a feed with no articles, the Refresh button during a
    refresh, 410 Gone feeds (#132).
  - Search: no results, empty query, very long query, special characters.
  - Empty states that aren't errors but read like one: a new account with no feeds, an
    empty site page.
  - Subscriptions: a subscribe or unsubscribe that fails.
  - Reading: an empty timeline (no subscriptions), "No older articles.", an invalid
    `before` cursor (400), an article with no text in its feed, an article with no URL,
    an unknown or malformed article ID, a failed read ping (silent by design).
- [ ] **Review pass.** For each state ask: does the user know what happened and what to
  do next? Is the message free of internals (SQL, stack traces, raw Go errors)? Is the
  status code right? Does the form keep what they typed? Is it usable on a narrow screen
  and with a screen reader (focus, `role="alert"`)? Record findings in the catalog and
  turn the ones that need work into issues.
- [ ] **Cadence.** Repeat after any change to a template, middleware or error path, and
  at least before each release. Note the date of the last pass at the top of the catalog.

## Load and performance testing (needs planning)

Background refresh (#128) has never run against more than a few feeds.
Rough capacity: feeds per hour ≈ workers × 3600 s ÷ average fetch duration (2 workers at
1 s per fetch ≈ 7,200 feeds/hour). The poll interval only matters when nothing is due, so
it doesn't limit throughput. Measure before relying on these numbers. Worth doing before
production, or once there are a few thousand feeds.

- [ ] **Load-test harness.** A program that seeds N fake feeds pointing at a local fake
  feed server and runs the refresh workers against them. The fake server takes configurable
  latency, error rate, hang rate and body size. The `www` binary can't fetch local
  addresses (`fetch.Options.AllowPrivate` is tests-only), so the harness should wire
  `internal/refresh` itself rather than add a `www` flag that turns off the address check.
  Rows are uniquely named and deleted afterwards, as in the DB tests.
- [ ] **What to measure.**
  - Due backlog: `count(*)` and `max(now() - next_fetch_at)` of overdue feeds. Should stay
    near 0 and under a minute.
  - Fetch duration p50/p95 (from the `feed refreshed` log's `duration`).
  - Worker utilization: fetches per hour × average duration ÷ (workers × 3600). Keep it
    under ~50–70% to leave headroom for spikes.
  - Web request latency p95 while the workers are busy.
  - Database connections in use (`pg_stat_activity`).
  - Memory and CPU of the `www` process.
- [ ] **Scenarios.**
  - Steady state: find the most feeds each worker count (2, 8, 16) keeps up with, using a
    realistic latency mix.
  - Slow and dead feeds: a share of feeds hang until the 10 s fetch timeout or fail
    outright. How much capacity they cost, and whether backoff wins it back over time.
  - Everything due at once: a fresh database, a restart after downtime, or a large import
    (`next_fetch_at` defaults to `now()`). How long the backlog takes to drain, and
    whether jitter spreads the next round.
  - Outage recovery: a host serving many feeds goes down and comes back. Do retries
    cluster? Does jitter spread them?
  - Many feeds on one host: requests per second hitting a single host (input for per-host
    politeness).
  - Large feeds: bodies near the fetch size cap and `ingest.MaxArticles` items, so the
    per-article upserts in `saveArticles` show their cost.
  - Web traffic during refresh load: page latency and DB connection contention.
    Workers and handlers share one pool (`-db-max-open-conns`, default 25).
  - Lookups and refresh together: both runners claiming and writing at the same time.
  - Several instances: `SKIP LOCKED` claims never fetch a feed twice, and throughput
    scales with instances.
  - Data growth: `EXPLAIN ANALYZE` for `ClaimDue`, `ListByFeed`, search (`ILIKE`) and
    site pages at ~100k feeds and millions of articles.
- [ ] **Act on the results.** Possible outcomes: a different default `-refresh-workers`, a
  DB connection cap, per-host politeness, a separate worker process, or the adaptive
  refresh interval (all listed under Feed ingestion). Record the numbers that led to each
  decision.

## Performance watchlist

Things that are fine at today's scale but could become performance problems as feeds,
articles, users or instances grow: what each is, the issue that introduced it, the signal
that it has become a problem, and the planned fix. Don't fix these early: watch the
signal, and use the load tests above to measure (see "Writing issues" in CLAUDE.md).

Background refresh:

- [ ] **`ClaimDue` walks past feeds nobody wants refreshed** (#137). The `EXISTS`
  subscription check and `refresh_requested_at` can't be in the partial
  `feeds_next_fetch_at_idx`. Unsubscribed feeds keep an old `next_fetch_at`, so they pile
  up at the front of the order and every claim skips them. Ordering requested feeds
  first (`refresh_requested_at IS NULL, next_fetch_at`) also stops the index from serving
  the sort. *Signal:* `EXPLAIN ANALYZE` of `ClaimDue` reads far more rows than it returns,
  or idle claims get slow, once most feeds are unsubscribed. *Fix:* keep a
  `subscriber_count` on `feeds` (updated by `Subscribe`/`Unsubscribe` in the same
  transaction) and index `next_fetch_at WHERE gone_at IS NULL AND (subscriber_count > 0
  OR refresh_requested_at IS NOT NULL)`.
- [ ] **Polling latency and idle queries.** Each idle refresh, lookup and mail worker
  queries every poll interval (15 s for refresh, 10 s for mail (#260)), on every
  instance. A requested refresh can wait a full interval before a worker sees it; the
  mail worker is woken by its own instance when an email is queued, so only emails
  queued by another instance wait. *Signal:* users wait noticeably for "Checking for
  new articles…" or for emails, or idle claim queries show up in `pg_stat_statements`.
  *Fix:* Postgres `LISTEN`/`NOTIFY` from `RequestRefresh`, `Subscribe`, lookup requests
  and email `Enqueue` to wake a worker, keeping the poll as a fallback.
- [ ] **Stale lookup resets scan `lookups`** (#195). Every instance's lookup runner resets
  stale jobs every minute, and no index covers `ResetStale`'s `status = 'running'`
  filter, so each reset scans the table, which keeps a row per site key ever looked up.
  *Signal:* `ResetStale` in `pg_stat_statements` totals, or a large `lookups` table.
  *Fix:* a partial index on `started_at WHERE status = 'running'`.
- [ ] **One statement per article in `saveArticles`.** Each fetched item is a separate
  upsert inside the `SaveFetch` transaction. *Signal:* `SaveFetch` time dominates the
  `feed refreshed` log's `duration` for large feeds (see the "Large feeds" scenario
  above). *Fix:* a single multi-row upsert (`unnest` of arrays) per fetch.
- [ ] **Posting rate recomputed on every save** (#350). `saveArticles` and
  `RecordNotModified` count each feed's articles of the last 30 days on every fetch,
  304s included, served by `articles_feed_sort_idx`. *Signal:* the count shows up in
  `pg_stat_statements`, or `SaveFetch` time grows for busy feeds. *Fix:* recompute only
  when articles were inserted, plus at most once a day.
- [ ] **Writes from feed page views** (#137). Viewing a stale feed runs an `UPDATE`
  (`RequestRefresh`). The `refresh_requested_at IS NULL` guard limits it to once per stale
  period per feed, so this is unlikely to matter. *Signal:* `RequestRefresh` shows up in
  `pg_stat_statements` or in row-lock waits on `feeds`. *Fix:* skip the call unless the
  feed has been stale for a while, or move requests to their own table.
- Also listed under Feed ingestion: adaptive refresh interval, per-host politeness,
  separate worker process, giving up on long-dead feeds.

Other background work (#302, #304, #306):

- [ ] **Enrichment request volume** (#306). Every new article missing an image, title or
  excerpt costs one page fetch, and a feed's first save queues up to 20. One worker
  fetches one page at a time (about one a second), so a large OPML import (#302) of
  feeds without images queues thousands of jobs that take hours to drain, and many of
  them hit the same few hosts back to back. *Signal:* `SELECT priority, count(*),
  min(next_attempt_at) FROM enrich_jobs GROUP BY 1` growing or the oldest due job
  getting older; 429s from one host in the `article enriched` logs. *Fix:* per-host
  pacing (skip jobs whose host was fetched in the last few seconds), more
  `-enrich-workers`, or a lower first-save count.
- [ ] **Enrichment and icon claims skip unsubscribed work** (#304, #306). Like
  `ClaimDue`, `ClaimNext` and `ClaimIconCheck` filter on an `EXISTS` subscription check
  the index can't serve, so jobs for feeds nobody subscribes to (looked up, never
  followed) stay at the front and every claim walks past them. *Signal:* `EXPLAIN
  ANALYZE` of either claim reading far more rows than it returns. *Fix:* the
  `subscriber_count` column planned for `ClaimDue`, or deleting jobs of feeds without
  subscribers after a while.
- [ ] **One import at a time** (#302). The import queue takes the oldest import's feeds
  first, so a 1,000-feed import delays a small one started after it, up to an hour with
  two workers. *Signal:* imports reported slow, or pending `import_feeds` rows waiting
  long behind another user's import. *Fix:* round-robin between imports (claim the
  lowest position across imports, oldest import first), or a per-import worker cap.

Pages and queries:

- [ ] **My feeds is unpaginated** (#136). It loads every subscription with a `LATERAL`
  lookup of each feed's newest article. *Signal:* users with hundreds of subscriptions, or
  `/feeds` p95 latency rising. *Fix:* store `latest_article_at` on `feeds` (set in
  `SaveFetch`) instead of the `LATERAL` subquery; add filtering or pagination.
- [ ] **Site pages and search results load all of a site's feeds.** `ListBySite` and the
  second search query have no limit. Platform hosts and category-heavy sites can have
  hundreds of feeds. *Signal:* a site page or search response with hundreds of feeds, or
  slow ones in the logs. *Fix:* limit feeds per site in search (with a "more" link to the
  site page) and paginate the site page.
- [ ] **Debug panels count a feed's articles on every view** (#327). `FeedDebug` runs
  `count(*)` and `max()` over a feed's articles, for every feed on a site page, while
  debug mode is on. Only allowed accounts pay for it. *Signal:* site or feed pages are
  noticeably slower with debug mode on, for feeds with many thousands of articles.
  *Fix:* drop the counts from the site page's per-feed lines, or use the
  `articles_feed_timeline_idx` for the newest time and stop counting.
- [ ] **`SubscribedFeedIDs` on every page** (#135, #138). One extra indexed query per site,
  feed and search page. Unlikely to matter. *Signal:* it shows up in
  `pg_stat_statements` totals. *Fix:* fold it into the page's main query.
- [ ] **Search scans with `ILIKE`.** Sites and feeds are matched with `%q%`, which can't
  use a b-tree index. *Signal:* search latency growing with the number of sites and feeds.
  *Fix:* see "Better search ranking" under Feed discovery (`pg_trgm` or full-text search,
  with an index).
- [ ] **Timeline query merges many feeds** (#154). `ListTimeline` joins the user's
  subscriptions to their articles and sorts by `timeline_at`. Postgres can't merge the
  per-feed `(feed_id, timeline_at)` index scans, so it may read and sort every subscribed
  article to return one page, and deeper pages cost more. *Signal:* `/` p95 rising with
  subscription count, or `EXPLAIN ANALYZE` reading far more rows than the page returns.
  *Fix:* a `LATERAL` top-N per subscribed feed merged in the query, or a per-user
  timeline table filled at ingest.
- [ ] **Filtered timelines read past many articles** (#356, #359, #360). A restrictive
  filter (one quiet tag, a rarely matched keyword, quiet posters) still makes
  `ListTimeline` collapse copies over all the matching subscriptions' articles, and
  keyword matches (`~*` over title, page title and excerpt) can't use an index, so a
  page that matches few articles reads many to fill 30 rows. *Signal:* `/timelines/{id}`
  p95 above `/`'s, or `EXPLAIN ANALYZE` reading far more rows than the page returns.
  *Fix:* the per-user timeline table above; for words, a `pg_trgm` or full-text index on
  the matched text, or bounding the scan to recent articles.
- [ ] **Timeline tabs on every Home view** (#356). The tab row adds one small indexed
  query (`TimelineRepository.List`) to `/` and each timeline page, and Home adds
  `GetHome` (#362). *Signal:* they show up in `pg_stat_statements` totals. *Fix:* fold
  them into one query, or keep the tab list in the session until a timeline changes.
- [ ] **Tag page checklist is unpaginated** (#353, #356). A tag's page, and also
  the timeline form, list all of a user's subscriptions as checkboxes, like My feeds.
  *Signal:* users with hundreds of subscriptions, or those pages' p95 rising. *Fix:*
  with My feeds' fix, plus a filter box over the list.
- [ ] **Starred timelines read all of a user's stars** (#358). With Show limited to tags,
  `ListStarred` walks the user's stars by `starred_at` and drops those outside the
  filter, like the Saved search. Fine for hundreds of stars. *Signal:* starred-only
  timelines slow for users with thousands of stars. *Fix:* join from the filter's feeds
  instead, or index stars by feed.
- [ ] **Shared feeds polled by other readers** (#363, #364). Every request for a
  shared page or Atom feed runs its owner's timeline query, anonymously, and feed
  readers poll every 15–60 minutes; a link that spreads multiplies that. `ETag`, 304s
  and `Cache-Control: public` cut the bytes, not the query. *Signal:* `/shared/` requests
  in the logs, their latency, or `ListShared` in `pg_stat_statements`. *Fix:* an
  in-memory cache per token (keyed by the timeline's `updated_at` and newest article),
  a CDN in front of `/shared/`, or a per-IP limit.
- [ ] **Sanitizing article HTML on every view** (#151). `sanitize.HTML` parses and
  renders the article with `x/net/html`, then bluemonday parses it again, on each article
  page view; cost grows with the size of the HTML, which feeds don't bound. *Signal:*
  article page p95 latency or CPU rising, especially for long articles. *Fix:* cache the
  sanitized HTML per article (keyed by a policy version, so a policy change still applies
  everywhere), or cap the HTML size we render.
- [ ] **Read writes on article views** (#156, #158). Opening an article page, and each
  click through to the original, runs an `INSERT … ON CONFLICT DO NOTHING` on `reads`.
  *Signal:* `MarkRead` in `pg_stat_statements` totals, or lock waits on `reads`.
  *Fix:* skip the insert when the page already knows the article is read, or batch writes.
- [ ] **`ReadArticleIDs` on every list page** (#156, #161). One query per timeline or feed
  page, matching reads across copies by `canonical_url` (#161). Saves add
  `SavedArticleIDs` (#299), stars add `StarredArticleIDs` (#349) and icons add
  `IconHashes` (#305), each one more query per page with the same shape. *Signal:* they
  show up in `pg_stat_statements`, or slow for users with many reads or saves.
  *Fix:* fold them into the page's main query, or store the canonical URL on `reads`
  and `saves`.
- [ ] **Saved search scans with `ILIKE`** (#301). Search within Saved matches `%q%`
  against each of the user's saved articles. Fine for hundreds of saves. *Signal:*
  `/saved?q=` latency rising for users with thousands of saves. *Fix:* `pg_trgm` or
  full-text search, with the site search ("Better search ranking").
- [ ] **Site icons served from Postgres** (#305). Each icon request that misses the
  browser's cache reads its `bytea` row. URLs carry the icon's hash and are cached for a
  year, so repeat views cost nothing, but every new visitor's first timeline asks for up
  to 30 icons. *Signal:* `GetIcon` in `pg_stat_statements` totals, or icon requests in
  the logs dominating traffic. *Fix:* an in-memory cache keyed by hash, or a CDN in
  front of `/sites/*/icon`.

Data growth:

- [ ] **Articles grow without bound.** Raw `summary` and `content` HTML is stored for every
  article forever. *Signal:* `articles` table and index size, slower `ListByFeed` and
  timeline queries, backup size. *Fix:* see "Article retention" under Feed ingestion.
- [ ] **Timeline deduplication at query time** (#162). The timeline collapses duplicate
  articles across a user's feeds with `DISTINCT ON (canonical_url)` over all of the
  user's subscribed articles, for every page. *Signal:* timeline queries slow for users
  with many subscriptions or overlapping feeds. *Fix:* precompute per-user timeline
  entries (the same table as "Timeline query merges many feeds").
- [ ] **`reads` grows with every article read** (#156). One row per user per article
  opened, kept until the article is deleted. *Signal:* `reads` table and index size.
  *Fix:* article retention (deleting articles cascades to `reads`).
- [ ] **Imports are kept forever** (#302). Every upload keeps its `imports` row and a row
  per feed in the file, only shown on the results page and the last five in Settings.
  *Signal:* `import_feeds` table size. *Fix:* delete imports older than 90 days (the
  results page then says the import is no longer available).
- [ ] **Sessions in Postgres.** scs reads the `sessions` row on every request in the
  session group and writes it whenever the session changes (flash messages, login).
  `authenticate` then reads the `user_sessions` row joined to `users` (#258, in place of
  the old `Exists` query) and updates its `last_seen_at` at most once a day. Expired
  scs rows are swept periodically, but they live up to a year (#258), and visitors who
  aren't logged in get one too as soon as something is stored for them: a flash message
  after logging out, or the page to return to after login (#262), which crawlers
  following links to login-only pages would trigger. *Signal:* session queries in
  `pg_stat_statements`, or a `sessions` table much larger than the number of logged-in
  devices. *Fix:* keep the return-to page in a short-lived cookie of its own instead of
  the session, or a faster store (e.g. Redis) or signed cookie sessions.
- [ ] **In-memory rate limiters.** Their maps grow with distinct keys between sweeps and
  aren't shared between instances. The login and signup limits (#182, #183) add maps
  keyed by client IP and by any submitted email, so a stream of made-up emails or
  rotating addresses grows them within a window: the sweep only drops expired entries.
  *Signal:* process memory climbing during login floods. *Fix:* cap each map and refuse
  new keys past it, or move the limits to the proxy or edge. Revisiting the limiter in
  general is the "Rate limiter approach" item under Security.
- [ ] **Queued password hashing** (#184). Argon2 runs in at most `GOMAXPROCS` slots, and
  requests beyond that wait in line instead of failing: logins and signups, and from
  #263–#268 password changes, resets, email changes and account deletions, which also
  hash or verify a password. *Signal:* slow logins under load, or login requests
  running past `WriteTimeout`. *Fix:* bound the queue and return 503 with `Retry-After`
  when it's full.

Hosting (once deployed; see Deployment in decisions.md):

- [ ] **Small instance and database.** The web service is a Render Starter instance (512
  MB, a fraction of a CPU), and Postgres is the smallest paid plan with little storage.
  Argon2 hashing runs at most `GOMAXPROCS` at once (1 here, since Go sizes it to the
  container's CPU limit). The common-password list (#256) adds about 1 MB (the 450 KB
  file and its 700 KB index).
  *Signal:* Render's memory and CPU graphs near their limits, restarts for running out of
  memory, slow logins, or database storage above 80%. *Fix:* the next instance or
  database size, or the separate worker process under Feed ingestion.
- [ ] **Email sending limits** (#259, #260). Resend's free tier allows 100 emails a day
  and 3,000 a month, and one mail worker sends one email at a time. *Signal:* sends
  failing with quota or rate-limit errors in the logs, or emails waiting in the outbox
  for minutes. *Fix:* a paid plan, the move to SES (see Accounts), or more mail workers
  (`-mail-workers`).
- [ ] **Database connections.** Each instance opens up to `-db-max-open-conns`
  connections (default 25), and small Postgres plans allow few. Production sets
  `RSS_DB_MAX_OPEN_CONNS=10`. *Signal:* "too many connections" errors, or connections
  waiting in `sql.DBStats`. *Fix:* lower it per instance, or a bigger plan.
- [ ] **The database is never idle.** Refresh workers poll every 15 seconds, so
  usage-billed databases that scale to zero (Neon and similar) would bill every hour of
  the month. Not a problem on Render. *Signal:* only matters if we move to such a
  database. *Fix:* sleep until the next feed is due instead of polling.
