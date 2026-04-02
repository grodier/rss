# PR 5: Middleware — Rate Limiting, Auth, Step-up

## Context

This PR builds all middleware needed for the auth system. The API owns authentication, rate limiting, and step-up enforcement — no reliance on clients.

Three middleware layers:
1. **Rate limiting** — protects public endpoints (register, login, password reset) from abuse
2. **Authentication** — validates Bearer tokens, extracts user/account context from sessions
3. **Step-up** — enforces recent password verification for destructive operations

The rate limiter uses an interface (`RateLimiter`) with an in-memory implementation, swappable to Redis later.

See `plans/auth-implementation.md` for the full plan.

## Prerequisites

- PR 4 (domain types) — middleware uses `Session`, `User`, `Account` types and the `Repository` interface for session lookup

## Scope

### `internal/server/ratelimit.go` — RateLimiter interface + in-memory impl

```go
type RateLimiter interface {
    Allow(key string, limit int, window time.Duration) (bool, error)
}
```

- `InMemoryRateLimiter` using a map of key -> sliding window counters
- Thread-safe (mutex-protected)
- Automatic cleanup of expired entries (background goroutine or lazy cleanup)
- Tests: allows requests under limit, rejects at limit, window expiry resets counter, concurrent access

### `internal/server/middleware.go` — Auth middleware

**`RateLimit(limiter RateLimiter, limit int, window time.Duration) func(http.Handler) http.Handler`**
- Extracts client IP from request (consider `X-Forwarded-For` for proxied setups)
- Calls `limiter.Allow(ip, limit, window)`
- If denied: return 429 `RATE_LIMITED` structured error
- If allowed: call next handler

**`Authenticate(repo auth.Repository) func(http.Handler) http.Handler`**
- Parses `Authorization: Bearer <token>` header
- If missing/malformed: stores no context, calls next (allows unauthenticated routes to pass through)
- If present: hashes token with SHA-256, calls `repo.GetSessionByTokenHash(ctx, hash)`
- Validates session: not revoked, not expired (absolute), not idle-timed-out (30 days from `last_activity_at`)
- If invalid: return 401 `UNAUTHENTICATED`
- If valid: updates `last_activity_at`, stores user ID, account ID, and session in request context
- Note: Authenticate is permissive — it sets context if a valid token is found but doesn't require it. RequireAuth enforces.

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

### Modify `internal/server/server.go`

- Add `AuthService *auth.Service` and `RateLimiter RateLimiter` fields to Server struct
- Update constructor to accept these dependencies

## Files Changed

- Create `internal/server/ratelimit.go`
- Create `internal/server/ratelimit_test.go`
- Create `internal/server/middleware.go`
- Create `internal/server/middleware_test.go`
- Create `internal/server/context.go`
- Modify `internal/server/server.go`

## Verification

- `make test` — all tests pass

### Rate limiting tests
- Requests under limit: allowed
- Requests at limit: rejected with 429
- After window expires: allowed again
- Different keys are independent

### Authenticate tests
- No Authorization header: context has no user (passes through)
- Malformed header (not "Bearer ..."): context has no user (passes through)
- Valid token with active session: context populated with user/account/session
- Valid token with revoked session: 401
- Valid token with expired session (absolute): 401
- Valid token with idle-timed-out session (30 days): 401
- Token not found in DB: 401

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
