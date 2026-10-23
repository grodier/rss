# Features

How each feature works and which code to read, in order. Read
[architecture.md](architecture.md) first for the overall shape and the glossary.

Each section has the flow, a reading list (file and symbol, in the order a request or job
goes through them), the tests that pin the behavior, and where the reasons behind it are
in [decisions.md](decisions.md).

## Accounts and sessions

**Flow.** Signup exists only when the app runs with `-invite-code`, and no page links to
it: people arrive through `/signup?invite=<code>`, which `signupHandler` pre-fills into
the "Invite code" field. `signupFormHandler` checks the per-IP limit, validates the form
(the code, compared with `subtle.ConstantTimeCompare`, then `validator.CheckNewAccount`
for the name, email and password, after trimming the name), and creates the user with an
Argon2id hash. An email that already has an account gets "There's already an account
with this email." on the email field, with "Log in instead, or reset your password."
under it, linking to `/login` and `/password/forgot?email=…` (`DuplicateEmail` on
`signupPage`). Login checks the per-IP limit, validates, then counts
the attempt against the submitted email *before* checking the password (refunded unless
the password was wrong), so parallel guesses can't slip past the limit. `Authenticate`
returns `rss.ErrNoAccount` for an unknown email ("There's no account with this email."
on the email field) and `rss.ErrWrongPassword` for a wrong password ("That password
isn't right." on the password field). Under the password field, "Forgot password?" links
to `/password/forgot`, with `?email=` and the address typed when re-rendered after a
failed attempt; a rate-limited login page also says "Forgot your password? Reset it."
(`RateLimited` on `loginPage`). On a successful login or signup, `startSession` renews
the session token, creates a `user_sessions` row for this device (deleting the user's
rows idle for 90 days), stores its ID in the session as `sessionID`, and the handler
redirects to `popReturnTo`: when `requireAuthentication` sends an anonymous GET or HEAD
to `/login`, it stores the request URI in the session as `returnTo`, and `popReturnTo`
returns it through `safeReturnPath` (else the timeline, `/`) and removes it. Other
methods aren't remembered, since replaying one as a GET would give a 405. A logged-in
user who opens `/login` or `/signup` is sent to the timeline. On every later request, `authenticate` loads that row
with its user in one query. A missing row, or one unused for 90 days
(`sessionIdleTimeout`, the row is deleted), makes the request anonymous; otherwise it
updates `last_seen_at` if it's over a day old (`sessionTouchInterval`) and puts the user's
ID and the session in the request context, where handlers read them with
`s.authenticatedUserID(r)` and `s.authenticatedSession(r)`. Logging out (the Log out
button on Settings) deletes the row, renews the token and removes `sessionID`. A login lasts at most a year (scs's
`Lifetime`).

Both forms show each field's error under it, tied to the input with `aria-describedby`,
and signup shows the name and password hints there all the time. After a failed submit
the server puts `autofocus` on the first invalid field (`Validator.FirstError`); nothing
is focused when a form first loads. `app.js` adds a Show/Hide toggle to the password
inputs.

The other way accounts are made is `www create-user` (`runCreateUser`), for your local
account: it reads the password from the first line of stdin, checks the account with the
same `CheckNewAccount`, and creates it with the same `Create`. Unlike signup, it says
when the email is taken. `make db/seed` runs it with `RSS_DEV_EMAIL` and
`RSS_DEV_PASSWORD` from `.env`, and `make db/reset` runs `db/seed` when `RSS_DEV_EMAIL`
is set.

**Settings.** The nav's account entry (the user's letter avatar, labelled Settings) opens
`/settings` (`settingsHandler`): a header with the avatar, name and email, then an
"Account" section of rows (`.settings-list`), each a label, a value and, on links, a
chevron, and a Log out button at the bottom. Each row that can be changed opens its own
small page with one form. Name (`/settings/name`) is the first:
`settingsNameFormHandler` trims the name, checks it with `CheckName` (required, at most
100 characters; `CheckNewAccount` uses it too), re-renders with 422 when it's invalid,
and otherwise saves it with `UpdateName`, flashes "Your name was updated." and redirects
to `/settings`. The Email row links to `/settings/email` (under "Changing email") and
has a "Not confirmed" tag (`.chip`) until the address is confirmed. A "Your data" section below has
the subscriptions export (under "Subscriptions").

**Privacy policy and terms.** `/privacy` and `/terms` (`privacyHandler`, `termsHandler`
in `policy.go`) are logged-out `.solo` pages with a `.prose` column; their text is HTML
in `privacy.html` and `terms.html`, with a "Last updated" date at the top, and they show
the support address through the `support-link` partial. They're linked from under the
signup button, the `policy-links` footer on the login page and the logged-out home page,
Settings' Help section, and every email's footer (`Privacy: <base URL>/privacy` in
`layout.tmpl`). When the text changes, change its date too.

Password (`/settings/password`) changes the password. Its form has a read-only email
field (`autocomplete="username"`, so password managers update the right entry), the
current password and the new one, and says beforehand that every other device will be
logged out. `settingsPasswordFormHandler` checks the per-user limit
(`passwordChangeLimiter`), that the current password isn't blank, the new one with
`CheckNewPassword`, and that the two differ, re-rendering with 422 (never echoing the
passwords) when something is wrong. `UserRepository.ChangePassword` then verifies the
current password before starting a transaction (Argon2 is slow; a wrong one is
`rss.ErrWrongPassword`, "That password isn't right." on the current password field), and
in one transaction stores the new hash and deletes the user's other `user_sessions` rows,
keeping this device's. The handler queues the `password_changed` email ("Your rss
password was changed", with a link to `/password/forgot` in case it wasn't them;
`sendPasswordChanged`) to the account's address, logging an error rather than failing if
it can't, flashes "Your password was changed. Other devices were logged out." and
redirects to `/settings`. `/.well-known/change-password` redirects there, so password
managers can link to it; it's outside the login group, so an anonymous visitor logs in
first and `returnTo` brings them back. Another device finds its row gone on its next
request and continues as anonymous.

A "Devices" section under Account has a "Log out of all other devices" button
(`logOutOthersHandler`), which needs no password since it can only protect:
`UserSessionRepository.DeleteOthers` deletes the user's other sessions and returns how
many, and the flash says "You're logged out on every other device." or, when there were
none, "There were no other devices to log out."

A last "Help" section has a `mailto:` row for the support address
(`Config.SupportEmail`; left out when it isn't set) and rows for the privacy policy and
terms.

**Deleting an account.** At the very bottom of Settings, under Log out, a "Delete
account" link in danger text opens `/settings/delete` (`settingsDeleteHandler`). The page
says what goes (the account, subscriptions and reading history) and what stays (feeds and
articles, which are shared), offers the OPML export first ("Export them first"), and has
a read-only email field (`autocomplete="username"`), a password field and a "Delete my
account" button (`.btn-danger`). `settingsDeleteFormHandler` checks the per-user limit
(`deleteAccountLimiter`, 429), that the password isn't blank ("Enter your password."),
then calls `UserRepository.Delete`, which reads the user and verifies the password
outside any transaction (a wrong one is `rss.ErrWrongPassword`: "That password isn't
right.", 422) and deletes the `users` row; the foreign keys cascade to the user's
subscriptions, reads, `user_sessions` and `user_tokens`. Deletion is immediate. `Delete`
returns the user as they were, and the handler queues the `account_deleted` email ("Your
rss account was deleted", which says backup copies go within 35 days) to the old address
(the outbox has no foreign key to users), logging an error rather than failing if it
can't. Then it renews the session token, removes `sessionID`, flashes "Your account was
deleted. Thanks for trying rss." and redirects to `/`, whose logged-out page shows the
flash.

**Email.** A handler that sends email (the password change and reset, email
confirmation and change, and account deletion) calls `s.sendEmail(ctx, to, name, data)`, which renders `internal/ui/emails/<name>.tmpl`
(parsed at startup by `mail.ParseTemplates` with the app's base URL and support email;
links are built with `s.absoluteURL`, never from the request) and queues it with
`mail.Queue.Enqueue`: `EmailRepository.Enqueue` inserts it into the `emails` outbox, then
the queue wakes this process's `mail.Runner`, so the page responds without waiting on the
provider. Over the per-address cap, the email isn't queued and `sendEmail` logs a warning
and returns nil. A runner worker claims the email (`ClaimNext`), sends it with the
configured sender, and records the outcome: `MarkSent`, `MarkFailed` with a retry time
(`retryDelay`), or `GiveUp`. The cap, retries, lease and retention are under "Background
work" and "Security measures" in [architecture.md](architecture.md).

**Email confirmation.** Confirmation is soft: nothing requires it yet. After signup's
`startSession`, `signupFormHandler` calls `sendConfirmation`, which makes a token with
`token.New` (32 random bytes, base64url; only its SHA-256 is stored), saves it with
`TokenRepository.Create` (purpose `confirm_email`, the address it's sent to, a 7-day
expiry; `Create` first deletes the user's expired tokens), and queues the `confirm_email`
email with the link `<base URL>/confirm-email?token=<token>`. If that fails, signup still
succeeds and the error is logged: the notice offers Resend. Asking again doesn't cancel
earlier links, since emails arrive out of order.

Until the address is confirmed, the timeline and Settings show the `confirm-notice`
partial (`.notice`; its data is `s.confirmNotice(r)`, nil for anonymous or confirmed
users): "Confirm your email address: we sent a link to <address>." with a Resend button
and a "Wrong address?" link to `/settings/email`. Resend posts to `/confirm-email/resend` with the page as `return_to`.
`confirmResendHandler` flashes "Your email address is already confirmed." for a
confirmed user, refuses past `confirmResendLimiter` (3 per user per hour) with "We've
already sent you several links. …", and otherwise calls `sendConfirmation` and flashes
"We sent a new link to <address>."; then it redirects to `safeReturnPath(return_to,
"/settings")`. The timeline shows flash messages, like other pages.

The link works in any browser, logged in or not. `GET /confirm-email`
(`confirmEmailHandler`) sets `Referrer-Policy: no-referrer` and `Cache-Control:
no-store`, and looks the token up with `TokenRepository.Get`, which only returns an
unexpired token whose address is still the account's. A valid one gets
`confirm_email.html`: "Confirm <address> for your rss account." and a Confirm email
address button that posts the token back; the GET never writes, so a mail scanner that
opens the link can't confirm. `POST /confirm-email` (`confirmEmailFormHandler`) calls
`UserRepository.ConfirmEmail`, which in one transaction deletes the token (checking the
same conditions), sets `email_confirmed_at` (keeping an earlier time), and deletes the
user's other `confirm_email` tokens; then it flashes "Thanks, your email address is
confirmed." and redirects to the timeline, or to `/login` when not logged in. An unknown,
expired, used or outdated token gets `link_expired.html` with a 410: "This link has
expired" and "Confirmation links work for 7 days and only once.", then a Send a new link
button (logged in, unconfirmed), "Your email address is already confirmed." and a link to
Settings (confirmed), or "Log in to get a new link" (anonymous). The page takes its
title, text and action from `linkExpiredPage`, so other email links can reuse it.

**Password reset.** `GET /password/forgot` (`forgotPasswordHandler`,
`forgot_password.html`) asks for the email you signed up with, prefilled from `?email=`.
`forgotPasswordFormHandler` validates the address's format, then checks
`resetIPLimiter` (10 per client IP per hour) and `resetEmailLimiter` (3 per lowercased
email per hour), re-rendering with 429 past either. `UserRepository.GetByEmail` finds
the account (none: "There's no account with this email.", 422), and
`sendPasswordReset` makes a token (purpose `reset_password`, 1-hour expiry) and queues
the `reset_password` email ("Reset your rss password") with the link
`<base URL>/password/reset?token=<token>`. The handler puts the address in the session
as `resetSentTo` and redirects to `/password/forgot/sent`
(`forgotPasswordSentHandler`, `forgot_password_sent.html`): "We sent a link to
<address>. It works for 1 hour.", with a button that posts the address to
`/password/forgot` again; without `resetSentTo` it redirects to the form. Both pages
show the support address for people who can't get into their email. Asking again
doesn't cancel earlier links.

`GET /password/reset` (`resetPasswordHandler`) sets the same no-referrer and no-store
headers as confirmation (`resetToken`), and `TokenRepository.Get` checks the token. A
valid one gets `reset_password.html`: a read-only email field with the token's address
(`autocomplete="username"`), a new-password field with its hint, and the token hidden; the
GET never writes. `POST /password/reset` (`resetPasswordFormHandler`) checks the token
again, then the password with `CheckNewPassword` (422 without using the token).
`UserRepository.ResetPassword` hashes the password, then in one transaction deletes the
token (unexpired, and its address still the account's), sets the new hash, confirms the
email (receiving the link proves the address), and deletes the user's other
`reset_password` tokens and every `user_sessions` row. The handler then `startSession`s
here, queues `password_changed`, flashes "Your password was changed, and you're logged
in." and redirects to the timeline. An unknown, expired, used or outdated token gets
`link_expired.html` (410) with "Reset links work for 1 hour and only once." and a Send a
new link link to `/password/forgot`.

**Changing email.** `GET /settings/email` (`settingsEmailHandler`,
`settings_email.html`) shows the current address, a new-email field, the current
password, and "We'll send a link to your new address. Your email won't change until you
open it." `settingsEmailFormHandler` checks `emailChangeLimiter` (5 per user per hour,
429), then the new address's format, that it isn't the current one ignoring case
("That's already your email address."), and that the password isn't blank, re-rendering
with 422 (never echoing the password). `UserRepository.CheckPassword` verifies the
password (`rss.ErrWrongPassword`: "That password isn't right."), and `GetByEmail` checks
the address is free ("There's already an account with this email."). Then it makes a
token (purpose `change_email`, the **new** address, a 24-hour expiry), queues the
`confirm_new_email` email to the new address with the link
`<base URL>/email-change?token=<token>`, flashes "We sent a link to <new>. Open it to
finish changing your email." and redirects to `/settings`. Nothing changes until the link
is used: you keep logging in with the old address, and asking again doesn't cancel
earlier links.

The link works in any browser. `GET /email-change` (`emailChangeHandler`) sets the
no-referrer and no-store headers and checks the token with `TokenRepository.Get` (for
`change_email` it skips the address check, since the address is the new one). A valid
one gets `confirm_email_change.html`: "Make <new> the email for your rss account?" and a
Change email address button that posts the token. `POST /email-change`
(`emailChangeFormHandler`) calls `UserRepository.ChangeEmail`, which in one transaction
deletes the token, reads the user, sets the email to the new address and
`email_confirmed_at` to now, and deletes the user's other `change_email` tokens. If
another account took the address in the meantime (`23505`, `rss.ErrDuplicateEmail`)
nothing changes and the page re-renders with 409, "That address is taken". On success
the handler queues `email_changed` to the **old** address (logging rather than failing
if it can't), flashes "Your email address is now <new>." and redirects to `/settings`,
or, when not logged in, adds "Log in with it to continue." and redirects to `/login`.
Sessions stay. Confirmation and reset links sent to the old address stop working by
themselves, since `Get` and the repositories only accept them while the account still
has the address they were sent to. An unknown, expired or used token gets
`link_expired.html` (410) with "Links to change your email work for 24 hours and only
once." and a Try again link to `/settings/email`.

**Read:**

1. `internal/server/router.go`: the session group and the `requireAuthentication` group.
2. `internal/server/user.go`: `signupHandler`, `signupFormHandler`, `loginHandler`,
   `loginFormHandler`, `popReturnTo`, `startSession`, `logoutHandler`; `internal/validator/account.go`: `CheckNewAccount`, which checks the
   password with `CheckNewPassword` (`password.go`: length, not the email, not on the
   embedded common-password list, `isCommonPassword`); `internal/ui/templates/login.html`
   and `signup.html` (the field pattern), `Validator.FirstError`, and the password toggle
   in `internal/ui/static/js/app.js`.
3. `internal/server/ratelimit.go` (`rateLimiter`, `Refund`) and `clientip.go`
   (`Server.clientIP`, `ipRateLimitKey`).
4. `internal/psql/users.go`: `Create` (duplicate → `rss.ErrDuplicateEmail`),
   `Authenticate` (`rss.ErrNoAccount`, `rss.ErrWrongPassword`).
5. `internal/password/password.go`: `Hash`, `Verify` (both NFKC-normalize first), the
   `slots` cap.
6. `internal/server/middleware.go` (`authenticate`, `sessionIdleTimeout`,
   `sessionTouchInterval`, `requireAuthentication`, which stores `returnTo`) and
   `helpers.go` (`authenticatedUserID`, `authenticatedSession`, `safeReturnPath`); `internal/psql/user_sessions.go`
   (`UserSessionRepository`: `Create`, `Get`, `Touch`, `Delete`).
7. `cmd/www/application.go`: `newSessionManager`, the session manager's settings
   (`sessionLifetime`, one year).
8. `cmd/www/create_user.go`: `runCreateUser`, `readPassword`; the `db/seed` target in
   `Makefile`.
9. Settings: `internal/server/html.go` (`navData`, `nav`) and
   `internal/ui/templates/partials/nav.html` (`app-nav`, the account entry);
   `internal/server/settings.go` (`settingsHandler`, `settingsNameHandler`,
   `settingsNameFormHandler`, `settingsPasswordFormHandler`, `logOutOthersHandler`,
   `changePasswordWellKnownHandler`, `settingsDeleteFormHandler`);
   `internal/validator/account.go` (`CheckName`); `internal/psql/users.go`
   (`UpdateName`, `ChangePassword`, `Delete`) and `user_sessions.go` (`DeleteOthers`);
   `settings.html`, `settings_name.html`, `settings_password.html`,
   `settings_delete.html`; `home.go` (`homeHandler`'s logged-out flash) and `home.html`;
   `internal/ui/emails/password_changed.tmpl`, `account_deleted.tmpl`.
   The policy pages: `internal/server/policy.go`, `privacy.html`, `terms.html` and
   `partials/policy.html` (`policy-links`, `support-link`).
10. Email: `internal/server/email.go` (`sendEmail`, `absoluteURL`); `internal/mail/queue.go`
    (`Queue`), `runner.go` (`Runner`, `retryDelay`) and `templates.go`
    (`ParseTemplates`, `Render`); `internal/psql/emails.go` (`EmailRepository`); the mail
    runner's wiring and `newMailSender` in `cmd/www/application.go` and `config.go`.
11. Email confirmation: `internal/token/token.go` (`New`, `Hash`);
    `internal/server/confirm.go` (`sendConfirmation`, `confirmNotice`,
    `confirmEmailHandler`, `confirmEmailFormHandler`, `confirmResendHandler`,
    `confirmLinkExpired`, `linkExpiredPage`); `internal/psql/tokens.go`
    (`TokenRepository`: `Create`, `Get`) and `users.go` (`ConfirmEmail`);
    `confirm_email.html`, `link_expired.html`, `partials/confirm_notice.html`;
    `internal/ui/emails/confirm_email.tmpl`.
12. Password reset: `internal/server/password_reset.go` (`sendPasswordReset`,
    `sendPasswordChanged`, `forgotPasswordFormHandler`, `forgotPasswordSentHandler`,
    `resetToken`, `resetPasswordHandler`, `resetPasswordFormHandler`,
    `resetLinkExpired`); `internal/psql/users.go` (`GetByEmail`, `ResetPassword`);
    `forgot_password.html`, `forgot_password_sent.html`, `reset_password.html`;
    `internal/ui/emails/reset_password.tmpl`.
13. Changing email: `internal/server/email_change.go` (`settingsEmailFormHandler`,
    `emailChangeHandler`, `emailChangeFormHandler`, `changeEmailLinkExpired`);
    `internal/psql/users.go` (`CheckPassword`, `ChangeEmail`); `settings_email.html`,
    `confirm_email_change.html`; `internal/ui/emails/confirm_new_email.tmpl`,
    `email_changed.tmpl`.

**Tests:** `internal/server/user_test.go` (`TestSignupHandlerInvitePrefill`,
`TestSignupFormHandlerInviteCode`, `TestSignupFormHandlerTrimsName`,
`TestSignupFormHandlerLogsIn`, `TestSignupFormHandlerDuplicateEmail`,
`TestLoginFormHandlerErrors`, `TestLoginFormHandlerFailureLimit`, `TestReturnTo`,
`TestLoggedInRedirect`), `router_test.go` (`TestSignupRoutes`), `settings_test.go`,
`policy_test.go` (`TestPolicyPages`, `TestPolicyLinks`),
`templates_test.go` (`TestNoSignupLinks`, `TestFormFieldErrorsDescribed`,
`TestFormAutofocus`, `TestSignupHints`, `TestAppNav`), `middleware_test.go`,
`ratelimit_test.go`, `clientip_test.go`; `internal/validator/account_test.go`
(`TestCheckNewAccount`, `TestCheckName`),
`password_test.go` (`TestCheckNewPassword`, `TestCommonPasswordsFile`),
`validator_test.go` (`TestFirstError`);
`cmd/www/config_test.go` (`TestParseConfigInviteCode`, `TestParseConfigMailWorkers`),
`create_user_test.go`; `internal/psql/users_test.go`, `user_sessions_test.go`,
`emails_test.go`, `tokens_test.go` (`TestTokenRepository`, `TestConfirmEmail`,
`TestResetPassword`, `TestChangeEmail`);
`internal/token/token_test.go`; `internal/server/confirm_test.go`
(`TestSignupSendsConfirmation`, `TestConfirmEmailHandler`,
`TestConfirmEmailFormHandler`, `TestConfirmResendHandler`, `TestConfirmNotice`,
`TestSettingsNotConfirmedTag`, `TestTimelineFlash`); `password_reset_test.go`
(`TestForgotPasswordHandler`, `TestForgotPasswordFormHandler`,
`TestForgotPasswordSentHandlerWithoutEmail`, `TestResetPasswordHandler`,
`TestResetPasswordFormHandler`, `TestLoginLinksToReset`); `email_change_test.go`
(`TestSettingsEmailHandler`, `TestSettingsEmailForm`, `TestEmailChangeHandler`,
`TestEmailChangeFormHandler`); `settings_test.go` (`TestSettingsDeleteForm`,
`TestDeleteAccountLogsOut`); `internal/psql/users_test.go` (`TestDeleteUser`, `TestUserRepositoryCheckPassword`); `internal/password/password_test.go`; `internal/server/email_test.go`;
`internal/mail/runner_test.go`, `queue_test.go`, `templates_test.go`
(`TestEmailFooterLinksPrivacy`).

**Why:** "Security" and "Accounts" in decisions.md.

## Search, site lookups and discovery

**Flow.** `GET /search?q=…` only searches the database: sites whose title or host, or
one of whose feeds' title or URL, contains `q`. When `q` looks like a website address,
the page also offers a "Look up example.com" button, which posts to `/lookups`. That handler
normalizes the input (`discovery.ParseInput`), applies the per-user limit, and calls
`LookupRepository.Request`, which returns the site key's lookup, creating it or
re-queuing it if its result is older than its TTL (24 h done, 1 h failed). The handler
then polls the row for up to 3 s and redirects to the site page if the lookup found one.
Otherwise it redirects to `/lookups/{id}`, which reloads itself every 2 s while the lookup
runs, then redirects to the site or says that no feeds were found or the site couldn't
be reached.

Meanwhile a lookup worker claims the row and runs `Discoverer.Discover`: fetch the URL;
if it is itself a feed, that's the result. Otherwise read the HTML page's title,
description and `<link rel="alternate">` feeds, or, if there are none, try
`CommonFeedPaths`. (`ParsePage` also reads a page's own title, Open Graph image and
icon links, for the icon worker and article enrichment; discovery doesn't use them.)
Then fetch up to 15 candidates, 4 at a time, and keep up to 10 that parse. The worker
turns each feed's items into articles (`ingest.Articles`) and saves the site, its feeds
and their articles in one transaction (`DiscoveryRepository.Save`), so a looked-up feed
has articles right away. The site page lists the feeds with subscribe
buttons and offers "Check for new feeds" once the last lookup's TTL has passed.

**Read:**

1. `internal/server/search.go` (`searchHandler`) and `internal/psql/search.go` (`Search`).
2. `internal/discovery/normalize.go`: `ParseInput`, `SiteKey`, `CanonicalFeedURL`.
3. `internal/server/lookups.go`: `lookupCreateHandler`, `waitForLookup`, `lookupHandler`.
4. `internal/psql/lookups.go`: `Request`, then the worker side: `ClaimNext`, `Finish`,
   `Fail`, `ResetStale`.
5. `internal/lookup/lookup.go`: `Runner.Run`, `work`, `process`, `run`.
6. `internal/discovery/discover.go` (`Discover`, `checkCandidates`) and `page.go`
   (`ParsePage`).
7. `internal/feedparse/feedparse.go`: `Sniff`, `Parse`.
8. `internal/psql/discoveries.go` (`Save`), with `upsertSite` in `sites.go` and
   `upsertFeed` in `feeds.go`.
9. `internal/server/sites.go`: `siteHandler`, `canRecheck`.

**Tests:** `internal/server/search_test.go`, `lookups_test.go`, `sites_test.go`;
`internal/lookup/lookup_test.go`; `internal/discovery/*_test.go`;
`internal/feedparse/feedparse_test.go` (fixtures in `testdata/`);
`internal/psql/search_test.go`, `lookups_test.go`, `discoveries_test.go`,
`sites_test.go`.

**Why:** "Feed discovery" in decisions.md.

## Feed ingestion and background refresh

**Flow.** Three things fetch a feed's articles: discovery (the first save, above), the
Refresh button, and the refresh workers. The last two share one path,
`ingest.Refresher.Refresh`:

1. A conditional GET with the stored `ETag` / `Last-Modified`.
2. The outcome is recorded and the next fetch scheduled. After a success (2xx or 304) the
   feed is due again in an hour ±10% (`NextFetch`), or later if the response's
   `Cache-Control` / `Expires` says it stays fresh longer (`freshness`, capped at 24 h).
   - **304:** `RecordNotModified`.
   - **410:** `MarkGone`; not fetched again until a lookup finds the feed again.
   - **Network error, other non-2xx, or not a feed:** `RecordFailure`, with backoff
     (`RetryAt`: 1 h, 2 h, 4 h, … up to 24 h), delayed further by a 429's or 503's
     `Retry-After`.
   - **2xx feed:** parse it, turn the items into articles (`Articles`), and `SaveFetch`
     in one transaction: update the feed's metadata, validators and `next_fetch_at`, then
     upsert the articles and record the success.

Turning items into articles (`ingest.Articles`): each item's identity within its feed is
its ID, else its URL, else a hash of its text; duplicates are dropped and at most 200 are
kept. Each article gets a plain-text excerpt, a `canonical_url` (tracking parameters
removed) for matching copies across feeds, and the MIME type of the item's first audio or
video enclosure or `media:content` (`feedparse.Item.MediaType`, set by `itemMediaType`),
which nothing displays yet. Saving (`saveArticles`) locks the feed row, computes each new
article's `timeline_at` (see Reading), and upserts by `(feed_id, external_id)`: unchanged
articles aren't written, and an existing `published_at` is kept because some feeds bump
it on every edit.

Which feeds the workers fetch: `ClaimDue` takes a due feed that has a subscriber or a
pending refresh request, requested ones first. A request is made when someone views a
stale feed's page (`feedHandler` calls `RequestRefresh`, and the page says "Checking for
new articles…"). The Refresh button instead fetches right away, within 8 s, and reports
the result in a flash message. It refuses gone feeds, feeds fetched successfully (2xx or
304) in the last 5 minutes, and users over the per-user limit.

**Read:**

1. `internal/refresh/refresh.go`: `Runner.work`, `refresh`.
2. `internal/psql/feeds.go`: `ClaimDue`, `RequestRefresh`.
3. `internal/feedparse/feedparse.go`: `itemImage`, `itemMediaType`.
4. `internal/ingest/ingest.go`: `Refresher.Refresh`, `fetch`, `record`, `Articles`,
   `externalID`.
5. `internal/ingest/schedule.go` (`NextFetch`, `RetryAt`, `retryAfter`, `freshness`) and
   `canonical.go` (`CanonicalURL`).
6. `internal/psql/feeds.go`: `SaveFetch`, `RecordFailure`, `RecordNotModified`,
   `MarkGone`.
7. `internal/psql/articles.go`: `saveArticles`, `timelineAt`.
8. `internal/server/feeds.go`: `feedRefreshHandler`, `refreshFlash`, and the
   `RequestRefresh` call in `feedHandler`.

**Tests:** `internal/feedparse/feedparse_test.go` (`TestParseItemImage`,
`TestParseItemMediaType`); `internal/ingest/*_test.go`; `internal/refresh/refresh_test.go`;
`internal/psql/feeds_test.go`, `articles_test.go`; `internal/server/feeds_test.go`.

**Why:** "Feed ingestion" in decisions.md.

## Subscriptions

**Flow.** Subscribe and Unsubscribe buttons on the feed and site pages (and Unsubscribe on
My feeds) post to
`/feeds/{id}/subscribe` or `/unsubscribe` with a `return_to` path; the handler changes
the subscription, flashes a message, and redirects back (`safeReturnPath` keeps it
local). Gone feeds can't be subscribed to but can be unsubscribed from. "My feeds"
(`/feeds`) lists the user's subscriptions grouped by site, with each feed's latest
article date and a warning after 3 failed fetches in a row. Site, feed and search pages
mark the feeds the user subscribes to with one extra query (`SubscribedFeedIDs`).
Subscriptions also decide which feeds are refreshed in the background and what's in the
timeline.

**Export.** "Export subscriptions", under "Your data" in Settings, downloads
`/settings/subscriptions.opml` (`exportOPMLHandler`): every subscription from
`ListByUser`, gone feeds included, as a flat OPML 2.0 list with one `<outline>` per
feed (`type="rss"`, `text` the feed's title or else its URL, `xmlUrl`, and `htmlUrl` the
feed's site URL or else its site's). It's encoded into a buffer first, so a store or
encoding error is an HTML 500 instead of half a file, and sent as
`rss-subscriptions-YYYY-MM-DD.opml` (UTC).

**Import.** "Import subscriptions", under "Your data" in Settings and linked from the
empty timeline, opens `/settings/import` (`settingsImportHandler`): an upload form and
the user's last 5 imports from `ImportRepository.ListByUser`, each with its date,
filename, feed count and "importing…" or how many couldn't be added. The form posts the
file as multipart to `settingsImportFormHandler`, which caps the body with
`http.MaxBytesReader` (not `decodePostForm`), parses the file with `opml.Parse`, and
re-renders the form with the error on the `file` field for no file (422), a file over
1 MiB (413), a file that isn't OPML, has no feeds or has more than 1,000 (422), or a
sixth import within the hour (`importLimiter`, 429 with `Retry-After`). Otherwise it
creates the import, with the file's base name, and redirects to `/settings/imports/{id}`
(`importHandler`; another user's import is a 404). While any feed is pending or running
that page shows "Checked {done} of {total} feeds." and refreshes itself with
`<meta http-equiv="refresh">`; then "Import finished" and a one-line summary. Feeds
finished so far are listed in three groups: Subscribed and Already subscribed (the
outline's title, else the feed's from `Get`, linking to the feed), and Couldn't add,
with the URL, a plain reason for its `Failure` (`importReasons`) and a link to search
for its host.

The import itself is a queue worked in the background. `opml.Parse` reads every `<outline>` with an `xmlUrl`, at any
depth (folders are flattened), in file order and without repeats; it parses non-strictly
and decodes legacy charsets, since older readers' exports are sloppy.
`ImportRepository.Create` stores the import and one pending `import_feeds` row per feed.
The import runner (`importer.Runner`, `-import-workers`) claims one row at a time, oldest
import first, and in `run`: parses the URL with `discovery.ParseInput` (else `failed` /
`invalid_url`); looks up its `CanonicalFeedURL` with `FeedRepository.GetByURL`; for an
unknown or gone feed, runs discovery (an error is `unreachable`, no feeds `not_feed`),
saves what it found with `lookup.ToSave` and `DiscoveryRepository.Save` exactly like a
lookup, and takes the first feed; then records `already_subscribed`, or subscribes and
records `subscribed`. Failures aren't retried; a crashed row is re-queued by
`ResetStale`. Subscribing bumps nothing onto the timeline: the backlog keeps its
published dates.

**Read:**

1. `internal/server/feeds.go`: `feedSubscribeHandler`, `feedUnsubscribeHandler`,
   `subscriptionTarget`, `redirectWithFlash`, then `feedsHandler`.
2. `internal/server/helpers.go`: `returnForm`, `safeReturnPath`.
3. `internal/psql/subscriptions.go`: `Subscribe`, `Unsubscribe`, `SubscribedFeedIDs`,
   `ListByUser`.
4. Templates: `feed.html`, `site.html`, `feeds.html`.
5. Export: `internal/server/settings.go` (`opmlDoc`, `exportOPMLHandler`); the "Your
   data" section of `settings.html`.
6. Import: `internal/server/import.go` (`settingsImportHandler`,
   `settingsImportFormHandler`, `importFilename`, `importHandler`, `importSummary`);
   templates `settings_import.html`, `import.html`; `internal/opml/opml.go` (`Parse`);
   `internal/psql/imports.go` (`ImportRepository`); `internal/importer/importer.go`
   (`Runner.process`, `run`, `discover`); `internal/lookup/lookup.go` (`ToSave`);
   `FeedRepository.GetByURL` in `internal/psql/feeds.go`.

**Tests:** `internal/server/feeds_test.go`, `sites_test.go`, `search_test.go`,
`settings_test.go` (`TestExportOPML`); `internal/psql/subscriptions_test.go`; import:
`internal/server/import_test.go` (`TestSettingsImportForm`, `TestSettingsImportHandler`,
`TestImportHandler`), `internal/opml/opml_test.go`, `internal/psql/imports_test.go`,
`internal/importer/importer_test.go`.

**Why:** "Subscriptions" and "Accounts" in decisions.md.

## Reading: timeline, article pages and read state

**Timeline** (`/` when logged in). `homeHandler` asks `ListTimeline` for 31 entries to
know whether there's a next page, and shows 30. `ListTimeline` joins the user's
subscriptions to their feeds' articles, collapses copies of one article (same
`canonical_url`) into the earliest to arrive, orders by `(timeline_at, id)` descending,
and pages with a keyset cursor: `?before=<time>_<id>` (`encodeCursor`, `decodeCursor`).
`fillAlsoIn` then lists the other subscribed feeds that carry each entry. Feed pages work
the same way with `ListByFeed`, but in publication order and without collapsing copies.
List queries never load `summary` or `content`.

**Timeline position.** `timelineAt` sets `timeline_at` once, when an article is first
saved. *News* (undated, or published since a day before the feed's previous successful
fetch) goes at the save time, 1 µs apart, newest-published first. *Backlog* (every dated
item on a feed's first save, or older items appearing later) goes at its published date,
or now if that's in the future. So new items always land on top, and an imported archive
doesn't.

**Article page.** `articleHandler` records the read, then renders the content (else the
summary) through `sanitize.HTML`, with relative URLs resolved against the article's URL
(else the feed's). Plain text (no block-level markup) is split into paragraphs at blank
lines, with `<br>` for single line breaks, and bare http(s) URLs become links. The
article's card image (`image_url`) shows above the text as a lead image unless
`sanitize.ShowsImage` finds the content already shows it: the content starts with an
image, the block after its first paragraph is one, or any image in it is the same file
(ignoring host case, `www.`, the query and WordPress size suffixes). Wide tables scroll
inside the column. YouTube and Vimeo player iframes (`/embed/{id}` on a YouTube host,
`player.vimeo.com/video/{id}`) become a `figure.embed` preview linking to the video,
with YouTube's thumbnail; clicking it makes `app.js` swap in the player
(`youtube-nocookie.com` or `player.vimeo.com`, sandboxed, autoplaying). Without
JavaScript the link opens the video in a new tab. Any other iframe becomes an "Open
embedded content" link.

**Read state.** An article is read when its page is opened, or when the reader clicks
through to the original from a row: the link has `ping="/articles/{id}/read"`, which
`app.js` replaces with `navigator.sendBeacon` because Firefox ignores `ping`. List pages
ask `ReadArticleIDs` which rows are read (matching any copy with the same
`canonical_url`) and the `article-row` partial marks them (tinted card, dimmed text and image).

**Infinite scroll.** `app.js` watches the "Older articles" link; when it nears the
viewport it fetches that page, appends its rows, and swaps in the new pager. It keeps the
address bar on the topmost visible row (`history.replaceState`), so reload and Back land
nearby. Without JavaScript the link just works.

**Read:**

1. `internal/server/home.go`: `homeHandler`; `cursor.go`.
2. `internal/psql/articles.go`: `ListTimeline`, `fillAlsoIn`, `ListByFeed`,
   `articleListColumns`, then `timelineAt`.
3. `internal/server/articles.go`: `articleHandler`, `articleReadHandler`, `articleRow`,
   `fillRead`.
4. `internal/psql/reads.go`: `MarkRead`, `ReadArticleIDs`.
5. `internal/sanitize/sanitize.go`: `HTML`, `prepare`, `rewrite`, `reflow`, `linkify`,
   `videoEmbed`, `embedLink`, `newPolicy`, then `ShowsImage` and `imageKey`.
6. Templates: `timeline.html`, `article.html`, `partials/article_row.html`.
7. `internal/ui/static/js/app.js`.
8. Also: `rss.Article.DisplayTitle` (untitled articles) and `FeedSortAt` in
   `internal/rss/rss.go`.

**Tests:** `internal/server/home_test.go`, `articles_test.go`, `cursor_test.go`,
`scroll_test.go`; `internal/psql/articles_test.go`, `reads_test.go`;
`internal/sanitize/sanitize_test.go`.

**Why:** "Reading" and "Design" in decisions.md.

## Saved articles

**Flow.** Every article row (timeline and feed pages) and the article page has a Save
button with a bookmark icon, in the `article-actions` group: on rows it's right-aligned
under the card (the row's `article-foot`, with "Also in" on the left), on the article
page under the title. The button is the `save-button` partial, a form posting to
`/articles/{id}/save`, or `/unsave` when the article is saved, with a `return_to` of the
page's request URI. `articleSaveHandler` and `articleUnsaveHandler` (both `saveAction`)
call `SaveRepository.Save` or `Unsave` and redirect to `return_to` with
`#article-{id}` appended, so without JavaScript the page comes back at the same row.
Saving is quiet: no flash, and it doesn't mark the article read. With JavaScript,
`app.js` posts the form with `fetch` and flips the button in place; the accessible name
stays "Save" and `aria-pressed` carries the state, from which `main.css` shows the
outline or the filled icon.

**Copies.** Saving covers every copy of an article (same non-empty `canonical_url`),
like read state. `Save` inserts nothing when the user already saved a copy, and reports
`rss.ErrNoRecord` for an unknown article; `Unsave` deletes the saves of the article and
of every copy. List pages ask `SavedArticleIDs` which rows are saved (`fillSaved`), with
the same query shape as `ReadArticleIDs`; the article page asks it for its one article.

**Saved page** (`/saved`, "Saved" in the nav, after Home). `savedHandler` is modelled on
the timeline: it asks `SaveRepository.ListSaved` for 31 articles and shows 30, newest
saved first, paged with a `?before=` cursor whose time is the `saved_at` (infinite
scroll with JavaScript, "Older saved articles" without). `ListSaved` joins `saves` to
`articles` and `feeds` with keyset paging on `(saved_at, article_id)`, served by
`saves_user_saved_at_idx`. Each row is an `articleRow` with its feed, `Saved: true` and
`fillRead`'s read marker. Unsaving from the page comes back to `/saved`; with
JavaScript the row stays (its button flips) until the page is reloaded, so an
accidental unsave can be undone in place. There's no count anywhere.

**Read:**

1. `internal/server/articles.go`: `articleSaveHandler`, `articleUnsaveHandler`,
   `saveAction`, `withFragment`, then `articleRow` and `fillSaved`; `articleHandler`'s
   `Saved`; `internal/server/saved.go`: `savedHandler`.
2. `internal/server/helpers.go`: `returnForm`, `safeReturnPath`.
3. `internal/psql/saves.go`: `Save`, `Unsave`, `SavedArticleIDs`, `ListSaved`.
4. Templates: `partials/save_button.html` (`save-button`, `article-actions`),
   `partials/article_row.html`, `article.html`, `saved.html`, `partials/nav.html`; `icon-bookmark` and
   `icon-bookmark-fill` in `partials/icons.html`.
5. `internal/ui/static/js/app.js`: "Save buttons".

**Tests:** `internal/server/articles_test.go` (`TestArticleSaveHandler`,
`TestArticleUnsaveHandler`, `TestArticleHandlerSaved`), `saved_test.go`
(`TestSavedHandler`), `home_test.go`, `feeds_test.go` (`TestFeedHandlerSavedRows`),
`scroll_test.go` (`TestSaveButtonMarkup`, `TestInfiniteScrollMarkup`),
`templates_test.go` (`TestAppNav`); `internal/psql/saves_test.go` (`TestListSaved`).

**Why:** "Reading" in decisions.md.

## Site icons

**Flow.** The site icon runner (`siteicon.Runner`, `-icon-workers`) claims one site at a
time with `SiteRepository.ClaimIconCheck`: a site whose `icon_next_check_at` has passed
and that has never been checked (`icon_checked_at IS NULL`, so a site created by a
lookup or an import gets its first check soon, subscribed or not) or has a subscribed
feed. The claim moves `icon_next_check_at` 5 minutes ahead as a lease, and returns the
current icon's `source_url` and validators.

`Runner.check` fetches the site's home page with the shared `fetch.Client`; an error or
a non-2xx keeps the icon (`KeepIcon`, not checked) and retries in a day. Otherwise it
reads the icons the page declares (`discovery.ParsePage(...).Icons`; a non-HTML body
declares none) and builds the candidates (`candidates`), deduplicated and at most four:
apple-touch icons, then `rel="icon"` links that aren't SVG, each largest declared size
first and unknown sizes last; then `/apple-touch-icon.png` and `/favicon.ico` on the
page's origin. An icon declaring a size under 32 isn't downloaded.

Each candidate is downloaded with a second `fetch.Client` capped at 256 KiB. The current
icon's URL gets `If-None-Match` / `If-Modified-Since`, and a 304 keeps it (`KeepIcon`,
checked). `usable` recognizes PNG, JPEG and GIF from the bytes (`http.DetectContentType`)
and reads their size with `image.DecodeConfig`; an ICO by its signature, its size from
its directory (`icoSize`, largest entry). The first icon at least 32 px on its shorter
side and at most twice as long as wide is stored (`SaveIcon`, with its SHA-256 `hash`,
`ETag` and `Last-Modified`). If none is usable, the icon is removed (`RemoveIcon`),
unless a candidate failed with a network error, a 429 or a 5xx: then it's kept, not
checked, and retried in a day. A check that reached the site schedules the next one in
30 days ±10% (`nextCheck`). Each check logs one `site icon checked` line with the
outcome (`saved`, `unchanged`, `removed` or `failed`).

**Serving and showing.** `GET /sites/{id}/icon` (`siteIconHandler`, no session) serves
the stored bytes (`SiteRepository.GetIcon`) with the stored `Content-Type`, an `ETag` of
the hash (a matching `If-None-Match` is a 304) and a CSP of `default-src 'none';
sandbox`; it's cached for a year when `?v=` is the current hash, else for an hour. A site
without an icon is a 404.

Pages build icon URLs with `siteIconURLs`: one `IconHashes` query for the page's
deduplicated site IDs, giving `/sites/{id}/icon?v={hash}` for each site with an icon.
Article rows get theirs from `fillIcons` (`articleRow.Icon`); the feed, article, site,
My feeds and search pages pass an `Icons` map. Templates call the `avatar` partial with
`(avatar siteID name iconURL)`: an `<img>` of the icon on a white tile, else the letter on
its tint. Icons are decoration: if `IconHashes` fails, `pageIcons` logs the error and the
page shows letters.

**Read:**

1. `internal/siteicon/siteicon.go`: `Runner.work`, `check`, `find`, `candidates`,
   `usable`, `nextCheck`; `ico.go`: `icoSize`.
2. `internal/psql/sites.go`: `ClaimIconCheck`, `SaveIcon`, `RemoveIcon`, `KeepIcon`,
   `GetIcon`, `IconHashes`.
3. `internal/discovery/page.go`: `Icon`, `ParsePage`.
4. `cmd/www/application.go`: the runner and its two `fetch.Client`s.
5. `internal/server/sites.go`: `siteIconHandler`, `siteIconURLs`, `pageIcons`;
   `articles.go`: `fillIcons`; `html.go`: `avatar`.
6. `internal/ui/templates/partials/avatar.html`; `.avatar` and `.avatar-icon` in
   `main.css`.

**Tests:** `internal/siteicon/siteicon_test.go` (an `httptest` site with generated PNG,
JPEG, GIF and ICO fixtures), `ico_test.go`; `internal/psql/sites_test.go`
(`TestSiteRepositoryClaimIconCheck`, `TestSiteRepositoryIcons`); `cmd/www/config_test.go`
(`TestParseConfigIconWorkers`); `internal/server/sites_test.go` (`TestSiteIconHandler`),
the `…SiteIcons` page tests (built on `testSiteIcons` in `testutils_test.go`) and
`templates_test.go` (`TestAvatarPartial`).

**Why:** "Design" in decisions.md.

## Adding a feature

The usual path for a new page or action, following the conventions in CLAUDE.md:

1. **Schema:** edit `migrations/00001_init.sql` and run `make db/reset`.
2. **Domain type:** add or extend a type in `internal/rss`, and a sentinel error in
   `errors.go` if handlers need to tell a case apart.
3. **Repository:** a method in `internal/psql` with a DB test.
4. **Interface:** add the method to the store interface in `internal/server/server.go`
   (or a new interface, a `Services` field, the compile-time check, and the wiring in
   `cmd/www/application.go`), and a function field to the fake in `fakes_test.go`.
5. **Handler and route:** a method on `*Server`, registered in `router.go` (see "Code
   conventions" in CLAUDE.md for forms, flash messages and error pages).
6. **Template:** a page in `internal/ui/templates`, with shared parts in `partials/`.
7. **Tests:** handler tests with fakes for the new behavior, and DB tests for the
   repository method.
8. **Docs:** see "Docs" in CLAUDE.md for which files to update.
