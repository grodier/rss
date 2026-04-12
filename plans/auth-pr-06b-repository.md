# PR 6b: Repository — Users, Accounts, Memberships

## Context

Builds on PR 6a. Implements the straightforward entity CRUD methods — users, accounts, and memberships. These are the simplest queries in the repository and establish the patterns (parameterized queries, soft-delete filtering, `DBTX` parameter) that the rest of the repository follows.

See `plans/auth-pr-06-repository.md` for the full PR 6 index.

## Prerequisites

- PR 6a (repository scaffold)

## Scope

### Extend `internal/pgsql/auth_repository.go`

**Users (3 methods)**
- `GetUserByID(ctx, db, id)` — SELECT by ID, exclude soft-deleted
- `UpdateUserDisplayName(ctx, db, id, name)` — UPDATE display_name, return updated user
- `SoftDeleteUser(ctx, db, id)` — SET deleted_at = now(), purge_after = now() + 90 days

**Accounts (1 method)**
- `SoftDeleteAccount(ctx, db, id)` — SET deleted_at = now(), purge_after = now() + 90 days

**Memberships (3 methods)**
- `ListMembershipsByUserID(ctx, db, userID)` — SELECT all memberships for user, JOIN accounts for name
- `GetPrimaryMembership(ctx, db, userID)` — membership with most recent `last_used_at` (or owner role as fallback)
- `UpdateMembershipLastUsedAt(ctx, db, userID, accountID)` — UPDATE last_used_at = now()

### Create `internal/pgsql/auth_repository_test.go`

Integration tests against a real Postgres instance. Pattern:
- Use test DB (same as `make db/start`)
- Each test runs in a transaction that's rolled back after — clean isolation
- Helper function to set up test fixtures (create user, account, membership)

**Test coverage:**
- `GetUserByID`: found, not found, soft-deleted excluded
- `UpdateUserDisplayName`: updates and returns user
- `SoftDeleteUser`: sets deleted_at and purge_after
- `SoftDeleteAccount`: sets deleted_at and purge_after
- `ListMembershipsByUserID`: returns all memberships for user
- `GetPrimaryMembership`: returns most recently used
- `UpdateMembershipLastUsedAt`: updates timestamp

## Files Changed

- Modify `internal/pgsql/auth_repository.go`
- Create `internal/pgsql/auth_repository_test.go`

## Verification

- `make db/start` (if not already running)
- `make test` — all tests pass including new integration tests
- Edge cases verified:
  - Soft-deleted users excluded from `GetUserByID`
  - `GetPrimaryMembership` falls back to owner role when no `last_used_at` is set
