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

CREATE UNIQUE INDEX auth_tokens_token_hash ON auth_tokens(token_hash) WHERE used_at IS NULL;

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

CREATE UNIQUE INDEX sessions_token_hash ON sessions(token_hash) WHERE revoked_at IS NULL;
CREATE INDEX sessions_user_id ON sessions(user_id) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE sessions;
```

## Schema Documentation

Create `docs/auth-schema.md` — a developer reference documenting the purpose of each table and its columns. One section per table:

### `users`

The root identity record. Every person who signs up gets one `users` row, regardless of how many accounts they belong to or how they authenticate.

| Column | Purpose |
|---|---|
| `id` | UUIDv7 primary key (encodes creation time) |
| `display_name` | Optional human-readable name shown in UI |
| `created_at` | Row creation timestamp |
| `deleted_at` | Soft-delete marker; non-NULL means the user has been deactivated |
| `purge_after` | Scheduled hard-delete date for GDPR/data-retention compliance |

### `accounts`

A billing/organizational boundary. Users interact with the system through an account (selected at login). An account can have many members.

| Column | Purpose |
|---|---|
| `id` | UUIDv7 primary key |
| `name` | Optional display name for the account (e.g. team or org name) |
| `created_at` | Row creation timestamp |
| `deleted_at` | Soft-delete marker |
| `purge_after` | Scheduled hard-delete date |

### `memberships`

Join table linking users to accounts with a role. The composite primary key `(user_id, account_id)` enforces one membership per user-account pair.

| Column | Purpose |
|---|---|
| `user_id` | FK → `users.id` |
| `account_id` | FK → `accounts.id` |
| `role` | One of `owner`, `admin`, `member` — enforced by CHECK constraint |
| `created_at` | When the user joined the account |
| `last_used_at` | Updated on login/switch; used to default the user to their most-recently-used account |

### `auth_identities`

Stores each way a user can prove who they are (email/password, OAuth provider, etc.). A user may have multiple identities but only one primary email.

| Column | Purpose |
|---|---|
| `id` | UUIDv7 primary key |
| `user_id` | FK → `users.id` |
| `provider` | Identity type: `email`, `google`, `github`, etc. |
| `identifier` | Provider-scoped identifier (email address, OAuth subject ID) |
| `password_hash` | Bcrypt/argon2 hash; NULL for OAuth-only identities |
| `verified_at` | When the identity was verified (e.g. email confirmation click) |
| `is_primary` | Whether this is the user's primary email; enforced unique per user by partial index |
| `created_at` | Row creation timestamp |
| `deleted_at` | Soft-delete marker |
| `purge_after` | Scheduled hard-delete date |

**Partial indexes:**
- `auth_identities_one_primary_email_per_user` — ensures at most one active primary email per user
- `auth_identities_unique_verified_email` — ensures no two active users share the same verified email (case-insensitive)

### `auth_tokens`

Short-lived, single-use tokens for out-of-band flows: email verification, password reset, magic links, etc.

| Column | Purpose |
|---|---|
| `id` | UUIDv7 primary key |
| `user_id` | FK → `users.id` — who this token was issued to |
| `identity_id` | FK → `auth_identities.id` — optional; ties the token to a specific identity (e.g. verify *this* email) |
| `type` | Token purpose: `email_verification`, `password_reset`, `magic_link`, etc. |
| `token_hash` | Hash of the actual token value (never store plaintext) |
| `created_at` | Row creation timestamp |
| `expires_at` | Absolute expiry; token is invalid after this time |
| `used_at` | Set on first use; prevents replay |

**Indexes:**
- `auth_tokens_token_hash` — unique partial index for token lookup, filtered to unconsumed (`used_at IS NULL`) tokens; enforces one unconsumed token per hash

### `sessions`

API-owned session records. Each row represents an active (or revoked) login session, scoped to a specific user + account pair. The API validates sessions via `Bearer` token on every request.

| Column | Purpose |
|---|---|
| `id` | UUIDv7 primary key |
| `user_id` | FK → `users.id` |
| `account_id` | FK → `accounts.id` — the account this session is acting on |
| `token_hash` | Hash of the opaque Bearer token sent by the client |
| `created_at` | Row creation timestamp |
| `last_activity_at` | Updated on each authenticated request; drives sliding idle timeout |
| `expires_at` | Absolute session cap; session is invalid after this regardless of activity |
| `ip_address` | Client IP at session creation (audit/anomaly detection) |
| `user_agent` | Client User-Agent at session creation (audit/anomaly detection) |
| `last_step_up_at` | When the user last re-authenticated for a sensitive operation |
| `revoked_at` | Non-NULL means session is explicitly revoked (logout, admin action) |

**Indexes:**
- `sessions_token_hash` — unique partial index for token lookup, filtered to active (non-revoked) sessions; enforces one active session per token hash
- `sessions_user_id` — list active sessions for a user (e.g. "manage sessions" UI)

## Files Changed

- `docs/auth-schema.md`
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
