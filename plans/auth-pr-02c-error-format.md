# PR 02c: Auth Error Helpers

## Context

Builds on PR 02b. These are the remaining error response helpers needed by auth endpoints (registration, login, session management). Pure additions — no existing code changes, no migration.

## Scope

### Add to `internal/server/errors.go`

- `badRequestResponse(w, r, err)` — 400 `BAD_REQUEST`
- `unauthorizedResponse(w, r, message)` — 401 `UNAUTHENTICATED`
- `forbiddenResponse(w, r, errorCode, message)` — 403 (flexible error_code for `STEP_UP_REQUIRED` vs `FORBIDDEN`)
- `rateLimitedResponse(w, r)` — 429 `RATE_LIMITED`
- `conflictResponse(w, r, errorCode, message)` — 409 (flexible for `EMAIL_UNAVAILABLE`, etc.)
- `validationErrorResponse(w, r, details)` — 400 `INVALID_INPUT` with field-level details

### Add tests to `internal/server/errors_test.go`

- Test each new error response function for correct status code, error_code, message, and details

## Files Changed

- Modify `internal/server/errors.go`
- Modify `internal/server/errors_test.go`

## Verification

- `make test` — all tests pass
- No remaining error helpers from the original plan are missing
- All auth-related error codes are covered: `BAD_REQUEST`, `UNAUTHENTICATED`, `FORBIDDEN`, `STEP_UP_REQUIRED`, `RATE_LIMITED`, `EMAIL_UNAVAILABLE`, `INVALID_INPUT`
