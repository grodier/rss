# PR 6: Auth Repository (pgsql)

## Context

This PR implements the `auth.Repository` interface against Postgres. It's the persistence layer for all auth operations — users, accounts, memberships, identities, tokens, and sessions. All methods use parameterized queries (no SQL injection). The repository accepts a `DBTX` interface so methods work with both `*sql.DB` and `*sql.Tx`, enabling transaction-per-test rollback in integration tests.

See `plans/auth-implementation.md` for the full repository interface definition.

## Prerequisites

- PR 3 (migrations) — tables must exist
- PR 4 (domain types) — repository implements the `auth.Repository` interface and returns domain structs

## Scope

### Modify `internal/pgsql/pgsql.go`

- Add `SqlDB() *sql.DB` accessor method on the DB struct, so the repository can access the underlying `*sql.DB` for transactions

### Create `internal/pgsql/auth_repository.go`

Implement all `auth.Repository` methods:

**Registration (transactional)**
- `CreateRegistration(ctx, params)` — INSERT user + account + membership + identity + session in a single transaction. Return all created entities.

**Users**
- `GetUserByID(ctx, id)` — SELECT by ID, exclude soft-deleted
- `UpdateUserDisplayName(ctx, id, name)` — UPDATE display_name
- `SoftDeleteUser(ctx, id)` — SET deleted_at, purge_after (now + 90 days)

**Accounts**
- `SoftDeleteAccount(ctx, id)` — SET deleted_at, purge_after (now + 90 days)

**Memberships**
- `ListMembershipsByUserID(ctx, userID)` — SELECT all memberships for user, JOIN accounts for name
- `GetPrimaryMembership(ctx, userID)` — membership with most recent `last_used_at` (or owner role as fallback)
- `UpdateMembershipLastUsedAt(ctx, userID, accountID)` — UPDATE last_used_at = now()

**Email Identities**
- `GetEmailIdentityByEmail(ctx, email)` — SELECT by lower(identifier) WHERE provider='email' AND deleted_at IS NULL
- `GetEmailIdentityByID(ctx, id)` — SELECT by ID, exclude soft-deleted
- `ListEmailIdentitiesByUserID(ctx, userID)` — SELECT all for user, exclude soft-deleted
- `CreateEmailIdentity(ctx, params)` — INSERT new identity
- `SetEmailIdentityVerified(ctx, id)` — UPDATE verified_at = now()
- `SetPrimaryEmail(ctx, userID, emailID)` — Unset current primary, set new primary (transaction)
- `SoftDeleteEmailIdentity(ctx, id)` — SET deleted_at, purge_after
- `CountActiveEmailIdentities(ctx, userID)` — COUNT where deleted_at IS NULL

**Password**
- `UpdatePasswordHash(ctx, identityID, hash)` — UPDATE password_hash

**Auth Tokens**
- `CreateAuthToken(ctx, params)` — INSERT token
- `ConsumeAuthToken(ctx, tokenHash, type)` — SELECT unused + unexpired token, SET used_at = now(). Return token or ErrTokenInvalidOrExpired.

**Sessions**
- `CreateSession(ctx, params)` — INSERT session with token_hash, expires_at = now() + 180 days
- `GetSessionByTokenHash(ctx, hash)` — SELECT session WHERE token_hash = hash AND revoked_at IS NULL
- `UpdateSessionActivity(ctx, id)` — UPDATE last_activity_at = now()
- `UpdateSessionStepUp(ctx, id)` — UPDATE last_step_up_at = now()
- `UpdateSessionAccount(ctx, id, accountID)` — UPDATE account_id
- `RevokeSession(ctx, id)` — UPDATE revoked_at = now()
- `RevokeAllUserSessions(ctx, userID)` — UPDATE revoked_at = now() WHERE user_id AND revoked_at IS NULL

### Create `internal/pgsql/auth_repository_test.go`

Integration tests against a real Postgres instance. Pattern:
- Use test DB (same as `make db/start`)
- Each test runs in a transaction that's rolled back after — clean isolation
- Helper function to set up test fixtures (create user, identity, etc.)

**Test coverage:**
- `CreateRegistration`: creates all entities, returns correct data, second registration with same verified email fails
- `GetUserByID`: found, not found, soft-deleted excluded
- `SoftDeleteUser`: sets deleted_at and purge_after
- `ListMembershipsByUserID`: returns all memberships
- `GetPrimaryMembership`: returns most recently used
- `UpdateMembershipLastUsedAt`: updates timestamp
- Email identity CRUD: create, get by email (case insensitive), get by ID, list, verify, set primary, soft delete
- `CountActiveEmailIdentities`: correct count, excludes deleted
- Verified email uniqueness constraint: duplicate verified email raises error
- Primary email constraint: only one primary per user
- Auth tokens: create, consume (marks used), consume expired (fails), consume already-used (fails)
- Sessions: create, get by token hash, update activity, update step-up, update account, revoke, revoke all

## Files Changed

- Modify `internal/pgsql/pgsql.go`
- Create `internal/pgsql/auth_repository.go`
- Create `internal/pgsql/auth_repository_test.go`

## Verification

- `make db/start` (if not already running)
- `make test` — all tests pass including integration tests
- Edge cases verified:
  - Duplicate verified email: returns appropriate error
  - Expired token consumption: fails
  - Already-used token: fails
  - Soft-deleted records: excluded from queries
  - Session validity: revoked, expired, idle-timed-out sessions handled correctly
  - Case-insensitive email lookup: `User@Example.com` matches `user@example.com`
