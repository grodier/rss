# Auth & Identity Architecture

## Overview

This document defines the authentication, identity, and session architecture for the application.

The system is designed to support:
• multiple login methods per user
• future OAuth/passkey providers
• team/workspace accounts
• strong security defaults
• excellent UX

The API owns authentication end-to-end — sessions, step-up auth, rate limiting, enumeration safety, and email delivery. Clients (BFF, native apps, CLI) are responsible only for their channel-specific concerns.

```
Browser <--cookie--> BFF <--Bearer token (opaque, DB-backed)--> API <--> Postgres
Native app <--Bearer token (opaque, DB-backed)--> API <--> Postgres
CLI <--Bearer token (opaque, DB-backed)--> API <--> Postgres
```

The API is the single source of truth for authentication. The BFF is a thin translation layer that converts browser cookies into Bearer tokens for the API.

---

## Core Design Principles

### User identity is separate from login method

Users can authenticate via multiple identities:
• email/password
• OAuth (future)
• passkeys (future)
• ATProto (future)

Each login method attaches to a single canonical user.

---

### Users belong to accounts

Even though the current product is primarily personal, the system supports:
• team accounts
• shared feeds
• enterprise features later

---

### Email is not the user identity

Users are identified by:

`user_id` (UUIDv7)

Emails exist only as authentication identities.

---

### Multiple login methods

A user may have multiple identities attached:

Examples:
• email/password
• google oauth
• github oauth
• passkey

---

### Multiple emails

Users may have multiple email identities.

Rules:
• one primary email
• primary email must be verified
• verified emails must be globally unique

---

### Grace period email verification

Users can begin using the application immediately after signup.

Restricted actions require verification.

---

### Password policy

Minimal password rules: 8 character minimum, 128 character maximum.

Security is enforced through:
• argon2id hashing (OWASP params: memory=47104 KiB, iterations=1, parallelism=1)
• API-owned login rate limiting
• step-up authentication for sensitive operations

UI may provide password strength guidance.

---

### MFA direction

MFA will be implemented via passkeys, not OTP.

Enterprise plans may optionally support TOTP in the future.

---

### API owns auth

The API is the single authority for:
• session management (opaque, DB-backed tokens)
• authentication (password verification, credential validation)
• step-up auth (15-min window, tracked on session)
• rate limiting (via `RateLimiter` interface)
• enumeration safety (generic errors on login/password-reset)
• email delivery (via `EmailSender` interface)
• identity model (users, accounts, memberships, email identities)
• password hashing (argon2id)
• token lifecycle (email verification, password reset)
• database constraints

Clients (BFF, native, CLI) are responsible only for their transport-specific concerns:
• BFF: cookie-to-Bearer translation, browser UX
• Native: keychain storage
• CLI: config file storage

---

## Session Model

Sessions are API-owned. The API issues opaque, DB-backed session tokens presented as `Authorization: Bearer <token>`.

Characteristics:
• opaque token: 32 random bytes, base64url encoded
• stored as SHA-256 hash (`token_hash`) — raw token never persisted
• 30-day idle timeout (`last_activity_at` updated on each valid request)
• 180-day absolute expiry (`expires_at` set at creation, never extended)
• unlimited concurrent sessions per user
• each session tracks an active account (`account_id`)

Session is valid when: `revoked_at IS NULL AND expires_at > now() AND last_activity_at > now() - interval '30 days'`

---

## Revocation Strategy

Revocation is immediate. Revoking a session sets `revoked_at` on the session row. The next request with that token fails authentication instantly — there is no window of continued access.

Revocation triggers:
• `POST /v1/auth/logout` — revoke current session
• `POST /v1/auth/logout-all` — revoke all sessions for user
• `POST /v1/auth/confirm-password-reset` — revoke all sessions for user
• `POST /v1/users/{userID}/delete` — revoke all sessions for user

---

## Security Policies

### Login rate limiting

API-owned rate limiting via `RateLimiter` interface (in-memory implementation first, swappable to Redis later).

Protect against credential stuffing using:
• IP-based throttling on all public endpoints

Example policy:
• 5–10 attempts per minute per IP

All failures return the same message:
`Invalid email or password`

---

### Enumeration safety

API-owned. Login and password reset endpoints return generic errors regardless of whether the email exists. This prevents account enumeration attacks.

---

### Auto-link OAuth accounts

If OAuth is added in the future, identities are automatically linked only when: 1. provider confirms email is verified 2. email matches a verified email identity 3. no ambiguity exists

Otherwise explicit linking is required.

---

## Step-up Authentication

API-owned. Required for sensitive operations:

• change password
• link/unlink login method
• change primary email
• remove email
• delete account

The session must have a recent `last_step_up_at` (within 15 minutes).

Flow:
1. Client calls `POST /v1/auth/verify-password` with the user's current password
2. API verifies password, updates `last_step_up_at` on the session
3. Client calls the protected endpoint
4. Step-up middleware checks `last_step_up_at` is within 15-min window
5. If expired/missing: returns 403 `STEP_UP_REQUIRED`

---

## Account Deletion

Account deletion is soft-delete first.

```
user deletes account
↓
account enters pending deletion
↓
recovery window (90 days)
↓
automatic purge
```

---

## Identity Deletion

Identities are soft deleted with a purge TTL (~90 days).

---

## Database Model

IDs use UUIDv7.

All timestamps use timestamptz.

---

### Tables

#### users

Represents a human identity.

```sql
CREATE TABLE users (
    id            uuid PRIMARY KEY,
    display_name  text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz,
    purge_after   timestamptz
);
```

---

#### accounts

Container for resources and permissions.

```sql
CREATE TABLE accounts (
    id          uuid PRIMARY KEY,
    name        text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    purge_after timestamptz
);
```

---

#### memberships

Users belong to accounts via memberships.

```sql
CREATE TABLE memberships (
    user_id    uuid NOT NULL REFERENCES users(id),
    account_id uuid NOT NULL REFERENCES accounts(id),
    role       text NOT NULL CHECK (role IN ('owner','admin','member')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz,
    PRIMARY KEY (user_id, account_id)
);
```

`last_used_at` tracks the most recently used account per user, used to default account selection at login.

---

#### auth_identities

Represents login methods.

```sql
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
```

Example providers:
• email
• oauth_google
• oauth_github
• passkey
• atproto

---

#### Primary email constraint

Only one primary email per user.

```sql
CREATE UNIQUE INDEX auth_identities_one_primary_email_per_user
ON auth_identities(user_id)
WHERE provider = 'email'
  AND is_primary = true
  AND deleted_at IS NULL;
```

---

#### Verified email uniqueness

```sql
CREATE UNIQUE INDEX auth_identities_unique_verified_email
ON auth_identities (lower(identifier))
WHERE provider = 'email'
  AND verified_at IS NOT NULL
  AND deleted_at IS NULL;
```

---

#### sessions

API-owned session storage. Tokens presented as `Authorization: Bearer <token>`.

```sql
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
```

- `token_hash`: SHA-256 of the raw token (never store raw)
- `last_activity_at`: updated on each valid request (sliding idle timeout)
- `expires_at`: set to `now() + 180 days` at creation, never extended (absolute cap)
- `last_step_up_at`: updated by verify-password endpoint, checked by step-up middleware

Session is valid when: `revoked_at IS NULL AND expires_at > now() AND last_activity_at > now() - interval '30 days'`

---

#### auth_tokens

Used for email verification and password reset.

```sql
CREATE TABLE auth_tokens (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id),

    type        text NOT NULL,
    token_hash  text NOT NULL,

    identity_id uuid REFERENCES auth_identities(id),

    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz
);
```

Token types:
• email_verification (72h TTL)
• password_reset (1h TTL)

Tokens are stored as hashes, never plaintext.

---

## API Endpoint Contracts

All endpoints are under the `/v1/` prefix. The API serves all client types (BFF, native, CLI) via `Authorization: Bearer <token>`.

### Public endpoints (no auth, rate limited)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/auth/register` | Create user + account + membership + identity + session. Returns session token |
| `POST` | `/v1/auth/login` | Verify credentials, create session. Returns session token. Defaults to last-used account |
| `POST` | `/v1/auth/verify-email` | Consume verification token, mark identity verified |
| `POST` | `/v1/auth/resend-verification` | Generate new verification token, send email |
| `POST` | `/v1/auth/request-password-reset` | Generate reset token, send email. Always 204 (enumeration safe) |
| `POST` | `/v1/auth/confirm-password-reset` | Consume reset token, set new password, revoke all sessions |

### Authenticated endpoints (valid Bearer token required)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/users/{userID}` | Get user profile |
| `PATCH` | `/v1/users/{userID}` | Update display name |
| `GET` | `/v1/users/{userID}/emails` | List email identities |
| `POST` | `/v1/users/{userID}/emails` | Add email identity, send verification |
| `GET` | `/v1/users/{userID}/memberships` | List account memberships |
| `POST` | `/v1/auth/logout` | Revoke current session |
| `POST` | `/v1/auth/logout-all` | Revoke all sessions for user |
| `POST` | `/v1/accounts/switch` | Switch active account on session |

### Authenticated + step-up required (15-min window)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/auth/verify-password` | Verify password, update `last_step_up_at` on session |
| `POST` | `/v1/users/{userID}/password/change` | Change password |
| `PUT` | `/v1/users/{userID}/emails/primary` | Set primary email (must be verified) |
| `DELETE` | `/v1/users/{userID}/emails/{emailID}` | Remove email identity |
| `POST` | `/v1/users/{userID}/delete` | Soft-delete user + personal account |

### Design notes

- Middleware enforces `{userID}` path param matches session's user — prevents cross-user access
- Enumeration safety: login and password reset return generic errors regardless of email existence
- Rate limiting on all public endpoints (IP-based)
- `POST /v1/auth/verify-password` is the step-up trigger — clients call it first, then the protected endpoint

---

## Multi-Client Auth Flows

### Web (via BFF)

```
Browser <--cookie--> BFF <--Bearer token--> API <--> Postgres
```

- BFF calls `POST /v1/auth/login`, gets session token
- BFF stores token server-side, sets HttpOnly/Secure/SameSite cookie for browser
- On each request: BFF reads cookie, attaches `Authorization: Bearer <token>`, forwards to API
- BFF handles cookie-specific concerns only

### Native app

```
App <--Bearer token--> API <--> Postgres
```

- App calls `POST /v1/auth/login`, gets session token
- Token stored in OS keychain
- App attaches `Authorization: Bearer <token>` on each request

### CLI

```
CLI <--Bearer token--> API <--> Postgres
```

- CLI calls `POST /v1/auth/login`, gets session token
- Token stored in `~/.config/rss/credentials`
- CLI attaches `Authorization: Bearer <token>` on each request

---

## Future Extensions

Planned but not implemented in v1:

• OAuth login providers
• Passkeys
• Team accounts UI
• Enterprise SSO
• MFA
• Access + refresh token pattern (if token-theft blast radius becomes a concern)
• Real email provider / event-based email delivery

---

## Implementation Notes

### Password hashing

Use argon2id with OWASP params: memory=47104 KiB, iterations=1, parallelism=1, salt=16 bytes, key=32 bytes. PHC format string.

---

### Token storage

Always store `hash(token)`. Never store raw tokens. Use SHA-256 for session tokens and auth tokens.

---

### Token generation

Session tokens and auth tokens: 32 bytes from `crypto/rand`, base64url encoded.

---

### Email normalization

Emails should be:
• lowercased
• trimmed

before storage.

---
