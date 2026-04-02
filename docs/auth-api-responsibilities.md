---
Auth Architecture Diagrams

This document provides visual diagrams for the auth/identity architecture, including entity relationships, flow boundaries, and token/session lifecycles.
---

## 1. System Boundary Diagram

```
+---------+  cookie   +---------+  Bearer token  +---------+  SQL  +-----------+
| Browser | <-------> |   BFF   | <------------> |   API   | <---> | Postgres  |
+---------+           +---------+                +---------+       +-----------+
     |                     |                          |
     | (UX, forms,        | (cookie-to-Bearer        | (sessions, authentication,
     |  redirects)        |  translation for web,    |  step-up, rate limiting,
     |                    |  browser UX)             |  enumeration safety,
     |                    |                          |  identity model, password
     |                    |                          |  hashing, token lifecycle,
     |                    |                          |  email delivery, constraints)
```

### Ownership

• **Browser**: UI only
• **BFF owns**: cookie-to-Bearer translation for web, browser UX
• **API owns**: sessions, authentication, step-up auth, rate limiting, enumeration safety, identity model, password hashing/verification, token lifecycle, email delivery (via `EmailSender` interface), database constraints

---

## 2. Domain Model ERD (Conceptual)

```
             +------------------+
             |      users       |
             |------------------|
             | id (uuidv7)      |
             | display_name     |
             | created_at       |
             | deleted_at       |
             | purge_after      |
             +------------------+
                      |
                      | 1..N
                      |
             +------------------+          +------------------+
             |   memberships    |          |     accounts      |
             |------------------|          |------------------|
             | user_id (FK)     |------->  | id (uuidv7)       |
             | account_id (FK)  |          | name              |
             | role             |          | created_at        |
             | created_at       |          | deleted_at        |
             | last_used_at     |          | purge_after       |
             +------------------+          +------------------+
                      |
                      | 1..N
                      |
             +------------------+
             | auth_identities  |
             |------------------|
             | id (uuidv7)      |
             | user_id (FK)     |
             | provider         |
             | identifier       |
             | password_hash?   |
             | verified_at?     |
             | is_primary       |
             | created_at       |
             | deleted_at       |
             | purge_after      |
             +------------------+

             +------------------+
             |    auth_tokens   |
             |------------------|
             | id (uuidv7)      |
             | user_id (FK)     |
             | identity_id? (FK)|
             | type             |
             | token_hash       |
             | created_at       |
             | expires_at       |
             | used_at?         |
             +------------------+

             +------------------+
             |     sessions     |   (owned by API)
             |------------------|
             | id (uuidv7)      |
             | user_id (FK)     |
             | account_id (FK)  |
             | token_hash       |
             | created_at       |
             | last_activity_at |
             | expires_at       |
             | ip_address?      |
             | user_agent?      |
             | last_step_up_at? |
             | revoked_at?      |
             +------------------+
```

Key rules:
• user_id is canonical identity.
• User can have multiple identities.
• Multiple email identities allowed; exactly one verified primary.
• Verified emails globally unique (case-insensitive).
• Accounts enable future team/workspace features.
• Sessions are API-owned, presented as `Authorization: Bearer <token>`.

---

## 3. Authentication Responsibilities Diagram

**API responsibilities:**

- Create/revoke sessions (opaque, DB-backed tokens)
- Authenticate requests via Bearer token
- Rate limit public endpoints (via `RateLimiter` interface)
- Track step-up auth timestamp per session (`last_step_up_at`)
- Send emails via `EmailSender` interface (verification/reset)
- Create users/accounts/memberships transactionally
- Hash + verify passwords (argon2id)
- Create + consume auth_tokens
- Maintain auth_identities state
- Enforce DB constraints (verified email uniqueness, etc.)
- Enforce enumeration safety (generic errors on login/password-reset)
- Soft delete + TTL purge scheduling

**BFF responsibilities:**

- Translate browser cookies to `Authorization: Bearer <token>` headers
- Store session token server-side, set HttpOnly/Secure/SameSite cookie
- Return browser-friendly responses (redirects, HTML forms)

---

## 4. Register Flow Diagram (Email + Password)

```
Browser              BFF                       API                          Postgres        Email
  |  POST /auth/register  |                       |                              |              |
  |----------------------->|                       |                              |              |
  |                        | POST /v1/auth/register|                              |              |
  |                        |---------------------->| validate + normalize         |              |
  |                        |                       | create user/account/ident    |              |
  |                        |                       |----------------------------->| INSERTs (txn)|
  |                        |                       |<----------------------------| ok            |
  |                        |                       | create session              |              |
  |                        |                       |----------------------------->| INSERT session|
  |                        |                       |<----------------------------| ok            |
  |                        |                       | create verification token   |              |
  |                        |                       |----------------------------->| INSERT token  |
  |                        |                       |<----------------------------| ok            |
  |                        |                       | send verification email     |              |
  |                        |                       |--------------------------------------------->|
  |                        |<----------------------| 200 OK {user, account, token}|              |
  |                        | set cookie            |                              |              |
  |<-----------------------| 200 OK {user, account}|                              |              |
```

Notes:
• Grace period: user can use app immediately.
• Email verification is required for sensitive actions and for primary email status.
• API returns session token; BFF stores it and sets a cookie for the browser.
• Registration returns the session token so the user is immediately logged in.

---

## 5. Login Flow Diagram (Email + Password)

```
Browser              BFF                       API                          Postgres
  |  POST /auth/login     |                       |                              |
  |---------------------->|                       |                              |
  |                       | POST /v1/auth/login   |                              |
  |                       |---------------------->| rate limit check             |
  |                       |                       | lookup identity by email     |
  |                       |                       |---------------------------->| SELECT identity
  |                       |                       |                             | compare hash
  |                       |                       |                             | lookup memberships
  |                       |                       |<----------------------------| user + accounts
  |                       |                       | create session              |
  |                       |                       |---------------------------->| INSERT session
  |                       |                       |<----------------------------| ok
  |                       |<----------------------| 200 OK {user, account, token}
  |                       | set cookie            |
  |<----------------------| 200 OK {user, account}|
```

Security:
• Failures are generic: `INVALID_CREDENTIALS`.
• No account enumeration — same error for nonexistent email, wrong password, deleted user/identity.
• Rate limiting is API-owned (IP-based via `RateLimiter` interface).
• Login defaults to the user's last-used account (`last_used_at` on memberships).

---

## 6. Email Verification Token Lifecycle

Create token:
API: generate raw token, store `token_hash` (never raw)
API: send verification email via `EmailSender` interface with raw token

Consume token:
Browser -> BFF -> API: token
API: `hash(token)` -> find unused/unexpired -> mark used -> set `identity.verified_at`
API: if user has no verified primary email -> set `is_primary=true`

```
                 +------------------+
  raw token ---->|   Email Link     |
                 +------------------+
                         |
                         v
  Browser -> BFF -> API: hashes token -> Postgres lookup -> mark used -> update identity
```

---

## 7. Password Reset Token Lifecycle

Request reset:
Browser -> BFF -> API: email
API: if verified identity exists -> create token (hash stored) -> send email via `EmailSender`
API: always responds 204 (enumeration safe)

Confirm reset:
Browser -> BFF -> API: token + new password
API: validate token unused/unexpired -> mark used -> update `password_hash` -> revoke all sessions

---

## 8. Step-up Auth Diagram

Step-up is API-owned. The API tracks `last_step_up_at` on the session and enforces the 15-min window.

```
User action: "Set primary email"
   |
   v
Client calls POST /v1/auth/verify-password {password}
   |
   v
API verifies password against identity
   |
   +-- wrong -> 401 INVALID_CREDENTIALS
   |
   +-- correct -> update session.last_step_up_at = now()
   |              return 204
   v
Client calls PUT /v1/users/{userID}/emails/primary
   |
   v
API step-up middleware checks session.last_step_up_at
   |
   +-- within 15 min -> proceed
   |
   +-- missing/expired -> 403 STEP_UP_REQUIRED
```

---

## 9. Account Context in Session (Multi-account ready)

`sessions.account_id` is the "active account" for that session.

Switch flow:

```
Client -> POST /v1/accounts/switch {account_id}
API -> verify membership(user_id, account_id)
API -> update session.account_id
API -> update membership.last_used_at
```

At login, the API defaults to the user's most recently used account (`last_used_at` on memberships).

---

## 10. Deletion + Purge Timeline

Soft delete first, then purge after TTL.

T0: user deletes account

- `users.deleted_at = now()`
- `users.purge_after = now() + 90 days`
- `accounts.deleted_at = now()`
- `accounts.purge_after = now() + 90 days`
- revoke all sessions immediately

T0..TTL: recovery window

- login disabled (recommended)
- data hidden from UI

T0+TTL: purge job runs

- hard delete rows + related domain data

Identity deletion behaves similarly:

```
auth_identities.deleted_at = now()
auth_identities.purge_after = now() + 90 days
purge job hard-deletes after TTL
```

---

## 11. Multi-Client Auth Flows

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

## 12. Future Extensions Diagram (No Schema Rewrite)

Because identities are normalized, new login methods are additive:

```
auth_identities:
  provider = oauth_google
  identifier = provider_subject
  verified_at = provider_email_verified_timestamp? (optional)

auth_identities:
  provider = passkey
  identifier = credential_id
  (passkey metadata stored in separate passkey table if needed)

auth_identities:
  provider = atproto
  identifier = did (or handle)
```

---

## Summary

• **API owns**: sessions (opaque, DB-backed tokens), authentication, step-up auth (15-min window), rate limiting, enumeration safety, email delivery, users/accounts/memberships, identities, password verification, token lifecycle, data constraints.
• **BFF owns**: cookie-to-Bearer translation for web, browser UX.
• The model supports future OAuth/passkeys/teams with minimal changes.

---
