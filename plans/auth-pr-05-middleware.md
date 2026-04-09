# PR 5: Middleware — Rate Limiting, Auth, Step-up (Index)

This plan was split into three sub-PRs for reviewability.

| Sub-PR | Description | Files |
|--------|-------------|-------|
| [5a: Rate Limiter](auth-pr-05a-middleware.md) | RateLimiter interface, in-memory impl, RateLimit middleware | `ratelimit.go`, `ratelimit_test.go` |
| [5b: Context + Authenticate + Server](auth-pr-05b-middleware.md) | Context helpers, Authenticate middleware, Server struct changes | `context.go`, `middleware.go`, `middleware_test.go`, `server.go` |
| [5c: Guard Middlewares](auth-pr-05c-middleware.md) | RequireAuth, RequireUserMatch, RequireStepUp | `middleware.go`, `middleware_test.go` |

**Merge order**: 5a → 5b → 5c

See `plans/auth-implementation.md` for the full auth plan.
