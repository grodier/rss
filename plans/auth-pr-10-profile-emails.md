# PR 10: User Profile + Email Management (Service + Handlers)

## Context

This PR adds user profile endpoints (get, update display name) and email management (list, add, set primary, remove). Email management includes step-up requirements for destructive operations (set primary, remove).

All endpoints are scoped to the authenticated user via `RequireAuth` + `RequireUserMatch` middleware — a user can only access their own profile and emails.

See `plans/auth-implementation.md` for the full plan.

## Prerequisites

- PR 5 (middleware) — RequireAuth, RequireUserMatch, RequireStepUp middleware
- PR 7 (register + login) — users and sessions exist
- PR 9 (sessions + accounts) — verify-password endpoint exists for step-up

## Scope

### Service implementation — `internal/auth/service.go`

**`GetUser(ctx, userID uuid.UUID) (*User, error)`**
1. Call `repo.GetUserByID(ctx, userID)`
2. If not found: return `ErrUserNotFound`

**`UpdateDisplayName(ctx, userID uuid.UUID, displayName *string) (*User, error)`**
1. Call `repo.UpdateUserDisplayName(ctx, userID, displayName)`
2. Return updated user

**`ListEmails(ctx, userID uuid.UUID) ([]EmailIdentity, error)`**
1. Call `repo.ListEmailIdentitiesByUserID(ctx, userID)`

**`AddEmail(ctx, userID uuid.UUID, email string) (*EmailIdentity, error)`**
1. Validate email format, normalize
2. Check if email already exists (verified) via `repo.GetEmailIdentityByEmail()`
3. If exists and verified by another user: return `ErrEmailUnavailable`
4. Create identity via `repo.CreateEmailIdentity()`
5. Generate verification token, store hash via `repo.CreateAuthToken()` (72h TTL)
6. Send verification email via `emailSender.SendVerificationEmail()`
7. Return new identity

**`SetPrimaryEmail(ctx, userID uuid.UUID, emailID uuid.UUID) error`**
(Step-up required — enforced by middleware, not by service)
1. Get identity via `repo.GetEmailIdentityByID(ctx, emailID)`
2. Verify identity belongs to user
3. If not verified: return `ErrEmailNotVerified`
4. Call `repo.SetPrimaryEmail(ctx, userID, emailID)` — unsets old primary, sets new

**`RemoveEmail(ctx, userID uuid.UUID, emailID uuid.UUID) error`**
(Step-up required — enforced by middleware)
1. Get identity via `repo.GetEmailIdentityByID(ctx, emailID)`
2. Verify identity belongs to user
3. If identity is primary: return `ErrCannotRemovePrimaryEmail`
4. Count active identities via `repo.CountActiveEmailIdentities(ctx, userID)`
5. If count <= 1: return `ErrCannotRemoveLastEmail`
6. Call `repo.SoftDeleteEmailIdentity(ctx, emailID)`

### Handlers — `internal/server/user_handlers.go`

**`getUserHandler(w, r)`**
- Get userID from URL param (already validated by RequireUserMatch)
- Call `authService.GetUser()`
- Return 200 with user

**`updateUserHandler(w, r)`**
- Parse: `{display_name}`
- Call `authService.UpdateDisplayName()`
- Return 200 with updated user

**`listEmailsHandler(w, r)`**
- Call `authService.ListEmails()`
- Return 200 with `{emails: [...]}`

**`addEmailHandler(w, r)`**
- Parse: `{email}`
- Call `authService.AddEmail()`
- Map: `ErrEmailUnavailable` -> 409, validation errors -> 400
- Return 201 with new email identity

**`setPrimaryEmailHandler(w, r)`**
- Parse: `{email_id}`
- Call `authService.SetPrimaryEmail()`
- Map: `ErrEmailNotVerified` -> 409, `ErrIdentityNotFound` -> 404
- Return 204

**`removeEmailHandler(w, r)`**
- Get emailID from URL param
- Call `authService.RemoveEmail()`
- Map: `ErrCannotRemovePrimaryEmail` -> 409, `ErrCannotRemoveLastEmail` -> 409, `ErrIdentityNotFound` -> 404
- Return 204

### Router additions

```go
r.Route("/v1/users/{userID}", func(r chi.Router) {
    r.Use(s.requireAuth)
    r.Use(s.requireUserMatch)

    r.Get("/", s.getUserHandler)
    r.Patch("/", s.updateUserHandler)
    r.Get("/emails", s.listEmailsHandler)
    r.Post("/emails", s.addEmailHandler)

    // Step-up required
    r.Group(func(r chi.Router) {
        r.Use(s.requireStepUp)
        r.Put("/emails/primary", s.setPrimaryEmailHandler)
        r.Delete("/emails/{emailID}", s.removeEmailHandler)
    })
})
```

### Tests

**Service unit tests:**
- GetUser: found, not found
- UpdateDisplayName: updates and returns
- ListEmails: returns all non-deleted
- AddEmail: success (sends verification), duplicate verified email (error), invalid format
- SetPrimaryEmail: success, not verified (error), not owned by user (error)
- RemoveEmail: success, is primary (error), is last email (error)

**Handler tests:**
- All endpoints: 401 without token, 403 with wrong userID in path
- GetUser: 200
- UpdateUser: 200
- ListEmails: 200
- AddEmail: 201, 409 duplicate, 400 invalid
- SetPrimaryEmail: 204, 403 STEP_UP_REQUIRED (no recent verify-password), 409 not verified
- RemoveEmail: 204, 403 STEP_UP_REQUIRED, 409 last email, 409 primary email

## Files Changed

- Modify `internal/auth/service.go` — add 6 methods
- Create `internal/auth/service_user_test.go`
- Create `internal/auth/service_email_test.go`
- Create `internal/server/user_handlers.go`
- Create `internal/server/user_handlers_test.go`
- Modify `internal/server/router.go` — add user routes

## Verification

- `make test` — all tests pass
- Step-up enforcement:
  1. Register -> get token
  2. `PUT /v1/users/{id}/emails/primary` without step-up -> 403 `STEP_UP_REQUIRED`
  3. `POST /v1/auth/verify-password` -> 204
  4. `PUT /v1/users/{id}/emails/primary` within 15 min -> proceeds normally
- User ID mismatch: `GET /v1/users/{otherUserID}` -> 403
- Add email triggers EmailSender (visible in logs)
- Cannot remove primary email
- Cannot remove last email
