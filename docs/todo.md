# TODO

Work that has been identified but does **not** yet have a GitHub issue. Items marked
"needs planning" are too large or too open-ended to hand to an agent as-is; plan them
first and split them into issues. When an item gets an issue, replace it here with a link
or delete it.

## Feed ingestion

Done. Feeds are fetched and their articles saved on discovery, from the feed page's
Refresh button, and in the background by `internal/refresh` workers inside `www`
(`-refresh-workers`, default 2). Shipped in #126 and #128–#133: fetch attempts and
`next_fetch_at` scheduling with failure backoff, background workers, fetch failures on the
feed page, conditional GET, 410 Gone, and `Retry-After` / `Cache-Control`.

Decisions made while planning (don't reopen them without a reason): workers run inside
`www`, not as a cron-hit endpoint or a separate service; the `feeds` table is the queue,
claimed with a lease instead of a status column; only feeds someone subscribes to, or
whose page view requested a refresh (#137), are refreshed; the Refresh button stays
synchronous and goes through the same `ingest.Refresher`; RSS `<ttl>` /
`sy:updatePeriod` are ignored.

Later, each with when it pays off:

- [ ] **Adaptive refresh interval.** Fetch busy feeds often and dormant ones rarely, from
  posting frequency: count articles published in the last 14 days (`n`), interval =
  clamp(14 days ÷ 4n, 15 min, 24 h), 24 h when `n = 0`, then apply the `Retry-After` /
  `Cache-Control` floors (#133). Worth it at a few thousand feeds, or when most fetches
  are 304s or find no new articles.
- [ ] **Permanent redirects (301/308).** Update `feeds.url`. Needs `fetch` to report the
  redirect chain, and a decision for the unique-URL collision when two feeds end up at
  the same URL (merge, or keep the old one). Redirects are already followed, so this can
  wait until moved feeds are a visible problem.
- [ ] **Give up on long-dead feeds.** E.g. stop after 30 days of consecutive failures and
  say so on the feed page. When dead feeds are a noticeable share of fetches.
- [ ] **Per-host politeness.** At most N concurrent fetches per host. When one host has
  many feeds (category feeds, platform hosts like Substack or Medium).
- [ ] **Separate worker process or `-once` mode.** Move `internal/refresh` to its own
  process (`cmd/worker` or a `-role` flag) when fetching measurably slows web requests or
  should scale separately from web instances. On a scale-to-zero host, add a `-once`
  command that drains the due feeds, run by the platform's cron, rather than an HTTP
  endpoint.
- [ ] **WebSub (push).** Subscribe to hubs that feeds advertise for near-instant updates.
  Only when freshness matters more than it does now; needs a public callback URL.
- [ ] **Article retention (needs planning before launch).** Whether to delete old
  articles, and when. Read state doesn't drive it (the timeline isn't an inbox), so a
  time- or count-based rule works (e.g. keep 6 months, or the newest N per feed), with
  `reads` rows deleted along with their articles (`ON DELETE CASCADE`). Before launch,
  weigh deleting against keeping: storage and backup cost and the "Articles grow without
  bound" watchlist entry on one side; the value of a long article archive (search,
  history, business uses of the data) and its copyright and privacy implications on the
  other. Saved articles (see Reading) must survive whatever rule is chosen.
- [ ] **Operational visibility.** An admin page or metrics (due backlog, failure rate,
  304 rate). When the app runs somewhere the logs aren't at hand.

## Performance watchlist

Things that are fine at today's scale but could become performance problems as feeds,
articles, users or instances grow. Each entry says what it is, the signal that it has
become a problem, and the planned fix. Don't fix these early: watch the signal, and use the
load tests below to measure. When planning adds a new risk, add it here (see CLAUDE.md,
"Writing issues"); when one is fixed, delete it.

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
- [ ] **Polling latency and idle queries.** Each idle refresh and lookup worker queries
  every poll interval (15 s for refresh), on every instance. A requested refresh can wait
  a full interval before a worker sees it. *Signal:* users wait noticeably for "Checking
  for new articles…", or idle claim queries show up in `pg_stat_statements`. *Fix:* Postgres
  `LISTEN`/`NOTIFY` from `RequestRefresh`, `Subscribe` and lookup requests to wake a
  worker, keeping the poll as a fallback.
- [ ] **Stale lookup resets scan `lookups`** (#195). Every instance's lookup runner resets
  stale jobs every minute, and no index covers `ResetStale`'s `status = 'running'`
  filter, so each reset scans the table, which keeps a row per site key ever looked up.
  *Signal:* `ResetStale` in `pg_stat_statements` totals, or a large `lookups` table.
  *Fix:* a partial index on `started_at WHERE status = 'running'`.
- [ ] **One statement per article in `saveArticles`.** Each fetched item is a separate
  upsert inside the `SaveFetch` transaction. *Signal:* `SaveFetch` time dominates the
  `feed refreshed` log's `duration` for large feeds (see the "Large feeds" scenario
  below). *Fix:* a single multi-row upsert (`unnest` of arrays) per fetch.
- [ ] **Writes from feed page views** (#137). Viewing a stale feed runs an `UPDATE`
  (`RequestRefresh`). The `refresh_requested_at IS NULL` guard limits it to once per stale
  period per feed, so this is unlikely to matter. *Signal:* `RequestRefresh` shows up in
  `pg_stat_statements` or in row-lock waits on `feeds`. *Fix:* skip the call unless the
  feed has been stale for a while, or move requests to their own table.
- Also listed under Feed ingestion: adaptive refresh interval, per-host politeness,
  separate worker process, giving up on long-dead feeds.

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
  page, matching reads across copies by `canonical_url` (#161). *Signal:* it shows up in
  `pg_stat_statements`, or slows for users with many reads. *Fix:* fold it into the
  page's main query, or store the canonical URL on `reads`.

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
- [ ] **Sessions in Postgres.** scs reads the `sessions` row on every request in the
  session group and writes it whenever the session changes (flash messages, login).
  Expired rows are swept periodically. *Signal:* session queries in `pg_stat_statements`
  or a large `sessions` table. *Fix:* a faster store (e.g. Redis) or signed cookie
  sessions.
- [ ] **In-memory rate limiters.** Their maps grow with distinct keys between sweeps and
  aren't shared between instances. The login and signup limits (#182, #183) add maps
  keyed by client IP and by any submitted email, so a stream of made-up emails or
  rotating addresses grows them within a window: the sweep only drops expired entries.
  *Signal:* process memory climbing during login floods. *Fix:* cap each map and refuse
  new keys past it, or move the limits to the proxy or edge. Revisiting the limiter in
  general is covered by the rate-limiter item under Security.
- [ ] **Queued password hashing** (#184). Argon2 runs in at most `GOMAXPROCS` slots, and
  logins and signups beyond that wait in line instead of failing. *Signal:* slow logins
  under load, or login requests running past `WriteTimeout`. *Fix:* bound the queue and
  return 503 with `Retry-After` when it's full.

Hosting (see Deployment):

- [ ] **Small instance and database.** The web service is a Render Starter instance (512
  MB, a fraction of a CPU), and Postgres is the smallest paid plan with little storage.
  Argon2 hashing runs at most `GOMAXPROCS` at once (1 here, since Go sizes it to the
  container's CPU limit). *Signal:* Render's memory and CPU graphs near their limits,
  restarts for running out of memory, slow logins, or database storage above 80%.
  *Fix:* the next instance or database size, or the separate worker service under
  Deployment.
- [ ] **Database connections.** Each instance opens up to `-db-max-open-conns`
  connections (default 25), and small Postgres plans allow few. Production sets
  `RSS_DB_MAX_OPEN_CONNS=10`. *Signal:* "too many connections" errors, or connections
  waiting in `sql.DBStats`. *Fix:* lower it per instance, or a bigger plan.
- [ ] **The database is never idle.** Refresh workers poll every 15 seconds, so
  usage-billed databases that scale to zero (Neon and similar) would bill every hour of
  the month. Not a problem on Render. *Signal:* only matters if we move to such a
  database. *Fix:* sleep until the next feed is due instead of polling.

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
  - Forms: signup (invalid email, short or weak password, a duplicate email that must
    look like a success (#185), signup turned off (#186), rate limited (#182)), login
    (wrong credentials, rate limited per IP (#182) or per email (#183)), empty or
    oversized input, a missing or expired CSRF token, a malformed form body.
  - Auth and sessions: visiting a login-only page while logged out (redirect and
    message), an expired session mid-action, logging out, a logged-in user on `/login`.
  - HTTP errors: 403, 404 (unknown feed, site or lookup id; malformed id), 405, 413, 429
    (the lookup rate limit; login and signup re-render their form instead), and 500,
    including a database outage.
  - Lookups and discovery: invalid URL, a URL that is blocked (private address, bad
    scheme), a site that is unreachable, times out, redirects too often, returns a non-HTML
    or huge body, has no feeds, or has a feed that fails to parse; a lookup that is
    pending, running, failed or done; the status page after the lookup row is gone.
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

## Feed discovery

Done in #50 (search, site pages, background lookups). Later improvements, not issues
yet:

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

## Subscriptions

Subscriptions are done. Shipped in #140–#143 (issues #135–#138): subscribe and unsubscribe
from the feed and site pages, `/feeds` as "My feeds" (the user's subscriptions), background
refresh only for subscribed feeds with a queued refresh when a stale feed is viewed, and
subscribed markers in search results.

Decisions made while planning (don't reopen them without a reason): subscribing and
unsubscribing are one click with no confirmation, and redirect back to the page they were
posted from; the global "latest feeds" list is removed, not moved; subscribed state comes
from a separate `SubscribedFeedIDs` query rather than a user parameter on feed queries;
refresh requests from page views are a `feeds` column worked by the existing refresh
workers, never a goroutine or in-memory queue; subscriptions are private (no subscriber
counts).

Later, not issues yet:

- [ ] **Feed tabs on the site page.** Show a site's articles one feed at a time, as tabs
  that are sub-routes of the site page (e.g. `/sites/{id}/feeds/{feedID}`). This is for
  browsing a site's feeds in context. It isn't a way to organise subscriptions (no
  folders or pinned feeds). Open question: how it relates to the standalone feed page.
- [ ] **OPML import/export.** Import subscriptions from another reader and export your own.

## Reading

Done. Shipped in #164–#179 (issues #147–#162): arrival-ordered timeline at `/` with one
entry per article and "Also in", article pages with sanitized HTML, shared article cards,
excerpts and feed-declared images, paging with infinite scroll, local times, and read
state (article opens and click-throughs, matched across copies by `canonical_url`).

Decisions made while planning (don't reopen them without a reason):

- **A timeline, not an inbox.** Read state is recorded but only shown as a quiet marker on
  read rows (dimmed, like a visited link). No unread counts, badges, "N new" indicators,
  "mark all read", or manual mark read/unread. No unread filter for now.
- **What counts as read:** opening the article page, or clicking through to the original
  (via `<a ping>`, with a `sendBeacon` fallback in `app.js` because Firefox disables
  ping). Scrolling past doesn't; "seen" isn't recorded. Reads are per user and article
  (`reads` table, with `read_at`), independent of subscriptions; an article counts as read
  if any copy with the same `canonical_url` was read.
- **Timeline order is arrival order.** Each article gets `timeline_at` once, on insert:
  the save time for news, its published date for backlog (a feed's first save, or an item
  dated more than 24 h before the feed's previous successful fetch). New items can't land
  below ones already seen, and republished archives don't flood the top. No bump on
  subscribe. Displayed dates may look out of order; accepted. Feed pages keep publication
  order.
- **The timeline is `/`** for logged-in users (nav label stays "Home"). Duplicates are
  collapsed at query time on `canonical_url`, keeping the earliest arrival, with "Also in"
  on the row. Feed pages show every copy.
- **Rows are divided sections with a preview card** (one design for the timeline and
  feed pages): the feed's avatar and name with the time, then a card with the optional
  feed-declared image, title, two-line excerpt and a small link to the original. The
  whole card links to the in-app article page. Feed pages leave out the avatar and
  name. Untitled articles use the start of the excerpt.
- **Article page:** sanitized content, else summary, plus "Read on {site}". No
  previous/next. Any logged-in user can open any article.
- **Article HTML** is sanitized at render time with bluemonday, relative URLs resolved,
  iframes replaced by a link, links opened in a new tab with `noopener noreferrer`.
  Images load from the publisher with `referrerpolicy="no-referrer"` and lazy loading; the
  CSP allows `img-src https:`. No proxy (see below).
- **Paging works without JavaScript** ("Older articles" with a `?before=` cursor);
  infinite scroll enhances it. Logged-in pages keep `Cache-Control: no-store`; the script
  keeps the URL on the visible row so Back and reload land nearby.
- **JavaScript is progressive enhancement:** one plain file (`static/js/app.js`), no
  build step, no inline scripts, no libraries without a justification.
- **Times:** server-rendered UTC with a `<time>` the script localizes; date-only feed
  dates show just the date and aren't localized. No day headers or relative times.
- **New articles don't appear live** while you scroll; a reload shows them on top.

Later, not issues yet:

- [ ] **Saved articles.** Save or star articles to keep and find later. Must be exempt
  from article retention.
- [ ] **Enrich articles from their page.** One background fetch of the article's web page
  (through `internal/fetch`, its own queue, polite to publishers) could provide:
  `og:image` when the feed has no image; a better title for untitled items (`og:title`,
  `<h1>`, or eventually AI-assisted heuristics over headings, URL path, images and text);
  and the full text when the feed only sends a summary (needs heuristics for when it's
  worth fetching, and readability-style extraction). Costs an extra request per article,
  some sites block bots, and it needs a retry and failure story.
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
- [ ] **Sandboxed embeds.** Allow selected iframes (e.g. `youtube-nocookie.com`) with the
  `sandbox` attribute instead of replacing every embed with a link.
- [ ] **First `<img>` as the card image.** Fall back to the first content image when the
  feed declares none, filtering out tracking pixels, avatars and emoji (size attributes,
  known hosts). Or rely on `og:image` from enrichment instead.
- [ ] **Keyboard shortcuts.** E.g. `j`/`k` to move between rows, `o` to open, `v` to open
  the original.
- [ ] **"You were here" divider.** A subtle marker in the timeline at your last visit
  (possible because positions never change). No counts.
- [ ] **Unread filter.** Maybe never; it pulls toward the inbox model.
- [ ] **Timezone setting and day headers.** Only if local times via JavaScript aren't
  enough (e.g. "Today"/"Yesterday" groups rendered server-side).

## Design

A modern, minimal look modelled on social timelines. Decisions made (don't reopen them
without a reason):

- **One centered column, nav on the left.** A rail with icons and labels on wide
  screens, icons only on medium ones, and a top bar (logo, logout) with a bottom tab
  bar on phones. CSS only; no JavaScript.
- **Lists are sections separated by dividers, not boxes.** A card is only for a preview
  of something elsewhere: an article now, Open Graph data later (see "Enrich articles
  from their page" under Reading).
- **Avatars are letters on a tint chosen by site** (`avatarTone`), so a site's feeds
  share a color, until we have site icons.
- **Light theme only for now.** Colors are custom properties on `:root` in `main.css`,
  so a theme can redefine them in one place. System font stack; icons are inline SVG
  partials (`templates/partials/icons.html`), not an icon font or library.

Later, not issues yet:

- [ ] **Site icons (favicons) as avatars.** Show the site's icon instead of its letter.
  Find it during lookup (`<link rel="icon">` or `apple-touch-icon`, preferring one that
  is sharp at 40px, else `/favicon.ico`) through `internal/fetch`, store its URL on
  `sites`, and keep the letter as the fallback when there is none or it fails to load.
  Loading icons from publishers has the same privacy cost as article images; the image
  proxy (see Reading) would cover both.
- [ ] **Themes, including dark mode (needs planning).** A theme choice (light, dark,
  follow the system) on a settings page, which doesn't exist yet, applied as a
  `data-theme` attribute on `<html>` that redefines the color tokens. Needs somewhere
  to store user preferences. The avatar tints (`.tone-1` to `.tone-6`) need dark
  variants too.
- [ ] **Asset build step (needs planning).** Bundle and minify the CSS and JavaScript
  for production, writing content-hashed file names. Templates link assets through
  `asset` (#199), so only `asset` changes: it looks names up in the build's manifest
  instead of hashing files at startup, and every file under `/static` gets the immutable
  `Cache-Control`. This reverses the "no build step" decision (Reading, and CLAUDE.md),
  so planning it updates both. Pages must still work without JavaScript.

## Pre-alpha testing

What's needed before other people use the app on a public URL. Running it locally for
yourself needs none of this.

Decided (don't reopen it without a reason): testers' data isn't kept. Schema changes keep
editing `migrations/00001_init.sql`, and rebuilding the database deletes every account,
subscription and read. Testers are told their data may be wiped. This holds until it's
explicitly changed (see "Prototype stage" in CLAUDE.md).

- [ ] **Deploy** (see Deployment). The app runs in production with signup off once its
  issues are done.
- [ ] **Shared invite code.** Signup is off unless `-signup` is set (#186), and accounts
  are made by starting the app with it briefly. Before inviting testers, replace
  `-signup` with `-invite-code`: signup is open when it's set, and the code arrives
  pre-filled from a link like `/signup?invite=…`. Remove the bool flag rather than
  keeping both. Testers choose their own password, which matters while there's no reset.
  Rotating the code is a restart. Keep the per-IP signup limit (#182) so the code can't
  be brute-forced.
- [ ] **Single-use invites.** A table of invites with expiry and a CLI command to make
  them. Only once people we don't know personally are invited.
- [ ] **Account management.** No password change, password reset or account deletion.
  With a few known testers, passwords can be reset by hand in the database. Self-service
  reset needs email, which the app doesn't send yet.
- [ ] **"Check your inbox" signup (needs email).** #185 makes a duplicate signup look
  like a success, so signup doesn't reveal which emails have accounts. The cost: someone
  who forgot they have an account thinks they made a new one, and their new password
  fails at login. Once the app sends email, signup should say "check your inbox", and an
  existing owner should get a "you already have an account" email instead.
- [ ] **Log in to the timeline.** `loginFormHandler` redirects to `/feeds`. Since `/` is
  now the timeline, send users there instead.
- [ ] **Session lifetime.** Sessions expire 12 hours after login
  (`sessionManager.Lifetime` in `cmd/www/application.go`), so a daily reader logs in
  every day. Consider a longer lifetime with an idle timeout (`sessionManager.IdleTimeout`).
- [ ] **Malformed form bodies return 500.** `decodePostForm` errors go to
  `serverErrorHTML`, so a bad body is a 500 when it should be a 400, and form bodies
  aren't capped beyond `ParseForm`'s 10 MB default. Return 400 for decode errors and cap
  bodies with `http.MaxBytesReader` (413 when exceeded). The error-state walkthrough
  lists both states.

## Deployment

Planned in #218. Issues, in order:

1. #208 (manual): set up Render, Cloudflare R2 and GitHub. Any time before #213.
2. #209 container image and #210 configuration from environment variables and JSON
   logs, in parallel.
3. #211 `migrate` command and #212 client IP behind a proxy, both after #210.
4. #213 Terraform for Render.
5. #214 (manual): first apply and go-live.
6. #215 deploy, reset and Terraform workflows, then #216 database backups to R2.
7. #217 (manual): exercise deploys, rollback, resets and backups in production, and add
   the uptime check.

Decisions made while planning (don't reopen them without a reason):

- **Render, Virginia (US East).** A Starter web service (always on, 512 MB) and the
  smallest paid Render Postgres 17, about $15 a month with backups. Chosen over a VPS (no
  server to look after), AWS ECS/RDS (about $50–60 a month before there are paying
  users), Fly.io (its managed Postgres starts at $38) and Neon (the refresh workers poll
  every 15 seconds, so it never scales to zero). Check prices again before the first
  `terraform apply`.
- **Portable by contract, not by host.** Every host-specific piece sits behind the same
  six things, so moving to Fly, AWS or a VPS is a new Terraform module, a new deploy step
  and a DNS change, with no app code changes: one container image built by GitHub Actions
  and pushed to GHCR; configuration from `RSS_*` environment variables; a `www migrate`
  command run before new code goes live (Render's pre-deploy command, Fly's
  `release_command`, an ECS one-off task); `/healthcheck` for the host's health checks;
  JSON logs on stdout; plain Postgres 17. Don't add host-specific code paths.
- **Terraform**, with the official Render provider and the Cloudflare provider (for R2).
  State lives in an R2 bucket. After the first apply (by hand, during setup), applies run
  only from GitHub Actions, one at a time.
- **No domain yet.** The app is served at its `*.onrender.com` URL with Render's HTTPS.
  A custom domain (possibly a subdomain of georgerodier.com) is a later, separate change:
  Cloudflare DNS plus a Render custom domain, both in Terraform.
- **Migrations are built into the binary** (`www migrate`) and run before each deploy,
  not at app startup, so they also work with several instances, and a failed migration
  aborts the deploy while the old version keeps serving. One artifact carries the code
  and the migrations it expects, and the same command runs everywhere.
- **Wiping the database is never automatic.** While pre-alpha, `migrate` refuses to run
  when `migrations/*.sql` have changed since they were applied (code-only changes never
  touch the database). It wipes and rebuilds only when given the new migrations' exact
  fingerprint: locally `make db/reset`, in production the "Reset production database"
  workflow (typed confirmation), which sets `RSS_ALLOW_RESET_TO` and redeploys. A
  one-off Render job can't do it, since it runs the old image.
- **Images are public on GHCR**, like the source. A private image would need a
  long-lived GitHub token stored at Render. Deploys reference images by digest, and CI
  attests build provenance. No secrets at build time.
- **Deploys switch with no downtime.** Render starts the new version, waits for
  `/healthcheck`, switches traffic, then stops the old one (30-second shutdown delay,
  matching the app). A failed health check or pre-deploy leaves the old version
  serving. Old and new versions overlap for a few seconds: background workers are safe
  (`SKIP LOCKED`) and in-memory rate limits reset. Rollback redeploys an earlier image and
  doesn't undo migrations. No blue-green slots or canaries on Render; Fly or ECS offer
  them if we move.
- **Client IP:** `X-Forwarded-For`, taking the rightmost address that isn't in
  `-trusted-proxies`, which defaults to trusting nobody. Render's proxy keeps whatever
  `X-Forwarded-For` the client sent and appends to it, so the leftmost entry can be forged.
  Render's proxy address range is measured after the first deploy, not guessed.
- **Backups:** Render's point-in-time recovery (3 days on the Hobby workspace) plus a
  nightly `pg_dump` to R2 from a Render cron job, kept 35 days. The R2 copy is at a different provider from the
  database, and restoring it is also how the database moves to a new host.
- **Logs** stay in Render's log viewer for as long as Render keeps them (a few days on
  the Hobby workspace; the README records the current figure). No log shipping, so client
  IPs aren't kept anywhere else.
- **Deploys:** every push to `main` that passes CI deploys automatically; merging the PR
  is the approval. Infrastructure changes (`terraform apply`) wait for the owner's
  approval in the `infrastructure` GitHub environment, since some Terraform changes
  replace or destroy resources. The database also has `prevent_destroy`.

Later, not issues yet:

- [ ] **Custom domain.** Cloudflare DNS record, Render custom domain, and HSTS once the
  domain is settled.
- [ ] **PR preview environments.** Render preview environments need the Professional
  workspace (about $19 per user a month plus compute). Only from same-repo branches: never
  `pull_request_target` with fork code, since this repository is public.
- [ ] **Separate worker service.** If web and background work compete for the Starter
  instance, run the same image as a Render background worker with the web service's
  workers set to 0 (`RSS_LOOKUP_WORKERS=0`, `RSS_REFRESH_WORKERS=0`), for about $7 more.
- [ ] **Leaving pre-alpha.** Remove the fingerprint check, `-allow-reset-to` and the
  "Reset production database" workflow, and make schema changes new migrations (see
  CLAUDE.md). From then on every migration must also work with the version still
  running during a deploy ("expand/contract": add, deploy code using both, backfill,
  deploy code using the new, then drop the old). Give migrations their own Postgres role
  that owns the schema, and the app a role that can only read and write data.

## Security

Login and signup protection is done. Shipped in #188–#192 (issues #182–#186): per-IP
limits on login (20 per 10 minutes) and signup (10 per hour), at most 10 failed logins
per email per 15 minutes, at most `GOMAXPROCS` Argon2 hashes at once, signup and login
that don't reveal which emails have accounts, and signup off unless `-signup` is set.

Decisions made while planning (don't reopen them without a reason):

- **In-memory limits** (`internal/server/ratelimit.go`) are enough for one instance, and
  they reset on restart. Revisiting them is the last item below.
- **IPv6 clients are keyed by their /64**, since one client usually controls a whole /64.
- **A limited login or signup re-renders its form with 429** and `Retry-After`, keeping
  the email filled in. The generic 429 page is only for lookups.
- **Failed logins are counted per submitted email, whether or not it has an account**,
  so a 429 doesn't reveal which emails do. Successful logins don't count or reset it.
- **A duplicate signup looks exactly like a successful one**, and login verifies a dummy
  hash for unknown emails so it takes as long as a wrong password.
- **Hashing beyond the cap waits in line rather than failing** (see "Queued password
  hashing" in the Performance watchlist).
- **`clientIP` reads only `RemoteAddr`** until #212 reads `X-Forwarded-For` from trusted
  proxies (see Deployment).

Later, not issues yet:

- [ ] **Per-email login lockout.** #183 lets anyone keep a known email locked out of
  login for 15 minutes at a time by failing logins for it. That's accepted while users
  are a handful of known people. Revisit before signup is open to strangers (e.g. key
  the limit on IP and email together, or slow down with backoff instead of refusing).
- [ ] Evaluate whether the in-memory rate limiter (`internal/server/ratelimit.go`) is the
  right approach long term. It resets on restart and isn't shared across instances. Before
  running more than one instance, or if limits need to survive restarts, compare options
  (e.g. Postgres-backed counters, a reverse-proxy/edge limit, Redis) and decide. No
  decision yet.

## Suggested order

1. Fixes and quick wins, in parallel with each other and with planning deployment:
   #194 (concurrent logins get past the per-email limit), #195 (stuck lookups), #196
   (article lists load full HTML), #197 (request log status and duration), #198 (HEAD
   requests), #199 (static asset caching) and #200 (dead code). #197, #198 and #199 each
   edit `router.go`, and #198 and #200 each edit `errors.go`; see each issue's notes.
2. Deployment, #208–#217 in the order listed under Deployment. Once they're done, the
   app runs in production with signup off.
3. Before inviting testers: the shared invite code (Pre-alpha testing).
4. The rest of Pre-alpha testing (account management, logging in to the timeline,
   session lifetime, malformed form bodies), in parallel with testing.
5. Now that the initial features are done, **plan** the error-state walkthrough and run
   the first pass; then repeat it periodically.
6. Before production or a few thousand feeds, **plan** load and performance testing.

Merge one PR at a time; each branch should pull in the latest `main` before opening its PR.
