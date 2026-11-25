# Features

How each feature works and where to start reading.
Read [architecture.md](architecture.md) first for the overall shape and the glossary.

Each section has the flow (what happens, and the parts that aren't obvious from the code), where to start reading (a few entry points, in the order a request or job reaches them; follow the calls from there), the test files that pin the behavior, and where the reasons are in [decisions.md](decisions.md).
User-facing strings, CSS classes and every helper's name live in the code and templates, and the tests check them; this file doesn't repeat them.

## Accounts and sessions

**Signup and login.** Signup needs a single-use invite (an `invites` row), and no page links to it: people arrive through `/signup?invite=<token>`, the link the admin's invite email or its Invites page gives.
`signupHandler` looks the invite up by the token's hash (`signupInvite`) and, if it's usable, shows the form with the token in a hidden field; a bound invite (one with an email, sent to a confirmed waitlist entry) fills in the entry's name and its email, locked (`readonly`).
Without a token the page says the app is invite-only and links to the waitlist; an expired, used, revoked or unknown invite gets a page that says which, with no form (410; `renderInviteUnusable`).
The page with a token, and the POST, send `Referrer-Policy: no-referrer` and `Cache-Control: no-store`, like the email links.
`signupFormHandler` checks the per-IP limit, then the invite again, takes a bound invite's email whatever was submitted, checks the name, email and password with `validator.CheckNewAccount`, and calls `UserRepository.CreateFromInvite`.
That hashes the password, then in one transaction redeems the invite with a conditional `UPDATE` (so of two signups racing on one invite, only one gets the row; the other gets `rss.ErrInviteUnusable` and the "already been used" page, 422), inserts the user with the default timelines (confirmed if the invite is bound), records the account on the invite, copies the sections of the invite's waitlist entry to the account (`users.sections`, for Discover's order, #643), and deletes the account's waitlist entry, returning its sites answer.
A taken email rolls it all back, so the invite stays usable, and gets an error on the email field with links to log in or reset the password.
Once the session starts, `followWaitlistSites` follows the sites that answer named (#640): it splits it with `rss.SplitSitesAnswer`, keeps the first 10 parts, and `SiteRepository.MatchForSignup` matches each to a site (an address by its site key, anything else by a title exactly one site has, ignoring case) and to the feed to follow, the site's only feed that isn't gone or else its one such feed in Discover's catalog.
It subscribes to each of those feeds and puts a flash naming what it followed, the sites with several feeds to choose from in Discover and what it didn't find; matching or subscribing errors are only logged, so signup never fails on them.
Home doesn't show the confirm-email notice beside a flash, so it waits for the next load.
A bound invite's account starts confirmed, so only an open invite's signup sends the confirmation email.
Login checks the per-IP limit, then counts the attempt against the submitted email *before* checking the password (refunded unless the password was wrong), so parallel guesses can't slip past the limit.
`Authenticate` tells an unknown email (`rss.ErrNoAccount`) from a wrong password (`rss.ErrWrongPassword`), and each gets its own field error; the login page links to password reset, with the typed address.

On a successful login or signup, `startSession` renews the session token, creates a `user_sessions` row for this device (deleting the user's rows idle for 90 days) and stores its ID in the session.
The handler then redirects to `popReturnTo`: `requireAuthentication` remembers an anonymous GET or HEAD's URI as `returnTo` before sending it to `/login`, and `safeReturnPath` keeps it local.
Other methods aren't remembered, since replaying one as a GET would give a 405.
A logged-in user who opens `/login` or `/signup` is sent to the timeline.

**Sessions.** On every later request, `authenticate` loads the device's `user_sessions` row with its user in one query.
A missing row, or one unused for 90 days (`rss.SessionIdleTimeout`; the row is deleted), makes the request anonymous; otherwise it updates `last_seen_at` if it's over a day old and puts the user and session in the request context (`s.authenticatedUserID(r)`, `s.authenticatedSession(r)`).
Logging out deletes the row and renews the token.
A login lasts at most a year (scs's `Lifetime`).

Both forms follow the field pattern in CLAUDE.md, and signup shows the name and password hints all the time.
With JavaScript, their buttons show they're working while the password is hashed ("Logging in…", "Signing up…"; busy forms (`form[data-busy]`, `busy-forms.ts`)).
The other way to make an account is `admin create-user` (`runCreateUser` in `cmd/admin`), for your local account: it reads the password from stdin and uses the same checks and `Create`; `make db/seed` runs it from `.env`, and `make db/reset` runs `db/seed` when `RSS_DEV_EMAIL` is set.

**Invites.** Invites are made in the admin: in waves from the waitlist, or by hand with Invites → New (`newInviteFormHandler` in `internal/admin/invites.go`, see "Invites in the admin" under Admin), which stores one with a new token (`token.New`; only its SHA-256 is stored) and shows its link once, built from `-app-url`, the reader's URL.
The Invites page shows each invite's status (`rss.Invite.Status`), and Revoke stops an unused one working.
Creating and revoking are recorded in the admin's audit log (see "Audit log" under Admin).

**Settings.** The nav's account entry opens the Settings home (`/settings`, `settingsHandler`): the account as its first row (avatar, name and email), then Reading, Your data and Help, each with a one-line summary, then Log out.
Each opens its category page (`/settings/account`, `/reading`, `/data`, `/help`), and a form that saves redirects with a flash to the category it belongs to.
Account (`settingsAccountHandler`) has rows that each open a small page with one form (Name, Email, Password), "Log them out" for other devices, and Delete account at its foot.
Name trims and checks the name (`CheckName`, also used by signup).
Reading (`settingsReadingHandler`) is itself the form, two sets of radio buttons saved together: Theme offers System, Light or Dark, and Article layout offers Headlines or Cards.
`UserRepository.UpdateReading` stores them in `users.theme` and `users.layout`, which `authenticate` loads with the session, so every logged-in page then renders them as `data-theme` and `data-layout` on `<html>` (see "Themes" and "Article layouts" under "Frontend" in [architecture.md](architecture.md)).
The layout changes how article rows are drawn on every page that lists articles, not what they hold or do.
The Settings home's account row and Account's Email row show "Not confirmed" until the address is confirmed (see "Changing email").
Password asks for the current and the new password; `UserRepository.ChangePassword` verifies the current one before starting a transaction (Argon2 is slow), then in one transaction stores the new hash and deletes the user's other sessions, keeping this device's.
The handler queues the `password_changed` email, logging rather than failing if it can't.
`/.well-known/change-password` redirects there, for password managers.
Logging out other devices needs no password, since it can only protect.
Your data has the subscriptions import and export (see "Subscriptions"); Help has the support address (`Config.SupportEmail`, left out when unset), the policy pages and, for allowed accounts, debug mode.

**Deleting an account.** `/settings/delete` says what goes (the account, subscriptions, reading history) and what stays (feeds and articles, which are shared), offers the OPML export first, and asks for the password.
`settingsDeleteFormHandler` checks the per-user limit, then `UserRepository.Delete` verifies the password outside any transaction and deletes the `users` row, which cascades to the user's subscriptions, reads, sessions and tokens.
Deletion is immediate.
The handler queues the `account_deleted` email to the old address (the outbox has no foreign key to users), ends the session and redirects to the logged-out home page with a flash.

**Privacy policy and terms.** `/privacy` and `/terms` are logged-out pages whose text is HTML in `privacy.html` and `terms.html`, with a "Last updated" date to change with the text.
They're linked from signup, login, the logged-out home page, Settings and every email's footer.

**Email.** A handler that sends email calls `s.sendEmail(ctx, to, name, data)`, which renders `internal/ui/emails/<name>.tmpl` (links built with `s.absoluteURL` from the base URL, never from the request) and queues it with `mail.Queue.Enqueue`.
That inserts it into the `emails` outbox and wakes this process's `mail.Runner`, so the page doesn't wait on the provider.
Over the per-address cap, the email isn't queued and `sendEmail` logs a warning and returns nil.
A runner worker claims the email, sends it and records the outcome: sent, retried later (`retryDelay`), or given up.
The cap, retries, lease and retention are under "Background work" and "Security measures" in [architecture.md](architecture.md).

**Email confirmation.** Confirmation is soft: nothing requires it yet, except sharing a timeline.
After signup, `sendConfirmation` has `accounts.Emails.Confirmation` make a token with `token.New` (only its SHA-256 is stored), saves it (purpose `confirm_email`, the address it's sent to, 7 days) and queues the link `<base URL>/confirm-email?token=<token>`.
If that fails, signup still succeeds and the error is logged.
Asking again doesn't cancel earlier links, since emails arrive out of order.
Until the address is confirmed, the timeline and Settings show a notice with a Resend button (`confirmResendHandler`, 3 per user per hour).

The link works in any browser, logged in or not.
`GET /confirm-email` sets `Referrer-Policy: no-referrer` and `Cache-Control: no-store`, and `TokenRepository.Get` only returns an unexpired token whose address is still the account's.
A valid token gets a page with a Confirm button that posts it back: the GET never writes, so a mail scanner that opens the link can't confirm.
`UserRepository.ConfirmEmail` then, in one transaction, deletes the token (checking the same conditions), sets `email_confirmed_at` (keeping an earlier time) and deletes the user's other confirmation tokens.
Confirming while logged in as that account redirects Home with a flash.
Anyone else (logged out, or logged in as another account) is redirected to `GET /confirm-email/done` (`confirmEmailDoneHandler`), a static page saying the address is confirmed, with a link to log in or, if logged in, to Home; confirming never logs anyone in.
An unknown, expired, used or outdated token gets the expired-link page (410), built by `linkExpiredPage`, which the other email links reuse.

**Password reset.** `/password/forgot` asks for the account's email (prefilled from `?email=`), checks the per-IP and per-email limits, finds the account (an unknown email gets a field error), and `sendPasswordReset` queues a link with a 1-hour token (`accounts.Emails.PasswordReset`).
The address is kept in the session as `resetSentTo` for the "sent" page, which can send the link again.
Both pages show the support address for people who can't get into their email.
`GET /password/reset` checks the token like confirmation (no-referrer, no-store, no writes) and shows a new-password form with the token's address read-only.
`UserRepository.ResetPassword` hashes the password, then in one transaction deletes the token, sets the hash, confirms the email (receiving the link proves the address), and deletes the user's other reset tokens and every session.
The handler then starts a session here and queues `password_changed`.

**Changing email.** `/settings/email` asks for the new address and the current password, checks the per-user limit, that the address is new and free, and the password (`CheckPassword`), then sends a 24-hour link to the **new** address.
Nothing changes until the link is used.
`GET /email-change` checks the token (for `change_email` the address check is skipped, since the address is the new one) and shows a Change button; `UserRepository.ChangeEmail` in one transaction deletes the token, sets the new address as confirmed and deletes the user's other change tokens.
If another account took the address in the meantime (`rss.ErrDuplicateEmail`), nothing changes and the page re-renders with 409.
On success the old address gets `email_changed`; sessions stay.
Links sent to the old address stop working by themselves, since tokens are only accepted while the account still has the address they were sent to.

**Start at:**

1. `internal/server/router.go`: the session group and the `requireAuthentication` group.
2. `internal/server/user.go` (signup, login, `startSession`, `popReturnTo`), `internal/validator/account.go` (`CheckNewAccount`), and for invites `internal/psql/invites.go`, `UserRepository.CreateFromInvite` and `internal/admin/invites.go`; following the waitlist's sites, `followWaitlistSites` and `SiteRepository.MatchForSignup`.
3. `internal/server/middleware.go` (`authenticate`, `requireAuthentication`) and `internal/psql/user_sessions.go`.
4. `internal/server/settings.go`, then `confirm.go`, `password_reset.go` and `email_change.go`, with `internal/psql/users.go` and `tokens.go`.
5. Email: `internal/server/email.go` (`sendEmail`), then `internal/mail` (`Queue`, `Runner`, `ParseTemplates`).
6. `internal/password/password.go`, `internal/token/token.go`, and `cmd/www/create_user.go`.

**Tests:** in `internal/server`, `user_test.go`, `settings_test.go`, `confirm_test.go`, `password_reset_test.go`, `email_change_test.go`, `email_test.go`, `policy_test.go`, `middleware_test.go`, `ratelimit_test.go`, `clientip_test.go`, `router_test.go` and `templates_test.go` (the field pattern and the nav); `web/shared/lib/busy-forms.test.ts`; `internal/psql/users_test.go`, `invites_test.go`, `user_sessions_test.go`, `tokens_test.go`, `emails_test.go`, `sites_test.go` (`TestSiteRepositoryMatchForSignup`); `internal/validator`, `internal/password`, `internal/token` and `internal/mail`; `cmd/www/config_test.go`; `cmd/admin/create_user_test.go` and `invite_test.go`.

**Why:** "Security" and "Accounts" in decisions.md, and for following the waitlist's sites "First run and discovery" and "Waitlist and invites".

## Waitlist

**Joining.** The logged-out home page (`/`, `homeHandler`) is the landing page, and it hosts the waitlist form, a partial (`waitlist-form`) so the owner's landing page can take it later.
`/` stays one address for two pages: the timeline when logged in, the landing page when not; neither is kept by any cache, since a cache can't tell them apart (`privateCache`: `no-store` on navigations, as for every logged-in page).
A `?ref=` on the page that passes `waitlistSource` is carried in a hidden field and stored as the entry's source.
`POST /waitlist` (`waitlistJoinHandler`) sends a logged-in user home, then answers a filled honeypot field as if it worked (storing and sending nothing), then checks the per-IP limit, the fields and whether the address has an account.
Invalid or refused, it re-renders the page that hosts the form (`renderWaitlistHost`) with 422 (429 when limited), so moving the form means changing only that function.
`WaitlistRepository.Join` inserts the entry unconfirmed in one statement: an unconfirmed entry with the same address is refreshed with the new details and token, so only the newest link works, and a confirmed one is `rss.ErrDuplicateEmail`.
The optional sites answer is kept as typed, for the owner to send to the seed list from the admin (below) and to check discovery against before inviting; nothing reads it when someone joins.
The optional "What do you like to read?" offers Discover's sections as check pills, like the timeline form's tags (`DiscoverRepository.ListSections`, read for every logged-out `/` and every join); the handler keeps only the IDs of sections that exist, once each, in the sections' order (`chosenSections`), and stores them on the entry, replaced like the other answers when joining again.
The handler then queues `waitlist_confirm`, keeps the address in the session as `waitlistJoinedTo` and redirects to `/waitlist/joined`.
Log in links to the form for people who arrive there without an account.

**Confirming and leaving.** Each entry has one link token (`token.New`; only its SHA-256 is stored), carried by both of the email's links.
It doesn't expire, since the leave link must keep working in later emails (the invite), and it's replaced only by joining again while unconfirmed, or by an invite, whose email carries a new one.
`GET /waitlist/confirm` and `GET /waitlist/leave` send `Referrer-Policy: no-referrer` and `Cache-Control: no-store`, look the entry up and show a page whose button posts the token back, so a mail scanner that opens a link changes nothing.
The POSTs confirm (keeping the first `confirmed_at`) or delete the entry and redirect to `/` with a flash.
An unknown token gets the expired-link page (410).
Signing up with an invite deletes the entry (see "Signup and login" under "Accounts and sessions"); leaving the list first keeps a bound invite's email but unlinks it from the entry.

**Inviting from the admin.** Invites go out in waves from the admin's Waitlist (`/waitlist`, `waitlistHandler`): confirmed entries, oldest first, 100 a page, on three tabs, Waiting (no pending invite, so an expired or revoked one counts as waiting again), Invited and Unconfirmed.
On Waiting you tick people, with `select.ts` adding select-all and shift-click ranges and the bar at the foot saying how many, or ask for "the oldest N", and either posts to `POST /waitlist/review` (`waitlistReviewHandler`), which acts on nothing.
The review lists who's still waiting among them, a label ("Wave {date}") and an expiry (14 days), and checks the wave against the email limits (`newSendCheck`, `MailLimits.Available`): within them it offers "Send {n} invites"; past 80% of the day it warns how many emails the day keeps for account emails; over what fits it says how many fit and offers only "Send the oldest {k}", whose IDs are the only ones the form carries.
`POST /waitlist/invite` (`waitlistInviteHandler`) makes each invite's token and the entry's new link token, renders the `invite` email with both (`newWave`), and `InviteRepository.SendWave` stores the invites, replaces the entries' tokens, queues the emails and records one `invite.wave` audit entry in one transaction, after checking the limits again under an advisory lock and that every entry is still waiting.
If either check fails, nothing is sent and the review comes back (409) with the numbers as they are now; otherwise it redirects to the Invited tab with a flash, and `www`'s mail runner sends the emails.

**Sending their sites to the seed list.** The Waiting tab's selection has a second button, Add their sites to the seed list, which posts the same IDs to `POST /seed/add/waitlist` (`seedAddWaitlistHandler`, nothing selected goes back with a flash).
`WaitlistRepository.SitesAnswers` reads the selected entries' non-empty answers, oldest first, and `splitWaitlistSites` splits each with `rss.SplitSitesAnswer` (commas, semicolons and new lines, the split signup will use too), keeping the parts `discovery.ParseInput` accepts, each site key once with its first spelling, as the lines of the seed list's Add sites form (see "Seed list and Discover's catalog"), with the source "waitlist".
The other parts ("Gopher Days") are listed under the text area to look up by hand; the form is only filled in, and nothing is added until the owner previews and adds, as with any paste.

**Invites in the admin.** `GET /invites` (`invitesHandler`) shows each wave's summary (`InviteRepository.Waves`: sent, signed up, and accounts with a session seen 7 or more days after signing up), then every invite, newest first, 100 a page by the last invite's ID (`List`).
Each row offers what its status allows, posting to `POST /invites/{id}/resend` (a bound invite, pending or expired) or `POST /invites/{id}/revoke` (a pending one), which redirect back with a flash.
Resending (`inviteResendHandler`) renders a new email with a new token, expiring 14 days from now, through the same `newInviteEmail` as a wave, and `InviteRepository.Resend` replaces the invite's token and expiry, gives its entry a new link token if it still has one, and queues the email, checked against the limits under the same lock; the old link stops working.
`GET, POST /invites/new` (`newInviteFormHandler`) invites someone by hand: with an email, it must be a confirmed waitlist entry with no pending invite, and `SendInvite` sends it like a wave of one; without, `Create` makes an open invite.
Either way the page then shows the link once, since only its hash is stored.
The Waitlist and review tables show each entry's sections by name, read with the entry, so a section dropped since is left out.

**Start at:**

1. `internal/server/home.go` (`homeHandler`) and `internal/server/waitlist.go` (`waitlistJoinHandler`, `renderWaitlistHost`, `waitlistActionHandler`).
2. `internal/psql/waitlist.go` (`Join`, and the admin's `Counts`, `List`, `ListWaiting`, `Oldest` and `SitesAnswers`).
3. `internal/ui/templates/partials/waitlist.html` and `internal/ui/emails/waitlist_confirm.tmpl`.
4. The admin's side: `internal/admin/waitlist.go`, `invites.go`, `limits.go` and `seed_add.go` (`seedAddWaitlistHandler`), `internal/psql/invites.go` (`SendWave`, `sendBound`, `Resend`, `Waves`) and `emails.go` (`enqueue`, `Usage`), `internal/ui/admin/waitlist.html`, `waitlist_review.html`, `invites.html` and `invite_new.html`, and `internal/ui/emails/invite.tmpl`.

**Tests:** `internal/server/waitlist_test.go` and `middleware_test.go` (`TestCacheControl`); `internal/psql/waitlist_test.go` (`TestWaitlistAdminQueries`, `TestWaitlistRepositorySitesAnswers`), `users_test.go` (`TestCreateFromInvite`), `invites_test.go` (`TestSendWave`, `TestInviteWaves`, `TestResend`, `TestSendInvite`) and `emails_test.go` (`TestEmailCounts`); `internal/admin/waitlist_test.go`, `invites_test.go`, `limits_test.go` and `seed_add_test.go` (`TestSeedAddWaitlist`); `internal/rss/rss_test.go` (`TestSplitSitesAnswer`); `web/admin/features/select.test.ts`.

**Why:** "Waitlist and invites" in decisions.md.

## Discover, site lookups and discovery

**Flow.** Discover (`GET /discover`, the fourth tab, with the magnifier) shows Discover's catalog by section when there's no query, and is the whole app's search when there is one (`?q=…`; see "Discover's search" below).
The box submits from the magnifier at its start, with Enter, or with a phone keyboard's Search key; there's no Search button.
The box is the search-box partial (`partials/search_box.html`, `searchBox`) that My feeds and Saved use too: while it has text, a clear button at its end links to the page without the query, and with JavaScript `search-clear.ts` shows and hides it as the reader types and clears the box in place on pages that update as you type (Discover, My feeds), sending the same `input` event as typing, so they react as they do to an emptied box; Escape in a box with text does the same. On Saved, whose results change only on Enter, it reloads `/saved` when the page shows a query's results. The browser's own clear button is hidden, so every browser shows this one.
With JavaScript, suggestions update as you type: live search (`live-search.ts`) fetches the same page with `q` alone once typing pauses, swaps in its results container (`data-search-results`) and announces its `data-announce`, with the URL following the query; emptying the box fetches the no-query page at once, so the sections come back without the feeds followed meanwhile.
Enter (or the magnifier) submits the form with its hidden `scope`, the tab shown, and gets the results (see "Discover's search" below).
While a search is slower than `--delay-busy`, a spinner takes the place of the box's search icon and the stale results dim until the new ones arrive.
When `q` is a website address (`discovery.ParseInput`, which drops whitespace next to `.`, `/` or `:` so `npr. org` counts), the site search also matches its site key exactly and ranks that site first, and the suggestions and the results start with a Look up row, which posts the cleaned-up address to `/lookups`, unless the address is one of the feeds of a site the search found (`knownAddress`, which ignores the scheme and `www.`), or that site's home page once its lookup has finished there (`homePageChecked`, which reads the site key's lookup).
With JavaScript, Look up and the site page's "Check {host} for new feeds" show they're working while the post waits for the lookup (busy forms (`form[data-busy]`, `busy-forms.ts`)).
That handler normalizes the input (`discovery.ParseInput`), applies the per-user limit, and calls `LookupRepository.Request`, which returns the site key's lookup, creating it or re-queuing it if its result is older than its TTL (24 h when it found a site, 1 h when it failed or found no feeds), or at once when the address differs from the one it ran on, unless it found a site from its home page and the address is the home page too.
It then waits up to 3 s for the row and redirects to the site page if the lookup found one; otherwise to `/lookups/{id}`, which shows a spinner while the lookup runs, then redirects to the site or says what went wrong.
While it runs, `lookup-status.ts` fetches the page's own URL 2 s after each answer, only while the page is visible: a redirect to the site page replaces the lookup page (`location.replace`, so Back skips it), any other redirect (Log in) is followed, and a finished page's message replaces the spinner and is announced in the polite live region; without JavaScript, a `<meta http-equiv="refresh">` in `<noscript>` reloads the page instead.
An address on the app's own site (`-base-url`'s site key, such as a shared timeline's link) is never looked up: Discover says so in place of Look up, and `/lookups` refuses it with a field error before the per-user limit.

Meanwhile a lookup worker claims the row and runs `Discoverer.Discover`: fetch the URL; if it is itself a feed, that's the result.
Otherwise read the page's title, description and `<link rel="alternate">` feeds, or, if there are none, try `CommonFeedPaths`, except on a site whose key has a path, where they'd find the platform's own feed (`medium.com/feed`).
The site is the page's, or for a feed, that of its link, else of the feed itself: `SiteKey` and `SiteRoot` (`sitekeys.go`) add the path that names a site on a shared host, by a rule per platform (Medium, YouTube, Reddit, GitHub, Bluesky) or, on any other host, a first path segment `@name`.
A page whose key has a path takes its first feed's site when that's another key with a path on the same host, so a YouTube `/@handle` page and its `/channel/ID` feed are one site.
Such a site is named as it names itself (`OwnName`, applied by `Discover`: a page's "Alice – Medium" becomes "Alice", while a feed's title, already the feed's own name, loses only Medium's "Stories by … on Medium"), and wherever it's named its platform follows (`Platform`, the `siteName` template function: "Alice · Medium", "Bob · mastodon.social"), so the platform is never lost; "Read on" names the platform for an article on such a site (`readOnLabel`).
Then fetch up to 15 candidates, 4 at a time, and keep up to 10 that parse.
`Discover` refuses with `ErrAppLink` an address on the app's own site (`Discoverer.AppSiteKey`) before fetching it, and a result that ended up there (a redirect, or a feed naming it as its site), so lookups, imports and the debug inspector never save one.
`Result.Checked` records every candidate and its outcome, for the debug inspector; if the deadline ends the checking, `Discover` returns what it has alongside the error, and the runners ignore the result on any error.
The worker turns each feed's items into articles (`ingest.Articles`) and saves the site, its feeds and their articles in one transaction (`DiscoveryRepository.Save`), so a looked-up feed has articles right away.
The site's address, title and description come from the page or feed the lookup fetched, so only a home page's (`Result.HomePage`: the address, or where it led, is the site's root with no query, `IsHomePage`) replace an existing site's; a feed's or another page's (an article, a Medium author's page) name a new site, but an existing one keeps its own, gaining only a title or description it lacks (`upsertSite`); imports save the same way.
A feed already saved under a shared host's own site (`medium.com`, from before sites on shared hosts had their own keys) moves to the site with a path the lookup found it under (`upsertFeed`); it never moves between two sites with a path or two hosts.
When the key rules change (`discovery.SiteKeyRulesVersion` is above the version in `site_key_rules`), `www migrate` moves such feeds without waiting for a lookup (`SiteRepository.Rekey`, in one transaction, holding the row so two deploys can't run it at once): each feed on a bare host's site gets its key from its link, else its URL (`FeedSiteKey`), and when that's a key with a path on the same host, moves to that key's site, created with an empty title if needed, whose root lookup is queued at the seed's priority to name it and find its feeds and icon; a bare host's site left with no feeds is deleted, and the version is recorded.
The site page (below) lists the feeds, and offers "Check {host} for new feeds" at its foot once the last lookup's TTL has passed, or at once when that lookup ran on another address than the home page (`canRecheck`, which reads the lookup's `home_page`, set by `LookupRepository.Request` from `IsHomePage` of the address).
(`ParsePage` also reads a page's title, image and icon links, for the icon worker and enrichment.)

**Discover's suggestions.** With a query but no `scope` (live search's fetch, or a reload of the URL it left), `discoverSuggest` reads only the reader's subscriptions that match (6) and the sites (`searchSites`, 12, as fully followed ones are left out), and builds the same rows as the Feeds tab below, compacted (`followedFeedSuggestion`, `siteSuggestion`: one line of facts, no tagline, no Follow), ranked by `rankResults` and cut to 6.
After them comes "Search everything", a link to the results' Top tab; with no feeds it's the only row (with Look up, for an address).

**Discover's search.** With a query and `scope`, `discoverSearch` reads, in turn, the reader's subscriptions that match (`SubscriptionRepository.Search`: their name, feed title, site title or host), their saved articles that match (`ListSaved`, Saved's own search), the sites that match (`searchSites`: `SearchRepository.Search` and which of their feeds the reader follows), and whether they've saved anything at all (`HasSaves`).
Each source returns up to 51 rows, ordered by how well the name matches (`matchOrder` in `internal/psql/search.go`), so the best are within the limit and 51 tells a tab's "50+" from 50.
They become rows (`searchResult`): a followed feed with "✓ Following", a site with Follow for its one feed the reader doesn't follow or with its feed count when there are several (a site whose feeds they all follow is left out, its followed feeds being there), and a saved article with its excerpt.
`rankResults` orders the feeds, and Top's mix of feeds and saved articles, by `matchRank` (the same order as `matchOrder`), the reader's own things, recency, then title (`searchScore`).
The tabs, Top, Feeds and Saved (`?scope=`), are links in the timeline tabs' markup; a reader with no saves gets none and sees the feeds; Top shows 10 and links the tabs with more.
Follow works as on Discover's rows (`save-star.ts`), returning to the results.

**Sites on a shared host in search** (#739). Searching a platform's name ("bsky", "medium") would otherwise list every profile on it, since the name is in each one's key and feed URLs.
`SearchRepository.Search` groups the sites with a path in their key (`bsky.app/profile/alice.bsky.social`) that matched only outside their own name, in the same statement: a site stays its own row when it's the pasted address (`siteKey`) or its title, its own name without its platform (`discovery.OwnName`), matches at its start or a word's start, and a host with only one site to group keeps it as a row.
The rest come back as one `rss.SiteGroup` per host with its count, so the limit is spent on sites that are listed.
Suggestions, Top and the Feeds tab show each group as one row (`groupResult`), ranked as a match outside the title, linking to the results narrowed to that host (`/discover?q=…&scope=feeds&host=bsky.app`, `discoverHostSearch`): the Feeds tab's rows whose key's host is `host`, none grouped, the reader's followed feeds there included, without tabs and with a line linking back to all the results, and one after the rows when there are more than 50.
The reader's own feeds come from their subscriptions, not the site search, so a followed profile is never grouped.

**Recent searches.** They're kept only in the browser, never on the server: `recent-searches.ts` keeps up to 5 queries, newest first, in `localStorage` (`paperround.recentSearches`), a repeat (ignoring case) moving to the top.
A page of results records its query, which the server puts on the results container as `data-recent-query`; suggestions record nothing, so neither typing nor a reload of the URL it left does.
While the box is focused and empty, the list (from `<template data-recent-template>` in `discover.html`, as the CSP allows no inline script) takes the place of the container's contents, each row linking to its query's Top tab; the contents come back on a press or focus outside the box and the list, on Escape, or when the reader types.
Clear empties the list, and the logged-out pages' entry (`login.ts`: Log in, where logging out lands, Sign up, Reset password and the landing page) clears it too, whatever the reason for being logged out, so the next person on a shared device doesn't see it.
Without JavaScript, or with storage blocked, there are no recent searches.

**The seed list.** The `seed_sites` table lists sites to look up before anyone searches for them, one per site key; it's edited only in the admin (see "Seed list and Discover's catalog" under "Admin"), and deploys don't touch it.
Its lookups are ordinary `lookups` rows, which the lookup workers discover like any other; the admin's Add sites and Look up all again queue them at the seed's priority (1), so the workers take readers' lookups (priority 0) first, and nothing retries a failed one on its own.
A reader who requests a site still queued by the seed moves its lookup to priority 0 (`LookupRepository.Request`).

**Discover's catalog.** The feeds picked for Discover, each under one of Discover's sections (`discover_sections`, seeded by its migration), are the `discover_feeds` table, edited one feed at a time on the admin's feed page (see "Feeds and sites" under "Admin").
The seed list gets sites and their feeds into the database; the catalog is which of those feeds Discover shows.

**Discover's sections.** With no query, `discoverHandler` reads `DiscoverRepository.Sections` with 3 feeds a section (`discoverPerSection`), in one statement for every section: the catalog's feeds the reader doesn't follow and that aren't gone, each section's first 3 by their newest article (one probe of `articles_feed_sort_idx` per feed), how many catalog feeds the section has, followed or not (`Total`, for "All N"), and each kept feed's 3 newest articles.
A section with none of them is left out, chip and all; an empty catalog leaves the search box alone.
The same statement puts the sections in the reader's order: the most catalog feeds they follow first (counted from a join on the subscriptions key, gone feeds aside), then, among equals, the sections they chose on the waitlist (`users.sections`, in its order, see "Waitlist"), then the default order (`position`).
It's computed on each load, so following a feed reorders nothing until the next one; when any section is out of its default place (`inDefaultOrder`, by `DiscoverSection.Position`), a quiet line under the chips says "Sections you follow most come first."
The page shows the sections as chips linking to their anchors, then each section's rows: the site (linking to its page) and host, the feed's own title when it isn't just the site's name (`DiscoverFeed.FeedTitle`), its posting rate and "Latest post {when}" (`postingRate`, and the `since-date` partial: `since`'s relative day, or past two months a month and year, with the date in its `title`), a "Latest posts" disclosure with the 3 titles, and Follow.
Follow posts to `/feeds/{id}/subscribe` with `return_to` the row's anchor; without JavaScript it redirects back and the flash says "Following {name}.", and the next load leaves the feed out.
With JavaScript, `save-star.ts` posts it with fetch (the handlers answer fetch with a 204, `fetched`), puts the "✓ Following" link the form holds hidden in its place (`data-follow-once`, see "Subscriptions"), focused, and announces "Following {name}" (`DiscoverFeed.Label`) in the polite live region, so the row stays in place until the next load; a gone feed's redirect makes it submit the form instead, for the flash, without following the redirect itself (`redirect: "manual"`), so the page loads once.

**Import on Discover.** With no query, Discover also says something about importing, decided by `discoverImportFor` from what the account has done, so it needs no stored state and no dismiss button.
While the reader's latest import (`ImportRepository.ListByUser` with a limit of 1) has feeds pending or running, a card above the chips shows its progress as on load, without polling (its feeds done out of its total, and a `<progress>` bar), and links to its page, which updates in place.
Otherwise a reader who has never imported and follows fewer than 10 feeds (`importOfferBelow`, `SubscriptionRepository.Count`) gets a card offering import, linking to `/settings/import`; anyone else gets a one-line link at the end of the page.
With a query, none of them shows, as live search replaces the container they're in.

**A section's page.** Each section's head on Discover links "All {Total} ›" to its page, `GET /discover/sections/{slug}` (`discoverSectionHandler`), whose head has "Discover" as a small link above the section's name, then two tabs, Feeds and Recent posts, each its own page with `aria-current`, in the timeline tabs' style (`timeline-tabs.ts` slides the underline between them); an unknown slug is a 404 (`DiscoverRepository.Section`).
Feeds lists every catalog feed in the section that isn't gone, followed or not (`SectionFeeds`), the most recently posting first, as Discover's rows (the `discover-feed` partial, shared with Discover); a feed the reader follows shows "✓ Following" linking to its page in place of Follow, since the page answers "what's in this section", not "what's new to me".
Recent posts, `GET /discover/sections/{slug}/posts` (`discoverSectionPostsHandler`), is those feeds' articles, newest first, as article rows paged like a feed page (`SectionPosts`, one `articles_feed_sort_idx` scan per feed, merged; "Older posts" with infinite scroll); each row names its feed by `DiscoverFeed.Label` and has Follow in its foot, beside Star, Share and Save, when the reader doesn't follow the feed (`articleRow.Follow`).
Follow works as on Discover, with `return_to` the page and the row's anchor.

**A site's page.** `GET /sites/{id}` (`siteHandler`) is where a reader picks which of a site's feeds to follow; Discover's rows, a section's page, search results and lookups all lead there.
Its head is the site's avatar, name, host and description.
Then its feeds (`ListBySite`), each with its catalog section and newest article's time (`FeedRepository.SiteFeedFacts`, one statement with a probe of `articles_feed_sort_idx` per feed), ordered by `siteFeedRows`: Discover's feeds first, then the others, then gone ones, each group most recently posting first.
Each row shows the feed's title without the site's name (`shortFeedName`), "{Section} · About N posts a week · Latest post {when}" (`postingRate`, `since-date`), and Follow, or a pressed "Following" that unfollows (`follow-form` with `followButton.Following`); `save-star.ts` toggles it without a reload, and without JavaScript it redirects back to the row with a flash.
The page lists the first 4 feeds (`siteFeedsShown`) and puts the rest in a "Show N more feeds" `<details>`; a site with a single feed shows its Follow and facts in the head instead, as a feed's page does, and a site with none just says so.
Recent posts are the newest 10 (`sitePostsLimit`) of the site's live feeds in Discover's catalog, or of all its live feeds when none is (`sitePostFeeds`), read by `ArticleRepository.ListByFeeds` (the feed page's query over several feeds: one `articles_feed_sort_idx` scan per feed, merged), with the reader's read, saved and starred state; a copy in two of the feeds (same canonical URL) shows once, so the query asks for twice the limit.
Posts name their feed (without an avatar, `articleRow.NoAvatar`) only when they come from more than one, and when they're from only some of the live feeds a line says which ("From Harbor Works, the feed Discover lists.").
The foot says when the site's feeds were last looked for and offers to look again.

**Start at:**

1. `internal/server/discover.go` (`discoverHandler`, `discoverSectionHandler`, `discoverSectionPostsHandler`), `discover_search.go` (`discoverSuggest`, `discoverSearch`), `internal/psql/search.go` and `discover.go` (`Sections`, `SectionFeeds`, `SectionPosts`); live search is `web/app/features/live-search.ts`, recent searches `recent-searches.ts`, the search box `partials/search_box.html` (`internal/server/search_box.go`) and its clear button `web/shared/lib/search-clear.ts`, Follow's toggle `save-star.ts`.
   The search: `internal/server/discover_search.go` (`discoverSearch`, `matchRank`, `rankResults`), `internal/psql/subscriptions.go` (`Search`) and `saves.go` (`HasSaves`), and `partials/search_result.html`.
2. `internal/server/lookups.go` (`lookupCreateHandler`, `lookupHandler`) and `internal/psql/lookups.go`.
3. `internal/lookup/lookup.go` (`Runner`).
   The seed list: see "Seed list and Discover's catalog" under "Admin".
4. `internal/discovery/discover.go` (`Discover`), `page.go` (`ParsePage`), `normalize.go` (`ParseInput`, `CanonicalFeedURL`) and `sitekeys.go` (`SiteKey`, `SiteRoot`, `IsHomePage`).
5. `internal/feedparse/feedparse.go` (`Sniff`, `Parse`), then `internal/psql/discoveries.go` (`Save`).
6. `internal/server/sites.go` (`siteHandler`, `siteFeedRows`, `sitePosts`), `internal/psql/feeds.go` (`SiteFeedFacts`) and `articles.go` (`ListByFeeds`), and `internal/ui/templates/site.html`.

**Tests:** `internal/server/discover_test.go`, `discover_search_test.go`, `search_box_test.go`, `lookups_test.go`, `sites_test.go`; `web/app/features/live-search.test.ts`, `recent-searches.test.ts`, `save-star.test.ts`, `web/shared/lib/search-clear.test.ts`; `internal/psql/discover_test.go`; `internal/rss/sections_test.go`; `internal/lookup`; `internal/discovery`; `internal/feedparse` (fixtures in `testdata/`); `internal/psql/search_test.go`, `subscriptions_test.go` (`TestSubscriptionRepositorySearch`), `saves_test.go` (`TestSaveRepositoryHasSaves`), `lookups_test.go`, `discoveries_test.go`, `sites_test.go`, `feeds_test.go` (`TestFeedRepositorySiteFeedFacts`), `articles_test.go` (`TestListByFeeds`).

**Why:** "Feed discovery" in decisions.md, and "OPML import" for where import is offered.

## Feed ingestion and background refresh

**Flow.** Three things fetch a feed's articles: discovery (the first save, above), the Refresh button, and the refresh workers.
The last two share one path, `ingest.Refresher.Refresh`:

1. A conditional GET with the stored `ETag` / `Last-Modified`.
2. The outcome is recorded and the next fetch scheduled.
   After a success (2xx or 304) the feed is due again after an interval set by its posting rate as of its last save (`NextFetch`, from `feed.PostsPerWeek`): about four fetches per expected article, between 15 minutes and an hour (an hour for a rate of 0), ±10%.
   A save that finds the feed may have outrun its file (`FetchResult.Behind`: a later save of two or more articles, all new, of a feed attempted within two days) schedules a catch-up fetch instead: half the time since the previous successful fetch, at least 15 minutes, and never later than the rate's interval (`CatchUpFetch`); `SaveFetch` picks between the two after saving the articles, and the next ordinary save goes back to the rate.
   Either is delayed if `Cache-Control` / `Expires` says the response stays fresh longer (capped at 24 h).
   A feed with no subscribers, whatever the outcome, isn't due again for at least a day (`rss.UnfollowedRefreshInterval`): `SaveFetch`, `RecordNotModified` and `RecordFailure` check for a subscriber as they store `next_fetch_at` (`scheduledNext`), so the Refresher doesn't need to know who follows.
   The workers log `behind` with each refreshed feed.
   - **304:** `RecordNotModified`.
   - **410:** `MarkGone`; not fetched again until a lookup finds the feed again.
   - **Network error, other non-2xx, or not a feed:** `RecordFailure`, with backoff (`RetryAt`: 1 h, 2 h, 4 h, … up to 24 h), delayed further by a 429's or 503's `Retry-After`.
   - **2xx feed:** parse it, turn the items into articles, and `SaveFetch` in one transaction: the feed's metadata and validators, the articles and the success, then `next_fetch_at`.

Turning items into articles (`ingest.Articles`): each item's identity within its feed is its ID, else its URL, else a hash of its text; duplicates are dropped and at most 200 are kept.
Each article gets a plain-text excerpt, a `canonical_url` (tracking parameters removed) for matching copies across feeds, and the MIME type of its first audio or video enclosure (`media_type`).
Its `authors` are the names `feedparse.itemAuthors` finds: every `dc:creator` for RSS (gofeed keeps only the first), else gofeed's single pick (`<author>`, `itunes:author`, …), and every author for Atom and JSON Feed, with email addresses reduced to their name or dropped.
A feed's `categories`, what it says it's about, are its channel's categories (RSS `<category>` and every level of `itunes:category`, Atom `<category>`'s term, else its label), then its items' categories (JSON Feed's `tags`) found on at least two items, most items first (`feedparse.feedCategories`): read from gofeed's original feed rather than its merged `Categories`, which mix in `itunes:keywords` and `dc:subject`.
Like the title, they're replaced on every save that parses the feed, by discovery (`upsertFeed`) and by `SaveFetch`, also with none; only admins see them, as plain text ("Admin" in decisions.md).
What `Parse` reads is also listed by hand in `usedChannelFields` and `usedItemFields` (`feedparse/inspect.go`) for the feed inspector, so a change to `Parse` updates them too.
Saving (`saveArticles`) locks the feed row, sets each new article's `timeline_at` (see "Reading"), and upserts by `(feed_id, external_id)`: unchanged articles aren't written, and an existing `published_at` is kept because some feeds bump it on every edit.
A newly inserted article missing an image, title or excerpt gets an enrichment job in the same transaction (see "Article enrichment").

`feeds.posts_per_week` is the feed's articles published in the last 30 days (undated ones count from when they were saved) × 7 ÷ the days its articles cover, from its oldest article to now but at least 7 and at most 30 (`postsPerWeek`), recomputed at the end of every `saveArticles` and by `RecordNotModified`, so the window keeps sliding for feeds that stopped changing.

Which feeds the workers fetch: `ClaimDue` takes a due feed that has a subscriber, a pending refresh request or a place in Discover's catalog (`discover_feeds`), requested ones first.
A request is made when someone views a stale feed's page (`feedHandler` calls `RequestRefresh`).
Catalog feeds nobody follows are so fetched about once a day, so what Discover and Picks show of them is at most a day old; other feeds nobody follows are fetched only on request.
A feed that gains its first follower keeps its `next_fetch_at`, at most a day away, and goes back to its posting rate's interval after its next fetch.
The Refresh button instead fetches right away, within 8 s, and reports the result in a flash; it refuses gone feeds, feeds fetched successfully in the last 5 minutes, and users over the per-user limit.
With JavaScript, the button shows it's working ("Refreshing…") while the fetch runs (busy forms (`form[data-busy]`, `busy-forms.ts`)).

**Start at:**

1. `internal/refresh/refresh.go` (`Runner`) and `ClaimDue` in `internal/psql/feeds.go`.
2. `internal/ingest/ingest.go` (`Refresher.Refresh`, `Articles`, `ExternalID`), `schedule.go` and `canonical.go`.
3. `internal/psql/feeds.go` (`SaveFetch`, `RecordFailure`, `RecordNotModified`, `MarkGone`) and `saveArticles` in `articles.go`.
4. `internal/server/feeds.go` (`feedRefreshHandler`, and `RequestRefresh` in `feedHandler`).

**Tests:** `internal/feedparse`; `internal/ingest`; `internal/refresh`; `internal/psql/feeds_test.go`, `articles_test.go`; `internal/server/feeds_test.go`.

**Why:** "Feed ingestion" in decisions.md.

## Subscriptions

**Flow.** Follow is one partial (`follow-form`, its data a `followButton`) in two kinds, by the page it's on.
On the pages that manage a feed, its own and its site's (`followButton.Manage`), it's Follow, then, pressed, "Following", which unfollows; on a site's page `save-star.ts` toggles it in place, and on the feed page it posts as a plain form (`followButton.Reload`), since Edit beside it is only for a followed feed.
In the lists for finding feeds (Discover's rows, a section's Feeds and Recent posts, search results), it's Follow only: a followed feed shows "✓ Following" linking to its page (the `following-label` partial), and with JavaScript, a pressed Follow turns into that label in place (`data-follow-once`); without, the next load shows it, or, on Discover and Recent posts, which leave followed feeds' Follow out, nothing.
Search shows it as plain text after a reload, since a followed feed's row is then the feed's own, which opens it.
Either posts to `/feeds/{id}/subscribe` or `/unsubscribe` with a `return_to` path and redirects back with a flash.
Unfollowing a subscription with a name or tags doesn't unfollow at once: `feedUnsubscribeHandler` redirects to a confirmation, `GET /feeds/{id}/unfollow` (`feedUnfollowHandler`, `unfollow.html`), which shows the feed with the reader's name and tags, says which of them will be removed, and has a red Unfollow that posts again with `confirm=true` and Cancel, a link back to `return_to`.
With JavaScript the redirect makes `save-star.ts` submit the form, so the browser goes there.
Edit subscription's Unfollow, at its foot with its own warning, posts `confirm=true` too, and always returns to My feeds.
Without a name or tags, Following unfollows at once, since nothing but the follow is lost.
Gone feeds can't be subscribed to but can be unsubscribed from.
My feeds (`/feeds`) lists the user's subscriptions grouped by site, with when each feed last posted (`since-date`), posting rate (`postingRate`, from `posts_per_week`) and a warning after 3 failed fetches in a row.
Only a site with two or more subscriptions gets a header; a single subscription whose name differs from its site's says "From <site>" instead (`feeds.html`).
Each row opens the feed page, through its title link stretched over the row (`.my-feed` in `feeds.css`), and has one control, an Edit pencil.
Sort by Recently updated (`?sort=updated`) lists the feeds whose latest article is newest first, without site headers, so every row whose name differs from its site's says "From <site>"; feeds without articles come last, and Edit subscription returns to the sort.
The search (`?q=`) matches the row's name, the feed's title, and the site's title and host, ignoring case: `feedsHandler` renders every row with that text lowercased in `data-filter` and hides the rows (and the sections left empty) that don't contain the query, so the page works without JavaScript, and `feeds-filter.ts` toggles the same rows as the reader types, with nothing fetched.
A group keeps its header while any of its feeds match, and the double rule that ends it goes on the next shown section (`data-after-group`, set by both).
Site, feed and search pages mark subscribed feeds with one extra query (`SubscribedFeedIDs`).
Subscriptions also decide which feeds are refreshed in the background and what's in the timeline.

**Names.** A subscription can have the user's own name for the feed (`subscriptions.title`, private to them; `""` means "use the feed's title", so a feed that changes its title still updates).
Edit subscription (`/feeds/{id}/edit`) has a Name field and the subscription's tags (see "Tags"), and Unfollow at its foot; a blank name, or one equal to the feed's current title, is stored as `""`.
The name shows wherever the user's own reading shows the feed: per-user list queries (`ListTimeline`, `fillAlsoIn`, `ListSaved`) select `subscriptionTitle` as the feed's title, so rows and "Also in" show it without template changes, and the feed page, the article page and the OPML export use it too.
Catalog pages (site, search) and debug panels keep the feed's own title.
Unsubscribing drops the name and the subscription's tags, which is why unfollowing one that has them asks first (above).

**Export.** `/settings/export` asks whether to turn tags into folders (the default) or ignore them, then downloads `/settings/subscriptions.opml` (`exportOPMLHandler`): every subscription, gone feeds included, as OPML 2.0, one `<outline>` per feed with the user's name for it.
With folders, feeds are nested in a folder per tag (a feed with two tags in both), then the untagged feeds at the top level.
It's encoded into a buffer first, so an error is an HTML 500 instead of half a file.

**Import.** `/settings/import` uploads an OPML file, with a choice to turn folders into tags (the default), and lists the user's last 5 imports.
With JavaScript, the Import button shows it's working while the file uploads and is read (busy forms (`form[data-busy]`, `busy-forms.ts`)).
`settingsImportFormHandler` caps the body with `http.MaxBytesReader` (not `decodePostForm`), parses the file with `opml.Parse`, and re-renders with an error on the file field for no file (422), over 1 MiB (413), not OPML, no feeds or over 1,000 (422), or a sixth import within the hour (429).
Otherwise it turns each feed's folders into tags (`importTags`: normalized like typed tags, cut to 50 characters, repeats dropped), creates the import and redirects to `/settings/imports/{id}`, which updates in place while feeds are pending (`import-status.ts` polls it and inserts new rows; a meta refresh without JavaScript) and then lists what was subscribed, already subscribed, or couldn't be added and why.

`opml.Parse` reads every `<outline>` with an `xmlUrl`, at any depth, in file order and without repeats; each feed's folders are those it's nested in, at any depth.
It parses non-strictly and decodes legacy charsets, since older readers' exports are sloppy.
`ImportRepository.Create` stores one pending `import_feeds` row per feed, with its tags.
The import runner (`importer.Runner`, six workers by default) claims one row per worker at a time, oldest import first: it parses the URL, looks up its `CanonicalFeedURL`, runs discovery for an unknown or gone feed (saved exactly like a lookup; a link to the app itself fails as `app_link`), then subscribes or records `already_subscribed`, names a subscription it made after the outline's title when that differs from the feed's title (`importedName`), and adds the row's tags (`TagRepository.AddToSubscription`: reuses the user's tags by name, creates the rest up to the cap, never removes one).
A tag error is only logged: the feed stays subscribed.
Failures aren't retried; a crashed row is re-queued by `ResetStale`.
Subscribing bumps nothing onto the timeline: the backlog keeps its published dates.

**Start at:**

1. `internal/server/feeds.go`: subscribe, unsubscribe and its confirmation (`feedUnsubscribeHandler`, `feedUnfollowHandler`), `feedsHandler` (its sort and search), and the subscription edit handlers; the `follow-form` partial in `partials/discover_feed.html`; `web/app/features/save-star.ts` and `feeds-filter.ts`.
2. `internal/psql/subscriptions.go`.
3. Export: `exportOPMLHandler` in `internal/server/settings.go`.
4. Import: `internal/server/import.go`, `internal/opml/opml.go`, `internal/psql/imports.go`, then `internal/importer/importer.go`.

**Tests:** `internal/server/feeds_test.go`, `articles_test.go`, `sites_test.go`, `discover_test.go`, `discover_search_test.go`, `settings_test.go`, `import_test.go`; `web/app/features/save-star.test.ts`, `feeds-filter.test.ts`; `internal/psql/subscriptions_test.go`, `articles_test.go`, `saves_test.go`, `imports_test.go`, `tags_test.go`; `internal/opml`; `internal/importer`.

**Why:** "Subscriptions" and "Accounts" in decisions.md.

## Tags

**Flow.** `/tags` (linked from My feeds) has a New tag form and the user's tags with their feed counts.
A name is normalized with `rss.TagName` (trimmed, inner whitespace collapsed) and must be non-blank, at most 50 runes and without commas (Edit subscription separates new tags with commas).
`Create` reports `ErrDuplicateTag` for a name the user already has, ignoring case (`citext`), and `ErrTooManyTags` at 100.

A tag's page (`/tags/{id}`; another user's tag is a 404) is one form with a checkbox per subscription.
Saving posts the ticked feeds; `tagFeedsHandler` drops any the user no longer subscribes to, and `SetFeeds` in one transaction removes the tag from the other subscriptions and adds it to these, leaving their other tags alone.
Rename uses the same checks; Delete (no confirmation) cascades to `subscription_tags` and `timeline_tags`.
All of it works without JavaScript.

**On a subscription.** Edit subscription has a checkbox per tag and a New tags field for comma-separated names.
The post keeps only the user's own tag IDs (a tag deleted in another tab is ignored), splits and normalizes the new names, and calls `SetForSubscription` before renaming, so a refusal changes nothing.
In one transaction that checks the subscription exists, refuses names that would pass the cap, creates new names with `INSERT … ON CONFLICT (user_id, name) DO UPDATE` (so an existing name, ignoring case, reuses that tag), and replaces the subscription's tags.
`ListByUser` and `Get` fill `Subscription.Tags` with one more query each (`tagsByFeed`), and My feeds shows each feed's tags as links.
Unsubscribing cascades to the subscription's tags; the tags themselves stay.

**Start at:**

1. `internal/server/tags.go`, and the subscription edit handlers in `internal/server/feeds.go`.
2. `internal/psql/tags.go`, and `tagsByFeed` in `subscriptions.go`.
3. `Tag`, `TagName` and the tag errors in `internal/rss`.

**Tests:** `internal/server/tags_test.go`, `feeds_test.go`; `internal/psql/tags_test.go`, `subscriptions_test.go`; `internal/rss/rss_test.go`.

**Why:** "Stars and tags" under "Filtering and timelines" in decisions.md.

## Timelines

**Flow.** Home and each custom timeline are headed by a tab row (from `TimelineRepository.List`): the user's tabs in order, Home wherever they put it, then an Edit pill, outside the part that scrolls, to `/timelines` (at the current timeline's row, `#timeline-{id}`); the row starts scrolled so the current tab shows (CSS, before the first render), `timeline-tabs.ts` keeps it where the reader scrolled it when they move to another tab, and between two tabs the current tab's underline slides and stretches from the old tab to the new one (a view transition, #521).
Under the tabs, a row shows only for the timeline's description or an older page's "Back to newest".
A timeline is a name, an optional description and an `rss.TimelineFilter` over the user's subscriptions:

- **Show:** all feeds, or (`OnlyIncluded`) only those with one of `IncludeTagIDs` or in `IncludeFeedIDs`.
- **Leave out** (`ExcludeTagIDs`, `ExcludeFeedIDs`) always wins.
- **Only show:** `StarredOnly` (the user's stars); `MaxPostsPerWeek` (feeds whose 30-day `posts_per_week` is at most that, so a feed that gets busier takes its older articles with it); `MediaOnly` (articles with an audio or video enclosure; embedded videos don't count); `IncludeWords` (articles mentioning one of its words or phrases); `ExcludeWords` (left out even when they also mention an include word).

Tag-based timelines stay current, since the filter reads `subscription_tags` on every page.

Words and phrases match whole words, ignoring case, in `concat_ws(' ', title, page_title, excerpt)` (never the full content).
`wordsPattern` turns a list into one Postgres regular expression for `~*`, `(?<![[:alnum:]])(?:…|…)(?![[:alnum:]])`, with each ASCII character that isn't a letter or digit escaped (so `C++`, `.NET` and `a|b` match literally) and each run of whitespace as `\s+`.
So `AI` matches "AI's" and "#AI" but not "said", and `C` matches "C++" too (the accepted trade-off).
`[[:alnum:]]` follows the database's `LC_CTYPE`, so in a UTF-8 database `café` doesn't match "cafés".

`/timelines` lists the tabs in order, Home among them, with rows like My feeds': the row opens the timeline, and move up, move down (disabled at the ends) and the Edit pencil are icon buttons at its end.
Each row says what the timeline is made of (`timelineSummary`, from `TimelineRepository.ListWithFilters`, which reads every filter, its tags and feeds included, in one statement, as `Get` reads one, and the user's tag names).
Home, labelled "All feeds" (`homeName`), is one of the tabs: `TimelineRepository.List` returns it as the zero `rss.Timeline` at `users.home_position`, in the same numbering as `timelines.position` (Home first on a tie), and it moves like the others but can't be edited, shared or deleted.
It has its own address, `/timelines/all` (`homeTabID` stands for it in URLs, fragments and the tab cookie), since `/` may show another tab.
`TimelineRepository.Move` locks the user's row and timelines and renumbers every tab 0…n, Home included, with the tab swapped with its neighbour, which also repairs ties and gaps from deletes.
With JavaScript, `reorder.ts` shows each row's drag handle (rendered hidden) and makes the move buttons move rows in place; after a drop or a press it announces the new position and posts the whole order to `POST /timelines/order` (`timelineOrderHandler`, a 204), where `TimelineRepository.Reorder` checks it's exactly the user's tabs (else `rss.ErrOrderChanged`: a flash and `/timelines`) and renumbers through the same code as `Move`.
Rows glide to their new places on springs (`spring` and `glide` in `reorder.ts`, animating the real rows), and a moved row is kept in view; a dragged row follows the pointer while the others slide aside, the order changes only on the drop, and holding a row near the top or bottom edge scrolls the page.
Without JavaScript the move buttons post to `/timelines/{id}/move` as before.
New timeline and Edit render one form, `timeline_form.html`: a summary box, then Name, Feeds (All my feeds or Only some, then Leave out) and Articles (a checkbox per condition), then Save.
The summary is a sentence saying what the timeline shows and how many of the user's feeds it draws from (`timelineSentence`, `timelineFeedCount`); the server renders it from the form's values, and `timeline-form.ts` rebuilds it as fields change from the feeds' and tags' `data-*` attributes, with the same rules and words.
A condition's setting, and Only some's tags and feeds, are revealed with CSS `:has()`; hidden fields still post, so `timelineForm` decides by the `quiet`, `mentions` and `not_mentions` checkboxes and stores nothing for an unticked condition.
They and the form's `<details>` open and close smoothly, in CSS only (`timeline-form.css`; the look is under "Motion and feedback" in decisions.md).
Every new account starts with two ordinary timelines, Quiet posters (at most 2 posts a week) and Starred: `UserRepository.Create` adds `rss.DefaultTimelines` in the account's transaction, through the same insert as `TimelineRepository.Create`, so signup and `admin create-user` both get them.
New timeline (`timelineNewHandler`) opens on a list of starting points (`timeline_new.html`), each a link back with a `?preset=` from `timelinePresets` that fills in the form; a tag's page links to it with `?tag={id}`, and an unknown preset or a tag that isn't the user's shows the list.
`timelineForm.check` keeps only the user's own tags and feeds and validates the fields (limits are in the code and its tests); words are split on commas by `splitTerms`.
`ErrTooManyTimelines` (20) is a form error; another user's timeline is a 404 (`timelineFromURL`).

A timeline's page and Home (the zero `rss.Timeline`) share `renderTimeline` (see "Reading").
`/` (`homeHandler`) renders the start tab in place: the one the `tab` cookie names, if it's still one of the user's tabs, else the first.
Every timeline page sets that cookie (`HttpOnly`, `SameSite=Lax`, `Max-Age` of `rss.VisitGap`, renewed on each view), so the nav's Home returns to the tab being read and a later visit starts on the first.
`ListTimeline` applies `timelineWhere`'s conditions to the earlier copy it looks for as well as to the entry, so copies collapse after filtering and a left-out feed's copy never stands in for an entry; "Also in" still lists every subscribed feed with a copy.
A starred-only timeline lists stars instead (`StarRepository.ListStarred`), newest starred first, paged by `starred_at`, without "Also in".
It LEFT JOINs `subscriptions`, so with Show all a star from a feed the user has since left still shows; `timelineWhere` handles the missing subscription (Show conditions fail, Leave out conditions pass).

**Sharing.** A custom timeline can be shared as an Atom feed, with a landing page at the same link; sharing needs a confirmed email.
Its share page (`/timelines/{id}/share`, `timelineSharePageHandler`, from Share in the edit page's head) shares it (`TimelineRepository.Share`, a `token.Random` token), shows the feed and page links (`sharedURLs`, built from the base URL), and offers New link (`NewShareLink`) and Stop sharing (`Unshare`); each of those posts comes back to it (`redirectToSharing`).

The public routes are outside the session group.
`sharedTimelineFromURL` 404s a malformed token or one `GetShared` doesn't know.
The page (`sharedTimelineHandler`) shows the name and description, how to follow it, and `<link rel="alternate">` to the feed; it reads no articles.
The feed (`sharedFeedHandler`) reads the newest 50 with `ArticleRepository.ListShared`: the query for the timeline's articles without a row (`timelineStmt`, or the starred list), with `shared` set, so feeds keep their own titles (never the owner's names), articles without a URL are left out, and every entry is at its shared position, since a shared feed has no reader.
`sharedAtomFeed` builds the feed: the page URL as its ID, each entry's ID its canonical URL (else `urn:uuid:{id}`), an `<author>` for each of the article's authors, and a `<source>` naming its feed.
It's sent with an `ETag` and `Last-Modified` (a matching `If-None-Match` is a 304), `X-Robots-Tag: noindex` and `Cache-Control: public`.

**Start at:**

1. `internal/server/home.go` (`homeHandler`, `renderTimeline`, `timelineRows`).
2. `internal/server/timelines.go` (the handlers and `timelineForm`).
3. `internal/psql/timelines.go`, then `timelineWhere`, `ListTimeline` and `ListShared` in `articles.go`, and `ListStarred` in `stars.go`.
4. `internal/server/shared.go`.

**Tests:** `internal/server/timelines_test.go` (including `TestTimelineSummaryFixture`), `shared_test.go`, `home_test.go`, `policy_test.go`; `timeline-form.test.ts`, `timeline-tabs.test.ts` and `reorder.test.ts` in `web/app/features`; `internal/psql/timelines_test.go`, `articles_test.go`, `stars_test.go`; `internal/token`.

**Why:** "Timelines" and "Sharing" under "Filtering and timelines" in decisions.md.

## Reading: timeline, article pages and read state

**Timeline.** `renderTimeline` asks `ListTimeline` for 31 entries to know whether there's a next page, and shows 30.
A timeline is one list in position order, newest first: each entry at the reader's own position (their `timeline_entries` row, see "The reader's position") when they have one, else at its `timeline_at`, which `ListTimeline` returns as `rss.ArticleWithFeed.Position`; it's paged with a keyset cursor on that position and the ID, `?before=<time>_<id>` (`cursor.go`).
A page merges two queries by `(position, id)` descending: the reader's rows, from `timeline_entries_user_timeline_idx` (`entriesStmt`, joined to `subscriptions` so rows of a feed they've left don't show), and the articles without a row, read for each of the user's subscriptions (those the timeline's filter shows) as that feed's newest entries before the cursor from `articles_feed_timeline_idx` (`timelineStmt`).
The second leaves out the articles the reader has a row for with an uncorrelated `NOT IN`, which Postgres runs as a hashed SubPlan: it reads the reader's article IDs once per query and checks each scanned article in memory, so leaving them out costs no lookup per article.
When the rows fill the page, the second query is bounded below by the last of them, since nothing below it can be on the page, so each feed's scan is a short range and a reader's first pages are cheap; past their rows, a page costs what #404's per-feed query costs (see the Performance watchlist in backlog.md).
Positions are set once, so a row never moves between pages, and later pages need nothing but the cursor.
Copies of one article (same `canonical_url`) collapse into the earliest: an entry is skipped when an earlier copy (smaller position, then ID, each copy at the reader's row's position if it has one, else its `timeline_at`) is in one of the user's feeds, a check only articles flagged `has_copies` at ingest (`saveArticles`) get.
`fillAlsoIn` then lists the other subscribed feeds that carry each entry.
Feed pages work the same way with `ListByFeed`, but in publication order and without collapsing copies.
List queries never load `summary` or `content`.
Times are rendered in UTC for `local-times.ts` to localize (the `local-time` and `local-date` partials), on every page that shows one; a date-only feed's date (the `local-day` partial) takes the reader's date format but stays on its UTC day.

**Card values.** Rows and the article page show an article through accessors on `rss.Article`, so every page agrees: `CardImageURL` (the feed's image, else the page's, over https), `DisplayTitle` (the title, else the page's title, else the start of the excerpt) and `DisplayExcerpt` (the excerpt, else the page's description).
`Byline` joins the article's authors ("A, B and C") for the article page's dateline; rows use `ShortByline` ("A and 4 others" past two, the full list in its `title`) in the kicker after the time, through `articleRow.Byline`, which leaves them out when the only author is the feed's name the kicker shows.
The page's values come from enrichment and only fill gaps.
An image that fails to load in the browser (a 404, or a host without working https) doesn't show as a broken box: `image-fallback.ts` tries its `data-fallback-src` (`CardImageFallbackURL`, the page's image when the feed's is the one shown; the article page's lead image gets it only if the content doesn't show it), then removes the image, and in an article's body what only held it (its link, paragraph or figure).
Without JavaScript the broken box stays.
Images show as they download, over their `--surface` placeholder, without a fade.
Card images are lazy except one: on a list's first page (timeline, Saved, a feed), `markFirstImageEager` makes the first row with an image load at once with `fetchpriority="high"`, since it's usually a phone's largest first-screen element; the article page's lead image is eager and high priority too.

**Timeline position.** `timelineAt` sets `timeline_at` once, when an article is first saved.
*News* (on a later save: undated, or published since a day before the feed's previous successful fetch) goes by its date, never below a floor, the later of the previous fetch and `rss.NewsWindow` (6 hours) before the save (`placeNews`): undated news at the save time, 1 µs apart in document order; news dated above the floor at its date, but below the undated news; news dated at or below the floor spread evenly, newest-published first, between the floor and the lowest news placed by date.
*Backlog* (every item on a feed's first save, or older items appearing later) goes at its published date, or now if that's in the future.
A first save's undated items are spaced out at the feed's pace below the item before them in its document, the newest at least a day back (`placeUndated`).
A later save of a feed with no fetch attempt for two days (`staleFeedAfter`), such as a looked-up feed nobody followed until now, counts as a first save: nobody was fetching it, so when its items appeared is unknown and their dates are the best order (the previous attempt decides, not the previous success, so a followed feed that was down keeps its news); it gets the first save's enrichment allowance and is never `Behind`.
Whether an article was placed as backlog is stored with it (`isBacklog`), for debug mode.
So new items land near the top in publish order (and, for a reader who has looked recently, above what they've seen: see "The reader's position"), and an imported archive, dated or not, doesn't.

**The reader's position.** A reader who has looked in the last `rss.NewsWindow` also gets their own position for what arrives in a feed they follow, a `timeline_entries` row set once and never changed, which their timelines read instead of `timeline_at` (see "Timeline").
`saveArticles` writes one per inserted article that is news or positioned within `rss.NewsWindow` of the save (a first save's latest posts, for followers who subscribed before the feed was fetched), for each follower whose mark's `seen_at` is that recent; `Subscribe` writes one per article of the feed positioned within `rss.NewsWindow`, when the subscription is new and the reader has a mark, so imports (`importer.Runner.run`) do too.
Both use `raisePositions`: a shared position above the reader's `seen_up_to` is kept, and those at or below it are spread evenly, newest first, just above it, so nothing they get lands among what they've seen.
Shared news is never below its feed's floor, so a follower whose `seen_up_to` is at or below the floor gets the shared positions, and one away longer than `rss.NewsWindow` would, so gets no rows; readers without a mark read shared positions.
`Subscribe` also keeps the reader's entries in place when the new feed has copies of them (`keepCopiesInPlace`): copies collapse into the earliest (see "Timeline"), so a new feed's copy at an earlier position, like a blog's two-day-old post the reader just saw at the top through an aggregator, gets their own position 1 µs below the entry they have, never below its own `timeline_at`; only entries at or above the reader's oldest row are kept, the part their rows cover.
`Unsubscribe` keeps the reader's rows for the feed: timelines leave out rows of feeds they don't follow, so following it again finds them where they were and `Subscribe`'s `ON CONFLICT DO NOTHING` doesn't raise its recent posts a second time; each new visit trims them like any other row (see "You were here").

**Article page.** `articleHandler` records the read and loads the user's subscription to the feed, if any, for its name.
The "Read on" button (`readOnLabel`) names the article's domain when it's on another site, else the feed.
The body is the content, else the summary, through `sanitize.HTML` (relative URLs resolved against the article's URL); with neither, the page's description as plain text.
`sanitize.HTML` also splits plain text into paragraphs and links bare URLs, turns YouTube and Vimeo iframes into a click-to-play preview (`video-embeds.ts` swaps in the sandboxed player; without JavaScript the link opens the video), turns posts embedded from X and Bluesky into a post card (`xPost` and `blueskyPost` read the embed's markup, `socialCard` writes the card, whose time `local-times.ts` shows in the reader's zone), and turns any other iframe into a link, and makes `http:` images load over `https:`.
Images a site lazy-loads with a script show without it: `imageSource` takes the URL from `data-src`, `data-original` or `data-lazy-src` when `src` is missing or a `data:` placeholder, `noscriptImage` uses the `<img>` in a `<noscript>`, and `dropEmptyImages` removes an image still left without an http(s) source, with the paragraph, link or figure that held only it; `ShowsImage` reads sources the same way.
The card image shows above the text unless `sanitize.ShowsImage` finds the content already shows it: an image first, or right after the first paragraph (a `<p>`, or opening text outside one), or the same image anywhere (`imageKey`: ignoring the query, WordPress's size suffix, and an image server's settings when its path names the original); tracking pixels, emoji and empty blocks (an empty anchor, `<p>&nbsp;</p>`) are skipped, as they are for `dropRepeatedTitle`'s first block.
`sanitize.HTML` also keeps footnotes and other in-page links on the page (`localAnchors`): every id gets the prefix `body-`, and a link to one, whether `#frag` or the article's own URL with that fragment (Substack's footnotes), becomes `#body-frag`, while a fragment with no target in the article opens the original; `article.css` gives the targets a scroll margin that clears the phone's sticky top bar.
`sanitize.HTML` also keeps the page's title its only `<h1>`: a feed's `<h1>` becomes an `<h2>`, and `dropRepeatedTitle` drops the content's first block when it's a heading repeating the title the page shows (`DisplayTitle`), read inside a single wrapper as `ShowsImage` reads blocks (`firstBlocks`).
`sanitize.HTML` also returns the article's outline (`Body.Outline`, from `outline` in `outline.go`): its `<h2>`s and `<h3>`s, outside blockquotes and post cards, each given an id from its text when it has none (`body-` and a slug, made unique); with at least `outlineMin` (3) of them, the page shows a contents list linking to each (`contents` nests the `<h3>`s under their `<h2>`), beside the column from 80rem and not below, rendered by the server, so it works without JavaScript; `contents.ts` marks the section being read and folds the other sections' `<h3>`s.
`sanitize.HTML` also names a code block's language, when the feed's highlighter marked it, in `data-lang` on its `<pre>` (`labelCode` in `code.go`), which the block's bar shows.
Each code block (`.article-body pre`) gets a copy button from `code-copy.ts`, cloned from the page's `template[data-code-copy]` into the block's bar, since the sanitizer allows no button and it needs JavaScript; it's added only where the Clipboard API works, copies the block's text less one trailing line break, and shows and announces the outcome ("Code copied" or "Couldn't copy") with the same label as Share's (`copied.ts`, below).

**Read state.** An article is read when its page is opened, or when the reader clicks through to the original from a row: the link has `ping="/articles/{id}/read"`, which `read-pings.ts` replaces with `navigator.sendBeacon` because Firefox ignores `ping`.
List pages ask `ReadArticleIDs` which rows are read (matching any copy with the same `canonical_url`), and the row is shown quieter.
A row also shows as read as soon as the reader follows either link, before the server has recorded it, so a list that isn't loaded again (Back to a page restored from the back/forward cache, a link opened in another tab) shows it too: `initReadMarks` in `read-pings.ts` adds the row's `.read` and the same hidden " (read)" `article_row.html` renders, which `TestArticleRowReadMarkup` pins; other copies on the page get their marker on the next load.

**"You were here."** A timeline's first page (not starred-only) carries its first row's position, the newest on it, in `.article-list[data-seen-up-to]`, and the timeline in `data-timeline`, and `seen-beacon.ts` sends both to `POST /timeline/seen` with a beacon when the page is first shown (at load, its first time visible, or restored from the back-forward cache) and each time it's hidden: the reader's positions for what arrives next are set against the latest report, so an article arriving while the page is open already counts what's on it.
While the page is visible and used (a scroll, a key, a tap or a click since the last time), it also reports every 10 minutes, so a long read of one page stays one visit; a page left on screen unused doesn't.
`MarkSeen` keeps the newest position reported on any timeline (never later than now) and when, in the user's mark, and the newest reported on this timeline in its own line (`timeline_lines`; a timeline that isn't the user's records the mark only); a report after `rss.VisitGap` without one starts a new visit, whose divider becomes where the previous visit ended (`divider_at`), the mark's and every timeline line's, before this report's position is recorded.
So a timeline not opened in the previous visit keeps the line from the last visit it was opened in.
A new visit also trims the reader's positions, in the same transaction: it keeps every row above the previous visit's divider (the mark's `divider_at` before the report), but at least `rss.MinEntries` and at most `rss.MaxEntries`, the newest by position, or the newest `rss.MaxEntries` when there's no divider yet.
Since the page reports when it's shown, this runs right after a visit's first page renders, before any older page loads; a deleted row's article goes back to its shared position, which differs only if the row was raised, and is never higher.
At render, `TimelineLine` reads the timeline's own line with the user's `seen_at`, `TimelineMark.Divider` gives the line, the newest position the timeline had on screen in the last visit it was opened in (none for a timeline never seen), and `fillDivider` puts it above the first row at or below it, only when something is above it: an earlier row, or a cursor positioned above it.
So above the line is everything that arrived since you last looked at that timeline, by the reader's positions, including articles published before it but fetched after.
With motion, `divider-draw.ts` draws the line in the first time at least half of it is in view on a page load (on dividers infinite scroll appends too), once, by marking it `data-draw` for `rows.css`; without the script or under reduced motion it's simply there.
A store error renders the page without the line.

**Home's empty and short states.** Every article of a followed feed is on Home, so an empty first page means the reader follows nothing, or only feeds with no articles; `SubscriptionRepository.Following` tells them apart in one statement (`homeFollowing`).
Following feeds never fetched yet (no attempt at all), it says it's getting the latest from two of them by name; any other empty Home, following nothing or only feeds that were fetched and had nothing or failed, offers the ways in, Discover and import, so it never waits on a feed that won't fill it.
Waiting is short-lived, as a lookup or import saves a feed's articles with it (`DiscoveryRepository.Save`) and a feed never fetched is due at once, so nothing is requested from the GET.
The page checks again: `home-fetching.ts` fetches Home's own address (not `/`, which shows whichever tab the tab cookie names) every 3 s while it's visible, swaps in its rows (setting up the list-wide enhancements then) and announces them; after about a minute it stops and takes the spinner away.
Without JavaScript a `<noscript>` meta refresh reloads it every 5 s.
The last page of Home, for every account, ends with a line counting the sites the reader follows and linking to Discover; other timelines have none, and on a store error it's left out, since it's decoration.

**Infinite scroll.** `infinite-scroll.ts` watches the "Older articles" link; when it nears the viewport it fetches that page, appends its rows (and the "You were here" line, in place) and swaps in the new pager, or, from Home's last page, its end line.
It keeps the address bar on the topmost visible row (`history.replaceState`), so reload, and Back where the page isn't restored, land nearby.

**Back.** In Chrome, Back to a timeline (or a feed, a site or Saved) restores the page from the back/forward cache as the reader left it: the rows infinite scroll added, the scroll position and the images, with no request.
Navigations stay `no-store`; what keeps the page restorable is that nothing it sends after loading changes a cookie or gets a `no-store` answer: the pages infinite scroll and Home's polls fetch, Save, Star and Follow, and the beacons are answered `private, no-cache` (`privateCache`, in `middleware.go`), and only a navigation sets the `tab` cookie.
Chrome still loads the page again after 3 minutes, after logging out or in (the session cookie changed), and after moving to another tab of the timeline (the `tab` cookie changed); Safari and Firefox always load it again.
Without JavaScript the link just works.

**Start at:**

1. `internal/server/home.go` (`renderTimeline`, `fillDivider`, `timelineSeenHandler`) and `cursor.go`.
2. `internal/psql/articles.go` (`ListTimeline`, `entriesStmt`, `timelineStmt`, `fillAlsoIn`, `ListByFeed`, `timelineAt`).
3. `internal/server/articles.go` (`articleHandler`, `articleRow`) and `internal/psql/reads.go` (including `MarkSeen`, `TimelineLine`), with `rss.TimelineMark`.
4. `internal/sanitize/sanitize.go` (`HTML`, `ShowsImage`), `code.go` (`labelCode`) and `social.go` (`xPost`, `blueskyPost`, `socialCard`).
5. `partials/article_row.html`, and `seen-beacon.ts`, `infinite-scroll.ts`, `home-fetching.ts`, `read-pings.ts`, `image-fallback.ts` and `code-copy.ts` in `web/app/features`.

**Tests:** `internal/server/home_test.go`, `articles_test.go`, `cursor_test.go`, `scroll_test.go`; `web/app/features/seen-beacon.test.ts`, `read-pings.test.ts`, `infinite-scroll.test.ts`, `home-fetching.test.ts`, `image-fallback.test.ts`, `code-copy.test.ts`, `contents.test.ts`; `internal/psql/articles_test.go`, `reads_test.go`, `subscriptions_test.go`; `internal/sanitize`; `internal/rss/rss_test.go`.

**Why:** "Reading" and "Design" in decisions.md.

## Saved articles

**Flow.** Every article row and the article page has an icon-only Save button (the `save-button` partial), named by its `aria-label` and with the same word as its `title`, like Star and Share: a form posting to `/articles/{id}/save`, or `/unsave`, with a `return_to` of the page.
The handlers (both `toggleAction`) redirect back with `#article-{id}`, so without JavaScript the page comes back at the same row; with JavaScript, `save-star.ts` flips the button in place as it's pressed (`aria-pressed` carries the state) and posts the form with `fetch`, which the handlers tell apart by its `Sec-Fetch-Mode` header and answer with a `204`.
It sends one request at a time per button: presses made while one runs count, and when it ends the script sends the request for the state last pressed if that isn't the one the `204` confirmed, so the reader's last press wins.
Requests are `keepalive`, and a press still waiting when the page is hidden (the reader leaves it, or a phone puts the browser in the background) is sent at once, so leaving doesn't lose it; if the page is still there when the running request ends, that state is sent once more, since the two may have crossed.
On anything but a `204`, such as the redirect to Login when the session has ended, the button goes back to the last confirmed state and the form for the state the reader wanted is submitted normally.
Saving is quiet: no flash, and it doesn't mark the article read.
A press that turns Save or Star on drops the bookmark into place or pops the star with a burst of dots, without waiting for the server (only under `prefers-reduced-motion: no-preference`; the burst is off in forced colors); going back after a failure doesn't animate.

**Copies.** Saving covers every copy of an article (same non-empty `canonical_url`), like read state: `Save` inserts nothing when a copy is already saved, `Unsave` removes every copy's save, and `SavedArticleIDs` matches copies.

**Stars.** A star means "this is good" (Saved stays "keep").
The Star button sits next to Save and works the same way (`toggleAction`); `StarRepository` mirrors `SaveRepository`.
Stars are private, with no counts; starred-only timelines list them (see "Timelines").

**Share.** Between Star and Save, an icon-only Share button (the `share-button` partial) carries the article's `CanonicalURL` and `DisplayTitle`; an article without a canonical URL has none.
It's rendered `hidden` and `share.ts` shows it, so it never appears without JavaScript.
A click opens the system share sheet (`navigator.share`) where there is one, else copies the link and announces "Link copied" in a polite live region.
When it copies, `showCopied` in `copied.ts` (shared with code blocks' Copy buttons) also shows the outcome in a label above the button: a success for 2 seconds (longer while a mouse is over it or it has keyboard focus), with a check that draws itself in place of the share icon, and a failure until the reader's next press or key.
Only the latest copy's label shows, and a copy repeated while it shows replays its entrance (`data-copy-round` restarts the CSS animations).
The label is `aria-hidden`, so it isn't read on top of the announcement.
A browser that can neither share nor copy never shows the button.
It never talks to the server: nothing about a share is recorded.

**Saved page** (`/saved`). Modelled on the timeline: `ListSaved` returns the user's saves newest first, paged by `(saved_at, article_id)` (served by `saves_user_saved_at_idx`), and left-joins the user's subscriptions so a named feed shows its name.
Unsaving from the page keeps the row until reload when JavaScript is on, so an accidental unsave can be undone in place.
Search (`/saved?q=…`, submitted by Enter or the box's magnifier, in the search box Discover and My feeds share, its clear button included; see "Discover, site lookups and discovery") trims `q` and cuts it to 200 runes, then matches it with `ILIKE` (`likePattern` escapes the wildcards) against the article's title, page title and excerpt, the feed's title and the user's name for it.
There's no count anywhere.

**Start at:**

1. `internal/server/articles.go` (`toggleAction` and the save and star handlers) and `saved.go`.
2. `internal/psql/saves.go` and `stars.go`.
3. `partials/save_button.html`, and `save-star.ts`, `share.ts` and `copied.ts` in `web/app/features`.

**Tests:** `internal/server/articles_test.go`, `saved_test.go`, `home_test.go`, `feeds_test.go`, `scroll_test.go`, `templates_test.go`; `save-star.test.ts`, `share.test.ts` and `copied.test.ts` in `web/app/features`; `internal/psql/saves_test.go`, `stars_test.go`.

**Why:** "Reading" (its "Saved articles") and "Stars and tags" under "Filtering and timelines" in decisions.md.

## Site icons

**Flow.** The site icon runner (`siteicon.Runner`, `-icon-workers`) claims one site at a time with `SiteRepository.ClaimIconCheck`: a site whose `icon_next_check_at` has passed and that has never been checked (so a new site gets its first check soon, subscribed or not) or has a subscribed feed.
The claim moves `icon_next_check_at` 5 minutes ahead as a lease.

`Runner.check` fetches the site's home page; an error or a non-2xx keeps the icon and retries in a day.
Otherwise it builds up to four candidates (`candidates`): the icons the page declares, apple-touch first and largest first, skipping SVG and those declaring a size under 32, then `/apple-touch-icon.png` and `/favicon.ico`.
A site on a shared host (a key with a path) tries its own picture before them, since the icons its page declares are the platform's (#744): the page's `og:image` (else `twitter:image`), then the picture of the first feed the page declares, fetched and parsed for it (`pictures`, `feedparse`'s `Image`: an Atom feed's square icon before its logo); either must also be nearly square (`squarish`), so a banner or a link card falls through (`ownPicture`).
This stage gets half the check's time, so the declared icons keep theirs; if the feed or a picture failed in a way that may not last and the site already has an icon, the check keeps it and retries in a day, rather than replacing the author's picture with the platform's.
Each is downloaded with a second `fetch.Client` capped at 256 KiB, the current icon conditionally (a 304 keeps it).
`usable` recognizes PNG, JPEG, GIF and ICO from the bytes and reads their size; the first icon at least 32 px on its shorter side and at most twice as long as wide is stored (`SaveIcon`, with its SHA-256 `hash`).
If none is usable the icon is removed, unless a candidate failed with a network error, a 429 or a 5xx: then it's kept and retried in a day.
A check that reached the site schedules the next one in 30 days ±10%.

**Serving and showing.** `GET /sites/{id}/icon` (no session) serves the stored bytes with their `Content-Type`, an `ETag` of the hash and a CSP of `default-src 'none'; sandbox`; it's cached for a year when `?v=` is the current hash, else for an hour.
Pages get icon URLs with one `IconHashes` query for the page's sites (`siteIconURLs`), and templates call the `avatar` partial: the icon on a white tile, else the site's letter.
Icons are decoration: if `IconHashes` fails, the error is logged and the page shows letters.

**Start at:**

1. `internal/siteicon/siteicon.go` (`Runner`, `check`) and `ico.go`.
2. `internal/psql/sites.go` (the icon methods).
3. `internal/server/sites.go` (`siteIconHandler`, `siteIconURLs`) and `partials/avatar.html`.

**Tests:** `internal/siteicon` (an `httptest` site with generated fixtures); `internal/psql/sites_test.go`; `internal/server/sites_test.go`, the page tests built on `testSiteIcons`, `templates_test.go`; `cmd/www/config_test.go`.

**Why:** "Design" in decisions.md.

## Installing the app

**Flow.** Every page includes `app-head`, which links the favicons, the web app manifest and a `theme-color` (the user's theme's, else the system's; see "Themes" in [architecture.md](architecture.md)).
That is all the browser needs to offer installing: Safari's Share → Add to Home Screen on iOS, Chrome's Install app on Android and desktop.
The installed app opens `/` full screen (`display: standalone`): the timeline when logged in, the landing page when not.
There is no service worker, install button or banner, and no pull-to-refresh: tapping the current tab loads its page again.

**Start at:**

1. `internal/server/manifest.go` (`manifestHandler`) and `internal/ui/manifest.webmanifest`.
2. `internal/ui/templates/partials/head.html` (`app-head`).
3. `web/app/public/img/app-icon.svg` (the source of every install icon, Apple's included).

**Tests:** `internal/server/manifest_test.go`.

**Why:** "Mobile" in decisions.md.

## Article enrichment

**Flow.** Many feed items arrive without an image, a title or any text; enrichment fetches their web page in the background and stores what its meta tags say.

1. **Queuing.** `saveArticles` knows which upserted articles were inserted (`RETURNING id, (xmax = 0)`).
   Jobs exist only for *wanted* feeds, ones someone follows or in Discover's catalog (`enrichWanted` in `internal/psql/enrich.go`).
   A new article that `needsEnrichment` (it has a URL and lacks an image, a title or an excerpt) gets an `enrich_jobs` row if `enrichCandidates` allows it: none for a feed that isn't wanted; for a wanted feed, on a save placed as a first save only the 20 with the latest `timeline_at`, at priority 1, and on later saves every one, at priority 0.
   Updated articles aren't queued again, and older articles never.
2. **Becoming wanted or not.** The places that change follows and the catalog keep the rule, each in its own transaction: `SubscriptionRepository.Subscribe`, for a feed's first follower while it isn't in the catalog, and `AdminFeedRepository.SetDiscoverSection`, for a feed entering the catalog while nobody follows it, queue its backlog (`queueEnrichBacklog`: its 20 newest that need it and have no `page_*` values yet, at priority 1); `Unsubscribe`, `UserRepository.Delete` (the feeds the account followed) and `SetDiscoverSection` taking a feed out drop the pending jobs of those no longer wanted (`dropUnwantedEnrichJobs`).
   So a looked-up or seeded site nobody follows costs no page fetches, and no job is left that nobody would claim.
3. **Claiming.** The enrich runner (`enrich.Runner`, `-enrich-workers`) claims a due job whose article's feed is wanted, priority 0 first, with a 2-minute lease, and counts the attempt.
4. **Fetching** (20 s per job):
   - **network error, 429 or 5xx:** retried after 1 hour, then 6 hours (or a later `Retry-After`, capped at 24 h), and given up after the third attempt;
   - **any other non-2xx, a body that isn't HTML, or a fetch the client refuses:** given up at once;
   - **an HTML page:** `discovery.ParsePage` reads its `og:image` (else `twitter:image`), `og:title` (else `<title>`) and description, `PageOf` turns them into an `rss.ArticlePage`, and `SavePage` stores them in `page_image_url`, `page_title` and `page_description` and deletes the job, in one transaction.
5. Outcomes are recorded with a fresh short context; on shutdown nothing is recorded and the lease retries the job.

The `page_*` columns are never the feed's values: `saveArticles` overwrites `image_url`, `title` and `excerpt` on every changed fetch and leaves `page_*` alone.
Pages read them only through the card-value accessors (see "Card values" under "Reading").

**Start at:**

1. `saveArticles`, `needsEnrichment` and `enrichCandidates` in `internal/psql/articles.go`.
2. `internal/psql/enrich.go` (`enrichWanted`, `queueEnrichBacklog`, `dropUnwantedEnrichJobs`, `ClaimNext`), and their callers in `subscriptions.go`, `users.go` and `admin_feeds.go`.
3. `internal/enrich/enrich.go` (`Runner`, `attempt`, `PageOf`).

**Tests:** `internal/psql/articles_test.go`, `enrich_test.go`, `enrich_wanted_test.go`; `internal/enrich` (an `httptest` server); `cmd/www/config_test.go`.

**Why:** "Enrichment" in decisions.md.

## Debug mode

**Flow.** Debug mode shows the stored data behind site, feed and article pages and article rows, and inspects feeds, article pages and site discovery live, for the accounts listed in `-debug-emails` only.

1. **Gating.** `-debug-emails` is lowercased and validated in `parseConfig`.
   For an allowed account (`debugAllowed`), Settings → Help shows a Debug mode row that puts or removes `"debug"` in the scs session, so it's per device and logging out ends it; for anyone else `POST /settings/debug` is a 404.
   `debugEnabled` (allowed and toggled on) is checked on every request, so removing an account from the flag turns debug mode off at once; the nav shows a Debug badge.
2. **Panels.** When `debugEnabled`, the site and feed pages load `DebugStore.SiteDebug` and `FeedDebug` (icon bookkeeping, refresh state, article and enrich job counts) and render them in a collapsed panel after the header.
   The article page loads `ArticleDebug` (its enrich job, up to 20 copies by `canonical_url`, and this user's read and save rows) and shows every column, where each shown value came from, its timeline placement, and the raw `summary` and `content` (cut at 20 000 bytes); `?debug=1` opens the panel.
   Off, none of these queries run.
3. **Provenance.** `articleProvenance` is a pure function of the article that says where each shown value comes from (feed, page, excerpt or none; timeline `backlog` or `news`; external ID kind).
   Its rules mirror `DisplayTitle`, `DisplayExcerpt` and `CardImageURL`, whose comments point to it.
4. **List line.** List pages call `fillDebug` after building their rows; it adds a line per row from columns the lists already load, so no queries, with a link to the article's panel.
5. **Inspectors.** Routes under `/debug/` are POSTs behind `requireDebug` (404 unless `debugEnabled`), since they fetch URLs; each extends its write deadline past the server's `WriteTimeout`, fetches within 30 s, and saves nothing.
   - **Feed** (`feedInspectHandler`): fetches the feed (optionally with the stored validators), and `feedparse.Inspect` shows what `Parse` returns, every channel and item field marked used or ignored, and each item's raw source; `ingest.Articles` and `ArticlesByExternalID` give each item its outcome (new, unchanged, which columns differ, or dropped).
   - **Page** (`articlePageInspectHandler`): fetches the article's page and shows what enrichment would do, next to the stored `page_*` columns, then every meta tag (`discovery.InspectPage`, marked used or ignored from `usedPageKeys`) and its JSON-LD.
   - **Discovery** (`siteDiscoverHandler`): runs `discovery.Discoverer` on the site and shows every candidate checked and its outcome, the feeds found marked known or new, and stored feeds it didn't find.

   Untrusted text (bodies, tags, sources) is shown only as escaped text, and cut to a size.

**Start at:**

1. `internal/server/debug.go` (`debugAllowed`, `debugEnabled`, `requireDebug`, the panels and inspectors).
2. `internal/psql/debug.go`.
3. `internal/feedparse/inspect.go` and `internal/discovery/inspect.go`.
4. `internal/ui/templates/partials/debug.html`.

**Tests:** `internal/server/debug_test.go`; `internal/psql/debug_test.go`; `internal/feedparse/inspect_test.go`; `internal/discovery/inspect_test.go` and `discover_test.go`; `internal/enrich/enrich_test.go`; `cmd/www/config_test.go`.

**Why:** "Debug mode" in decisions.md.

## Admin

**Flow.** The admin site is a second binary, `admin`, with its own server on its own origin (production's is `admin.paperround.app`, behind Cloudflare Access; see "The admin" in `infra/README.md`), for the owner only; this is its skeleton (#562) and login (#567), and later admin issues add its sections.
Locally it runs with `make run/admin` (http://localhost:4001).

1. **Command.** `cmd/admin` (`run`) runs the server for `admin` or `admin serve`, and `admin enroll` (below); an unknown subcommand prints the usage and exits 2.
   `parseConfig` reads the flags, each also settable by its `RSS_` variable (`internal/cli`), and `serve` opens a pool of at most `-db-max-open-conns` (3) connections on the reader's database and runs `admin.Server`, and `admin.Pinger` when `-app-internal-url` is set (see "Observability"), until a signal.
2. **Server.** `internal/admin` has the reader's middleware order without the metrics (`logRequest`, `recoverPanic`, `http.CrossOriginProtection`, `commonHeaders`, `GetHead`) and its own headers, stricter than the reader's (see "Security measures" in architecture.md); it serves `/healthcheck` like `www`'s, its own `/static/`, and the pages.
3. **Pages.** Templates are `internal/ui/admin` (`ui.AdminTemplates`): standalone pages, each with `admin-head`, its page group (`{{template "assets" "admin"}}`) and `admin-nav`, which takes the page's section.
   The nav lists only the sections whose routes exist, in the order in `partials/nav.html`; it's a sidebar on wide screens, and on phones a top bar whose Menu (a `<details>`, no script) opens the same links.
   The Overview, Feeds and sites, Users and support, the Seed list and Discover's catalog, Queues, Web performance, Service health and Endpoints are below; the Waitlist is under "Waitlist" and the Audit log below.
4. **Frontend.** `web/admin` is built apart from the reader's (`web/vite.admin.config.ts`, into `web/dist/admin`, `web.Admin`), and both servers read their build with `internal/assets`.
   `admin/styles/base.css` is every admin page's sheet: the shared partials, then the admin's (`shell`, `page`, `table`, `chart`, `waitlist`, `webperf`, `service`, `endpoints`, `feeds`, `queues`); `admin/entries/admin.ts` sets up busy forms and row selection (`admin/features/select.ts`).
5. **Charts.** A page draws a `chart.LineChart` or `chart.Histogram` (`internal/admin/chart`) with the `chart` template function, as an SVG made on the server, with no script.
   The drawing stretches to its container at a fixed height, with positions across in percentages and down in pixels, so its text keeps its size (11px) at every width; a line chart's latest values and threshold labels sit in a gutter at its right.
   Each chart is a CSS container: the server gives each time-axis or bucket label a class saying from which width it fits (`thin`), and `chart.css` hides those that don't, so narrow charts draw fewer.
   Colors are classes (`chart-series-1` to `-3`) set from the tokens, and every text from data is escaped; each `<svg>` has `role="img"`, a `<title>` and a `<desc>` that says what it shows (latest values and ranges, or a histogram's median and 95th percentile, from `metrics.Percentile`).

**Start at:**

1. `cmd/admin/main.go`, `config.go` and `serve.go`.
2. `internal/admin/router.go` and `middleware.go`.
3. `internal/ui/admin/partials/nav.html` and `home.html`.
4. `web/admin/styles/base.css`.
5. `internal/admin/chart/chart.go` (`LineChart.SVG`, `Histogram.SVG`) and `web/admin/styles/partials/chart.css`.

**Tests:** `internal/admin/server_test.go`; `internal/admin/html_test.go`; `internal/admin/chart/chart_test.go` (with golden SVGs in `testdata`, `-update`); `cmd/admin/config_test.go`; `internal/assets/assets_test.go`; `internal/cli/cli_test.go`; `web/build_test.go` (`TestAdminManifest`); `web/admin/entries/entries.test.ts`.

### Overview

`GET /` (`homeHandler`) answers "is everything all right, and how is it growing?" with figures across every reader, never one reader's reading ("Admin" in decisions.md).

1. **Counting.** `AdminOverviewRepository.Overview` counts the People and Content sections in one query at the handler's `now`: small tables exactly, and `articles`, `reads` and `timeline_entries` from the planner's estimate (`estimatedRows`, `pg_class.reltuples`), since counting them reads them whole (the watchlist entry "The admin's overview counts big tables").
   Readers active count distinct `user_sessions.user_id` by `last_seen_at`, which moves at most once a day, so the label says "about"; waiting uses the waitlist's Waiting rule (`waitingEntry`).
2. **The page.** `peopleTiles` and `contentTiles` turn the counts into stat rows (the `stat-row` partial): a label, a number with thousands separators (`count`) and an optional second line, the label a link stretched over the figure where a page exists (Confirmed accounts to `/users`, Waiting to `/waitlist`, Invites pending to `/invites`, Feeds to every feed on `/feeds`).
   The head says the admin's build and when the page was rendered.
   The Service section, first, shows `www`'s uptime over 24 hours and its version from the pinger's checks (`serviceTiles`), linking to the service page, or "Not configured" without the pinger.
3. **Queues.** `AdminOverviewRepository.Queues` counts each runner's due jobs and finds the oldest's due time in one query, with the same condition its claim query uses (`feedDue`, `siteIconDue`, `enrichJobDue`, `emailDue`, `lookupPending`, `importFeedPending`), so it counts what the runner would take next; lookups and import feeds are due from when they were asked for.
   `queueRows` says how long the oldest has waited (`wait`), and a queue is behind when that's over `rss.QueueBehindAfter` (15 minutes); each queue but refresh links to its section of the queues page.
4. **Jobs.** `AdminOverviewRepository.Jobs` sums `job_metrics`' minute rows of the last 24 hours by runner and outcome; minute rows are kept 7 days, so the window needs no hourly rollups.
   `jobRows` sorts each shown runner's outcomes (refresh, lookups, enrichment, email) into the buckets they all share (`jobRunners`: done, failed, and other for the rest), and `bucketBar` draws them as one bar per runner, failures first and at least 3% wide, with the per-outcome breakdown as the bar's tooltip and label; the Failed column gives the exact share, and with no job metrics at all, the section says so.

**Start at:**

1. `internal/admin/home.go`.
2. `internal/psql/admin_overview.go`.
3. `internal/ui/admin/home.html` and `partials/stats.html`.

**Tests:** `internal/admin/overview_test.go`; `internal/psql/admin_overview_test.go`.

### Feeds and sites

`GET /feeds` (`feedsHandler`) finds the feeds that fail, went quiet or answered 410, `GET /sites` (`sitesHandler`) lists the sites with their lookup and icon, and each feed's and site's page fixes their fetching; subscribers are counts, never who ("Admin" in decisions.md).

1. **The list.** A row of filter links (`feedFilters`): Failing (`rss.AdminFailingAfter`, 3, failures in a row, the reader's warning), Gone, Never fetched (no successful fetch), Stale (subscribed, not gone, and no successful fetch for a day, counting a feed never fetched from when it was found), No subscribers and All, Failing by default; a search of titles, URLs and site hosts (`?q=`); and headers that sort by subscribers (the default), posts a week, failures (most first) or last success (oldest first, never fetched before all).
   A filter or order the page doesn't offer is its default, and each link keeps the others (`feedsParams.href`).
   Each filter shows how many feeds matching the search it keeps (`AdminFeedRepository.CountFeeds`, one pass with a `count(*) FILTER` per filter).
   Each row is the feed with its site's host (linking to the site's page, `feedTable.LinkSites`) and its Discover section, if any, under it, its problem (`problemOf`, the `problem-text` partial), its subscribers, its posts a week, and its last success and next fetch relative to now (`ago`), with the UTC time on hover; a subscribed feed's last success over a day old is in bold (`isLate`), since Stale is a filter, not a problem.
   The problem is one phrase, the first that applies, then the last error cut to one line: "Gone since {date}" and "Failing {n}×" (from `rss.AdminFailingAfter` failures in a row) in the danger tone, "Failed {n}×" (fewer) and "Never fetched" in the neutral one, and nothing for a feed that's fine; the Problem header sorts by failures in a row.
   `AdminFeedRepository.ListFeeds` returns 100 a page; its cursor (`?after=`) is the last row's feed, compared on the order's key and then the ID, read from the feed itself, so a row that changes between pages doesn't lose the place.
   Every filter but Gone reads every feed and counts its subscribers (the watchlist entry "The admin's feed list").
2. **A feed's page.** `GET /feeds/{id}` (`feedHandler`, `AdminFeedRepository.Feed`) opens with its status panel, tinted when its problem is bad: its problem as the heading, else "Last fetched {when}" when it's late, else "Fetching normally"; a line of what happened and when; the whole last error while it's failing; and only the actions that apply (Refresh now, or Clear gone when it's gone; Reset backoff after a failure).
   Then its figures (subscribers, posts a week, articles with the newest's age, enrichment jobs with the oldest's: `feedFigures`), its Discover panel, and every other column as escaped text in Details (its categories, as the feed gave them, or "None"), with links to its page in the reader, where debug mode inspects it, and to its History in the audit log.
3. **The sites list.** Sites have their own section in the sidebar, after Feeds, and the list follows the feeds list's pattern (`sitesParams`, like `feedsParams`): filter links (`siteFilters`: All, the default, No icon, Lookup failed and No subscribers) with their counts (`AdminFeedRepository.CountSites`, one pass), a search of hosts and titles, and headers that sort by subscribers (the default), feeds, host (A to Z) or when it was found (newest first), 100 a page with the same kind of cursor.
   Each row is the site's icon as an image on a white tile, as readers see it (an empty tile without one), then its host, linking to its page, with its title under it; its lookup (the one keyed by its host) in `lookupSummary`'s words, in the danger tone when it failed; its icon's size and type ("64×64 PNG", or "None", `iconLabel`); its feeds; its subscribers, each reader once across its feeds; and when it was found.
   `AdminFeedRepository.ListSites` counts each site's feeds and subscribers and joins its icon (never the image) and lookup on every view (the watchlist entry "The admin's site list").
   The images come from the admin itself, `GET /sites/{id}/icon` (`siteIconImageHandler`, `AdminFeedRepository.SiteIcon`), since the admin's CSP allows only its own images: like the reader's icon route, its URL carries the icon's hash (`?v=`, `iconURL`), so the browser keeps it for good (privately) until the icon changes, and the image is sandboxed.
4. **A site's page.** `GET /sites/{id}` (`siteHandler`, `AdminFeedRepository.Site`), under Sites in the sidebar, shows its feeds in the list's table (the `feed-table` partial, unsorted, their hosts not linked); its lookup (by its host, the lookups' site key) as a sentence (`lookupSummary`: waiting, running, done with or without feeds, or failed, with its error) and its icon (the image, larger, then its size, type, bytes, source and when fetched, or none, and its next check) side by side, each with its action; then the site's columns in Details.
   Both a site's page and a feed's open with breadcrumbs of where they sit (the `breadcrumbs` partial, from `siteCrumbs` and `feedCrumbs`): Sites › the site › the feed, though the sidebar keeps Feeds current on a feed's page.
5. **Actions.** Each is a POST that redirects back to its page with a flash, and each is recorded in the audit log:
   - Refresh now (`feedRefreshHandler`) calls `ingest.Refresher.Refresh` within 8 s, as the reader's Refresh button does but without its 5-minute rule or per-reader limit, through the admin's own `fetch.Client` (see "Outbound HTTP" in architecture.md); the fetch records its outcome on the feed as any does, and the handler then records `feed.refresh` with the outcome (saved with its counts, not modified, gone, or failed with the error), even when the fetch used up the request's time.
     A gone feed isn't refreshed: Clear gone first.
   - Reset backoff (`AdminFeedRepository.ResetBackoff`, `feed.reset_backoff`) and Clear gone (`ClearGone`, `feed.clear_gone`, only for a gone feed) make the feed due now, the first with no failures counted; the background refresh runner fetches it if it has subscribers.
   - Check for new feeds (`CheckForFeeds`, `site.lookup`) queues a lookup of the site's home page at a reader's priority with the reader's `LookupRepository.Request` statement (`requestLookup`, in the action's transaction) and no time-to-live, so a finished lookup runs again; one already pending or running stays as it is.
   - Check the icon again (`CheckIcon`, `site.check_icon`) makes the site's icon due now, subscribed or not: it also clears `icon_checked_at`, so the icon runner takes the site as if it had never been checked.
6. **Discover's catalog.** A feed's Discover panel says whether it's in the catalog and under which section, as its heading, with a section select and Save posting to `POST /feeds/{id}/discover` (`feedDiscoverHandler`); the empty choice takes it out.
   `AdminFeedRepository.SetDiscoverSection` changes the feed's `discover_feeds` row and records `feed.discover` with the section's ID and name before and after, in one transaction, so the entry still reads after a rename; saving the section it already has records nothing, and putting a skipped feed in the catalog unskips it (see "Picking feeds" below).
   The panel's choices are `discover_sections` in their order (`DiscoverSections`), posted by ID; a section that doesn't exist and adding a gone feed are refused with a field error (422); a gone feed already in the catalog can still be taken out.

**Start at:**

1. `internal/admin/feeds.go` (`feedsHandler`, `feedRefreshHandler`, `feedDiscoverHandler`) and `sites.go` (`sitesHandler`).
2. `internal/psql/admin_feeds.go` (`ListFeeds`, `updateRow`, `SetDiscoverSection`) and `admin_sites.go` (`ListSites`, `CheckForFeeds`).
3. `internal/ui/admin/feeds.html`, `sites.html`, `feed.html`, `site.html` and `partials/feeds.html`.

**Tests:** `internal/admin/feeds_test.go`, `sites_test.go`; `internal/psql/admin_feeds_test.go`, `admin_sites_test.go`.

### Users and support

`GET /users` (`usersHandler`) finds a reader's account, and `GET /users/{id}` (`userHandler`) shows what state it's in and acts on it, for answering someone who writes in ("I never got the confirmation", "I can't get in", "please delete my account").
Aggregates only: a reader's page shows account facts and counts, never their reads, saves, stars, tags, timelines or feeds, and the admin never logs in as them ("Admin" in decisions.md).

1. **The list.** Users have their own section in the sidebar, after Waitlist, and the Overview's Confirmed accounts links to it.
   Readers newest first, 50 a page (`AdminUserRepository.ListUsers`), with a search that matches the start of the email or the name, in any case (`?q=`, LIKE wildcards escaped); the cursor (`?after=`, `userCursor`) is the last row's join time and ID, compared in that order since `users.created_at` is to the second, and carries the time so the next page still follows when that user is deleted in between.
   Each row is the name, linking to the reader's page, the email, Confirmed or Not confirmed as a status label, when they joined and when they were last seen (the newest of their sessions' `last_seen_at`, "Never" without one), relative to now with the UTC time on hover.
   It reads every user (the watchlist entry "The admin's users list").
2. **A reader's page.** `AdminUserRepository.User` reads, in a few statements:
   - **Account**, a two-column list of facts (one column on phones): name, email, when the email was confirmed (or "Not confirmed"), joined, last seen, the invite they signed up with (its wave, or an open invite's note, and when it was made; `invites.user_id`) and the ID.
   - **Counts**, a stat row (`userCounts`): devices logged in (sessions seen within `rss.SessionIdleTimeout`, 90 days, since an older one is only deleted at the next login), subscriptions, tags, timelines with how many are shared, saved and starred articles, and imports with their feeds subscribed (or already subscribed) and failed, over all of them.
   - **Email**: their email links that haven't expired (purpose, the address it went to, created and expires; never hashes), and the newest 50 emails in the outbox to the reader's address or a link's, so a change of address's confirmation to the new one shows (subject, to, status, queued, sent, attempts; never bodies, which can carry one-time links), noting the outbox keeps finished ones for a week (`mail.Runner.KeepFor`).
     Only emails queued since they joined count: `emails.to_address` isn't a foreign key, so an address reused within the week after its account was deleted would otherwise show that account's emails.
   - A History link to the reader's entries in the audit log (`target_type=user`).
   `userPage` holds only these: `TestUserPageHoldsNoReading` walks its type and fails on any list but counts, emails and links, or a field named for a body or hash.
3. **Actions.** The page ends with them, each a POST back to the page with its outcome in a flash, and each recorded in the audit log by its `AdminUserRepository` method, in its own transaction, which first locks the user's row (`lockUser`):
   - **Resend confirmation** and **Send a password reset link** make the same emails as `www`'s signup and "Forgot password?", from `internal/accounts` (`Emails.Confirmation`, `Emails.PasswordReset`), which builds the token and the email; `www` stores them with its token and email stores (`sendLink`), and the admin with `SendLink`, which stores the token and queues the email in one transaction with `user.resend_confirmation` or `user.send_password_reset`.
     The admin runs no mail runner: `www`'s sends the email from the outbox, as it does invites.
     Resend refuses a confirmed address (`rss.ErrAlreadyConfirmed`), and both refuse an address over the outbox's per-address cap, sending nothing.
     Their forms, and Mark confirmed's, post the address the page showed, which the action uses; the method refuses it when the account's address is no longer that one (`rss.ErrAddressChanged`, in any case), so a link never goes to an address the admin didn't see.
   - **Mark confirmed** (`MarkConfirmed`, `user.mark_confirmed`) sets `email_confirmed_at` and deletes the confirmation links, as confirming with one does; it refuses a confirmed address.
   - **Log out everywhere** (`LogOutEverywhere`, `user.log_out_everywhere`) deletes all their `user_sessions` rows and says how many devices that logged out, counting them as the page does.
   - **Delete account** is its own page, `GET /users/{id}/delete`, behind `requireRecentCode` like its form: typing the account's email (`confirmDestructive`, any case) deletes it with `Delete` (`user.delete`, keeping the address in the entry), which shares `deleteUser` with the reader's own delete, then queues `account_deleted` to the old address (`Emails.AccountDeleted`), as the reader's own delete does; the flash says when that email couldn't be queued, since the account is gone either way.

**Start at:**

1. `internal/admin/users.go` (`usersHandler`, `userHandler`, `userCounts`) and `user_actions.go`.
2. `internal/psql/admin_users.go` (`ListUsers`, `User`) and `admin_user_actions.go`.
3. `internal/accounts/accounts.go`.
4. `internal/ui/admin/users.html`, `user.html` and `user_delete.html`.

**Tests:** `internal/admin/users_test.go`; `internal/admin/user_actions_test.go`; `internal/psql/admin_users_test.go`; `internal/psql/admin_user_actions_test.go`; `internal/accounts/accounts_test.go`.

### Seed list and Discover's catalog

`GET /seed` (`seedHandler`) shows how each seed list entry's lookup went, and `GET /discover` (`catalogHandler`) what Discover's catalog holds, so the owner sees a failed entry and the catalog as a whole rather than a feed at a time.

1. **The entries.** `AdminFeedRepository.SeedList` reads a page of `seed_sites`, newest first (`added_at`, then site key, both descending, as `seed_sites_added_at_idx` is), 100 at a time with the last entry's site key as the `?after=` cursor (a cursor not on the list, or past the end, is 404, as on Feeds); each entry is joined to `lookups` by site key, the site that lookup found (`lookups.site_id`), and that site's feeds and catalog feeds, counted in one grouped subquery over the listed sites' feeds.
2. **States and filters.** `rss.AdminSeedEntry.State` splits the lookup's status: found (done with a site), failed, no feeds (done without one), running, and waiting (pending, or no lookup yet).
   The filters (`seedFilters`: All, the default; Not found, failed or no feeds; Waiting, waiting or running; Found but not in Discover) are conditions in SQL (`adminSeedFilters`), and `CountSeed` counts them all in one statement; each row says its state in the feeds' Problem style (`seedStatus`), a failure's error cut to one line, links the site found to its page, and says when it was added.
3. **Look up again.** A failed or no-feeds entry has the button, which posts its site key to `POST /seed/lookup` (`seedLookupHandler`): `AdminFeedRepository.QueueSeedLookup` reads the entry's address from `seed_sites` (`rss.ErrNoRecord`, so 404, when the key isn't on the list) and queues it at a reader's priority with no time-to-live (`requestLookup`, as `CheckForFeeds` does), recorded as `seed.lookup` on target `seed` with the site key; the page then goes back to the list under the same filter, its flash saying when the lookup was already running and was left to finish.
4. **Look up all again.** Not found, when it has entries, has a button that posts to `POST /seed/retry` (`seedRetryHandler`): `AdminFeedRepository.RetrySeedLookups` re-queues, in one statement, every listed lookup that failed or finished without a site (`adminSeedNotFound`, so it includes an entry found before whose later lookup failed), with the entry's address, at the seed's priority (1); it's recorded as `seed.retry` on target `seed` with the count, unless there were none, and the page goes back to Not found saying how many.
5. **The catalog.** `AdminFeedRepository.Catalog` returns every Discover section in its order with its feeds by title (site, posts a week, latest article, subscribers as a count), empty sections included; the page offers a filter link per section with its count, and All, the default, adds a Section column.
   A gone feed still in the catalog is listed, marked as hidden from Discover (whose queries skip gone feeds), so the owner can take it out.
   Each feed links to its admin page, whose Discover panel (above) puts it in a section or takes it out.
6. **Adding sites.** The list's Add sites goes to `GET /seed/add` (`seedAddHandler`), a text area for a pasted list and an optional source ("waitlist", "IndieWeb list"), which the waitlist can also fill in from its answers (see "Waitlist"); `POST /seed/add` (`seedAddFormHandler`) reads up to 1 MiB (`maxSeedUploadBytes`, over every other form's 64 KiB) and 10,000 lines.
   `seed.Parse` reads the text with no database: one address per line (a home page, or a feed's address, which adds only that feed, as its lookup runs on it), optionally followed by a comma or tab and a section's slug or name in any case, blank and `#` lines skipped; each line gets an outcome: can be added, not an address (`discovery.ParseInput`), names no section, an address on the reader's own site (`-app-url`), or the same site as an earlier line that can be added.
   The handler then asks `AdminFeedRepository.SeedOnList` which sites are already on the list.
   Preview (the form's first button, so Enter in Source is Preview) re-renders the form with the text kept, counts per outcome and every line in a table, problems first, then line order, with an Add button counting the lines that can be added.
   Add parses and checks the text again rather than trusting the preview, then `AddSeedSites`, in one transaction, inserts the lines (`ON CONFLICT (site_key) DO NOTHING`, so a site added by someone else since the preview isn't added or counted twice) with their section hint and the source, queues a lookup of each at the seed's priority (1): a new one, or a re-queue of one that failed or found no feeds however long ago it ran, leaving found, pending and running ones alone; and records one `seed.add` on target `seed` with the source, the count and every site key added, the list's history, unless none was added.
   The page goes back to the list with a flash of how many were added; each entry there shows its section hint and source under its address.
   The hint is stored only: it pre-selects the section of the site's feeds on the picking page (below), and only an admin's Save there puts them in the catalog.
7. **Picking feeds.** The catalog page's head links Pick feeds with how many sites have feeds to pick (`CountPick`, All's count) to `GET /discover/pick` (`pickHandler`): a queue of sites with candidate feeds, ones that aren't gone, in the catalog or skipped (`adminPickCandidate`), 25 sites a page with the last site's ID as the `?after=` cursor (a malformed one, or one past the end, is 404).
   Its filters are From the seed list, the default (sites a seed entry's lookup found, by their newest entry, newest first, `adminPickSeeded`), All sites (every site with a candidate, newest site first) and Skipped (sites with skipped feeds that aren't gone, listing those feeds with an Unskip button each).
   `AdminFeedRepository.PickSites` reads the page's sites with their seed entry's source and section hint, else the section of the site's feed already in the catalog, then, in a second statement, up to 20 candidate feeds per site (most recently posting first, comments feeds, whose title has the word "comment(s)" or whose address has it as a segment, last), each with its posting rate, newest post and 3 newest titles (`articles_feed_sort_idx` per feed), and how many more the site has, which link to its page.
   Each feed is a table row under its site's header row with a tick and a section select, pre-filled with the hint, its categories (see "Feed ingestion") under its notes; it's ticked for you only when its site has one candidate and its seed entry's hint, the admin's own upload, placed it.
   Each site has Skip the rest, ticked to start with.
   `POST /discover/pick` (`pickFormHandler`, up to 256 KiB, `maxPickBytes`) posts each listed feed's ID, site, tick and section and each site's Skip the rest; a ticked feed without a section is 422 with "Choose a section." on its row, which takes the focus, and above the table, the posted choices kept (a site not posted, new on the page since, keeps Skip the rest's default).
   Otherwise `SavePicks`, in one transaction, puts each ticked feed, in ID order so two saves lock feeds alike, in the catalog through `setDiscoverSection`, the feed page's own code (recorded as `feed.discover`, and `feed.discover_unskip` for a skipped one), and skips the posted feeds of Skip the rest sites that weren't ticked (`discover_skips`, recorded as `feed.discover_skip`); a feed the store refuses (gone, an unknown section, deleted) is an `rss.PickError` naming it, which saves nothing and shows the page again at 422 with the error on its row and above the table.
   The page then goes back to the filter's first page with a flash of how many were added and skipped.
   Unskip posts to `POST /discover/pick/unskip` (`unskipHandler`; `Unskip`, recorded as `feed.discover_unskip`; 404 when the feed isn't skipped) and goes back to Skipped.

**Start at:**

1. `internal/admin/seed.go` (`seedHandler`, `seedLookupHandler`, `seedRetryHandler`), `seed_add.go` (`seedAddFormHandler`), `catalog.go` and `pick.go` (`renderPick`, `pickFormHandler`).
2. `internal/seed/seed.go` (`Parse`) and, in `internal/psql`, `admin_seed.go` (`SeedList`, `CountSeed`, `QueueSeedLookup`, `RetrySeedLookups`, `SeedOnList`, `AddSeedSites`), `admin_catalog.go` (`Catalog`), `admin_pick.go` (`PickSites`, `SavePicks`) and `admin_feeds.go` (`setDiscoverSection`).
3. `internal/ui/admin/seed.html`, `seed_add.html`, `discover.html` and `pick.html`.

**Tests:** `internal/admin/seed_test.go`, `seed_add_test.go`, `pick_test.go`; `internal/seed`; `internal/psql/admin_seed_test.go`, `admin_catalog_test.go`, `admin_pick_test.go`.

### Queues

`GET /queues` (`queuesHandler`) shows each background queue's jobs and its failures grouped by cause, so a cause that spreads (a host down, a parser bug) is acted on at once ("Admin" in decisions.md).

1. **Figures.** `AdminQueuesRepository.Counts` counts each queue's jobs by state in one query: pending with its claim query's condition (as the Overview's do), running for the queues with a status (lookups, imports), failed (failed lookups and import feeds, enrichment jobs that failed and wait to be tried again, emails given up on), and, for icons, which store no error, the checks overdue by `rss.QueueBehindAfter`; `queueTiles` adds how long the oldest pending job has waited.
   `JobsPerHour` sums `job_metrics`' minute rows of the last 24 hours into hours that end at now less a whole number of hours, so the last isn't cut short, and `jobsChart` draws each queue's as a line, 0 for an hour with none.
2. **Failures.** For lookups, imports and enrichment, `AdminQueuesRepository.Failures` reads every failed job, newest first, and `groupFailures` groups them by `normalizeError` of their error (an import feed's failure kind first): exact text split one cause into a group per site, since errors carry addresses, so it replaces addresses with `<url>`, unknown hosts (and the resolver asked) with `<host>`, IPv6 addresses with `<ip>` and numbers standing alone with `<n>`.
   The page lists the 20 largest groups (ties: the most recent failure first), each with its error escaped and cut to 200 characters (whole on hover), its count, its oldest and newest failure (enrichment jobs: when queued), and its 5 newest jobs: a lookup's address with its site's page, an import feed's address with its import's date and its feed's page, an article's ID and address with its feed's page.
   An enrichment job on its first attempt has no error yet and isn't a failure; emails' failures are for the email page, so Email shows its figures only.
3. **Actions.** `POST /queues/{queue}/retry` and `/delete` (`queueAction`) take one job (`id`) or a group's (`ids`, space-separated, with the group's error): a group's are the IDs its form listed when the page was rendered, the newest 1,000 at most, never what matches when the post arrives, and its count must be typed to confirm (commas allowed), else the page is shown again with the error on that group's field (422); more than 1,000, or IDs that aren't the queue's, are refused (400).
   `AdminQueuesRepository.Retry` and `Delete` change only the jobs still failed and record `queue.retry` or `queue.delete` with the count, on the job (`lookup`, `import_feed` as `{import_id}.{position}`, or `enrich_job`) for one, or with the group's error for a group, in the same transaction; with nothing changed, nothing's recorded.
   Retry makes a lookup or import feed pending again with its attempts and error cleared (a lookup also re-requested now), and an enrichment job due now; Delete deletes a lookup, which is also its site key's cache, so the next lookup runs afresh, or an enrichment job, whose article keeps what it has.
   Import feeds can't be deleted, since they're their import's results page.
   Each action goes back to the queue's section with a flash saying how many changed.

**Start at:**

1. `internal/admin/queues.go` (`renderQueues`, `groupFailures`, `normalizeError`, `queueAction`).
2. `internal/psql/admin_queues.go`.
3. `internal/ui/admin/queues.html` and `web/admin/styles/partials/queues.css`.

**Tests:** `internal/admin/queues_test.go` (with `TestNormalizeError`); `internal/psql/admin_queues_test.go`.

### Web performance

`GET /web-performance` (`webPerfHandler`) shows the real-user beacons (`rum_views` and `rum_daily`, written as "Observability" below says): how fast the reader's pages load and respond for real people, as page views only, never visitors or sessions ("Observability" in decisions.md).

1. **Filters.** Three rows of links (`readWebPerfParams`, `webPerfParams.href`), so every view has its own URL and needs no script: the period (24h, 7d by default, 30d, 90d, 13m), the device (all, phone, tablet, desktop) and the percentile (p75 by default, p90, p95); a value the page doesn't offer is its default.
   Up to 30 days read `rum_views`, which keeps 30; 90d and 13m read `rum_daily`'s whole UTC days before today, in whole steps (13 months are 56 weeks), so no point is part of one (`webPeriod.query`).
2. **Reading.** `AdminWebPerfRepository.WebPerf` returns every view's stats, the breakdowns by device, browser, installed and route (LCP, INP and CLS only), the metrics a step at a time, each route's views a step at a time, the 20 referrer hosts with the most views (anyone can post a beacon, so there can be many), the protocols and connections, and the Back and Forward views (`backForward`).
   From `rum_views` the percentiles are exact (`percentile_cont`), one query for the totals and one with grouping sets for the breakdowns; from `rum_daily` they're `metrics.Percentile` over the summed buckets, interpolated within a bucket, and a group's views are the most any of its metrics counted per day.
   Either way the shares of good, needs-improvement and poor views are exact, since `metrics.RUMThresholds` are among `metrics.RUMBounds`.
   Whether a view was installed, its referrer, protocol and connection, and why a Back or Forward wasn't restored, are only in `rum_views`, so those parts say so for 90d and 13m.
3. **Core Web Vitals.** A row per metric (`vital`): LCP, INP and CLS, then FCP and TTFB smaller, each with its value at the chosen percentile and its rating, the other two percentiles small beneath, the shares bar, how many views reported it, and its chart over time with the good and poor thresholds, a point per step of the period (`steps`), with a gap where a step had no views.
   Above them, the assessment (`assess`) passes when LCP, INP and CLS are all good at p75, as Google assesses them, whatever percentile is chosen.
   A rating (`rating`, at the precision the page shows, so "2.50 s" is never rated worse than 2.5 s; a value on a threshold is in the better rating) is never color alone: each has a shape (`rating-glyph`: a circle, a square, a triangle) and its words.
4. **Breakdowns and pages.** The device, browser and installed tables, and the routes table (`routes`: sortable by each column through links, the top 20 with "Show all", each route linking to its page with the filters kept), give the views and LCP, INP and CLS at the chosen percentile (`segmentRows`); a row under 100 views is dimmed and tagged "few views", Safari's row notes that its INP is known to over-report, and a row missing a metric says which.
   The page views chart (`routeViewsChart`) draws the three routes with the most views.
5. **Back and Forward.** How many of the period's Back and Forward views were restored from the back/forward cache (`restore`) rather than loaded again (`back_forward`), then the reasons the browser gave for those loaded again, each with its views, most first, the 20 named by the most views (`backForwardData`); a view can name up to 3, so the reasons' shares can add up to more than 100%, and the views with no reason kept (Safari, Firefox and Chrome before 123 report none) come last.

6. **A route's page.** `GET /web-performance/route?route=<pattern>` (`webPerfRouteHandler`) has the same filters, its links keeping the route, and reads the route's views (`AdminWebPerfRepository.WebPerfRoute`, from `rum_views` or `rum_daily` as above): its vitals' rows, as on the page, their charts marked with deploys as on the endpoints pages (`deploysSince`); each metric's distribution, the server's time too, as a histogram of `metrics.RUMBounds` buckets with its thresholds as dashed lines (`distributionOf`; raw views are counted into the buckets as the rollup counts them, `rawBuckets`); TTFB beside the server's time (Server-Timing's `app`) at the chosen percentile, with their difference and both over time (`serverVsNetwork`); the device and browser breakdowns; and, from `rum_views` only, the LCP elements, the slowest interactions' targets (the most slow ones first, slow being over INP's good threshold), the navigation types and the HTML's transfer size, with the share that came from the browser's cache (`webPerfRouteData`).
   A route under 100 views says its percentiles shift; one with none in the period, or never, is an empty state, not a 404, and no route is a 404.
   Its head links back to the page, with its filters, and to the route's endpoints page for a GET at the nearest period (`endpointHref`), whose page links back here (`webPerfLink`).

**Start at:**

1. `internal/admin/webperf.go` (`webPerfHandler`, `webPerfData`) and `webperf_route.go` (`webPerfRouteHandler`, `webPerfRouteData`).
2. `internal/psql/admin_webperf.go` (`WebPerf`, `raw`, `rollups`) and `admin_webperf_route.go` (`WebPerfRoute`, `routeRaw`, `routeRollups`).
3. `internal/ui/admin/webperf.html`, `webperf_route.html`, `partials/webperf.html`, `partials/rating.html` and `web/admin/styles/partials/webperf.css`.

**Tests:** `internal/admin/webperf_test.go` and `webperf_route_test.go`; `internal/psql/admin_webperf_test.go` (raw and rolled-up percentiles agree within a bucket, for the page and a route, `TestAdminWebPerfBackForward` and `TestAdminWebPerfRouteDetails`); `internal/metrics/percentile_test.go`.

### Service health

`GET /service` (`serviceHandler`) shows `www` as a whole: its availability as the admin's pinger sees it from inside Render's network (see "Observability" below), its instances' CPU and memory from Render's API, its Go runtime, its version and its deploys.

1. **Period.** A row of links (the `segmented` partial), 24h by default, 7d, 30d or 90d, for the incidents and the charts; a value the page doesn't offer is the default.
2. **Availability.** Only when the pinger runs (`Config.AppInternalURL`, from `-app-internal-url`); otherwise the section says how to turn it on and nothing is read.
   `AvailabilityRepository.Uptime` counts the checks, and the ok ones, over 24 hours, 7, 30 and 90 days, always all four (`uptimeTiles`, rounded down so one failed check never shows as 100%), and `Incidents` returns the runs of failed checks in the period, newest first, at most 50: each ok check starts a new run, so a missing minute doesn't split one, a run with no ok check after it is ongoing, and one under way when the period starts keeps its real start.
3. **Render.** Only with both `-render-api-key` and `-render-service-id` (`Services.Render`, a `render.Client`); otherwise Instances and Deploys say how to set them and nothing is read.
   `renderSections` asks Render for the period's metrics and the latest 100 deploys at once, within 5 seconds (`renderTimeout`, inside the server's write timeout), to the minute so that loads within a minute reuse the client's cached answers.
   Render failing or timing out shows "Couldn’t reach Render." in its sections and is logged; the rest of the page renders.
4. **Instances.** `instanceCharts` draws CPU and memory, the highest instance's at each step (`render.Client.Instances`; two instances overlap during every deploy), with Render's limits as threshold lines, and the instance count, each spanning the whole period.
5. **Deploy markers.** Every chart marks the deploys that went live in the period with dashed lines, without text: deploys too close to tell apart are one line, and hovering a line shows which deploys it marks (`chart.groupMarkers`, `markerTitle`).
   Render's records of our image-backed deploys carry an image digest, not a commit, so `deployVersions` labels each deploy that went live with the version the pinger last saw before the next one went live (`AvailabilityRepository.VersionsIn`), and any other deploy, or every one without the pinger, with its digest's first 7 characters.
6. **Go runtime.** `MetricsRepository.ListRuntime` reads the period's `runtime_metrics` rows (90d, past the 30 days kept, `metrics.RuntimeRetention`, says to choose a shorter period instead, as the web performance page does past its raw views), and `runtimeCharts` steps them into five charts: at each minute memory, goroutines and GC pause take the highest instance and connections the sum, then each step takes its highest minute; connection waits are the increase of each instance's counter, spread over the minutes since its previous sample, per minute that measured them; a step with no samples is a gap.
7. **Version.** The newest check that reported one (`LatestVersion`); a commit SHA is shortened and linked to the commit.
   Under it, the 10 newest deploys with their status and version.

**Start at:**

1. `internal/admin/service.go` (`serviceHandler`, `renderSections`, `runtimeCharts`).
2. `internal/psql/availability.go` (`Uptime`, `Incidents`, `VersionsIn`).
3. `internal/admin/render` (`Client.Instances`, `Client.Deploys`).
4. `internal/ui/admin/service.html` and `web/admin/styles/partials/service.css`.

**Tests:** `internal/admin/service_test.go`; `internal/admin/render/render_test.go` (Render's answers in `testdata/`); `internal/psql/availability_test.go`.

### Endpoints

`GET /endpoints` (`endpointsHandler`) shows `www`'s requests as a whole and by route, from its request metrics (see "Observability" below), and `GET /endpoints/route?route=<pattern>&method=<method>` (`routeHandler`) one route's.

1. **Period.** A row of links (the `segmented` partial), 24h by default, 1h, 7d or 30d, which both pages keep between them, as a route's page keeps the overview's sort for its way back; a value the pages don't offer is the default (`readPeriod`).
   Up to 24 hours they read minute rows, up to the current minute, which isn't written yet; beyond, hourly rows, up to the end of the current hour, whose minutes so far come from the minute rows not rolled up yet (`endpointPeriod.window`).
2. **Reading.** `AdminEndpointsRepository.Endpoints` reads the period's rows (`endpointRows`: hourly rows, plus the minute rows from the hour after the last rolled-up one, as `RollUp` picks them, so no request is counted twice), each with its step's start (`date_bin`), and sums them in Go into the total, each step, and each route and method, or, for one route from minute rows, each instance (hourly rows have none); latency buckets are summed element-wise, and percentiles come from them with `metrics.Percentile`, interpolated within a bucket.
   Summing in Go after one read was 2 to 3 times faster on local data than summing the buckets in SQL, which has to unnest each row's array (the timings are in the watchlist).
3. **Charts.** Requests per minute (per hour beyond 24 hours: each step's requests over the part of it that has passed, so the current hour isn't low), the share of 5xx (and of 4xx on a route's page), and p95 latency (p50, p95 and p99 on a route's page), a point per step (`endpointSteps`), with a gap where a step had no requests for the rates and latencies.
   Each is marked with the deploys that went live in the period, as on the service page (`markDeploys`, `deployMarkers`), the time axis running to the period's end so a deploy in its last step is marked too; without Render's settings, or with Render failing, a line under the charts says so.
4. **Routes.** A dense table of every route and method in the period, `(unmatched)` included: requests, the shares of 4xx and 5xx, p50, p95 and p99, and the average response size, sortable by each column through its header's link (`?sort=`, `routeRows`: numbers most first, a route with no value last, ties by route).
   Each route links to its page.
5. **A route's page.** Its charts, its latency histogram for the period, its requests by status class, its instances when there are several (minute rows only), and its 50 most recent server errors (`AdminEndpointsRepository.RouteErrors`, by route and method), which are kept by count, not age, so they don't follow the period: time, instance, panic or error, and the message's first line, cut to the column, with the whole message in a `<details>`.
   A route with no requests in the period, or none ever, shows that as an empty state, not a 404; a missing route or a method `metrics.NormalizeMethod` wouldn't record is a 404.
   A GET route with page views in the period, which only the reader's pages send, links to its web performance page (`webPerfLink`, `AdminWebPerfRepository.HasViews`).

**Start at:**

1. `internal/admin/endpoints.go` (`endpointsHandler`, `routeHandler`, `routeRows`).
2. `internal/psql/admin_endpoints.go` (`Endpoints`, `endpointRows`, `RouteErrors`).
3. `internal/ui/admin/endpoints.html`, `endpoint.html`, `partials/endpoints.html` and `web/admin/styles/partials/endpoints.css`.

**Tests:** `internal/admin/endpoints_test.go`; `internal/psql/admin_endpoints_test.go` (minute and hourly reads agree, and element-wise sums).

### Login

Admins are their own accounts (`admins`), apart from readers, and every admin page but the login's needs one logged in with a password and a second factor (see "The admin's login" in architecture.md).

1. **Enrolling.** `admin enroll -email <email>` (`runEnroll`) reads the password from stdin's first line and checks it like a new reader's (`CheckNewPassword`), refuses an email that's already an admin's, then starts an `admin.Enrollment`: a new TOTP secret, shown as its provisioning URI and as a QR code (`cmd/admin/qr.go`, drawn with half blocks, black on white).
   It reads codes from the next lines until one matches (3 tries); only then does `Enrollment.Finish` store the admin in one write, with the secret sealed under `-secret-key`, the code's step as the last one used, and 10 new recovery codes' hashes, which it prints once.
   With `-reset` it replaces an existing admin's password, secret and recovery codes instead, and deletes every admin session in the same transaction (`AdminRepository.Reset`).
2. **Password.** `POST /login` (`loginFormHandler`) counts the attempt against `loginLimiter`, checks the form, then `AdminRepository.Authenticate`; on success it renews the session token and stores the admin as pending, with the time, and redirects to `/login/code`.
3. **Code.** `POST /login/code` (`loginCodeFormHandler`) takes the pending admin (5 minutes at most, else back to Log in), counts the attempt against `loginLimiter` and `codeFailureLimiter` (refunded when the code works), and `checkCode`: a recovery code (`normalizeRecoveryCode` accepts any case, spaces or dash) is deleted as it's used; a 6-digit code is checked with `Verify` against the opened secret and its step recorded with `AcceptTOTPStep`, which refuses a step already used, even by a concurrent request.
   Then it renews the token, stores the admin and when the code was entered, and returns to the page `requireAuthentication` remembered, or the Overview.
4. **Each request.** `authenticate` loads the session's admin (a deleted admin's session is logged out) for `requireAuthentication` and the nav, which shows the sections, the admin's email and Log out only when someone is logged in.
5. **Confirming.** `requireRecentCode`, on a destructive action's page and its form at the same path, sends the admin to `/login/code?next=<path>` ("Confirm it's you") when their last code is over 10 minutes old, and the code returns them there.
6. **Logging out.** `POST /logout` renews the token, removes the admin from the session and shows Log in with a flash.

**Start at:**

1. `cmd/admin/enroll.go` and `internal/admin/enroll.go`.
2. `internal/admin/login.go`, `sessions.go` and `middleware.go` (`authenticate`, `requireAuthentication`, `requireRecentCode`).
3. `internal/psql/admins.go`.
4. `internal/ui/admin/login.html` and `login_code.html`.

**Tests:** `internal/admin/login_test.go`, `enroll_test.go`, `totp_test.go` and `seal_test.go`; `cmd/admin/enroll_test.go` and `qr_test.go`; `internal/psql/admins_test.go`; `internal/ratelimit/ratelimit_test.go`.

### Audit log

Every admin action and login is recorded in `admin_audit` (see "The admin's audit log" in architecture.md), and `/audit` shows them.

1. **Logins.** `loginFormHandler` records a wrong email or password as `login_failed` with the submitted email, and `loginCodeFormHandler` a wrong code as `login_failed` with the admin; both through `s.audit`, which records them by the request's actor (`s.actor`: the logged-in admin, `via = 'web'` and the client IP) or, before anyone is logged in, by the admin or email given, and a failure to record is logged, not shown.
   A correct code records `login` before the session holds the admin, so a login that can't be recorded fails (500); `logoutHandler` records `logout`, and logs out even when that can't be recorded.
2. **Actions.** An action that changes something passes who did it, an `rss.AuditActor`, to the repository method that makes the change, which records what it did (the action, the target, and the fields it changed before and after) in the same transaction, so the same change is recorded the same way from the web or the command line.
   The commands that write pass `cliActor` (`via = 'cli'`, no admin): `admin enroll` (`admin.create`, `admin.reset`, from `Enrollment.Finish`), and `admin create-user` (`user.create`); web actions pass `s.actor(r)`, like sending a wave of invites (`invite.wave`, with its label, count and entries) or the feeds and sites pages' fixes (`feed.reset_backoff` and others, under "Feeds and sites").
3. **Typed confirmation.** A destructive action's form has a field where the admin types the target's name, and `confirmDestructive` checks it (ignoring case and the spaces around it) before acting, with "Type {name} to confirm."; no action uses it yet.
4. **The page.** `GET /audit` (`auditHandler`) lists entries newest first, 100 at a time, with an Older link whose cursor (`?before=`) is the last entry's ID (`AuditRepository.List`, keyed on `occurred_at` and `id`), filtered by `?action=`, `?target_type=` and `?target_id=`.
   Each row shows the time (UTC), the admin, the action, the target with a History link (`auditHistoryURL`, which a record's admin page links to once one exists) and each changed field's values before and after as JSON, and the client IP or "Command line".

**Start at:**

1. `internal/admin/audit.go` (`audit`, `confirmDestructive`, `auditHandler`).
2. `internal/psql/audit.go` (`recordAudit`, `List`) and the repository methods that call `recordAudit`.
3. `internal/ui/admin/audit.html`.

**Tests:** `internal/admin/audit_test.go`, `audit_page_test.go` and `login_test.go` (`TestLoginIsAudited`); `internal/psql/audit_test.go` (`TestAuditInTransaction`); `cmd/admin/enroll_test.go`, `invite_test.go` and `create_user_test.go`.

### Observability

`www` records its own request, background job and runtime metrics, its recent server errors and real-user metrics from every page in Postgres for the admin, and the admin checks `www`'s availability from inside Render's network; the admin's Web performance page reads the real-user metrics, its Service page the availability checks and runtime metrics, and its Endpoints pages the request metrics and server errors (above).

1. **Counting.** `recordRequest` (`internal/server/middleware.go`) reports each request to `metrics.Requests.Observe`: its chi route pattern (never its URL), method, status, duration and size.
   `Observe` counts it in memory under the minute it finished, its route, its method (unknown methods fold into `OTHER`, so clients can't add rows) and its status class, with its latency in the `metrics.DurationBounds` histogram.
   Each background runner reports each job it finishes to `metrics.Jobs.Observe` the same way: its runner, its outcome and its duration (the outcomes are under "Background work" in architecture.md).
2. **Writing.** `Requests.Run` and `Jobs.Run` write the counts at the start of each minute (`MetricsRepository.SaveRequests` and `SaveJobs`, which add to a row already there), and `metrics.Runtime` writes a sample of the process's memory, goroutines, GC pause p99 and connection pool.
3. **Server errors.** `serverErrorHTML` and `serverErrorJSON` (`internal/server/errors.go`) hand the error behind each 500 to `metrics.ErrorLog.Record` (`recordError`), with its route pattern and method, and marked as a panic when `recoverPanic` caught it (`panicError`); `ErrorLog.Run` writes it to `server_errors` (`ServerErrorRepository.Record`, which cuts the message to 2,000 bytes) without the response waiting.
4. **Housekeeping.** `metrics.Housekeeper` rolls minute rows into hourly ones after 2 hours and deletes them after 7 days, hourly rows after 90 and runtime rows after 30, and keeps the newest 1,000 server errors (see "Background work" in architecture.md).
5. **Server-Timing.** `serverTiming` gives HTML responses `Server-Timing: app;dur=<ms>`, so the browser's timings (DevTools, and the real-user metrics below) can split time to first byte into server and network.
6. **Measuring a page view.** Every page's entry sets up `rum.ts` first (`setupRUM`), which does nothing in a browser without `sendBeacon`.
   `web-vitals` reports every change to TTFB, FCP, LCP, INP and CLS, and `rum.ts` keeps the latest of each, the LCP element's tag name and the slowest interaction's target, named by its control's first `data-*` attribute or its tag.
7. **The beacon.** The first time the page is hidden (`visibilitychange`, or `pagehide`), it sends the view once to `POST /rum` with `navigator.sendBeacon`: its path, the metrics, the navigation entry's type (or `prerender`), load timings, transfer size, protocol and `Server-Timing` `app` duration, and the viewport width, whether it's installed, the effective connection type and another site's referrer host.
   A page restored from the back/forward cache starts a new view, `nav=restore`, with only what `web-vitals` measures for the restore, sent when it's next hidden.
   A Back or Forward that loaded the page again (`back_forward`) also sends why it wasn't restored, where the browser says (`notRestoredReasons`, Chrome only): the top-level frame's reasons, comma separated, never its frames'.
8. **Storing it.** `rumHandler` (`internal/server/rum.go`) always answers 204; it drops the beacon over `rumLimiter`'s or `rumTotalLimiter`'s limit, or when its path resolves to no page (`rumRoute`), or its navigation type or width isn't valid.
   It keeps the route pattern, a device class from the width, and the browser's family and major version from the User-Agent (`parseBrowser`), cuts numbers to their ranges (outside them, NULL) and checks every other field against a set or a pattern, keeping a `back_forward` view's first 3 distinct reason names (`rumNotRestored`), then stores the row (`RUMRepository.Record`).
9. **Rollup.** The housekeeper counts each UTC day's views into `rum_daily`, one row per route, device, browser and metric with its views per `metrics.RUMBounds` bucket, an hour after the day ends (`RUMRepository.RollUp`), deletes views after 30 days and daily rows after 13 months, and keeps at most the newest 500,000 views (`Trim`).
10. **Availability.** With `-app-internal-url` set, `admin serve` runs `admin.Pinger`, which requests `www`'s `/healthcheck` every minute and stores the outcome in `availability_checks` (`AvailabilityRepository.Record`): ok when `www` answers `available` within 5 seconds, else its status, `timeout` or `error` (details under "Background work" in architecture.md).
    It deletes checks after 90 days.

**Start at:**

1. `internal/server/middleware.go` (`recordRequest`, `serverTiming`) and `errors.go` (`recordError`).
2. `internal/metrics/requests.go`, `jobs.go`, `runtime.go`, `errors.go`, `housekeeper.go` and `rum.go`.
3. `internal/psql/metrics.go` (`RollUp`), `server_errors.go` and `rum.go`.
4. `web/app/features/rum.ts` and `internal/server/rum.go`.
5. `internal/admin/pinger.go` (`Pinger.Check`) and `internal/psql/availability.go`.

**Tests:** `internal/metrics/*_test.go`; each runner's `TestRunMetrics`, `TestProcessMetrics`, `TestRefreshMetrics`, `TestCheckMetrics` or `TestSendMetrics`; `internal/psql/metrics_test.go`, `server_errors_test.go` and `rum_test.go`; `internal/server/middleware_test.go` (`TestRecordRequest`, `TestServerTiming`), `errors_test.go` and `rum_test.go`; `web/app/features/rum.test.ts`; `internal/admin/pinger_test.go` (`TestPingerReadsWWWsHealthcheck` reads the answers `internal/server`'s `TestHealthcheckFixture` writes, `-update`), `internal/psql/availability_test.go` and `cmd/admin/config_test.go`.

**Why:** "Observability" in decisions.md.

**Why:** "Admin" in decisions.md.

## Adding a feature

The usual path for a new page or action, following the conventions in CLAUDE.md:

1. **Schema:** a new migration (`make db/migrations/new name=<name>`), applied with `go run ./cmd/www migrate`.
2. **Domain type:** add or extend a type in `internal/rss`, and a sentinel error in `errors.go` if handlers need to tell a case apart.
3. **Repository:** a method in `internal/psql` with a DB test.
4. **Interface:** add the method to the store interface in `internal/server/server.go` (or a new interface, a `Services` field, the compile-time check, and the wiring in `cmd/www/application.go`), and a function field to the fake in `fakes_test.go`.
5. **Handler and route:** a method on `*Server`, registered in `router.go` (see "Code conventions" in CLAUDE.md for forms, flash messages and error pages).
6. **Template:** a page in `internal/ui/templates`, with shared parts in `partials/`.
7. **Tests:** handler tests with fakes for the new behavior, and DB tests for the repository method.
8. **Docs:** see "Docs" in CLAUDE.md for which files to update.
