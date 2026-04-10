# PR 5b: Middleware — Context Helpers, Authenticate, Server Struct

## Context

This PR adds the core auth middleware infrastructure: context helpers for passing user/session data through the request chain, the `Authenticate` middleware that validates Bearer tokens and populates context, and the Server struct changes to wire in auth dependencies.

`Authenticate` is permissive — it sets context if a valid token is found but doesn't require one. Guard middlewares that enforce auth requirements come in PR 5c.

Builds on PR 5a (rate limiter).

See `plans/auth-implementation.md` for the full plan.

## Prerequisites

- PR 4 (domain types) — middleware uses `Session`, `User`, `Account` types and the `Repository` interface for session lookup
- PR 5a (rate limiter) — `RateLimiter` type added to Server struct

## Scope

### `internal/server/context.go` — Context helpers

```go
type contextKey string

const (
    userIDKey    contextKey = "userID"
    accountIDKey contextKey = "accountID"
    sessionKey   contextKey = "session"
)

func contextSetUserID(r *http.Request, id uuid.UUID) *http.Request
func contextGetUserID(r *http.Request) (uuid.UUID, bool)
func contextSetAccountID(r *http.Request, id uuid.UUID) *http.Request
func contextGetAccountID(r *http.Request) (uuid.UUID, bool)
func contextSetSession(r *http.Request, s *auth.Session) *http.Request
func contextGetSession(r *http.Request) (*auth.Session, bool)
```

### `internal/server/middleware.go` — Authenticate middleware

**`Authenticate(repo auth.Repository) func(http.Handler) http.Handler`**
- Parses `Authorization: Bearer <token>` header
- If missing/malformed: stores no context, calls next (allows unauthenticated routes to pass through)
- If present: hashes token with SHA-256, calls `repo.GetSessionByTokenHash(ctx, hash)`
- Validates session: not revoked, not expired (absolute), not idle-timed-out (30 days from `last_activity_at`)
- If invalid: return 401 `UNAUTHENTICATED`
- If valid: updates `last_activity_at`, stores user ID, account ID, and session in request context
- Note: Authenticate is permissive — it sets context if a valid token is found but doesn't require it. RequireAuth enforces.

### Modify `internal/server/server.go`

- Add `AuthService *auth.Service` and `RateLimiter RateLimiter` fields to Server struct
- Update constructor to accept these dependencies

## Files Changed

- Create `internal/server/context.go`
- Create `internal/server/middleware.go`
- Create `internal/server/middleware_test.go`
- Modify `internal/server/server.go`

## Verification

- `make test` — all tests pass

### Authenticate tests
- No Authorization header: context has no user (passes through)
- Malformed header (not "Bearer ..."): context has no user (passes through)
- Valid token with active session: context populated with user/account/session
- Valid token with revoked session: 401
- Valid token with expired session (absolute): 401
- Valid token with idle-timed-out session (30 days): 401
- Token not found in DB: 401
