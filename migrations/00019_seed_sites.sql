-- +goose Up
-- The seed list (#649): sites to look up before anyone searches for them,
-- added in the admin. It replaces internal/seed/sites.txt, which listed no
-- sites. A row is the admin's intent; the lookup with its site key
-- (lookups.site_key) is how it went.
CREATE TABLE seed_sites (
    site_key TEXT PRIMARY KEY,  -- discovery.SiteKey of url: one entry per site, as a site has one lookup
    url TEXT NOT NULL,          -- the address to look up (discovery.ParseInput, as a string)
    added_by UUID REFERENCES admins(id) ON DELETE SET NULL, -- NULL once that admin is deleted
    added_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX seed_sites_added_at_idx ON seed_sites (added_at DESC, site_key DESC);

-- +goose Down
DROP TABLE seed_sites;
