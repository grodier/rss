# PR 02a: Core Structured Error Type

## Context

The API currently uses `{"error": "message"}` for error responses. The auth system needs richer error information — error codes for programmatic handling, details for field-level validation errors. This PR introduces the `APIError` struct and new `errorResponse` function, then migrates the existing `serverErrorResponse` to use the new format. Everything else in the series builds on this.

New format:

```json
{
  "error_code": "NOT_FOUND",
  "message": "The requested resource could not be found.",
  "details": {}
}
```

## Scope

### Create `internal/server/errors.go`

- `APIError` struct with `ErrorCode string`, `Message string`, `Details any`, `StatusCode int`
- `APIError` implements the `error` interface
- `errorResponse(w, r, status, errorCode, message, details)` — writes structured JSON error response
- `serverErrorResponse(w, r, err)` — logs error, returns 500 `INTERNAL_ERROR`

### Modify `internal/server/helpers.go`

- Remove `errorResponse` (old format) and `serverErrorResponse` — both move to `errors.go` with new signatures
- Keep `writeJSON`, `logError`, and the `envelope` type in place

### Update tests

- Update `internal/server/helpers_test.go` — remove `TestErrorResponse` and `TestServerErrorResponse` (these move to errors_test.go)
- Create `internal/server/errors_test.go` — test `errorResponse` (new format) and `serverErrorResponse`

## Files Changed

- Create `internal/server/errors.go`
- Modify `internal/server/helpers.go`
- Modify `internal/server/helpers_test.go`
- Create `internal/server/errors_test.go`

## Verification

- `make test` — all tests pass
- `serverErrorResponse` returns `{"error_code": "INTERNAL_ERROR", "message": "...", "details": {}}`
- No remaining references to old `{"error": "..."}` envelope format in production code
