-- +goose Up
-- People waiting for an invite (internal/server waitlist.go). An entry
-- counts once confirmed_at is set; leaving deletes it.
CREATE TABLE waitlist (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email citext NOT NULL UNIQUE,
    name TEXT NOT NULL,
    current_reader TEXT NOT NULL DEFAULT ''
        CHECK (current_reader IN ('', 'none', 'feedly', 'inoreader', 'netnewswire', 'other')), -- '' if not answered
    reading_interests TEXT NOT NULL DEFAULT '', -- free text, at most 500 characters; '' if not answered
    source TEXT NOT NULL DEFAULT '', -- the page's ?ref=: at most 64 of [a-z0-9-], else ''
    token_hash BYTEA NOT NULL UNIQUE, -- sha256 of the entry's link token: confirms it, and leaves the list
    confirmed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX waitlist_created_at_idx ON waitlist (created_at) WHERE confirmed_at IS NOT NULL;

-- +goose Down
DROP TABLE waitlist;
