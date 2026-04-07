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

CREATE UNIQUE INDEX auth_tokens_token_hash ON auth_tokens(token_hash) WHERE used_at IS NULL;

-- +goose Down
DROP TABLE auth_tokens;
