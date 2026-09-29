#!/usr/bin/env bash
# Creates one GitHub issue per markdown file in this directory, in priority order.
# Requires the GitHub CLI (gh), authenticated with access to grodier/rss.
# Usage: ./docs/issues/create_issues.sh
set -euo pipefail
cd "$(dirname "$0")"
REPO=grodier/rss

# Make sure the labels exist (no-op if they already do).
for l in bug security enhancement documentation "good first issue"; do
  gh label create "$l" --repo "$REPO" 2>/dev/null || true
done

create() {
  local file="$1" title="$2" labels="$3"
  echo "Creating: $title"
  gh issue create --repo "$REPO" --title "$title" --label "$labels" --body-file "$file"
}

create 01-db-open-error.md "App exits with success when the database connection fails" "bug"
create 02-signup-password-bytes.md "Signup returns 500 for passwords over 72 bytes but 72 characters or fewer" "bug"
create 03-readme.md "Add a README with setup, run, and test instructions" "documentation"
create 04-logout-post.md "Logout uses GET, so any page can log users out" "bug,security"
create 05-csrf.md "Add CSRF protection for state-changing requests" "security,enhancement"
create 06-feed-id-404.md "Malformed feed ID in /feeds/{id} returns 500 instead of 404" "bug"
create 07-duplicate-feed-url.md "Adding a feed URL that already exists returns 500" "bug"
create 08-nullable-feed-columns.md "Reading a feed with NULL site_url or description fails" "bug"
create 09-panic-html-error.md "Recovered panics return JSON instead of the HTML error page" "bug"
create 10-password-echo.md "Login and signup forms echo the password back into the page" "security"
create 11-signup-flash.md "Signup success message exposes the internal user ID" "security"
create 12-static-dir-listing.md "/static/ serves directory listings" "security"
create 13-page-titles.md "Login page title says \"signup\", and other titles need a cleanup" "bug,good first issue"
