# PR 11: Password Change + Account Deletion (Service + Handlers)

## Context

This is the final auth PR. It implements password change and account deletion — both require step-up authentication. It also adds the `ListMemberships` endpoint.

Password change verifies the current password before accepting the new one. Account deletion soft-deletes the user and their personal account, sets purge timers (90 days), and revokes all sessions.

See `plans/auth-implementation.md` for the full plan.

## Prerequisites

- PR 5 (middleware) — RequireStepUp middleware
- PR 9 (sessions + accounts) — verify-password endpoint for step-up, session management for revocation
- PR 10 (profile + emails) — user routes structure established

## Scope

### Service implementation — `internal/auth/service.go`

**`ChangePassword(ctx, userID uuid.UUID, currentPassword, newPassword string) error`**
(Step-up required — enforced by middleware)
1. Validate new password (8 min, 128 max)
2. Get user's primary email identity to retrieve password hash
3. Compare current password with `PasswordHasher`
4. If mismatch: return `ErrInvalidCredentials`
5. Hash new password
6. Call `repo.UpdatePasswordHash(ctx, identityID, newHash)`
7. Optionally: revoke all other sessions (recommended for security — force re-login with new password)

**`SoftDeleteAccount(ctx, userID uuid.UUID) error`**
(Step-up required — enforced by middleware)
1. Call `repo.SoftDeleteUser(ctx, userID)` — sets `deleted_at = now()`, `purge_after = now() + 90 days`
2. Get user's personal account (owner membership)
3. Call `repo.SoftDeleteAccount(ctx, accountID)` — sets `deleted_at = now()`, `purge_after = now() + 90 days`
4. Call `repo.RevokeAllUserSessions(ctx, userID)` — force logout everywhere

**`ListMemberships(ctx, userID uuid.UUID) ([]Membership, error)`**
1. Call `repo.ListMembershipsByUserID(ctx, userID)`

### Handlers — additions to `internal/server/user_handlers.go`

**`changePasswordHandler(w, r)`**
- Parse: `{current_password, new_password}`
- Get userID from URL param
- Call `authService.ChangePassword()`
- Map: `ErrInvalidCredentials` -> 401, `ErrPasswordTooShort/Long` -> 400
- Return 204

**`deleteAccountHandler(w, r)`**
- Get userID from URL param
- Call `authService.SoftDeleteAccount()`
- Return 204

**`listMembershipsHandler(w, r)`**
- Get userID from URL param
- Call `authService.ListMemberships()`
- Return 200 with `{memberships: [...]}`

### Router additions

```go
r.Route("/v1/users/{userID}", func(r chi.Router) {
    // ... existing routes from PR 10 ...

    r.Get("/memberships", s.listMembershipsHandler)  // auth required, no step-up

    r.Group(func(r chi.Router) {
        r.Use(s.requireStepUp)
        // ... existing step-up routes from PR 10 ...
        r.Post("/password/change", s.changePasswordHandler)
        r.Post("/delete", s.deleteAccountHandler)
    })
})
```

### Tests

**Service unit tests:**
- ChangePassword: correct current password -> updates hash, wrong current password -> error, new password too short -> error, new password too long -> error
- SoftDeleteAccount: sets deleted_at and purge_after on user and personal account, revokes all sessions
- ListMemberships: returns all memberships

**Handler tests:**
- ChangePassword: 204 success, 401 wrong current password, 400 new password too short, 403 STEP_UP_REQUIRED
- DeleteAccount: 204 success, 403 STEP_UP_REQUIRED
- ListMemberships: 200, 401 without token, 403 wrong userID

## Files Changed

- Modify `internal/auth/service.go` — add 3 methods
- Create `internal/auth/service_password_test.go`
- Create `internal/auth/service_deletion_test.go`
- Modify `internal/server/user_handlers.go` — add 3 handlers
- Modify `internal/server/user_handlers_test.go` — add handler tests
- Modify `internal/server/router.go` — add 3 routes

## Verification

- `make test` — all tests pass
- Full end-to-end flow:
  1. Register -> get token
  2. `POST /v1/auth/verify-password` -> 204 (step up)
  3. `POST /v1/users/{id}/password/change` with correct current + valid new -> 204
  4. Login with old password -> 401
  5. Login with new password -> 200
  6. `POST /v1/auth/verify-password` -> 204 (step up again)
  7. `POST /v1/users/{id}/delete` -> 204
  8. All sessions revoked — token no longer works
  9. Login with deleted user -> 401
- `purge_after` is set to approximately now + 90 days on both user and account
- `GET /v1/users/{id}/memberships` returns membership list
