# PR 5c: Middleware — Guard Middlewares (RequireAuth, RequireUserMatch, RequireStepUp)

## Context

This PR adds the authorization guard middlewares that enforce access control. Each guard reads from request context (populated by `Authenticate` in PR 5b) and returns a pass/fail decision. These are applied to specific route groups that need protection.

Builds on PR 5b (context helpers and Authenticate middleware).

See `plans/auth-implementation.md` for the full plan.

## Prerequisites

- PR 5b (context helpers + Authenticate) — guards read user ID, account ID, and session from request context

## Scope

### Add to `internal/server/middleware.go` — Guard middlewares

**`RequireAuth(next http.Handler) http.Handler`**
- Checks request context for user ID
- If missing: return 401 `UNAUTHENTICATED`
- If present: call next

**`RequireUserMatch(next http.Handler) http.Handler`**
- Reads `{userID}` from chi URL params
- Compares to user ID in request context
- If mismatch: return 403 `FORBIDDEN`
- If match: call next

**`RequireStepUp(next http.Handler) http.Handler`**
- Reads session from request context
- Checks `last_step_up_at` is within 15 minutes of now
- If missing or expired: return 403 `STEP_UP_REQUIRED`
- If valid: call next

## Files Changed

- Modify `internal/server/middleware.go`
- Modify `internal/server/middleware_test.go`

## Verification

- `make test` — all tests pass

### RequireAuth tests
- Context has user: passes through
- Context has no user: 401

### RequireUserMatch tests
- Path param matches context user: passes through
- Path param doesn't match: 403
- Invalid UUID in path: 400

### RequireStepUp tests
- `last_step_up_at` within 15 minutes: passes through
- `last_step_up_at` older than 15 minutes: 403 `STEP_UP_REQUIRED`
- `last_step_up_at` is null: 403 `STEP_UP_REQUIRED`
