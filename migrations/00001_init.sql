-- +goose Up
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    email TEXT NOT NULL UNIQUE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE feeds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    url TEXT NOT NULL UNIQUE,
    site_url TEXT,

    title TEXT NOT NULL,
    description TEXT,

    last_fetched_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

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

-- +goose Down
DROP TABLE users;
DROP TABLE feeds;
DROP TABLE articles;
DROP TABLE subscriptions;
