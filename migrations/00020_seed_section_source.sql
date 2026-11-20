-- +goose Up
-- A seed list entry's section hint and where it came from (#649's bulk
-- upload). The hint pre-selects the section of the site's feeds when an
-- admin picks them for Discover; it never puts them in the catalog.
ALTER TABLE seed_sites ADD COLUMN section_id UUID REFERENCES discover_sections(id) ON DELETE SET NULL; -- NULL: no hint
ALTER TABLE seed_sites ADD COLUMN source TEXT NOT NULL DEFAULT ''; -- as the uploader named it; "" for none

-- +goose Down
ALTER TABLE seed_sites DROP COLUMN source;
ALTER TABLE seed_sites DROP COLUMN section_id;
