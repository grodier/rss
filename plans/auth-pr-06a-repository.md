# PR 6a: Auth Repository Scaffold

## Context

This PR sets up the `AuthRepository` struct and refactors `pgsql.go` to replace the `DB` wrapper struct with a conventional `OpenDB` function that returns `*sql.DB` directly. It's pure infrastructure — no query methods yet.

See `plans/auth-pr-06-repository.md` for the full PR 6 index.

## Prerequisites

- PR 3 (migrations) — tables must exist
- PR 4 (domain types) — `auth.Repository` interface and `auth.DBTX` are defined

## Scope

### Refactor `internal/pgsql/pgsql.go`

- Replace the `DB` wrapper struct with a single `OpenDB(dsn string, maxOpen, maxIdle int, maxIdleTime time.Duration) (*sql.DB, error)` function
- This is more idiomatic Go — `*sql.DB` is already a connection pool, concurrency-safe, and has its own `Close()` and `BeginTx()` methods
- Callers pass `*sql.DB` directly to repositories; no accessor needed

### Create `internal/pgsql/auth_repository.go`

- Define `AuthRepository` struct holding a `*sql.DB`
- Constructor: `NewAuthRepository(db *sql.DB) *AuthRepository`
- The compile-time interface check (`var _ auth.Repository = (*AuthRepository)(nil)`) is deferred to PR 6e, after all methods are implemented

### Update `cmd/api/application.go`

- Replace the `DB` struct usage with a call to `pgsql.OpenDB(...)`, passing `*sql.DB` directly

## Files Changed

- Refactor `internal/pgsql/pgsql.go`
- Create `internal/pgsql/auth_repository.go`
- Update `cmd/api/application.go`

## Verification

- `make test` — all existing tests still pass
- `AuthRepository` struct exists and is exported
- `OpenDB` returns a configured, pinged `*sql.DB`
