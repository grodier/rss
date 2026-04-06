# PR 02b-b: readJSON Helper

## Context

Builds on PR 02a. Auth endpoints will need to parse JSON request bodies safely. This PR adds a `readJSON` helper with size limits, unknown-field rejection, and descriptive error messages.

## Scope

### Add to `internal/server/helpers.go`

- `readJSON(w, r, dst)` helper for parsing request bodies with:
  - Max body size limit (e.g., 1MB)
  - Unknown field rejection
  - Single JSON value enforcement
  - Descriptive error messages for malformed JSON

### Update tests

- Add `readJSON` tests to `internal/server/helpers_test.go`

## Files Changed

- Modify `internal/server/helpers.go`
- Modify `internal/server/helpers_test.go`

## Verification

- `make test` — all tests pass
