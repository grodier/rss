# TODO

Work that has been identified but does **not** yet have a GitHub issue. Items marked
"needs planning" are too large or too open-ended to hand to an agent as-is; plan them
first and split them into issues. When an item gets an issue, replace it here with a link
or delete it.

## Feed ingestion (needs planning)

The core missing feature: feeds are stored by URL only and never fetched.

- [ ] **Fetch and parse feeds.** When a feed is added, fetch and parse it (e.g. with
  `gofeed`), fill in title, site URL and description, and save its articles. The
  `articles` table needs a unique `(feed_id, external_id)` so re-fetching doesn't create
  duplicates.
- [ ] **Background refresh.** Re-fetch feeds on a schedule using `last_fetched_at`, skip
  unchanged feeds via ETag / Last-Modified, and back off on errors.
- [ ] **Safe fetching.** The server will fetch user-supplied URLs, so: block private and
  internal addresses (SSRF), set timeouts, cap response size and redirects, and allow only
  http/https.

## Subscriptions and reading (needs planning)

- [ ] **Subscriptions.** Replace the placeholder `POST /subscribe`, show each user only
  their own feeds, and support unsubscribing. Adding a feed that already exists should
  subscribe the user to it (follow-on to #7). Needs the user ID in the request context
  (#40).
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

- [ ] Rate-limit login and signup.

## Suggested order

1. **Code structure:** #38 then #39; #40 after #39 (so it can test `authenticate` with a
   fake). In parallel with those, the template issues in order, since they touch the
   same files: #43, then #41, then #42.
2. **Then plan** feed ingestion and subscriptions in their own sessions, and turn them
   into issues.

Merge one PR at a time; each branch should pull in the latest `main` before opening its PR.
