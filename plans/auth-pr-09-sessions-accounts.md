# PR 9: Session Management + Account Switching (Service + Handlers)

## Context

This PR adds session lifecycle management (logout, logout-all) and account switching. It also implements the step-up trigger endpoint (`verify-password`) which updates `last_step_up_at` on the current session.

Account switching follows the Linear/GitHub model — the session has an active `account_id`, and the switch endpoint changes it. `membership.last_used_at` is updated to support "default to last-used account at login."

See `plans/auth-implementation.md` for the full plan.

## Prerequisites

- PR 5 (middleware) — RequireAuth middleware protects these endpoints
- PR 7 (register + login) — sessions exist to manage

## Scope

### Service implementation — `internal/auth/service.go`

**`Logout(ctx, sessionID uuid.UUID) error`**
1. Call `repo.RevokeSession(ctx, sessionID)`

**`LogoutAll(ctx, userID uuid.UUID) error`**
1. Call `repo.RevokeAllUserSessions(ctx, userID)`

**`VerifyPassword(ctx, sessionID uuid.UUID, userID uuid.UUID, password string) error`**
1. Get user's email identity (primary) to retrieve password hash
2. Compare password with `PasswordHasher`
3. If mismatch: return `ErrInvalidCredentials`
4. Call `repo.UpdateSessionStepUp(ctx, sessionID)` — sets `last_step_up_at = now()`

**`SwitchAccount(ctx, sessionID uuid.UUID, userID uuid.UUID, accountID uuid.UUID) error`**
1. Call `repo.ListMembershipsByUserID(ctx, userID)`
2. Verify user has a membership for the target `accountID`
3. If not a member: return `ErrNotAMember`
4. Call `repo.UpdateSessionAccount(ctx, sessionID, accountID)`
5. Call `repo.UpdateMembershipLastUsedAt(ctx, userID, accountID)`

### Handlers — `internal/server/auth_handlers.go` (additions)

**`logoutHandler(w, r)`**
- Get session from context
- Call `authService.Logout(ctx, session.ID)`
- Return 204

**`logoutAllHandler(w, r)`**
- Get user ID from context
- Call `authService.LogoutAll(ctx, userID)`
- Return 204

**`verifyPasswordHandler(w, r)`**
- Parse: `{password}`
- Get session and user ID from context
- Call `authService.VerifyPassword(ctx, session.ID, userID, password)`
- Map: `ErrInvalidCredentials` -> 401
- Return 204

**`switchAccountHandler(w, r)`**
- Parse: `{account_id}`
- Get session and user ID from context
- Call `authService.SwitchAccount(ctx, session.ID, userID, accountID)`
- Map: `ErrNotAMember` -> 403 `FORBIDDEN`
- Return 204

### Router additions

```go
// Authenticated routes
r.Group(func(r chi.Router) {
    r.Use(s.requireAuth)
    r.Post("/auth/logout", s.logoutHandler)
    r.Post("/auth/logout-all", s.logoutAllHandler)
    r.Post("/auth/verify-password", s.verifyPasswordHandler)
    r.Post("/accounts/switch", s.switchAccountHandler)
})
```

### Tests

**Service unit tests:**
- Logout: revokes single session, subsequent requests with that token fail
- LogoutAll: revokes all user sessions
- VerifyPassword: correct password updates `last_step_up_at`, wrong password returns `ErrInvalidCredentials`
- SwitchAccount: valid membership switches account and updates `last_used_at`, non-member returns `ErrNotAMember`

**Handler tests:**
- Logout: returns 204, token no longer valid
- LogoutAll: returns 204
- VerifyPassword: correct -> 204, wrong -> 401
- SwitchAccount: valid -> 204, not a member -> 403
- All endpoints: 401 without Bearer token

## Files Changed

- Modify `internal/auth/service.go` — add 4 methods
- Create `internal/auth/service_session_test.go`
- Create `internal/auth/service_account_test.go`
- Modify `internal/server/auth_handlers.go` — add 4 handlers
- Modify `internal/server/auth_handlers_test.go` — add handler tests
- Modify `internal/server/router.go` — add 4 routes

## Verification

- `make test` — all tests pass
- Manual flow:
  1. Register -> get token
  2. `POST /v1/auth/logout` with Bearer token -> 204
  3. Use same token on any endpoint -> 401 (revoked)
  4. Login -> get new token
  5. `POST /v1/auth/verify-password` with correct password -> 204
  6. `POST /v1/auth/verify-password` with wrong password -> 401
  7. `POST /v1/accounts/switch` with personal account ID -> 204
  8. `POST /v1/auth/logout-all` -> 204, all sessions revoked
