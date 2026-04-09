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
    Allow(key string, limit int, window time.Duration) (bool, error)
}
```

- `InMemoryRateLimiter` using a map of key -> sliding window counters
- Thread-safe (mutex-protected)
- Automatic cleanup of expired entries (background goroutine or lazy cleanup)

**`RateLimit(limiter RateLimiter, limit int, window time.Duration) func(http.Handler) http.Handler`**
- Extracts client IP from request (consider `X-Forwarded-For` for proxied setups)
- Calls `limiter.Allow(ip, limit, window)`
- If denied: return 429 `RATE_LIMITED` structured error
- If allowed: call next handler

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
