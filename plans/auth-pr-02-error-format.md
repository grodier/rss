# PR 2: Structured Error Format (Index)

This work has been broken into three reviewable PRs, each building on the last:

| PR | Description | Key Changes |
|----|-------------|-------------|
| [02a](auth-pr-02a-error-format.md) | Core structured error type | `APIError` struct, new `errorResponse`, migrate `serverErrorResponse` |
| [02b](auth-pr-02b-error-format.md) | Router error handlers + `readJSON` | Wire 404/405 into chi, add JSON body parsing helper |
| [02c](auth-pr-02c-error-format.md) | Auth error helpers | `unauthorized`, `forbidden`, `rateLimited`, `conflict`, `validationError` |

## Merge order

02a -> 02b -> 02c (each builds on the previous)

See `plans/auth-implementation.md` for the full auth plan.
