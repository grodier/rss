# TODO

Work that has been identified but does **not** yet have a GitHub issue. Items marked
"needs planning" are too large or too open-ended to hand to an agent as-is; plan them
first and split them into issues. When an item gets an issue, replace it here with a link
or delete it.

## Feed ingestion (needs planning)

The core missing feature: feeds are never fetched for their articles.

- [ ] **Fetch and save articles.** Feed discovery (#50) creates feeds with their title,
  site URL and description, but no articles. Fetch each feed and save its items, reusing
  `internal/fetch` (#52) and the parser chosen in #54. The `articles` table needs a unique
  `(feed_id, external_id)` so re-fetching doesn't create duplicates.
- [ ] **Background refresh.** Re-fetch feeds on a schedule using `last_fetched_at`, skip
  unchanged feeds via ETag / Last-Modified, and back off on errors. The lookup worker loop
  (#64) is a model for running this inside `www`.

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
- [ ] **Reading experience.** List articles on the feed page, add an "all my feeds"
  timeline, and track read/unread per user (needs a new table).

## Security

- [ ] Rate-limit login and signup. Reuse the limiter added for lookups in #67.
- [ ] Evaluate whether the in-memory rate limiter (`internal/server/ratelimit.go`) is the
  right approach long term. It resets on restart and isn't shared across instances. Before
  running more than one instance, or if limits need to survive restarts, compare options
  (e.g. Postgres-backed counters, a reverse-proxy/edge limit, Redis) and decide. No
  decision yet.

## Suggested order

1. #87: signal handling only in `cmd/www`, `server.Serve` takes a context.
2. **Plan** feed ingestion, then subscriptions and reading, in their own sessions, and
   turn them into issues. Ingestion's background refresh will run alongside the lookup
   worker, so do #87 first so both use the same shutdown context.

Merge one PR at a time; each branch should pull in the latest `main` before opening its PR.
