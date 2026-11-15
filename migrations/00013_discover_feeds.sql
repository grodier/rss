-- +goose Up
-- Discover's sections (#628), in Discover's default order. They're rows,
-- not a list in code, so one can be renamed, added or dropped without a
-- deploy; feeds refer to them by id, so a rename changes nothing else.
CREATE TABLE discover_sections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'), -- in URLs
    name TEXT NOT NULL UNIQUE CHECK (name <> ''),                        -- as shown
    position INTEGER NOT NULL,                                           -- the default order, ascending
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO discover_sections (slug, name, position) VALUES
    ('news', 'News', 1),
    ('technology', 'Technology', 2),
    ('programming', 'Programming', 3),
    ('science', 'Science', 4),
    ('business-and-economics', 'Business and economics', 5),
    ('culture', 'Culture', 6),
    ('books', 'Books', 7),
    ('design', 'Design', 8),
    ('food', 'Food', 9),
    ('sport', 'Sport', 10),
    ('games', 'Games', 11),
    ('music', 'Music', 12),
    ('film-and-tv', 'Film and TV', 13),
    ('health', 'Health', 14),
    ('travel', 'Travel', 15);

-- Feeds picked for Discover (#628), each under one section. Edited in the
-- admin. A section can't be deleted while feeds are under it.
CREATE TABLE discover_feeds (
    feed_id UUID PRIMARY KEY REFERENCES feeds(id) ON DELETE CASCADE,
    section_id UUID NOT NULL REFERENCES discover_sections(id),
    added_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX discover_feeds_section_id_idx ON discover_feeds (section_id);

-- +goose Down
DROP TABLE discover_feeds;
DROP TABLE discover_sections;
