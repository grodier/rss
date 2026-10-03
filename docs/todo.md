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
- Safe fetching: #52.

## Feed discovery

Planned and split into issues: #50 tracks #51–#67 (search, site pages, background
lookups). Later improvements, not issues yet:

- [ ] **Progressive enhancement for lookups.** Replace the status page's
  `<meta http-equiv="refresh">` with JavaScript polling, server-sent events or websockets
  (after #65).
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

## Code structure

Tracked as issues:

- #38 Pass request contexts through to database calls
- #39 Repository interfaces so handlers can be tested without a database (after #38)
- #40 Logged-in user's ID in the request context
- #41 Shared base layout and nav partials for templates
- #42 HTML instead of JSON / plain text for 404, 405 and 403
- #43 Clean-up: dead code, placeholder `main.js`, unused `Run` ctx, stale TODOs

## Small fixes

- [ ] `signup.html`: the submit button says "Add Feed" and the name field's label has
  `for="url"` instead of `for="name"`.
- [ ] 405 responses don't include an `Allow` header (chi doesn't set it when a custom
  `MethodNotAllowed` handler is installed).

## Security

- [ ] Rate-limit login and signup. Reuse the limiter added for lookups in #67.

## Suggested order

1. **Code structure:** #38 then #39; #40 after #39 (so it can test `authenticate` with a
   fake). In parallel with those, the template issues in order, since they touch the
   same files: #43, then #41, then #42.
2. **Feed discovery:** work through #50 in the order listed there.
3. **Then plan** article ingestion and subscriptions in their own sessions, and turn them
   into issues.

Merge one PR at a time; each branch should pull in the latest `main` before opening its PR.
