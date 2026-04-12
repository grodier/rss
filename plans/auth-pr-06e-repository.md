# PR 6e: Repository — Registration (Transactional)

## Context

Builds on PR 6a. Implements the single `CreateRegistration` method — the most complex method in the repository. It INSERTs across all tables (users, accounts, memberships, auth_identities, sessions) in a single transaction, ensuring atomicity for the registration flow.

Best reviewed after PRs 6b–6d, since the reviewer will already be familiar with the query patterns and table shapes.

See `plans/auth-pr-06-repository.md` for the full PR 6 index.

## Prerequisites

- PR 6a (repository scaffold)

## Scope

### Extend `internal/pgsql/auth_repository.go`

**Registration (1 method, transactional)**
- `CreateRegistration(ctx, params)` — BEGIN transaction, INSERT user + account + membership + identity + session, COMMIT. Return `CreateRegistrationResult` with all created entities. Rollback on any failure.

With this method implemented, enable the compile-time interface check:
```go
var _ auth.Repository = (*AuthRepository)(nil)
```

### Extend `internal/pgsql/auth_repository_test.go`

**Test coverage:**
- `CreateRegistration`: creates all entities, returns correct data
- Second registration with same verified email fails with appropriate error
- Partial failure rolls back all inserts (no orphaned rows)

## Files Changed

- Modify `internal/pgsql/auth_repository.go`
- Modify `internal/pgsql/auth_repository_test.go`

## Verification

- `make db/start` (if not already running)
- `make test` — all tests pass
- `var _ auth.Repository = (*AuthRepository)(nil)` compiles — full interface satisfied
- Edge cases verified:
  - Duplicate verified email during registration: returns appropriate error, no partial data
  - All five inserts succeed or none do (transaction atomicity)
