-- +goose Up
-- Feeds an admin left out of Discover's catalog from the picking page
-- (#649), so it stops offering them. Unskipping deletes the row, and so
-- does putting the feed in the catalog.
CREATE TABLE discover_skips (
    feed_id UUID PRIMARY KEY REFERENCES feeds(id) ON DELETE CASCADE,
    skipped_by UUID REFERENCES admins(id) ON DELETE SET NULL, -- NULL once that admin is deleted
    skipped_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE discover_skips;
