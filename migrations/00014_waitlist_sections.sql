-- +goose Up
-- The answer to the waitlist's "What do you like to read?" (#642): the IDs
-- of the Discover sections (discover_sections) they chose, in Discover's
-- default order. IDs, not names, as for catalog feeds, so a renamed section
-- is still the one they chose; an ID whose section was dropped is ignored.
ALTER TABLE waitlist ADD COLUMN sections UUID[] NOT NULL DEFAULT '{}'; -- empty if not answered
ALTER TABLE users ADD COLUMN sections UUID[] NOT NULL DEFAULT '{}';    -- copied from their waitlist entry at signup; orders Discover (#643)

-- +goose Down
ALTER TABLE users DROP COLUMN sections;
ALTER TABLE waitlist DROP COLUMN sections;
