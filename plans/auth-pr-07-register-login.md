# PR 7: Registration + Login (Service + Handlers + Wiring)

## Context

This is the core auth flow — the first PR where the full stack is wired together. Registration creates a user, personal account, membership, email identity, verification token, and session — then returns a session token. Login verifies credentials, creates a session, and defaults to the user's last-used account.

This PR also wires the full dependency chain in `application.go`: DB -> repository -> service -> server.

See `plans/auth-implementation.md` for the full plan, endpoint contract, and wiring diagram.

## Prerequisites

- PR 2 (error format) — handlers use structured errors
- PR 4 (domain types) — service uses domain types, interfaces, hasher, token gen, email sender
- PR 5 (middleware) — routes use rate limiting and authenticate middleware
- PR 6 (repository) — service depends on repository implementation

## Scope

### Service implementation — `internal/auth/service.go`

**`Register(ctx, params RegisterParams) (*RegisterResult, error)`**
1. Validate email format, normalize
2. Validate password (8 min, 128 max)
3. Hash password with `PasswordHasher`
4. Generate session token (raw + hash)
5. Generate email verification token (raw + hash)
6. Call `repo.CreateRegistration()` — transactional creation of user + account + membership + identity + session
7. Call `emailSender.SendVerificationEmail()` with raw verification token
8. Return `RegisterResult` with user, account, email identity, and raw session token

**`Login(ctx, params LoginParams) (*LoginResult, error)`**
1. Normalize email
2. Call `repo.GetEmailIdentityByEmail()` — if not found, return `ErrInvalidCredentials` (enumeration safe)
3. If identity's user is soft-deleted: return `ErrInvalidCredentials`
4. Compare password with `PasswordHasher` — if mismatch, return `ErrInvalidCredentials`
5. Get user's last-used membership via `repo.GetPrimaryMembership()`
6. Generate session token (raw + hash)
7. Create session with `repo.CreateSession()` (account_id = last-used account)
8. Update `membership.last_used_at` via `repo.UpdateMembershipLastUsedAt()`
9. Return `LoginResult` with user, account, and raw session token

### Mock repository — `internal/auth/mock_repository_test.go`

- Create a mock implementation of `auth.Repository` for unit testing
- Each method backed by a function field for per-test customization
- Used by service unit tests to avoid DB dependency

### Service unit tests

- Register: success (all entities created, token returned), duplicate email error, invalid email, password too short/long
- Login: success (returns token), wrong password (generic error), nonexistent email (same generic error), deleted user (same generic error), defaults to last-used account

### Handlers — `internal/server/auth_handlers.go`

**`registerHandler(w, r)`**
- Parse JSON body: `{email, password, display_name}`
- Call `authService.Register()`
- Map errors: `ErrEmailUnavailable` -> 409, `ErrPasswordTooShort/Long` -> 400, validation errors -> 400
- Return 201 with: `{user, account, email, session_token}`

**`loginHandler(w, r)`**
- Parse JSON body: `{email, password}`
- Call `authService.Login()`
- Map errors: `ErrInvalidCredentials` -> 401 (generic message "Invalid email or password")
- Return 200 with: `{user, account, session_token}`

### Handler tests — `internal/server/auth_handlers_test.go`

- Register: valid request -> 201, duplicate email -> 409, invalid email -> 400, short password -> 400
- Login: valid -> 200, wrong password -> 401 generic, nonexistent email -> 401 generic
- Both: malformed JSON -> 400, missing fields -> 400
- Rate limiting: rapid requests -> 429

### Router — `internal/server/router.go`

```go
r.Route("/v1", func(r chi.Router) {
    r.Use(s.authenticate)

    // Public auth routes (rate limited)
    r.Group(func(r chi.Router) {
        r.Use(s.rateLimit(10, time.Minute))  // example limits
        r.Post("/auth/register", s.registerHandler)
        r.Post("/auth/login", s.loginHandler)
    })

    // Existing
    r.Get("/healthcheck", s.healthcheckHandler)
})
```

### Wiring — `cmd/api/application.go`

```go
// In Run():
db, err := pgsql.OpenDB(...)
authRepo := pgsql.NewAuthRepository(db)
hasher := auth.NewArgon2Hasher()
emailSender := auth.NewLoggingEmailSender(logger)
authService := auth.NewService(authRepo, hasher, emailSender, logger)
rateLimiter := server.NewInMemoryRateLimiter()
srv := server.NewServer(logger, authService, rateLimiter, ...)
```

## Files Changed

- Create `internal/auth/mock_repository_test.go`
- Modify `internal/auth/service.go` — implement `Register()`, `Login()`
- Create `internal/auth/service_register_test.go`
- Create `internal/auth/service_login_test.go`
- Create `internal/server/auth_handlers.go`
- Create `internal/server/auth_handlers_test.go`
- Modify `internal/server/router.go`
- Modify `internal/server/server.go`
- Modify `cmd/api/application.go`

## Verification

- `make test` — all unit and integration tests pass
- Manual integration test:
  1. `make db/start && make db/reset`
  2. Start server
  3. `curl -X POST /v1/auth/register -d '{"email":"test@example.com","password":"password123","display_name":"Test"}' ` -> 201 with session_token
  4. `curl -H "Authorization: Bearer <token>" /v1/healthcheck` -> 200 (authenticated)
  5. `curl -X POST /v1/auth/login -d '{"email":"test@example.com","password":"password123"}'` -> 200 with session_token
  6. Login with wrong password -> 401 generic error
  7. Login with nonexistent email -> 401 same generic error (enumeration safe)
  8. Rapid login attempts -> 429 rate limited
