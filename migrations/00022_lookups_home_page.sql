-- +goose Up
-- Whether a lookup ran on its site's root (discovery.IsHomePage), which a
-- site on a shared host has at a path ("https://medium.com/@alice", #726),
-- so it can no longer be read from the url. Existing rows ran on "/".
ALTER TABLE lookups ADD COLUMN home_page BOOLEAN NOT NULL DEFAULT false; -- the lookup ran on its site's root (discovery.IsHomePage)
UPDATE lookups SET home_page = url ~ '^https?://[^/?#]+/$';

-- +goose Down
ALTER TABLE lookups DROP COLUMN home_page;
