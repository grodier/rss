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
  `feeds.last_error`). Pages say what happened in plain words. Debug mode's panels show
  them, to allowed accounts only (see "Debug mode"). (#50, #129, #327)

## Pre-alpha testing

- **Testers' data isn't kept**: schema changes keep editing the initial migration and
  wipe the database, and testers are told so. The rule and its scope are under "Prototype
  stage" in CLAUDE.md. (#180)
- **A planned batch lands its schema in one change**, so production is reset once per
  batch rather than once per feature: the account tables (#255), then the reading
  features' tables (#293). A prototype that may be dropped adds its own table instead,
  so dropping it leaves nothing behind. Before a reset, export your OPML, and import it
  afterwards (#303).

## Debug mode

Planned with issues #327–#330. Its purpose is exploration more than bug fixing: showing
how pages are put together from stored data, and what feeds and pages publish that the
app doesn't use yet.

- **Allowed accounts are listed in a flag** (`-debug-emails`). It's empty by default,
  and then nothing about debug mode shows and its routes are 404s. This was chosen over:
  - a request header, which needs a browser extension and doesn't work on a phone;
  - a user role, which needs a schema change. (#327)
- **It's turned on per device, from Settings.** It's a flag in the scs session, so
  logging out turns it off. The allowlist is checked on every request, so removing an
  account turns debug mode off at once. (#327)
- **Nothing new is stored.** There's no schema change, no fetch history and no copies of
  raw responses. Panels read existing rows, and inspectors fetch live. A fetch history
  is a todo item. (#327, #328)
- **Inspectors are read-only POSTs:**
  - they fetch through `internal/fetch` (GET handlers never fetch URLs);
  - they render their result directly;
  - they save nothing;
  - they get a 30 s budget, like a lookup job, by extending their own write deadline
    instead of raising the server's `WriteTimeout`;
  - they only take IDs, so every URL they fetch is one already in the database.
  (#328, #330)
- **Panels show internal details**, such as `last_error` and the `lookups` error, to
  allowed accounts. Untrusted text (raw XML, JSON, article HTML, page tags) is only
  ever shown as escaped text, never rendered. (#327, #328, #329)
- **Panels cost nothing when debug mode is off:**
  - their queries only run when it's on;
  - the line on list rows uses columns the lists already load. (#327, #329)
- **"Used" and "ignored" come from lists kept by hand next to the parsers:**
  - `usedChannelFields` and `usedItemFields` beside `feedparse.Parse`;
  - `usedPageKeys` beside `discovery.ParsePage`.

  A PR that changes what a parser reads updates its list. (#328, #330)
- **Discovery records every candidate it checks** (`Result.Checked`), and returns what
  it has so far when its deadline passes. The lookup and import runners ignore both.
  (#330)
- **No subscriber counts or other cross-user data.** Those belong to an owner admin
  page, which would build on the same allowlist (todo). (#327)

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
- **Subscriptions are private:** no subscriber counts. Anonymous totals across users may
  come later, under the conditions in "Cross-user use" under "Filtering and timelines".
- **Readers can rename their subscriptions** (planned in #344, issues #342 and #343). The
  name is per subscription (user and feed), not per site, since one site can have many
  feeds and future tags will be per subscription too. It's edited on "Edit subscription"
  (`/feeds/{id}/edit`), where tags would also go. `""` means the feed's title, so a
  feed's new title still reaches readers who didn't rename it; a blank name or the
  feed's own title stores `""`. At most 100 characters. Unsubscribing drops the name.
- **The name shows wherever the reader's own reading shows the feed** (timeline, "Also
  in", saved list, article and feed pages, "Read on", OPML export); search results, site
  pages and debug panels keep the feed's own title, since they describe the feed. Per-user
  list queries put the name into `Feed.Title` in SQL, so templates don't change. (#343)

### OPML import

Planned in #309 (issues #302, #303).

- **Everything in the file is imported**, with no preview or checklist; unsubscribing is
  one click if something unwanted came in. Folders become tags (#354; until then they were
  flattened, since the app had no folders): see "Filtering and timelines".
- **Import is a background queue**, one `import_feeds` row per feed in the file, worked by
  its own runner: an unknown feed has to be fetched and checked first, since feeds are
  only created by discovery. The `lookups` table can't be reused: it's keyed by site key,
  and feeds on a shared host would collide. A known feed is subscribed to directly; an
  unknown or gone one goes through discovery and is saved like a lookup's. A URL that
  turns out to be a web page subscribes to the first feed it advertises.
- **The results page reports every feed**: subscribed, already subscribed, or couldn't
  add, with a plain reason ("Not a valid web address.", "Couldn't reach the site.",
  "There's no feed at this address.") and a link to search for the site. Failures aren't
  retried automatically and there's no "Try again"; uploading the file again retries
  them. While it runs, the page refreshes itself with `<meta http-equiv="refresh">`.
- **Offered in Settings, next to Export, and on the empty timeline.**
- **Files up to 1 MiB and 1,000 feeds, 5 imports per user an hour.** The upload is a
  multipart form with its own size cap, not `decodePostForm`'s 64 KiB.

## Reading

Planned in #163 (issues #147–#162).

- **A timeline, not an inbox.** Read state is recorded but only shown as a quiet marker on
  read rows (tinted card, dimmed text and image). No unread counts, badges, "N new" indicators,
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
  pages: the feed's avatar and name with the time, then a card with the optional image,
  title, two-line excerpt and a small link to the original. The whole card links to the
  in-app article page. Feed pages leave out the avatar and name. Untitled articles use
  the start of the excerpt. (#181) The image, title and excerpt are the feed's, else the
  article page's (see "Enrichment", #307); the feed's own values always win.
- **Article page:** sanitized content, else summary, plus "Read on {site}". No
  previous/next. Any logged-in user can open any article. (#152) The card image shows
  above the text as a lead image unless the content already shows it: the content starts
  with an image, has one right after its first paragraph, or contains the same image
  anywhere (#295). With no content or summary, the page's description from enrichment is
  shown (#307).
- **"Read on" names where the link goes** (planned in #344, issues #340–#343): the
  article's domain when its URL is on another site than the feed's (aggregators such as
  Hacker News), else the reader's name for the feed, else the site's title, else its
  host. A name the reader gave the feed beats the site's title: it's theirs to choose,
  and an off-site article still shows its domain. Hosts on a subdomain of each other count as one site, without the public-suffix
  list (a dependency for a rare case). The domain needs no fetching and is always
  accurate; `og:site_name` from enrichment is deferred, since enrichment doesn't run for
  every article. A long name is cut with "…" so the button stays on one line, and its
  `title` and link text keep the full label. (#340, #341, #343)
- **Article HTML is sanitized at render time** with bluemonday: relative URLs resolved,
  iframes replaced by a link, links opened in a new tab with `noopener noreferrer`. Images
  load from the publisher with `referrerpolicy="no-referrer"` and lazy loading; the CSP
  allows `img-src https:`. No image proxy yet (a todo item, with why it's its own
  project). (#151) Changed in #309 for two cases:
  - Content with no block-level markup (plain text, such as JSON Feed's `content_text`)
    is split into paragraphs at blank lines, with `<br>` for single line breaks, and bare
    URLs become links everywhere (#294). Stored HTML is unchanged.
  - YouTube and Vimeo iframes become click-to-play previews (#296): nothing loads from
    them but YouTube's thumbnail until the reader clicks, then a sandboxed player from
    `youtube-nocookie.com` or `player.vimeo.com` (the CSP's only `frame-src` hosts).
    Without JavaScript the preview links to the video. Every other iframe stays a link;
    more embed types wait for the measurements in todo.md.
- **Reading preferences** (text size, serif or sans, line width) **wait for themes**; the
  article page gets better defaults first: wide tables scroll inside the column, styled
  code, lists and rules (#295).
- **Audio and video are measured before they're built** (#297): only the media type of an
  item's first audio or video enclosure is stored, so the owner can see how common they
  are, alongside queries over stored HTML for embeds. No player yet.
- **Paging works without JavaScript** ("Older articles" with a `?before=` cursor);
  infinite scroll enhances it. Logged-in pages keep `Cache-Control: no-store`; the script
  keeps the URL on the visible row so Back and reload land nearby. (#155, #159)
- **Times are server-rendered in UTC** in a `<time>` the script localizes; date-only feed
  dates show just the date and aren't localized. No day headers or relative times.
  (#157, #160) This covers every user-visible time, not just articles: feed and site
  status times ("Last fetched", "Next check around") and list dates ("Latest article")
  too, still absolute, with no time zone setting. (#337)
- **New articles don't appear live** while you scroll; a reload shows them on top.

### Saved articles

Planned in #309 (issues #299–#301).

- **Saved means bookmarked, kept until you remove it**, not a read-later queue: no
  archive, no "done", no counts. A queue you work through to empty is an inbox, which
  the timeline deliberately isn't.
- **Called Save / Saved, with a bookmark icon**, not a star or heart, which read as
  "like". Saving is quiet (no flash) and doesn't mark the article read. The star is a
  separate action that does mean "this is good" (#349, "Filtering and timelines").
- **Saving covers every copy** (same `canonical_url`), like read state: `saves` keys on
  `article_id`, and copies are matched at query time. Saving a copy of an already saved
  article adds nothing; unsaving removes them all.
- **Saved has its own nav entry**: Home, Saved, My feeds, Search. Newest saved first,
  paged like the timeline, with a search box (`ILIKE` over title, page title, excerpt
  and feed title; a user's saves are a small set).
- **Saved articles must survive article retention**, and go into the full data export.
- **The Save button sits under the card, right-aligned, with icon and label** (owner's
  pick in #316, from three layouts: this bar, an icon on the card's corner, an icon in
  the meta line). The card's left edge stays clean, and it's under the thumb on phones.
  "Also in" shares its line, on the left. More actions (Share) join it to Save's left.
  On the article page it sits under the title, left-aligned with the heading.

### Hiding articles (not taken in)

Planned in #309 (issue #308) as a prototype; built in draft PR #325 and **not taken in**
(#308 closed), for the three reasons below. Planning filters (see "Filtering and
timelines") kept it out: keyword filters and muting on Home cover what it was for
(sponsored posts, topics you don't follow) without a per-article table. Revisit only if
they turn out not to.

- **UX:** Undo lasts only until the page reloads; after that the only way back is the
  article's feed page, since there's no Hidden list. Hiding everything also lands on the
  empty-timeline message, which reads as "you have no feeds".
- **It invites inbox use.** Hiding each article as it arrives turns the timeline into an
  inbox to clear, which the timeline is designed not to be ("Reading" above).
- **Performance:** `hides` grows by a row per hide, never trimmed, and every timeline
  page must read all of a user's hides to leave them out. In the prototype, a
  `NOT EXISTS` filter made Postgres pick a plan about 5x slower, and the faster `NOT IN`
  stops being hashed once a user's hides outgrow `work_mem`, re-checking them per article.

What the prototype settled, as a starting point if it comes back:

- **Hiding only affects your timeline.** The article stays on its feed page, marked
  "Hidden from timeline", with an Unhide button; there's no Hidden list, so hiding
  doesn't become archiving. Unhiding puts it back at its original position, since
  positions never change. Hiding covers every copy, and doesn't mark the article read.
- **Undo right away:** with JavaScript the row collapses to "Hidden · Undo"; without it,
  a flash says where the article went.
- **Its own table, outside the batch's schema change**, so dropping the prototype leaves
  nothing behind.
- **Muting feeds and keyword filters are a separate planning item**, not part of hiding.
- **The hide ✕ goes at the row's top right**, on the meta line, not in Save's action bar
  (#316): next to Save, a mis-tap would hide the article you meant to keep.

## Filtering and timelines

Planned in #365 (issues #348–#364), from the owner's list: favorites, tags, several
timelines with filters, a quiet-posters timeline, shared timelines. The batch's schema
lands in one change (#348).

### Stars and tags

- **A star on articles means "this is good"; Saved stays "keep".** The owner chose stars
  on articles over favorite feeds; a tag such as "Favorites" covers favorite feeds. A
  star is what a picks timeline, and a shared link blog, is made from. Starring covers
  every copy, is quiet, doesn't mark the article read, and is private except through a
  timeline the user shares. Where the star sits next to Save is the implementer's layout
  options. (#349)
- **Stars outlive subscriptions**, like saves, so unsubscribing never edits a picks
  timeline that shows all feeds. (#358)
- **Tags are free text on subscriptions, several per feed, not folders**: a feed can be
  both "Tech" and "News". There's no shared list of categories, which would be a guess
  and worse for private organizing. Names are trimmed with inner whitespace collapsed,
  compared ignoring case (`citext`), 1–50 characters with no commas, up to 100 per user.
  Managed at `/tags`; added on Edit subscription (checkboxes plus a comma-separated
  field, no JavaScript) and in bulk from a tag's page. (#351–#353)
- **Composite foreign keys keep tags and timelines inside one account**, and
  unsubscribing drops the subscription's tags, Home mute and timeline rows (cascade),
  like its name. (#348)
- **OPML: folders become tags on import** (every folder an outline sits in, at any
  depth; import only adds tags), **and the export nests feeds in a folder per tag** (a
  feed with two tags appears in both; untagged feeds at the top level), which every
  reader imports as folders. No `category` attribute, which most readers ignore. These
  change "OPML import" under Subscriptions and the export under Accounts. (#354, #355)

### Timelines

- **Timelines are explicit, not one per tag.** Tags organize; a timeline is made on
  purpose (a tag's page offers "Make a timeline"), which keeps the tab row short. (#356)
- **Tabs at the top of Home**, at `/timelines/{id}`, managed at `/timelines` and
  reordered with Move up / Move down (no drag and drop, which needs JavaScript). The nav
  rail is unchanged, and tabs carry no counts. The tab row's layout is the implementer's
  options. (#356, #357)
- **A filter is a form, not a query builder.** Show is all my feeds, or feeds with any of
  these tags or these feeds; Leave out is tags and feeds, and always wins; then
  conditions that must all hold: starred only, at most N posts a week, must mention /
  leave out words, audio or video only. Timelines pick only from your subscriptions,
  except a starred-only one showing all feeds. (#356, #358–#361)
- **The same reading rules as Home:** arrival order, copies collapsed among the
  timeline's own feeds, "Also in", read markers. A starred-only timeline orders by star
  time instead, so new picks land on top. (#356, #358)
- **Presets on New timeline, not built-in tabs:** Starred (all feeds, starred only),
  Quiet posters (at most 2 a week), and From a tag. Each is an ordinary timeline you can
  rename, filter, share or delete. (#356, #358, #359)
- **Quiet posters use the feed's 30-day average**, stored as `feeds.posts_per_week` on
  every save and 304, and shown on My feeds and feed pages ("About 3 posts a week") to
  help pick N. One whole-number field for now; richer frequency rules are a todo item.
  (#350, #359)
- **Keyword filters match whole words and phrases, ignoring case, in the title, page
  title and excerpt**, never the full content (lists don't load it, and it's unbounded).
  A term matches where the characters just before and after it aren't letters or
  digits, so terms like `C++`, `C#`, `.NET` and `#hashtag` work (symbols are matched
  literally), and `AI` still matches "#AI" but not "said". Accepted: `C` alone also
  matches "C++"; counting symbols as letters would stop `AI` matching "#AI". A term
  needs a letter or digit. Any term matches; leave-out wins. Plain matching only:
  smarter matching belongs with related articles (todo). (#360)
- **Audio or video means an enclosure** (`articles.media_type`), not a video embedded in
  the HTML. (#361)
- **Muting is Home's Leave out.** Home is a `timelines` row created on first mute, with
  no other settings; "Show on Home" sits on Edit subscription and tag pages, and My feeds
  marks muted feeds. This is the muting half of the old "Filters and muting" item. (#362)
- **Limits:** 20 timelines; names up to 50 characters, descriptions up to 280. (#356)
- **No unread filter or counts**, as under "Reading".

### Sharing

- **Opt-in per timeline, unlisted, at an unguessable link**: 32 random bytes, stored as
  is (not hashed like email tokens), since the owner's settings show the link again and
  it only grants reading what they published. A public page (`noindex`, no session) and
  an Atom feed. Stop sharing makes the link a 404; New link replaces it. Home can't be
  shared. Needs a confirmed email ("Accounts"). (#363, #364)
- **Each item is the title, a link to the original, the date, the source and the
  excerpt.** Never the content: republishing publishers' text is the copyright question
  under "Enrichment". Articles without a link are left out. (#363)
- **No owner name or email**, only the timeline's name and description; the page does
  show which feeds the articles come from, and the settings say so. **The source is the
  feed's own title**, never the owner's private name for it (#342). (#363)
- **Atom, not RSS 2.0:** typed text (`<summary type="text">`, so plain excerpts are never
  guessed to be HTML), required stable IDs (the canonical URL) and RFC 3339 dates, and
  `<source>` for each entry's original feed. Every reader reads both, and users only see
  "feed". (#364)
- **Public routes are a deliberate exception** to "Search, site pages and lookups are
  login-only" (Feed discovery): their owner opted in.
- **Shared timelines aren't listed in Search** for now: their names and descriptions
  would be the first user-written text shown to strangers, which brings spam,
  impersonation and reporting. A todo item.
- **The privacy policy says what's kept and what sharing publishes**, in the same PR
  (#363).

### Cross-user use (later)

- **Stars and tags may later feed anonymous totals across users**, e.g. "people who
  starred this also starred…" or a feed's usual tags as its category. Only as aggregates
  above a minimum count, never traceable to one person, and after a privacy policy
  update. The tag hint says "Only you see your tags", which stays true; it doesn't
  promise the data is never used. It would relax "no subscriber counts" (Subscriptions)
  for aggregates only, when planned (see "Related articles" in todo.md). The data is
  shaped for it now: tags on subscriptions add up per feed, and stars are on articles
  with `starred_at`, matched across copies by `canonical_url`.

## Enrichment

Planned in #309 (issues #298, #306, #307).

- **Articles missing an image, title or excerpt get their web page fetched once**, in the
  background, for its `og:image` (else `twitter:image`), `og:title` (else `<title>`) and
  description. Articles complete from their feed cost nothing. No full text (an
  on-demand "Load full article" is a todo item, never automatic: storage, failed
  extractions, and the copyright question of storing publishers' text) and no AI.
- **Enriched values have their own `page_*` columns** and only fill gaps: the feed's own
  image, title and excerpt always win. `saveArticles` overwrites the feed's fields on
  every changed fetch, so enrichment never writes them.
- **How far back:** a feed's first save (a lookup or an import) queues only its 20 newest
  articles, at a lower priority than new arrivals; after that, every new article that
  qualifies. Older articles wait for a low-priority backlog queue (a todo item, for
  future search and related-article features).
- **Its own queue table (`enrich_jobs`) and runner**, one worker by default. Jobs are only
  claimed for feeds someone subscribes to, as refresh only fetches subscribed feeds, so a
  looked-up site nobody follows costs no page fetches.
- **Polite, with a retry story:** one request at a time through `internal/fetch`; a
  network error, 429 or 5xx is retried after 1 hour, then 6, then given up; any other
  4xx or a non-HTML response is given up at once, as is a fetch `internal/fetch` refuses
  (a blocked address, a body over 5 MiB, too many redirects), which a retry wouldn't
  change (#306). `robots.txt` isn't consulted: it's one fetch of a page a reader is about
  to see, like a link preview.
- **One page parser:** `discovery.ParsePage` reads Open Graph tags and icon links too
  (#298), for both enrichment and site icons.

## Design

A modern, minimal look modelled on social timelines, from the redesign in #181.

- **One centered column, nav on the left.** A rail with icons and labels on wide screens,
  icons only on medium ones, and a top bar (logo, logout) with a bottom tab bar on
  phones. CSS only; no JavaScript.
- **Lists are sections separated by dividers, not boxes.** A card is only for a preview of
  something elsewhere: an article now, Open Graph data later.
- **Avatars are letters on a tint chosen by site** (`avatarTone`), so a site's feeds share
  a color, for sites without a usable icon (below).
- **Site icons replace the letter where a site has a usable one** (planned in #309,
  issues #304, #305):
  - We keep our own copy and serve it from `/sites/{id}/icon?v={hash}`, rather than
    loading it from the publisher: publishers don't see who's reading, hotlink blocking
    doesn't break it, and we have to download it anyway to know its real size.
  - Only PNG, JPEG, GIF and ICO up to 256 KiB, served with a sandboxing CSP and
    `nosniff`. Never SVG, which could run script from our origin; no WebP for now (its
    size needs `golang.org/x/image`).
  - At least 32 px on the shorter side and roughly square, else the letter stays: a
    16 px favicon stretched to 40 px looks worse than the letter. Generated avatars for
    those sites are a todo item.
  - Checked by their own runner soon after a site is created, then about every 30 days
    for sites with subscribers, with a conditional request so an unchanged icon costs
    almost nothing. A failed check keeps the current icon; it's removed only when the
    site's page loads and none of its icons is usable.
  - Site and feed avatars become rounded squares, icon or letter, since most icons are
    designed as squares. The user's own avatar stays a circle: people are circles, sites
    squares.
- **Light theme only for now.** Colors are custom properties on `:root` in `main.css`, so
  a theme can redefine them in one place. System font stack; icons are inline SVG
  partials (`templates/partials/icons.html`), not an icon font or library.
- **When a layout is an open question, the implementer proposes options before
  finishing** (#309, for #299 and #308): a draft PR with two or three layouts behind one
  switch, screenshots at desktop and phone widths (committed under `.screenshots/` while
  the PR is a draft), and a stop until the owner picks one in a PR comment. Each control
  must work on its own and next to the others planned for the same place, since not all
  of them may ship.

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
- **Export your subscriptions as OPML 2.0, downloaded from Settings** (#270, #267): one
  `<outline>` per feed, with no nesting by site, since most readers turn nested outlines
  into folders and would make one per site. Gone feeds are included: they're still your
  subscriptions. Changed in #355: feeds are nested in a folder per tag (see "Filtering and
  timelines"), and the list stays flat for someone without tags. Import: see "OPML import"
  under Subscriptions.
- **Security emails:** password changed, email changed (to the old address), and account
  deleted. No "new login" alerts: they're noisy, and people learn to ignore them.
- **Email goes over SMTP behind `mail.Sender`** (#259), so the provider changes by
  configuration: Resend's free tier while testing, Amazon SES later (a todo item).
  Without an SMTP host, emails are logged instead, in development only; production
  refuses to start without one, since emails carry one-time links. Links use
  `-base-url`, never the request's `Host` header.
- **An outbox table, sent by a background worker** (#260), so pages don't wait on the
  provider and failures retry. The handler that queues an email wakes its own worker, so
  it goes out in about a second. Bodies are cleared once sent. About 20 emails to one
  address an hour: a backstop under each flow's own limit, so nobody can use the app to
  flood an inbox. It's a count before the insert, so simultaneous requests can slightly
  exceed it (#260); a backstop doesn't need a per-address lock. On shutdown the mail
  runner finishes the send in flight instead of aborting it (#260): an aborted send can
  still be delivered, then sent again on the retry, and it delays the email by the lease.
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
