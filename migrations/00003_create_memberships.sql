-- +goose Up
CREATE TABLE memberships (
    user_id      uuid NOT NULL REFERENCES users(id),
    account_id   uuid NOT NULL REFERENCES accounts(id),
    role         text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, account_id)
);

-- +goose Down
DROP TABLE memberships;
