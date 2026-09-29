# Issue drafts

Ready-to-file GitHub issues, numbered in the suggested order to work on them. Each one is written so an agent can implement it on its own branch.

Create them all with `./docs/issues/create_issues.sh` (needs the `gh` CLI). Once they are filed, delete this directory.

| # | File | Title | Labels |
|---|---|---|---|
| 1 | [01-db-open-error.md](01-db-open-error.md) | App exits with success when the database connection fails | bug |
| 2 | [02-signup-password-bytes.md](02-signup-password-bytes.md) | Signup returns 500 for passwords over 72 bytes but 72 characters or fewer | bug |
| 3 | [03-readme.md](03-readme.md) | Add a README with setup, run, and test instructions | documentation |
| 4 | [04-logout-post.md](04-logout-post.md) | Logout uses GET, so any page can log users out | bug,security |
| 5 | [05-csrf.md](05-csrf.md) | Add CSRF protection for state-changing requests | security,enhancement |
| 6 | [06-feed-id-404.md](06-feed-id-404.md) | Malformed feed ID in /feeds/{id} returns 500 instead of 404 | bug |
| 7 | [07-duplicate-feed-url.md](07-duplicate-feed-url.md) | Adding a feed URL that already exists returns 500 | bug |
| 8 | [08-nullable-feed-columns.md](08-nullable-feed-columns.md) | Reading a feed with NULL site_url or description fails | bug |
| 9 | [09-panic-html-error.md](09-panic-html-error.md) | Recovered panics return JSON instead of the HTML error page | bug |
| 10 | [10-password-echo.md](10-password-echo.md) | Login and signup forms echo the password back into the page | security |
| 11 | [11-signup-flash.md](11-signup-flash.md) | Signup success message exposes the internal user ID | security |
| 12 | [12-static-dir-listing.md](12-static-dir-listing.md) | /static/ serves directory listings | security |
| 13 | [13-page-titles.md](13-page-titles.md) | Login page title says "signup", and other titles need a cleanup | bug,good first issue |

**Merge-conflict note:** several issues create `internal/server/testutils_test.go` and `router_test.go` / `validator_test.go`. Merge them one at a time, and rebase or merge `main` into each branch before opening its PR.
