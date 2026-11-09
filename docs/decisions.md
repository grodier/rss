# Decisions

Decisions made while planning, with the reason for each and the issue or PR where it was made.
Don't reopen one without a new reason.
A plan or PR that changes one edits it here and says so in its description.
Planned work is the [Roadmap issue](https://github.com/grodier/rss/issues/385), and work without an issue is in [backlog.md](backlog.md), under the same section names; how the code works is in [architecture.md](architecture.md) and [features.md](features.md).

## Overall

- **One binary, one database.** `www` runs the web server and the background workers (lookups, feed refresh) as goroutines; Postgres is the only other infrastructure.
  It holds the job queues and the sessions too.
  One thing to deploy and operate, which is enough at this scale.
  Splitting out a worker process is a backlog item with its trigger.
  (#50, #126)
- **Postgres tables are the job queues**, claimed with `FOR UPDATE SKIP LOCKED`, so several instances never take the same job and no queue service is needed.
  `lookups` has a status column; `feeds` is claimed with a lease on `next_fetch_at` instead of a status column.
  (#50, #126)
- **Server-rendered HTML; JavaScript is progressive enhancement.** Every page works without it.
  The rules that follow are in CLAUDE.md.
  (#50, #163; #543 reverses their "one plain file, no build step": see "Frontend assets")
- **Outbound requests to user-influenced URLs only go through `internal/fetch`**, which blocks private addresses (SSRF) and limits time, size and redirects.
  (#50)
- **Cross-site requests are blocked with Go's `http.CrossOriginProtection`** (`Sec-Fetch-Site` / `Origin` headers) rather than CSRF tokens, so forms and sessions carry no tokens.
- **GET handlers never fetch user-supplied URLs or make changes on the user's behalf**, because `CrossOriginProtection` only guards non-GET requests.
  The only writes from GET are documented, idempotent upkeep: queuing a refresh of a stale feed, recording that the user opened an article, and updating a session's `last_seen_at` at most once a day.
  (#50, #137, #156, #258)
- **Errors stored for debugging are never shown to users** (`lookups.error`, `feeds.last_error`).
  Pages say what happened in plain words.
  Debug mode's panels show them, to allowed accounts only (see "Debug mode").
  (#50, #129, #327)

## Pre-alpha testing

- **Testers' data isn't kept**: schema changes keep editing the initial migration and wipe the database, and testers are told so.
  The rule and its scope are under "Prototype stage" in CLAUDE.md.
  (#180; ended by #561, below)
- **A planned batch lands its schema in one change**, so production is reset once per batch rather than once per feature: the account tables (#255), then the reading features' tables (#293), then subscription names and the filtering batch's (#348).
  A prototype that may be dropped adds its own table instead, so dropping it leaves nothing behind.
  Before a reset, export your OPML, and import it afterwards (#303).
  (Until #561; afterwards each change adds its own migration.)
- **Production data is kept from #561 on**, before the waitlist (#566) collects its first address: a reset after that would delete everyone waiting, and the people invited from it are the next testers.
  The app is still pre-alpha in every other way.
  Rejected: keep resetting and carry the waitlist across each reset with an export and import in the admin, a compatibility path that loses everyone the one time it's forgotten.
  (#583)
- **Data is kept, old code isn't.** A schema change migrates the rows that exist in its own migration, so code still never handles rows or flows the old code produced, and the "no compatibility shims" rule in CLAUDE.md stands.
  (#583)
- **A migration must work with the version still running during a deploy**, which overlaps the new one for a few seconds ("Deploys switch with no downtime" under "Deployment"): add before the code uses it, drop only after the code has stopped using it.
  (#583, from "Leaving pre-alpha" in the backlog)

## Debug mode

Planned with issues #327–#330.
Its purpose is exploration more than bug fixing: showing how pages are put together from stored data, and what feeds and pages publish that the app doesn't use yet.

- **Allowed accounts are listed in a flag** (`-debug-emails`).
  It's empty by default, and then nothing about debug mode shows and its routes are 404s.
  This was chosen over:
  - a request header, which needs a browser extension and doesn't work on a phone;
  - a user role, which needs a schema change.
    (#327)
- **It's turned on per device, from Settings.** It's a flag in the scs session, so logging out turns it off.
  The allowlist is checked on every request, so removing an account turns debug mode off at once.
  (#327)
- **Nothing new is stored.** There's no schema change, no fetch history and no copies of raw responses.
  Panels read existing rows, and inspectors fetch live.
  A fetch history is a backlog item.
  (#327, #328)
- **Inspectors are read-only POSTs:**
  - they fetch through `internal/fetch` (GET handlers never fetch URLs);
  - they render their result directly;
  - they save nothing;
  - they get a 30 s budget, like a lookup job, by extending their own write deadline instead of raising the server's `WriteTimeout`;
  - they only take IDs, so every URL they fetch is one already in the database.
    (#328, #330)
- **Panels show internal details**, such as `last_error` and the `lookups` error, to allowed accounts.
  Untrusted text (raw XML, JSON, article HTML, page tags) is only ever shown as escaped text, never rendered.
  (#327, #328, #329)
- **Panels cost nothing when debug mode is off:**
  - their queries only run when it's on;
  - the line on list rows uses columns the lists already load.
    (#327, #329)
- **"Used" and "ignored" come from lists kept by hand next to the parsers:**
  - `usedChannelFields` and `usedItemFields` beside `feedparse.Parse`;
  - `usedPageKeys` beside `discovery.ParsePage`.

  A PR that changes what a parser reads updates its list.
  (#328, #330)
- **Discovery records every candidate it checks** (`Result.Checked`), and returns what it has so far when its deadline passes.
  The lookup and import runners ignore both.
  (#330)
- **No subscriber counts or other cross-user data.** Those belong to an owner admin page, which would build on the same allowlist (backlog).
  (#327; the admin, planned in #583, has its own accounts instead of this allowlist and shows cross-user data only as aggregates: see "Admin")

## Admin

Planned in #583, from the owner's list and must-haves; the issues are on the [Admin Roadmap](https://github.com/grodier/rss/issues/560).

- **A separate admin site and command, `cmd/admin`**, built into the same image as `www` and run as its own Render web service (about $7 a month).
  Its web pages and its CLI subcommands share one package, `internal/admin`, so each action is written once.
  Rejected: admin routes inside `www`, which would put owner-only code and credentials behind the reader's host and sessions.
  The production database is only reachable from Render's private network, so `admin` commands against production run in the admin service's shell; the web pages are the everyday tool.
- **It runs none of `www`'s background workers**, only its own small jobs (the health pinger, #576).
- **On a subdomain, `admin.paperround.app`, set up as its own origin**: `__Host-` cookies with `SameSite=Strict`, its own CSP and its own HSTS (the reader's has no `includeSubDomains`).
  The reader and the admin are same-site but not same-origin, and `http.CrossOriginProtection` only lets same-origin form posts through, so no reader page can post to the admin.
  Rejected: a separate domain, which hides nothing (certificate transparency logs list every hostname) and is one more domain to keep.
- **Three layers in front of it**: Cloudflare Access on the hostname, whose DNS record is the only proxied one; the service's `onrender.com` subdomain turned off, so Access can't be bypassed; and the admin's own login, which is the real gate.
  Access is defense in depth and needs no code.
- **Admins are their own accounts, not readers** (an `admins` table), so a leaked reader password never opens the admin.
  A second factor is required: TOTP, with hashed one-time recovery codes, and the TOTP secret encrypted with a key held only in the admin service's environment.
  TOTP is a few lines of the standard library (RFC 6238); passkeys are a later upgrade (backlog).
  The first admin is created with `admin enroll` in the service's shell; nothing seeds one.
- **Short sessions**: 30 minutes idle and 12 hours at most, and a destructive action asks for a TOTP code again when the last one is over 10 minutes old.
- **The `__Host-` cookie is production's only** (#567): locally, over `http://localhost`, the cookie is `admin_session` without `Secure`, like the reader's, since `__Host-` requires `Secure` and some browsers (Safari) refuse `Secure` cookies over plain `http`.
- **`admin enroll` always needs `-secret-key`** (#567): it runs as its own process, so a key made for one run would seal a secret the site can't open; locally the key lives in `.env`, and `admin serve` without one makes a key per run, with a warning, rather than refusing to start.
- **`admin enroll` draws the QR code itself** (#567, `cmd/admin/qr.go`: byte mode, level L, versions 1 to 10), rather than adding a QR library for one terminal picture; it also prints the URI, for an app that takes one pasted.
- **Both login steps share generic errors, and a wrong code counts against the admin, not just the IP** (#567): "That email and password don't match an admin." whether or not the email is an admin's (with a dummy hash checked for timing), and 5 wrong codes per admin per 15 minutes on top of 10 attempts per IP.
- **Every write is recorded in an audit log** (who, what, which record, before and after, when), and so are logins and failed logins; destructive actions also need a typed confirmation.
- **No impersonation.** Support uses read-only views: logging in as a reader would show their private reading.
- **Aggregates only: the admin never shows a reader's reads, saves, stars, tags, timelines or feed names.**
  A user's page shows account facts (dates, sessions, emails sent and their status, import outcomes) and counts.
  Seeing what someone reads has no clear use, and isn't what readers or the privacy policy expect; a later need names its use case and updates the policy first.
- **The admin's skeleton (#562) set its layout, and later admin pages reuse it; a large feature that changes how the admin looks or works still gets its own design checkpoint** (the owner, at #562's checkpoint; planning had the skeleton as the admin's only one).
  A page built from the approved parts (tables, forms, stat rows) needs none.
  What the admin changes for readers (the waitlist form) keeps its checkpoint.
- **The admin's layout** (#562, approved at its design checkpoint): a sidebar of the sections on wide screens, and on phones a top bar whose Menu opens the same list, a `<details>`, so it needs no script.
  Rejected: top tabs on every width, which ten sections would make wrap or scroll, and the reader's `.shell`, a reading column too narrow for tables.
- **It looks like the reader, marked as the admin** (#562): the reader's tokens and fonts, a tinted sidebar, and an "Admin" mark beside the logo, so it's never mistaken for the reader; it follows the system's theme, as it has no settings.
- **Dense tables** (#562): smaller type and tighter rows than the reader's lists, since it's a working tool; on a phone a wide table scrolls in its own frame, not the page.
  Figures are stat rows separated by dividers, not cards, as in the reader ("Design").
- **Charts are SVG drawn on the server** (#571), with no chart library: they fit the CSP and work without JavaScript.
- **`create-user` and `send-test-email` move from `www` to `admin`** (#568); `seed` stays in `www`, since it's a deploy step (`www migrate -seed`).

## Observability

Planned in #583 with the admin, which is where it's read.

- **One place to look: the admin.** The app records its own request, background-job and real-user metrics in Postgres, and the admin fetches container CPU and memory and deploys from Render's API, and uptime from UptimeRobot's (#576).
  Render's latency by path and its metrics streaming need a Professional workspace (production is on Hobby), and a hosted tool (Grafana, Datadog) would be another data processor in the privacy policy, for volumes Postgres handles easily.
- **Request metrics are grouped by chi route pattern** (`/feeds/{id}`), never by URL, so no IDs or tokens are stored (#563).
  Each instance counts in memory and writes one row per route per minute, with latency in fixed histogram buckets, from which the admin reads percentiles.
  Requests that never reach a route (404s, 405s, and middleware's responses before routing, such as cross-origin 403s) share one route, `(unmatched)`, and methods outside the standard ones share `OTHER`, so clients can't add rows (#563).
- **A `Server-Timing` header gives the browser the server's time** (#563), so real-user TTFB splits into server and network; it carries durations only.
- **Real-user metrics: one beacon per page view** (#565), sent when the page is hidden, with Core Web Vitals (LCP, INP, CLS), loading timings and coarse context (device class, installed or not, browser family).
  Measured with Google's `web-vitals` package (planning had it at about 2 KB; version 6, which #565 pins, is 3.3 KB with Brotli, and 4 KB gzipped with `rum.ts`, on every page and cached), the frontend's first runtime library: its INP and CLS rules (session windows, back/forward cache, background tabs, prerendering) are easy to get subtly wrong, and it matches what Chrome reports.
  Rejected: Cloudflare Web Analytics, a third-party script and processor that also needs a proxied DNS record.
- **Beacons carry no identifiers**: no cookie, user ID, IP address or URL.
  The server resolves the page's path to its route pattern and drops the path; another site's referrer is kept as a host only.
  So there are page views, not visitors or sessions.
  The beacon stores nothing in the browser either, so "no cookie banner" (under "Accounts") stands; the privacy policy says what's measured.
- **Every browser on iOS and iPadOS counts as Safari** (#565): they all use Safari's engine, so Chrome or Firefox there measures what Safari does (no LCP or INP before 26.2), and counting them under their own names would mix WebKit's gaps into Chrome's and Firefox's numbers.
  Only Safari's own User-Agent gives the engine's version, so the others' major version is 0 (unknown).
  Other browsers built on Chromium count as Chrome, by its version, except Edge and Samsung Internet, which have families of their own.
- **Real-user histograms bound the Core Web Vitals' thresholds** (#565): times are roughly log-spaced from 50 ms to 20 s with each metric's "good" and "poor" limits among the bounds, and CLS from 0.01 to 1 with 0.1 and 0.25, so the share of good and poor views is exact rather than interpolated.
  The server's time has the request metrics' bounds (5 ms to 10 s), since it's mostly under 50 ms.
  A day is rolled up an hour after it ends (UTC), so the day's last beacons are in.
- **The beacon is capped three ways** (#565), as it's the one route that writes a row per request without a login: 120 beacons per client IP per 10 minutes, 600 a minute per instance from every client together, and the newest 500,000 raw views kept, whatever the rollup does.
  The per-IP limit alone can be got round by rotating addresses (an IPv6 /48 holds 65,536 /64s), and a full disk would take the whole app down, not just its metrics; beacons past a cap are dropped, real ones too, which only loses measurements.
  The total also bounds a day's views, at 864,000 per instance, whose rollup took about 6 s on a local database, inside the housekeeping's minute.
  Not prevented, as nothing identifies a beacon's sender: plausible made-up values, which can outweigh real ones on a quiet route.
- **Retention**: request metrics at one minute for 7 days and hourly for 90; raw beacons for 30 days (at most 500,000) and daily rollups for 13 months (#563, #565).
- **Recent server errors are kept in a table capped by count** (#564), by route pattern, so the admin can show them next to each route without log access.
- **A Render API key on the admin service** (#576) is accepted: Render keys have full account access, but a compromised admin can already write to the database.
- **Availability is checked from both sides** (#576): UptimeRobot's API for the outside view, and the admin pinging `www`'s `/healthcheck` over the private network every minute, to tell the app being down from the edge or DNS being down.

## Feed discovery

Planned in #50 (issues #51–#67).

- **No "add feed" form.** Users search; a website that isn't known yet is *looked up*, which finds its feeds in the background and creates a site page listing them.
  Every feed belongs to a site, and feeds are only created by discovery.
  (#61)
- **Search only reads the database** (site title, host and description, feed title, URL and description).
  Results are sites, each with its feeds.
  Descriptions were added with search as you type (#449), so a site can be found by what it's about, not only by its name or address.
- **The search box is for names and addresses, not prose** (#441): it turns off autocorrect, auto-capitalization and spellcheck, which on phones broke addresses ("npr. org"), and an address typed with stray spaces is still offered as a lookup.
  The input has `autocapitalize="none" autocorrect="off" spellcheck="false"` and `enterkeyhint="search"`, and keeps `type="search"`; the Saved page's search box keeps spellcheck, since it searches article text.
  Only whitespace next to `.`, `/` or `:` is removed (`npr. org`, `https: //npr.org`); any other whitespace still means the query isn't an address, so free text such as `news on npr.org` isn't glued into one.
  The query shown back stays as typed; only the lookup offer and the site-key match use the cleaned-up address.
- **Search updates as you type by fetching the Search page itself** (#449): no new route; `app.js` fetches `/search?q=…` and swaps in the page's results, as infinite scroll does with the next page, so the results markup lives in one template.
  The cost is a heavier response than a list of names (3–14 KB with about 1,300 sites and feeds locally; timings are in the watchlist's "Search scans with `ILIKE`").
  A request goes out about 250 ms after the last keystroke, from 2 characters, aborting any still in flight; an empty box shows the empty Search page without a request.
  The URL follows the query (`history.replaceState`), so reload and Back show the same results.
  There's no rate limit: debouncing bounds each reader's requests, and the `ILIKE` scan's cost is on the watchlist.
  GET handlers still never fetch user-supplied URLs, so looking up an address still takes a click on Look up, which appears as you type.
  The number of sites is announced in a polite live region rather than making the results a live region, which would read every result out.
- **Results update in place under the search box, not in a dropdown** (#449, approved at the design checkpoint in #476): the Search page already is the results, so updating it adds no new widget, keeps each result's feeds, "Subscribed" marker and the Look up button in view, and leaves keyboard and screen-reader behavior as it was.
  A dropdown of up to 5 site names was the alternative; it's a new ARIA combobox (arrow keys, Enter, Escape) to get right, and hides the results it duplicates.
  Results can shift under the box as you type, but only after a pause.
- **A query that is an address also matches its site key exactly** (#441), and that site comes first, so pasting `https://www.npr.org/news` finds npr.org: a substring match on the whole string never matches `sites.host`.
- **The database is seeded with a list of sites** (#447), so search finds well-known sites before anyone has looked them up.
  #447 first planned a catalog with topics and a Browse list on the Search page, so new readers could discover sites without knowing their addresses; both were built and dropped in review, since Browse worked as recommendations but poorly otherwise, and shared topics sat awkwardly beside readers' own tags, which are private, with no clear use yet.
  How new readers discover sites, and whether topics or keywords come back, is for first run (#450) and keyword search (#451) to plan.
- **The seed list is a plain text file kept in the repository** (`internal/seed/sites.txt`, #447): one website address per line, with comments, so the owner edits it in PRs and it can start empty and grow to thousands of sites.
  A test parses the embedded file, so a bad edit can't ship.
  Each site is listed once, since a site has one lookup; a home page's lookup finds the feeds the page advertises, and a feed's address adds only that feed.
  Plain text was picked over OPML, which lists feeds, often several per site, so all but one of a site's feeds would be dropped.
- **Seeding queues lookups**, so seeded feeds are created by discovery like any other, and needs no table of its own: the seed list is in the binary, and a seeded site is an ordinary site once found.
- **Seeding is its own command, `www seed`, run in production on every deploy and locally only by hand** (#447): production's pre-deploy command is `www migrate -seed`, so every deploy and production reset seeds without anyone remembering, while `make db/reset`, which runs often, never looks up the whole list.
  `-seed` exists because Render's pre-deploy takes one command and the image has no shell to chain two.
  CI doesn't seed: it can't reach the production database (Render's private network only), and its test database has no lookup workers.
  A Render job started by the deploy workflow after each deploy was the alternative, but it needed the jobs API, polling and error reporting in two workflows for a list that rarely changes.
  Like a migration, a failed seed fails the deploy and the old version keeps serving.
- **A seeded site's lookup is retried on the next seed if it failed or found no feeds over an hour ago** (#447), the same wait as a reader's lookup, so a transient failure (a timeout, a rate limit) doesn't keep a site out of search for good.
  A lookup that found a site, or is in progress, is never re-queued, so sites already found aren't fetched again.
  The seed logs the sites it retried and those that failed too recently, so a site that keeps failing (one blocking datacenter addresses, say) shows up in every deploy's log, as a reason to fix or drop it; a periodic retry worker was more machinery than the seed needs, since each deploy already retries.
- **Readers' lookups go before the seed's** (#447): lookups have a priority, and the seed's are claimed only when no reader's is pending, so seeding thousands of sites after a reset never makes a reader's lookup wait.
  A reader who looks up a site the seed has queued moves that lookup ahead, since someone is now waiting for it.
- **A lookup is offered when the query looks like a URL or domain.** Turning a name into a website ("the verge" → theverge.com) is a backlog item.
- **The `lookups` table is the queue and the cache:** one row per site key, re-runnable 24 hours after it found a site, or 1 hour after it failed or found no feeds (#439), since "No feeds found" is also worth retrying soon.
  A finished lookup re-runs at once for a different address on the same site (#439), so one result doesn't hide the site's other addresses, such as its feed URLs; pasting a feed URL on a site already found adds that feed to the site.
  Only the home page (path `/`, no query) of a site already found keeps the cached result, so `npr.org` and `www.npr.org/` still go straight to the site page.
  A lookup in progress is never re-queued, so two people typing different addresses don't keep restarting each other's lookup, and re-runs need no limit of their own: each is a lookup the per-user limit already counts.
- **`POST /lookups` waits about 3 seconds**, then redirects to the site page if the lookup found the site, else to a status page that updates in place while the lookup runs (#515).
  With JavaScript it fetches its own URL every 2 s, as live search fetches its own, so it needs no new route, and moves on when the answer changes; the 2 s interval was kept, and polling stops while the page is hidden, so the server sees no more requests than the meta refresh made.
  It doesn't reload itself, which restarted the spinner, sent focus back to the top and had screen readers read the page again every 2 s (WCAG failure F41); without JavaScript it falls back to `<meta http-equiv="refresh">` inside `<noscript>`.
  A redirect to the site page replaces the lookup page in the history, so Back skips the finished lookup.
- **A link to the app itself is refused, not hidden** (#471; why it never becomes a feed is under "Sharing" in "Filtering and timelines"): discovery refuses an address on the app's own site key before fetching it, and a result on it (a redirect, such as a URL shortener's), so lookups, imports and "Check for new feeds" all refuse it in one place.
  Search says so in place of Look up, the lookup form with a field error, and an import records it as its own failure, each pointing to another feed reader; the redirect case is rare and fails like any failed lookup, with no copy of its own.
  Saving such feeds but leaving the app's site out of search and site pages was rejected: a special case in two read paths, the feed still fetched from ourselves over the internet, and its link, token included, still in `feeds`.
  A feed on another site that names a shared link as its site is refused too; one that only advertises a shared link with `<link rel="alternate">` is saved under its own site, since its publisher made the link public.
- **Discovery order:** fetch the page (or a feed URL directly), take its `<link rel="alternate">` feeds, else try common paths (`/feed`, `/rss.xml`, …), and keep only candidates that parse as feeds.
- **A site is keyed by its lowercase host without `www.`**, and lookups always land on the site page, even for one feed.
  A feed belongs to the first site it was discovered from.
  Sites under a path on a shared host (`medium.com/@user`) aren't handled yet.
- **Search, site pages and lookups are login-only** for now (logged-out access is a backlog item), and lookups are rate-limited per user.
  (#67)
- **Feeds are parsed with `gofeed`**, which reads RSS 0.9x–2.0, RSS 1.0, Atom and JSON Feed.
  (#54)

## Feed ingestion

Planned in #126, with issues #128–#133.

- **Refresh workers run inside `www`**, not as a cron-hit endpoint or a separate service.
- **Only feeds someone subscribes to, or whose page view requested a refresh, are refreshed in the background.** Nobody waits on the rest.
  (#137)
- **The Refresh button stays synchronous** and goes through the same `ingest.Refresher` as the workers, so there is one fetch-and-save path.
- **The schedule is ours:** an interval with jitter, backoff after failures, and the server's `Retry-After` / `Cache-Control` hints, which can only delay a fetch.
  RSS `<ttl>` and `sy:updatePeriod` are ignored.
  (#126, #133)
- **Busy feeds are fetched more often; quiet ones stay hourly** (#456, planned on #438).
  The interval is about four fetches per expected article, from `posts_per_week`, between 15 minutes and an hour, so feeds posting more than 6 a day are fetched sooner and 24 a day or more every 15 minutes.
  At an hour, an article waited 30 minutes on average to be fetched, and a feed whose file holds fewer items than an hour of posts loses articles outright; in #438's simulation, faster busy feeds cost 22% more fetches and cut the wait to 11 minutes.
  A later save where every item of two or more is new means the feed may have outrun its file, so the next fetch comes in half the time since the previous one, at least 15 minutes, but never later than the rate's own interval (#456), so a busy feed's first success after failures doesn't wait half the outage.
  Fetching quiet feeds less often (up to a day) would save requests but delay the rare posts readers value most; it's a backlog item for when fetch volume matters.
- **A 410 Gone stops background fetches** of the feed; rediscovering it clears that.
  (#132)
- **A save after two days without a fetch attempt is placed as a feed's first save** (#497, planned in #491): everything is backlog at its date, undated items are spaced back, and only the first save's enrichment allowance applies.
  Feeds nobody follows aren't refreshed, so a seeded or looked-up site followed weeks later would otherwise bring every post since a day before its last fetch in as news, spread into the top 6 hours as one block; nobody was fetching it, so we don't know when those posts appeared, and dates are the best order we have.
  The previous attempt decides, not the previous success: a followed feed is attempted at least once a day even while it fails, so a feed that was down keeps its news.
- **Articles keep their authors' names, never their email addresses** (`articles.authors`).
  RSS's `<author>` is an email address by spec, often a placeholder (`noreply@blogger.com (Unknown)`), so only the name in it is kept, and "Unknown" is dropped.
  For RSS every `dc:creator` is read, since that's how multi-author posts list their authors; otherwise the first source with an author wins, as in gofeed.
  An item without authors doesn't inherit the feed's: on a single-author blog that only repeats the feed's name on every row.
  Rows name them in the card, before the link to the original ("Jane Doe · example.com ↗"), shortened past two to "Jane Doe and 4 others", so no row grows taller or pushes its date to a second line on phones.
  The article page names them all in a dateline under the title ("By … · date"), and its line above the title keeps only the feed, like a newspaper.
  Picked from layouts compared at 0, 1, 2 and 5 authors over authors in the meta line, which wrapped on phones, and a byline under a row's title, which made cards with authors taller.
  At most 10 per article.

## Subscriptions

Planned in #139 (issues #135–#138).

- **Subscribing and unsubscribing are one click with no confirmation**, and redirect back to the page they were posted from.
- **My feeds' rows are links with one control** (#424, the owner's pick of three options at the design checkpoint): the whole row opens the feed page, Edit is a pencil, and Unsubscribe is on Edit subscription and the feed page rather than on the row.
  A list of 50 subscriptions had 100 pills, and on phones they squeezed each row's text to a third of the width; unsubscribing from the list now takes two taps, but it's one click on the page where its consequence (losing the name and tags) is set.
  An overflow menu per row (hides both actions, and `<details>` menus don't close without JavaScript) and the pills under each row's text (still two per row) were the other options.
  Unsubscribe on Edit subscription always returns to My feeds, since the feed page would only offer to subscribe again.
- **My feeds has a site header only where it groups something** (#424): sites with two or more subscriptions; a single subscription says "From <site>" under its name when the names differ, rather than in its meta line, which would wrap to three lines on phones.
  Most headers had repeated the row under them; no headers at all would leave a site's feeds unmarked as a group.
  A group ends in a double rule, so the rows after it don't read as part of it.
- **The global "latest feeds" list was removed, not moved:** `/feeds` is "My feeds".
- **Subscribed state comes from a separate `SubscribedFeedIDs` query** rather than a user parameter on every feed query.
- **Refresh requests from page views are a `feeds` column** (`refresh_requested_at`) worked by the existing refresh workers, never a goroutine or an in-memory queue, so they survive restarts and work across instances.
- **Subscriptions are private:** no subscriber counts.
  Anonymous totals across users may come later, under the conditions in "Cross-user use" under "Filtering and timelines".
- **Readers can rename their subscriptions** (planned in #344, issues #342 and #343).
  The name is per subscription (user and feed), not per site, since one site can have many feeds and future tags will be per subscription too.
  It's edited on "Edit subscription" (`/feeds/{id}/edit`), where tags would also go.
  `""` means the feed's title, so a feed's new title still reaches readers who didn't rename it; a blank name or the feed's own title stores `""`.
  At most 100 characters.
  Unsubscribing drops the name.
- **The name shows wherever the reader's own reading shows the feed** (timeline, "Also in", saved list, article and feed pages, "Read on", OPML export); search results, site pages and debug panels keep the feed's own title, since they describe the feed.
  Per-user list queries put the name into `Feed.Title` in SQL, so templates don't change.
  (#343)

### OPML import

Planned in #309 (issues #302, #303).

- **Everything in the file is imported**, with no preview or checklist; unsubscribing is one click if something unwanted came in.
  Folders become tags unless the user chooses to ignore them (#354; until then they were flattened, since the app had no folders): see "Filtering and timelines".
- **Import is a background queue**, one `import_feeds` row per feed in the file, worked by its own runner: an unknown feed has to be fetched and checked first, since feeds are only created by discovery.
  The `lookups` table can't be reused: it's keyed by site key, and feeds on a shared host would collide.
  A known feed is subscribed to directly; an unknown or gone one goes through discovery and is saved like a lookup's.
  A URL that turns out to be a web page subscribes to the first feed it advertises.
- **Six import workers by default, and known feeds stay in the queue** (#448).
  Measured locally, a known feed's row takes about 7 ms, and an unknown feed's about 100–200 ms of our own work (mostly saving its articles) plus its fetch, so an import's time goes to unknown feeds waiting on other sites, which more workers overlap.
  Subscribing known feeds during the upload was rejected for now: it would save milliseconds a feed and move work into the request.
  Waking the runner on upload (saves up to a second) and round-robin between imports (the watchlist's "One import at a time") wait for production numbers that call for them.
  Six fits production's 10 database connections: a worker holds one only while it queries or saves, and a 100-feed import with six workers and 10 connections never waited for one.
- **The results page reports every feed**: subscribed, already subscribed, or couldn't add, with a plain reason ("Not a valid web address.", "Couldn't reach the site.", "There's no feed at this address.") and a link to search for the site.
  Failures aren't retried automatically and there's no "Try again"; uploading the file again retries them.
  While it runs, the page updates in place (#536), polling its own URL like the lookup page (see "Feed discovery"), with `<meta http-equiv="refresh">` inside `<noscript>` as the fallback; a reload restarted the spinner, sent focus back to the top and had screen readers read the page again every 2 s.
  New rows and groups are inserted around the ones on the page and nothing on it is replaced while it runs, so focus, selection and the scroll position stay put in a list that grows while it's read; this holds because a row lands in a group once and never moves, and rows are listed by position.
  Only the finish is announced, once, and it doesn't move the reader: no redirect and no focus change; announcing the progress line on every poll would bring back the repeated reading.
- **An outline's name becomes the subscription's name when it differs from the feed's title** (#442), so a reader's renames survive export and import; a name matching the feed's title stays `""`, so the feed's own title changes still reach them.
  "Differs" ignores case and whitespace, so a reader that exports the feed's title with different capitalization doesn't freeze it.
  Only a feed the import subscribes to is named: an already subscribed one keeps the name the reader has, as an import only ever adds tags.
  An outline title that's empty, equals its `xmlUrl` (our export writes the URL as `text` for a feed with no title) or is over 100 characters isn't a name, and the feed is subscribed without one rather than failing.
  A failed rename is logged and doesn't fail the feed, like adding tags.
  (#442)
- **Offered in Settings, next to Export, and on the empty timeline.**
- **Files up to 1 MiB and 1,000 feeds, 5 imports per user an hour.** The upload is a multipart form with its own size cap, not `decodePostForm`'s 64 KiB.
- **The file input has no `accept` filter.** iOS turns `accept` into system file types, has none for `.opml`, and so greys out OPML files in the picker, including our own export.
  No `accept` value both narrows the picker and lets iOS pick `.opml`, and the filter was never a safeguard: the server's size and feed caps and `opml.Parse` handle any file, and a file that isn't OPML gets the usual error.
  (#367)

## Reading

Planned in #163 (issues #147–#162).

- **A timeline, not an inbox.** Read state is recorded but only shown as a quiet marker on read rows (tinted card, dimmed text and image).
  No unread counts, badges, "N new" indicators, "mark all read", or manual mark read/unread.
  No unread filter for now.
- **Hovering a card never looks like reading it** (#421): hover never tints a card, on unread or read cards, in either theme, so the quiet read marker stays unambiguous.
  Instead the border firms up and the title takes the link color (`--accent`), the opposite of a read card's dimmed title, so a read card being reconsidered gets the same clear feedback as an unread one.
  Rejected: the border alone (too faint for a card that is all link), an underlined title (heavy on two-line Bricolage headlines) and a shadow (unused elsewhere, and invisible in dark).
  Hover styles apply only under `(hover: hover)`, so a tap on a phone doesn't leave a card looking hovered.
- **What counts as read:** opening the article page, or clicking through to the original from a row.
  Scrolling past doesn't; "seen" isn't recorded.
  Reads are per user and article, independent of subscriptions; an article counts as read if any copy with the same `canonical_url` was read.
  (#156, #158, #161)
- **Timeline order is publish order, never below what you've seen, and an entry never moves** (#147; changed by #455, planned on #438; per-reader positions planned in #491, issues #493–#495, #497 and #498, replacing #455's read-time grouping).
  Each article gets `timeline_at` once, on insert, shared by every reader: for news, its published date, but never below the later of the feed's previous fetch and 6 hours before the save, with news dated earlier spread evenly above that floor instead of stacked (aggregators date items by submission, some feeds only by day, some were down); the save time for undated news; its published date for backlog (how news and backlog are told apart is under Reading in features.md).
  A reader who has looked in the last 6 hours also gets their own position for news that arrives in a feed they follow, set once when it arrives, with the same rule but the floor raised to the newest position they've had on screen: news dated above what they saw keeps its date, and news dated below it is spread just above it (#491).
  So new items can't land below ones you've seen, a busy feed's fetch doesn't arrive as one block, republished archives don't flood the top, and nothing moves between two looks.
  With one shared position only two of those were possible: #455 grouped each reader's timeline at read time (on a reload, what arrived since their latest look; then since their previous visit; then the line), so a post published 4 hours ago and fetched while you read went first on the reload, then dropped to its 4-hours-ago spot on the next.
  Until #455, news went at the save time, which kept the first guarantee but made a busy site or aggregator a run of rows on every fetch.
  In a simulation of ~120 mixed feeds over three weeks, rows in a run of three or more from one feed went from 74% to 5%, with new articles missed staying at 0.1% (posts dated over a day before they appeared, which are backlog).
  Rejected: publish order without grouping (any window wide enough to mix things buried 1–7% of new articles), raising the shared floor to other subscribers' latest look (it reveals when others read, and with 50 readers 43% of rows were in runs again; a floor per reader has neither problem), delaying articles so they interleave (31 minutes later on average, and aggregators still clumped), and giving an article its own position only when the "seen" beacon fires, for arrivals shown out of date order (an arrival shown at its shared position before the beacon would then move).
  A feed you've just followed is new to you too: its posts from the last 6 hours get your own positions, above what you've seen, and its older posts stay at their dates, so you see what you subscribed to without its archive flooding the top (#491; imports follow the same rule).
  That replaces #490's rule that a first save never counts as arriving, which only avoided #455's jump-then-drop: judged by position, a new feed's latest post went first on a reload, then dropped to its date, and positions set once can't do that.
  Displayed dates may look out of order; accepted.
  Feed pages keep publication order.
- **Per-reader positions are kept only for each reader's recent entries** (#491, issues #493–#495; the table and the kept window were the owner's choice in triage).
  A row per reader per news article (`timeline_entries`) is written in `saveArticles`' transaction, for each follower who has looked in the last 6 hours (`rss.NewsWindow`): a reader away longer than that has a floor below every news position, so their own position would be the shared one, and readers without a mark (no JavaScript) read shared positions.
  Following a feed, one at a time or by import, writes rows for its posts from the last 6 hours, as does a first save for followers who subscribed before the feed was fetched.
  Every row is the shared position raised above what the reader has seen, when it's at or below it.
  At the start of each visit, a reader keeps every row above the previous visit's "You were here" line (what arrived since that visit started), but at least the newest 100 and at most the newest 500 by position (`rss.MinEntries`, `rss.MaxEntries`); older entries are at their shared positions.
  500 covers what 300 feeds bring since the previous visit started (in #491's measurements, 168 articles overnight and 435 a day) with room to spare, and 100 keeps about 5 hours of a heavy reader's history stable; a row takes about 224 B, so 500 take about 110 KB.
  A row's position is never below the article's `timeline_at`, so deleting one only returns the article to its shared position: trimming moves only rows that were raised, and only below the previous visit's line, where the reader isn't reading.
  It's also why there's no rebuild tool for drift: a raised position can't be recomputed (it depended on what the reader had seen when the article arrived), and deleting a reader's rows is always safe.
  A timeline is one list in position order, each entry at the reader's row if they have one, else at `timeline_at`: a page merges the reader's rows with #404's per-feed query for articles without a row, bounded below by the last row when the rows fill the page, so there's no seam to cross and no group in the cursor.
  On #491's prototype at `make db/loadseed`'s scale, Home's first page for 300 feeds took 4.6 ms (79 ms with #404's query alone, 5–52 ms grouped), and the first page past the rows 75 ms, as #404's pages do.
  As built (#494), the first page past the rows measured about 30% slower than #404's pages, from a lookup per scanned article to leave out those with a row; #502 checks the reader's row IDs in memory instead, after #495's trim bounds how many there are, rather than skipping the lookup below the oldest row, which cost page 1 more and didn't help the first page past the rows.
  It does that with an uncorrelated `a.id NOT IN (SELECT article_id FROM timeline_entries WHERE user_id = $1)` in the same statement, which Postgres hashes once per query, rather than #502's plan of reading the IDs in a statement of their own and passing them back as a `uuid[]`: the buffers are the same (25,300 to 44,000 before the copy check's change below), but it saves a statement and a round trip, keeps up to 1,500 or so IDs out of Go and the query's parameters on every page, and reads them in the same snapshot as the articles it checks, as the lookup did.
  Unlike hides' `NOT IN` (see "Hiding articles"), it doesn't outgrow `work_mem`: Postgres decides to hash from its estimate of a reader's rows, which the trim keeps near `rss.MaxEntries` (500), far below the over 100,000 IDs the default 4 MB holds, and a reader with 1,500 rows (a long visit's before its trim) measured the same.
  #502 also checks a copy's shared position before looking up its row: a row is never below its article's `timeline_at`, so a copy that isn't earlier by `timeline_at` can't be earlier by its row, and the article itself and later copies are left out before the joins (20,450 buffers, against #404's 19,650 for the same page).
  Copies collapse at query time by the reader's positions, so nothing is collapsed on write.
  Following a feed doesn't move what's already in your timeline through its copies (#506): a blog's post from two days ago, just seen at the top through an aggregator, would otherwise become the earlier copy and take the entry down to its date, so the new feed's earlier copy gets your own position just below the entry you have, within the part of the timeline your rows cover (below that, a row would be trimmed at your next visit anyway).
  Unsubscribing keeps your rows for the feed (#506): timelines only show rows of feeds you follow, so they're unseen until you follow it again, when its posts stay where they were instead of its last 6 hours being raised above what you've seen a second time; visits trim them like any other row.
  Every timeline uses the same positions (the mark is already shared across tabs), except starred-only timelines (star order) and shared timelines, which have no reader and use `timeline_at`.
  Older backlog stays shared: only a newly followed feed's posts from the last 6 hours get rows, so subscribing and importing write a few rows per feed rather than a backfill, and old posts never jump.
  Rejected: rows for every article ever arrived (a backfill on every subscribe and import, 4.8 s for 300 feeds of two years, and storage growing with articles × followers); a fixed count alone (300 feeds bring well over 100 articles overnight, so the oldest arrivals since the last visit would lose their rows); a time window alone (unbounded for heavy readers, and still short after a long absence); rows complete above a stored seam, with backlog rows and a backfill above it on subscribe (page 1 from rows alone took 1.2 ms, but every write path would have to keep the seam whole); and trimming at ingest or in a background job (what's kept moves with visits).
- **On a feed's first save, undated items are backlog too** (#438): each run goes below the item before it in the feed's document, a step apart (the median gap between the feed's dated items, at most a day; a day without one), the newest no later than a day before the save, and spread evenly down to the next dated item where stepping would pass it.
  We can't know when they were published, and an undated feed would otherwise fill the top of Home on import; spacing them keeps them from forming a block lower down too.
  Rejected: below the save's dated items 1 µs apart, as #438 first proposed (still a block), and the newest near the top as a taste of the new feed (an import with many undated feeds would put one per feed on top).
  On later saves an undated item is news.
- **The timeline is `/`** for logged-in users (the nav label stays "Home"); `/` opens your first tab, or the one you were on during this visit (#480, "Timelines" under "Filtering and timelines").
  Duplicates are collapsed at query time on `canonical_url`, keeping the earliest copy by position, with "Also in" on the row.
  Feed pages show every copy.
  (#154, #162)
- **Timelines read each followed feed's newest entries, then merge them** (#388, #404).
  A page reads at most a page of entries from each subscribed feed and keeps the newest, so its cost follows the number of feeds followed (about 0.1 ms each at #388's scale), not how much history is kept.
  Copies are still collapsed at query time, but ingest flags articles that have a copy (`has_copies`, set and never cleared), so only those get the earlier-copy check; a stale flag costs a lookup, never a wrong result.
  Chosen over a per-reader timeline table, which measured faster (under 1 ms) but stores a row per reader per article, needs a backfill job on every subscribe and import, and must repair copies when someone unsubscribes.
  #491 adds per-reader rows for each reader's recent entries only (above), and older pages still come from this query.
  It doesn't wait for the article-retention decision, since its cost doesn't depend on history; neither do the capped per-reader rows.
- **JIT is off for the app's database connections** (#404).
  Postgres compiles queries whose estimated cost is high, which added 20–70 ms to each timeline query in #388 and saves nothing on queries this size.
  It's set per connection in `psql.NewConnector` (used by `psql.OpenDB` and by `tools/loadseed`'s measurements), so production, dev, CI and the measurements match without an owner step.
- **Word filters will use a full-text index, not trigrams** (#388).
  On #388's seed a `pg_trgm` index was 127 MB (a quarter of `articles`) and didn't help common words, while a `simple` full-text index was 26 MB and took a rare-word timeline from ~0.9 s to ~50 ms.
  The exact word match stays as the final check, so results don't change.
- **Rows are divided sections with a preview card**, one design for the timeline and feed pages: the feed's avatar and name with the time, then a card with the optional image, title, two-line excerpt and a small link to the original.
  The whole card links to the in-app article page.
  Feed pages leave out the avatar and name.
  Untitled articles use the start of the excerpt.
  (#181) The image, title and excerpt are the feed's, else the article page's (see "Enrichment", #307); the feed's own values always win.
- **Article page:** sanitized content, else summary, plus "Read on {site}".
  No previous/next.
  Any logged-in user can open any article.
  (#152) The card image shows above the text as a lead image unless the content already shows it: the content starts with an image, has one right after its first paragraph, or contains the same image anywhere (#295).
  With no content or summary, the page's description from enrichment is shown (#307).
- **"Read on" names where the link goes** (planned in #344, issues #340–#343): the article's domain when its URL is on another site than the feed's (aggregators such as Hacker News), else the reader's name for the feed, else the site's title, else its host.
  A name the reader gave the feed beats the site's title: it's theirs to choose, and an off-site article still shows its domain.
  Hosts on a subdomain of each other count as one site, without the public-suffix list (a dependency for a rare case).
  The domain needs no fetching and is always accurate; `og:site_name` from enrichment is deferred, since enrichment doesn't run for every article.
  A long name is cut with "…" so the button stays on one line, and its `title` and link text keep the full label.
  (#340, #341, #343)
- **Article HTML is sanitized at render time** with bluemonday: relative URLs resolved, iframes replaced by a link, links opened in a new tab with `noopener noreferrer`.
  Images load from the publisher with `referrerpolicy="no-referrer"` and lazy loading; the CSP allows `img-src https:`.
  An `http:` image URL (card image or `<img>` in the body) loads over `https:` instead, rewritten at render time so stored values stay as published: a host that serves both loads, and one that serves only `http:` was blocked anyway, so upgrading beats dropping the image (#426).
  No image proxy yet (a backlog item, with why it's its own project).
  (#151) Changed in #309 for two cases:
  - Content with no block-level markup (plain text, such as JSON Feed's `content_text`) is split into paragraphs at blank lines, with `<br>` for single line breaks, and bare URLs become links everywhere (#294).
    Stored HTML is unchanged.
  - YouTube and Vimeo iframes become click-to-play previews (#296): nothing loads from them but YouTube's thumbnail until the reader clicks, then a sandboxed player from `youtube-nocookie.com` or `player.vimeo.com` (the CSP's only `frame-src` hosts).
    Without JavaScript the preview links to the video.
    Every other iframe stays a link; more embed types wait for the measurements in backlog.md.
- **Reading preferences** (text size, serif or sans, line width) **wait for themes**; the article page gets better defaults first: wide tables scroll inside the column, styled code, lists and rules (#295).
- **Audio and video are measured before they're built** (#297): only the media type of an item's first audio or video enclosure is stored, so the owner can see how common they are, alongside queries over stored HTML for embeds.
  No player yet.
- **Paging works without JavaScript** ("Older articles" with a `?before=` cursor); infinite scroll enhances it.
  Logged-in pages keep `Cache-Control: no-store`; the script keeps the URL on the visible row so Back and reload land nearby.
  (#155, #159)
- **Times are server-rendered in UTC** in a `<time>` the script localizes; date-only feed dates show just the date and aren't localized.
  No day headers or relative times.
  (#157, #160) This covers every user-visible time, not just articles: feed and site status times ("Last fetched", "Next check around") and list dates ("Latest article") too, still absolute, with no time zone setting.
  (#337)
- **New articles don't appear live** while you scroll; a reload shows them on top.
- **A "You were here" line marks where the previous visit ended**, with no counts (planned in #395, issue #397).
  It's stored per user on the server (`timeline_marks`), so it follows the reader across devices; a marker in the browser would need no schema but differ on every device.
  The page reports the newest position on it with a beacon, never from the GET that renders it ("Overall"): when it's hidden, and with #491 also when it's shown, since each arriving article's position for the reader is set against the latest report, so an article arriving while a page is open must already count what's on it.
  The moment between a render and its report (well under a second) isn't covered; accepted.
  Each timeline has its own line (#498, planned in #491): where you were on that timeline at the end of the last visit you opened it in, so reading only Quiet posters doesn't move Home's line above Home articles you never had on screen; starred-only timelines, ordered by star time, show no line.
  The floor arrivals are raised above stays one per user, the newest position you've had on screen on any timeline: per-timeline floors would mean per-timeline positions (up to 21 times the rows and the writes, and the same article in a different order on each tab), and a shared floor can only put an arrival higher, by at most the minutes since its feed's previous fetch.
  The line moves only after 30 minutes away (`rss.VisitGap`), so a reload or a quick tab switch keeps it in place.
  While you're using a timeline page (scrolling, a key, a tap or a click), it also reports every 10 minutes (#506): with the report on show, a visit was measured from the page's load, so a read of one page past 30 minutes started a new visit when you left it, moving every line to the top of the page you'd been reading; a page left on screen while you're away still lets the visit end.
  With per-reader positions the line is just a position again (#491): everything that arrived since the previous visit is above it by position, so the mark no longer keeps when each look was rendered, as #455's grouping needed.
  Readers without a mark (before their first report, or without JavaScript) get the shared positions; accepted.
  There's no line before the second visit, at the top of the page, or without JavaScript (no beacon, so it would never move).
  #397 accepted one line for every timeline (a row per timeline for a rare case); with default tabs (#482) and `/` reopening the tab you were on (#480), reading one tab only is common, so #491 changed that.

### Saved articles

Planned in #309 (issues #299–#301).

- **Saved means bookmarked, kept until you remove it**, not a read-later queue: no archive, no "done", no counts.
  A queue you work through to empty is an inbox, which the timeline deliberately isn't.
- **Called Save / Saved, with a bookmark icon**, not a star or heart, which read as "like".
  Saving is quiet (no flash) and doesn't mark the article read.
  The star is a separate action that does mean "this is good" (#349, "Filtering and timelines").
- **Saving covers every copy** (same `canonical_url`), like read state: `saves` keys on `article_id`, and copies are matched at query time.
  Saving a copy of an already saved article adds nothing; unsaving removes them all.
- **With JavaScript, a Save or Star succeeds only on a `204`** (#429, from the first error-state pass, #392).
  The toggle routes answer a script (`Sec-Fetch-Mode` `cors` or `same-origin`, which a page can't set) with `204` and a form submission with the `303` back to the row; `app.js` submits the form normally on anything else.
  Before this, both a success and the redirect to the login page after a session ended came back to `fetch` as the same opaque redirect, so Save looked done when nothing was saved.
  A browser that doesn't send `Sec-Fetch-Mode` gets the `303` and falls back to a reload: slower, but right.
- **Saved has its own nav entry**: Home, Saved, My feeds, Search.
  Newest saved first, paged like the timeline, with a search box (`ILIKE` over title, page title, excerpt and feed title; a user's saves are a small set).
- **Saved articles must survive article retention**, and go into the full data export.
- **The Save button sits under the card, right-aligned, with icon and label** (owner's pick in #316, from three layouts: this bar, an icon on the card's corner, an icon in the meta line).
  The card's left edge stays clean, and it's under the thumb on phones.
  "Also in" shares its line, on the left.
  More actions (Share) join it to Save's left.
  On the article page it sits under the title, left-aligned with the heading.
- **The Star is an icon-only button to Save's left** (owner's pick in #349, from three layouts: a labelled Star next to Save, this icon-only Star, a Star in the meta line).
  It stays compact next to the labelled Save and leaves room for Share, which goes between them.
  Its accessible name is its `aria-label`, "Star"; `aria-pressed` carries the state, as on Save.

### Sharing articles

Planned in #395 (issue #396).

- **Share sends the original article's link, never the in-app page**, which only logged-in users can open: its canonical URL, so tracking parameters don't travel on, with the title.
  Articles without a usable URL have no Share button.
- **The system share sheet where there is one, else copy the link** (the link alone, so it pastes cleanly).
- **It needs JavaScript, so without it there's no button**, rather than a button that does nothing; "Read on" and the browser's own share remain.
  For the same reason a browser with neither a share sheet nor the Clipboard API (which needs a secure context) gets no button either (#511).
- **An icon-only button between Star and Save**, as "Saved articles" planned.
- **Nothing about sharing is recorded**: no counts, no request to the server.

### Hiding articles (not taken in)

Planned in #309 (issue #308) as a prototype; built in draft PR #325 and **not taken in** (#308 closed), for the three reasons below.
Planning filters (see "Filtering and timelines") kept it out: a timeline's keyword filters and Leave out cover what it was for (sponsored posts, topics you don't follow) without a per-article table.
Revisit only if they turn out not to.

- **UX:** Undo lasts only until the page reloads; after that the only way back is the article's feed page, since there's no Hidden list.
  Hiding everything also lands on the empty-timeline message, which reads as "you have no feeds".
- **It invites inbox use.** Hiding each article as it arrives turns the timeline into an inbox to clear, which the timeline is designed not to be ("Reading" above).
- **Performance:** `hides` grows by a row per hide, never trimmed, and every timeline page must read all of a user's hides to leave them out.
  In the prototype, a `NOT EXISTS` filter made Postgres pick a plan about 5x slower, and the faster `NOT IN` stops being hashed once a user's hides outgrow `work_mem`, re-checking them per article.

What the prototype settled, as a starting point if it comes back:

- **Hiding only affects your timeline.** The article stays on its feed page, marked "Hidden from timeline", with an Unhide button; there's no Hidden list, so hiding doesn't become archiving.
  Unhiding puts it back at its original position, since positions never change.
  Hiding covers every copy, and doesn't mark the article read.
- **Undo right away:** with JavaScript the row collapses to "Hidden · Undo"; without it, a flash says where the article went.
- **Its own table, outside the batch's schema change**, so dropping the prototype leaves nothing behind.
- **Muting feeds and keyword filters are a separate planning item**, not part of hiding.
- **The hide ✕ goes at the row's top right**, on the meta line, not in Save's action bar (#316): next to Save, a mis-tap would hide the article you meant to keep.

## Filtering and timelines

Planned in #365 (issues #348–#364; #353, #355 and #361 were folded into #351, #354 and
#359, and #363 and #364 into #381), from the owner's list: favorites, tags, several
timelines with filters, a quiet-posters timeline, shared timelines.
The batch's schema, with #342's subscription names, lands in one change (#348).

### Stars and tags

- **A star on articles means "this is good"; Saved stays "keep".** The owner chose stars on articles over favorite feeds; a tag such as "Favorites" covers favorite feeds.
  A star is what a picks timeline, and a shared link blog, is made from.
  Starring covers every copy, is quiet, doesn't mark the article read, and is private except through a timeline the user shares.
  It's an icon-only button to Save's left (see "Saved articles" under Reading).
  (#349)
- **Stars outlive subscriptions**, like saves, so unsubscribing never edits a picks timeline that shows all feeds.
  (#358)
- **Tags are free text on subscriptions, several per feed, not folders**: a feed can be both "Tech" and "News".
  There's no shared list of categories, which would be a guess and worse for private organizing.
  Names are trimmed with inner whitespace collapsed, compared ignoring case (`citext`), 1–50 characters with no commas, up to 100 per user.
  Managed at `/tags`; added on Edit subscription (checkboxes plus a comma-separated field, no JavaScript) and in bulk from a tag's page.
  (#351, #352)
- **A tag's page is one checklist form of all the user's subscriptions**, since tagging many feeds one Edit page at a time is the real pain.
  Saving sets exactly the ticked ones, leaving their other tags alone, with no JavaScript needed; it isn't paginated, like My feeds (watchlist).
  **Deleting a tag isn't confirmed**, like unsubscribing: the flash says what was deleted.
  (#351)
- **Composite foreign keys keep tags and timelines inside one account**, and unsubscribing drops the subscription's tags and timeline rows (cascade), like its name.
  (#348)
- **OPML: folders become tags on import** (every folder an outline sits in, at any depth; import only adds tags), **and the export nests feeds in a folder per tag** (a feed with two tags appears in both; untagged feeds at the top level), which every reader imports as folders.
  No `category` attribute, which most readers ignore.
  These change "OPML import" under Subscriptions and the export under Accounts.
  (#354)
- **Both are a choice, defaulting to tags and folders** (decided while building #354): the import form asks "Turn each folder into a tag" or "Ignore folders", and an export page asks "Turn each tag into a folder" or "Ignore tags" (a flat list), since someone coming from a reader whose folders they don't want, or going to one that handles nesting badly, shouldn't have to undo it by hand.
  The choice isn't remembered.
  The export's choice is a query parameter (`?tags=ignore`), so the plain download link (on the delete account page) still works and gives folders.

### Timelines

- **Timelines are explicit, not one per tag.** Tags organize; a timeline is made on purpose (a tag's page offers "Make a timeline"), which keeps the tab row short.
  (#356)
- **Tabs at the top of the timeline pages**, at `/timelines/{id}`, managed at `/timelines`.
  The nav rail is unchanged, and tabs carry no counts.
  (#356, #357)
- **Reordering works with buttons, and dragging is an extra** (#479, #481; this reverses #356's "no drag and drop, which needs JavaScript"): every row on `/timelines` has move up and move down buttons, which work without JavaScript, and `app.js` adds drag handles and moves rows in place.
  The buttons stay where dragging works: WCAG 2.2's 2.5.7 (Dragging Movements, AA) needs a single-pointer way to do whatever dragging does (W3C's technique G219), and they're the keyboard path.
  Dragging uses Pointer Events, not HTML drag and drop, which doesn't work with touch on many phones.
  The look (#481, approved at its design checkpoint): a six-dot grip at the start of each row, rendered hidden and shown by `app.js` like the Share button, and the only part of the row that takes a touch drag, so the rest still scrolls; the dragged row lifts with a shadow and follows the pointer, held within the list, and the others slide out of its way when its edge crosses their midpoints (#519; they used to jump when its pointer did); Escape puts the rows back; a moved row flashes the info tint for under a second (none under reduced motion), and the live region says "{name} moved to position {n} of {total}."
  Each change saves the whole order at once; an order that no longer matches the user's tabs (changed in another window) isn't saved, and the page reloads with a flash saying so.
- **Deleting a timeline stays at the foot of its edit page, not on `/timelines`' rows** (#479): the move buttons beside it are pressed often, deleting has no confirmation or undo, and deleting a shared timeline breaks its link for everyone following it.
  At 320 px a row has no room for a fourth control, and row menus were ruled out for My feeds (#424).
- **The tab row is underlined tabs in place of the page head's heading**, the owner's pick of three options (pills under the heading; wrapping pills with More on phones) for its app-like look.
  The tabs scroll sideways when they don't fit; `app.js` scrolls the current one into view, and without JavaScript the row starts at Home.
  The row ends in an "Edit" pill to `/timelines`, outside the part that scrolls, so it shows on phones however many tabs there are; the tabs fade out before it when they overflow (#479, replacing the "New timeline" / "Timelines" item that changed its name and scrolled out of view).
  A text pill rather than an icon, like every page-head action; a gear would read as Settings.
  The description goes in a row under the tabs, only when there is one, so switching tabs doesn't move the articles; the Edit link that row used to hold is gone (#479).
  (#356, #479)
- **`/timelines` mirrors the tab row, with rows like My feeds'** (#479): Home first, then each timeline; the row opens the timeline, and move up, move down and the Edit pencil are icon buttons at its end, the arrows disabled at the ends so rows line up.
  Each row says what the timeline is made of ("Tagged Tech · Mentions “AI” · Leaves out 1 feed") instead of its description, so a timeline can be told apart, or found broken, without opening it.
- **The tab row is navigation, not ARIA tabs.** Each tab is its own page, so it's a `<nav aria-label="Timelines">` list of links with `aria-current="page"`; `role="tablist"` is for switching panels within a page and would promise arrow-key behavior links don't have.
  The `<h1>` (the timeline's name, or Home) stays, visually hidden and first in the header, so headings still outline the page.
  (#356)
- **A filter is a form, not a query builder.** Show is all my feeds, or feeds with any of these tags or these feeds; Leave out is tags and feeds, and always wins; then conditions that must all hold: starred only, at most N posts a week, must mention / leave out words, audio or video only.
  Timelines pick only from your subscriptions, except a starred-only one showing all feeds.
  (#356, #358–#360)
- **The form reads itself back** (#483, the owner's approval at the design checkpoint): a summary box first says what the timeline shows in a sentence ("Articles mentioning “AI” from feeds tagged Tech, except The Verge.") and how many of the user's feeds it draws from, then Name, Feeds and Articles, then Save.
  The sentence spells out the rules the form can't show (Leave out wins; every ticked condition must hold), which is where a filter form goes wrong (Apple Mail's any/all menu is the cautionary case); the count answers "what number?" for the posting rate better than pointing at My feeds.
  It replaced one long page that asked everything at once in four styles: the tag list twice, hints in rows of their own, and Save above a Sharing section it didn't save.
- **One pattern per kind of setting** (#483): "All my feeds" or "Only some" as one segmented control, with tags as wrapping pills and feeds folded away with a filter box; Leave out folded away, open when it holds something; and every condition a checkbox that reveals its setting.
  Reveals are CSS `:has()`, so no JavaScript is needed; since hidden fields still post, the server decides by the checkboxes, and an unticked condition stores nothing.
  The summary is built in Go and rebuilt by `app.js` with the same words; the Go tests are the spec.
- **Sharing has its own page** (#483), from Share in the editor's head, since sharing isn't part of what Save saves; the editor's "All timelines" pill went with it (Cancel and the tab row's Edit cover it).
- **The same reading rules as Home:** arrival order, copies collapsed among the timeline's own feeds, "Also in", read markers.
  A starred-only timeline orders by star time instead, so new picks land on top.
  (#356, #358)
- **New accounts start with Quiet posters and Starred** (#482; this replaces #356's "presets on New timeline, not built-in tabs"): two ordinary timelines, created with the account, that can be renamed, changed, shared, moved or deleted like any other, and count toward the limit.
  Quiet posters best shows what the app is for, and nobody makes it on day one; without a starred-only timeline a star has nowhere to appear (NetNewsWire ships Starred as a built-in view for the same reason).
  The cost is two tabs for people who'd use neither.
  New timeline still offers both, among its starting points, so a deleted default can come back.
  Accounts made before #482 don't get them (pre-alpha).
- **New timeline starts from a list of starting points** (#482): Feeds with a tag, Quiet posters, Starred, Keywords, Audio and video, Start from scratch, each a row like Settings' home list (an icon on an avatar tone, a one-line hint, a chevron) that opens the form filled in.
  It replaced a small "Start from" line above the form, so the form with every option was where everyone started; the list also says what timelines can be made of.
  Without tags, Feeds with a tag leads to Tags, to make one first.
- **An empty quiet-feeds timeline says what will show up** (#482): "When a feed you follow posts at most 2 times a week, its articles show up here, so busy feeds never bury them.", since a new account's Quiet posters is empty until it follows quiet feeds, and "Nothing in this timeline yet." read as broken.
- **Quiet posters use the feed's 30-day average**, stored as `feeds.posts_per_week` on every save and 304, and shown on My feeds and feed pages ("About 3 posts a week") to help pick N. One whole-number field for now; richer frequency rules are a backlog item.
  The average is over the time the feed's articles cover, up to 30 days and never less than 7: a feed we've just found only has its current file, so dividing by 30 days made a busy new feed look quiet.
  The 7-day minimum keeps a weekly feed whose file holds only its latest post from counting as 7 a week (as a 1-day minimum would); the cost is that a busy feed whose file holds few items is understated in its first week.
  Accepted: an undated feed's articles all date from their first save, so it gets the 7-day minimum until it has history.
  (#350, #359, #443)
- **Keyword filters match whole words and phrases, ignoring case, in the title, page title and excerpt**, never the full content (lists don't load it, and it's unbounded).
  A term matches where the characters just before and after it aren't letters or digits, so terms like `C++`, `C#`, `.NET` and `#hashtag` work (symbols are matched literally), and `AI` still matches "#AI" but not "said".
  Accepted: `C` alone also matches "C++"; counting symbols as letters would stop `AI` matching "#AI".
  A term needs a letter or digit.
  Any term matches; leave-out wins.
  Plain matching only: smarter matching belongs with related articles (backlog).
  (#360)
- **Audio or video means an enclosure** (`articles.media_type`), not a video embedded in the HTML.
  (#359)
- **Home shows every feed you subscribe to and can't be edited; there's no muting.** To keep a noisy feed out of view, make a timeline that leaves it out.
  Muting on Home (#362, not built) was dropped: a feed missing from the main view, with the reason only on its Edit page, raises "why don't I see X?" with no answer on the page, and it would have been a second way to filter beside the timeline form.
  Home has no `timelines` row.
- **Home can move, and `/` opens the tab you were on during this visit, else your first tab** (#480), for someone who reads a filtered timeline more than Home (the backlog's "Start on a chosen timeline").
  Home is one of the tabs you order on `/timelines`, but it still can't be edited, shared or deleted; most people will keep it first.
  A visit is the 30-minute gap that also moves "You were here" (`rss.VisitGap`), so the nav's Home and the logo bring you back to the tab you were reading, and a later visit starts on your first tab.
  Per browser session was the other choice, but session cookies on phones last for days, so the first tab would rarely apply.
  The visit's tab is a cookie, checked against the user's tabs on every use, rather than a session value: sessions are stored in Postgres, so it would be a write on every page view.
  Home gets its own address, `/timelines/all`, since `/` may show another tab; its position is a column on `users` in the same numbering as `timelines.position`, since Home has no `timelines` row.
  Its tab is called "All feeds" (the owner's pick at #480's design checkpoint, over keeping "Home" and over Bluesky's "Following"): once it can move, the nav's "Home", which opens your first tab, and a tab called "Home" sitting third would say different things, and "All feeds" says what it is in the app's own words ("My feeds").
  The code still calls it Home (`homeName` holds the label).
- **Limits:** 20 timelines; names up to 50 characters, descriptions up to 280.
  (#356)
- **The form stores only what has an effect.** With Show set to all feeds, the tags and feeds ticked under Show are dropped on save; a tag or feed both shown and left out is stored as left out; IDs that aren't the user's are ignored.
  A timeline showing only some feeds whose sources are all gone (say its only tag was deleted) stays, with an empty state asking to edit it, rather than silently showing every feed.
  (#356)
- **Home's heading is "All feeds"**, matching its tab, and each custom timeline's heading is its name, both visually hidden (above).
  (#356, #480)
- **No unread filter or counts**, as under "Reading".

### Sharing

- **A shared timeline is an Atom feed, with a landing page at its link** (#381, which replaced #363 and #364).
  People follow someone's picks in their own reader, which brings new ones to them; a web page listing the articles is a weaker copy of the feed, with no profile to attach it to (none is planned).
  The page shows the timeline's name and description and how to subscribe, and advertises the feed, so the link can be sent to anyone and readers given the page's address find the feed.
  It doesn't list articles, so only the feed runs the timeline query. #363 planned a page of the newest 50 articles; this reverses it.
- **Opt-in per timeline, unlisted, at an unguessable link**: 32 random bytes, stored as is (not hashed like email tokens), since the owner's settings show the link again and it only grants reading what they published.
  The page and feed are `noindex` and use no session.
  Stop sharing makes the links a 404; New link replaces them.
  Home can't be shared.
  Needs a confirmed email ("Accounts").
  (#381)
- **Each entry is the title, a link to the original, the dates, the excerpt, the article's authors and the source feed.** Never the content: republishing publishers' text is the copyright question under "Enrichment".
  The authors were added after #381, with the privacy policy's list of what sharing publishes; an entry without them falls back to the feed's author (the timeline's name), as Atom specifies.
  Articles without a link are left out.
  The feed has the newest 50.
  (#381)
- **No owner name or email**, only the timeline's name and description (the feed's author is the timeline's name, since Atom requires one); the feed does name the feeds its articles come from, and the settings say so.
  **The source is the feed's own title**, never the owner's private name for it (#342).
  (#381)
- **Atom, not RSS 2.0:** typed text (`<summary type="text">`, so plain excerpts are never guessed to be HTML), required stable IDs (the canonical URL) and RFC 3339 dates, and `<source>` for each entry's original feed.
  Every reader reads both, and users only see "feed".
  Built with `encoding/xml`.
  (#381)
- **Cheap to poll:** an `ETag` of the body, a 304 when `If-None-Match` matches, and `Cache-Control: public` (15 minutes for the feed, 5 for the page).
  More caching is a watchlist item.
  (#381)
- **Public routes are a deliberate exception** to "Search, site pages and lookups are login-only" (Feed discovery): their owner opted in.
- **Shared timelines aren't listed in Search** for now: their names and descriptions would be the first user-written text shown to strangers, which brings spam, impersonation and reporting.
  A backlog item.
- **A link to the app itself never becomes a feed in the catalog** (#471): a shared link looked up or imported in Paper Round would be saved under the app's own site, whose feeds search and the site page list to every account, so one follower would publish an unlisted timeline to everyone.
  How it's refused is under "Feed discovery".
  Until following picks inside Paper Round is planned ("Your friends' picks" in backlog.md), a shared link is followed in another feed reader.
- **The privacy policy says what's kept and what sharing publishes**, in the same PR (#381).

### Cross-user use (later)

- **Stars and tags may later feed anonymous totals across users**, e.g. "people who starred this also starred…" or a feed's usual tags as its category.
  Only as aggregates above a minimum count, never traceable to one person, and after a privacy policy update.
  The tag hint says "Only you see your tags", which stays true; it doesn't promise the data is never used.
  It would relax "no subscriber counts" (Subscriptions) for aggregates only, when planned (see "Related articles" in backlog.md).
  The data is shaped for it now: tags on subscriptions add up per feed, and stars are on articles with `starred_at`, matched across copies by `canonical_url`.

## Enrichment

Planned in #309 (issues #298, #306, #307).

- **Articles missing an image, title or excerpt get their web page fetched once**, in the background, for its `og:image` (else `twitter:image`), `og:title` (else `<title>`) and description.
  Articles complete from their feed cost nothing.
  No full text (an on-demand "Load full article" is a backlog item, never automatic: storage, failed extractions, and the copyright question of storing publishers' text) and no AI.
- **Enriched values have their own `page_*` columns** and only fill gaps: the feed's own image, title and excerpt always win.
  `saveArticles` overwrites the feed's fields on every changed fetch, so enrichment never writes them.
- **How far back:** a feed's first save (a lookup or an import) queues only its 20 newest articles, at a lower priority than new arrivals; after that, every new article that qualifies.
  Older articles wait for a low-priority backlog queue (a backlog item, for future search and related-article features).
- **Its own queue table (`enrich_jobs`) and runner**, one worker by default.
  Jobs are only claimed for feeds someone subscribes to, as refresh only fetches subscribed feeds, so a looked-up site nobody follows costs no page fetches.
- **Polite, with a retry story:** one request at a time through `internal/fetch`; a network error, 429 or 5xx is retried after 1 hour, then 6, then given up; any other 4xx or a non-HTML response is given up at once, as is a fetch `internal/fetch` refuses (a blocked address, a body over 5 MiB, too many redirects), which a retry wouldn't change (#306).
  `robots.txt` isn't consulted: it's one fetch of a page a reader is about to see, like a link preview.
- **One page parser:** `discovery.ParsePage` reads Open Graph tags and icon links too (#298), for both enrichment and site icons.

## Design

A modern, minimal look modelled on social timelines, from the redesign in #181.

- **One centered column, nav on the left.** A rail with icons and labels on wide screens, icons only on medium ones, and a top bar (logo, logout) with a bottom tab bar on phones.
  CSS only; no JavaScript.
- **320 px is the narrowest width we support:** no page scrolls sideways at 320 px or wider (#431).
  A subscribed feed page shows Edit and Unsubscribe without a "Subscribed ✓" chip, since the red Unsubscribe already says you're subscribed and the chip pushed the row off narrow phones; wrapping the buttons beside the avatar looked ragged, and moving them under the heading pushed the feed's facts down.
- **Lists are sections separated by dividers, not boxes.** A card is only for a preview of something elsewhere: an article now, Open Graph data later.
- **Avatars are letters on a tint chosen by site** (`avatarTone`), so a site's feeds share a color, for sites without a usable icon (below).
- **Site icons replace the letter where a site has a usable one** (planned in #309, issues #304, #305):
  - We keep our own copy and serve it from `/sites/{id}/icon?v={hash}`, rather than loading it from the publisher: publishers don't see who's reading, hotlink blocking doesn't break it, and we have to download it anyway to know its real size.
  - Only PNG, JPEG, GIF and ICO up to 256 KiB, served with a sandboxing CSP and `nosniff`.
    Never SVG, which could run script from our origin; no WebP for now (its size needs `golang.org/x/image`).
  - At least 32 px on the shorter side and roughly square, else the letter stays: a 16 px favicon stretched to 40 px looks worse than the letter.
    Generated avatars for those sites are a backlog item.
  - Checked by their own runner soon after a site is created, then about every 30 days for sites with subscribers, with a conditional request so an unchanged icon costs almost nothing.
    A failed check keeps the current icon; it's removed only when the site's page loads and none of its icons is usable.
  - Site and feed avatars become rounded squares, icon or letter, since most icons are designed as squares.
    The user's own avatar stays a circle: people are circles, sites squares.
- **Colors are custom properties on `:root` in `main.css`, so a theme redefines them in one place.**
  Icons are inline SVG partials (`templates/partials/icons.html`), not an icon font or library.
- **A dark theme follows the system, with a choice of System, Light or Dark in Settings** (#413).
  The dark palette is the light one's, darkened: the logo's ink, darkened, for the background, its sky for links and a lighter tomato (`--brand-strong`), with text at 7:1 or more.
  The values live in one block per theme (repeated once for the media query and once for the explicit choice), so another theme is one more block.
- **Dark keeps the light theme's color where color means something, and stays calm everywhere else** (#413).
  Avatars are mid-tone colored tiles with near-white letters, the dark counterpart of light's pastel tiles; the error, flash and debug boxes are mid-tone tints with light text (`--on-danger-bg` for errors); the sky accent is saturated.
  The page, cards, text and images stay low-chroma, since saturated large areas glare at night.
  A first version mixed the tints almost into the background and read as dull next to light.
  Logged-out and error pages always follow the system, since they don't know the user, and the installed app's splash stays light, since the manifest can't vary.
  The browser bar (`theme-color`) follows the user's choice on logged-in pages, so a dark page doesn't sit under a white bar; the issue had accepted the system's, but the page knows the choice, so it costs one argument per page.
- **Read cards stay quieter than unread ones in both themes** (#413): they have their own tint (`--read-bg`), the sky tint in light and darker than the page in dark, since the lighter `--surface` would make read cards stand out at night.
- **The danger color is lighter in dark** (#413), so error text stays readable, and the Delete button's text is the dark background, at about 8:1.
- **Reading text is a little heavier in dark** (#413): Newsreader at weight 440 rather than 400, since light text on dark reads thinner.
  Pure white text is avoided, since it glares.
- **The logo stays as it is in dark** (#413): its ink outline disappears, but the white page still reads as the mark; the favicon stays too, since browser tabs have their own background.
  Site icons keep a light tile in dark, softened to the text color, since many are drawn for white.
- **Images in articles are left as they are in dark** (#413), even transparent diagrams and logos with black text, since a white tile behind every image looks worse for the common case; revisit if testers report them.
- **The app is called Paper Round** (paperround.app, #408, #409), written as two capitalised words wherever users see it: page titles, the nav, emails, the OPML export, the shared feeds' generator and the fetcher's User-Agent.
  The repository, Go module, `rss` package and `RSS_` variables keep their names, since nobody outside the code sees them.
- **"Log in" and "Log out" are verbs, and "Login" never appears in the UI** (headings, buttons, links, page titles), because buttons name actions.
  The `/login` route, `loginHandler` and other identifiers keep `login`: nobody sees them.
- **The logo is a front page:** a newspaper whose nameplate is a P over a double rule, with a sunrise as the lead photo, drawn with ink outlines (`web/app/public/img/logo.svg`).
  It was picked over a letterbox (read as a ballot box), a mailbox (too close to email), a delivery bike, and a route drawn as a P (needed explaining).
  The favicon (`web/app/public/img/favicon.svg`, plus 32 px and Apple touch PNGs) is the same page without the small text lines, so it reads at 16 px.
  The P is outlined in both files, so they don't depend on the font loading.
- **The palette comes from the logo:** ink for text, sky blue and tomato from the sunrise, with a sky tint for surfaces and read rows.
  Green and gold, maroon and an orange box were tried and dropped.
- **Tomato is the one accent, used sparingly** (#414): the active timeline tab, starred articles (so Star no longer looks like Saved's blue) and the "You were here" marker.
  It's a deeper shade than the logo's (`--brand-strong`), so the marker's label passes AA as small text.
- **Text colors pass WCAG AA** (4.5:1) on white and on the read-card tint (#414); the lightest grey was 3.2:1 before.
- **Input and outline-button borders are 3:1 in both themes** (`--border-strong`, #413), WCAG's minimum for a form field's edge; light's was 1.6:1 before.
  Filled fields (the search boxes) have no border and keep it that way.
- **Actions that lose something you can't get back are outlined pills with red text** (`.btn-danger-quiet`, #422): Unsubscribe (it drops the subscription's name and tags), Delete tag, Delete timeline and Delete account, on every page that has them.
  Red (`--danger`) marks only these, so it keeps its meaning; Stop sharing and New link stay neutral, since no data is lost and Share gives links again.
  They keep the pill so they read as buttons at once (borderless red text was tried and didn't), and stay as calm as Edit beside them, as on the feed page; with one-click actions and no undo, the red is the only warning before the click.
  Only the final step of a confirmation, "Delete my account", is a filled red button (`.btn-danger`).
  A page's delete action sits at its foot, below a divider (`.danger-zone`).
- **Page heads end in the logo's nameplate rule** (#414), a thick ink line over a thin one.
  The timeline's tab head keeps a plain divider, since the active tab's underline sits on it.
- **No back links in page heads** (#423, closed): the browser's back button and the nav already cover it, and this is a web app, not a native one.
  Back links on every detail page were built and shown at the design checkpoint, and dropped; the article, feed and site pages keep starting with their own heading, without a head.
  Pages that are hard to leave without one (sub-nav pages such as Tags and Timelines) get a better way in rather than a back link.
- **Page-head actions are small outlined pills** (`btn btn-sm`, #424), on every page that has them: they read as controls opposite a big heading, where text links were easy to miss.
  None is filled, since a filled pill is a form's primary action; two actions are grouped (`.head-actions`) so they stay on one line on phones.
- **Bricolage Grotesque for the name and headlines** (the brand, page titles, article titles), self-hosted from `web/shared/fonts` rather than loaded from Google Fonts, so the CSP stays `default-src 'self'` and readers' browsers make no third-party requests.
  It's subset to Latin and weights 600–800 (77 KB, cached for a year).
- **Newsreader for what people read** (#414): article text, card excerpts and feed descriptions, so a grotesque headline sits over serif text like a front page.
  It has optical sizes, so one file is sturdy in a 17 px excerpt and refined in 20 px article text.
  It was picked over Literata on a warm paper background (drifts from the logo's cool palette) and an all-sans option with Instrument Sans and ink outlines on every card (busier).
  Self-hosted like Bricolage, subset to Latin and weights 400–700 (93 KB; the italic, 103 KB, loads only on pages that use it).
  Nav, buttons and forms stay in the system font, so the interface feels native.
- **While the fonts load, text shows in size-matched fallbacks, with `font-display: swap`** (#546, approved at its design checkpoint): adjusted Arial for Bricolage and adjusted Georgia for Newsreader, sized from the fonts' metrics so the swap barely rewraps anything.
  `swap` rather than `optional`, which would sometimes show a first visit without the headline font, or `fallback`, which hides text for a moment for little gain over a matched fallback; with the fonts cached for a year, the swap happens once per device.
  The first frame on Apple devices is adjusted Arial and Georgia rather than system-ui and Charter: closer to the final shape, which the recordings favoured.
  Each fallback face is tuned for its first `local()` font, so Noto Serif (Linux without Georgia) matches less closely.
- **Fonts are preloaded only where a page shows them** (#546): Bricolage on every page, upright Newsreader on pages with reading text on their first screen, never the italic, which only some articles use.
- **A change to how the app looks is shown and agreed before its PR is ready for review** (#309, for #299 and #308; widened from open layout questions to every visual change in #425): a draft PR with the option or options behind one switch, screenshots at desktop and phone widths (committed under `.screenshots/` while the PR is a draft), and a stop until the owner picks one in a PR comment.
  Every option comes with a justification (what it fixes, why this way, and what it trades away), so the pick rests on reasons that can be recorded here rather than on taste alone.
  One option is enough when it's clearly right, but it's still shown and justified.
  An issue can say a change needs no checkpoint: copy changes, and changes that reuse an existing pattern exactly (#419, #420).
  Each control must work on its own and next to the others planned for the same place, since not all of them may ship.
- **Choices about how the app looks or reads are recorded here only once the owner has approved them in the implementation** (#425), at the design checkpoint or, without one, in the PR's review, since seeing the result can change the owner's mind.
  Until then the issue holds them as the current thinking, and the implementation PR adds them to this file.

### Motion and feedback

Planned in #507 (issues #508–#525), from a review of where progressive enhancement and subtle motion would make the app nicer to use.
Each piece's look (its timing, curve and effect) is recorded here once the owner approves it in its implementation (above); these are the rules all of them follow.

- **Motion is an enhancement the reader opts into.** It's written inside `@media (prefers-reduced-motion: no-preference)`, so a forgotten guard means no motion rather than unwanted motion.
  With reduced motion nothing moves, scales or slides, and view transitions are off; color and opacity changes of up to 150 ms stay, since they aren't motion (WCAG 2.3.3).
  The reorder tint (#481) already followed this.
- **The feel is quick and soft, like a sheet of paper sliding into place** (#508): things answer a tap at once and slow down as they arrive; nothing bounces except starring and, slightly, the tab underline (#521); motion is felt more than watched.
  The values are tokens on `:root` in `main.css`, so a value tuned there changes everywhere; later issues use them by name.
- **Four curves** (#508): `--ease-out` for anything appearing or answering the reader (most motion), since it starts at full speed and a slow start reads as lag on a press; `--ease-in` for things leaving, so the eye isn't held on them; `--ease-in-out` for moving from one place on screen to another, since both ends are on screen; `--ease-spring` (one overshoot of about 16%) only for the star, the one celebratory moment.
- **Five durations and a busy delay** (#508): `--duration-press` for pressing in, `--duration-quick` for hover and color changes, everything leaving and the crossfade between pages (#513), `--duration-base` for small things appearing or changing state, `--duration-move` for moving across the screen, `--duration-flourish` for the rare celebratory moments, and `--delay-busy` before a busy indicator.
  Exits take about two-thirds of their entrance.
  Only a highlight fading away (the reorder tint) and the sunrise (#523) run longer, each with a comment where it's set.
- **Small distances and cheap properties** (#508): travel 3–8 px, presses scale to 0.97–0.99 and only the star goes past 1.
  Only `opacity`, `transform`, `clip-path` and SVG `stroke-dashoffset` animate, which stay smooth on phones; never layout (except the timeline form's disclosures, #520), and never blur or shadows on large areas.
- **Presses act only while pressed** (`:active`), so a target never shrinks while it's being aimed at; the focus ring isn't animated (#508).
- **Press states** (#510): the browser's tap highlight is off, and every control has its own press.
  Buttons scale to 0.97, icon buttons take the hover background and their icon scales to 0.9, rows take their hover background, and the phone tab bar and timeline tabs take `--text` (the tab bar icon scales to 0.9).
  An article card firms its border and scales to 0.99 only while its own title link is pressed, and is never tinted, since the tint is the read marker.
  Presses go in over `--duration-press` and out over `--duration-quick`; colors apply under reduced motion, scales don't.
- **Starring and saving** (#512): while the request runs the button is `aria-disabled` and dims to 0.6 only after `--delay-busy`.
  Once a `204` turns Star on, the filled star scales from 0.4 on `--ease-spring` over `--duration-flourish` (to about 1.1, then 1), with a burst of five dots in `--brand-strong` at its points that travel 4 px outward, fading and shrinking, over 400 ms on `--ease-out` from 60 ms in.
  Turning Save on drops the bookmark 3 px into place over `--duration-base` on `--ease-out`, with no overshoot or burst, since saving is frequent.
  Turning either off doesn't animate, so only the reader's own change is marked.
- **Live search while it's working** (#518, approved at its design checkpoint; this departs from the issue's spinner at the end of the box): while a request is in flight the results are `aria-busy`, and once it has taken `--delay-busy` the box's search icon fades into a small spinner in `--text-3` and the stale results dim to 0.6, both over `--duration-quick`.
  The spinner takes the icon's place rather than sitting at the end of the box, because the clear button is a control and stays at the edge where readers expect it, while the spinner is passing status; at the end it pushed the clear button in from the edge, and Firefox, which has no clear button, put them in a different order.
  Both go at once when the new results arrive, the request fails or the box is emptied, so new results never fade in; a keystroke that aborts a request for a new one keeps them on without restarting the delay, and nothing new is announced.
- **Slow forms show they're working** (#509, approved at its design checkpoint, #538): Look up, Check for new feeds, Refresh, Import, Log in and Sign up still post normally, but on submit the button is `aria-disabled`, takes a busy label ("Looking up {site}…", "Checking for new feeds…", "Refreshing…", "Importing…", "Logging in…", "Signing up…") and ignores further presses, with a `progress` cursor and no hover change.
  It keeps its width where it can, so the row doesn't jump; once the post has taken `--delay-busy` a 1em spinner ring in `currentColor` fades in before the label over `--duration-quick` and the label is announced, since a changed label isn't read out by itself.
  On Refresh and Check for new feeds the spinner takes the refresh icon's place, since two rings side by side read as a glitch; a page restored from the back/forward cache gets its buttons back.
- **Share's copy feedback** (#511; the owner's pick at its design checkpoint, #533, from four options): where there's no share sheet, a pill in the inverse colors appears 6 px above the button, over whatever is there, so nothing in the row moves, and on success the check draws itself in place of the share icon.
  It says "Link copied", not "Copied", since the button says Share and the words say what it did; a failure says "Couldn't copy" and keeps the share icon.
  It fades in and rises 4 px over `--duration-base` on `--ease-out` and fades out over `--duration-quick` on `--ease-in`, and under reduced motion only fades; it has no arrow, so it reads as the button answering rather than a hover tooltip.
  A success stays 2 s, and longer while a mouse is over the button or it has keyboard focus (until the next key); a failure stays until the reader's next press or key, since a message that times out before it's read fails WCAG 2.2.1 (why toasts aren't taken, below).
  Only the latest copy shows (owner's call in #533): the clipboard holds one link, so copying another article hides the previous pill at once, rather than leaving it saying "Link copied" over a link that no longer is.
  Every press gets a visible answer: a copy repeated while its pill shows replays the rise and the check without hiding the pill (under reduced motion its opacity dips to 0.4 and back over `--duration-quick`) and starts its 2 s over, since a reader who presses again usually wasn't sure the first press worked.
  Not taken: status text at the row's open end (on phones it lands on "Also in", and it left the screen on the article page), and the button turning into a labelled, tinted "Copied" (it widens about 60 px, so Star jumps aside and at 320 px "Also in" is squeezed to a word a line).
- **Page to page** (#513): following a link or a form post that redirects crossfades into the next page with the browser's own cross-document view transition, over `--duration-quick` on `--ease-out`.
  The browser's crossfade blends the two pages, so whatever is the same on both (the nav, the head's rule, card frames) holds still and only what differs fades; separate fade-out and fade-in animations, as first planned, left both pages half visible for about 70 ms, putting text on text and dimming the parts that don't change.
  Nothing else has a `view-transition-name` (the tab underline does, #521, below): a named element is drawn from a snapshot during the transition, and the phone tab bar's frosted glass doesn't match its snapshot, so it jumped at the start and end; without names the current section's highlight crossfades with the page instead of switching at once.
  Back and Forward cut: Safari animates its own swipe-back (iOS, and macOS with a trackpad), and a page can't tell a swipe from the Back button on a cross-document navigation, so a crossfade could play after the browser's animation; an instant Back also suits a page restored with its scroll position.
  The skip is in the deferred `app.js` (`pageswap` on the page being left), not a render-blocking script: if it ever misses, Back gets a 150 ms crossfade, which isn't worth blocking every page's first render for.
- **Tab to tab** (#521): the current tab's underline is its own element, named for view transitions, so between two tab pages it moves from the old tab to the new one while the page crossfades, telling the reader where they moved along the row.
  It moves like something elastic, not a rigid bar: the edge in front leaves at once on a stiff spring and the edge behind follows on a softer one, so the line reaches toward the new tab (about 1.4× its length between neighbours, topping out near 1.9× across the row, where the back edge follows more closely so long moves don't over-stretch), then the back edge catches up; the front edge overshoots by at most 6 px and springs back, and the line thins as it stretches, keeping its area, so the stretch reads as speed.
  Springs reach every distance in about the same time, growing a little with distance: a neighbour is in place in about 175 ms and a tab across the row in about 240 ms, settled by 200–320 ms.
  Slowing far moves further was tried and not taken: a third more time barely lowered their peak speed, since the speed is in the stretch, and lengthened the transition; there's no wind-up before it leaves, since a slow start reads as lag on a tap.
  Only the page that's arriving can shape the move (a CSS curve moves both edges together, and the old page's `view-transition-class` doesn't carry over), so the springs are in the deferred `app.js`, in its `pagereveal` listener.
  No script blocks rendering for it: one loaded without `defer` cost about 16 ms of first paint on a throttled phone, on every timeline page view, while the deferred `app.js` is listening in time on a typical device but misses about two in three on a 6× throttled CPU.
  When it misses, `main.css`'s move plays: the line slides on `--ease-out` over `--duration-move` and stretches about its middle as it leaves (1.35×, thinning), squashes a little as it lands (0.95×) and settles, so it still feels alive, just without the reach.
  The group animates `width` and `height` as well as `transform`, which is cheap here: it's a 2 px line drawn from a snapshot above the page.
  The row keeps where the reader scrolled it from one tab page to the next, so its labels don't jump during the crossfade and the line slides along a row that stays put; it's only scrolled when the new tab isn't in view, now just far enough to show it clear of the fade rather than centered, since that's what CSS can do before the first render.
  That first scroll is CSS (`scroll-initial-target`), not `app.js`: when the deferred script ran after the transition had captured the row, the row was still at its start, so the line flew toward a tab hidden past the Edit pill and the labels smeared between two scroll positions.
  A late `app.js` leaves the row where CSS put it rather than restore the reader's scroll under them; on a slow device a far switch can then crossfade the labels between two scroll positions, but the line always moves between tabs that are in view.
  An underline scrolled out of the row on the page being left isn't named, since its snapshot isn't clipped by the row and would slide in from outside it; the new underline fades in with the page, as it does to and from a page without the row.
  Tapping the current tab moves nothing, and at rest the underline looks as before; in forced colors it's a border, so only the current tab is underlined (the old transparent borders on every tab were forced visible).
- **The sunrise** (#523, approved at its design checkpoint): on the landing page and Log in, once per load, the logo's sun rises into place from 7 units lower (of the 64-unit drawing, so its top just shows) over 900 ms on `--ease-out`, after a 200 ms pause so the page has settled.
  It's clipped to the sky, so it never paints over the water.
  The sky brightens with it, a deeper blue fading to the logo's own over the same time, so it reads as dawn rather than a shape sliding up; the deeper blue is an overlay whose opacity fades, so only opacity and transform animate and `main.css` names no color.
  The logo is drawn inline on those two pages only (the rail and the other logged-out pages keep the `<img>`), and it's still under reduced motion and in forced colors.
- **Reordering** (#519, approved at its design checkpoint; this departs from the issue's view transition and eased curves, and widens it to dragging): on `/timelines`, rows glide to their places as an animation of the real rows with the Web Animations API, not a view transition.
  A view transition draws snapshots over the page while it runs, so a click during a glide would land on the transition rather than a button, the focus ring would be frozen in the snapshot, and a press mid-glide could only skip to the end; real rows keep focus and take every press.
  Every row moves on a critically damped spring (ω 28 rad/s: a row's height in about 260 ms), sampled into keyframes like the tab underline's (#521), not on the curve tokens: a curve restarts from rest, so five quick presses made the row stop and lurch at each one, and a curve fast enough to answer a press (`--ease-out`) moved a sliding row a quarter of its way in the first frame, which read as a jump.
  A spring from rest eases in and out; interrupted, it carries on from where the row is at the speed it has, so nothing restarts or stops; it never overshoots, so rows don't bounce into each other, and it starts as of the current frame, since a pending start held the row still for a frame.
  A move button's row and its neighbour trade places, the moved row drawn above, and the page scrolls smoothly to keep the moved row clear of the sticky bars and the phone tab bar (moving down never scrolled before, since the pressed button kept its focus, so on a phone the row went under the tab bar).
  Dragging is direct: the row follows the pointer with no animation, so it never lags, and lifts with a shadow that fades in over `--duration-quick` (opacity only, on a pseudo-element); the rows it passes slide aside on the spring once its edge crosses their midpoints, and slide back if it changes its mind, keeping their speed.
  The order changes only on the drop (moving rows in the document under the pointer made them jump a whole place at a time), and the row settles into its gap carrying the pointer's speed as its shadow fades; Escape, a cancelled pointer or leaving the window glide every row back.
  Held near the top or bottom of the visible page (below the sticky bars, above the phone tab bar) a drag scrolls the page, faster nearer the edge, and only towards rows out of sight, so a phone can move a row the whole length of the list.
  Under reduced motion nothing glides or slides aside, but the dragged row still follows the pointer, since that's the reader's own movement, not an animation; in forced colors an outline replaces the shadow.
- **Under reduced motion the spinner pulses instead of freezing** (#508): a frozen ring no longer says "working", and a slow opacity pulse moves nothing and is far below the flashing threshold.
- **The state changes first, and motion is drawn on top.** `aria-pressed`, text and `hidden` change at once; nothing waits for an animation before updating, moving focus or taking the next press.
- **Motion never says anything on its own**: what it shows is also in the markup, the visible text or the live region.
  Nothing flashes more than three times a second (WCAG 2.3.1), only progress spinners loop, no animation moves focus, and decoration drops out in forced-colors mode.
- **Delight only on rare or meaningful moments**: starring, first seeing "You were here", the sunrise on logged-out pages.
  What readers do many times a visit (opening articles, saving, pressing buttons) gets quick feedback only, so the effects don't wear out.
- **Busy indicators wait about 300 ms**, so fast responses never flicker; the control stops taking presses at once, with `aria-disabled` rather than `disabled`, so focus stays on it.
- **A design checkpoint for motion includes short recordings** (desktop and phone, with and without reduced motion), since screenshots can't show it; this adds to the checkpoint rule above.
- **No backend changes in this batch**, so it's all quick wins.
  Making navigation itself faster (prefetching, the back-forward cache) needs server work, so it's planned separately ("Instant navigation" in backlog.md).
- **Two issues are experiments** (#524, the card growing into the article page, and #525, images fading in): built to be seen, then kept or closed as not planned.
- **No framework for page transitions** (Turbo, htmx): cross-document view transitions are CSS, with `app.js` only skipping them on Back and Forward (#513) and shaping the tab underline's move when it's in time (#521), and every page keeps working without JavaScript.
- **Not taken**, each for its reason:
  - Toasts that dismiss themselves: easy to miss, and a time limit on reading them fails WCAG 2.2.1; flash messages stay until the next page.
  - An "all caught up" ending, a "new articles" pill or pull-to-refresh: they make the timeline an inbox ("Reading"), and #394 already turned down pull-to-refresh ("Mobile").
  - A tab bar that hides as you scroll: it moves on every scroll and makes the nav something to look for.
  - Cards that lift on hover: the firmer border is the hover (#421), and cards stay quiet previews.
  - Crossfading live search results: it makes typing feel slow.
  - An animated theme switch, for something people do once.
  - Skeleton screens: server-rendered pages arrive whole, so a skeleton only adds a flash.
  - Smooth scrolling everywhere: it fights Back's anchored position and can make people queasy.
  - A reading-progress bar: it moves on every scroll, and the scrollbar already shows how far you are.
  - "You were here" animated by scroll position: tied to scrolling, it replays back and forth and wears out, so it plays once instead (#522).

## Mobile

Planned in #395, from a discussion of what would stop people switching to the app; the first issue is #394.

- **Mobile comes in three steps, each only when the one before it isn't enough.**
  First the web app made installable (#394); then, shortly after launch if the app should be in the App Store, a native shell around the server-rendered pages (Hotwire Native or Capacitor), adding native screens only where they pay off; a fully native app only if offline reading or performance decides it, built screen by screen inside the shell rather than as a rewrite.
  A fully native app needs its own JSON API and token login, and every feature built twice, which a small team can't keep up with; the server-rendered pages already work without JavaScript, which is what a shell needs.
- **Installable, not offline** (#394): a manifest and icons, no service worker.
  Chrome no longer needs one to offer installing, and iOS never did; offline reading is a backlog item.
- **No install button or banner of our own** (#394).
  `beforeinstallprompt` is Chromium-only, and asking to install is the kind of nagging the app avoids; people use the browser's own install.
- **The manifest is served by its own handler at a fixed URL** (`/manifest.webmanifest`), not from `/static` (#394).
  Go's `mime` table has no `.webmanifest` type and the distroless image has no `/etc/mime.types`, so the file server would serve it as `text/plain`.
- **`start_url`, `scope` and `id` are `/`, with `display: standalone`** (#394).
  `/` is the timeline when logged in and the landing page when not; a fixed `id` keeps the installed app the same app if `start_url` changes.
- **No pull-to-refresh of our own** (#394).
  Tapping the current tab loads its page again, which is how new articles show up, since the timeline doesn't update live ("Reading").
- **The app is installed on its final domain only**: an installed app belongs to the origin it was installed from, so #394 waits for the domain move (#389).
- **One install icon on every platform: the logo on tomato, lit softly from the top-left corner** (#445).
  iOS ignores the manifest's icons, so `apple-touch-icon.png` is rendered from `app-icon.svg` like the others; it used to be the favicon's page on white, which a tester found dull next to other apps.
  The highlight is a radial gradient about 8% lighter than tomato, fading to flat by the middle, and touches only the background so the logo stays flat with its ink outlines; a 4% version and flat tomato were the alternatives.
  The favicon keeps the page on white, which reads better at 16 px.
- **The manifest repeats the light theme's background color** (`theme_color`, `background_color`), since it can't read CSS custom properties; the pages add a dark `theme-color` (#394, #413).
- **No third-party client API at launch** (Google Reader API, Fever), so apps such as Reeder or NetNewsWire can't read from the app.
  People reading through them would get an unread-count inbox, not the timeline, timelines or cards that make the app different, and the API costs client-specific bugs, support and a second interface to secure.
  Revisit if trial users or cancellations keep asking for it, or if the app's own mobile experience can't get good enough; the small Fever API would be the first step, possibly on a paid plan.

## Frontend assets

Planned with #540–#547, from a review of how the app serves its assets: the fonts were downloaded again on every page view, and the JavaScript and CSS shipped unminified.

- **Every asset URL carries a hash of its content and is cached for a year (`immutable`)**, the fonts included (#540): the fonts' URLs in `main.css` had no version, so browsers couldn't cache them.
  A URL without the current hash gets `no-cache` and an `ETag`, so neither browsers nor the edge keep a copy they can't revalidate.
  Built files carry the hash in their names; files that need a fixed URL (the install icons the web app manifest names) keep `?v=` (#543).
- **An outdated hash of an entry or stylesheet still gets the current file, uncached** (#543): Render runs the old and new versions side by side during a deploy, and a page rendered by the old one must keep its styles.
  An outdated chunk is a 404 (#547): a page loaded in that window loses only its enhancements, and works without them.
- **Render's edge cache, not a CDN of our own** (#541): Render already puts Cloudflare in front of the service and caches "Common static files" at no extra charge.
  Proxying our own Cloudflare records would add an `X-Forwarded-For` hop ("Client IP" under "Deployment"), and a separate assets host would cost every first visit a DNS lookup and a TLS handshake that the page's own HTTP/2 connection doesn't.
  Not "All files": logged-out pages send no `Cache-Control`, so the edge would keep them for 2 hours.
- **HTTP/2, HTTP/3 and compression are the edge's job**: the app doesn't compress responses or speak HTTP/2 itself, and nothing is precompressed; production serves HTTP/3 as well as HTTP/2 (#541).
- **A build step** (#543; this reverses #50's and #163's "one plain file, no build step" under "Overall"): bundling and minifying halves the JavaScript and CSS (with Brotli, `app.js` from 16.8 to about 8.3 KB and `main.css` from 15.8 to about 9.2 KB), and lets the code become tested TypeScript modules that a second site can share.
  - **One npm project in `web/`** for every site (the reader now, an admin site later), with what they share in `web/shared`: not at the repository root, which is the Go module's, and not inside one site's Go package.
    Its output is embedded by a Go package in `web/` that each site's server imports.
  - **The admin is a second build, from a second config** (#562): each server reads its own manifest (`internal/assets`), and a Vite build writes one, so `web/vite.admin.config.ts` builds `web/admin` into `web/dist/admin`, both configs made by one function in `vite.config.ts`, and `npm run build` runs both.
    Not one build with both sites' entries: its one manifest would mix the sites' files, and each binary would embed the other's.
    The admin build copies the reader's public files, for the logo and favicons, rather than keeping copies of them.
  - **Vite, as a build tool only**: its manifest tells Go which files, stylesheets and chunks each entry needs, and Vitest shares its config.
    Not its dev server: Go renders every page, and the dev client would need CSP exceptions and a development-only template path.
    Not esbuild on its own (a second tool next to Vitest, and no manifest of that kind), and no PostCSS (Vite bundles and minifies CSS without it).
  - **A failed build fails the build, not the server**: `make`, the Dockerfile and CI build the assets before Go compiles, and `web/dist` is gitignored with no placeholder, so a missing build is a compile error.
    Building in the server at startup with esbuild's Go library was considered and not taken: a broken build would only show when the server started, and TypeScript wants a real build step anyway.
  - **Node only at build time**: the image stays distroless, and the Node version is pinned in `web/.node-version`.
  - **Stylesheets write standard properties, and the build adds the vendor prefixes** (#543): with the source's `backdrop-filter` and `-webkit-backdrop-filter` both set to `var(--blur)`, Vite's minifier (Lightning CSS) kept only the prefixed one, which Firefox ignores.
    A prefixed property belongs in the source only where there's no standard one (`-webkit-tap-highlight-color`); `TestBuiltCSSKeepsStandardProperties` catches a standard property the build drops.
- **TypeScript (`strict`) and Prettier** (#544): `tsc --noEmit` checks types and Prettier checks formatting in CI, like gofmt; no linter beyond `tsc`.
- **One module per enhancement** (#544), each still hooked onto server-rendered markup, so pages work without JavaScript and nothing is inlined (the CSP).
  Earlier entries here name `app.js` and `main.css`; after #544 their code lives in modules and partials under `web/app` and `web/shared`.
- **Vitest with happy-dom** (#545), tests next to their modules; where JavaScript mirrors Go (the timeline form's summary), a fixture the Go tests generate keeps both in step, with Go as the spec.
- **Bundles by page group, not by feature** (#547): a few entries (reading pages, Search, Timelines, account forms), each template naming its group once, with stylesheets, `modulepreload`s and the module script written into the head from Vite's manifest.
  Not `import()` when a feature's markup turns up: enhancements would arrive a round trip late, buttons revealed by script would shift the layout, and the deferred `pagereveal` listener (#521) would miss more often.
  - **A group is a sheet, an entry or both, by name, and an entry can import another group's sheet** (#547): a page that loads no script stays without one while sharing the stylesheet of pages that do (Settings pages without password fields, `settings`; those with them, `account`), and Vite links the imported sheet as the same file, so it's cached once.
    Since #565 every page loads a script, as every page sends the real-user beacon ("Observability"): `settings` and `solo` have entries that set up only that, and the groups stay as they were.
  - **One sheet for Settings, My feeds, tags and Timelines** (#547; planning had `settings.css` and `timelines.css`): their pages share most partials (the timeline form's choices are Settings > Reading's too, and its tag checklist a tag page's), so two sheets would be nearly the same file.
    The reading pages, where readers spend their time, keep a sheet of their own.
  - **The partials keep one order, and every sheet imports them in it, with `press.css` last** (#547): `base.css` followed by any one sheet is then the cascade one stylesheet of every partial would be, for the rules that match the page, so splitting the CSS can't change how a page looks.
    A partial is split only where its sections meet, and a section a page needs goes in that page's sheet, even if it's mostly another area's (the nav's `.debug-badge` puts `debug.css` in every logged-in sheet).
  - **Chunks get their own folder, `assets/chunks/`** (#547), so an outdated chunk's name can't be taken for an outdated entry's, which gets the current file.
  - **The Newsreader preload stays a page's own include, `{{template "reading-font"}}`, not part of its group** (#547; planning had it in the `reading` group's partial): the pages with reading text on their first screen aren't a group (the timeline form has it, the debug inspectors don't), and a preload a page doesn't use costs a download and a console warning.
- **No lazy-loaded or inlined CSS**: both need inline code the CSP blocks, lazy CSS flashes unstyled content, and inlining defeats caching; a small, cached stylesheet that blocks rendering is the better trade.
- **No prefetching of assets, on load or on intent**: the article page shares the reading pages' bundle, so nothing article-specific is left to fetch, and other pages' bundles are small; prefetching the next page itself belongs to "Instant navigation" in backlog.md.
- **The first card image on a list's first page isn't lazy** (#542) and gets `fetchpriority="high"`: it's usually the largest thing on a phone's first screen, and a lazy image only starts after layout; the rest stay lazy.
- **Font preloads use the build's URLs** (#546), or the font downloads twice; how text looks while the fonts load is under "Design".
- **Not now**, each for its reason:
  - A service worker: the HTTP cache with hashed, immutable URLs already does its caching, and offline reading is its own backlog item.
  - 103 Early Hints: they only help first visits to slow pages, and Render's support is unconfirmed.
  - Subsetting the fonts further: they're Latin-only already, with only the axes the CSS uses.

## Security

Planned in #187 (issues #182–#186).

- **In-memory rate limits** (`internal/ratelimit`) are enough for one instance; they reset on restart.
  Whether to keep them long term is a backlog item.
- **IPv6 clients are keyed by their /64**, since one client usually controls a whole /64.
- **A limited login or signup re-renders its form with 429** and `Retry-After`, keeping the email filled in.
  The generic 429 page is only for lookups.
- **Failed logins are counted per submitted email** (#183), to slow password guessing against one account.
  Successful logins don't count or reset it.
  Unknown emails were counted too, so that a 429 didn't reveal which emails have accounts; "Accounts" dropped that goal, and from #262 only wrong passwords count.
- **Signup, login and reset say which emails have accounts** (see "Accounts"). #185 had made a duplicate signup look exactly like a successful one, and login verify a dummy hash for unknown emails; #262 removes both.
- **Hashing beyond the cap waits in line rather than failing** (see "Queued password hashing" in the Performance watchlist).
- **Signup is off unless an invite code is set.** (#186 started with a `-signup` flag, turned on briefly to make accounts; #226 replaced it with the code below.)
- **A shared invite code replaces `-signup`** (#226).
  Signup is open exactly when `-invite-code` is set; the bool flag goes rather than staying alongside it.
  - The code arrives pre-filled from `/signup?invite=…` into a visible field, so it can also be typed.
    While signup needs an invite code, no page links to `/signup`: without a code it's useless, and advertising it only invites guessing, which the per-IP signup limit (#182) bounds.
    Whatever opens signup more widely adds the links back.
  - At least 16 characters, compared in constant time.
  - In production it's set in Render's dashboard and ignored by Terraform, like `RSS_SMTP_PASSWORD`: a Terraform variable would keep it in plain text in the state and need a PR and an approved apply to rotate.
  - It may appear in request logs (they log the URI); only the owner reads them, and the owner sets it.
- **`www create-user` makes accounts locally** (#225), and `make db/reset` uses it to put your account back.
  Not seeding in `www migrate`: that manages the schema and runs before every deploy.
  The password comes from stdin, never a flag (shell history, process listings), and there's no hidden prompt, which would need a new dependency.
  It can't run in production (distroless image, no external database access); production accounts come from the invite code.
- **Sessions: see "Accounts"** (#258). #248 kept them to 30 days from login, with no idle timeout, until they could be revoked. scs's own idle timeout writes the session row on every page view, which is why #258 tracks `last_seen_at` itself, at most once a day.
- **Malformed form bodies get 400 and bodies over 64 KiB get 413** (#249), as HTML error pages, not logged as server errors.
  The cap is applied in `decodePostForm`, so only routes that decode a form are capped; a future upload sets its own limit.
- **One canonical host** (#390): in production, a request for any host but `-base-url`'s gets a 308 to the same path there, for every method (308 keeps the method, so feed readers polling an old shared-feed link follow it).
  `/healthcheck` is served on every host, for Render's health checks.
  Sessions are per host, so serving several splits them.
- **HSTS: `max-age=31536000`, in production, on the canonical host's responses only** (#390).
  No `includeSubDomains` or preload: other hosts on the domain may not be HTTPS, and preload is hard to undo.
  Redirects from other hosts don't send it, so the old `rss.georgerodier.com` is never pinned.
  It waited for "the domain to be settled"; #389 settles it.

## Accounts

Planned in #270 (issues #254–#269).
In these flows, trade-offs favor the user's experience.

- **Helpful errors, not hiding which emails have accounts.** Signup says when an email already has an account, login says whether the email or the password is wrong, and password reset says when there's no account (#262, #265).
  What this reveals, that someone uses an RSS reader, isn't sensitive, and the commonest failures (forgetting you signed up, using another of your addresses, a typo) get a next step instead of a wait for an email that never comes.
  All three say the same, since hiding it in one place protects nothing if another reveals it.
  Per-IP limits keep bulk checking slow.
  This reverses #185.
- **Soft email confirmation** (#264).
  Signup logs you in at once (#262), and a quiet notice asks you to confirm until you do; a password reset also confirms.
  Confirming first was rejected: it puts a trip to the inbox before first use, a slow or filtered email strands you, and on phones the link opens in the mail app's own browser.
  A confirmed address is required only for:
  - changing your email: the new address takes effect once confirmed (#266);
  - later, email that isn't about your account (digests, notifications);
  - later, anything that reaches other people (invites by email, sharing, public profiles);
  - later, linking another sign-in method by matching email, which otherwise lets someone who signed up with your address first keep a way in.
    A reset already covers that case: it replaces the password and logs out every other session.
- **Email links open a page with a button, and the POST does the work** (confirmation, email change, and the reset form), so GET handlers still don't write and mail scanners that open links can't act for the recipient.
  Tokens are 32 random bytes, and only their SHA-256 is stored.
  They're single use; asking again doesn't cancel earlier links (emails arrive out of order), and using one deletes the rest of its kind.
  Confirmation links last 7 days, email change 24 hours, reset 1 hour.
  Confirmation and reset links only work while the account still has the address they were sent to.
  Pages with a token in the URL send `Referrer-Policy: no-referrer`.
- **Confirming on a device where you aren't logged in says it's done** (#444): a page saying the address is confirmed, with a way on, rather than the login page with a flash, which read as if logging in were part of confirming.
  It never logs anyone in, and being logged in as another account gets the same page rather than Home with a flash about an address that isn't theirs.
  The page is its own route (`/confirm-email/done`, static) so a reload doesn't re-post a used token.
  It never logs anyone in: a confirmation link isn't a login link.
- **The email is how you log in and how we reach you, and it's never shown to other people.** You're shown by your name and a letter avatar.
  No usernames until something public or social needs one: they bring "taken" errors at signup, squatting, and broken links on renames.
- **No credentials table yet.** The password hash stays on `users`.
  Passkeys (a backlog item) add their own table, one user to many passkeys, and make `hashed_password` nullable.
  Revisit before production if Google or Apple sign-in is planned.
- **The name stays required at signup** (#261): it's what we call you in emails.
  One field, what you'd like to be called, with no first/last split (which fails for many names); at most 100 characters, trimmed.
- **Avatar: the first letter of your name on a tint chosen by user ID**, like site avatars.
  No uploads: only you see it, and storing and resizing untrusted images costs more than it gives.
  No Gravatar: it sends a hash of your email to a third party.
- **Settings is a home page of categories, each its own page, like a phone's settings** (#261, changed in #446).
  The home's first row is the account itself (avatar, name and email), then Reading, Your data and Help, each with a one-line summary of what's inside, then Log out.
  #261 had one page of sections; a tester found it cramped and hard to find their way around, and the backlog plans more categories (plans and billing, passkeys, email, a device list) than fit on one page.
  Tabs (Account, Reading, Data, Help) were the alternative: one tap between categories, but a one-word label says less than a summary, no more than four or five fit at 320 px, and a tab row already means switching timelines on Home.
  A tidied single page was the other, and would keep growing.
  The cost is one more tap to reach a setting, which matters little on a page people visit rarely; the nav's account entry leads back to the home from anywhere in Settings.
  On a category page, a setting that needs its own form (name, email, password) is a row opening a small page with one form, and settings that are choices (the theme, later text size and typeface) are one form on the category page itself, so the commonest changes cost no extra tap.
  A setting that acts at once (logging out other devices, debug mode) is a row with its button at the end, so every list ends the same way.
  Delete account sits at the foot of Account, where people look for it first; debug mode is under Help, for allowed accounts only.
  The nav's Logout button becomes an account entry (the avatar, labelled Settings), and Log out moves to Settings: with sessions this long, a prominent Logout mostly invites accidental logouts.
  Preferences are typed columns on `users`, not a key-value table: the first is `theme` (#413).
- **A `user_sessions` row per logged-in device** (#258), its ID kept in the scs session.
  You stay logged in while you visit at least every 90 days, for at most a year (scs's lifetime; browsers cap cookies at 400 days).
  `last_seen_at` is written at most once a day, not on every page view.
  No "Remember me" checkbox: it asks a question most people ignore, and logs out everyone who didn't tick it.
  Logout deletes this device's row and only that one; changing or resetting your password deletes the others, and so does logging out other devices in Settings (#263).
  A list of devices comes later.
- **No password confirmation field** (#257).
  A show/hide toggle (JavaScript) catches typos instead, and reset covers the rest.
- **Passwords: 8 to 256 characters, not a common password, not your email** (#256), normalized to NFKC.
  No composition rules or expiry.
  NIST's 15-character minimum for password-only login was considered; the common-password list gets most of its benefit with less friction.
  Checking Have I Been Pwned is a backlog item.
- **Re-enter your password to change your email or password, or to delete your account** (#263, #266, #268).
  No "sudo mode" window that skips it for a while.
- **Account deletion is immediate** (#268), after your password, with no grace period: it's rare and deliberate, and a pending state adds a scheduled job and doubt about whether the data is gone.
  Feeds and articles stay, since they're shared.
  Backups keep the data up to 35 days, which the privacy policy says.
- **Export your subscriptions as OPML 2.0, downloaded from Settings** (#270, #267): one `<outline>` per feed, with no nesting by site, since most readers turn nested outlines into folders and would make one per site.
  Gone feeds are included: they're still your subscriptions.
  Changed in #354: feeds are nested in a folder per tag unless the user chooses a flat list (see "Filtering and timelines"), and the list stays flat for someone without tags.
  Import: see "OPML import" under Subscriptions.
- **Security emails:** password changed, email changed (to the old address), and account deleted.
  No "new login" alerts: they're noisy, and people learn to ignore them.
- **Email goes over SMTP behind `mail.Sender`** (#259), so the provider changes by configuration: Resend's free tier while testing, Amazon SES later (a backlog item).
  Without an SMTP host, emails are logged instead, in development only; production refuses to start without one, since emails carry one-time links.
  Links use `-base-url`, never the request's `Host` header.
- **An outbox table, sent by a background worker** (#260), so pages don't wait on the provider and failures retry.
  The handler that queues an email wakes its own worker, so it goes out in about a second.
  Bodies are cleared once sent.
  About 20 emails to one address an hour: a backstop under each flow's own limit, so nobody can use the app to flood an inbox.
  It's a count before the insert, so simultaneous requests can slightly exceed it (#260); a backstop doesn't need a per-address lock.
  On shutdown the mail runner finishes the send in flight instead of aborting it (#260): an aborted send can still be delivered, then sent again on the retry, and it delays the email by the lease.
- **`rss.georgerodier.com` served the app and sent its email** (#254) until the move to the app's own domain (#389).
  A subdomain keeps the email provider's records away from the personal mail on `georgerodier.com`.
  Its DNS record is DNS-only, since a proxied one would add a third `X-Forwarded-For` hop.
  DMARC starts at `p=none`, with reports.
  The SMTP password is set in Render's dashboard and ignored by Terraform, like the invite code.
- **The app moves to its own domain before testers arrive** (#389), `paperround.app`, registered in Cloudflare, while there are no links or sessions to lose.
  It's served at the apex, with `www` redirecting to it, and sends its email from the apex too: a new domain has no personal mail to keep apart.
  The DNS record stays DNS-only and DMARC starts at `p=none`, for the reasons above.
  `rss.georgerodier.com` stays a custom domain on the service, redirecting to the new one (#390), so old links keep working.
- **Support mail goes to `rss-support@georgerodier.com`, a Google alias** (changed in #254 from `support@rss.georgerodier.com`).
  The app's CNAME on `rss.georgerodier.com` can't share its name with Email Routing's MX records, and Cloudflare's Email Routing would have replaced the Google MX records on `georgerodier.com`.
  The domain is temporary, so the address only has to work until the product has its own.
  #389 keeps it: moving it to the new domain is a separate change (a backlog item).
- **No `_dmarc.rss` record** (#254): mail from `rss.georgerodier.com` fell back to the `p=none` record on `georgerodier.com`, whose reports Cloudflare's DMARC Management reads; it doesn't support subdomains.
  Since #389 the app sends from `paperround.app`, whose own `p=none` record DMARC Management manages.
- **A support address** (`-support-email`) is the Reply-To of every email, and is shown on the reset pages and in Settings (#259, #265, #269).
- **A privacy policy and terms before people we don't know use the app** (#269).
  The app sets only the essential session cookie, so there's no cookie banner; anything else (analytics, third-party scripts) means revisiting that.
- **All the account tables land in one schema change** (#255), so production is reset once rather than four times.

### Waitlist and invites

Planned in #583.

- **A waitlist before signup opens**, so people can join early and be let in in waves, by invite, from the admin (#566, #569, #574).
- **Joining asks for an email, a name and one optional question, "A few sites you'd like to follow"** (#566, approved at its design checkpoint; #583 had planned up to two questions).
  Its answers are used: the sites go on the seed list, so their lookups have run before the person arrives, and checking that discovery finds their feeds before their wave is invited catches failures before a new reader's first search.
  Rejected: "Which feed reader do you use now?", whose answer wouldn't change anything we do; "What do you mostly read?", too vague to act on; and "How many feeds do you follow now?", useful only for planning waves by load, which one more field on the form doesn't justify yet.
  Double opt-in: a confirmation email, so nobody can add someone else's address and invites only go to addresses that work.
  Every waitlist email has a link to leave the list.
- **Joining twice says so**: "already on the waitlist" for a confirmed address, a new confirmation email for an unconfirmed one, and "there's already an account" with a link to log in, as signup does ("Helpful errors" above).
- **Bots meet a honeypot field and a per-IP limit**, not Turnstile yet, which needs a script and a CSP change (backlog, under "Security").
- **The form is a partial on today's logged-out home page**; the landing page is separate work the owner tracks, which places it there.
- **The form sits under the tagline in the landing page's panel** (#566, approved at its design checkpoint), after a divider, with Log in kept as a secondary link above the policy links.
  Rejected: a "Join the waitlist" button opening a separate page, an extra step for the main thing the page asks.
  Its intro only says what happens ("Leave your email and we'll send you an invite when there's a place for you."); #566's opening "Paper Round is opening in waves." was cut at the checkpoint.
- **The tagline is "The sites you follow, in timelines you shape."** (#566, approved at its design checkpoint), replacing "One quiet timeline for the sites you follow.", which several timelines contradict.
  It names the two things the app does that most readers don't: following sites, and timelines you make yourself.
  Rejected: "Follow the sites you love, at your own pace." (calm, but says nothing that sets the app apart) and "A calm reader for the sites you follow." (safe, generic).
- **`/` stays one address for two pages** (#566): the landing page with the form when logged out, the timeline when logged in, as #394 already had it.
  Rejected: a separate `/waitlist` page, which puts a page between a visitor and joining when `/?ref=` already gives each place a link; moving the app to `/app` so `/` is always the landing page, which reverses opening the app in one request and the installed app's `start_url`; and a landing site of its own, a second deploy whose form posts back to the app anyway.
  Login-only pages still send a logged-out visitor to Log in, not the waitlist: someone opening `/saved` without a session is almost always a member whose session ended, and Log in brings them back.
  Log out still goes to Log in too.
- **`/` is never cached** (`Cache-Control: no-store`, #566): the same address is a public page or someone's timeline depending on the session, which a cache can't see.
  It had sent no `Cache-Control` at all, unlike the other logged-in pages, which `requireAuthentication` covers.
- **The form's errors re-render the page that hosts it**, through one function (`renderWaitlistHost`, #566), so the landing page can take the form without changing the join handler.
- **Log in links to the waitlist** (#566): a visitor who reaches Log in without an account would otherwise have no way on.
  Signup itself stays unlinked while it needs a code ("A shared invite code replaces `-signup`" under "Security").
- **One link token per entry, for both its links, that doesn't expire** (#566): the leave link must keep working in later emails (the invite, #574), and joining again while unconfirmed replaces it, so only the newest confirmation link works.
- **Single-use invites replace the shared invite code** (#569): `RSS_INVITE_CODE` goes.
  An invite is bound to a waitlist entry's email (signup fills it in and locks it, and the account starts confirmed, since the address was confirmed when joining) or open, for people the owner invites by hand.
  Invites expire and can be revoked; the token is stored hashed, like email links.
- **Invites go out in waves from the admin** (#574): pick people (checkboxes, or "the oldest N"), review, send.
  No open or click tracking.
- **The email limit is checked before a wave is sent** (#574): emails queued in the last 24 hours and still pending, against the daily and monthly limits (Resend's free tier: 100 a day, 3,000 a month), less a reserve for account emails, so a wave can't use up the day and block a password reset.
  Past 80% of the day it warns; past the limit it says how many fit and offers to send just those.
  Moving to SES changes only the limits (`-mail-daily-limit`, `-mail-monthly-limit`, `-mail-reserve`).

## Deployment

Planned in #218; issues #208–#217.

- **Render, Virginia (US East).** A Starter web service (always on, 512 MB) and the smallest paid Render Postgres 17, about $15 a month with backups.
  Chosen over a VPS (no server to look after), AWS ECS/RDS (about $50–60 a month before there are paying users), Fly.io (its managed Postgres starts at $38) and Neon (the refresh workers poll every 15 seconds, so it never scales to zero).
  Check prices again before the first `terraform apply`.
- **Portable by contract, not by host.** Every host-specific piece sits behind the same six things, so moving to Fly, AWS or a VPS is a new Terraform module, a new deploy step and a DNS change, with no app code changes: one container image built by GitHub Actions and pushed to GHCR; configuration from `RSS_*` environment variables; a `www migrate` command run before new code goes live (Render's pre-deploy command, Fly's `release_command`, an ECS one-off task); `/healthcheck` for the host's health checks; JSON logs on stdout; plain Postgres 17.
  Don't add host-specific code paths.
- **Terraform**, with the official Render provider and the Cloudflare provider (for R2).
  State lives in an R2 bucket.
  After the first apply (by hand, during setup), applies run only from GitHub Actions, one at a time.
- **The domain is Cloudflare DNS plus a Render custom domain, both in Terraform** (#218, #254).
  Which domain is under "Accounts".
- **Migrations are built into the binary** (`www migrate`) and run before each deploy, not at app startup, so they also work with several instances, and a failed migration aborts the deploy while the old version keeps serving.
  One artifact carries the code and the migrations it expects, and the same command runs everywhere.
- **Wiping the database is never automatic.** `migrate` only applies new migrations; nothing in the app or its workflows drops the schema, and only `make db/reset` rebuilds a local database.
  - Until #561, while production was reset on schema changes, `migrate` refused to run when `migrations/*.sql` had changed since they were applied, and wiped only when given the new migrations' fingerprint (a `schema_fingerprint` table, #211); production resets went through a workflow with a typed confirmation.
  - #561 ended production resets (see "Pre-alpha testing"): the fingerprint check, `-allow-reset-to` and the reset workflow went, migration `00002` drops `schema_fingerprint`, and a CI step fails a PR that edits, renames or deletes a migration already on `main`, which replaces the check.
- **No account is seeded after a production reset.** It would need a credential (or its hash) stored in Render, Terraform state or GitHub, and a one-off job to run `www create-user` in.
  Signing up again with the invite link (#226) is the way back in, so production isn't reset before that's live.
  (#214, #215)
- **Images are public on GHCR**, like the source.
  A private image would need a long-lived GitHub token stored at Render.
  Deploys reference images by digest, and CI attests build provenance.
  No secrets at build time.
- **Deploys switch with no downtime.** Render starts the new version, waits for `/healthcheck`, switches traffic, then stops the old one (30-second shutdown delay, matching the app).
  A failed health check or pre-deploy leaves the old version serving.
  Old and new versions overlap for a few seconds: background workers are safe (`SKIP LOCKED`) and in-memory rate limits reset.
  Rollback redeploys an earlier image and doesn't undo migrations.
  No blue-green slots or canaries on Render; Fly or ECS offer them if we move.
- **Client IP:** `X-Forwarded-For`, taking the entry `-proxy-hops` places from the right; the default 0 ignores the header.
  Render's proxy keeps whatever `X-Forwarded-For` the client sent and appends to it, so the leftmost entry can be forged.
  Production sets 2: `onrender.com` traffic passes through Cloudflare and then Render's proxy, and each appends one entry, so the app doesn't need to know either's address.
  This relies on every request going through Cloudflare; one that reached Render's proxy directly could forge its address.
  The first production check (#214) found the second hop: with 1 the app saw Cloudflare's address.
  This replaces trusting a measured proxy range (#212): Render doesn't publish that range and can change it without notice, and a stale range silently made every visitor share one login limit.
  If the host adds a hop inside its network, the picked address is private and the app logs a warning; an added public hop (like Cloudflare) doesn't warn, so the runbook's check in `infra/README.md` is what catches it.
  Host-specific headers (`CF-Connecting-IP`) aren't used: they'd be a host-specific code path.
  (#228)
- **Backups:** Render's point-in-time recovery (3 days on the Hobby workspace) plus a nightly `pg_dump` to R2 from a Render cron job, kept 35 days.
  The R2 copy is at a different provider from the database, and restoring it is also how the database moves to a new host.
- **The backup job is deployed by digest with every app deploy** (#386), from `deploy.yml`, so it runs the image CI built and a rollback rolls it back too.
  Render only pulls a tag when a job is deployed, so a manual deploy was easy to forget, and the backup image's `pg_dump` must match the database's major version.
  Terraform ignores the job's image, like the web service's.
- **Terraform plans must be clean when nothing changed** (#387): a plan that always shows a change hides real ones, and the PR's plan is how infrastructure changes are approved.
  The R2 lifecycle rules are listed in the order Cloudflare returns them, rather than ignored with `ignore_changes`, which would also hide a real change to a rule.
  `ignore_changes` covers only each rule's unused transition, which the provider reads back as an empty condition that no valid config can match (cloudflare/terraform-provider-cloudflare#7338); it comes out once the provider is fixed (#387).
- **An external uptime monitor** (#391), UptimeRobot: a free keyword monitor on `/healthcheck` every 5 minutes, alerting the owner by email, so someone hears when the app is down for visitors and not only when Render restarts it.
  It checks the database too (#430, changed from "reachable, not the database" after the first error-state pass, #392, found the monitor would stay green through a database outage).
- **`/healthcheck` reports the database but always answers `200`** (#430): `status` is `available` when a ping (1-second timeout) succeeds and `degraded` when it doesn't, so the monitor's keyword `available` stops matching while Render's check, which reads only the status code, doesn't restart an instance a restart can't fix.
  Never `unavailable`, which contains the keyword.
- **Logs** stay in Render's log viewer for as long as Render keeps them (a few days on the Hobby workspace; the README records the current figure).
  No log shipping, so client IPs aren't kept anywhere else.
- **Deploys:** every push to `main` that passes CI deploys automatically; merging the PR is the approval.
  Infrastructure changes (`terraform apply`) wait for the owner's approval in the `infrastructure` GitHub environment, since some Terraform changes replace or destroy resources.
  The database also has `prevent_destroy`.
- **Workflow secrets.** Every value the workflows read is a repository secret, the IDs (`RENDER_SERVICE_ID`, `CLOUDFLARE_ACCOUNT_ID`) included, rather than the repository variables #208 planned for them, so they are masked in logs too.
  Jobs with secrets run only on `main` or on pull requests from branches in this repository, never with `pull_request_target`.
  Dependabot's pull requests get no Terraform plan, since they can't read Actions secrets; copying the secrets into Dependabot's store isn't worth a second place to keep them.
  (#215)
- **The deploy workflow follows Render's deploy status**, then checks `/healthcheck` once it's live.
  Polling only `/healthcheck` for the new SHA would be shorter, but a failed pre-deploy (`migrate` refusing a schema change, which happens with every schema change while pre-alpha) would only show at a timeout, with no reason given; following the status fails within seconds and names the cause.
  A deploy Render queues behind a running one (202, no ID) fails the job rather than being tracked down: our deploys never overlap (one concurrency group), and changing a variable through the API doesn't start one, so only a dashboard deploy or a Terraform apply can be running.
  The Render CLI's `deploys create --wait` would do the polling, but it's a binary to download and pin in a job holding an account-wide API key.
  (#215)

## Planning and docs

Settled when the docs were slimmed and the Roadmap issue started (#385).
The rules that follow from them are under "Docs" and "Finding work" in CLAUDE.md.

- **The order of planned work is the Roadmap issue (#385), not a file.** Each planned issue is a sub-issue; a PR's `Closes #N` ticks it off, and implementation PRs never edit it.
  "Next up" in `todo.md` changed in 27 of 30 commits, almost always on the same few lines, so PRs built in parallel conflicted there.
- **Work without an issue stays in a file, `docs/backlog.md`**, with the Performance watchlist.
  Few PRs change it, so it rarely conflicts; it's reviewed in PRs and readable without GitHub access; and an issue body would lose edits silently when two sessions save it (the last save wins) and has a size limit the backlog is close to.
- **Docs are one sentence per line.** A change touches only its own sentences instead of rewrapping a paragraph, which is what made parallel PRs conflict on neighbouring text.
  New entries in lists and tables go next to related ones, not at the end.
  No `merge=union`: it would merge both sides silently and can break tables and lose edits.
- **Docs say what the code can't**: reasons, flows across packages, and invariants that aren't obvious.
  `features.md` names a feature's entry points and test files, not every symbol and test, and none of the docs repeat user-facing strings, CSS classes, column lists or Terraform values.
  Before this, every name the docs mentioned still existed, but 3,700 lines of them were a cost on every PR and every read.
- **Docs stay in the same PR as the code they describe**, even though separate docs PRs would conflict less: they would let the docs fall behind.
- **The admin and its observability have their own roadmap**, the [Admin Roadmap issue](https://github.com/grodier/rss/issues/560), at the owner's request, so the reader's roadmap stays readable; it works like #385, and neither edits the other's waves.
  (#583)
- **Measure the reading queries before planning load testing** (#388), with a dev-only seed tool outside `cmd/www`, so seeding code never ships, measuring the repository methods themselves rather than copied SQL.
  The timeline query collapses copies over a user's whole history before paginating, so it's the likeliest to break first; its numbers decide what load testing covers.
- **The first error-state pass is by hand, with no tool** (#392), and fixes nothing itself: it shows how many states there are and how much needs work before deciding whether a walkthrough tool is worth building.
