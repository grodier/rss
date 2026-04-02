# Auth Endpoints Spec

This document defines HTTP endpoint contracts for the API. All endpoints are under the `/v1/` prefix. The API serves all client types (BFF, native, CLI) via `Authorization: Bearer <token>`.

## Conventions

### Authentication

• Public endpoints require no authentication. They are rate limited by IP.
• Authenticated endpoints require a valid `Authorization: Bearer <token>` header.
• Step-up endpoints additionally require a recent `last_step_up_at` on the session (within 15 minutes).

### Headers

• Requests may include:
  • `Authorization: Bearer <token>` (for authenticated endpoints)
  • `X-Request-Id` (optional; generated if missing)
  • `Content-Type: application/json` for JSON requests

### Standard response envelope

For simplicity, responses are plain JSON objects (no global envelope required). Where useful, fields include:
• `error_code` (stable string)
• `message` (human-readable)
• `details` (optional object)

### Error response format

All non-2xx responses use:

```json
{
  "error_code": "string",
  "message": "string",
  "details": {}
}
```

### Enumeration safety

Endpoints dealing with login / password reset must not reveal whether an email exists. The API enforces this by returning generic errors regardless of email existence.

### Step-up authentication

Some endpoints require "recent re-auth" (step-up).
• Step-up window: 15 minutes
• Trigger: `POST /v1/auth/verify-password`
• If missing/expired: return 403 with `error_code=STEP_UP_REQUIRED`

### Email normalization

Emails must be normalized before storage and comparisons:
• trim whitespace
• lowercase

### Token handling

• Verification/reset tokens are opaque strings (32 bytes, base64url encoded).
• Server stores only SHA-256 hashes.
• Tokens are single-use and expire.

---

## Schemas

### UserSummary

```json
{
  "user_id": "uuid",
  "display_name": "string|null",
  "created_at": "timestamptz"
}
```

### AccountSummary

```json
{
  "account_id": "uuid",
  "name": "string|null",
  "role": "owner|admin|member"
}
```

### EmailIdentity

```json
{
  "email_id": "uuid",
  "email": "string",
  "verified_at": "timestamptz|null",
  "is_primary": "boolean",
  "created_at": "timestamptz"
}
```

### MembershipSummary

```json
{
  "account_id": "uuid",
  "name": "string|null",
  "role": "owner|admin|member",
  "last_used_at": "timestamptz|null"
}
```

---

## Endpoint Summary

### Public endpoints (no auth, rate limited)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/auth/register` | Create user + account + membership + identity + session |
| `POST` | `/v1/auth/login` | Verify credentials, create session |
| `POST` | `/v1/auth/verify-email` | Consume verification token |
| `POST` | `/v1/auth/resend-verification` | Resend verification email |
| `POST` | `/v1/auth/request-password-reset` | Request password reset email |
| `POST` | `/v1/auth/confirm-password-reset` | Consume reset token, set new password |

### Authenticated endpoints (valid Bearer token required)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/users/{userID}` | Get user profile |
| `PATCH` | `/v1/users/{userID}` | Update display name |
| `GET` | `/v1/users/{userID}/emails` | List email identities |
| `POST` | `/v1/users/{userID}/emails` | Add email identity |
| `GET` | `/v1/users/{userID}/memberships` | List account memberships |
| `POST` | `/v1/auth/logout` | Revoke current session |
| `POST` | `/v1/auth/logout-all` | Revoke all sessions for user |
| `POST` | `/v1/accounts/switch` | Switch active account on session |

### Authenticated + step-up required (15-min window)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/auth/verify-password` | Verify password (step-up trigger) |
| `POST` | `/v1/users/{userID}/password/change` | Change password |
| `PUT` | `/v1/users/{userID}/emails/primary` | Set primary email |
| `DELETE` | `/v1/users/{userID}/emails/{emailID}` | Remove email identity |
| `POST` | `/v1/users/{userID}/delete` | Soft-delete user + personal account |

---

## Auth: Registration & Login

### POST /v1/auth/register

Create a user, personal account, membership, email identity, send verification email, and create a session.

**Auth:** Public
**Rate limit:** Yes (IP-based)

**Request**

```json
{
  "email": "string",
  "password": "string",
  "display_name": "string|null"
}
```

**Response 200**

Returns session token. Clients store the token appropriately (BFF: server-side + cookie, native: keychain, CLI: config file).

```json
{
  "session_token": "string",
  "user": {
    "user_id": "uuid",
    "display_name": "string|null",
    "created_at": "timestamptz"
  },
  "account": {
    "account_id": "uuid",
    "name": "string|null",
    "role": "owner"
  },
  "email": {
    "email_id": "uuid",
    "email": "string",
    "verified_at": null,
    "is_primary": false
  }
}
```

**Errors**
• 400 `INVALID_INPUT` (invalid email format, password too short/long, etc.)
• 409 `EMAIL_UNAVAILABLE` (email already used as a verified email identity)
• 429 `RATE_LIMITED`

---

### POST /v1/auth/login

Login with email/password. Creates a new session. Defaults to the user's last-used account.

**Auth:** Public
**Rate limit:** Yes (IP-based)
**Enumeration safe:** Yes — same error for nonexistent email, wrong password, deleted user/identity

**Request**

```json
{
  "email": "string",
  "password": "string"
}
```

**Response 200**

```json
{
  "session_token": "string",
  "user": {
    "user_id": "uuid",
    "display_name": "string|null",
    "created_at": "timestamptz"
  },
  "account": {
    "account_id": "uuid",
    "name": "string|null",
    "role": "owner|admin|member"
  }
}
```

**Errors**
• 401 `INVALID_CREDENTIALS` — returned for: nonexistent email, wrong password, deleted user, deleted identity. Message: "Invalid email or password."
• 429 `RATE_LIMITED`

---

### POST /v1/auth/logout

Revoke the current session.

**Auth:** Bearer

**Response 204**

No body.

**Errors**
• 401 `UNAUTHENTICATED` (if no valid session)

---

### POST /v1/auth/logout-all

Revoke all sessions for the current user.

**Auth:** Bearer

**Response 204**

No body.

**Errors**
• 401 `UNAUTHENTICATED`

---

## Auth: Email Verification

### POST /v1/auth/verify-email

Consume an email verification token.

**Auth:** Public (token-based; no session required)

**Request**

```json
{
  "token": "string"
}
```

**Response 200**

```json
{
  "status": "verified"
}
```

**Errors**
• 400 `TOKEN_INVALID_OR_EXPIRED`

---

### POST /v1/auth/resend-verification

Resend email verification. Must not reveal whether email exists.

**Auth:** Public
**Enumeration safe:** Yes
**Rate limit:** Yes (IP-based)

**Request**

```json
{
  "email": "string"
}
```

**Response 204**

No body.

**Errors**
• 400 `INVALID_INPUT` (invalid email format)
• 429 `RATE_LIMITED`

---

## Auth: Password Reset

### POST /v1/auth/request-password-reset

Request a password reset email. Always returns 204 regardless of whether email exists.

**Auth:** Public
**Enumeration safe:** Yes
**Rate limit:** Yes (IP-based)

**Request**

```json
{
  "email": "string"
}
```

**Response 204**

No body.

**Errors**
• 400 `INVALID_INPUT` (invalid email format)
• 429 `RATE_LIMITED`

---

### POST /v1/auth/confirm-password-reset

Consume reset token, set new password, and revoke all active sessions.

**Auth:** Public

**Request**

```json
{
  "token": "string",
  "password": "string"
}
```

**Response 204**

No body.

**Errors**
• 400 `TOKEN_INVALID_OR_EXPIRED`
• 400 `INVALID_INPUT` (password too short/long)

---

## Auth: Step-up

### POST /v1/auth/verify-password

Verify the user's current password and update `last_step_up_at` on the session. This is the step-up trigger — clients call this before calling any step-up-protected endpoint.

**Auth:** Bearer

**Request**

```json
{
  "password": "string"
}
```

**Response 204**

No body.

**Errors**
• 401 `UNAUTHENTICATED`
• 401 `INVALID_CREDENTIALS`

**Notes**
• On success: sets `sessions.last_step_up_at = now()`.

---

## User: Profile

### GET /v1/users/{userID}

Get user profile.

**Auth:** Bearer
**Middleware:** `{userID}` must match session user

**Response 200**

```json
{
  "user": {
    "user_id": "uuid",
    "display_name": "string|null",
    "created_at": "timestamptz"
  },
  "account": {
    "account_id": "uuid",
    "name": "string|null",
    "role": "owner|admin|member"
  }
}
```

**Errors**
• 401 `UNAUTHENTICATED`
• 403 `FORBIDDEN` (userID mismatch)

---

### PATCH /v1/users/{userID}

Update display name.

**Auth:** Bearer
**Middleware:** `{userID}` must match session user

**Request**

```json
{
  "display_name": "string|null"
}
```

**Response 200**

```json
{
  "user": {
    "user_id": "uuid",
    "display_name": "string|null",
    "created_at": "timestamptz"
  }
}
```

**Errors**
• 401 `UNAUTHENTICATED`
• 403 `FORBIDDEN`
• 400 `INVALID_INPUT`

---

## User: Email Management

### GET /v1/users/{userID}/emails

List email identities for the user.

**Auth:** Bearer
**Middleware:** `{userID}` must match session user

**Response 200**

```json
{
  "emails": [
    {
      "email_id": "uuid",
      "email": "string",
      "verified_at": "timestamptz|null",
      "is_primary": "boolean",
      "created_at": "timestamptz"
    }
  ]
}
```

**Errors**
• 401 `UNAUTHENTICATED`
• 403 `FORBIDDEN`

---

### POST /v1/users/{userID}/emails

Add a new email identity and send verification email.

**Auth:** Bearer
**Middleware:** `{userID}` must match session user

**Request**

```json
{
  "email": "string"
}
```

**Response 200**

```json
{
  "email": {
    "email_id": "uuid",
    "email": "string",
    "verified_at": null,
    "is_primary": false,
    "created_at": "timestamptz"
  }
}
```

**Errors**
• 401 `UNAUTHENTICATED`
• 403 `FORBIDDEN`
• 400 `INVALID_INPUT`
• 409 `EMAIL_UNAVAILABLE` (email is already a verified email identity for another user)
• 429 `RATE_LIMITED`

---

### PUT /v1/users/{userID}/emails/primary

Set primary email. Target must be verified.

**Auth:** Bearer
**Middleware:** `{userID}` must match session user
**Step-up:** Required

**Request**

```json
{
  "email_id": "uuid"
}
```

**Response 204**

No body.

**Errors**
• 401 `UNAUTHENTICATED`
• 403 `FORBIDDEN`
• 403 `STEP_UP_REQUIRED`
• 404 `NOT_FOUND` (email_id not owned by user / doesn't exist)
• 409 `EMAIL_NOT_VERIFIED` (attempt to set unverified email as primary)

---

### DELETE /v1/users/{userID}/emails/{emailID}

Remove an email identity. Soft delete + TTL purge.

**Auth:** Bearer
**Middleware:** `{userID}` must match session user
**Step-up:** Required

**Response 204**

No body.

**Errors**
• 401 `UNAUTHENTICATED`
• 403 `FORBIDDEN`
• 403 `STEP_UP_REQUIRED`
• 404 `NOT_FOUND`
• 409 `CANNOT_REMOVE_LAST_EMAIL`
• 409 `CANNOT_REMOVE_PRIMARY_EMAIL`

---

## User: Password Management

### POST /v1/users/{userID}/password/change

Change password.

**Auth:** Bearer
**Middleware:** `{userID}` must match session user
**Step-up:** Required

**Request**

```json
{
  "current_password": "string",
  "new_password": "string"
}
```

**Response 204**

No body.

**Errors**
• 401 `UNAUTHENTICATED`
• 403 `FORBIDDEN`
• 403 `STEP_UP_REQUIRED`
• 401 `INVALID_CREDENTIALS` (current_password wrong)
• 400 `INVALID_INPUT` (new password too short/long)

**Notes**
• Consider revoking other sessions on password change (optional but recommended).

---

## User: Memberships

### GET /v1/users/{userID}/memberships

List account memberships for the user.

**Auth:** Bearer
**Middleware:** `{userID}` must match session user

**Response 200**

```json
{
  "memberships": [
    {
      "account_id": "uuid",
      "name": "string|null",
      "role": "owner|admin|member",
      "last_used_at": "timestamptz|null"
    }
  ]
}
```

**Errors**
• 401 `UNAUTHENTICATED`
• 403 `FORBIDDEN`

---

## Account: Switch

### POST /v1/accounts/switch

Switch the active account on the current session.

**Auth:** Bearer

**Request**

```json
{
  "account_id": "uuid"
}
```

**Response 204**

No body.

**Errors**
• 401 `UNAUTHENTICATED`
• 403 `NOT_A_MEMBER` (user not a member of account)

---

## Account: Deletion

### POST /v1/users/{userID}/delete

Soft delete user + personal account, revoke all sessions, start purge timer.

**Auth:** Bearer
**Middleware:** `{userID}` must match session user
**Step-up:** Required

**Response 204**

No body.

**Errors**
• 401 `UNAUTHENTICATED`
• 403 `FORBIDDEN`
• 403 `STEP_UP_REQUIRED`

---

## Future Endpoints (Placeholder)

These are intentionally not implemented in v1, but reserved for vNext:

**OAuth**
• `GET /v1/auth/oauth/{provider}/start`
• `GET /v1/auth/oauth/{provider}/callback`
• `POST /v1/users/{userID}/identities/link/{provider}` (explicit linking, step-up required)

**Passkeys**
• `POST /v1/auth/passkey/register/options`
• `POST /v1/auth/passkey/register/verify`
• `POST /v1/auth/passkey/login/options`
• `POST /v1/auth/passkey/login/verify`
• `DELETE /v1/users/{userID}/identities/{identityID}` (unlink passkey, step-up required)

---
