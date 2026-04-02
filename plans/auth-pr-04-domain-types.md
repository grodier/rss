# PR 4: Domain Types, Errors, Interfaces, and Utilities

## Context

This PR establishes the auth package's contracts and utilities without implementing the service layer. It defines the domain model (structs), error types, repository interface, service struct skeleton, and utility implementations (password hashing, token generation, email validation, email sending).

All interfaces are designed for testability and swappability:
- `PasswordHasher` — argon2id now, swappable for testing
- `EmailSender` — logging impl now, swappable to real provider or event system
- `Repository` — interface for mock testing, implemented against Postgres in PR 6

See `plans/auth-implementation.md` for the full plan.

## Prerequisites

- PR 3 (migrations) — domain types should match the DB schema

## Scope

### `internal/auth/models.go` — Domain structs

```go
type User struct {
    ID          uuid.UUID
    DisplayName *string
    CreatedAt   time.Time
    DeletedAt   *time.Time
    PurgeAfter  *time.Time
}

type Account struct {
    ID         uuid.UUID
    Name       *string
    CreatedAt  time.Time
    DeletedAt  *time.Time
    PurgeAfter *time.Time
}

type Membership struct {
    UserID    uuid.UUID
    AccountID uuid.UUID
    Role      string
    CreatedAt time.Time
    LastUsedAt time.Time
}

type EmailIdentity struct {
    ID           uuid.UUID
    UserID       uuid.UUID
    Provider     string
    Identifier   string
    PasswordHash *string
    VerifiedAt   *time.Time
    IsPrimary    bool
    CreatedAt    time.Time
    DeletedAt    *time.Time
    PurgeAfter   *time.Time
}

type AuthToken struct {
    ID         uuid.UUID
    UserID     uuid.UUID
    IdentityID *uuid.UUID
    Type       string
    TokenHash  string
    CreatedAt  time.Time
    ExpiresAt  time.Time
    UsedAt     *time.Time
}

type Session struct {
    ID             uuid.UUID
    UserID         uuid.UUID
    AccountID      uuid.UUID
    TokenHash      string
    CreatedAt      time.Time
    LastActivityAt time.Time
    ExpiresAt      time.Time
    IPAddress      *string
    UserAgent      *string
    LastStepUpAt   *time.Time
    RevokedAt      *time.Time
}
```

Plus param/result structs for service methods (e.g., `RegisterParams`, `RegisterResult`, `LoginParams`, `LoginResult`).

### `internal/auth/errors.go` — Sentinel errors

All sentinel errors as `var Err... = errors.New("...")`:
- `ErrInvalidCredentials`
- `ErrEmailUnavailable`
- `ErrEmailNotVerified`
- `ErrIdentityNotFound`
- `ErrTokenInvalidOrExpired`
- `ErrUserNotFound`
- `ErrCannotRemoveLastEmail`
- `ErrCannotRemovePrimaryEmail`
- `ErrPasswordTooShort`
- `ErrPasswordTooLong`
- `ErrSessionNotFound`
- `ErrSessionExpired`
- `ErrStepUpRequired`
- `ErrNotAMember`

Plus `ValidationError` struct for field-level validation details.

### `internal/auth/repository.go` — Repository interface

Full interface with all methods listed in the master plan's "Repository interface" section. Methods accept `context.Context` as first param. Methods that need transaction support accept a `DBTX` interface.

### `internal/auth/service.go` — Service struct + constructor

```go
type Service struct {
    repo        Repository
    hasher      PasswordHasher
    emailSender EmailSender
    logger      *slog.Logger
}

func NewService(repo Repository, hasher PasswordHasher, emailSender EmailSender, logger *slog.Logger) *Service
```

No method implementations yet — just the struct and constructor.

### `internal/auth/hasher.go` — PasswordHasher interface + argon2id

- `PasswordHasher` interface: `Hash(password string) (string, error)`, `Compare(password, hash string) (bool, error)`
- `Argon2Hasher` struct implementing the interface
- OWASP params: memory=47104 KiB, iterations=1, parallelism=1, salt=16 bytes, key=32 bytes
- PHC format string for storage: `$argon2id$v=19$m=47104,t=1,p=1$<salt>$<hash>`
- Tests: round-trip (hash then compare), wrong password fails, different hashes for same password (unique salts)

### `internal/auth/token.go` — Token generation + hashing

- `GenerateToken() (raw string, hash string, err error)` — 32 bytes crypto/rand, base64url encode, SHA-256 hash
- `HashToken(raw string) string` — SHA-256 hash of raw token
- Tests: generated tokens are unique, hash is deterministic, round-trip works

### `internal/auth/validation.go` — Email + password validation

- `ValidateEmail(email string) error` — format check via `net/mail.ParseAddress` or regex
- `NormalizeEmail(email string) string` — trim + lowercase
- `ValidatePassword(password string) error` — 8 min, 128 max, returns `ErrPasswordTooShort` or `ErrPasswordTooLong`
- Tests: valid/invalid emails, normalization, password edge cases (7 chars, 8 chars, 128 chars, 129 chars)

### `internal/auth/email.go` — EmailSender interface + LoggingEmailSender

- `EmailSender` interface: `SendVerificationEmail(ctx, to, token)`, `SendPasswordResetEmail(ctx, to, token)`
- `LoggingEmailSender` struct that logs the email details via `slog.Logger` (for dev/testing)
- Tests: verify LoggingEmailSender doesn't error, logs expected output

### Dependencies

- Add `github.com/google/uuid` to go.mod
- Add `golang.org/x/crypto` to go.mod

## Files Changed

- Create `internal/auth/models.go`
- Create `internal/auth/errors.go`
- Create `internal/auth/repository.go`
- Create `internal/auth/service.go`
- Create `internal/auth/hasher.go`
- Create `internal/auth/hasher_test.go`
- Create `internal/auth/token.go`
- Create `internal/auth/token_test.go`
- Create `internal/auth/validation.go`
- Create `internal/auth/validation_test.go`
- Create `internal/auth/email.go`
- Create `internal/auth/email_test.go`
- Modify `go.mod` / `go.sum`

## Verification

- `make test` — all tests pass
- Interfaces compile without implementation (repository, service methods not yet implemented)
- `Argon2Hasher`: hash + compare round-trips correctly, wrong password returns false
- `GenerateToken`: produces unique values, hash is deterministic
- `ValidatePassword`: boundary cases at 7, 8, 128, 129 characters
- `ValidateEmail`: rejects malformed, accepts valid formats
- `NormalizeEmail`: trims whitespace, lowercases
- `LoggingEmailSender`: runs without error
