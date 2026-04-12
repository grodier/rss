# PR 6: Auth Repository (pgsql) (Index)

Parent plan: `plans/auth-implementation.md`

## Sub-PRs

| Sub-PR | Title | Description |
|--------|-------|-------------|
| [06a](auth-pr-06a-repository.md) | Repository scaffold | `SqlDB()` accessor, `AuthRepository` struct + constructor |
| [06b](auth-pr-06b-repository.md) | Users, Accounts, Memberships | 7 methods — straightforward entity CRUD |
| [06c](auth-pr-06c-repository.md) | Email Identities + Password | 9 methods — complex constraints, case-insensitive lookup |
| [06d](auth-pr-06d-repository.md) | Auth Tokens + Sessions | 9 methods — token lifecycle + session management |
| [06e](auth-pr-06e-repository.md) | Registration (transactional) | 1 method — cross-table transaction, interface check |

## Merge Order

```
06a → 06b ─┐
06a → 06c ─┼→ 06e
06a → 06d ─┘
```

06a first, then 06b/06c/06d in any order, then 06e last.
