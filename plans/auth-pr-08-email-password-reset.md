# PR 8: Email Verification + Password Reset (Service + Handlers)

## Context

This PR implements the token-based flows: email verification (verify, resend) and password reset (request, confirm). These are all public endpoints — no session required, but rate limited. The API owns email delivery via the `EmailSender` interface and enumeration safety (password reset always returns 204 regardless of whether the email exists).

Token lifecycle: raw token returned to caller or sent via email, SHA-256 hash stored in DB. Tokens are single-use and have TTLs (verification: 72h, reset: 1h).

See `plans/auth-implementation.md` for the full plan.

## Prerequisites

- PR 7 (register + login) — registration creates the initial verification token; this PR implements consuming it

## Scope

### Service implementation — `internal/auth/service.go`

**`VerifyEmailToken(ctx, token string) error`**
1. Hash the raw token
2. Call `repo.ConsumeAuthToken(ctx, hash, "email_verification")` — marks used, returns token record
3. If not found/expired/used: return `ErrTokenInvalidOrExpired`
4. Call `repo.SetEmailIdentityVerified(ctx, token.IdentityID)` — set `verified_at = now()`
5. If user has no verified primary email, auto-promote this to primary via `repo.SetPrimaryEmail()`

**`ResendVerification(ctx, email string) error`**
1. Normalize email
2. Look up identity: `repo.GetEmailIdentityByEmail(ctx, email)`
3. If not found: return nil (enumeration safe — don't reveal whether email exists)
4. If already verified: return nil
5. Generate new verification token
6. Call `repo.CreateAuthToken()` with 72h TTL
7. Call `emailSender.SendVerificationEmail()` with raw token

**`RequestPasswordReset(ctx, email string) error`**
1. Normalize email
2. Look up identity: `repo.GetEmailIdentityByEmail(ctx, email)`
3. If not found: return nil (enumeration safe)
4. If identity not verified: return nil (can't reset password for unverified email)
5. Generate reset token
6. Call `repo.CreateAuthToken()` with 1h TTL
7. Call `emailSender.SendPasswordResetEmail()` with raw token

**`ConfirmPasswordReset(ctx, token, newPassword string) error`**
1. Validate new password (8 min, 128 max)
2. Hash the raw token
3. Call `repo.ConsumeAuthToken(ctx, hash, "password_reset")`
4. If not found/expired/used: return `ErrTokenInvalidOrExpired`
5. Hash new password with `PasswordHasher`
6. Call `repo.UpdatePasswordHash(ctx, token.IdentityID, hash)`
7. Call `repo.RevokeAllUserSessions(ctx, token.UserID)` — security: force re-login everywhere

### Handlers — `internal/server/auth_handlers.go` (additions)

**`verifyEmailHandler(w, r)`**
- Parse: `{token}`
- Call `authService.VerifyEmailToken()`
- Map: `ErrTokenInvalidOrExpired` -> 400 `TOKEN_INVALID_OR_EXPIRED`
- Return 200 `{status: "verified"}`

**`resendVerificationHandler(w, r)`**
- Parse: `{email}`
- Call `authService.ResendVerification()`
- Always return 204 (enumeration safe)

**`requestPasswordResetHandler(w, r)`**
- Parse: `{email}`
- Call `authService.RequestPasswordReset()`
- Always return 204 (enumeration safe)

**`confirmPasswordResetHandler(w, r)`**
- Parse: `{token, password}`
- Call `authService.ConfirmPasswordReset()`
- Map: `ErrTokenInvalidOrExpired` -> 400, `ErrPasswordTooShort/Long` -> 400
- Return 204

### Router additions

All under the rate-limited public group:
```go
r.Post("/auth/verify-email", s.verifyEmailHandler)
r.Post("/auth/resend-verification", s.resendVerificationHandler)
r.Post("/auth/request-password-reset", s.requestPasswordResetHandler)
r.Post("/auth/confirm-password-reset", s.confirmPasswordResetHandler)
```

### Tests

**Service unit tests:**
- VerifyEmailToken: valid token consumed, expired token rejected, already-used token rejected, auto-promote to primary if no primary exists
- ResendVerification: existing unverified email sends token, nonexistent email returns nil, already verified returns nil
- RequestPasswordReset: existing verified email creates token, nonexistent email returns nil, unverified email returns nil
- ConfirmPasswordReset: valid token resets password and revokes all sessions, expired token rejected, password too short rejected

**Handler tests:**
- Each endpoint: success case, error cases, malformed input
- Enumeration safety: resend-verification and request-password-reset return same response regardless of email existence
- Rate limiting applied

## Files Changed

- Modify `internal/auth/service.go` — add 4 methods
- Create `internal/auth/service_verify_email_test.go`
- Create `internal/auth/service_password_reset_test.go`
- Modify `internal/server/auth_handlers.go` — add 4 handlers
- Modify `internal/server/auth_handlers_test.go` — add handler tests
- Modify `internal/server/router.go` — add 4 routes

## Verification

- `make test` — all tests pass
- Token TTLs: verification token at 72h, reset token at 1h
- Used tokens cannot be reused
- Expired tokens rejected
- Enumeration safety: `resend-verification` and `request-password-reset` always return 204
- `confirm-password-reset` revokes all existing sessions
- `EmailSender` called for resend and reset (visible in logs with LoggingEmailSender)
