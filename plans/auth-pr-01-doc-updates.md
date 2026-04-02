# PR 1: Architecture Doc Updates

## Context

The auth architecture has been revised. The API now owns authentication end-to-end (sessions, step-up, rate limiting, enumeration safety, email delivery). The previous docs described a BFF-centric model where the BFF owned sessions, cookies, rate limiting, and step-up state. These docs need to reflect the new reality before implementation begins.

Key decisions driving the updates:
- API owns sessions via opaque DB-backed tokens, presented as `Authorization: Bearer <token>`
- No API key or X-User-ID headers — session token replaces both
- Step-up auth is API-owned (15-min window, `last_step_up_at` on sessions table)
- Rate limiting is API-owned via `RateLimiter` interface
- Enumeration safety is API-owned — generic errors on login/password-reset
- Email delivery is API-owned via `EmailSender` interface
- BFF is a thin translation layer: cookies in from browser, Bearer out to API
- Multi-account: active account on session, switch endpoint, last-used tracking

See `plans/auth-implementation.md` for the full plan.

## Prerequisites

None — this is the first PR.

## Scope

Update all three auth docs to reflect the revised architecture.

### `docs/auth-architecture.md`

- Update system boundary diagram: remove "internal JWT auth" between BFF and API, replace with "Bearer token (opaque, DB-backed)"
- Update "Core Design Principles" section: API owns auth, sessions, step-up
- Update "Session model" section: sessions are API-owned, not BFF-owned. 30-day idle / 180-day absolute TTL. Stored in sessions table with `token_hash`.
- Update "Revocation strategy": revocation is immediate (delete/revoke session row), not dependent on short-lived JWTs
- Update "Step-up authentication": API-owned, 15-min window, `last_step_up_at` on session
- Update "Login rate limiting": API-owned rate limiting via `RateLimiter` interface
- Update "Database Model" / sessions table: API-owned, add `token_hash` column, update schema to match plan
- Update "API Endpoint Contracts": replace BFF-facing endpoints with internal API contract (see endpoint table in master plan)
- Update "Implementation Notes": remove references to BFF owning sessions/cookies/rate-limiting as primary responsibilities
- Add "Multi-Client Auth Flows" section showing web (BFF), native, and CLI flows

### `docs/auth-api-responsibilities.md`

- Update system boundary diagram ownership section:
  - BFF owns: cookie-to-Bearer translation for web, browser UX
  - API owns: sessions, authentication, step-up, rate limiting, enumeration safety, identity model, password hashing, token lifecycle, email delivery (via interface), constraints
- Update all flow diagrams to show Bearer token instead of internal JWT
- Update register flow: API creates session and returns token
- Update login flow: API creates session and returns token
- Update step-up flow: API-owned, not BFF-owned
- Update summary section

### `docs/auth-flows.md`

- Update all endpoint tables to match the revised contract (public, authenticated, authenticated+step-up)
- Remove BFF session cookie references from endpoint auth requirements
- Replace `Auth: Session` with `Auth: Bearer` or `Auth: Public`
- Add login endpoint (was previously "create BFF session" — now returns API session token)
- Add logout, logout-all endpoints
- Add verify-password (step-up) endpoint
- Add account switch endpoint
- Remove `/me` endpoints — replaced with `/v1/users/{userID}`
- Update step-up section: API enforces, not BFF
- Add rate limiting notes to public endpoints
- Add enumeration safety notes

## Files Changed

- `docs/auth-architecture.md`
- `docs/auth-api-responsibilities.md`
- `docs/auth-flows.md`

## Verification

- Read all three docs end-to-end for internal consistency
- No references to: API key, X-API-Key, X-User-ID, X-Account-ID, "BFF-owned sessions", "internal JWT"
- All endpoint paths match the master plan's endpoint contract
- Session model matches the sessions table schema in the master plan
- Multi-client flows (web/native/CLI) are clearly documented
