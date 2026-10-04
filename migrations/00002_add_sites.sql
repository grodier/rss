-- +goose Up
CREATE TABLE sites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE feeds
    ADD COLUMN site_id UUID REFERENCES sites(id) ON DELETE SET NULL;

CREATE INDEX feeds_site_id_idx ON feeds (site_id);

-- +goose Down
DROP INDEX feeds_site_id_idx;
ALTER TABLE feeds DROP COLUMN site_id;
DROP TABLE sites;
