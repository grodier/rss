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

- **A separate admin site and command, `cmd/admin`**, built from the same commit as `www`, into its own image (#573), and run as its own Render web service (about $7 a month).
  Its web pages and its CLI subcommands share one package, `internal/admin`, so each action is written once.
  Rejected: admin routes inside `www`, which would put owner-only code and credentials behind the reader's host and sessions.
  The production database is only reachable from Render's private network, so `admin` commands against production run in the admin service's shell; the web pages are the everyday tool.
- **The admin's image has a shell; `www`'s doesn't** (#573, changing "built into the same image as `www`").
  Render's Shell, how `admin` commands reach production's database, needs a login shell for the image's user and a `~/.ssh` it owns, and Render doesn't support distroless images for it: even `:debug-nonroot` adds busybox but leaves the user's shell `/sbin/nologin`.
  So the Dockerfile's `admin` stage is Alpine with a non-root `admin` user, and `www`, which takes untrusted input on a public host, stays distroless; one build makes both images' binaries, so both still run the same commit.
  A shell only helps someone already running code in the process, and the admin's is behind Access and its login; the account that matters more is Render's, whose dashboard shows the services' secrets anyway.
  Rejected: one Alpine image for both (simpler, but the public service gets a shell); enrolling from a laptop against production's database (opens the database to the internet and takes the secret key out of the service); a bootstrap page (new code on a public host, needed again for every `-reset`); Render one-off jobs (no stdin, and the QR code and recovery codes would land in the logs).
  Revisit when the admin's pages and a web enrollment (passkeys) cover what the shell is for.
- **It runs none of `www`'s background workers**, only its own small jobs (the health pinger, #576).
- **On a subdomain, `admin.paperround.app`, set up as its own origin**: `__Host-` cookies with `SameSite=Strict`, its own CSP and its own HSTS (the reader's has no `includeSubDomains`).
  The reader and the admin are same-site but not same-origin, and `http.CrossOriginProtection` only lets same-origin form posts through, so no reader page can post to the admin.
  Rejected: a separate domain, which hides nothing (certificate transparency logs list every hostname) and is one more domain to keep.
- **Three layers in front of it**: Cloudflare Access on the hostname, whose DNS record is the only proxied one; the service's `onrender.com` subdomain turned off, so Access can't be bypassed; and the admin's own login, which is the real gate.
  Access is defense in depth and needs no code.
  It's on the Zero Trust Free plan, signed in with the owner's Cloudflare account (#573): the Cloudflare identity provider, new organizations' default, limited to the account's members.
  Chosen over a one-time PIN by email: it needs the account's password and second factor rather than an inbox, and adds nothing new to protect, since whoever holds the Cloudflare account can already change Access and the DNS.
  Its `/healthcheck` is let through Access, so the deploy workflow checks the deployed version as it does for `www`; it reports nothing `paperround.app/healthcheck` doesn't.
  The record starts DNS-only and is proxied once Render has issued the certificate, as Render asks.
- **Admins are their own accounts, not readers** (an `admins` table), so a leaked reader password never opens the admin.
  A second factor is required: TOTP, with hashed one-time recovery codes, and the TOTP secret encrypted with a key held only in the admin service's environment.
  TOTP is a few lines of the standard library (RFC 6238); passkeys are a later upgrade (backlog).
  The first admin is created with `admin enroll` in the service's shell; nothing seeds one.
- **Short sessions**: 30 minutes idle and 12 hours at most, and a destructive action asks for a TOTP code again when the last one is over 10 minutes old.
- **The `__Host-` cookie is production's only** (#567): locally, over `http://localhost`, the cookie is `admin_session` without `Secure`, like the reader's, since `__Host-` requires `Secure` and some browsers (Safari) refuse `Secure` cookies over plain `http`.
- **`admin enroll` always needs `-secret-key`** (#567): it runs as its own process, so a key made for one run would seal a secret the site can't open; locally the key lives in `.env`, and `admin serve` without one makes a key per run, with a warning, rather than refusing to start.
- **`admin enroll` draws the QR code itself** (#567, `cmd/admin/qr.go`: byte mode, level L, versions 1 to 10), rather than adding a QR library for one terminal picture; it also prints the URI, for an app that takes one pasted.
- **`admin enroll -reset` logs every admin out** (#567): it's for a lost phone or a leaked password, so a session on that device must end, and scs stores a session's data encoded, so sessions can't be picked out by admin; with one owner, ending all of them costs nothing.
- **Both login steps share generic errors, and a wrong code counts against the admin, not just the IP** (#567): "That email and password don't match an admin." whether or not the email is an admin's (with a dummy hash checked for timing), and 5 wrong codes per admin per 15 minutes on top of 10 attempts per IP.
- **Every write is recorded in an audit log** (who, what, which record, before and after, when), and so are logins and failed logins; destructive actions also need a typed confirmation.
- **An action's repository method takes who did it and records the change itself, in its own transaction** (#572), rather than repositories running on a transaction handed to them (an `Exec(ctx, func(tx *sql.Tx) error)` helper), or callers building the whole entry.
  The repositories already open their own transactions (an admin with its recovery codes, a reset with the sessions it ends), so this keeps transactions inside `internal/psql`, and callers, the handlers and commands, never hold a `*sql.Tx`.
  Callers pass only an `rss.AuditActor` (who, and from where): the method is what knows the action, the new record's ID and the values it changed, so the same change is recorded the same way from the web and the command line.
  The types are in `rss`, with the other admin types, since `internal/psql` can't import `internal/admin`.
- **The admin's own IP is kept in the audit log** (#572): it's the owner's, and it's what tells a stolen session from a real one.
  A failed code is recorded as `login_failed` with the admin, so a leaked password shows up even when the second factor holds.
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
- **The Overview's look** (#575, approved at its design checkpoint, PR #612): a section per group, in the order People, Content, then Queues (#610) and Jobs (#611), with the build as a line under the page head.
  People and Content are stat rows: each figure a label, a number and a short second line ("of 182", "104 in 30 days"), "about" in the label of an estimated count, and, where a page exists, the label in the accent color and the whole figure its link.
  It reuses #562's stat rows, and each number reads on its own at a glance, which is what the page is for.
  Rejected: a dense table per section (compact, but no figure stands out and it reads as a report), and a strip of key figures in large type above compact rows (repeats figures, and which ones are key will shift as the app grows).
- **The Overview's queues are a dense table with a "Behind" label** (#610, approved at its design checkpoint, PR #613): Queue, Due, Longest wait and Status, the status "OK" or "Behind" as the invites page's status label in the danger tone.
  A queue has several values to compare across queues, and the label puts "Behind" where a status is looked for without a new pattern.
  Rejected: a stat tile per queue (makes the count due the headline, when the wait is what says whether it keeps up), and a summary line above a plain table ("Behind: Email (22 min)."; loud on a calm page, and it repeats the table).
- **The Overview's jobs: every runner's outcomes in the same three buckets, a bar each and a Failed column** (#611, approved at its design checkpoint, PR #614, after two rounds): done (a 304 and a lookup that found no feeds count as done), failed, and other (retries, gone feeds), in green, red and grey, with one key above the table.
  The raw outcomes differ by runner, so a column of them, or bars of them, can't be scanned down the page; with shared buckets every bar compares and "is anything failing?" is one column.
  Failures come first on the bar and are drawn at least 3% wide, since a linear bar shrinks the number that matters most; the column has the exact share, and the per-outcome breakdown is the bar's tooltip and label, with the detail for the Queues page (#582).
  Rejected: the outcomes as one string per row ("saved 42% · 304 56% · failed 2% · gone <1%"), with or without a bar beside it (needs colored words to stand out, and doesn't scan); a row per outcome (too long); a bar per runner of its raw outcomes with a key under each (the bars don't compare); and the buckets as percentage columns only (no picture, which an overview wants).
- **The web performance page: a row per metric, at a percentile chosen on the page** (#578, approved at its design checkpoint, PR #615, after two rounds): a row per metric, its summary (the value at the chosen percentile with its rating, the other two percentiles small beneath, the shares bar and how many views reported it) beside its chart over time, LCP, INP and CLS first and FCP and TTFB smaller; then the breakdowns and the routes as dense tables, then referrers and connections side by side.
  A Percentile filter (p75, p90, p95) beside the period and device filters sets the large value, the charts and every table, so each chart has one line and each cell one number, and the tail is still visible without switching; it's Vercel Speed Insights' model.
  The first round's tiles above a grid of charts were set aside for the rows, which put each number beside its trend.
  Rejected: all three percentiles at once, as three lines per chart and two numbers per cell, with buttons to hide lines (busy charts and tables twice as tall); a distribution histogram per metric with the chart over time behind a toggle (shows the tail's shape, but trends are a click away; it may suit the route page, #589); a scorecard matrix of metrics by segment (scrolls sideways even on a laptop); and tiles above the charts.
- **The service page: one page, its runtime charts two to a row** (#616, approved at its design checkpoint, PR #622): Availability (a stat row of uptime over 24 hours, 7, 30 and 90 days over the incidents table), then Go runtime, then Version, on one page, with #576's Instances after Availability; the five runtime charts two to a row on desktop and one on phones, each under its title and unit.
  The period is the web performance page's row of links under the head, so the admin's pages filter the same way; a source that's not configured or empty is a one-line quiet message in its section, not an error box, and an ongoing incident is the status label in the danger tone.
  Rejected: one chart per row at every width (more precise, but about twice as long on desktop, and #576 adds charts); a tab per section (short tabs, but availability and the runtime are no longer seen together, which is the point of one service page).
- **At 90 days the service page shows no runtime charts, only that they're kept 30 days and which periods show them** (#616, PR #622, changed after the checkpoint): charts of the 30 days kept under a 90-day period would show less than the period says, and the same as 30d; this is how the web performance page treats data its rollups don't keep.
  Rejected: a period per section (two filter rows, and #576's sections would each need one); an hourly rollup of runtime metrics for 90 days (a migration and a rollup for trends that each deploy resets; its own issue if wanted).
- **Ratings are never color alone** (#578): good, needs improvement and poor each have a shape (a circle, a square, a triangle) and their words, in the new `--good` and `--needs-improvement` tokens and `--danger`.
- **The assessment stays at p75** (#578): "Passed" when LCP, INP and CLS are all good at p75, as Google assesses them, whatever percentile the page shows; a rating at p90 or p95 uses the same thresholds.
- **p75, p90 and p95, not p99** (#578): at the app's traffic, p99 is a handful of views, so it would be noise; add it if traffic grows.
- **The page filters by device** (#578, beyond the issue): phones and desktops differ the most, and every RUM tool splits them; it's one condition on each query.
- **A breakdown's row under 100 views is dimmed and tagged "few views"** (#578), so a percentile from 43 views isn't read like one from 4,000, and each metric says how many views reported it.
- **Raw percentiles are exact, rollups' are interpolated within a bucket** (#578): `percentile_cont` over `rum_views`, and `metrics.Percentile` over `rum_daily`'s summed buckets; the shares by rating are exact either way, since the thresholds are among the bounds.
  Installed, referrers, protocols and connections aren't in `rum_daily`, so for 90d and 13m the page says to choose a shorter period rather than adding them to the rollups.
- **Charts are SVG drawn on the server** (#571), with no chart library: they fit the CSP and work without JavaScript.
- **The charts' look** (#571, approved at its design checkpoint): thin lines in up to three series colors (`--accent`, `--brand-strong`, then `--series-3`), gridlines only at the value ticks, markers such as deploys as dashed vertical lines, thresholds as dashed horizontal lines, and a legend above the chart only when it has more than one series.
  Rejected: thresholds as tinted bands (good, needs improvement, poor), which say a threshold is good or bad when a later one may not be (a queue's limit) and drown the line in dark mode; and a legend below the chart, read only after the lines.
- **Markers carry no text on the chart** (#576's follow-up, chosen by the owner, changing #571's labels at the top): a busy day's deploys made their labels overlap into an unreadable row, so a marker is only its dashed line, markers closer than 1.5% of the width are one line, and its version shows on hover (an SVG `title`, no script), in the chart's description and in the Deploys table.
  Rejected: keeping the labels that fit at each width, which leaves most lines unlabelled on busy days anyway; and a scripted readout on hover or tap, which could come later on top of this.
- **Chart labels sit where they're read** (#571): value ticks above their gridlines inside the plot, the top one with the unit, so a phone loses no width to a left margin; each line's latest value and each threshold's label in a gutter at the right, moved apart so none overlap; and a histogram's bucket upper bounds in short form ("1.5k"), with the unit written once.
- **A chart stretches to its column, but its text doesn't scale** (#571): positions across are percentages and positions down are pixels, so labels stay 11px or larger at every width, and a narrow chart draws fewer axis labels.
  This replaces the planned `viewBox` scaling, which can't keep labels at 11px on a 320px phone without making them over 30px on a desktop.
  Rejected: on phones, a fixed-width chart that scrolls sideways like the tables, which starts with the latest value off-screen.
- **The feeds pages are triage first** (#581, approved at its design checkpoint, PR #627, after two rounds): the list says what's wrong with each feed and why, so a fix can be chosen without opening each one; each feed's page opens with a status panel that puts the problem, the error and the actions that fix it together; and a site's page puts its feeds first, then its lookup and icon side by side as sentences.
  The list has six columns, fitting a 1280px screen with the sidebar: the feed with its site's host under it, Problem, subscribers, posts a week, and last success and next fetch as relative times with the UTC time on hover; last attempt and gone are on the feed's page.
  The filters show their counts, at one more pass over `feeds` per view (in the watchlist).
  Rejected: every column in one dense table, actions in their own section (data first; the error is a click away and the table scrolls on a laptop), and Overview-style figures as the filters over that table, with every field in one flat grid (summary first; repeats the Overview and gives the error the ETag's weight).
- **A feed's problem is one phrase, not a status label** (#581, the second round of its checkpoint): the first that applies of "Gone since {date}", "Failing {n}×" (from the Failing filter's threshold), "Failed {n}×" (fewer) and "Never fetched", then its error as plain text; only gone and failing are in the danger tone, and a feed that's fine shows nothing.
  Staleness isn't a problem but a filter, and shows as a last success in bold once it's over a day old.
  Rejected: the first round's labels for every state (Failing, Retrying, Stale, Never fetched, OK), each in its own tone, which mixed whether fetching works, the feed's lifecycle and its freshness in one column and contradicted the error beside them ("OK" over "status 500").
- **The feeds page's filters** (#581): Never fetched means no successful fetch yet, whether or not one was tried, and Stale counts a subscribed feed that was never fetched from when it was found, so one found a minute ago isn't stale and one that never worked is.
  Failing is the reader's threshold, 3 failures in a row (`rss.AdminFailingAfter`), so the admin and the reader's warning agree.
- **Refresh now is recorded after the fetch, with its outcome** (#581): the refresher records the fetch on the feed as any fetch does, and can't take an `rss.AuditActor`, so the handler records `feed.refresh` itself, the one admin action not recorded in its change's transaction; an entry that says what happened is worth more than one written before it.
  It refuses a gone feed (Clear gone first), since a successful fetch doesn't clear `gone_at`.
- **A row of filter links too wide for a phone scrolls in its own frame** (#581), like a table, rather than wrapping: the feeds page's six filters don't fit on 320px.
- **A feed's place in Discover is edited on its admin page, as a status panel** (#628, approved at its design checkpoint): its heading says where the feed stands ("In Discover under Food" or "Not in Discover"), like the fetching panel above it, with a section select and Save under it; the site page's feed table names each feed's section.
  The feed page is where the choice is made because it shows what the choice needs (posting rate, health, its site); an overview of the catalog by section is #648's Discover page.
  Rejected: a plain labelled form (it read as foreign to a page of status and figures), a button per section that saves on click (grows with the list, and a stray click saves), and the select in the page head (no room for its hint or error).
  A gone feed can't be added, since Discover would offer a feed that no longer updates; one already in the catalog can still be taken out.
  Breadcrumbs on the feed and site pages, which the checkpoint showed were missing, are #655.
- **The seed list's status is a page** (#648, from #450's plan): the list joined to `lookups` by site key, so the owner sees failed entries without the deploy log; it read the embedded file until #649 moved the list into a table (#710), which it now pages newest first, 100 at a time, its filters counted in SQL.
  Its one action, Look up again, is offered on entries not found (failed or no feeds) and always queues the entry's own address (`seed.lookup`), at a reader's priority so it runs now rather than behind the seed.
  It doesn't reuse a site's Check for new feeds, as #648 first planned: that fetches the site's home page, which isn't the entry's address when the entry is a feed (#678).
- **The seed list and the catalog are two pages after Feeds, each one table with filter links like the feeds list** (#648, approved at its design checkpoint in #678): the seed list's filters are All (the default), Not found, Waiting and Found but not in Discover; the catalog's are All, which adds a Section column, and one per section in Discover's order, empty ones included with their count.
  Rejected: the catalog grouped under a heading per section (long and mostly empty while the catalog is small, its tables' columns not lining up), and both on one page with tabs (two rows of filter links, for two questions asked apart).
- **Sites are their own section in the admin, above their feeds** (#706, found while looking for a site's icon): a Sites list after Feeds, a site's page under Sites in the sidebar, and each host in the feed tables linking to its site, since a site's page was reachable only by opening one of its feeds and going up.
  The list follows the Feeds list's pattern (filter links with counts, a search, sortable headers, a keyset cursor).
- **The Sites list shows each site's lookup and icon in columns of their own, and the icon itself** (#706, approved at its design checkpoint in #717): each row leads with the site's icon on a white tile, as readers see it, then its host and title, then its lookup in words (in the danger tone when it failed), its icon's size and type, its feeds, subscribers and when it was found; a site's page shows the icon larger in its Icon panel.
  The image answers what the owner opens a site to check (the right logo, or a default, a broken or a blurry one), and its size says why one looks blurry in Discover; it leads the Site cell, as readers' avatars do, so it shows on phones without scrolling the table sideways.
  The admin serves the images itself (`GET /sites/{id}/icon`), since its CSP allows only its own images, versioned by each icon's hash so the browser keeps it until it changes.
  Rejected: one Problem column, like Feeds ("Lookup failed" or "No icon"; triage first, but many sites normally have no icon, and a working lookup disappears), no lookup or icon columns (the narrowest, but each site has to be opened to see its state), the icon's size in words only (it can't show the wrong logo), and the image in the Icon column (off-screen on phones).
  Rejected: only linking hosts from the Feeds list (sites stay reachable only through their feeds), and grouping the Feeds list by site (it's triage first, and grouping buries failing feeds under healthy sites).
  Breadcrumbs (#655) follow where a page sits, not how it was reached: `Sites › host` and `Sites › host › feed`.
- **Discover's catalog grows from the admin in three steps** (#649): sites are added to the seed list in bulk (#711), their lookups find the feeds, and an admin picks feeds for Discover a page of sites at a time (#712).
  Bulk upload and picking come first; a model's suggestions wait in the backlog until the owner's own picks can measure one (below).
- **Sites are added to the seed list by pasting, then previewing, then adding** (#711): one site per line, optionally followed by a comma or tab and a Discover section, with a source naming the batch; the preview says what each line will do, and only Add writes.
  The section is a hint stored on the entry, which the picking page pre-selects; it never puts feeds in the catalog by itself, since a site can have several feeds and only an admin decides.
  One line per site keeps the list's rule (a site has one lookup), which is also why OPML files, which list several feeds per site, aren't accepted yet (backlog).
  No file upload: pasting works for thousands of lines, and the route takes up to 1 MiB and 10,000 lines.
  The preview is a table of every line, problems first, under a count per outcome (#711's design checkpoint, option (a)), with what happens right after the line number so it stays in view on phones, and an add's section hint beside it; Add counts what it will add.
  Rejected: an outcome's lines folded under its count (a click to see what failed) and only the problem lines (the adds, and their hints, unseen before adding).
  A line's address ends at its first comma or tab, since an address has no spaces and seldom a comma while a section's name may have both (#711); a line repeats only an earlier line that can be added, so a bad first line doesn't block a good second one.
  Each upload is one audit entry with every site key it added, as the list's history.
- **Picking Discover's feeds is a queue of sites with feeds still to decide** (#712): a feed leaves it when it's put in the catalog, skipped, or gone; skips are stored (`discover_skips`) and can be undone.
  Each feed shows what helps choose (posting rate, latest post, its newest titles, notes for comments feeds and silent ones, and its categories, #713), with a section pre-filled from the seed entry's hint, else from the site's feed already in Discover.
  A feed is ticked for you only when its site has one candidate and the admin's own upload named its section, a choice already made; nothing else is.
  Picks are recorded per feed as the feed page records them (`feed.discover`), so the catalog's history reads the same however a feed got in.
  Subscriber counts aren't shown there: no cross-reader data decides what's in the catalog.
  The page is one table, each site's header row (its host, source, hint and Skip the rest) above its feed rows (#712's design checkpoint, option (a)): the most decisions per screen, in the admin's dense-table pattern; on phones the table scrolls like the others, with the site's line pinned in view.
  Rejected: a bordered block per site with its feeds as a list (half as many per screen, and cards around lists), and one site per page with Save & next (the slowest way through thousands of sites).
  Skip the rest starts ticked, so a Save clears the page and the next one is new work; skips are undone from Skipped (#712).
- **Feeds' categories are kept and shown to admins only** (#713): a channel's own `<category>` and iTunes categories, then its items' most common, at most 10.
  They're free, help an admin pick, and are a model's input later; they never become sections or tags by themselves.
- **Waitlist answers reach the seed list through the upload form** (#714): the Waiting tab's selection fills the form with the addresses in their answers and lists the names that aren't addresses, for the owner to look up; nothing is added until the owner adds it, since answers are public input.
- **A model's suggestions for the catalog are deferred** (#649), to the backlog's "A model's suggestions for Discover's catalog".
  Cost isn't the reason (well under a dollar per thousand sites on a small model): which model, its prompt and the confidence to trust can only be chosen against labeled picks, and bulk picking (#712) produces them.
  Planned shape, for when it's filed: run from the admin, which holds the key, never `www`; public site data only (names, descriptions, feed titles, categories, headlines); answers constrained to the site's feed IDs and the section IDs, with a probability; stored as suggestions an admin accepts or changes on the picking page, never applied by themselves.
  Headlines are untrusted text that can try to steer a model, which is one more reason suggestions are only ever pre-filled choices.
- **Check the icon again works on any site** (#705): it clears `icon_checked_at` as well as making the check due, so the runner takes the site as if it were new, subscribed or not.
  Rejected: dropping the runner's subscriber condition (it would recheck thousands of unfollowed seed sites every 30 days) and a column for a requested check (a migration for what "never checked" already says); the cost is that the site's page loses "none found {when}" until the check runs, seconds later.
- **`create-user` and `send-test-email` move from `www` to `admin`** (#568); `seed` stayed in `www` as a deploy step until the seed list moved into a table (#710), which removed it.

## Observability

Planned in #583 with the admin, which is where it's read.

- **One place to look: the admin.** The app records its own request, background-job and real-user metrics in Postgres, and the admin fetches container CPU and memory and deploys from Render's API (#576), and uptime from UptimeRobot's (#623).
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
- **Retention**: request metrics at one minute for 7 days and hourly for 90; raw beacons for 30 days (at most 500,000) and daily rollups for 13 months (#563, #565); the admin's availability checks for 90 days (#588); runtime metrics for 30 days (#616, from 7).
- **Recent server errors are kept in a table capped by count** (#564), by route pattern, so the admin can show them next to each route without log access.
- **A Render API key on the admin service** (#576) is accepted: Render keys have full account access, but a compromised admin can already write to the database.
  It's its own key, not the one Terraform and CI use, so each can be revoked alone.
- **Deploys are labelled with the version the pinger saw** (#576): we deploy images by digest, so Render's deploy records carry no commit; a deploy that went live takes the version the pinger last saw before the next one went live, and any other its digest.
  Rejected: deploying by a reference that also carries the tag, which Render might not accept, and labelling by digest alone, which doesn't trace a change to a commit.
- **Render's figures are the highest instance's, and its limits come from its API** (#576): two instances overlap during every deploy and each has new IDs, so a series per instance would be many short lines; this matches the runtime charts, and the limits follow a plan change without a code change.
- **Availability is checked from both sides** (#623, #588): UptimeRobot's API for the outside view (split from #576 before building, to keep each PR reviewable), and the admin pinging `www`'s `/healthcheck` over the private network every minute, to tell the app being down from the edge or DNS being down.
- **The service page's inside view** (#616): uptime is the share of the pinger's checks that were ok, over the checks made (minutes with no check count neither way); an incident is a run of failed checks with no ok check between them, ended by the next ok check, and missing minutes don't end or split one.
- **Runtime across `www`'s instances** (#616): memory, goroutines and GC pause take the highest instance at each minute, since the limits are per instance; database connections are summed, since the instances share the database's; each chart step then takes the highest of its minutes, so a spike isn't averaged away.
- **Runtime metrics are kept 30 days, not 7** (#616): a slow leak builds over weeks, and 7 days may cover only one or two deploys to compare; about 43,000 small rows per instance.
- **Availability checks are kept 90 days, and the service page shows them all** (#588, #616): a 90-day period shows the trend over months, over the same span as the hourly request metrics.

## Feed discovery

Planned in #50 (issues #51–#67).

- **No "add feed" form.** Users search; a website that isn't known yet is *looked up*, which finds its feeds in the background and creates a site page listing them.
  Every feed belongs to a site, and feeds are only created by discovery.
  (#61)
- **Search only reads the database** (site title, host and description, feed title, URL and description).
  Sites are found with their feeds; Discover's search shows a site as one row, with Follow when it has one feed the reader doesn't follow, or opening its page to choose when it has several (#645).
  Descriptions were added with search as you type (#449), so a site can be found by what it's about, not only by its name or address.
- **The search box is for names and addresses, not prose** (#441): it turns off autocorrect, auto-capitalization and spellcheck, which on phones broke addresses ("npr. org"), and an address typed with stray spaces is still offered as a lookup.
  The input has `autocapitalize="none" autocorrect="off" spellcheck="false"` and `enterkeyhint="search"`, and keeps `type="search"`; the Saved page's search box keeps spellcheck, since it searches article text.
  Only whitespace next to `.`, `/` or `:` is removed (`npr. org`, `https: //npr.org`); any other whitespace still means the query isn't an address, so free text such as `news on npr.org` isn't glued into one.
  The query shown back stays as typed; only the lookup offer and the site-key match use the cleaned-up address.
- **Search updates as you type by fetching the page itself** (#449): no new route; `live-search.ts` fetches `/discover?q=…` and swaps in the page's results container, as infinite scroll does with the next page, so its markup lives in one template.
  The cost is a heavier response than a list of names (3–14 KB with about 1,300 sites and feeds locally; timings are in the watchlist's "Search scans with `ILIKE`").
  A request goes out about 250 ms after the last keystroke, from 2 characters, aborting any still in flight; an empty box shows the empty page without a request.
  The URL follows the query (`history.replaceState`), so reload and Back show the same results.
  There's no rate limit: debouncing bounds each reader's requests, and the `ILIKE` scan's cost is on the watchlist.
  GET handlers still never fetch user-supplied URLs, so looking up an address still takes a click on Look up, which appears as you type.
  What came in is announced in a polite live region rather than making the results a live region, which would read every result out: since #645, the server words it in the results container's `data-announce` ("7 results"), so the module needn't know the markup.
- **Typing shows suggestions, Enter the results** (#666, decided at #664's design checkpoint, rounds 4, 6 and 7), as on X, Bluesky and LinkedIn: typing is usually going somewhere (a feed you follow, a site you know), while Enter wants everything.
  This partly reverses #449's entry above: live search still fetches the page itself and swaps its container in place (still no dropdown, so #476's reasons hold), but while typing what it swaps in is suggestions, so the tabs and counts no longer change under your fingers.
  A hidden `scope` field in the form tells them apart: only a submit sends it, and live search fetches the form's action with `q` alone, so the server shows suggestions for `q` without `scope` and results with it; without JavaScript every submit has `scope`.
  The field holds the tab shown (Top on any other page), so editing the query on a results page shows suggestions until Enter, which keeps that tab.
  Suggestions read only the reader's subscriptions (6) and the site search (12, as some sites are left out as fully followed); saved articles wait for Enter.
  Live search announces "{n} feeds" for them.
- **Results update in place under the search box, not in a dropdown** (#449, approved at the design checkpoint in #476): the page already is the results, so updating it adds no new widget, keeps the results, their state and the Look up row in view, and leaves keyboard and screen-reader behavior as it was.
  A dropdown of up to 5 site names was the alternative; it's a new ARIA combobox (arrow keys, Enter, Escape) to get right, and hides the results it duplicates.
  Results can shift under the box as you type, but only after a pause.
- **Today's Search page is Discover, at `/discover`** (#450, #631): the fourth tab, labelled Discover, with no redirect from `/search`, which pre-alpha needs none of.
  #631 gave it a compass, to keep the magnifier for a separate whole-app search; #645's design checkpoint (#664) made Discover that search instead, so it took the magnifier (below).
- **Discover is the one search place** (#450, #645, decided at #664's design checkpoint over seven rounds), as X's and Bluesky's Explore are: its box searches the reader's feeds, their saved articles and sites to follow, and before typing it shows Discover's sections, so first run is unchanged.
  There's no `/search` page and no other entry: a separate Search page, opened from a top-bar icon (approved in #664's first round), was dropped because two boxes did one job (finding a site) differently, and two nav entries led to search.
  The tab keeps the label Discover, since first-run copy across the app sends readers there by name; its placeholder is "Search or paste a link", short enough for 320 px.
- **Results come in tabs by kind: Top, Feeds and Saved** (#645), each tab its own URL (`?scope=`), so they're navigation in the timeline tabs' style; #647's Articles tab goes between Feeds and Saved.
  Top mixes the kinds, ranked, up to 10, then links the tabs with more; Feeds and Saved list up to 50.
  The tabs depend on the account, not the query: a reader who has saved nothing gets none, since Top would be Feeds, so tabs never appear or vanish while typing.
  Rejected: groups each showing their first 3 (the page grew to screens and hid most of each group), one mixed list without tabs (it reads as ranked when the ranking is only a substring match), status tabs such as "Following" and "New" ("New" read as recently added, and any status split makes you check two lists), and scope chips (they look multi-select).
- **Following is a state on each feed row, not a kind of result** (#645), as Follow is on X's and Bluesky's people results: "✓ Following" on a feed the reader follows, Discover's Follow on a site's one feed they don't, and nothing on a site with several, whose row opens its page to choose (nothing guesses which, #629).
  A site whose feeds the reader all follows isn't listed as a site: its followed feeds are.
- **A pasted address gets a Look up row at the top of the results** (#645), in place of the old card's "Not what you're looking for?", which said nothing useful when nothing matched; it's left out when the address is one of a found site's feeds, or its home page once a lookup has finished there, as the site's own row (with Follow) is there (#716).
  Any other address on a found site, such as a feed its home page doesn't link to, keeps the row, since that lookup adds what it finds to the site; hiding it for every address on a found site left readers no way to add such a feed.
  A site found from another address (a feed, or a feed on another host, such as `feeds.npr.org` for `npr.org`) keeps the row for its home page, whose feeds nobody has looked for yet.
- **Matches aren't highlighted** (#664): marking the typed text read like a page's find, and none of the references does it; a saved article's excerpt and a site's tagline show why a row matched instead.
- **The ranking uses only the reader's own data** (#645): how well the title matches (all of it, its start, the start of a word, anywhere), a match only elsewhere below any title match, then +3 for a feed the reader follows and +2 for a saved article, then +1 for something posted or saved in the last 14 days.
  Each source orders by the same match quality in SQL (`matchOrder`), so its best matches are within its 51 rows.
  It's a deliberately simple, explainable order until #451 (sites) and #647 (articles) plan real matching; cross-reader signals are out, as "The feed most people follow" was (#628).
- **Decided at #664's checkpoint for later issues:**
  - While typing, one list of up to 6 feeds, followed or not, ranked as the Feeds tab, with no sections or headings (#666): compact rows (a 2 rem avatar, the name and one line of facts), a quiet "✓ Following" the only state, a pasted address's Look up row at the top, and "Search everything for “{q}”" last, with an Enter hint where there's a keyboard; it's last because Enter already does it, and the top is for the feed you're looking for.
    Buttons while typing were rejected (Choose repeated the row, and Follow was a second filled button beside the box's), as was a small "+" (a new Follow control); Follow lives on the results and the site page.
  - Recent searches, kept on the device only and never sent to the server, which reverses #645's first "No recent searches are stored" while keeping the rule against per-person reading data on the server; built in #667, which shows them while the box is focused and empty, as on X, and forgets them on the logged-out pages, for shared devices.
  - "/" opens the search from anywhere, and tapping Discover again focuses its box (#646), in place of #646's first plan of scope chips, "All {n}" and a link from Discover to Search.
- **A query that is an address also matches its site key exactly** (#441), and that site comes first, so pasting `https://www.npr.org/news` finds npr.org: a substring match on the whole string never matches `sites.host`.
- **The database is seeded with a list of sites** (#447), so search finds well-known sites before anyone has looked them up.
  #447 first planned a catalog with topics and a Browse list on the Search page, so new readers could discover sites without knowing their addresses; both were built and dropped in review, since Browse worked as recommendations but poorly otherwise, and shared topics sat awkwardly beside readers' own tags, which are private, with no clear use yet.
  How new readers discover sites is under "First run and discovery" (#450): sections came back, for Discover only; keywords are for #451 to plan.
- **The seed list is a table the admin edits, `seed_sites`, and its only source** (#649, #710), replacing #447's plain text file in the repository, which the owner edited in PRs and which never listed a site.
  Growing the list to thousands of sites from curated lists and waitlist answers needed a PR and a deploy per batch; the audit log now keeps the history git kept.
  Rejected: keeping the file and having the admin produce one to commit (slow, and waitlist answers would need PRs too), and a table plus the file for new databases (two sources of truth).
  Each site is listed once, since a site has one lookup; a home page's lookup finds the feeds the page advertises, and a feed's address adds only that feed.
  An entry is the admin's intent, and its site key's lookup is how it went.
- **Seeding queues lookups**, so seeded feeds are created by discovery like any other, and a seeded site is an ordinary site once found.
  Adding sites to the list queues their lookups there and then (#711), at the seed's priority.
- **Deploys don't seed, and nothing retries on its own** (#649, #709, #710), replacing #447's `www seed`, which every deploy ran as `www migrate -seed` and which retried failed entries an hour after they failed.
  With the list in a table, adding queues the lookups, so a deploy has nothing to do; the admin's seed list shows failures (#648), and its Not found filter has Look up all again, which re-queues every failed or no-feeds entry at the seed's priority.
  A periodic retry worker is still more machinery than the list needs: an entry that keeps failing is a reason to fix or drop it, which the page shows.
- **Readers' lookups go before the seed's** (#447): lookups have a priority, and the seed's are claimed only when no reader's is pending, so seeding thousands of sites after a reset never makes a reader's lookup wait.
  A reader who looks up a site the seed has queued moves that lookup ahead, since someone is now waiting for it.
- **A lookup is offered when the query looks like a URL or domain.** Turning a name into a website ("the verge" → theverge.com) is a backlog item.
- **The `lookups` table is the queue and the cache:** one row per site key, re-runnable 24 hours after it found a site, or 1 hour after it failed or found no feeds (#439), since "No feeds found" is also worth retrying soon.
  A finished lookup re-runs at once for a different address on the same site (#439), so one result doesn't hide the site's other addresses, such as its feed URLs; pasting a feed URL on a site already found adds that feed to the site.
  That adds the feed without renaming the site: only a site's home page describes it, so details read from a feed or another page (an article, `medium.com/@alice`) only fill in what an existing site lacks, and name a new one (#716).
  Only the home page (its root with no query, see below) of a site found from its home page keeps the cached result, so `npr.org` and `www.npr.org/` still go straight to the site page; a site found from another address re-runs for its home page at once, and its site page offers "Check for new feeds" at once (#716).
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
- **On a host shared by many sites, the key adds the path that names the site** (planned in #725, built in #726): `medium.com/@alice`, `youtube.com/channel/UC…`, `reddit.com/r/golang`, `github.com/owner/repo`, `bsky.app/profile/someone`, and on any other host a first path segment `@name` (`mastodon.social/@bob`), so each author, channel or project gets its own site page, name and lookup.
  The rules are a list in code for the platforms that need one, plus the generic `@name` rule, which covers every Mastodon and fediverse instance, Threads and the platforms' own `@` pages without listing them.
  A list alone would leave thousands of fediverse instances merged; keying every site by its feed's link was rejected, since it would split ordinary sites (each NPR section's feed links to its section's page).
  The rare site that uses `/@name` for something else gets one site per such path, which is harmless.
  A page and its feed that disagree on a key with a path on the same host take the feed's (a YouTube `/@handle` page's feed links to its `/channel/ID`), so a channel is one site whichever address was looked up.
  A path-scoped site doesn't try the common feed paths, which would find the platform's own feed.
- **Searching a platform's name groups its profiles; searching a person's name finds them** (#739, option 1 chosen by the owner over a per-host cap and over matching path-scoped sites by name only).
  A site with a path in its key is grouped with the others of its host into one row, counting them and linking to the results narrowed to that host, unless its own name matches the query at its start or a word's start, or it's the pasted address.
  Its own name is its title without the platform's suffix ("Alice – Medium" → "Alice"), since sites on a shared host must keep showing their platform (see #728) and "medium" mustn't count as a match of every Medium author's name.
  Grouping, not hiding: the reader learns the profiles exist and how to list them; matching by name only was rejected, as a profile whose handle isn't in its title couldn't be found by its handle.
  There are no groups of one, and the reader's followed feeds are never grouped.
- **A feed moves from a shared host's own site to its new site** (#725), the one exception to "a feed belongs to the first site it was discovered from": when a lookup saves it under a key with a path whose host is the key of its current site (`medium.com` → `medium.com/@alice`), never between two path-scoped sites or two hosts.
  Without it, sites merged before #726 would block their authors' new sites, which would come up empty.
- **A site's "home page" is its root, `SiteRoot`, with no query** (#725): `/` for an ordinary site, `/@alice` for `medium.com/@alice`; a lookup records whether it ran on it (`lookups.home_page`), replacing #716's check that its URL's path is `/`.
- **Stored sites follow a change of the key rules on deploy, only when the rules' version changes** (#725, built in #727): `discovery.SiteKeyRulesVersion`, bumped by any PR that changes what key an address gets, against the version stored in `site_key_rules`; `www migrate` (production's pre-deploy command) moves feeds from a bare host's site to their path-scoped sites, queues each new site's root lookup at the seed's priority, and deletes a bare host's site left with no feeds.
  An SQL migration was rejected, as it would repeat the rules as regexes and need another migration for each rule change; a step that scanned on every deploy was rejected for its cost.
  It isn't compatibility code with a removal date: it's how stored sites follow every future rule change, and a deploy with unchanged rules costs one read.
- **Search, site pages and lookups are login-only** for now (logged-out access is a backlog item), and lookups are rate-limited per user.
  (#67)
- **Feeds are parsed with `gofeed`**, which reads RSS 0.9x–2.0, RSS 1.0, Atom and JSON Feed.
  (#54)

## First run and discovery

Planned in #450 (issues #628–#649, tracked in #650; #629 was dropped in review), from testers' feedback that recommendations at signup were weak, over four rounds of clickable prototypes.
Look-and-feel choices are each issue's design checkpoint; what's below is settled.

- **First run isn't a mode: it's the app with nothing followed yet.** A welcome page, a setup step, a first "edition" on Home that switched off after five follows and a slice of Discover on an empty Home were all prototyped and dropped: each made the first visit unlike every later one, with no clear way back to what it showed.
  So what helps a new reader is the same thing an established one uses: Discover, Picks, Search and import.
- **Signup goes straight to Home, and Home only ever shows what you follow.**
  Rejected: landing on Discover (the first screen isn't where you'll live, and the next visit opens on a thin Home), and showing unfollowed sites' articles on an empty or short Home (a second kind of Home).
  Home's empty states offer ways to find sites, not samples of them (#639).
- **Search becomes Discover, and Discover is for finding what to follow** (#631, #633).
  It lists Discover's catalog by section, each feed with its site, posting rate, a peek at its latest posts and Follow, and leaves out feeds you follow so it keeps showing something new; its search results still include followed sites, marked Following, so they never look missing.
  Articles live one level down: a section's Recent posts (#634) and the site page (#635).
  Rejected: Discover as an article stream (it read as a second Home).
- **Readers follow feeds, and Discover's catalog lists feeds an admin picked** (#628), not sites.
  Following a site's "main" feed, the first its page advertised, was planned (#629) and dropped in review: a site that lists its comments or podcast feed first would make a new reader follow the wrong thing, and one bad guess is a poor first experience.
  So nothing guesses: Discover's rows are picked feeds (a newspaper's Science and Food feeds can sit in different sections), the site page keeps a Follow per feed with what helps choose (#635), and the waitlist's sites are followed only when the feed is unambiguous (#640).
  "The feed most people follow" was rejected as a way to pick, since it's cross-reader data.
- **Sections are back, for Discover only** (#628): one per catalog feed, from a fixed list, so a typo can't create a section.
  The list is a table (`discover_sections`), not code, changed in #628's review from the planned list in code: renaming, dropping or adding a section shouldn't need a migration of the catalog or a deploy, and the owner expects to edit them from the admin later (dropping one nobody picks, or a write-in option).
  Feeds refer to a section by its ID, so a rename changes nothing else; a section with feeds under it can't be deleted until they're moved.
  The migration seeds the starting list; there's no admin page for sections yet.
  They never appear on subscriptions, aren't tags and readers can't edit them, which is what made #447's topics awkward beside private tags.
- **The catalog lives in the database, edited in the admin** (#628), not in the seed list: production data is kept now that schema changes are migrations, and bulk upload (#711) and picking (#712) write to it, so a file would be a second source of truth.
  The seed list keeps its job of looking sites up before anyone searches for them.
  Local development adds sites and marks feeds in its own admin (#711, #712) rather than copying production, which holds people's emails and reading; there's no fixture of catalog data (#649).
- **The catalog is the same for everyone; each reader's Discover is computed from it on each load** (#633, #643): leaving out followed feeds and ordering sections are cheap queries over a few thousand feeds.
  A per-reader table was considered and not taken: it would need a row per reader per catalog feed and an update on every follow, unfollow, signup and catalog edit, for no speed-up at this size.
  Unlike `timeline_entries`, whose positions can't be recomputed, a per-reader ranking can always be rebuilt, so if scoring ever gets too slow it becomes a cache refreshed by a job; personalizing Picks and Discover from a reader's own follows, stars, saves and opens is planned together in #644.
- **Buttons say Follow, Following and Unfollow** (#630), as the tagline does; "subscriptions" stays only for the OPML file, the word other readers use.
- **Following keeps a row in place until the next load**, and tapping again undoes; after a reload a followed feed leaves Discover's lists and Picks, and "✓ Following" is a label, with unfollowing on the site's Edit page (#424's reasoning).
- **Discover's sections are ordered by what the reader follows, then what they chose on the waitlist, then the default order** (#643), computed on each load and never while the page is open; there's no settings page for it.
  What they follow is how many of a section's catalog feeds they follow, gone feeds aside, so a section stays put however many other feeds of its sites they follow.
  The order is part of `DiscoverRepository.Sections`' one statement, not a query of its own; on `make db/loadseed`'s data it cost nothing measurable.
  A line under the chips, "Sections you follow most come first.", says why whenever a section is out of its default place, whichever rule moved it; sections sharing a position never count as moved, since the database orders their names by its own collation.
- **The onboarding question is on the waitlist, not at signup** (#642): "What do you like to read?", optional, from the sections, answered while people wait, so signup stays instant.
  This reverses #566's rejection of "What do you mostly read?", which was too vague to act on; with sections the answer orders Discover.
- **The sites people name on the waitlist are followed at signup** (#640), confident matches only (an address, or a name matching exactly one known site) and only when the feed is unambiguous (the site's only feed, or its one feed in Discover), with a one-time flash naming what was followed, what needs a choice and what wasn't found.
  They chose these sites, and an unwanted one is one tap to undo, as with import; best guesses would follow the wrong site.
- **Picks is a timeline tab of articles from catalog feeds you don't follow yet** (#644), every account's second tab, removable and re-addable like any timeline, never shared.
  A tab rather than a feed mixed into All feeds: All feeds stays "what you follow", and a timeline is per reader, so it can become personal later.
  What it shows, and how it and Discover's order may become personal or algorithmic, is #644's to plan.
- **Import stays in plain sight** (#636, #637, #638): a card at the top of Discover until the account has imported a file or follows 10 feeds, then a line at its end, plus Settings and the empty Home; the import page has each reader's export steps, and a pasted list of sites works like a file.
  The card has no dismiss button: what the account has done decides, so it needs no stored state.
- **One "Getting started" guide, read like an article** (#641), pinned on All feeds for new accounts until "Done" or 14 days, and kept in Settings › Help.
  Rejected: tours and tips (research on onboarding tutorials finds people skip them and they don't help).
  A Paper Round blog that new accounts follow by default, with its welcome post, is the later version (backlog).
- **At most one notice above the first article**: the confirm-email notice waits for a page without a flash (#640).
- **One search for the whole app** (#645, #646), on Discover (#664's design checkpoint): your feeds, saved articles and sites to follow, with articles in your feeds planned (#647); see "Feed discovery".
  Rejected: a search box on each page, which makes you know where something is before you can find it.
  Saved keeps its own box for the list you're looking at.
- **No measurement of whether new accounts follow anything**, beyond the admin's existing aggregates (the Waves table's "active after 7 days"): that would be per-person data about reading.
- **Discover's catalog feeds are refreshed about once a day even without followers** (#632), so its "Latest" dates and Picks are current; the cost grows with the catalog, not with readers.

## Feed ingestion

Planned in #126, with issues #128–#133.

- **Refresh workers run inside `www`**, not as a cron-hit endpoint or a separate service.
- **Only feeds someone subscribes to, or whose page view requested a refresh, are refreshed in the background.** Nobody waits on the rest, except Discover's catalog (#632, below).
  (#137)
- **The Refresh button stays synchronous** and goes through the same `ingest.Refresher` as the workers, so there is one fetch-and-save path.
- **The schedule is ours:** an interval with jitter, backoff after failures, and the server's `Retry-After` / `Cache-Control` hints, which can only delay a fetch.
  RSS `<ttl>` and `sy:updatePeriod` are ignored.
  (#126, #133)
- **Discover's catalog feeds are refreshed even with no followers, about once a day** (#632, planned in #450): Discover shows each feed's posting rate and latest posts, and Picks recent articles from feeds the reader doesn't follow, so they can't keep the articles from their lookup forever.
  Only catalog feeds (`discover_feeds`): they're what Discover and Picks show, and other unfollowed feeds keep the rule above.
  Any feed nobody follows waits at least a day between fetches, whatever its posting rate or outcome, so the cost grows with the catalog, not with readers; once someone follows it, its normal schedule applies from its next fetch, with no fetch forced on follow, since the feed is at most a day old.
  Their new articles are enriched, since a catalog feed is wanted as a followed one is (#708, under "Enrichment").
- **Busy feeds are fetched more often; quiet ones stay hourly** (#456, planned on #438).
  The interval is about four fetches per expected article, from `posts_per_week`, between 15 minutes and an hour, so feeds posting more than 6 a day are fetched sooner and 24 a day or more every 15 minutes.
  At an hour, an article waited 30 minutes on average to be fetched, and a feed whose file holds fewer items than an hour of posts loses articles outright; in #438's simulation, faster busy feeds cost 22% more fetches and cut the wait to 11 minutes.
  A later save where every item of two or more is new means the feed may have outrun its file, so the next fetch comes in half the time since the previous one, at least 15 minutes, but never later than the rate's own interval (#456), so a busy feed's first success after failures doesn't wait half the outage.
  Fetching quiet feeds less often (up to a day) would save requests but delay the rare posts readers value most; it's a backlog item for when fetch volume matters.
- **A 410 Gone stops background fetches** of the feed; rediscovering it clears that.
  (#132)
- **A save after two days without a fetch attempt is placed as a feed's first save** (#497, planned in #491): everything is backlog at its date, undated items are spaced back, and, for a followed or catalog feed, only the first save's enrichment allowance applies.
  Feeds nobody follows aren't refreshed, so a seeded or looked-up site followed weeks later would otherwise bring every post since a day before its last fetch in as news, spread into the top 6 hours as one block; nobody was fetching it, so we don't know when those posts appeared, and dates are the best order we have.
  The previous attempt decides, not the previous success: a followed feed is attempted at least once a day even while it fails, so a feed that was down keeps its news.
- **Articles keep their authors' names, never their email addresses** (`articles.authors`).
  RSS's `<author>` is an email address by spec, often a placeholder (`noreply@blogger.com (Unknown)`), so only the name in it is kept, and "Unknown" is dropped.
  For RSS every `dc:creator` is read, since that's how multi-author posts list their authors; otherwise the first source with an author wins, as in gofeed.
  An item without authors doesn't inherit the feed's: on a single-author blog that only repeats the feed's name on every row.
  Rows name them in the kicker, after the time ("Feed · Nov 14, 9:16 AM · Jane Doe"), shortened past two to "Jane Doe and 4 others" and cut with an ellipsis rather than wrapped, so no row grows taller (#688, the owner, from the review #685 tracks).
  They're left out when the only author is the feed's name the kicker already shows, as on a personal blog's own posts.
  On a one-line kicker long authors are cut first, and the feed's name only past 70% of the line, since it says whose the row is; the time never is.
  On phones the feed's name goes over the time and the authors in both layouts (below 34rem in Headlines, 43rem in Cards), since the three don't fit on one line and a wrapped line left a separator dangling.
  The article page names them all in a dateline under the title ("By … · date"), and its line above the title keeps only the feed, like a newspaper.
  First picked from layouts compared at 0, 1, 2 and 5 authors over authors in the meta line, which wrapped on phones, and a byline under a row's title, which made cards with authors taller.
  #688 moved them to the meta line after all: since #681 the phone kicker puts the feed's name over the time, and the authors share the time's line, cut rather than wrapped; that leaves the foot one line, where on phones the byline had a line of its own and pushed the actions to a second.
  At most 10 per article.

## Subscriptions

Planned in #139 (issues #135–#138).

- **Subscribing and unsubscribing are one click with no confirmation**, and redirect back to the page they were posted from.
- **Readers follow feeds: actions and states say Follow, Following and Unfollow** (#630, planned in #450), matching the tagline and the empty states' "Find feeds to follow", and the Follow buttons the first-run pages will add.
  "Subscriptions" stays where it names the OPML file and its import and export ("Import subscriptions", `subscriptions.opml`), since that's the word other readers use for the file and switchers look for it; "Edit subscription" stays too, as the page edits the subscription's name and tags.
  The privacy policy and terms keep their wording unless a sentence describes a button.
  Only text people read changed: routes, tables and Go names keep "subscribe" (`/feeds/{id}/subscribe`, `subscriptions`, `SubscriptionService`).
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
- **Offered in Settings, next to Export, on the empty timeline, and on Discover** (#450, #636).
  For people switching from another reader, import is the first run, and Discover is where new readers look for sites: a card at the top of Discover with no query while the reader has never imported and follows fewer than 10 feeds, then a one-line link at the end of the page.
  What the account has done decides, so there's no dismiss button and no stored state.
  While an import is running, the card shows its progress on load instead, without polling, and links to the import's page, which updates in place.
- **Files up to 1 MiB and 1,000 feeds, 5 imports per user an hour.** The upload is a multipart form with its own size cap, not `decodePostForm`'s 64 KiB.
- **The file input has no `accept` filter.** iOS turns `accept` into system file types, has none for `.opml`, and so greys out OPML files in the picker, including our own export.
  No `accept` value both narrows the picker and lets iOS pick `.opml`, and the filter was never a safeguard: the server's size and feed caps and `opml.Parse` handle any file, and a file that isn't OPML gets the usual error.
  (#367)

## Reading

Planned in #163 (issues #147–#162).

- **A timeline, not an inbox.** Read state is recorded but only shown as a quiet marker on read rows (tinted card, dimmed text and image).
  No unread counts, badges, "N new" indicators, "mark all read", or manual mark read/unread.
  No unread filter for now.
- **Hovering a card never looks like reading it** (#421): in the Cards layout hover never tints a card, on unread or read cards, in any theme, so the quiet read marker, a tint there, stays unambiguous.
  The Headlines layout (#680) marks read without a tint (a dimmed headline, kicker and picture), so there the hover is a tint; see "Design".
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
- **Rows are divided sections with a preview card**, one design for the timeline and feed pages: the feed's avatar and name with the time, then a card with the optional image, title and two-line excerpt.
  The whole card links to the in-app article page.
  Since #680 this is the Cards layout, one of two, and the link to the original is under the card, not in it (the authors are in the kicker, #688); see "Article rows have two layouts" under "Design".
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
  - Posts embedded from X become a post card (#698): the blockquote X's embed code leaves (and X's embed iframes) is read for the post's text, author, handle and link, and nothing loads from X, since X's script would track every reader and isn't allowed by the CSP.
    Nothing is fetched from X either: its API is paid, and the endpoint some readers scrape is undocumented and against its terms.
    The link is built from the validated handle and ID, which drops X's `ref_src` tracking; the time comes from the ID (X's IDs have encoded it since November 2010), so even a link-only embed has one; a picture link (`pic.twitter.com/…`) is dropped from the text and changes the card's link to "See the full post on X".
    Markup that isn't recognized stays a plain blockquote, never dropped.
    How the card looks is under "Design".
  - Posts embedded from Bluesky become the same card (#699), from the blockquote `embed.bsky.app` writes and its iframes, on the same terms: nothing loads from Bluesky, and the link is built from the post's validated DID (or handle) and record key.
    The time comes from the record key when it's a TID, as Bluesky's app makes every post's; otherwise the date as published.
    Bluesky's markup writes "[image or embed]" for a picture, a video, a link card or a quoted post alike, so it changes the link to "See the full post on Bluesky" and is never "Watch on".
- **Article HTML gets a reader mode's clean-up in `sanitize`, not from a library** (#718, from comparing the article page with obsidian-clipper's reader and Defuddle, the library it uses): Defuddle is JavaScript that extracts an article from a whole page, while a feed's HTML already is the article, so we take its fixes for what feeds send, one issue each.
  - Every id in an article gets the prefix `body-`, so none collides with the page's own, and links to them follow; a link to the article's own URL with a fragment that's in it becomes an in-page link (Substack's footnotes), and a fragment with no target in the article opens the original, rather than going nowhere (#719).
  - A feed's `<h1>` becomes a `<h2>`, and other levels stay, so the many articles that never use `<h1>` keep their `<h2>`s; a first heading that repeats the title is dropped, one further down is the author's and stays (#720).
  - Lazy-loaded images show: `data-src`, `data-original` or `data-lazy-src`, then a `<noscript>` image, replace a missing, empty or `data:` `src`, never a URL guessed from other attributes; an image left with no source is removed, with what only held it (#721).
- **Code blocks in articles get a Copy button the script adds** (#722): the sanitizer's policy allows no `<button>` in untrusted HTML, and the button does nothing without JavaScript.
  It shows only where the Clipboard API works and reuses Share's copied feedback, so copying looks the same everywhere.
  Every block gets a bar, with the language on the left when the feed names one and the copy icon on the right (the owner's pick at #722's design checkpoint, over a `Copy` text button floated into the block's corner and an icon shown on hover over the code): the bar gives the button its own space, so it never covers the code, and a bar on every block keeps the button in the same place, even where it holds only the icon.
  The icon alone is clear enough in a bar over code; its accessible name is "Copy code", and its label sits at the button's end, so it stays on screen at the block's edge.
  The language comes from what the common highlighters write (`language-go` and the like, on the block, its `<code>` or a wrapper, `data-lang`, and SyntaxHighlighter's `brush:`), read by the sanitizer into a display name from a fixed list, kept in `data-lang` on the `<pre>`: no class from the feed reaches the page, and an unknown or plain-text token gets no label rather than the feed's spelling.
  File names aren't shown: no highlighter marks them in a standard way.
  The bar is the `<pre>`'s `::before`, so it's never part of the copied or selected text, and without JavaScript a block whose language is known still shows it.
- **Long articles get a contents list rendered by the server** (#723), from their `<h2>`s and `<h3>`s when there are at least three, so it works without JavaScript and doesn't shift the page; how it looks is #723's design checkpoint.
  No current section that follows the scroll: it changes on every scroll, like the progress bar rejected in "Motion and feedback" (an idea in backlog.md).
- **Not taken from obsidian-clipper's reader** (#718): blending images into the page (`mix-blend-mode: multiply` in Light), since Light's `--bg` is nearly white so it would change almost nothing, and images in Dark stay as they are (#413, under "Design"); and its theme presets, custom CSS and font detection, which aren't this product.
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
- **Jobs exist only for wanted feeds: one someone follows, or one in Discover's catalog** (#708, changing #715's first-follower version of it, which left Discover's rows without pictures and unfollowed feeds' jobs behind).
  The worker claims only their jobs, and every change that makes a feed wanted or unwanted keeps the rule, so no job is left that nobody would claim: a feed becoming wanted (its first follower, or entering the catalog) gets its backlog queued, and one becoming unwanted (its last follower leaving, by unfollowing or deleting the account, or leaving the catalog) has its pending jobs dropped.
  A lookup's or import's save queues nothing by itself; an import's follow does.
  Until #708 the first save queued jobs whatever followed, and only followed feeds' were claimed, so every feed looked up but never followed, or unfollowed, kept jobs nobody claimed: thousands once the seed list grows (#649).
  Catalog feeds are wanted because Discover's section pages and Picks show their articles as rows, which need pictures like a timeline's; they cost what one follower of each would, growing with the catalog, not with readers (the watchlist entry "Enrichment of Discover's catalog").
  Rejected: queuing on the first follower only (Discover's rows stay bare, and unfollowing still leaves jobs), a periodic cleanup of unclaimed jobs (machinery for rows that needn't exist), and database triggers (the app uses none; the few places that change follows and the catalog are in `internal/psql`).
- **How far back:** a feed becoming wanted queues its 20 newest articles that need it and haven't been enriched, at a lower priority than new arrivals; after that, every new article that qualifies.
  A wanted feed's save placed as a first save (never fetched, or not attempted for two days, below) gets the same 20 at the same priority, rather than every article.
  Older articles wait for a low-priority backlog queue (a backlog item, for future search and related-article features).
- **Its own queue table (`enrich_jobs`) and runner**, one worker by default.
- **Polite, with a retry story:** one request at a time through `internal/fetch`; a network error, 429 or 5xx is retried after 1 hour, then 6, then given up; any other 4xx or a non-HTML response is given up at once, as is a fetch `internal/fetch` refuses (a blocked address, a body over 5 MiB, too many redirects), which a retry wouldn't change (#306).
  `robots.txt` isn't consulted: it's one fetch of a page a reader is about to see, like a link preview.
- **One page parser:** `discovery.ParsePage` reads Open Graph tags and icon links too (#298), for both enrichment and site icons.

## Design

A modern, minimal look modelled on social timelines, from the redesign in #181, moved toward a newspaper's in #680, since beside Bluesky the app looked like it.

- **Article rows have two layouts, Headlines (the default) and Cards, chosen in Settings → Reading** (#680, the owner's pick of three options after three rounds).
  Headlines: the feed's avatar and name as a kicker with the time, then a large headline across the row and the excerpt with the picture beside it (#695), and no frame; below 34rem (phones) the picture goes full width between the headline and the excerpt (#687).
  Cards: the layout from #181, the feed's avatar in a column of its own and a framed preview card under its name.
  Headlines is the default because the avatar column and the framed card were what made a row read as a social post, on phones above all, where every app has the same bars and the row is what's left; it also shows about twice the articles per desktop screen.
  Cards stays, as a setting, because a framed card reads as one thing to press without hovering, and gives pictures more room; it isn't a compatibility path, and both are designed for.
  Every change to article rows is designed, built and screenshotted in both.
  Rejected for Headlines: a thumbnail beside the headline on phones too (denser, but cramped), a frame around each article (clearest to press, but on a phone it was the card again), serif headlines (further from the app's own voice), and a masthead across the top in place of the rail (nothing on phones, and two sticky bars cost height).
- **In Headlines the picture is a social card's shape** (#687, the owner's pick from mockups in the review #685 tracks): 1.91:1 at 12rem, where it was 3:2 at 10.5rem, and a 24px headline, where it was 22px.
  Most articles' pictures are Open Graph cards (1200 × 630), which a 3:2 box cropped by about 10% a side, cutting the text many of them carry; the new box is about the same area.
  Two lines beside the picture showed no more of the excerpt than a phone does; #687 added a third, and #695 took it to five.
  The picture sits beside the text from 34rem, not 43rem, at a share of the card's width, so small tablets and split screens don't get a full-width picture that swamps a short excerpt; phones keep theirs, and their two lines.
  Rejected: letterboxing (bands around social cards and squares), the new shape at the old width (a smaller picture), a wider picture with two lines (the text narrowed until desktop showed what a phone does), and text running around the picture (whole excerpts, but taller, uneven rows that can't be clamped with an ellipsis).
- **In Headlines the headline runs across the row, and the excerpt sits beside a larger picture under it** (#695, the owner's pick of three sizes at its checkpoint): from 34rem the picture is 16rem (17rem in the wider column), up to 42% of the card's width, beside five lines of excerpt, where the headline and excerpt shared a line with a 12rem picture and three lines.
  Beside the picture a headline took two lines and pushed the excerpt down; across the row it mostly takes one, like a front page's over its columns, which pays for most of the larger picture's height.
  This reverses #685's finding that the picture should sit level with the headline to show they open together: the whole card still opens the article, and the headline now leads the row.
  With no excerpt, the picture goes back beside the headline rather than under an empty column; with no picture, the excerpt takes the line.
  An excerpt shorter than the picture leaves space beside it, as one shorter than headline and excerpt did before; the larger picture makes it more common.
  Rejected: a 19rem picture with six lines (the picture leads, but rows about 20% taller, and Headlines drifts toward Cards, which already gives pictures room), and an 18rem 3:2 picture with seven lines (whole excerpts, but rows about 35% taller, wide gaps beside short excerpts, and social cards cropped again).
- **The reading column is 44rem on screens 80rem (1280px) and wider, and 40rem below** (#689, the owner's pick at its checkpoint, from the review #685 tracks): on a large screen the extra space sat beside the column.
  It widens on every page that has the rail, so the rail never moves between pages; in Headlines the extra width goes to the picture (13.5rem, then 17rem in #695), and the excerpt keeps its lines.
  On the article page the text grows from 20px to 22px there, which keeps lines at about 73 characters, as in the narrower column; uncapped they ran to about 84.
  Rejected: capping the article text at today's width (a gap beside it), wider even margins on the article page and a 42rem column (both about 80 characters, giving back most of the width).
  The privacy policy and terms keep 40rem: they have no rail to line up with.
- **Only the card opens the article, and nothing else pressable is inside it** (#680, the owner): the link to the original, Follow and Star, Share and Save are on a line under it, in both layouts, so a press never has two candidates; the authors are in the kicker above it (#688).
  Before, the link to the original sat inside the card, raised above the card's link.
  In Headlines the frameless card shows itself by tinting when hovered or pressed; on a phone at rest nothing outlines it, which the owner accepted over a frame.
- **Corners are barely rounded and buttons are soft rectangles** (#680): 6 px on frames, pictures and controls and 4 px on small things, where cards had 16 px and buttons were pills.
  Rounded pills and big radii were part of the social look; older entries here that say "pill" mean the same controls in their new shape.
  The current section in the rail has a sunrise-colored edge, and the timeline tabs are set in the display face with a thicker sunrise underline.
  The admin keeps its rounder shape: its design was approved on its own (#562).
- **Light is Paper: the page, the borders and the tints warmed, with the column nearly white on a page a shade darker** (#680 added Paper as a fourth theme; the owner made it the light theme after Dark became Graphite).
  So the themes are a pair, Paper by day and Graphite by night, and System picks between them; a white Light theme beside it was one more look every page had to be checked in.
  A first version with a stronger paper tone read as muddy (#680).
  Feed names in the Headlines kicker are in the text color, not the sunrise: a colored label on every row was part of the muddiness.
- **In Headlines the feed's name is set as a name, and on phones it sits over the time** (#681, the owner's pick of three options after using #680): the avatar at 28 px beside the name in sentence case, bold, at body size, on one line with the time; on phones a 36 px avatar with the name over the time.
  #680's kicker, a 20 px icon and the name in small capitals, made the feed too faint, on phones above all, where who published a thing is half of what a reader scans for.
  A phone's line can't hold a name at this size beside the full date, so there it stacks rather than cutting names; on wide screens there's room, and one line keeps the row compact, cutting only a very long name.
  Rejected: the same small capitals a size up (still faint, and cut names on phones) and the stacked form everywhere (about 18 px more per row on desktop, for nothing it needed).

- **One centered column, nav on the left.** A rail with icons and labels on wide screens, icons only on medium ones, and a top bar (logo, logout) with a bottom tab bar on phones.
  CSS only; no JavaScript.
- **320 px is the narrowest width we support:** no page scrolls sideways at 320 px or wider (#431).
  A followed feed page shows Edit and Unfollow without a "Following" chip, since the red Unfollow already says you're following and the chip pushed the row off narrow phones; wrapping the buttons beside the avatar looked ragged, and moving them under the heading pushed the feed's facts down.
- **Lists are sections separated by dividers, not boxes.** A card is only for a preview of something elsewhere: an article now, Open Graph data later.
- **Posts from other networks are a card in the shape of the network's own embed** (#698, the owner's pick at its checkpoint, over four rounds), and Bluesky's (#699) and any later network's follow it:
  - A framed card (`--border`, `--radius`, like the Cards layout's) at most 34rem wide, about X's own embed's 550px, centered in the column; full width on phones.
    At the column's full width it read as a framed section, not a card.
  - The author's name over their handle, and the network's logo at the top right, with its "on X" text kept for screen readers; the text in the interface's face, not the article's serif, so it can't pass for the author's own words.
    When the embed names no author (an iframe, a link-only embed with an unknown handle), the header reads "A post on X" beside the logo, rather than leaving the logo alone (#699).
    A handle wraps only before a dot, like a web address, since Bluesky's are domain names that can be wider than a phone's card (#699).
  - Under a rule, the time in the app's own format (`local-times.ts`), and one link to the post, whose words say what the card leaves out: "Read on X ↗", "Watch on X ↗" for a video, "See the full post on X ↗" when the embed left out a picture, video or other attachment, after "Read on <site> ↗" on the article page.
  - Only what the embed's markup holds: no made-up avatar (a letter avatar is the app's own, and isn't what's on X), no box standing in for a picture we can't show, and no counts.
  - The logo is a file the stylesheet uses as a mask in the text color, not inline SVG or a `data:` URL, which the CSP blocks (see "Frontend assets"); X's and Bluesky's are the app's only third-party logos, as video previews name their site in words.
  - Rejected: an attributed pull quote in the article's own blockquote style (it looked like the author's own quote, the problem being fixed), click-to-load of X's own embed (it lets X track the reader once clicked, needs a frame host in the CSP and a script, and X's hosts couldn't be tested), a letter avatar and a picture placeholder (above), the card at the column's start (a ragged gap beside it) and a tinted panel without a border (it read as an aside, and nearly vanished in Dark).
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
  The dark palette keeps the light one's colors where they mean something: its sky for links and a lighter tomato (`--brand-strong`), with text at 7:1 or more; its greys are Graphite (below).
  The values live in one block per theme (repeated once for the media query and once for the explicit choice), so another theme is one more block.
- **Dark is Graphite: near-neutral greys with a hint of the logo's ink, and the column a shade lighter than the page, as in Paper** (the owner's pick of three options, after #413's navy).
  #413's dark used the ink, darkened, for the page and the column alike, which looked right on phones but was a wall of navy on desktop; the column now sits on a page about 1.1:1 darker, a little more than Paper's step since the eye sees steps less well in the dark, and phones, where the column fills the screen, only lose the navy.
  Rejected: keeping the navy and only darkening the page (the smallest change, still navy) and a warm charcoal as Paper's night side (read as brown).
  Graphite was also picked as the dark half of the Paper and Graphite pair, which the themes became next.
- **Dark keeps the light theme's color where color means something, and stays calm everywhere else** (#413).
  Avatars are mid-tone colored tiles with near-white letters, the dark counterpart of light's pastel tiles; the error, flash and debug boxes are mid-tone tints with light text (`--on-danger-bg` for errors); the sky accent is saturated.
  The page, cards, text and images stay low-chroma, since saturated large areas glare at night.
  A first version mixed the tints almost into the background and read as dull next to light.
  Logged-out and error pages always follow the system, since they don't know the user, and the installed app's splash stays light, since the manifest can't vary.
  The browser bar (`theme-color`) follows the user's choice on logged-in pages, so a dark page doesn't sit under a white bar; the issue had accepted the system's, but the page knows the choice, so it costs one argument per page.
- **Read cards stay quieter than unread ones in both themes** (#413): they have their own tint (`--read-bg`), a warm tint in light and darker than the page in dark, since the lighter `--surface` would make read cards stand out at night.
  That is the Cards layout; in Headlines a read row dims its headline, kicker and picture instead (#680), having no card to tint.
- **The danger color is lighter in dark** (#413), so error text stays readable, and the Delete button's text is the dark background, at about 8:1.
- **Reading text is a little heavier in dark** (#413): Newsreader at weight 440 rather than 400, since light text on dark reads thinner.
  Pure white text is avoided, since it glares.
- **The logo is one drawing in both themes:** its ink ground has a ring in the link blue, which closes the circle on the dark background and all but disappears against the ink on white and on tomato.
  A second drawing for dark (a paper-colored or blue ground), a blue ground in both themes, a drop shadow and a glow were the alternatives; the owner preferred ink on white and tomato, and one file serves the rail, the favicon and the install icon.
  Site icons keep a light tile in dark, softened to the text color, since many are drawn for white.
- **Images in articles are left as they are in dark** (#413), even transparent diagrams and logos with black text, since a white tile behind every image looks worse for the common case; revisit if testers report them.
- **The app is called Paper Round** (paperround.app, #408, #409), written as two capitalised words wherever users see it: page titles, the nav, emails, the OPML export, the shared feeds' generator and the fetcher's User-Agent.
  The repository, Go module, `rss` package and `RSS_` variables keep their names, since nobody outside the code sees them.
- **Discover lists its catalog as sections of feed rows, each with a "Latest posts" peek** (#633, option (a) of three at its design checkpoint): a row shows the site and its host, the feed's own title when it isn't just the site's name, "About N posts a week · Latest 2 days ago", and a `<details>` with its 3 newest titles, then Follow.
  Section chips at the top scroll sideways on phones rather than wrapping; each section is a `list-head` and rows, like My feeds; three feeds a section.
  The meta line wraps between its parts, never inside one.
  Follow is a primary pill until pressed, then a quiet "Following", and its accessible name includes the feed's title, so a site's two catalog feeds aren't both "Follow Bitwise".
  Rejected: the newest title inline in place of the peek (one post stands for the whole feed, each row is a line taller, and article text in Discover's lists reads as a second Home), and a two-column grid on wide screens (the reading column is about 600 px even at 1280, so tiles broke hosts mid-word and left holes, for a second layout to maintain).
- **Discover offers import in a bordered card above the section chips** (#636, option (a) of three at its design checkpoint): a heading, a line of copy and a primary Import pill, or, while an import runs, its progress with a bar and a Details link, in the same frame.
  It's the one exception to "not boxes" on Discover, outlined (`--border`, `--radius-sm`) with no fill, since it's a call to action for switchers, like the email notice, not a list item; the end-of-page line is quiet centered text.
  Rejected: a row like a site row with an upload icon (reads as one more catalog row, with no clear button) and a line under the search box (the same size as the end line, easy to miss, and no room for the progress bar).
- **A Discover section's page has two tabs, Feeds and Recent posts** (#634, option (a) of three at its design checkpoint), each its own page, in the timeline tabs' style, under a head with "Discover" as a small link above the section's name; each section on Discover links to it as "All N ›", N counting every catalog feed in the section, followed or not, so it matches the Feeds tab.
  Feeds reuses Discover's rows, with a quiet "✓ Following" linking to the feed where Follow would be; Recent posts are article rows naming the feed as Follow does (site and feed title), with an outlined Follow before Star, Share and Save, since a filled pill on every row of an unfollowed feed outshouts the posts.
  The small "Discover" link is the only way up: no breadcrumb trail, since there's one level, and a back link is ruled out below.
- **A site's page lists its feeds compactly above its recent posts** (#635, option (f) at its design checkpoint, after three rounds), since readers often come to a site to pick a feed to follow and also choose by reading: on a phone the choice and the first post share the first screen.
  Each feed is a short row without an avatar (the site's is in the head): its name without the site's ("The Daily Ledger – Opinion" reads "Opinion"; Follow's accessible name keeps it), "{Section} · About N posts a week · Latest {when}", the section in the accent color and linking to its page so the feed Discover picked stands out, and Follow, or a pressed "Following" that unfollows, since this page is where a site's feeds are managed.
  The list shows 4 feeds, Discover's first, then the most recently posting, with the rest behind "Show N more feeds", so a site with many feeds keeps its posts in view.
  A site with one feed has Follow and its facts in the site's head instead of a list, laid out as a feed's page's head: with nothing to choose between, following it can't pick the wrong feed (the concern that dropped "Follow the site", #629).
  Posts have neither Follow (the list holds it) nor an avatar, and name their feed only when they come from more than one; when they're from Discover's feeds only, a line says so, so the other feeds' missing posts aren't a mystery.
  "Check for new feeds" is a quiet line at the page's foot, a `.link-button` in a sentence, with when the feeds were last checked, as the rarest action on the page.
  Rejected: posts first with the feeds below them (the choice is below the fold on phones), feeds with their latest post first (the writing is below the fold), a tab per feed (one feed's facts at a time, and messy), Recent posts and Feeds tabs as on a section's page (choosing a feed takes a tap first, and posts can't show a feed's rate or section), and the compact list with Follow on every post (a dozen identical buttons).
  Rejected: one page with the feeds above the posts (on a phone the posts start screens down, and paging them reloads the list), and posts first under a sideways strip of feed cards (the strip shows two or three feeds without what helps choose, and it's a second feed-row layout).
- **Home's empty state is a message with two buttons, Discover primary** (#639, option (a) of three at its design checkpoint): "Nothing on Home yet." as a bold lead in the text color over the muted explanation, then "Find sites in Discover" (primary) and "Import from another reader" as pills, side by side on wide screens and stacked on phones.
  "Getting the latest from …" uses the same lead, with the spinner above it, and the end of All feeds is quiet centered text under the last row, like Discover's end line.
  Rejected: two rows with icons like Settings' list (heavier, and it borrows the look of settings for something that isn't one) and the old sentence with links (the two ways in were easy to miss).
- **Discover's nav entry takes the magnifier, and its search results are rows of one shape under timeline-style tabs** (#645, approved at #664's design checkpoint, rounds 5–7).
  The magnifier says "search" where the compass said "explore"; #664's first round had approved a separate magnifier in the phone's top bar for a Search page, which went with the page.
  A result is an avatar, the title, one line of tagline (a site) or two of excerpt (a saved article, in the reading font, its title in the headline font), and a facts line, with "✓ Following" or Follow at the end; a saved article's facts lead with Saved's bookmark, which is enough to tell it from a feed, so Top has no "kind" label.
  Tab counts follow Feeds and Saved, dimmed when 0 and hidden under 360 px so the three tabs fit; a pasted address's row has a link icon in the avatar's place and a filled Look up.
  Rejected: each group in its own page's rows (My feeds' and Discover's), three shapes on one page; compact one-line rows after Enter (too little to choose by); and a small "kind" line above every title (redundant once each row shows its state).
- **A search box submits from the magnifier at the field's start, with no Search button beside it** (#665, approved at #664's design checkpoint, round 7), on Discover's and Saved's boxes alike.
  The text button took about 110 px from the field on a phone, cutting off Discover's placeholder at 320 px, and was a second filled button beside Follow; X, Bluesky and LinkedIn have none.
  The magnifier is a labelled submit button drawn as the bare icon, so it works without JavaScript and adds nothing to the screen; Enter and a phone keyboard's Search key submit too, and with JavaScript Discover's "Search everything" row (#666) is the visible, labelled way.
  Rejected: hiding the text button when scripting is on (the magnifier would do nothing, and a script that failed to load would leave no visible way to submit), an arrow button at the field's end (crowded beside the browser's clear ×), and a round icon button beside the field (two magnifiers side by side).
- **"Log in" and "Log out" are verbs, and "Login" never appears in the UI** (headings, buttons, links, page titles), because buttons name actions.
  The `/login` route, `loginHandler` and other identifiers keep `login`: nobody sees them.
- **The logo is a round sunrise:** a circle in which the sun rises over an ink ground, whose two white lines, thick over thin like the nameplate rule, read as the sun's reflection and as lines of text (`web/app/public/img/logo.svg`).
  It's flat, with no outlines, and five shapes, so it reads at 16 px and the favicon is the same file.
  It replaced a front page (a newspaper with a P nameplate and the sunrise as its lead photo), whose dozen thin-stroked parts blurred at the rail's size.
  A flat redrawing of that front page (needed a second drawing for dark) and a P monogram of an ink bar and a tomato half-disc (lost the sunrise) were the alternatives; left-aligning the lines as a headline was dropped for its asymmetry.
  The first logo was picked over a letterbox (read as a ballot box), a mailbox (too close to email), a delivery bike, and a route drawn as a P (needed explaining).
- **The palette comes from the logo:** ink for text, sky blue and tomato from the sunrise; surfaces and read rows had a sky tint until light became Paper, whose tints are warm.
  Green and gold, maroon and an orange box were tried and dropped.
- **Tomato is the one accent, used sparingly** (#414): the active timeline tab, starred articles (so Star no longer looks like Saved's blue) and the "You were here" marker.
  It's a deeper shade than the logo's (`--brand-strong`), so the marker's label passes AA as small text.
- **Text colors pass WCAG AA** (4.5:1) on the column, the page and the read-card tint (#414); the lightest grey was 3.2:1 before.
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
- **A change to how the app looks is shown and agreed before its PR is ready for review** (#309, for #299 and #308; widened from open layout questions to every visual change in #425): a draft PR with the options behind one switch, screenshots at desktop and phone widths (committed under `.screenshots/` while the PR is a draft), and a stop until the owner picks one in a PR comment.
  Every option comes with a justification (what it fixes, why this way, and what it trades away), so the pick rests on reasons that can be recorded here rather than on taste alone.
- **Every design checkpoint shows several options that differ in approach, and recommends one** (the owner, during #575; this replaces "one option is enough when it's clearly right").
  Variations of one idea (a color, a spacing) don't count as options: a recommendation can only be judged against real alternatives, and a single option leaves the owner approving or rejecting rather than choosing.
  The recommendation says why, so a pick against it is a deliberate choice.
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
- **Images show as they download, without a fade** (#686, the owner, closing #525's experiment from #683): an image held at opacity 0 until its last byte showed later than one the browser draws as it arrives, most of all on infinite scroll, where nearly every image was still loading, and its `--surface` placeholder was hidden with it.
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
- **The sunrise** (#523, approved at its design checkpoint): on the landing page and Log in, once per load, the logo's sun rises into place from 14 units lower (of the 64-unit drawing, so its top just shows) over 900 ms on `--ease-out`, after a 200 ms pause so the page has settled.
  The ground is drawn over it, so it never paints over the water.
  The sky brightens with it, a deeper blue fading to the logo's own over the same time, so it reads as dawn rather than a shape sliding up; the deeper blue is an overlay whose opacity fades, so only opacity and transform animate and `main.css` names no color.
  The logo is drawn inline on those two pages only (the rail and the other logged-out pages keep the `<img>`), and it's still under reduced motion and in forced colors.
- **"You were here" draws itself in** (#522, the issue's current thinking, taken without a design checkpoint at the owner's word): the first time at least half of the divider is in view on a page load, its two rules grow outward from the label (scaled along the line from 0, the left from its right end and the right from its left end) over `--duration-flourish` on `--ease-out`; the label never moves or hides, so the words read on every frame.
  It plays once per page load, not with the scroll position, since a scroll-driven line replays back and forth and wears out; scrolling past it again does nothing, and a divider infinite scroll appends draws the first time it's seen.
  One already in view at load waits until 200 ms after the script's setup, so it plays once the page crossfade (#513) has finished.
  Scaling a pseudo-element was taken over clipping it (`clip-path`): both are cheap, but a scale needs no inset arithmetic per side and reads the same on a 1 px rule.
  The script only marks a divider pending when motion is allowed, and the pending state is itself inside the motion and `forced-colors: none` query, so without JavaScript, under reduced motion and in forced colors the line is simply drawn.
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
  The highlight is a radial gradient about 8% lighter than tomato, fading to flat by the middle, and touches only the background so the logo stays flat; a 4% version and flat tomato were the alternatives.
  The favicon is the logo on no background, since browser tabs have their own.
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
- **No assets inlined as `data:` URLs** (`assetsInlineLimit: 0`, #698): the CSP's `img-src` doesn't allow them, so a small image a stylesheet uses, like a network's logo, would be blocked; emitted, it's one small request, cached for a year.
  Adding `data:` to the CSP was rejected: the CSP is the backstop for untrusted article HTML.
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
- **Signup needs an invite.** (#186 started with a `-signup` flag, turned on briefly to make accounts; #226 replaced it with the shared code below, and #569 replaced the code with single-use invites; see "Waitlist and invites" under "Accounts".)
- **A shared invite code replaced `-signup`** (#226; itself replaced by single-use invites in #569, so `-invite-code` is gone).
  Signup is open exactly when `-invite-code` is set; the bool flag goes rather than staying alongside it.
  - The code arrives pre-filled from `/signup?invite=…` into a visible field, so it can also be typed.
    While signup needs an invite code, no page links to `/signup`: without a code it's useless, and advertising it only invites guessing, which the per-IP signup limit (#182) bounds.
    Whatever opens signup more widely adds the links back.
  - At least 16 characters, compared in constant time.
  - In production it's set in Render's dashboard and ignored by Terraform, like `RSS_SMTP_PASSWORD`: a Terraform variable would keep it in plain text in the state and need a PR and an approved apply to rotate.
  - It may appear in request logs (they log the URI); only the owner reads them, and the owner sets it.
- **`www create-user` makes accounts locally** (#225; now `admin create-user`, #568), and `make db/reset` uses it to put your account back.
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
  The admin sends the selected entries' addresses to the seed list's upload form (#714).
  Rejected: "Which feed reader do you use now?", whose answer wouldn't change anything we do; "What do you mostly read?", too vague to act on; and "How many feeds do you follow now?", useful only for planning waves by load, which one more field on the form doesn't justify yet.
  Double opt-in: a confirmation email, so nobody can add someone else's address and invites only go to addresses that work.
  Every waitlist email has a link to leave the list.
- **A second optional question, "What do you like to read?", with Discover's sections as check pills** (#642, planned in #450; option (a) of three at its design checkpoint), under the sites question, with the hint "We'll show these sections first in Discover."
  This reverses #566's rejection of "What do you mostly read?" (above): with sections the answer is something the app acts on, ordering Discover; see "First run and discovery".
  It's stored on the entry and copied to the account at signup, since the entry is deleted then, and the admin shows it next to the sites answer.
  The pills are the timeline form's tag pills (#483), moved to the shared styles as `.check-pill`, so a set of checkboxes looks the same wherever it's offered; every section shows at once and wraps in about five rows on a phone.
  Rejected: a plain checkbox list (fifteen rows on a phone, pushing Join far down) and a dropdown of checkboxes (compact, but it hides the choices being made and can't show what's picked without a script).
- **The answer is stored as section IDs, not names** (#642): #628 moved the list of sections from code to a table after #642 was planned, with feeds referring to a section by its ID, so a renamed section is still the one people chose and a dropped one is left out where the answer is read.
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
  Signup itself stays unlinked while it needs an invite ("A shared invite code replaced `-signup`" under "Security").
- **One link token per entry, for both its links, that doesn't expire** (#566): the leave link must keep working in later emails (the invite, #574), and joining again while unconfirmed replaces it, so only the newest confirmation link works.
- **Single-use invites replace the shared invite code** (#569): `RSS_INVITE_CODE` goes.
  An invite is bound to a waitlist entry's email (signup fills it in and locks it, and the account starts confirmed, since the address was confirmed when joining) or open, for people the owner invites by hand.
  Invites expire and can be revoked; the token is stored hashed, like email links.
- **Only a confirmed waitlist entry gets a bound invite** (#569): a bound invite's account starts confirmed, so `admin invite create -email` refuses an address that isn't confirmed on the waitlist; anyone else gets an open invite, with a note.
- **Signing up deletes the account's waitlist entry**, by the invite's entry and by the account's address (#569): someone with an account no longer waits, and joining already refuses addresses with accounts.
- **An unusable invite says why, with no form** (#569): expired, used ("Log in"), or no longer valid (revoked or unknown), as 410 Gone like the expired email links; `/signup` with no invite says the app is invite-only and links to the waitlist.
  Redemption is one conditional `UPDATE`, so two signups racing on one invite can't both succeed; the loser sees "already been used".
- **Invites go out in waves from the admin** (#574): pick people (checkboxes, or "the oldest N"), review, send.
  No open or click tracking.
- **The email limit is checked before a wave is sent** (#574): emails queued in the last 24 hours and still pending, against the daily and monthly limits (Resend's free tier: 100 a day, 3,000 a month), less a reserve for account emails, so a wave can't use up the day and block a password reset.
  Past 80% of the day it warns; past the limit it says how many fit and offers to send just those.
  Moving to SES changes only the limits (`-mail-daily-limit`, `-mail-monthly-limit`, `-mail-reserve`).
- **The reserve is 10 emails by default, not 20** (#574, at its design checkpoint): with a daily limit of 100, a reserve of 20 put "past 80% of the day" and "past what fits" on the same line (80), so the warning could never show; 10 leaves it the band from 80 to 90.
  Rejected: warning at 70%, which warns about waves that leave a fifth of the day free.
- **An entry whose invite expired or was revoked is waiting again** (#574, at its design checkpoint): it's on the Waiting tab, with a status saying so ("Invite expired {date}"), and the next wave can include it; a pending invite is what makes an entry Invited.
  Rejected: keeping it on Invited until someone acts, which hides it from waves.
- **Sending an invite gives the entry a new link token** (#574): the invite's email carries a leave link, the entry's token is stored hashed, and the entry is confirmed already, so its old link only left the list.
- **A wave is sent in one transaction, checked again as it's sent** (#574): the invites, the entries' new tokens, the emails and one `invite.wave` audit entry commit together, under an advisory lock, after checking the limits and that everyone is still waiting; if anything changed since the review, nothing is sent and the review is shown again with the numbers as they are now, so two tabs can't both send.
- **Emails are counted per UTC day in their own table** (#574, `email_daily_counts`), incremented in the statement that queues each email, since the outbox deletes finished emails after 7 days and a month can't be counted from it.
- **The waitlist page's look** (#574, approved at its design checkpoint, PR #606): underlined tabs with their counts (Waiting, Invited, Unconfirmed), "Select the oldest N" above the table, and a bar fixed at the foot of the screen with "{n} selected" and Review invites, so the button stays in reach while scrolling a page of 100.
  Only the Waiting tab has checkboxes: only waiting people can be invited.
  Rejected: the counts as a row of figures that are the tabs, with the actions in the page head, which crowds the head on phones and scrolls the button away.
- **The review shows the day's limit as a bar** (#574, approved at its design checkpoint): what's used, this wave, the reserve at its end and the 80% mark, over the issue's sentence, which carries the same numbers for screen readers; people who don't fit today are dimmed and marked "Waits".
  Rejected: the numbers as a row of figures, which leaves the sums to the reader.
- **An invite made by hand with an email needs a confirmed waitlist entry with no pending invite** (#607), as `admin invite create -email` needs a confirmed one: its account starts confirmed, and a second pending invite would only duplicate the first, which can be resent instead.
- **Resending replaces the invite's token and expiry, 14 days from then** (#607), rather than making a new invite, so the wave's summary still counts it once; it's checked against the limits like a wave.
- **The invites page and invites by hand are their own issue** (#607, split from #574 at its design checkpoint to keep the PR reviewable): the waitlist, the review, sending a wave and the invite email came first.
  The invites page's agreed look, a Waves table (sent, signed up, active after 7 days) above one list of every invite, newest first, goes with it; rejected there: one list grouped under each wave's summary.

## Deployment

Planned in #218; issues #208–#217.

- **Render, Virginia (US East).** A Starter web service (always on, 512 MB) and the smallest paid Render Postgres 17, about $15 a month with backups.
  Chosen over a VPS (no server to look after), AWS ECS/RDS (about $50–60 a month before there are paying users), Fly.io (its managed Postgres starts at $38) and Neon (the refresh workers poll every 15 seconds, so it never scales to zero).
  Check prices again before the first `terraform apply`.
- **Portable by contract, not by host.** Every host-specific piece sits behind the same six things, so moving to Fly, AWS or a VPS is a new Terraform module, a new deploy step and a DNS change, with no app code changes: one container image built by GitHub Actions and pushed to GHCR; configuration from `RSS_*` environment variables; a `www migrate` command run before new code goes live (Render's pre-deploy command, Fly's `release_command`, an ECS one-off task); `/healthcheck` for the host's health checks; JSON logs on stdout; plain Postgres 17.
  Don't add host-specific code paths.
- **Terraform**, with the official Render provider and the Cloudflare provider (for R2).
  State lives in an R2 bucket.
- **Terraform is interrupted before its job's time limit, and a stale lock is released from Actions** (after a plan that hung on the Cloudflare provider was killed at the 10-minute limit and left the state locked, during #606).
  A killed job can't release the lock, but an interrupted Terraform does, so plans stop at 10 minutes (the job at 15) and applies at 25 (the job at 30).
  The unlock workflow needs the owner's approval in the `infrastructure` environment and only the state's credentials, so a lock can be cleared away from a computer.
  Rejected: plans with `-lock=false`, which would let a plan read the state while an apply writes it.
  After the first apply (by hand, during setup), applies run only from GitHub Actions, one at a time.
- **The domain is Cloudflare DNS plus a Render custom domain, both in Terraform** (#218, #254).
  Which domain is under "Accounts".
- **Migrations are built into the binary** (`www migrate`) and run before each deploy, not at app startup, so they also work with several instances, and a failed migration aborts the deploy while the old version keeps serving.
  One artifact carries the code and the migrations it expects, and the same command runs everywhere.
- **Wiping the database is never automatic.** `migrate` only applies new migrations; nothing in the app or its workflows drops the schema, and only `make db/reset` rebuilds a local database.
  - Until #561, while production was reset on schema changes, `migrate` refused to run when `migrations/*.sql` had changed since they were applied, and wiped only when given the new migrations' fingerprint (a `schema_fingerprint` table, #211); production resets went through a workflow with a typed confirmation.
  - #561 ended production resets (see "Pre-alpha testing"): the fingerprint check, `-allow-reset-to` and the reset workflow went, migration `00002` drops `schema_fingerprint`, and a CI step fails a PR that edits, renames or deletes a migration already on `main`, which replaces the check.
- **No account is seeded after a production reset.** It would need a credential (or its hash) stored in Render, Terraform state or GitHub, and a one-off job to run `admin create-user` in.
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
  The admin sets 3 (#573): its record is proxied, for Access, which adds the `paperround.app` zone's Cloudflare in front.
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
- **PRs stay small enough to review in one sitting** (#567): #567's login came to about 3,300 lines across 55 files, including a refactor it needed first and an optional terminal QR code, which together could have been two smaller PRs.
  Splitting is suggested before building, and only along seams where each part stands on its own; a change that only makes sense whole stays one PR.
- **Docs stay in the same PR as the code they describe**, even though separate docs PRs would conflict less: they would let the docs fall behind.
- **The admin and its observability have their own roadmap**, the [Admin Roadmap issue](https://github.com/grodier/rss/issues/560), at the owner's request, so the reader's roadmap stays readable; it works like #385, and neither edits the other's waves.
  (#583)
- **Measure the reading queries before planning load testing** (#388), with a dev-only seed tool outside `cmd/www`, so seeding code never ships, measuring the repository methods themselves rather than copied SQL.
  The timeline query collapses copies over a user's whole history before paginating, so it's the likeliest to break first; its numbers decide what load testing covers.
- **The first error-state pass is by hand, with no tool** (#392), and fixes nothing itself: it shows how many states there are and how much needs work before deciding whether a walkthrough tool is worth building.
