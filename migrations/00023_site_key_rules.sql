-- +goose Up
-- The version of discovery's site-key rules whose re-key last finished
-- (www migrate's re-key step, #727): one row. 0 is before any re-key, so
-- the first deploy with SiteKeyRulesVersion 1 splits the sites merged on
-- shared hosts before #726.
CREATE TABLE site_key_rules (
    version INT NOT NULL -- the discovery.SiteKeyRulesVersion whose re-key last finished
);
INSERT INTO site_key_rules (version) VALUES (0);

-- +goose Down
DROP TABLE site_key_rules;
