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
  aren't shared between instances. Covered by the rate-limiter item under Security.

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
  - Forms: signup (invalid email, short or weak password, duplicate email), login (wrong
    credentials, locked out or rate limited), empty or oversized input, a missing or
    expired CSRF token, a malformed form body.
  - Auth and sessions: visiting a login-only page while logged out (redirect and
    message), an expired session mid-action, logging out, a logged-in user on `/login`.
  - HTTP errors: 403, 404 (unknown feed, site or lookup id; malformed id), 405, 413, 429
    (the lookup rate limit), and 500, including a database outage.
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
  folders or pinned feeds). Plan it after the reading experience. Open question: how it
  relates to the standalone feed page.
- [ ] **OPML import/export.** Import subscriptions from another reader and export your own.

## Reading

Planned; issues #147–#162. Work them roughly in this order (each issue names its
dependencies). Several edit the `articles` table and `saveArticles`, so merge one at a time.

1. Data at ingest, independent of each other: arrival position `timeline_at` (#147),
   plain-text excerpts and `DisplayTitle` (#148), feed-declared images (#149),
   `canonical_url` (#150).
2. `internal/sanitize` with bluemonday (#151), then the article page `/articles/{id}`
   (#152).
3. Shared article-row cards on the feed page (#153), the timeline at `/` (#154), feed page
   paging (#155).
4. Read state (#156), local times and the first JavaScript (#157), outbound links that
   count as reads (#158), infinite scroll (#159).
5. Date-only timestamps (#160), read state across copies (#161), one timeline entry per
   article with "Also in" (#162).

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
- **Rows are cards** (one design for the timeline and feed pages): optional
  feed-declared image, title linking to the in-app article page, feed, time, two-line
  excerpt, and a small link to the original. Untitled articles use the start of the
  excerpt.
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

## Security

- [ ] Rate-limit login and signup. Reuse the limiter added for lookups in #67.
- [ ] Evaluate whether the in-memory rate limiter (`internal/server/ratelimit.go`) is the
  right approach long term. It resets on restart and isn't shared across instances. Before
  running more than one instance, or if limits need to survive restarts, compare options
  (e.g. Postgres-backed counters, a reverse-proxy/edge limit, Redis) and decide. No
  decision yet.

## Suggested order

1. Build the reading experience: issues #147–#162, in the order under Reading.
2. Rate-limit login and signup (Security); small and independent, can go in parallel.
3. Once the initial features are done, **plan** the error-state walkthrough and run the
   first pass; then repeat it periodically.
4. Before production or a few thousand feeds, **plan** load and performance testing.

Merge one PR at a time; each branch should pull in the latest `main` before opening its PR.
