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
  documented, idempotent upkeep: queuing a refresh of a stale feed, recording that the
  user opened an article, and updating a session's `last_seen_at` at most once a day.
  (#50, #137, #156, #258)
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
- **Failed logins are counted per submitted email** (#183), to slow password guessing
  against one account. Successful logins don't count or reset it. Unknown emails were
  counted too, so that a 429 didn't reveal which emails have accounts; "Accounts" dropped
  that goal, and from #262 only wrong passwords count.
- **Signup, login and reset say which emails have accounts** (see "Accounts"). #185 had
  made a duplicate signup look exactly like a successful one, and login verify a dummy
  hash for unknown emails; #262 removes both.
- **Hashing beyond the cap waits in line rather than failing** (see "Queued password
  hashing" in the Performance watchlist).
- **Signup is off unless an invite code is set.** (#186 started with a `-signup` flag,
  turned on briefly to make accounts; #226 replaced it with the code below.)
- **A shared invite code replaces `-signup`** (#226). Signup is open exactly when
  `-invite-code` is set; the bool flag goes rather than staying alongside it.
  - The code arrives pre-filled from `/signup?invite=…` into a visible field, so it can
    also be typed. While signup needs an invite code, no page links to `/signup`:
    without a code it's useless, and advertising it only invites guessing, which the
    per-IP signup limit (#182) bounds. Whatever opens signup more widely adds the links
    back.
  - At least 16 characters, compared in constant time.
  - In production it's set in Render's dashboard and ignored by Terraform, like
    `RSS_ALLOW_RESET_TO`: a Terraform variable would keep it in plain text in the state
    and need a PR and an approved apply to rotate.
  - It may appear in request logs (they log the URI); only the owner reads them, and the
    owner sets it.
- **`www create-user` makes accounts locally** (#225), and `make db/reset` uses it to put
  your account back. Not seeding in `www migrate`: that manages the schema and runs before
  every deploy. The password comes from stdin, never a flag (shell history, process
  listings), and there's no hidden prompt, which would need a new dependency. It can't run
  in production (distroless image, no external database access); production accounts
  come from the invite code.
- **Sessions: see "Accounts"** (#258). #248 kept them to 30 days from login, with no idle
  timeout, until they could be revoked. scs's own idle timeout writes the session row on
  every page view, which is why #258 tracks `last_seen_at` itself, at most once a day.
- **Malformed form bodies get 400 and bodies over 64 KiB get 413** (#249), as HTML error
  pages, not logged as server errors. The cap is applied in `decodePostForm`, so only
  routes that decode a form are capped; a future upload sets its own limit.

## Accounts

Planned in #270 (issues #254–#269). In these flows, trade-offs favor the user's
experience.

- **Helpful errors, not hiding which emails have accounts.** Signup says when an email
  already has an account, login says whether the email or the password is wrong, and
  password reset says when there's no account (#262, #265). What this reveals, that
  someone uses an RSS reader, isn't sensitive, and the commonest failures (forgetting
  you signed up, using another of your addresses, a typo) get a next step instead of a
  wait for an email that never comes. All three say the same, since hiding it in one
  place protects nothing if another reveals it. Per-IP limits keep bulk checking slow.
  This reverses #185.
- **Soft email confirmation** (#264). Signup logs you in at once (#262), and a quiet
  notice asks you to confirm until you do; a password reset also confirms. Confirming
  first was rejected: it puts a trip to the inbox before first use, a slow or filtered
  email strands you, and on phones the link opens in the mail app's own browser. A
  confirmed address is required only for:
  - changing your email: the new address takes effect once confirmed (#266);
  - later, email that isn't about your account (digests, notifications);
  - later, anything that reaches other people (invites by email, sharing, public
    profiles);
  - later, linking another sign-in method by matching email, which otherwise lets
    someone who signed up with your address first keep a way in. A reset already covers
    that case: it replaces the password and logs out every other session.
- **Email links open a page with a button, and the POST does the work** (confirmation,
  email change, and the reset form), so GET handlers still don't write and mail scanners
  that open links can't act for the recipient. Tokens are 32 random bytes, and only
  their SHA-256 is stored. They're single use; asking again doesn't cancel earlier links
  (emails arrive out of order), and using one deletes the rest of its kind. Confirmation
  links last 7 days, email change 24 hours, reset 1 hour. Confirmation and reset links
  only work while the account still has the address they were sent to. Pages with a
  token in the URL send `Referrer-Policy: no-referrer`.
- **The email is how you log in and how we reach you, and it's never shown to other
  people.** You're shown by your name and a letter avatar. No usernames until something
  public or social needs one: they bring "taken" errors at signup, squatting, and broken
  links on renames.
- **No credentials table yet.** The password hash stays on `users`. Passkeys (a todo
  item) add their own table, one user to many passkeys, and make `hashed_password`
  nullable. Revisit before production if Google or Apple sign-in is planned.
- **The name stays required at signup** (#261): it's what we call you in emails. One
  field, what you'd like to be called, with no first/last split (which fails for many
  names); at most 100 characters, trimmed.
- **Avatar: the first letter of your name on a tint chosen by user ID**, like site
  avatars. No uploads: only you see it, and storing and resizing untrusted images costs
  more than it gives. No Gravatar: it sends a hash of your email to a third party.
- **One Settings page** (#261): rows like a phone's settings, each opening its own small
  page with one form. The nav's Logout button becomes an account entry (the avatar,
  labelled Settings), and Log out moves to Settings: with sessions this long, a
  prominent Logout mostly invites accidental logouts. Preferences (themes) will be typed
  columns on `users`, not a key-value table.
- **A `user_sessions` row per logged-in device** (#258), its ID kept in the scs session.
  You stay logged in while you visit at least every 90 days, for at most a year (scs's
  lifetime; browsers cap cookies at 400 days). `last_seen_at` is written at most once a
  day, not on every page view. No "Remember me" checkbox: it asks a question most people
  ignore, and logs out everyone who didn't tick it. Logout deletes this device's row and
  only that one; changing or resetting your password deletes the others, and so does
  "Log out of all other devices" in Settings (#263). A list of devices comes later.
- **No password confirmation field** (#257). A show/hide toggle (JavaScript) catches
  typos instead, and reset covers the rest.
- **Passwords: 8 to 256 characters, not a common password, not your email** (#256),
  normalized to NFKC. No composition rules or expiry. NIST's 15-character minimum for
  password-only login was considered; the common-password list gets most of its benefit
  with less friction. Checking Have I Been Pwned is a todo item.
- **Re-enter your password to change your email or password, or to delete your
  account** (#263, #266, #268). No "sudo mode" window that skips it for a while.
- **Account deletion is immediate** (#268), after your password, with no grace period:
  it's rare and deliberate, and a pending state adds a scheduled job and doubt about
  whether the data is gone. Feeds and articles stay, since they're shared. Backups keep
  the data up to 35 days, which the privacy policy says.
- **Security emails:** password changed, email changed (to the old address), and account
  deleted. No "new login" alerts: they're noisy, and people learn to ignore them.
- **Email goes over SMTP behind `mail.Sender`** (#259), so the provider changes by
  configuration: Resend's free tier while testing, Amazon SES later (a todo item).
  Without an SMTP host, emails are logged instead, in development only; production
  refuses to start without one, since emails carry one-time links. Links use
  `-base-url`, never the request's `Host` header.
- **An outbox table, sent by a background worker** (#260), so pages don't wait on the
  provider and failures retry. The handler that queues an email wakes its own worker, so
  it goes out in about a second. Bodies are cleared once sent. At most 20 emails to one
  address an hour: a backstop under each flow's own limit, so nobody can use the app to
  flood an inbox.
- **`rss.georgerodier.com` serves the app and sends its email** (#254) until there's a
  product name. A subdomain keeps the email provider's records away from the personal
  mail on `georgerodier.com`. Its DNS record is DNS-only, since a proxied one would add a
  third `X-Forwarded-For` hop. DMARC starts at `p=none`, with reports. The SMTP password
  is set in Render's dashboard and ignored by Terraform, like the invite code.
- **Support mail goes to `rss-support@georgerodier.com`, a Google alias** (changed in
  #254 from `support@rss.georgerodier.com`). The app's CNAME on `rss.georgerodier.com`
  can't share its name with Email Routing's MX records, and Cloudflare's Email Routing
  would have replaced the Google MX records on `georgerodier.com`. The domain is
  temporary, so the address only has to work until the product has its own.
- **No `_dmarc.rss` record** (#254): mail from `rss.georgerodier.com` falls back to the
  `p=none` record on `georgerodier.com`, whose reports Cloudflare's DMARC Management
  reads; it doesn't support subdomains. Tightening DMARC for the app means adding
  `_dmarc.rss` with its own policy, or `sp=` on the parent's record.
- **A support address** (`-support-email`) is the Reply-To of every email, and is shown
  on the reset pages and in Settings (#259, #265, #269).
- **A privacy policy and terms before people we don't know use the app** (#269). The app
  sets only the essential session cookie, so there's no cookie banner; anything else
  (analytics, third-party scripts) means revisiting that.
- **All the account tables land in one schema change** (#255), so production is reset
  once rather than four times.

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
- **No account is seeded after a production reset.** It would need a credential (or its
  hash) stored in Render, Terraform state or GitHub, and a one-off job to run
  `www create-user` in. Signing up again with the invite link (#226) is the way back in,
  so production isn't reset before that's live. (#214, #215)
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
- **Client IP:** `X-Forwarded-For`, taking the entry `-proxy-hops` places from the right;
  the default 0 ignores the header. Render's proxy keeps whatever `X-Forwarded-For` the
  client sent and appends to it, so the leftmost entry can be forged. Production sets 2:
  `onrender.com` traffic passes through Cloudflare and then Render's proxy, and each
  appends one entry, so the app doesn't need to know either's address. This relies on
  every request going through Cloudflare; one that reached Render's proxy directly could
  forge its address. The first production check (#214) found the second hop: with 1 the
  app saw Cloudflare's address. This replaces trusting a measured proxy range (#212):
  Render doesn't publish that range and can change it without notice, and a stale range
  silently made every visitor share one login limit. If the host adds a hop inside its
  network, the picked address is private and the app logs a warning; an added public hop
  (like Cloudflare) doesn't warn, so the runbook's check in `infra/README.md` is what
  catches it. Host-specific headers (`CF-Connecting-IP`) aren't used: they'd be a
  host-specific code path. (#228)
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
- **Workflow secrets.** Every value the workflows read is a repository secret, the IDs
  (`RENDER_SERVICE_ID`, `CLOUDFLARE_ACCOUNT_ID`) included, rather than the repository
  variables #208 planned for them, so they are masked in logs too. Jobs with secrets run
  only on `main` or on pull requests from branches in this repository, never with
  `pull_request_target`. Dependabot's pull requests get no Terraform plan, since they
  can't read Actions secrets; copying the secrets into Dependabot's store isn't worth a
  second place to keep them. (#215)
- **The deploy workflow follows Render's deploy status**, then checks `/healthcheck`
  once it's live. Polling only `/healthcheck` for the new SHA would be shorter, but a
  failed pre-deploy (`migrate` refusing a schema change, which happens with every
  schema change while pre-alpha) would only show at a timeout, with no reason given;
  following the status fails within seconds and names the cause. A deploy Render
  queues behind a running one (202, no ID) fails the job rather than being tracked
  down: our deploys never overlap (one concurrency group), and changing a variable
  through the API doesn't start one, so only a dashboard deploy or a Terraform apply
  can be running. The Render CLI's `deploys create --wait` would do the polling, but
  it's a binary to download and pin in a job holding an account-wide API key. (#215)
