-- Production reset check (#217).
-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    email citext NOT NULL UNIQUE,
    email_confirmed_at TIMESTAMPTZ, -- NULL until the user confirms their address; a password reset also confirms it
    hashed_password bytea NOT NULL,
    theme TEXT NOT NULL DEFAULT 'system' CHECK (theme IN ('system', 'light', 'dark')), -- 'system' follows the device's light or dark setting
    created_at timestamp(0) with time zone NOT NULL DEFAULT now()
);

CREATE TABLE sites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    icon_checked_at TIMESTAMPTZ,                         -- last time the icon worker looked for the site's icon; NULL if never
    icon_next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- when the icon worker should next look; also its claim lease
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX sites_icon_next_check_at_idx ON sites (icon_next_check_at);

CREATE TABLE feeds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    site_id UUID NOT NULL
        REFERENCES sites(id)
        ON DELETE CASCADE,

    url TEXT NOT NULL UNIQUE,
    site_url TEXT NOT NULL DEFAULT '',

    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',

    last_fetched_at TIMESTAMPTZ, -- last successful fetch of the feed's articles; NULL if never
    last_attempt_at TIMESTAMPTZ, -- last fetch attempt, successful or not; NULL if never
    last_error TEXT NOT NULL DEFAULT '',             -- why the last attempt failed; '' after a success. Internal detail, never shown to users
    consecutive_failures INT NOT NULL DEFAULT 0,     -- failed attempts since the last success
    next_fetch_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- when background refresh should next fetch the feed
    etag TEXT NOT NULL DEFAULT '',          -- ETag of the last 2xx response, sent back as If-None-Match
    last_modified TEXT NOT NULL DEFAULT '', -- Last-Modified of the last 2xx response, sent back as If-Modified-Since
    gone_at TIMESTAMPTZ, -- set when the feed answered 410 Gone; it is no longer fetched
    refresh_requested_at TIMESTAMPTZ, -- set when a page view asks for a stale feed to be fetched; cleared when the outcome is recorded
    posts_per_week REAL NOT NULL DEFAULT 0, -- articles published in the last 30 days, per week; set by saveArticles and RecordNotModified

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX feeds_site_id_idx ON feeds (site_id);
CREATE INDEX feeds_next_fetch_at_idx ON feeds (next_fetch_at) WHERE gone_at IS NULL;

-- lookups is both the queue of site lookups that workers claim jobs from
-- and the cache of their results: one row per site key.
CREATE TABLE lookups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    site_key TEXT NOT NULL UNIQUE,   -- discovery.SiteKey of the input
    url TEXT NOT NULL,               -- normalized input URL to fetch
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'done', 'failed')),
    site_id UUID REFERENCES sites(id) ON DELETE SET NULL, -- set when done with >= 1 feed
    error TEXT NOT NULL DEFAULT '',  -- internal detail for logs/debugging; never shown to users
    attempts INT NOT NULL DEFAULT 0,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX lookups_pending_idx ON lookups (requested_at) WHERE status = 'pending';

-- The site catalog (internal/catalog/sites.txt), replaced by www on every
-- start. Joined to sites through its lookup (lookups.site_key = site_key),
-- once the lookup found the site.
CREATE TABLE catalog_sites (
    site_key TEXT PRIMARY KEY,   -- discovery.SiteKey of url
    url TEXT NOT NULL,           -- the address its lookup fetches
    topic TEXT NOT NULL,         -- the [Topic] heading it's under
    position INT NOT NULL        -- order in the file, from 0
);

CREATE TABLE articles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    feed_id UUID NOT NULL
        REFERENCES feeds(id)
        ON DELETE CASCADE,

    -- Identity within the feed (item ID, else link, else a content hash);
    -- computed by internal/ingest.
    external_id TEXT NOT NULL,

    url TEXT NOT NULL DEFAULT '',
    canonical_url TEXT NOT NULL DEFAULT '', -- ingest.CanonicalURL(url); matches copies of one article across feeds
    has_copies BOOLEAN NOT NULL DEFAULT false, -- another article has the same non-empty canonical_url; set by saveArticles, never cleared
    image_url TEXT NOT NULL DEFAULT '', -- image the feed declares for the item; "" if none
    title TEXT NOT NULL DEFAULT '',   -- plain text
    summary TEXT NOT NULL DEFAULT '', -- raw HTML from the feed; sanitize before rendering as HTML
    content TEXT NOT NULL DEFAULT '', -- raw HTML from the feed; sanitize before rendering as HTML
    excerpt TEXT NOT NULL DEFAULT '', -- plain text from summary, else content; computed by internal/ingest
    page_image_url TEXT NOT NULL DEFAULT '',   -- og:image (else twitter:image) of the article's web page; set by internal/enrich
    page_title TEXT NOT NULL DEFAULT '',       -- og:title, else <title>, of the article's web page; plain text
    page_description TEXT NOT NULL DEFAULT '', -- <meta name="description">, else og:description; plain text
    media_type TEXT NOT NULL DEFAULT '',       -- MIME type of the item's first audio or video enclosure; "" if none
    authors TEXT[] NOT NULL DEFAULT '{}',      -- names of the item's authors, plain text; see feedparse.Item.Authors

    published_at TIMESTAMPTZ,         -- NULL if the feed gave no date
    published_date_only BOOLEAN NOT NULL DEFAULT false, -- the feed's date had no time of day
    timeline_at TIMESTAMPTZ NOT NULL, -- position in timelines (psql.timelineAt): news by its published date above a floor, backlog at its published date; set on insert, never updated

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- when it arrived; timelines group news by it (rss.Article.IsNews)
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (feed_id, external_id)
);

CREATE INDEX articles_feed_sort_idx ON articles (feed_id, (COALESCE(published_at, created_at)) DESC, id DESC);
CREATE INDEX articles_feed_timeline_idx ON articles (feed_id, timeline_at DESC, id DESC);
CREATE INDEX articles_feed_media_timeline_idx ON articles (feed_id, timeline_at DESC, id DESC) WHERE media_type <> '';
CREATE INDEX articles_canonical_url_idx ON articles (canonical_url) WHERE canonical_url <> '';

CREATE TABLE subscriptions (
    user_id UUID NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,

    feed_id UUID NOT NULL
        REFERENCES feeds(id)
        ON DELETE CASCADE,

    title TEXT NOT NULL DEFAULT '', -- the user's name for the feed; '' uses feeds.title
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, feed_id)
);

CREATE INDEX subscriptions_feed_id_idx ON subscriptions (feed_id);

CREATE TABLE reads (
    user_id UUID NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,

    article_id UUID NOT NULL
        REFERENCES articles(id)
        ON DELETE CASCADE,

    read_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, article_id)
);

CREATE INDEX reads_article_id_idx ON reads (article_id);

-- Where each user's "You were here" divider goes (see rss.TimelineMark).
-- One row per user, written by the timeline's beacon; no row until the
-- first report.
CREATE TABLE timeline_marks (
    user_id UUID PRIMARY KEY
        REFERENCES users(id)
        ON DELETE CASCADE,
    seen_up_to TIMESTAMPTZ NOT NULL, -- the newest timeline_at the user has had on screen
    seen_as_of TIMESTAMPTZ NOT NULL, -- when the page that reported seen_up_to was rendered: the latest look
    seen_at TIMESTAMPTZ NOT NULL,    -- when seen_up_to was last reported: the end of the latest visit so far
    divider_at TIMESTAMPTZ,          -- the divider of the visit seen_at belongs to; NULL during the first visit
    divider_as_of TIMESTAMPTZ        -- the previous visit's latest look (its seen_as_of); NULL during the first visit
);

-- Articles a user saved to keep. Saving covers every copy of an article
-- (same canonical_url); see psql.SaveRepository.
CREATE TABLE saves (
    user_id UUID NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,

    article_id UUID NOT NULL
        REFERENCES articles(id)
        ON DELETE CASCADE,

    saved_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, article_id)
);

CREATE INDEX saves_user_saved_at_idx ON saves (user_id, saved_at DESC, article_id DESC);
CREATE INDEX saves_article_id_idx ON saves (article_id);

-- Articles a user starred: "this is good". Starring covers every copy of an
-- article (same canonical_url); see psql.StarRepository.
CREATE TABLE stars (
    user_id UUID NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,

    article_id UUID NOT NULL
        REFERENCES articles(id)
        ON DELETE CASCADE,

    starred_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, article_id)
);

CREATE INDEX stars_user_starred_at_idx ON stars (user_id, starred_at DESC, article_id DESC);
CREATE INDEX stars_article_id_idx ON stars (article_id);

-- A user's labels for their subscriptions. Private to the user.
CREATE TABLE tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,

    name citext NOT NULL, -- trimmed, inner spaces collapsed; unique per user ignoring case
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (user_id, name),
    UNIQUE (id, user_id) -- for the composite foreign keys below
);

CREATE TABLE subscription_tags (
    user_id UUID NOT NULL,
    feed_id UUID NOT NULL,
    tag_id UUID NOT NULL,

    PRIMARY KEY (user_id, feed_id, tag_id),
    FOREIGN KEY (user_id, feed_id) REFERENCES subscriptions (user_id, feed_id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id, user_id) REFERENCES tags (id, user_id) ON DELETE CASCADE
);

CREATE INDEX subscription_tags_tag_id_idx ON subscription_tags (tag_id);

-- A user's custom timelines, shown as tabs after Home (which shows every
-- subscription and has no row). Sources are timeline_tags and
-- timeline_feeds rows; the other conditions are columns.
CREATE TABLE timelines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,

    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    position INT NOT NULL DEFAULT 0,      -- tab order after Home, lowest first
    all_feeds BOOLEAN NOT NULL DEFAULT true, -- false: only the feeds of its included tags and feeds
    starred_only BOOLEAN NOT NULL DEFAULT false,
    max_posts_per_week INT NOT NULL DEFAULT 0, -- 0: any posting rate
    include_words TEXT[] NOT NULL DEFAULT '{}', -- an article must mention one of them; empty: no condition
    exclude_words TEXT[] NOT NULL DEFAULT '{}', -- an article mentioning one of them is left out
    media_only BOOLEAN NOT NULL DEFAULT false,  -- only articles with an audio or video enclosure
    share_token TEXT UNIQUE,              -- NULL: not shared; else the public link's token
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (id, user_id) -- for the composite foreign keys below
);

CREATE INDEX timelines_user_position_idx ON timelines (user_id, position);

CREATE TABLE timeline_tags (
    timeline_id UUID NOT NULL,
    user_id UUID NOT NULL,
    tag_id UUID NOT NULL,
    exclude BOOLEAN NOT NULL, -- false: show the tag's feeds; true: leave them out

    PRIMARY KEY (timeline_id, tag_id),
    FOREIGN KEY (timeline_id, user_id) REFERENCES timelines (id, user_id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id, user_id) REFERENCES tags (id, user_id) ON DELETE CASCADE
);

CREATE INDEX timeline_tags_tag_id_idx ON timeline_tags (tag_id);

CREATE TABLE timeline_feeds (
    timeline_id UUID NOT NULL,
    user_id UUID NOT NULL,
    feed_id UUID NOT NULL,
    exclude BOOLEAN NOT NULL, -- false: show the feed; true: leave it out

    PRIMARY KEY (timeline_id, feed_id),
    FOREIGN KEY (timeline_id, user_id) REFERENCES timelines (id, user_id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, feed_id) REFERENCES subscriptions (user_id, feed_id) ON DELETE CASCADE
);

CREATE INDEX timeline_feeds_user_feed_idx ON timeline_feeds (user_id, feed_id);

-- The enrichment queue: articles whose web page should be fetched for a
-- missing image, title or excerpt. A row is deleted once its work is done
-- or given up on.
CREATE TABLE enrich_jobs (
    article_id UUID PRIMARY KEY
        REFERENCES articles(id)
        ON DELETE CASCADE,
    priority SMALLINT NOT NULL DEFAULT 0, -- 0: new arrivals; 1: backlog from a feed's first save; lower goes first
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- when a worker may claim it; also the claim lease
    last_error TEXT NOT NULL DEFAULT '', -- internal detail for logs; never shown to users
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX enrich_jobs_due_idx ON enrich_jobs (priority, next_attempt_at);

-- A site's icon, downloaded and checked by the icon worker and served
-- from /sites/{id}/icon. No row: the site shows its letter avatar.
CREATE TABLE site_icons (
    site_id UUID PRIMARY KEY
        REFERENCES sites(id)
        ON DELETE CASCADE,
    source_url TEXT NOT NULL,    -- where it was downloaded from
    content_type TEXT NOT NULL
        CHECK (content_type IN ('image/png', 'image/jpeg', 'image/gif', 'image/x-icon')),
    width INT NOT NULL,          -- pixels; for an ICO, its largest image
    height INT NOT NULL,
    data BYTEA NOT NULL,         -- at most 256 KiB
    hash TEXT NOT NULL,          -- hex SHA-256 of data; the ?v= of icon URLs
    etag TEXT NOT NULL DEFAULT '',          -- of the download, sent back as If-None-Match
    last_modified TEXT NOT NULL DEFAULT '', -- of the download, sent back as If-Modified-Since
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One OPML file a user uploaded. Its feeds are import_feeds rows, which
-- are also the import queue.
CREATE TABLE imports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,

    filename TEXT NOT NULL DEFAULT '', -- as uploaded; shown on the results page
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX imports_user_id_idx ON imports (user_id, created_at DESC);

CREATE TABLE import_feeds (
    import_id UUID NOT NULL
        REFERENCES imports(id)
        ON DELETE CASCADE,
    position INT NOT NULL,           -- order in the file, from 0
    url TEXT NOT NULL,               -- the outline's xmlUrl, trimmed
    title TEXT NOT NULL DEFAULT '',  -- the outline's title, else its text
    tags TEXT[] NOT NULL DEFAULT '{}', -- the names of the folders the outline is in; applied as tags on subscribe
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'subscribed', 'already_subscribed', 'failed')),
    failure TEXT NOT NULL DEFAULT ''
        CHECK (failure IN ('', 'invalid_url', 'unreachable', 'not_feed')),
    feed_id UUID REFERENCES feeds(id) ON DELETE SET NULL, -- the feed subscribed to, once known
    error TEXT NOT NULL DEFAULT '',  -- internal detail for logs; never shown to users
    attempts INT NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,

    PRIMARY KEY (import_id, position)
);

CREATE INDEX import_feeds_pending_idx ON import_feeds (started_at) WHERE status = 'pending';

-- One row per logged-in device. Its id is kept in the scs session; deleting
-- the row logs that device out.
CREATE TABLE user_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now() -- updated at most once a day; the session ends 90 days after it
);

CREATE INDEX user_sessions_user_id_idx ON user_sessions (user_id);

-- One-time tokens sent in email links. Only a SHA-256 hash of each token is
-- stored; a token is deleted when it's used.
CREATE TABLE user_tokens (
    hash BYTEA PRIMARY KEY, -- sha256 of the token in the link

    user_id UUID NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,

    purpose TEXT NOT NULL
        CHECK (purpose IN ('confirm_email', 'reset_password', 'change_email')),
    email citext NOT NULL, -- the address it was sent to: the account's, or the new address for change_email
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX user_tokens_user_id_idx ON user_tokens (user_id);

-- The outbox: emails to send, worked by the mail runner (internal/mail).
CREATE TABLE emails (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    to_address citext NOT NULL, -- not a foreign key: the "account deleted" email outlives its user
    subject TEXT NOT NULL,
    text_body TEXT NOT NULL, -- cleared once sent or given up on: some carry one-time links
    html_body TEXT NOT NULL, -- likewise
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'sent', 'failed')),
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- when a worker may claim it; also the claim lease
    last_error TEXT NOT NULL DEFAULT '', -- internal detail for logs; never shown to users
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ
);

CREATE INDEX emails_pending_idx ON emails (next_attempt_at) WHERE status = 'pending';
CREATE INDEX emails_to_address_created_at_idx ON emails (to_address, created_at); -- the per-address hourly cap

CREATE TABLE sessions (
  token TEXT PRIMARY KEY,
  data BYTEA NOT NULL,
  expiry TIMESTAMPTZ NOT NULL
);

CREATE INDEX sessions_expiry_idx ON sessions (expiry);

-- +goose Down
DROP TABLE emails;
DROP TABLE user_tokens;
DROP TABLE user_sessions;
DROP TABLE import_feeds;
DROP TABLE imports;
DROP TABLE site_icons;
DROP TABLE enrich_jobs;
DROP TABLE timeline_feeds;
DROP TABLE timeline_tags;
DROP TABLE timelines;
DROP TABLE subscription_tags;
DROP TABLE tags;
DROP TABLE stars;
DROP TABLE saves;
DROP TABLE timeline_marks;
DROP TABLE reads;
DROP TABLE subscriptions;
DROP TABLE articles;
DROP TABLE catalog_sites;
DROP TABLE lookups;
DROP TABLE feeds;
DROP TABLE sites;
DROP TABLE users;
DROP TABLE sessions;

DROP EXTENSION IF EXISTS citext;
