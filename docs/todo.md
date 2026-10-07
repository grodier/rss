# TODO

Work that has been identified but does **not** yet have a GitHub issue. Items marked
"needs planning" are too large or too open-ended to hand to an agent as-is; plan them
first and split them into issues. When an item gets an issue, replace it here with a link
or delete it.

## Feed ingestion

Feeds are fetched and their articles saved (discovery, the Refresh button and the
feed page's article list), but only on demand.

Background refresh is planned; implement in this order: #119 → (#120 and #121 in
parallel) → #122 → #123 → #124.

- #119 Record fetch attempts and schedule each feed's next fetch (`next_fetch_at`,
  failure backoff 1h doubling to 24h).
- #120 Refresh due feeds in the background (`internal/refresh` workers inside `www`).
- #121 Show fetch failures on the feed page.
- #122 Conditional GET (ETag / Last-Modified).
- #123 Stop fetching feeds that answer 410 Gone.
- #124 Honor `Retry-After` and `Cache-Control` when scheduling.

Decisions made while planning (don't reopen them in these issues): workers run inside
`www`, not as a cron-hit endpoint or a separate service; the `feeds` table is the queue,
claimed with a lease instead of a status column; every feed is refreshed until
subscriptions exist; the Refresh button stays synchronous and goes through the same
`ingest.Refresher`; RSS `<ttl>` / `sy:updatePeriod` are ignored.

Later, each with when it pays off:

- [ ] **Adaptive refresh interval.** Fetch busy feeds often and dormant ones rarely, from
  posting frequency: count articles published in the last 14 days (`n`), interval =
  clamp(14 days ÷ 4n, 15 min, 24 h), 24 h when `n = 0`, then apply the `Retry-After` /
  `Cache-Control` floors (#124). Worth it at a few thousand feeds, or when most fetches
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

## Feed discovery

Done in #50 (search, site pages, background lookups). Later improvements, not issues
yet:

- [ ] **Progressive enhancement for lookups.** Replace the status page's
  `<meta http-equiv="refresh">` with JavaScript polling, server-sent events or websockets.
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

## Subscriptions and reading (needs planning)

- [ ] **Subscriptions.** Replace the placeholder `POST /subscribe`, show each user only
  their own feeds, and support unsubscribing. Subscribe/unsubscribe lives on the feed page
  that discovery (#50) leads to.
  Background refresh (#120) then refreshes only feeds with at least one subscriber (add
  `AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = feeds.id)` to `ClaimDue`),
  and an unsubscribed feed is refreshed when its page is viewed and its last fetch is
  older than `ingest.RefreshInterval`.
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

1. Background refresh: #119 → (#120 and #121) → #122 → #123 → #124.
2. **Plan** subscriptions and reading in their own session, and turn them into issues.

Merge one PR at a time; each branch should pull in the latest `main` before opening its PR.
