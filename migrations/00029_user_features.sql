-- +goose Up
-- The features a reader has beyond the base app; a row means they have it.
-- Revoking deletes the row; the admin's audit log keeps the history.
CREATE TABLE user_features (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feature TEXT NOT NULL CHECK (feature IN ('newsletters')), -- newsletters: an address that turns newsletters into feeds
    source TEXT NOT NULL CHECK (source IN ('admin')),           -- who granted it: an admin; billing will add 'plan'
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, feature)
);

-- +goose Down
DROP TABLE user_features;
