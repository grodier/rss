-- +goose Up
-- Single-use signup invites (internal/server signup; created by the admin).
CREATE TABLE invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash BYTEA NOT NULL UNIQUE, -- sha256 of the token in /signup?invite=
    email citext, -- NULL for an open invite; else only this address can sign up with it
    waitlist_id UUID REFERENCES waitlist(id) ON DELETE SET NULL, -- the entry it was sent to
    note TEXT NOT NULL DEFAULT '', -- who it's for (open invites); shown only in the admin
    wave TEXT NOT NULL DEFAULT '', -- the label of the wave it went out in (#574)
    expires_at TIMESTAMPTZ NOT NULL,
    redeemed_at TIMESTAMPTZ,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL, -- the account made with it
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (redeemed_at IS NULL OR revoked_at IS NULL)
);

CREATE INDEX invites_waitlist_id_idx ON invites (waitlist_id);

-- +goose Down
DROP TABLE invites;
