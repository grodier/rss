# Decisions

Decisions made while planning, with the reason for each and the issue or PR where it was
made. Don't reopen one without a new reason. A plan or PR that changes one edits it here
and says so in its description. Planned work is in [todo.md](todo.md), under the same
section names; how the code works is in [architecture.md](architecture.md) and
[features.md](features.md).

## Overall

- **One binary, one database.** `www` runs the web server and the background workers
  (lookups, feed refresh) as goroutines; Postgres is the only other infrastructure. It
  holds the job queues and the sessions too. One thing to deploy and operate, which is
  enough at this scale. Splitting out a worker process is a todo item with its trigger.
  (#50, #126)
- **Postgres tables are the job queues**, claimed with `FOR UPDATE SKIP LOCKED`, so
  several instances never take the same job and no queue service is needed. `lookups`
  has a status column; `feeds` is claimed with a lease on `next_fetch_at` instead of a
  status column. (#50, #126)
- **Server-rendered HTML; JavaScript is progressive enhancement.** Every page works
  without it. The rules that follow (one plain file, no build step, no libraries without
  a justification) are in CLAUDE.md. (#50, #163)
- **Outbound requests to user-influenced URLs only go through `internal/fetch`**, which
  blocks private addresses (SSRF) and limits time, size and redirects. (#50)
- **Cross-site requests are blocked with Go's `http.CrossOriginProtection`**
  (`Sec-Fetch-Site` / `Origin` headers) rather than CSRF tokens, so forms and sessions
  carry no tokens.
- **GET handlers never fetch user-supplied URLs or make changes on the user's behalf**,
  because `CrossOriginProtection` only guards non-GET requests. The only writes from GET are
  documented, idempotent upkeep: queuing a refresh of a stale feed and recording that the
  user opened an article. (#50, #137, #156)
- **Errors stored for debugging are never shown to users** (`lookups.error`,
  `feeds.last_error`). Pages say what happened in plain words. (#50, #129)

## Pre-alpha testing

- **Testers' data isn't kept**: schema changes keep editing the initial migration and
  wipe the database, and testers are told so. The rule and its scope are under "Prototype
  stage" in CLAUDE.md. (#180)

## Feed discovery

Planned in #50 (issues #51–#67).

- **No "add feed" form.** Users search; a website that isn't known yet is *looked up*,
  which finds its feeds in the background and creates a site page listing them. Every
  feed belongs to a site, and feeds are only created by discovery. (#61)
- **Search only reads the database** (site title and host, feed title and URL). Results
  are sites, each with its feeds.
- **A lookup is offered when the query looks like a URL or domain.** Turning a name into
  a website ("the verge" → theverge.com) is a todo item.
- **The `lookups` table is the queue and the cache:** one row per site key, re-runnable
  24 hours after it finished, or 1 hour after it failed.
- **`POST /lookups` waits about 3 seconds**, then redirects to the site page if the
  lookup found the site, else to a status page that refreshes itself with `<meta
  http-equiv="refresh">` (no JavaScript).
- **Discovery order:** fetch the page (or a feed URL directly), take its `<link
  rel="alternate">` feeds, else try common paths (`/feed`, `/rss.xml`, …), and keep only
  candidates that parse as feeds.
- **A site is keyed by its lowercase host without `www.`**, and lookups always land on the
  site page, even for one feed. A feed belongs to the first site it was discovered from.
  Sites under a path on a shared host (`medium.com/@user`) aren't handled yet.
- **Search, site pages and lookups are login-only** for now (logged-out access is a todo
  item), and lookups are rate-limited per user. (#67)
- **Feeds are parsed with `gofeed`**, which reads RSS 0.9x–2.0, RSS 1.0, Atom and JSON
  Feed. (#54)

## Feed ingestion

Planned in #126, with issues #128–#133.

- **Refresh workers run inside `www`**, not as a cron-hit endpoint or a separate service.
- **Only feeds someone subscribes to, or whose page view requested a refresh, are
  refreshed in the background.** Nobody waits on the rest. (#137)
- **The Refresh button stays synchronous** and goes through the same `ingest.Refresher`
  as the workers, so there is one fetch-and-save path.
- **The schedule is ours:** a fixed interval with jitter, backoff after failures, and the
  server's `Retry-After` / `Cache-Control` hints, which can only delay a fetch. RSS
  `<ttl>` and `sy:updatePeriod` are ignored. A posting-frequency interval is a todo item.
  (#126, #133)
- **A 410 Gone stops background fetches** of the feed; rediscovering it clears that.
  (#132)

## Subscriptions

Planned in #139 (issues #135–#138).

- **Subscribing and unsubscribing are one click with no confirmation**, and redirect back
  to the page they were posted from.
- **The global "latest feeds" list was removed, not moved:** `/feeds` is "My feeds".
- **Subscribed state comes from a separate `SubscribedFeedIDs` query** rather than a user
  parameter on every feed query.
- **Refresh requests from page views are a `feeds` column** (`refresh_requested_at`)
  worked by the existing refresh workers, never a goroutine or an in-memory queue, so
  they survive restarts and work across instances.
- **Subscriptions are private:** no subscriber counts.

## Reading

Planned in #163 (issues #147–#162).

- **A timeline, not an inbox.** Read state is recorded but only shown as a quiet marker on
  read rows (dimmed, like a visited link). No unread counts, badges, "N new" indicators,
  "mark all read", or manual mark read/unread. No unread filter for now.
- **What counts as read:** opening the article page, or clicking through to the original
  from a row. Scrolling past doesn't; "seen" isn't recorded. Reads are per user and
  article, independent of subscriptions; an article counts as read if any copy with the
  same `canonical_url` was read. (#156, #158, #161)
- **Timeline order is arrival order.** Each article gets `timeline_at` once, on insert:
  the save time for news, its published date for backlog (how the two are told apart is
  under Reading in features.md). New items can't land below ones already seen, and
  republished archives don't flood the top. No bump on subscribe. Displayed dates may
  look out of order; accepted. Feed pages keep publication order. (#147)
- **The timeline is `/`** for logged-in users (the nav label stays "Home"). Duplicates are
  collapsed at query time on `canonical_url`, keeping the earliest arrival, with "Also in"
  on the row. Feed pages show every copy. (#154, #162)
- **Rows are divided sections with a preview card**, one design for the timeline and feed
  pages: the feed's avatar and name with the time, then a card with the optional
  feed-declared image, title, two-line excerpt and a small link to the original. The
  whole card links to the in-app article page. Feed pages leave out the avatar and name.
  Untitled articles use the start of the excerpt. (#181)
- **Article page:** sanitized content, else summary, plus "Read on {site}". No
  previous/next. Any logged-in user can open any article. (#152)
- **Article HTML is sanitized at render time** with bluemonday: relative URLs resolved,
  iframes replaced by a link, links opened in a new tab with `noopener noreferrer`. Images
  load from the publisher with `referrerpolicy="no-referrer"` and lazy loading; the CSP
  allows `img-src https:`. No image proxy yet (a todo item, with why it's its own
  project). (#151)
- **Paging works without JavaScript** ("Older articles" with a `?before=` cursor);
  infinite scroll enhances it. Logged-in pages keep `Cache-Control: no-store`; the script
  keeps the URL on the visible row so Back and reload land nearby. (#155, #159)
- **Times are server-rendered in UTC** in a `<time>` the script localizes; date-only feed
  dates show just the date and aren't localized. No day headers or relative times.
  (#157, #160)
- **New articles don't appear live** while you scroll; a reload shows them on top.

## Design

A modern, minimal look modelled on social timelines, from the redesign in #181.

- **One centered column, nav on the left.** A rail with icons and labels on wide screens,
  icons only on medium ones, and a top bar (logo, logout) with a bottom tab bar on
  phones. CSS only; no JavaScript.
- **Lists are sections separated by dividers, not boxes.** A card is only for a preview of
  something elsewhere: an article now, Open Graph data later.
- **Avatars are letters on a tint chosen by site** (`avatarTone`), so a site's feeds share
  a color, until there are site icons.
- **Light theme only for now.** Colors are custom properties on `:root` in `main.css`, so
  a theme can redefine them in one place. System font stack; icons are inline SVG
  partials (`templates/partials/icons.html`), not an icon font or library.

## Security

Planned in #187 (issues #182–#186).

- **In-memory rate limits** (`internal/server/ratelimit.go`) are enough for one instance;
  they reset on restart. Whether to keep them long term is a todo item.
- **IPv6 clients are keyed by their /64**, since one client usually controls a whole /64.
- **A limited login or signup re-renders its form with 429** and `Retry-After`, keeping
  the email filled in. The generic 429 page is only for lookups.
- **Failed logins are counted per submitted email, whether or not it has an account**, so
  a 429 doesn't reveal which emails do. Successful logins don't count or reset it.
- **A duplicate signup looks exactly like a successful one**, and login verifies a dummy
  hash for unknown emails so it takes as long as a wrong password.
- **Hashing beyond the cap waits in line rather than failing** (see "Queued password
  hashing" in the Performance watchlist).
- **Signup is off unless `-signup` is set.** Accounts are made by starting the app with it
  briefly, until the shared invite code (a todo item) replaces the flag.
- **`clientIP` reads only `RemoteAddr`** until #212 reads `X-Forwarded-For` from trusted
  proxies (see Deployment).

## Deployment

Planned in #218; issues #208–#217.

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
- **No domain yet.** The app is served at its `*.onrender.com` URL with Render's HTTPS. A
  custom domain (possibly a subdomain of georgerodier.com) is a later, separate change:
  Cloudflare DNS plus a Render custom domain, both in Terraform.
- **Migrations are built into the binary** (`www migrate`) and run before each deploy, not
  at app startup, so they also work with several instances, and a failed migration
  aborts the deploy while the old version keeps serving. One artifact carries the code
  and the migrations it expects, and the same command runs everywhere.
- **Wiping the database is never automatic.** While pre-alpha, `migrate` refuses to run
  when `migrations/*.sql` have changed since they were applied (code-only changes never
  touch the database). It wipes and rebuilds only when given the new migrations' exact
  fingerprint: locally `make db/reset`, in production the "Reset production database"
  workflow (typed confirmation), which sets `RSS_ALLOW_RESET_TO` and redeploys. A
  one-off Render job can't do it, since it runs the old image.
  - The fingerprint is stored in a `schema_fingerprint` table that `migrate` creates
    itself, not in `00001_init.sql`: it belongs to the migration mechanism, not the app,
    and goes away with the pre-alpha rule. A database with no stored fingerprint (one
    migrated by the goose CLI) is refused like a changed one, with no special case (#211).
- **Images are public on GHCR**, like the source. A private image would need a long-lived
  GitHub token stored at Render. Deploys reference images by digest, and CI attests build
  provenance. No secrets at build time.
- **Deploys switch with no downtime.** Render starts the new version, waits for
  `/healthcheck`, switches traffic, then stops the old one (30-second shutdown delay,
  matching the app). A failed health check or pre-deploy leaves the old version serving.
  Old and new versions overlap for a few seconds: background workers are safe (`SKIP
  LOCKED`) and in-memory rate limits reset. Rollback redeploys an earlier image and
  doesn't undo migrations. No blue-green slots or canaries on Render; Fly or ECS offer
  them if we move.
- **Client IP:** `X-Forwarded-For`, taking the rightmost address that isn't in
  `-trusted-proxies`, which defaults to trusting nobody. Render's proxy keeps whatever
  `X-Forwarded-For` the client sent and appends to it, so the leftmost entry can be
  forged. Render's proxy address range is measured after the first deploy, not guessed.
- **Backups:** Render's point-in-time recovery (3 days on the Hobby workspace) plus a
  nightly `pg_dump` to R2 from a Render cron job, kept 35 days. The R2 copy is at a
  different provider from the database, and restoring it is also how the database moves
  to a new host.
- **Logs** stay in Render's log viewer for as long as Render keeps them (a few days on the
  Hobby workspace; the README records the current figure). No log shipping, so client
  IPs aren't kept anywhere else.
- **Deploys:** every push to `main` that passes CI deploys automatically; merging the PR
  is the approval. Infrastructure changes (`terraform apply`) wait for the owner's
  approval in the `infrastructure` GitHub environment, since some Terraform changes
  replace or destroy resources. The database also has `prevent_destroy`.
