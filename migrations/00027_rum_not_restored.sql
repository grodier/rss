-- +goose Up
-- Why a Back or Forward wasn't restored from the back/forward cache: the
-- top-level reasons' names Chrome reports (notRestoredReasons), at most 3;
-- empty for every other view.
ALTER TABLE rum_views ADD COLUMN not_restored TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE rum_views DROP COLUMN not_restored;
