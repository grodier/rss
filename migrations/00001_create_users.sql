-- +goose Up
CREATE TABLE users (
    id            uuid PRIMARY KEY,
    display_name  text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz,
    purge_after   timestamptz
);

-- +goose Down
DROP TABLE users;
