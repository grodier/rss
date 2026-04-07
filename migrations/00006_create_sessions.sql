-- +goose Up
CREATE TABLE sessions (
    id               uuid PRIMARY KEY,
    user_id          uuid NOT NULL REFERENCES users(id),
    account_id       uuid NOT NULL REFERENCES accounts(id),
    token_hash       text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    last_activity_at timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL,
    ip_address       inet,
    user_agent       text,
    last_step_up_at  timestamptz,
    revoked_at       timestamptz
);

CREATE INDEX sessions_token_hash ON sessions(token_hash) WHERE revoked_at IS NULL;
CREATE INDEX sessions_user_id ON sessions(user_id) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE sessions;
