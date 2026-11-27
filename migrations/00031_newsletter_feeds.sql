-- +goose Up
ALTER TABLE feeds ADD COLUMN owner_id UUID REFERENCES users(id) ON DELETE CASCADE; -- set on a reader's newsletter feed, which only they can see; NULL on feeds fetched from the web
CREATE INDEX feeds_owner_idx ON feeds (owner_id) WHERE owner_id IS NOT NULL;
-- The internal site of every newsletter feed; never listed, matched, fetched or checked.
-- icon_checked_at is set so the icon runner never claims it (siteIconDue claims sites never checked).
INSERT INTO sites (host, url, title, icon_checked_at) VALUES ('newsletters', '', 'Newsletters', now())
ON CONFLICT (host) DO NOTHING;

-- +goose Down
DELETE FROM sites WHERE host = 'newsletters';
DROP INDEX feeds_owner_idx;
ALTER TABLE feeds DROP COLUMN owner_id;
