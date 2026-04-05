# PR 02b: Router Error Handlers + readJSON

## Context

Builds on PR 02a. Chi's default 404/405 handlers return plain text. This PR wires structured JSON error responses into the router and adds a `readJSON` helper for safely parsing request bodies — both needed before auth endpoints can be written.

## Scope

### Add to `internal/server/errors.go`

- `notFoundResponse(w, r)` — 404 `NOT_FOUND`
- `methodNotAllowedResponse(w, r)` — 405 `METHOD_NOT_ALLOWED`

### Modify `internal/server/router.go`

- Set chi's `NotFound` handler to use `notFoundResponse`
- Set chi's `MethodNotAllowed` handler to use `methodNotAllowedResponse`

### Add to `internal/server/helpers.go`

- `readJSON(w, r, dst)` helper for parsing request bodies with:
  - Max body size limit (e.g., 1MB)
  - Unknown field rejection
  - Single JSON value enforcement
  - Descriptive error messages for malformed JSON

### Update tests

- Update `internal/server/router_test.go` — assert 404 and 405 responses use the new structured JSON format (`error_code`, `message`, `details`)
- Add `readJSON` tests to `internal/server/helpers_test.go`
- Add `notFoundResponse` and `methodNotAllowedResponse` tests to `internal/server/errors_test.go`

## Files Changed

- Modify `internal/server/errors.go`
- Modify `internal/server/helpers.go`
- Modify `internal/server/router.go`
- Modify `internal/server/router_test.go`
- Modify `internal/server/helpers_test.go`
- Modify `internal/server/errors_test.go`

## Verification

- `make test` — all tests pass
- `curl` a non-existent route: returns `{"error_code": "NOT_FOUND", "message": "...", "details": {}}`
- `curl` with wrong HTTP method: returns `{"error_code": "METHOD_NOT_ALLOWED", ...}`
