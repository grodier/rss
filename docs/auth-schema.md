# Auth Schema Reference

This document describes the database tables that support the auth system. All tables use UUIDv7 primary keys and `timestamptz` for timestamps.

## `users`

The root identity record. Every person who signs up gets one `users` row, regardless of how many accounts they belong to or how they authenticate.

| Column | Purpose |
|---|---|
| `id` | UUIDv7 primary key (encodes creation time) |
| `display_name` | Optional human-readable name shown in UI |
| `created_at` | Row creation timestamp |
| `deleted_at` | Soft-delete marker; non-NULL means the user has been deactivated |
| `purge_after` | Scheduled hard-delete date for GDPR/data-retention compliance |

## `accounts`

A billing/organizational boundary. Users interact with the system through an account (selected at login). An account can have many members.

| Column | Purpose |
|---|---|
| `id` | UUIDv7 primary key |
| `name` | Optional display name for the account (e.g. team or org name) |
| `created_at` | Row creation timestamp |
| `deleted_at` | Soft-delete marker |
| `purge_after` | Scheduled hard-delete date |

## `memberships`

Join table linking users to accounts with a role. The composite primary key `(user_id, account_id)` enforces one membership per user-account pair.

| Column | Purpose |
|---|---|
| `user_id` | FK to `users.id` |
| `account_id` | FK to `accounts.id` |
| `role` | One of `owner`, `admin`, `member` — enforced by CHECK constraint |
| `created_at` | When the user joined the account |
| `last_used_at` | Updated on login/switch; used to default the user to their most-recently-used account |

## `auth_identities`

Stores each way a user can prove who they are (email/password, OAuth provider, etc.). A user may have multiple identities but only one primary email.

| Column | Purpose |
|---|---|
| `id` | UUIDv7 primary key |
| `user_id` | FK to `users.id` |
| `provider` | Identity type: `email`, `google`, `github`, etc. |
| `identifier` | Provider-scoped identifier (email address, OAuth subject ID) |
| `password_hash` | Bcrypt/argon2 hash; NULL for OAuth-only identities |
| `verified_at` | When the identity was verified (e.g. email confirmation click) |
| `is_primary` | Whether this is the user's primary email; enforced unique per user by partial index |
| `created_at` | Row creation timestamp |
| `deleted_at` | Soft-delete marker |
| `purge_after` | Scheduled hard-delete date |

### Partial indexes

- **`auth_identities_one_primary_email_per_user`** — ensures at most one active primary email per user
- **`auth_identities_unique_verified_email`** — ensures no two active users share the same verified email (case-insensitive)

## `auth_tokens`

Short-lived, single-use tokens for out-of-band flows: email verification, password reset, magic links, etc.

| Column | Purpose |
|---|---|
| `id` | UUIDv7 primary key |
| `user_id` | FK to `users.id` — who this token was issued to |
| `identity_id` | FK to `auth_identities.id` — optional; ties the token to a specific identity (e.g. verify *this* email) |
| `type` | Token purpose: `email_verification`, `password_reset`, `magic_link`, etc. |
| `token_hash` | Hash of the actual token value (never store plaintext) |
| `created_at` | Row creation timestamp |
| `expires_at` | Absolute expiry; token is invalid after this time |
| `used_at` | Set on first use; prevents replay |

### Indexes

- **`auth_tokens_token_hash`** — unique partial index for token lookup, filtered to unconsumed (`used_at IS NULL`) tokens; enforces one unconsumed token per hash

## `sessions`

API-owned session records. Each row represents an active (or revoked) login session, scoped to a specific user + account pair. The API validates sessions via `Bearer` token on every request.

| Column | Purpose |
|---|---|
| `id` | UUIDv7 primary key |
| `user_id` | FK to `users.id` |
| `account_id` | FK to `accounts.id` — the account this session is acting on |
| `token_hash` | Hash of the opaque Bearer token sent by the client |
| `created_at` | Row creation timestamp |
| `last_activity_at` | Updated on each authenticated request; drives sliding idle timeout |
| `expires_at` | Absolute session cap; session is invalid after this regardless of activity |
| `ip_address` | Client IP at session creation (audit/anomaly detection) |
| `user_agent` | Client User-Agent at session creation (audit/anomaly detection) |
| `last_step_up_at` | When the user last re-authenticated for a sensitive operation |
| `revoked_at` | Non-NULL means session is explicitly revoked (logout, admin action) |

### Indexes

- **`sessions_token_hash`** — unique partial index for token lookup, filtered to active (non-revoked) sessions; enforces one active session per token hash
- **`sessions_user_id`** — list active sessions for a user (e.g. "manage sessions" UI)
