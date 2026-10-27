# Backlog

Work that has no GitHub issue yet, and the Performance watchlist.
What's planned, and in which order, is the [Roadmap issue](https://github.com/grodier/rss/issues/385): its sub-issues are the filed work.
When an item here gets an issue, the planning work deletes it from this file and adds the issue to the Roadmap.
Items marked "needs planning" are too large or open-ended to hand to an agent as-is; plan them first and split them into issues.
How to keep this file up to date is under "Docs" in CLAUDE.md; decisions already made are in [decisions.md](decisions.md).

## Pre-alpha testing

What's needed, after deployment, before other people use the app on a public URL.
Running it locally for yourself needs none of this.

- [ ] **Single-use invites.**
  A table of invites with expiry and a CLI command to make them.
  Only once people we don't know personally are invited.
  When the code comes in the link, show "Invited" instead of the text field.
- [ ] **Review the privacy policy and terms** (`privacy.html`, `terms.html`, #269).
  They were drafted without legal review and name George Rodier, in the United States, as who runs rss.
  Before launch: put in the product's name, and a business if one runs it; name a state for governing law (the terms only say "the United States"); check each section against what the app then does (processors, retention, the Settings features it mentions); have them reviewed; and bump "Last updated".

## Accounts

Deferred while planning account management (#270).
The decisions are under "Accounts" in decisions.md.

- [ ] **Passkeys.**
  Log in with Face ID, a fingerprint or a security key, with no password to make up at signup, and phishing-proof.
  Offered alongside the password form, since WebAuthn needs JavaScript.
  Needs a dependency (`go-webauthn/webauthn`) and a `passkeys` table (one user, many passkeys), and makes `users.hashed_password` nullable for passkey-only accounts.
  The best login upgrade once #254–#269 are in.
- [ ] **Sign in with Google or Apple (decide before production).**
  One-tap signup, but a third party learns who uses the app, linking accounts that share an email must only happen with confirmed addresses, and Apple charges a developer fee.
  If it's planned, revisit whether credentials move to their own table (the password hash stays on `users` for now).
- [ ] **Devices list.**
  Settings lists each logged-in device ("Firefox on macOS · active 3 days ago") with its own Log out, from `user_sessions` plus a device label taken from the User-Agent at login.
  No IP addresses or locations, which aren't kept outside the logs.
  Settings only has "Log out of all other devices" so far.
- [ ] **Email typo suggestions.**
  "Did you mean gmail.com?" under the signup email field, for near-misses of common domains.
  Until then, the confirmation notice shows the address you typed.
- [ ] **Rehash passwords when the Argon2 parameters change.**
  On a successful login, hash the password again if its stored parameters are older than `password`'s current ones.
  Only matters once the parameters change.
- [ ] **Have I Been Pwned check.**
  Reject passwords found in breaches, with the k-anonymity range API (only the first 5 characters of the password's SHA-1 leave the server), on top of the common-password list (#256).
  A third-party call per new password; allow the password if the API is down.
- [ ] **Bounces and complaints.**
  Record hard bounces and spam complaints from the email provider's webhooks in a suppression table we own, skip those addresses in the outbox, and tell the user in the app that we couldn't deliver to their address.
  Needed before the move to SES.
- [ ] **Move email to Amazon SES.**
  When Resend's free tier (100 emails a day, 3,000 a month) gets tight or costs more than SES.
  Sending is SMTP (#259), so the app only changes configuration.
  The rest: request SES production access (it asks how bounces and complaints are handled; see above), verify the domain with DKIM, set a custom MAIL FROM domain, move DMARC from `p=none` to `quarantine` once its reports are clean (the app has no `_dmarc.rss` record yet: add one, or `sp=` on `georgerodier.com`'s), and check the result with mail-tester.com.
- [ ] **Product name in the app.**
  Moving to the new domain is #389; the app still calls itself "rss" in page titles, emails and the policy pages.
- [ ] **Support address on the new domain.**
  The support address stays `rss-support@georgerodier.com` after #389; moving it needs Email Routing on the new domain's zone.
- [ ] **Full data export.**
  A JSON file with your account, subscriptions, reading history and saved articles (#299), next to the OPML export.
- [ ] **Clean up abandoned unconfirmed accounts**, e.g. delete accounts never confirmed and unused for 30 days after signup.
  Only if they pile up.
- [ ] **Support tools.**
  `www` commands can't run in production, so the self-service flows are the support path.
  If support requests need more (looking up an account, resending a confirmation for someone), plan an owner-only admin page.
  It would build on debug mode's allowlist (#327), and is where cross-user data such as subscriber counts belongs.
- [ ] **First-run experience (needs planning).**
  What a new account sees after signup: today, an empty timeline pointing to Search and to OPML import.
  Perhaps suggested sites or a short tour.

## Plans and billing

- [ ] **Paid plans and billing (needs planning before launch).**
  What's free and what's paid, a payment provider (e.g. Stripe), what happens when a subscription lapses, and receipts and refunds.
  Limits that already exist (20 timelines, 1,000 feeds an import, how often feeds are fetched) may become plan limits, so plan this before features depend on them.
  Selling inside an iOS app means Apple's in-app purchase and its 15–30% cut, under rules that have been changing (linking out to pay on the web): one common pattern is to sell on the web and let the app only log in.
  Decide that with the native shell (under Mobile), and before pricing, since it changes the margins.
  The privacy policy and terms need updating for payments and the processor.

## Deployment

- [ ] **Upgrade `render-oss/render` once its digest fix ships.**
  Provider v1.9.1 can't update a service pinned by digest, which deploys always leave `rss` and `rss-backup` (since #386) on: it sends `ghcr.io/grodier/rss:<hex>` and Render refuses with "unable to fetch image with provided input" ([render-oss/terraform-provider-render#106](https://github.com/render-oss/terraform-provider-render/issues/106), fix in PR #109).
  The #254 apply hit it.
  When a release includes #109 (Dependabot opens the upgrade PR; check its changelog), upgrade, check that a plan changing the service applies without the workaround, and remove "Changing the web service or the backup job" from `infra/README.md`.
- [ ] **Drop the R2 lifecycle rules' `ignore_changes` once the Cloudflare provider is fixed.**
  Provider 5.28 reads back an empty condition for the transition a rule doesn't use, which no valid config can match ([cloudflare/terraform-provider-cloudflare#7338](https://github.com/cloudflare/terraform-provider-cloudflare/issues/7338), closed without a fix), so #387 ignores those transitions in `infra/backup.tf`.
  When a provider upgrade's changelog mentions `r2_bucket_lifecycle`, remove the `ignore_changes` and check that the plan is still clean.
- [ ] **PR preview environments.**
  Render preview environments need the Professional workspace (about $19 per user a month plus compute).
  Only from same-repo branches: never `pull_request_target` with fork code, since this repository is public.
- [ ] **Leaving pre-alpha.**
  Remove the fingerprint check, `-allow-reset-to` and the "Reset production database" workflow, and make schema changes new migrations (see CLAUDE.md).
  From then on every migration must also work with the version still running during a deploy ("expand/contract": add, deploy code using both, backfill, deploy code using the new, then drop the old).
  Give migrations their own Postgres role that owns the schema, and the app a role that can only read and write data.

## Security

- [ ] **Per-email login lockout.** #183 lets anyone keep a known email locked out of login for 15 minutes at a time by failing logins for it.
  That's accepted while users are a handful of known people, and from #265 a locked-out owner can still get in by resetting their password.
  Revisit before signup is open to strangers (e.g. key the limit on IP and email together, or slow down with backoff instead of refusing).
- [ ] **Bot protection on signup.**
  Before signup opens beyond invite codes, add a challenge (e.g. Cloudflare Turnstile, which needs a script and a CSP change) or another bot check.
- [ ] **Rate limiter approach.**
  Evaluate whether the in-memory rate limiter (`internal/server/ratelimit.go`) is right long term.
  It resets on restart and isn't shared across instances.
  Before running more than one instance, or if limits need to survive restarts, compare options (e.g. Postgres-backed counters, a reverse-proxy/edge limit, Redis) and decide.
  No decision yet.

## Feed discovery

- [ ] **Live updates for pending work (lookups and feed refreshes).**
  Replace the lookup status page's `<meta http-equiv="refresh">` with JavaScript polling, server-sent events or websockets, and use the same mechanism to update a feed page after the background refresh it requested (#137) finishes: the request is pending while `feeds.refresh_requested_at` is set.
  Progressive enhancement: pages keep working without JavaScript.
- [ ] **Logged-out access.**
  Let logged-out visitors search and view site and feed pages, to help people find the app.
  Keep lookups login-only.
- [ ] **Name → website suggestions.**
  For free-text queries ("the verge"), suggest likely websites ("Try theverge.com?") the user can click to look up.
  Don't fetch them automatically.
- [ ] **Platform-specific discovery rules.**
  YouTube channels, Reddit, Substack, Medium, GitHub releases, etc. Includes sites that share a host and are identified by path (`medium.com/@user`), which the host-based site key can't tell apart.
- [ ] **`<a href>` heuristics.**
  When a page has no `<link rel="alternate">`, look for links whose URL or text mentions RSS/Atom/feed before probing common paths.
- [ ] **Search autosuggest** showing matching sites and their related feeds as you type.
- [ ] **Better search ranking.**
  `pg_trgm` or full-text search instead of `ILIKE` substring matching, with an index.

## Feed ingestion

Each with when it pays off:

- [ ] **Adaptive refresh interval.**
  Fetch busy feeds often and dormant ones rarely, from posting frequency: count articles published in the last 14 days (`n`), interval = clamp(14 days ÷ 4n, 15 min, 24 h), 24 h when `n = 0`, then apply the `Retry-After` / `Cache-Control` floors (#133).
  Worth it at a few thousand feeds, or when most fetches are 304s or find no new articles.
  `feeds.posts_per_week` (#350) is a 30-day version of `n` the schedule could use.
- [ ] **Permanent redirects (301/308).**
  Update `feeds.url`.
  Needs `fetch` to report the redirect chain, and a decision for the unique-URL collision when two feeds end up at the same URL (merge, or keep the old one).
  Redirects are already followed, so this can wait until moved feeds are a visible problem.
- [ ] **Give up on long-dead feeds.**
  E.g. stop after 30 days of consecutive failures and say so on the feed page.
  When dead feeds are a noticeable share of fetches.
- [ ] **Per-host politeness.**
  At most N concurrent fetches per host.
  When one host has many feeds (category feeds, platform hosts like Substack or Medium).
- [ ] **Separate worker process or `-once` mode.**
  Run the background workers in their own process when fetching measurably slows web requests or should scale separately from web instances.
  On Render, that's the same image as a background worker service with the web service's workers set to 0 (`RSS_LOOKUP_WORKERS=0`, `RSS_REFRESH_WORKERS=0`), for about $7 more a month; a `cmd/worker` or `-role` flag is the alternative.
  On a scale-to-zero host, add a `-once` command that drains the due feeds, run by the platform's cron, rather than an HTTP endpoint.
- [ ] **WebSub (push).**
  Subscribe to hubs that feeds advertise for near-instant updates.
  Only when freshness matters more than it does now; needs a public callback URL.
- [ ] **Article retention (needs planning before launch).**
  Whether to delete old articles, and when.
  Read state doesn't drive it (the timeline isn't an inbox), so a time- or count-based rule works (e.g. keep 6 months, or the newest N per feed), with `reads` rows deleted along with their articles (`ON DELETE CASCADE`).
  Before launch, weigh deleting against keeping: storage and backup cost and the "Articles grow without bound" watchlist entry on one side; the value of a long article archive (search, history, business uses of the data) and its copyright and privacy implications on the other.
  Saved articles (#299) must survive whatever rule is chosen: deleting an article cascades to its `saves` rows.
  Decide it before "A timeline table per reader" (under Reading), whose size it bounds.
  For scale: #388's seed takes about 1 KB per article on disk with `content` empty, so real full-text articles take several times that.
- [ ] **Operational visibility.**
  An admin page or metrics (due backlog, failure rate, 304 rate).
  When the app runs somewhere the logs aren't at hand.
  An admin page would build on debug mode's allowlist (#327).
- [ ] **Titles and summaries from `media:group`.**
  YouTube entries have no `summary`; their text is in `media:group/media:description` (and `media:title`), which `Parse` doesn't read, so their excerpt stays empty and each one gets an enrichment job.
  Read them as fallbacks when enrichment fetches of video pages become a noticeable share.
- [ ] **Image `media:content` in Atom and in `media:group`.**
  `itemImage` reads thumbnails at the top level and in a `media:group` (#334), but an image `media:content` only at an RSS item's top level, through gofeed's RSS translator (its Atom translator reads none).
  Reading them in both formats, grouped or not, would cover Media RSS feeds that declare no thumbnail.
  When such a feed turns up.
- [ ] **Fetch history.**
  Keep the last N fetch attempts per feed, with status, bytes, items, new and updated counts, and duration, but no bodies.
  Show them in the feed's debug panel.
  Debug mode (#327–#330) deliberately stores nothing and inspects live.
  Add this only if a problem comes and goes, so that a live fetch can't show it.
  Cap it per feed, since it's a table that grows with every fetch.

## Subscriptions

- [ ] **Feed tabs on the site page.**
  Show a site's articles one feed at a time, as tabs that are sub-routes of the site page (e.g. `/sites/{id}/feeds/{feedID}`).
  This is for browsing a site's feeds in context.
  It isn't a way to organise subscriptions (no folders or pinned feeds).
  Open question: how it relates to the standalone feed page.
- [ ] **Newsletters by email (needs planning).**
  Each user gets an address that turns the newsletters sent to it into a feed they subscribe to, as Feedbin and Inoreader do.
  Needs receiving email (an inbound webhook from the email provider, or Email Routing to a worker), a way to create feeds that discovery didn't find, spam and size limits, and sanitizing email HTML, which is messier than feed HTML.
- [ ] **Names from OPML imports.**
  Other readers' exports carry each feed's name as the outline's `text`/`title`; an import could set it as the subscription's name (#342) when it differs from the feed's title.
  Deferred from #344.

## Reading

- [ ] **Enrich older articles (low priority).** #306 only enriches a feed's 20 newest articles on its first save, then new arrivals.
  A second, lowest-priority queue could work back through older articles that are missing an image, title or excerpt, when nothing else is waiting.
  Worth it once something reads the old archive: searching all articles, related articles, or saved articles from far back that look bare.
- [ ] **Load full article (on demand).**
  A "Load full article" button on the article page when the feed only sends a summary: a POST that fetches the page once, extracts the text readability-style (a dependency, e.g. `go-shiori/go-readability`), sanitizes it like feed HTML and keeps it on the article for every reader.
  Never automatic: it costs storage, extraction fails on paywalls and cookie walls, and storing publishers' full text is a copyright question for a public product.
  Needs a failure story ("Couldn't load the article; read it on {site}").
- [ ] **Search your articles (needs planning).**
  Search across every article in your subscriptions, not just Saved: "that post I read last month".
  Postgres full-text search (`tsvector` with a GIN index) over the title, page title and excerpt, scoped to the user's feeds; whether it covers the full content too is a storage and copyright question.
  Plan after "Article retention" (under Feed ingestion), which decides how far back there is to search; it would also replace the Saved search's `ILIKE` ("Saved search scans with `ILIKE`" in the watchlist).
- [ ] **A timeline table per reader (needs planning).**
  One shared table with a small row per reader per article in the feeds they follow, written when the article arrives, with copies collapsed on write (as Tiny Tiny RSS, Miniflux and Mastodon do).
  Home is then one index lookup: 0.5–0.9 ms on any page at #388's scale, against ~30 ms for #404's per-feed query, and it stops growing with the number of feeds followed.
  Storage: about 225 bytes per entry, growing with articles × followers: 69 MB (and ~35 MB a year) for #388's heavy reader with 300 feeds, ~4 MB a year for a reader with 100 ordinary feeds.
  Subscribing and importing must backfill the feed's existing articles (4.8 s for 300 feeds of two years), in a background job like imports, so a new feed's articles appear a few seconds late, and the job competes with other queries while it runs.
  Ingest writes a row per follower for every new article; unsubscribing deletes rows and promotes the next copy when the feed holding the first one goes; and the rows are a derived copy that bugs can make drift, so it needs a rebuild tool.
  It doesn't speed up word filters (860 ms measured).
  A cheaper version keeps only each reader's recent entries (Mastodon keeps a few hundred) and serves older pages with #404's query.
  Decide "Article retention" (under Feed ingestion) first: it bounds this table's size.
  Only when timelines get slow again after #404 (its watchlist entry for the cost per followed feed).
- [ ] **AI-assisted titles and summaries.**
  For untitled items, or alongside enrichment: heuristics over headings, URL path, images and text, or a model.
  Deferred from #306.
- [ ] **Generic `og:image` detection.**
  Some sites use one fallback `og:image` (their logo) for every page, so enrichment (#306) would give every card the same picture.
  If that shows up, ignore a page image that a feed's recent articles share.
- [ ] **Measure embeds and media.**
  Before building more embeds or a player, see what articles carry.
  Embedded iframes by host:
  ```sql
  SELECT substring(m[1] from '^(?:https?:)?//([^/?#]+)') AS host, count(DISTINCT id)
  FROM articles, regexp_matches(content || summary, '<iframe[^>]*\ssrc=["'']([^"'']+)', 'gi') AS m
  GROUP BY host ORDER BY 2 DESC;
  ```
  Embedded social posts (their markup arrives as a blockquote with a class):
  ```sql
  SELECT count(*) FILTER (WHERE content || summary ~* 'class="twitter-tweet') AS x,
         count(*) FILTER (WHERE content || summary ~* 'bluesky-embed') AS bluesky,
         count(*) FILTER (WHERE content || summary ~* 'mastodon-embed') AS mastodon,
         count(*) FILTER (WHERE content || summary ~* 'instagram-media') AS instagram
  FROM articles;
  ```
  Audio and video items:
  ```sql
  SELECT split_part(media_type, '/', 1) AS kind, count(*), count(DISTINCT feed_id) AS feeds
  FROM articles WHERE media_type <> '' GROUP BY 1;
  ```
  Use the numbers to order the next three items.
- [ ] **Podcast and video player.**
  Keep the enclosure's URL, type and length (#297 only keeps the type), show the browser's own `<audio>` / `<video>` player on the article page, and an "Audio" or "Video" label on the card.
  Media loads from the publisher, so the CSP needs `media-src https:`.
- [ ] **Music embeds.**
  Spotify, SoundCloud and Bandcamp players as click-to-play previews, like YouTube and Vimeo (#296).
- [ ] **Embedded social posts.**
  Style the blockquote that X, Bluesky and Mastodon embeds arrive as into a quote card with the author and a link, without loading their scripts.
- [ ] **Save any web page.**
  Save a link that isn't in any feed to Saved, from a bookmarklet, a "Save a link" form on Saved, or the phone's share sheet (a share target added to the manifest from #394).
  Read-later apps keep closing (Pocket in 2025), and Saved plus enrichment already shows a page's title, image and description.
  Needs articles without a feed, or a feed per user for saved links: decide in planning.
- [ ] **Measure untitled articles.**
  Before investing in better titles, query the share of articles with an empty title (`SELECT count(*) FILTER (WHERE title = '') * 100.0 / count(*) FROM articles`).
- [ ] **Image proxy (needs planning).**
  Serve article and card images from our domain so publishers and trackers don't see readers' IPs, browsers and read times.
  Its own project because the proxy fetches stranger-chosen URLs (through `internal/fetch`), must only fetch URLs we signed (HMAC) so it isn't an open proxy, must never serve SVG or HTML from our origin, would decode untrusted images if it resized them (decompression bombs), and costs bandwidth, caching and possibly copyright questions.
  Site icons (#304, #305) are already served from our domain: they're small, one per site, and checked once a month.
- [ ] **Keyboard shortcuts.**
  E.g. `j`/`k` to move between rows, `o` or Enter to open, `v` to open the original, a key each for Star and Save, and `?` to list them; never while typing in a field or with a modifier held, and `j` on the last row loads the next page.
  Single-key shortcuts must be possible to turn off (WCAG 2.1.4: they collide with screen readers and speech input), so plan an off switch in Settings, a typed column on `users`.
  Deferred while planning #396 and #397; open questions are the letters for Star and Save (`s` meant star in older readers, but could read as Save).
- [ ] **Unread filter.**
  Maybe never; it pulls toward the inbox model.
- [ ] **Timezone setting and day headers.**
  Only if local times via JavaScript aren't enough (e.g. "Today"/"Yesterday" groups rendered server-side).
- [ ] **Site names from `og:site_name`.**
  For articles on another site than their feed's, enrichment could store the page's `og:site_name` ("The New York Times") so "Read on" shows it instead of the domain (#341).
  Enrichment doesn't run for every article, so the domain stays the fallback.
  Deferred from #344.
- [ ] **"Discussion on Hacker News".**
  For aggregator items, a second link on the article page to the item's own page (the feed's `comments` link), next to "Read on {domain}".
  Deferred from #344.

## Filtering and timelines

Deferred while planning filters (#365).
The decisions are under "Filtering and timelines" in decisions.md.

- [ ] **Related articles (needs planning).**
  "More like this" on the article page, and smarter topic matching than keyword filters (#360).
  Two approaches to compare:
  - *Content similarity:* article embeddings, in Postgres with `pgvector` or in a separate vector store, which means a model call per article and its cost; or Postgres full-text search (`tsvector` similarity) as a cheap baseline first.
  - *Signals across users:* articles starred together ("people who starred this also starred…", within a similar category), and a feed's category from the tags readers give it.

  The second needs real usage first, and the conditions under "Cross-user use" in decisions.md: anonymous totals above a minimum count, and a privacy policy update.
- [ ] **Shared timelines in Search (needs planning).**
  An opt-in "List in search" per shared timeline (#381), and a Timelines section in search results.
  Plan moderation first: names and descriptions are the first user-written text strangers would see (spam, impersonation, reporting), and whether to show an owner name or profile.
- [ ] **More posting-frequency conditions.**
  Beyond "at most N posts a week" (#359): posts a day, "posted in the last N days", "hasn't posted for N months".
  When someone asks.
- [ ] **Start on a chosen timeline.**
  A setting for which tab `/` opens, for someone who reads a filtered timeline (say, everything but a news wire) more than Home, which can't be edited (see "Timelines" in decisions.md).
  When someone asks.
- [ ] **Word filters from a full-text index.**
  Word filters (#360) check the text of every article in the reader's feeds: ~0.9 s at #388's scale, and a shared word timeline pays it on every poll of its feed.
  Store each article's words as a `tsvector` (`simple` dictionary, so no stemming: "run" doesn't match "running") with a GIN index, find candidates with it, and keep today's exact match (`wordsPattern`) as the final check, so results don't change.
  Measured on #388's seed: rare words ~0.9 s → ~50 ms; very common words (in a quarter of articles) only ~1.0 → 0.4 s, since every match still gets sorted; a 26 MB index (about 3% on top of `articles`, growing with it) that every new article updates.
  Today's text expression (`concat_ws`) can't be indexed because Postgres doesn't count it as immutable, so build the text with `||` (same result: the columns are never NULL).
  Words with punctuation (`c++`, `node.js`) are split differently by the index, so they skip it and use the exact match alone.
  If common words matter in practice, look at recent weeks first and widen only when a page isn't full.
  The same index could serve "Search your articles" (under Reading).
  Plan after #404, so it's measured against the new timeline query.
- [ ] **Embedded videos for the audio/video condition.** #359 only counts enclosures.
  A flag set at ingest for YouTube or Vimeo iframes would add blog posts with videos; decide with "Measure embeds and media".

## Design

- [ ] **Generated avatars for sites without a usable icon.**
  Sites whose only icon is a small favicon keep their letter (#304).
  Explore generating something better, possibly with AI: the site's colors on the tile, an upscaled or redrawn logo.
  Ideas only so far.
- [ ] **Icons a feed declares.**
  RSS `<image>`, Atom `<icon>` / `<logo>` and JSON Feed `icon` as extra candidates for #304, if many sites end up without an icon.
- [ ] **Themes, including dark mode (needs planning).**
  A theme choice (light, dark, follow the system) on the Settings page (#261), applied as a `data-theme` attribute on `<html>` that redefines the color tokens.
  Stored as a typed column on `users`, not a key-value table ("Accounts" in decisions.md).
  The avatar tints (`.tone-1` to `.tone-6`) and the icon tile (#305) need dark variants too.
  So does the `theme-color` meta tag (#394), with a `media` query for the system's dark mode.
  Reading preferences go here as well: text size, serif or sans, and line width on the article page (deferred from #295).
- [ ] **Asset build step (needs planning).**
  Bundle and minify the CSS and JavaScript for production, writing content-hashed file names.
  Templates link assets through `asset` (#199), so only `asset` changes: it looks names up in the build's manifest instead of hashing files at startup, and every file under `/static` gets the immutable `Cache-Control`.
  This reverses the "no build step" decision ("Overall" in decisions.md, and CLAUDE.md), so planning it updates both.
  Pages must still work without JavaScript.

## Mobile

The plan, and why, is under "Mobile" in decisions.md; installing the web app is #394.

- [ ] **Phone review pass.**
  Every page on a phone, in the browser and installed (#394): tap targets, the bottom tab bar over long pages and forms, the keyboard covering inputs, wide tables and code, and anything under the status bar or the home indicator.
  By hand, like the first error-state pass (#392), listing fixes as issues; best done right after #394 ships.
- [ ] **Install hint.**
  If testers don't find the browser's install, a short "Add rss to your home screen" note in Settings with the steps for iOS and Android.
  Never a banner or a prompt on the timeline (see "Mobile" in decisions.md).
- [ ] **Offline reading.**
  A service worker that keeps the recent timeline pages and the articles opened from them, so the installed app opens without a connection.
  Needs a cache budget, what to show offline for everything else, and care that a logged-out or other user's page is never served from the cache (logged-in pages are `no-store` today).
  When testers ask for it, or with the native shell.
- [ ] **Native app shell (needs planning, shortly after launch).**
  An iOS and Android app (Hotwire Native or Capacitor) around the server-rendered pages, with native navigation and share sheet, and native screens only where they pay off (the article reader first).
  Plan with it: App Store review (guideline 4.2 rejects apps that are just a website, so it needs real native parts), in-app payments (under "Plans and billing"), Sign in with Apple (Apple requires it when other social logins are offered; see "Sign in with Google or Apple" under Accounts), and whether there are any push notifications at all: never "N new articles", which is the inbox the timeline isn't.
- [ ] **Third-party client API.**
  Not at launch (see "Mobile" in decisions.md); only on the signals there, starting with the Fever API.

## Error-state walkthrough (needs planning)

A way to see every error state an end user can hit and judge the experience (wording, what the user can do next, tone, layout) without having to provoke each one by hand.
The first pass, by hand, is #392: it builds the catalog (`docs/error-states.md`) from "States to cover" below and reviews each state.
What's left needs planning once that pass shows how many states there are and how much needs fixing.

- [ ] **Walkthrough tool.**
  Something that renders every state in one place so they can be clicked through in a browser, preferably a dev-only route group (e.g. `/dev/states`, mounted only with a flag, never in production) that lists each state and renders it with fake data.
  A test table (`name`, `setup`, `request`, `want status`) can drive the same list, so the catalog can't drift: a test fails if a state is in the code but not the catalog.
  Prefer this over screenshots, which go stale.
- [ ] **States to cover.**
  - Forms: signup (invalid email, short, common or email-as-password password (#256), a duplicate email (#262), signup turned off (#186), rate limited (#182)), login (unknown email, wrong password (#262), rate limited per IP (#182) or per email (#183)), empty or oversized input, a form posted from another site (403), a malformed form body.
  - Auth and sessions: visiting a login-only page while logged out (redirect and message), an expired or idle session mid-action (#258), logging out, a logged-in user on `/login`.
  - Account emails and settings (#254–#269): a confirmation, reset or email-change link that's expired, used, or for an address the account no longer has; reset for an unknown email; the rate limits on reset, resend, password change, email change and deletion; an address over the outbox's hourly cap; a wrong current password in Settings; an email change to a taken address; the confirmation notice; deleting an account.
  - HTTP errors: 403, 404 (unknown feed, site or lookup id; malformed id), 405, 413, 429 (the lookup rate limit; login and signup re-render their form instead), and 500, including a database outage.
  - Lookups and discovery: invalid URL, a URL that is blocked (private address, bad scheme), a site that is unreachable, times out, redirects too often, returns a non-HTML or huge body, has no feeds, or has a feed that fails to parse; a lookup that is pending, running, failed or done; the status page after the lookup row is gone.
  - OPML import: an upload with no file, over 1 MiB, that isn't OPML, with no feeds or over 1,000, or over the hourly limit; an import still running; an import with failures (invalid URL, unreachable, no feed); another user's import.
  - Feeds: a refresh that fails (and the backoff / failure display from #129), a feed that has never been fetched, a feed with no articles, the Refresh button during a refresh, 410 Gone feeds (#132).
  - Search: no results, empty query, very long query, special characters.
  - Empty states that aren't errors but read like one: a new account with no feeds, an empty site page.
  - Subscriptions: a subscribe or unsubscribe that fails.
  - Reading: an empty timeline (no subscriptions), "No older articles.", an invalid `before` cursor (400), an article with no text in its feed, an article with no URL, an unknown or malformed article ID, a failed read ping (silent by design).
- [ ] **Cadence.**
  Repeat after any change to a template, middleware or error path, and at least before each release.
  Note the date of the last pass at the top of the catalog.

## Load and performance testing (needs planning)

Background refresh (#128) has never run against more than a few feeds.
Rough capacity: feeds per hour ≈ workers × 3600 s ÷ average fetch duration (2 workers at 1 s per fetch ≈ 7,200 feeds/hour).
The poll interval only matters when nothing is due, so it doesn't limit throughput.
Measure before relying on these numbers.
Worth doing before production, or once there are a few thousand feeds.
`make db/loadseed` (#388) measures the reading queries at realistic scale; its numbers are in the Performance watchlist entries below.

- [ ] **Load-test harness.**
  A program that seeds N fake feeds pointing at a local fake feed server and runs the refresh workers against them.
  The fake server takes configurable latency, error rate, hang rate and body size.
  The `www` binary can't fetch local addresses (`fetch.Options.AllowPrivate` is tests-only), so the harness should wire `internal/refresh` itself rather than add a `www` flag that turns off the address check.
  Rows are uniquely named and deleted afterwards, as in the DB tests.
- [ ] **What to measure.**
  - Due backlog: `count(*)` and `max(now() - next_fetch_at)` of overdue feeds.
    Should stay near 0 and under a minute.
  - Fetch duration p50/p95 (from the `feed refreshed` log's `duration`).
  - Worker utilization: fetches per hour × average duration ÷ (workers × 3600).
    Keep it under ~50–70% to leave headroom for spikes.
  - Web request latency p95 while the workers are busy.
  - Database connections in use (`pg_stat_activity`).
  - Memory and CPU of the `www` process.
- [ ] **Scenarios.**
  - Steady state: find the most feeds each worker count (2, 8, 16) keeps up with, using a realistic latency mix.
  - Slow and dead feeds: a share of feeds hang until the 10 s fetch timeout or fail outright.
    How much capacity they cost, and whether backoff wins it back over time.
  - Everything due at once: a fresh database, a restart after downtime, or a large import (`next_fetch_at` defaults to `now()`).
    How long the backlog takes to drain, and whether jitter spreads the next round.
  - Outage recovery: a host serving many feeds goes down and comes back.
    Do retries cluster?
    Does jitter spread them?
  - Many feeds on one host: requests per second hitting a single host (input for per-host politeness).
  - Large feeds: bodies near the fetch size cap and `ingest.MaxArticles` items, so the per-article upserts in `saveArticles` show their cost.
  - Web traffic during refresh load: page latency and DB connection contention.
    Workers and handlers share one pool (`-db-max-open-conns`, default 25).
  - Lookups and refresh together: both runners claiming and writing at the same time.
  - Several instances: `SKIP LOCKED` claims never fetch a feed twice, and throughput scales with instances.
  - Data growth: `EXPLAIN ANALYZE` for `ClaimDue`, `ListByFeed`, search (`ILIKE`) and site pages at ~100k feeds and millions of articles.
- [ ] **Act on the results.**
  Possible outcomes: a different default `-refresh-workers`, a DB connection cap, per-host politeness, a separate worker process, or the adaptive refresh interval (all listed under Feed ingestion).
  Record the numbers that led to each decision.

## Performance watchlist

Things that are fine at today's scale but could become performance problems as feeds, articles, users or instances grow: what each is, the issue that introduced it, the signal that it has become a problem, and the planned fix.
Don't fix these early: watch the signal, and use the load tests above to measure (see "Writing issues" in CLAUDE.md).
Add a new entry next to related ones, not at the end, so parallel PRs don't conflict.

Background refresh:

- [ ] **`ClaimDue` walks past feeds nobody wants refreshed** (#137).
  The `EXISTS` subscription check and `refresh_requested_at` can't be in the partial `feeds_next_fetch_at_idx`.
  Unsubscribed feeds keep an old `next_fetch_at`, so they pile up at the front of the order and every claim skips them.
  Ordering requested feeds first (`refresh_requested_at IS NULL, next_fetch_at`) also stops the index from serving the sort.
  *Signal:* `EXPLAIN ANALYZE` of `ClaimDue` reads far more rows than it returns, or idle claims get slow, once most feeds are unsubscribed.
  *Fix:* keep a `subscriber_count` on `feeds` (updated by `Subscribe`/`Unsubscribe` in the same transaction) and index `next_fetch_at WHERE gone_at IS NULL AND (subscriber_count > 0 OR refresh_requested_at IS NOT NULL)`.
- [ ] **Polling latency and idle queries.**
  Each idle refresh, lookup and mail worker queries every poll interval (15 s for refresh, 10 s for mail (#260)), on every instance.
  A requested refresh can wait a full interval before a worker sees it; the mail worker is woken by its own instance when an email is queued, so only emails queued by another instance wait.
  *Signal:* users wait noticeably for "Checking for new articles…" or for emails, or idle claim queries show up in `pg_stat_statements`.
  *Fix:* Postgres `LISTEN`/`NOTIFY` from `RequestRefresh`, `Subscribe`, lookup requests and email `Enqueue` to wake a worker, keeping the poll as a fallback.
- [ ] **Stale lookup resets scan `lookups`** (#195).
  Every instance's lookup runner resets stale jobs every minute, and no index covers `ResetStale`'s `status = 'running'` filter, so each reset scans the table, which keeps a row per site key ever looked up.
  *Signal:* `ResetStale` in `pg_stat_statements` totals, or a large `lookups` table.
  *Fix:* a partial index on `started_at WHERE status = 'running'`.
- [ ] **One statement per article in `saveArticles`.**
  Each fetched item is a separate upsert inside the `SaveFetch` transaction.
  *Signal:* `SaveFetch` time dominates the `feed refreshed` log's `duration` for large feeds (see the "Large feeds" scenario above).
  *Fix:* a single multi-row upsert (`unnest` of arrays) per fetch.
- [ ] **Posting rate recomputed on every save** (#350).
  `saveArticles` and `RecordNotModified` count each feed's articles of the last 30 days on every fetch, 304s included, served by `articles_feed_sort_idx`.
  *Signal:* the count shows up in `pg_stat_statements`, or `SaveFetch` time grows for busy feeds.
  *Fix:* recompute only when articles were inserted, plus at most once a day.
- [ ] **Writes from feed page views** (#137).
  Viewing a stale feed runs an `UPDATE` (`RequestRefresh`).
  The `refresh_requested_at IS NULL` guard limits it to once per stale period per feed, so this is unlikely to matter.
  *Signal:* `RequestRefresh` shows up in `pg_stat_statements` or in row-lock waits on `feeds`.
  *Fix:* skip the call unless the feed has been stale for a while, or move requests to their own table.
- Also listed under Feed ingestion: adaptive refresh interval, per-host politeness, separate worker process, giving up on long-dead feeds.

Other background work (#302, #304, #306):

- [ ] **Enrichment request volume** (#306).
  Every new article missing an image, title or excerpt costs one page fetch, and a feed's first save queues up to 20.
  One worker fetches one page at a time (about one a second), so a large OPML import (#302) of feeds without images queues thousands of jobs that take hours to drain, and many of them hit the same few hosts back to back.
  *Signal:* `SELECT priority, count(*), min(next_attempt_at) FROM enrich_jobs GROUP BY 1` growing or the oldest due job getting older; 429s from one host in the `article enriched` logs.
  *Fix:* per-host pacing (skip jobs whose host was fetched in the last few seconds), more `-enrich-workers`, or a lower first-save count.
- [ ] **Enrichment and icon claims skip unsubscribed work** (#304, #306).
  Like `ClaimDue`, `ClaimNext` and `ClaimIconCheck` filter on an `EXISTS` subscription check the index can't serve, so jobs for feeds nobody subscribes to (looked up, never followed) stay at the front and every claim walks past them.
  *Signal:* `EXPLAIN ANALYZE` of either claim reading far more rows than it returns.
  *Fix:* the `subscriber_count` column planned for `ClaimDue`, or deleting jobs of feeds without subscribers after a while.
- [ ] **One import at a time** (#302).
  The import queue takes the oldest import's feeds first, so a 1,000-feed import delays a small one started after it, up to an hour with two workers.
  *Signal:* imports reported slow, or pending `import_feeds` rows waiting long behind another user's import.
  *Fix:* round-robin between imports (claim the lowest position across imports, oldest import first), or a per-import worker cap.

Pages and queries:

- [ ] **My feeds is unpaginated** (#136).
  It loads every subscription with a `LATERAL` lookup of each feed's newest article.
  *Signal:* users with hundreds of subscriptions, or `/feeds` p95 latency rising.
  *Fix:* store `latest_article_at` on `feeds` (set in `SaveFetch`) instead of the `LATERAL` subquery; add filtering or pagination.
- [ ] **Site pages and search results load all of a site's feeds.**
  `ListBySite` and the second search query have no limit.
  Platform hosts and category-heavy sites can have hundreds of feeds.
  *Signal:* a site page or search response with hundreds of feeds, or slow ones in the logs.
  *Fix:* limit feeds per site in search (with a "more" link to the site page) and paginate the site page.
- [ ] **Debug panels count a feed's articles on every view** (#327).
  `FeedDebug` runs `count(*)` and `max()` over a feed's articles, for every feed on a site page, while debug mode is on.
  Only allowed accounts pay for it.
  *Signal:* site or feed pages are noticeably slower with debug mode on, for feeds with many thousands of articles.
  *Fix:* drop the counts from the site page's per-feed lines, or use the `articles_feed_timeline_idx` for the newest time and stop counting.
- [ ] **`SubscribedFeedIDs` on every page** (#135, #138).
  One extra indexed query per site, feed and search page.
  Unlikely to matter.
  *Signal:* it shows up in `pg_stat_statements` totals.
  *Fix:* fold it into the page's main query.
- [ ] **Search scans with `ILIKE`.**
  Sites and feeds are matched with `%q%`, which can't use a b-tree index.
  *Signal:* search latency growing with the number of sites and feeds.
  *Fix:* see "Better search ranking" under Feed discovery (`pg_trgm` or full-text search, with an index).
- [ ] **Timeline query merges many feeds** (#154).
  `ListTimeline` joins the user's subscriptions to their articles and sorts by `timeline_at`.
  Postgres can't merge the per-feed `(feed_id, timeline_at)` index scans, so it may read and sort every subscribed article to return one page, and deeper pages cost more.
  *Signal:* `/` p95 rising with subscription count, or `EXPLAIN ANALYZE` reading far more rows than the page returns.
  *Measured 2026-10-26* (#388, `make db/loadseed`: 1,000 feeds, 748k articles, on a dev container, not production): Home page 1 for 300 subscriptions (323k articles) returns 31 rows in 1,068 ms (median), reading 62,678 buffers (16,395 hit, 46,283 read) and all 748k rows of `articles` with a sequential scan.
  Page 20 costs the same (980 ms), because the cursor applies only after the whole history is collapsed and joined.
  With 30 subscriptions (37k articles) it's still 205 ms and the same full scan, so the cost grows with the user's history and the table's size, not with the page.
  The signal is met.
  *Fix:* #404 (each feed's newest entries, merged); if the per-feed cost shows later, "A timeline table per reader" under Reading.
- [ ] **Filtered timelines read past many articles** (#356, #359, #360).
  A restrictive filter (one quiet tag, a rarely matched keyword, quiet posters) still makes `ListTimeline` collapse copies over all the matching subscriptions' articles, and keyword matches (`~*` over title, page title and excerpt) can't use an index, so a page that matches few articles reads many to fill 30 rows.
  *Signal:* `/timelines/{id}` p95 above `/`'s, or `EXPLAIN ANALYZE` reading far more rows than the page returns.
  *Measured 2026-10-26* (#388, same seed): page 1 of every filtered timeline reads all 748k articles (about 62,700 buffers, 72,300 for Words) to return 31: Tag only (one tag of 5 weekly feeds, 611 articles) 258 ms, Quiet posters 267 ms, Media 160 ms, Words (3 words in 1% of titles) 898 ms.
  For Tag only, the planner estimates 150 matching subscriptions instead of 5 (the tag condition is an `OR` with an `EXISTS` inside `COALESCE`), so it scans the table rather than the 5 feeds' index entries; for Words, the `~*` over every row is most of the time.
  The signal is met.
  *Fix:* #404 for tags, posting rates and media (prototyped at 2–41 ms); for words, "Word filters from a full-text index" under Filtering and timelines.
- [ ] **Timeline tabs on every Home view** (#356).
  The tab row adds one small indexed query (`TimelineRepository.List`) to `/` and each timeline page.
  *Signal:* it shows up in `pg_stat_statements` totals.
  *Fix:* keep the tab list in the session until a timeline changes.
- [ ] **Tag page checklist is unpaginated** (#351, #356).
  A tag's page, and also the timeline form, list all of a user's subscriptions as checkboxes, like My feeds.
  Saving a tag posts every ticked feed's ID (43 bytes each), so past about 1,500 ticked feeds the form outgrows `decodePostForm`'s 64 KiB and gets a 413.
  *Signal:* users with hundreds of subscriptions, or those pages' p95 rising.
  *Fix:* with My feeds' fix, plus a filter box over the list.
- [ ] **Starred timelines read all of a user's stars** (#358).
  With Show limited to tags, `ListStarred` walks the user's stars by `starred_at` and drops those outside the filter, like the Saved search.
  Fine for hundreds of stars.
  *Signal:* starred-only timelines slow for users with thousands of stars.
  *Measured 2026-10-26* (#388, 2,000 stars): showing all feeds, page 1 reads 31 stars (1.8 ms, 286 buffers); limited to a tag of 5 feeds, it walks all 2,000 stars to return 3 (11.6 ms, 14,030 buffers).
  Not a risk at this scale: the cost grows linearly with the user's stars, so it nears 100 ms around 17,000 stars.
  *Fix:* join from the filter's feeds instead, or index stars by feed.
- [ ] **Shared feeds polled by other readers** (#381).
  Every request for a shared timeline's Atom feed runs its owner's timeline query, anonymously, and feed readers poll every 15–60 minutes; a link that spreads multiplies that.
  (The landing page only reads the timeline row.)
  `ETag`, 304s and `Cache-Control: public` cut the bytes, not the query.
  *Signal:* `/shared/` requests in the logs, their latency, or `ListShared` in `pg_stat_statements`.
  *Measured 2026-10-26* (#388): `ListShared` for a shared Words timeline takes 852 ms and reads 72,095 buffers (all 748k articles) on every request, since it's the owner's timeline query, so each poll costs nearly a second of database time.
  It gets cheaper with the timeline query's fix, but a cache still bounds what polls cost.
  *Fix:* an in-memory cache per token (keyed by the timeline's `updated_at` and newest article), a CDN in front of `/shared/`, or a per-IP limit.
- [ ] **Sanitizing article HTML on every view** (#151).
  `sanitize.HTML` parses and renders the article with `x/net/html`, then bluemonday parses it again, on each article page view; cost grows with the size of the HTML, which feeds don't bound.
  *Signal:* article page p95 latency or CPU rising, especially for long articles.
  *Fix:* cache the sanitized HTML per article (keyed by a policy version, so a policy change still applies everywhere), or cap the HTML size we render.
- [ ] **Read writes on article views** (#156, #158).
  Opening an article page, and each click through to the original, runs an `INSERT … ON CONFLICT DO NOTHING` on `reads`.
  *Signal:* `MarkRead` in `pg_stat_statements` totals, or lock waits on `reads`.
  *Fix:* skip the insert when the page already knows the article is read, or batch writes.
- [ ] **Copy flag on every save** (#404).
  `saveArticles` runs one more statement per save: an indexed `UPDATE` that flags the saved articles' copies by `canonical_url`.
  *Signal:* `saveArticles` slower in the refresh logs, or that `UPDATE` in `pg_stat_statements` totals, especially for feeds that resend many edited items.
  *Fix:* only check the URLs of inserted rows and of updates that changed `canonical_url`.
- [ ] **Timeline marks on every timeline view** (#397).
  Each timeline page reads the user's `timeline_marks` row (a primary-key lookup), and each time a timeline page is hidden its beacon upserts that row, so an active reader writes it every few minutes.
  *Signal:* `MarkSeen` in `pg_stat_statements` totals, or bloat on `timeline_marks` from frequent updates of the same rows.
  *Fix:* have `app.js` skip the beacon when the position hasn't changed since its last report, or report at most once a minute per page.
- [ ] **`ReadArticleIDs` on every list page** (#156, #161).
  One query per timeline or feed page, matching reads across copies by `canonical_url` (#161).
  Saves add `SavedArticleIDs` (#299), stars add `StarredArticleIDs` (#349) and icons add `IconHashes` (#305), each one more query per page with the same shape.
  *Signal:* they show up in `pg_stat_statements`, or slow for users with many reads or saves.
  *Fix:* fold them into the page's main query, or store the canonical URL on `reads` and `saves`.
- [ ] **Saved search scans with `ILIKE`** (#301).
  Search within Saved matches `%q%` against each of the user's saved articles.
  Fine for hundreds of saves.
  *Signal:* `/saved?q=` latency rising for users with thousands of saves.
  *Fix:* `pg_trgm` or full-text search, with the site search ("Better search ranking").
- [ ] **Site icons served from Postgres** (#305).
  Each icon request that misses the browser's cache reads its `bytea` row.
  URLs carry the icon's hash and are cached for a year, so repeat views cost nothing, but every new visitor's first timeline asks for up to 30 icons.
  *Signal:* `GetIcon` in `pg_stat_statements` totals, or icon requests in the logs dominating traffic.
  *Fix:* an in-memory cache keyed by hash, or a CDN in front of `/sites/*/icon`.

Data growth:

- [ ] **Articles grow without bound.**
  Raw `summary` and `content` HTML is stored for every article forever.
  *Signal:* `articles` table and index size, slower `ListByFeed` and timeline queries, backup size.
  *Fix:* see "Article retention" under Feed ingestion.
- [ ] **Timeline deduplication at query time** (#162).
  The timeline collapses duplicate articles across a user's feeds with `DISTINCT ON (canonical_url)` over all of the user's subscribed articles, for every page.
  *Signal:* timeline queries slow for users with many subscriptions or overlapping feeds.
  *Measured 2026-10-26* (#388): for 300 subscriptions, Home sorts all 323k subscribed articles on every page to collapse copies, spilling 15,723 temp blocks (123 MB) to disk at the default `work_mem` of 4 MB; collapsing takes about 530 ms of page 1's 1,068 ms.
  The signal is met.
  *Fix:* #404 (copies flagged at ingest, so only flagged articles get the earlier-copy check); "A timeline table per reader" under Reading would collapse them on write.
- [ ] **`reads` grows with every article read** (#156).
  One row per user per article opened, kept until the article is deleted.
  *Signal:* `reads` table and index size.
  *Fix:* article retention (deleting articles cascades to `reads`).
- [ ] **Imports are kept forever** (#302).
  Every upload keeps its `imports` row and a row per feed in the file, only shown on the results page and the last five in Settings.
  *Signal:* `import_feeds` table size.
  *Fix:* delete imports older than 90 days (the results page then says the import is no longer available).
- [ ] **Sessions in Postgres.** scs reads the `sessions` row on every request in the session group and writes it whenever the session changes (flash messages, login).
  `authenticate` then reads the `user_sessions` row joined to `users` (#258, in place of the old `Exists` query) and updates its `last_seen_at` at most once a day.
  Expired scs rows are swept periodically, but they live up to a year (#258), and visitors who aren't logged in get one too as soon as something is stored for them: a flash message after logging out, or the page to return to after login (#262), which crawlers following links to login-only pages would trigger.
  *Signal:* session queries in `pg_stat_statements`, or a `sessions` table much larger than the number of logged-in devices.
  *Fix:* keep the return-to page in a short-lived cookie of its own instead of the session, or a faster store (e.g. Redis) or signed cookie sessions.
- [ ] **In-memory rate limiters.**
  Their maps grow with distinct keys between sweeps and aren't shared between instances.
  The login and signup limits (#182, #183) add maps keyed by client IP and by any submitted email, so a stream of made-up emails or rotating addresses grows them within a window: the sweep only drops expired entries.
  *Signal:* process memory climbing during login floods.
  *Fix:* cap each map and refuse new keys past it, or move the limits to the proxy or edge.
  Revisiting the limiter in general is the "Rate limiter approach" item under Security.
- [ ] **Queued password hashing** (#184).
  Argon2 runs in at most `GOMAXPROCS` slots, and requests beyond that wait in line instead of failing: logins and signups, and from
  #263–#268 password changes, resets, email changes and account deletions, which also
  hash or verify a password.
  *Signal:* slow logins under load, or login requests running past `WriteTimeout`.
  *Fix:* bound the queue and return 503 with `Retry-After` when it's full.

Hosting (once deployed; see Deployment in decisions.md):

- [ ] **Small instance and database.**
  The web service is a Render Starter instance (512 MB, a fraction of a CPU), and Postgres is the smallest paid plan with little storage.
  Argon2 hashing runs at most `GOMAXPROCS` at once (1 here, since Go sizes it to the container's CPU limit).
  The common-password list (#256) adds about 1 MB (the 450 KB file and its 700 KB index).
  *Signal:* Render's memory and CPU graphs near their limits, restarts for running out of memory, slow logins, or database storage above 80%.
  *Fix:* the next instance or database size, or the separate worker process under Feed ingestion.
- [ ] **Email sending limits** (#259, #260).
  Resend's free tier allows 100 emails a day and 3,000 a month, and one mail worker sends one email at a time.
  *Signal:* sends failing with quota or rate-limit errors in the logs, or emails waiting in the outbox for minutes.
  *Fix:* a paid plan, the move to SES (see Accounts), or more mail workers (`-mail-workers`).
- [ ] **Database connections.**
  Each instance opens up to `-db-max-open-conns` connections (default 25), and small Postgres plans allow few.
  Production sets `RSS_DB_MAX_OPEN_CONNS=10`.
  *Signal:* "too many connections" errors, or connections waiting in `sql.DBStats`.
  *Fix:* lower it per instance, or a bigger plan.
- [ ] **The database is never idle.**
  Refresh workers poll every 15 seconds, so usage-billed databases that scale to zero (Neon and similar) would bill every hour of the month.
  Not a problem on Render.
  *Signal:* only matters if we move to such a database.
  *Fix:* sleep until the next feed is due instead of polling.
