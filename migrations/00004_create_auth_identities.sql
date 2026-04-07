-- +goose Up
CREATE TABLE auth_identities (
    id             uuid PRIMARY KEY,
    user_id        uuid NOT NULL REFERENCES users(id),
    provider       text NOT NULL,
    identifier     text NOT NULL,
    password_hash  text,
    verified_at    timestamptz,
    is_primary     boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    deleted_at     timestamptz,
    purge_after    timestamptz
);

CREATE UNIQUE INDEX auth_identities_one_primary_email_per_user
    ON auth_identities(user_id)
    WHERE provider = 'email'
    AND is_primary = true
    AND deleted_at IS NULL;

CREATE UNIQUE INDEX auth_identities_unique_verified_email
    ON auth_identities(lower(identifier))
    WHERE provider = 'email'
    AND verified_at IS NOT NULL
    AND deleted_at IS NULL;

-- +goose Down
DROP TABLE auth_identities;
