-- +goose Up
-- Feeds picked for Discover (#628), each under one section. Edited in the
-- admin.
CREATE TABLE discover_feeds (
    feed_id UUID PRIMARY KEY REFERENCES feeds(id) ON DELETE CASCADE,
    section TEXT NOT NULL,           -- an rss.Sections Name
    added_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX discover_feeds_section_idx ON discover_feeds (section);

-- +goose Down
DROP TABLE discover_feeds;
