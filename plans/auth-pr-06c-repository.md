# PR 6c: Repository — Email Identities + Password

## Context

Builds on PR 6a. Implements the email identity and password methods — the largest single group in the repository (9 methods). These have the most complex constraints: case-insensitive email lookup, verified email uniqueness, primary email uniqueness within a user, and transactional primary email swaps.

See `plans/auth-pr-06-repository.md` for the full PR 6 index.

## Prerequisites

- PR 6a (repository scaffold)

## Scope

### Extend `internal/pgsql/auth_repository.go`

**Email Identities (8 methods)**
- `GetEmailIdentityByEmail(ctx, db, email)` — SELECT by lower(identifier) WHERE provider='email' AND deleted_at IS NULL
- `GetEmailIdentityByID(ctx, db, id)` — SELECT by ID, exclude soft-deleted
- `ListEmailIdentitiesByUserID(ctx, db, userID)` — SELECT all for user, exclude soft-deleted
- `CreateEmailIdentity(ctx, db, identity)` — INSERT new identity
- `SetEmailIdentityVerified(ctx, db, id)` — UPDATE verified_at = now()
- `SetPrimaryEmail(ctx, db, userID, emailID)` — Unset current primary, set new primary (transaction)
- `SoftDeleteEmailIdentity(ctx, db, id)` — SET deleted_at, purge_after
- `CountActiveEmailIdentities(ctx, db, userID)` — COUNT where deleted_at IS NULL

**Password (1 method)**
- `UpdatePasswordHash(ctx, db, identityID, hash)` — UPDATE password_hash

### Extend `internal/pgsql/auth_repository_test.go`

**Test coverage:**
- Email identity CRUD: create, get by email (case insensitive), get by ID, list by user, verify, set primary, soft delete
- `CountActiveEmailIdentities`: correct count, excludes deleted
- Verified email uniqueness constraint: duplicate verified email raises `ErrEmailUnavailable`
- Primary email constraint: only one primary per user
- `UpdatePasswordHash`: updates hash on identity
- Case-insensitive email lookup: `User@Example.com` matches `user@example.com`

## Files Changed

- Modify `internal/pgsql/auth_repository.go`
- Modify `internal/pgsql/auth_repository_test.go`

## Verification

- `make db/start` (if not already running)
- `make test` — all tests pass
- Edge cases verified:
  - Duplicate verified email: returns `ErrEmailUnavailable`
  - Case-insensitive email lookup works
  - Primary email swap is atomic (unset + set in one operation)
  - Soft-deleted identities excluded from queries and counts
