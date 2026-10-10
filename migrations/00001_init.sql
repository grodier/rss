-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    email citext NOT NULL UNIQUE,
    hashed_password bytea NOT NULL,
    created_at timestamp(0) with time zone NOT NULL DEFAULT now()
);

CREATE TABLE sites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

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
    image_url TEXT NOT NULL DEFAULT '', -- image the feed declares for the item; "" if none
    title TEXT NOT NULL DEFAULT '',   -- plain text
    summary TEXT NOT NULL DEFAULT '', -- raw HTML from the feed; sanitize before rendering as HTML
    content TEXT NOT NULL DEFAULT '', -- raw HTML from the feed; sanitize before rendering as HTML
    excerpt TEXT NOT NULL DEFAULT '', -- plain text from summary, else content; computed by internal/ingest

    published_at TIMESTAMPTZ,         -- NULL if the feed gave no date
    timeline_at TIMESTAMPTZ NOT NULL, -- position in timelines: arrival time for news, published date for backlog; set on insert, never updated

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (feed_id, external_id)
);

CREATE INDEX articles_feed_sort_idx ON articles (feed_id, (COALESCE(published_at, created_at)) DESC, id DESC);
CREATE INDEX articles_feed_timeline_idx ON articles (feed_id, timeline_at DESC, id DESC);
CREATE INDEX articles_canonical_url_idx ON articles (canonical_url) WHERE canonical_url <> '';

CREATE TABLE subscriptions (
    user_id UUID NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,

    feed_id UUID NOT NULL
        REFERENCES feeds(id)
        ON DELETE CASCADE,

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

CREATE TABLE sessions (
  token TEXT PRIMARY KEY,
  data BYTEA NOT NULL,
  expiry TIMESTAMPTZ NOT NULL
);

CREATE INDEX sessions_expiry_idx ON sessions (expiry);

-- +goose Down
DROP TABLE reads;
DROP TABLE subscriptions;
DROP TABLE articles;
DROP TABLE lookups;
DROP TABLE feeds;
DROP TABLE sites;
DROP TABLE users;
DROP TABLE sessions;

DROP EXTENSION IF EXISTS citext;
