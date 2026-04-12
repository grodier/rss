# PR 6d: Repository — Auth Tokens + Sessions

## Context

Builds on PR 6a. Implements token lifecycle (create, single-use consume) and session management (create, lookup, activity tracking, step-up, revocation). These are the auth-specific persistence concerns that power login, session validation, and email verification flows.

See `plans/auth-pr-06-repository.md` for the full PR 6 index.

## Prerequisites

- PR 6a (repository scaffold)

## Scope

### Extend `internal/pgsql/auth_repository.go`

**Auth Tokens (2 methods)**
- `CreateAuthToken(ctx, db, token)` — INSERT token
- `ConsumeAuthToken(ctx, db, tokenHash, type)` — SELECT unused + unexpired token, SET used_at = now(). Return token or `ErrTokenInvalidOrExpired`.

**Sessions (7 methods)**
- `CreateSession(ctx, db, session)` — INSERT session with token_hash, expires_at
- `GetSessionByTokenHash(ctx, db, hash)` — SELECT session WHERE token_hash = hash AND revoked_at IS NULL
- `UpdateSessionActivity(ctx, db, id)` — UPDATE last_activity_at = now()
- `UpdateSessionStepUp(ctx, db, id)` — UPDATE last_step_up_at = now()
- `UpdateSessionAccount(ctx, db, id, accountID)` — UPDATE account_id
- `RevokeSession(ctx, db, id)` — UPDATE revoked_at = now()
- `RevokeAllUserSessions(ctx, db, userID)` — UPDATE revoked_at = now() WHERE user_id AND revoked_at IS NULL

### Extend `internal/pgsql/auth_repository_test.go`

**Test coverage:**
- Auth tokens: create, consume (marks used), consume expired (fails), consume already-used (fails)
- Sessions: create, get by token hash, update activity, update step-up, update account, revoke single, revoke all for user
- Revoked session excluded from `GetSessionByTokenHash`

## Files Changed

- Modify `internal/pgsql/auth_repository.go`
- Modify `internal/pgsql/auth_repository_test.go`

## Verification

- `make db/start` (if not already running)
- `make test` — all tests pass
- Edge cases verified:
  - Expired token consumption: returns `ErrTokenInvalidOrExpired`
  - Already-used token: returns `ErrTokenInvalidOrExpired`
  - Revoked sessions excluded from `GetSessionByTokenHash`
  - `RevokeAllUserSessions` only affects active (non-revoked) sessions
