# PR 2: Structured Error Format

## Context

The API currently uses `{"error": "message"}` for error responses. The auth system needs richer error information — error codes for programmatic handling, details for field-level validation errors. This PR migrates the entire project to a structured error format before any auth code is written, so all future endpoints use it from the start.

New format:
```json
{
  "error_code": "NOT_FOUND",
  "message": "The requested resource could not be found.",
  "details": {}
}
```

See `plans/auth-implementation.md` for the full plan.

## Prerequisites

- PR 1 (doc updates) — docs should reflect the new error format

## Scope

### Create `internal/server/errors.go`

- `APIError` struct with `ErrorCode string`, `Message string`, `Details any`, `StatusCode int`
- `APIError` implements the `error` interface
- Helper constructors/functions:
  - `errorResponse(w, r, status, errorCode, message, details)` — writes structured JSON error
  - `serverErrorResponse(w, r, err)` — logs error, returns 500 `INTERNAL_ERROR`
  - `notFoundResponse(w, r)` — 404 `NOT_FOUND`
  - `methodNotAllowedResponse(w, r)` — 405 `METHOD_NOT_ALLOWED`
  - `badRequestResponse(w, r, err)` — 400 `BAD_REQUEST`
  - `unauthorizedResponse(w, r, message)` — 401 `UNAUTHENTICATED`
  - `forbiddenResponse(w, r, errorCode, message)` — 403 (flexible error_code for STEP_UP_REQUIRED vs FORBIDDEN)
  - `rateLimitedResponse(w, r)` — 429 `RATE_LIMITED`
  - `conflictResponse(w, r, errorCode, message)` — 409 (flexible for EMAIL_UNAVAILABLE, etc.)
  - `validationErrorResponse(w, r, details)` — 400 `INVALID_INPUT` with field-level details

### Modify `internal/server/helpers.go`

- Remove old error helper functions (`errorResponse` if it exists with old format)
- Add `readJSON(w, r, dst)` helper for parsing request bodies with:
  - Max body size limit (e.g., 1MB)
  - Unknown field rejection
  - Single JSON value enforcement
  - Descriptive error messages for malformed JSON

### Modify `internal/server/router.go`

- Set chi's `NotFound` handler to use `notFoundResponse`
- Set chi's `MethodNotAllowed` handler to use `methodNotAllowedResponse`

### Update existing tests

- `internal/server/router_test.go` — update expected error format for 404 and 405 responses
- `internal/server/helpers_test.go` — update/add tests for new error helpers
- Add `internal/server/errors_test.go` — test each error response function

## Files Changed

- Create `internal/server/errors.go`
- Modify `internal/server/helpers.go`
- Modify `internal/server/router.go`
- Update `internal/server/router_test.go`
- Update `internal/server/helpers_test.go`
- Create `internal/server/errors_test.go`

## Verification

- `make test` — all tests pass
- `curl` a non-existent route: returns `{"error_code": "NOT_FOUND", "message": "...", "details": {}}`
- `curl` with wrong HTTP method: returns `{"error_code": "METHOD_NOT_ALLOWED", ...}`
- No remaining references to old `{"error": "..."}` format in handler code
