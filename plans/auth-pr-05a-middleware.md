# PR 5a: Middleware — Rate Limiter

## Context

This PR adds rate limiting infrastructure for the auth system. The rate limiter uses an interface (`RateLimiter`) with an in-memory implementation, swappable to Redis later. It also provides the `RateLimit` middleware wrapper that will be applied to public endpoints (register, login, password reset) to protect against abuse.

This is fully independent of the auth middleware — no session or user types involved.

See `plans/auth-implementation.md` for the full plan.

## Prerequisites

- PR 4 (domain types) — only for structured error response helpers (`rateLimitedResponse`)

## Scope

### `internal/server/ratelimit.go` — RateLimiter interface + in-memory impl + middleware

```go
type RateLimiter interface {
    Allow(key string) (bool, error)
    Window() time.Duration
}
```

- `InMemoryRateLimiter` configured with `limit` and `window` at construction time (per-instance policy prevents sliding window state corruption when the same key is used across endpoints with different limits)
- Thread-safe (mutex-protected)
- Automatic cleanup of expired entries (background goroutine)
- `Stop()` is idempotent via `sync.Once`

**`RateLimit(limiter RateLimiter) func(http.Handler) http.Handler`**
- Extracts client IP from request (`X-Forwarded-For` with `net.ParseIP` validation, falls back to `RemoteAddr`)
- Calls `limiter.Allow(ip)`
- If denied: sets `Retry-After` header derived from `limiter.Window()`, returns 429 `RATE_LIMITED` structured error
- If allowed: call next handler
- Per-endpoint policies use separate limiter instances (e.g., login vs register)

## Files Changed

- Create `internal/server/ratelimit.go`
- Create `internal/server/ratelimit_test.go`

## Verification

- `make test` — all tests pass

### Rate limiter unit tests
- Requests under limit: allowed
- Requests at limit: rejected with 429
- After window expires: allowed again
- Different keys are independent
- Concurrent access is safe

### RateLimit middleware tests
- Request under limit: passes through to next handler
- Request at limit: returns 429 `RATE_LIMITED` structured error
