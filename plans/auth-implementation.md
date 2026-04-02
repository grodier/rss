# Auth Implementation Plan (Revised)

## Context

The RSS API needs authentication. The original plan used an API-key + X-User-ID trust model where the API deferred user authentication to a future BFF. After review, this was identified as a security gap — any holder of the API key could impersonate any user. The API will eventually serve web (via BFF), native apps, and CLI directly, so it must own authentication end-to-end.

This revised plan makes the **API the single source of truth** for authentication, session management, step-up auth, rate limiting, and enumeration safety. Clients (BFF, native, CLI) are responsible only for their channel-specific concerns (cookies, keychain, config storage). The architecture mirrors how GitHub and Linear handle multi-client auth.

---

## Key Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Auth ownership | API owns everything | Multi-client support (web, native, CLI) requires centralized auth |
| Session tokens | Opaque, DB-backed (hashed) | Single service + single DB; immediate revocation; simpler than JWTs |
| Token presentation | `Authorization: Bearer <token>` | HTTP standard (RFC 6750), works across all client types |
| Session TTL | 30-day idle / 180-day absolute sliding | Active users stay logged in; absolute cap prevents immortal tokens |
| API key | Removed | Bearer token replaces it; API key was security theater for non-BFF clients |
| Step-up auth | API-owned, 15-min window | Sessions are long-lived (180 days); step-up protects destructive operations |
| Rate limiting | API-owned, interface-based | In-memory implementation first, swappable to Redis later |
| Enumeration safety | API-owned | Generic errors for login/password-reset regardless of email existence |
| Email delivery | API-owned via `EmailSender` interface | Logging impl now; swap to real provider or event system later |
| Multi-account | Active account on session, switch endpoint | Linear/GitHub model; `last_used_at` on memberships for default at login |
| Registration | Returns session token immediately | Grace period — user can use app before email verification |
| Error format | `{error_code, message, details}` project-wide | Replacing old `{error: msg}` |
| Password policy | 8 char min, 128 char max, argon2id (OWASP params) | |
| Token TTLs | Email verification: 72h, Password reset: 1h | |
| Purge TTL | 90 days for accounts and identities | |
| Architecture | Service + repository interfaces in `internal/auth/` | |
| Testing | Unit tests (mock repos) + integration tests (real Postgres), TDD | |
| Routes | All under `/v1/` prefix | |

### Future enhancements (not in scope)
- Access + refresh token pattern (if token-theft blast radius becomes a concern)
- Real email provider / event-based email delivery
- OAuth / passkeys / ATProto
- Team account creation and management UI
- MFA

---

## Endpoint Contract

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

## Sessions Table

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

Session is valid when: `revoked_at IS NULL AND expires_at > now() AND last_activity_at > now() - interval '30 days'`

- `token_hash`: SHA-256 of the raw token (never store raw)
- `last_activity_at`: updated on each valid request (sliding idle timeout)
- `expires_at`: set to `now() + 180 days` at creation, never extended (absolute cap)
- `last_step_up_at`: updated by verify-password endpoint, checked by step-up middleware

---

## Service Architecture

### Domain types — `internal/auth/models.go`
Structs: `User`, `Account`, `Membership`, `EmailIdentity`, `AuthToken`, `Session`, plus param/result structs for service methods.

### Repository interface — `internal/auth/repository.go`
Persistence boundary. Key methods:
- `CreateRegistration(ctx, params)` — transactional: user + account + membership + identity + session
- `GetUserByID`, `UpdateUserDisplayName`, `SoftDeleteUser`
- `SoftDeleteAccount`
- `ListMembershipsByUserID`, `GetPrimaryMembership`, `UpdateMembershipLastUsedAt`
- `GetEmailIdentityByEmail`, `GetEmailIdentityByID`, `ListEmailIdentitiesByUserID`
- `CreateEmailIdentity`, `SetEmailIdentityVerified`, `SetPrimaryEmail`, `SoftDeleteEmailIdentity`
- `CountActiveEmailIdentities`, `UpdatePasswordHash`
- `CreateAuthToken`, `ConsumeAuthToken`
- `CreateSession`, `GetSessionByTokenHash`, `UpdateSessionActivity`, `UpdateSessionStepUp`, `UpdateSessionAccount`, `RevokeSession`, `RevokeAllUserSessions`

Repository methods accept a `DBTX` interface (`QueryContext`, `ExecContext`, `QueryRowContext`) so both `*sql.DB` and `*sql.Tx` work.

### Service — `internal/auth/service.go`
Concrete struct with methods: `Register`, `Login`, `VerifyEmailToken`, `ResendVerification`, `RequestPasswordReset`, `ConfirmPasswordReset`, `Logout`, `LogoutAll`, `VerifyPassword`, `SwitchAccount`, `GetUser`, `UpdateDisplayName`, `ListEmails`, `AddEmail`, `SetPrimaryEmail`, `RemoveEmail`, `ChangePassword`, `SoftDeleteAccount`, `ListMemberships`.

Depends on: `Repository` interface, `PasswordHasher` interface, `EmailSender` interface.

### Interfaces — `internal/auth/`

```go
// hasher.go
type PasswordHasher interface {
    Hash(password string) (string, error)
    Compare(password, hash string) (bool, error)
}

// email.go
type EmailSender interface {
    SendVerificationEmail(ctx context.Context, to string, token string) error
    SendPasswordResetEmail(ctx context.Context, to string, token string) error
}

// ratelimit.go (or internal/server/ratelimit.go)
type RateLimiter interface {
    Allow(key string, limit int, window time.Duration) (bool, error)
}
```

### Sentinel errors — `internal/auth/errors.go`
`ErrInvalidCredentials`, `ErrEmailUnavailable`, `ErrEmailNotVerified`, `ErrIdentityNotFound`, `ErrTokenInvalidOrExpired`, `ErrUserNotFound`, `ErrCannotRemoveLastEmail`, `ErrCannotRemovePrimaryEmail`, `ErrPasswordTooShort`, `ErrPasswordTooLong`, `ErrSessionNotFound`, `ErrSessionExpired`, `ErrStepUpRequired`, `ErrNotAMember`, plus `ValidationError` struct.

HTTP handlers map these to the appropriate status code + error_code.

### Middleware — `internal/server/middleware.go`
1. **RateLimit** — uses `RateLimiter` interface, IP-based for public endpoints
2. **Authenticate** — parses `Authorization: Bearer <token>`, hashes token, looks up session, validates expiry/revocation, updates `last_activity_at`, stores user/account context in request context
3. **RequireAuth** — verifies user context is present
4. **RequireUserMatch** — verifies `{userID}` path param matches session user
5. **RequireStepUp** — checks `last_step_up_at` within 15-min window, returns 403 `STEP_UP_REQUIRED` if not

### Wiring — `cmd/api/application.go`
```
pgsql.DB.Open()
  -> pgsql.NewAuthRepository(db)
  -> auth.NewLoggingEmailSender(logger)
  -> auth.NewArgon2Hasher()
  -> auth.NewService(repo, hasher, emailSender, logger)
  -> server.NewInMemoryRateLimiter()
  -> server.NewServer(logger, authService, rateLimiter)
```

---

## Libraries

| Purpose | Library |
|---------|---------|
| UUIDv7 | `github.com/google/uuid` (v1.6+) |
| Argon2id | `golang.org/x/crypto/argon2` |
| Token generation | `crypto/rand` (32 bytes, base64url encoded) |
| Token/session storage | `crypto/sha256` hash in DB |
| Email validation | `net/mail.ParseAddress` or simple regex |

Argon2id params (OWASP): memory=47104 KiB, iterations=1, parallelism=1, salt=16 bytes, key=32 bytes. PHC format string.

---

## PR Breakdown

### PR 1: Architecture Doc Updates
See: `plans/auth-pr-01-doc-updates.md`

### PR 2: Structured Error Format
See: `plans/auth-pr-02-error-format.md`

### PR 3: Database Migrations
See: `plans/auth-pr-03-migrations.md`

### PR 4: Domain Types, Errors, Interfaces, and Utilities
See: `plans/auth-pr-04-domain-types.md`

### PR 5: Middleware — Rate Limiting, Auth, Step-up
See: `plans/auth-pr-05-middleware.md`

### PR 6: Auth Repository (pgsql)
See: `plans/auth-pr-06-repository.md`

### PR 7: Registration + Login (Service + Handlers + Wiring)
See: `plans/auth-pr-07-register-login.md`

### PR 8: Email Verification + Password Reset (Service + Handlers)
See: `plans/auth-pr-08-email-password-reset.md`

### PR 9: Session Management + Account Switching (Service + Handlers)
See: `plans/auth-pr-09-sessions-accounts.md`

### PR 10: User Profile + Email Management (Service + Handlers)
See: `plans/auth-pr-10-profile-emails.md`

### PR 11: Password Change + Account Deletion (Service + Handlers)
See: `plans/auth-pr-11-password-deletion.md`

---

## File Tree After All PRs

```
internal/
  auth/
    models.go
    errors.go
    repository.go
    service.go
    hasher.go         (PasswordHasher interface + argon2id impl)
    token.go          (token generation + hashing)
    validation.go     (email + password validation)
    email.go          (EmailSender interface + LoggingEmailSender)
    mock_repository_test.go
    service_*_test.go
    hasher_test.go
    token_test.go
    validation_test.go
    email_test.go
  pgsql/
    pgsql.go          (+ SqlDB accessor)
    auth_repository.go
    auth_repository_test.go
  server/
    server.go
    router.go
    helpers.go
    errors.go
    context.go
    middleware.go      (Authenticate, RequireAuth, RequireUserMatch, RequireStepUp)
    ratelimit.go       (RateLimiter interface + InMemoryRateLimiter)
    utility_handlers.go
    auth_handlers.go
    user_handlers.go
    middleware_test.go
    ratelimit_test.go
    auth_handlers_test.go
    user_handlers_test.go
    (other existing test files)
migrations/
  00001_create_users.sql
  00002_create_accounts.sql
  00003_create_memberships.sql
  00004_create_auth_identities.sql
  00005_create_auth_tokens.sql
  00006_create_sessions.sql
docs/
  auth-architecture.md   (updated)
  auth-api-responsibilities.md (updated)
  auth-flows.md          (updated)
```

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
