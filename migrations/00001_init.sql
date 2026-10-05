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

    last_fetched_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX feeds_site_id_idx ON feeds (site_id);

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

    external_id TEXT,

    url TEXT,
    title TEXT NOT NULL,
    summary TEXT,
    content TEXT,

    published_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

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

CREATE TABLE sessions (
  token TEXT PRIMARY KEY,
  data BYTEA NOT NULL,
  expiry TIMESTAMPTZ NOT NULL
);

CREATE INDEX sessions_expiry_idx ON sessions (expiry);

-- +goose Down
DROP TABLE subscriptions;
DROP TABLE articles;
DROP TABLE lookups;
DROP TABLE feeds;
DROP TABLE sites;
DROP TABLE users;
DROP TABLE sessions;

DROP EXTENSION IF EXISTS citext;
