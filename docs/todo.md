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
- [ ] **Article retention.** Whether to delete old articles, and when. Depends on read
  state; plan it with the reading experience.
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

Data growth:

- [ ] **Articles grow without bound.** Raw `summary` and `content` HTML is stored for every
  article forever. *Signal:* `articles` table and index size, slower `ListByFeed` and
  timeline queries, backup size. *Fix:* see "Article retention" under Feed ingestion.
- [ ] **Timeline deduplication at query time.** The planned timeline collapses duplicate
  articles across a user's feeds when it's queried ("One entry per article in the
  timeline"). *Signal:* timeline queries slow for users with many overlapping feeds.
  *Fix:* the indexed `canonical_url` column already planned there; if that isn't enough,
  precompute per-user timeline entries.
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
  - Later features: an article that can't be shown.
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

## Subscriptions and reading

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
- [ ] **Show feeds that have no title:** #145.

Reading (needs planning):

- [ ] **Reading experience.** Add an "all my feeds" timeline, show article content, and
  track read/unread per user (needs a new table). The feed page's article list (#110)
  shows titles and links only. Summary and content are stored as raw HTML (#106) and
  must be sanitized before rendering (e.g. bluemonday, a new dependency), along with
  resolving relative URLs inside them.
- [ ] **One entry per article in the timeline.** The same post often appears in several
  feeds (e.g. a site's "all posts" and category feeds). Feed pages should still show it in
  each feed, but a user's timeline should show it once, ideally with "Also in: X, Y".
  Approach agreed while planning ingestion:
  - Keep one `articles` row per feed (as #106 does) and collapse duplicates when the
    timeline is queried, not by sharing a row between feeds. Feed pages then stay
    faithful to each feed, and a bad match is fixed by changing the rule, not by un-merging
    stored data.
  - Match on a conservative normalized link: lowercase scheme and host, drop the
    fragment and known tracking parameters (`utm_*`, `fbclid`, …), keep the rest of the
    query. When in doubt, don't merge, since hiding a distinct article is worse than
    showing a duplicate. Proxy links (e.g. FeedBurner) and syndicated copies at other URLs
    won't match, which is acceptable.
  - Probably store the key as an indexed `canonical_url` column computed in
    `internal/ingest`. Not added yet because nothing uses it.
  - Open question: should read state follow the key, so reading one copy marks all of
    them read?

## Security

- [ ] Rate-limit login and signup. Reuse the limiter added for lookups in #67.
- [ ] Evaluate whether the in-memory rate limiter (`internal/server/ratelimit.go`) is the
  right approach long term. It resets on restart and isn't shared across instances. Before
  running more than one instance, or if limits need to survive restarts, compare options
  (e.g. Postgres-backed counters, a reverse-proxy/edge limit, Redis) and decide. No
  decision yet.

## Suggested order

1. **Plan** the reading experience in its own session and turn it into issues
   (subscriptions are done).
2. Rate-limit login and signup (Security); small and independent, can go in parallel.
3. Once the initial features are done, **plan** the error-state walkthrough and run the
   first pass; then repeat it periodically.
4. Before production or a few thousand feeds, **plan** load and performance testing.

Merge one PR at a time; each branch should pull in the latest `main` before opening its PR.
