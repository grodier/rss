# PR 6a: Auth Repository Scaffold

## Context

This PR sets up the `AuthRepository` struct and the `SqlDB()` accessor needed for transaction support. It's pure infrastructure — no query methods yet.

See `plans/auth-pr-06-repository.md` for the full PR 6 index.

## Prerequisites

- PR 3 (migrations) — tables must exist
- PR 4 (domain types) — `auth.Repository` interface and `auth.DBTX` are defined

## Scope

### Modify `internal/pgsql/pgsql.go`

- Add `SqlDB() *sql.DB` accessor method on the `DB` struct so callers (including the repository) can access the underlying `*sql.DB` for transactions

### Create `internal/pgsql/auth_repository.go`

- Define `AuthRepository` struct holding a `*sql.DB`
- Constructor: `NewAuthRepository(db *sql.DB) *AuthRepository`
- Compile-time interface check: `var _ auth.Repository = (*AuthRepository)(nil)`
  - This will not compile until all methods are implemented (PR 6b–6e). For now, comment it out or use a build tag — the important thing is that the struct and constructor exist.

## Files Changed

- Modify `internal/pgsql/pgsql.go`
- Create `internal/pgsql/auth_repository.go`

## Verification

- `make test` — all existing tests still pass
- `AuthRepository` struct exists and is exported
- `SqlDB()` returns the underlying `*sql.DB`
