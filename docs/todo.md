# TODO

Work that has been identified but does **not** yet have a GitHub issue. Items marked
"needs planning" are too large or too open-ended to hand to an agent as-is; plan them
first and split them into issues. When an item gets an issue, replace it here with a link
or delete it.

## Feed ingestion

Feeds are fetched and their articles saved (discovery, the Refresh button and the
feed page's article list), but only on demand.

- [ ] **Background refresh (needs planning).** Re-fetch feeds on a schedule. The lookup
  worker loop (#64) is a model for running this inside `www`, and it will call the same
  `ingest.Refresher` as the Refresh button. Deliberately left out of the on-demand ingestion work,
  because each only pays off when fetches repeat unattended:
  - Scheduling: use the `feeds` table itself as the queue (e.g. a `next_fetch_at` column
    claimed with `FOR UPDATE SKIP LOCKED`), not a separate jobs table. Never-fetched feeds
    (`last_fetched_at IS NULL`) are the most due. Decide what the Refresh button becomes
    (e.g. "set `next_fetch_at = now()`", or remove it).
  - Conditional GET: store ETag / Last-Modified per feed and send `If-None-Match` /
    `If-Modified-Since`. `fetch.Client.Get` can't send extra request headers yet.
  - Errors and backoff: record the last attempt and last error per feed
    (`last_fetched_at` only records the last *success*), back off on repeated failures,
    and show "last fetch failed" on the feed page. Retry on the next cycle, never inside a
    single fetch.
  - Redirects and gone feeds: update the feed URL on permanent redirects (301/308; needs
    `fetch` to report them, and `feeds.url` is unique, so two feeds can collide), and stop
    fetching on 410 Gone.
  - Article retention: whether to delete old articles, and when.

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

1. **Plan** background refresh, then subscriptions and reading, in their own sessions,
   and turn them into issues. Background refresh runs alongside the lookup worker, using
   the shutdown context from #87.

Merge one PR at a time; each branch should pull in the latest `main` before opening its PR.
