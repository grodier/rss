-- +goose Up
CREATE TABLE auth_tokens (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id),
    identity_id  uuid REFERENCES auth_identities(id),
    type         text NOT NULL,
    token_hash   text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    used_at      timestamptz
);

-- +goose Down
DROP TABLE auth_tokens;
