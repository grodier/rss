-- +goose Up
CREATE TABLE accounts (
    id            uuid PRIMARY KEY,
    name          text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz,
    purge_after   timestamptz
);

-- +goose Down
DROP TABLE accounts;
