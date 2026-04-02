# PR 3: Database Migrations

## Context

The auth system needs 6 tables: users, accounts, memberships, auth_identities, auth_tokens, and sessions. All tables use UUIDv7 primary keys and `timestamptz` for timestamps. The sessions table is API-owned (not BFF-owned as in the original docs).

Key schema decisions:
- `auth_identities`: partial unique indexes for one-primary-email-per-user and verified-email-uniqueness
- `memberships`: includes `last_used_at` for default-to-last-used-account at login
- `sessions`: includes `token_hash`, `last_activity_at` (sliding idle), `expires_at` (absolute cap), `last_step_up_at`
- Soft delete pattern: `deleted_at` + `purge_after` on users, accounts, and auth_identities

See `plans/auth-implementation.md` for the full plan and sessions table schema.

## Prerequisites

- PR 2 (error format) — no direct dependency, but keeps PRs ordered

## Scope

Create 6 Goose SQL migrations:

### `migrations/00001_create_users.sql`

```sql
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
```

### `migrations/00002_create_accounts.sql`

```sql
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
```

### `migrations/00003_create_memberships.sql`

```sql
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
```

### `migrations/00004_create_auth_identities.sql`

```sql
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
```

### `migrations/00005_create_auth_tokens.sql`

```sql
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
```

### `migrations/00006_create_sessions.sql`

```sql
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
```

## Files Changed

- `migrations/00001_create_users.sql`
- `migrations/00002_create_accounts.sql`
- `migrations/00003_create_memberships.sql`
- `migrations/00004_create_auth_identities.sql`
- `migrations/00005_create_auth_tokens.sql`
- `migrations/00006_create_sessions.sql`

## Verification

- `make db/reset` — migrations apply cleanly
- `make db/psql` then:
  - `\dt` — shows 6 tables (plus goose_db_version)
  - `\di` — shows partial unique indexes on auth_identities and indexes on sessions
  - `\d sessions` — confirm all columns match plan schema
  - `\d memberships` — confirm `last_used_at` column exists
- `make db/migrations/down` — all tables drop cleanly
- `make db/migrations/up` — re-apply cleanly (idempotent)
