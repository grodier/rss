-- +goose Up
-- What a feed says it's about (#649): see feedparse.Meta.Categories. Shown
-- to admins picking Discover's feeds; empty until the feed's next fetch.
ALTER TABLE feeds ADD COLUMN categories TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE feeds DROP COLUMN categories;
