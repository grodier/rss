# Error states

Every error and empty state a user can reach, with how to trigger it locally, what it shows, and the verdict of its last review.
It's the checklist for an error-state pass: reproduce each row, at desktop width and at 375 px, and update its verdict.
Unlike the other docs, it quotes user-facing text, since judging that text is the point.

**Last pass:** 2026-10-30, the first one (#392), at 1280 px and 375 px in Chromium.
States marked "code read" couldn't be reproduced locally and were checked by reading the code.

Keep it up to date: a PR that adds, removes or changes a user-visible failure or empty state updates its row in the same PR (see "Docs" in CLAUDE.md).
A pass repeats every row and moves the date above; the fixes it finds become issues (see "Error-state walkthrough" in [backlog.md](backlog.md)).

## Running a pass

- Start from an empty database (`make db/reset`, which also starts Mailpit) and run the app with an invite code: `go run ./cmd/www -invite-code $(openssl rand -hex 16)`.
  With the README's `RSS_SMTP_` settings, emails and their links show up in Mailpit (http://localhost:8025); without an SMTP host, they're written to the app's log.
- Make two accounts at `/signup?invite=<code>`: **A**, the one you review with, and **B**, to open A's pages as someone else.
  A new account is unconfirmed, which the confirmation rows need; confirm A when you get to E4.
- Rate limits are in memory and per process, so hitting one locks you out of that flow until the window ends; do those rows last, or restart the app (which resets them) between them.
- Feed states need rows the refresh workers won't touch.
  This makes a site with a working, a failing, a gone and a never-fetched feed, subscribes A to them and adds a few articles; run it with `psql "$RSS_DB_DSN" -v email=<A's email>`:

  ```sql
  INSERT INTO sites (host, url, title) VALUES ('ok.example', 'https://ok.example/', 'OK Site'), ('empty.example', 'https://empty.example/', 'Empty Site');
  INSERT INTO feeds (site_id, url, title, next_fetch_at, last_fetched_at, last_attempt_at, consecutive_failures, gone_at)
  SELECT s.id, f.url, f.title, now() + interval '1 day', f.fetched, f.fetched, f.failures, f.gone
  FROM sites s, (VALUES
    ('https://ok.example/feed', 'OK Feed', now() - interval '1 hour', 0, NULL::timestamptz),
    ('https://ok.example/fail1', 'Failing Once', now() - interval '1 hour', 1, NULL),
    ('https://ok.example/fail3', 'Failing Thrice', now() - interval '1 hour', 3, NULL),
    ('https://ok.example/gone', 'Gone Feed', now() - interval '1 hour', 0, now()),
    ('https://ok.example/never', 'Never Fetched', NULL, 0, NULL)
  ) AS f(url, title, fetched, failures, gone)
  WHERE s.host = 'ok.example';
  INSERT INTO subscriptions (user_id, feed_id) SELECT u.id, f.id FROM users u, feeds f WHERE u.email = :'email' AND f.url LIKE 'https://ok.example/%';
  INSERT INTO articles (feed_id, external_id, url, title, summary, timeline_at)
  SELECT f.id, a.id, a.url, a.title, a.summary, now() - a.age
  FROM feeds f, (VALUES
    ('a1', 'https://ok.example/a1', 'An article', '<p>Text</p>', interval '1 hour'),
    ('notext', 'https://ok.example/notext', 'No text', '', interval '2 hours'),
    ('nourl', '', 'No URL', '<p>Text</p>', interval '3 hours'),
    ('nothing', '', '', '', interval '4 hours')
  ) AS a(id, url, title, summary, age)
  WHERE f.url = 'https://ok.example/feed';
  ```

- For each row ask: does the user know what happened and what to do next?
  Is the message free of internals (SQL, Go errors, `last_error`)?
  Is the status code right?
  Does a form keep what they typed (never passwords)?
  Is focus on the error, and is it announced (`role="alert"`, `aria-describedby`)?
  Does it fit at 375 px without scrolling sideways?
- Verdict: `ok`, or `needs work: <one line>`.

In triggers, `{feed}` and the like are the ID of a row you made; `{uuid}` is any UUID that isn't one, such as `00000000-0000-0000-0000-000000000000`.

## Forms

Every form below re-renders with 422 on invalid input, keeps what was typed except passwords, puts focus on the first field in error, and ties each error to its field with `aria-invalid` and `aria-describedby`, unless the row says otherwise.

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| F1 Signup: invalid email | Signup with email `ann` | Signup, 422, under Email | "Email must be a valid email address" | Yes | ok |
| F2 Signup: short password | Password `short` | Signup, 422, under Password | "Password must be at least 8 characters long" | Yes | ok |
| F3 Signup: common password | Password `password123` | Signup, 422, under Password | "This password is too common. Try a longer one, or a few unrelated words." | Yes | ok |
| F4 Signup: email as password | Password the same as the email | Signup, 422, under Password | "Your password can't be your email address." | Yes | ok |
| F5 Signup: name blank or too long | Name blank, or 101 characters | Signup, 422, under Name | "Name is required"; "Name must not be more than 100 characters long" | Yes | ok |
| F6 Signup: missing or wrong invite code | `/signup` without `?invite=`, or `?invite=wrong` | Signup, 422, under Invite code | "Invite code is required"; "This invite code isn't valid" | Only with the right code; nothing says who to ask | needs work: point to the support address for a code |
| F7 Signup: email already has an account | A's email | Signup, 422, under Email, with links to log in and reset the password | "There's already an account with this email." | Yes | ok (says so on purpose; "Accounts" in decisions.md) |
| F8 Signup: turned off | Run without `-invite-code`, open an invite link | 404 error page | "Page not found" / "The page you're looking for doesn't exist." | No: an invite link from before looks broken | needs work: say signup is closed (discuss: no page links to `/signup` on purpose) |
| F9 Signup: rate limited | 11 signup posts from one IP in an hour, valid or not | Signup, 429 with `Retry-After`, alert above the form | "Too many attempts. Try again in 60 minutes." | Yes, after the wait; fields kept, alert announced | ok |
| F10 Login: unknown email | Log in as `nobody@example.com` | Login, 422, under Email | "There's no account with this email." | Yes | ok |
| F11 Login: wrong password | A's email, wrong password | Login, 422, under Password | "That password isn't right." | Yes; "Forgot password?" is on the form | ok |
| F12 Login: empty or invalid email | Submit empty, or email `ann` | Login, 422, under each field | "Email is required"; "Email must be a valid email address"; "Password is required" | Yes | ok |
| F13 Login: rate limited per IP | 21 login posts from one IP in 10 minutes | Login, 429 with `Retry-After`, alert with a reset link | "Too many attempts. Try again in 10 minutes." | Yes, after the wait or by resetting | needs work: the alert's "Reset it" link repeats the form's "Forgot password?" link just below |
| F14 Login: rate limited per email | 11 wrong passwords for one email in 15 minutes | Login, 429, as F13 | "Too many attempts. Try again in 15 minutes." | As F13 | needs work: as F13 |
| F15 Form posted from another site | `curl -X POST -H 'Sec-Fetch-Site: cross-site' -H 'Origin: https://evil.example' -d name=x localhost:8080/settings/name` | 403 error page | "Request blocked" / "This form was submitted from another site, so it was rejected for your security." | Only "Go to the home page" | ok |
| F16 Malformed form body | `curl` a post with body `name=%zz` (with an `Origin` matching the app) | 400 error page | "Bad request" / "That form submission couldn't be read." | Only "Go to the home page" | needs work: HTTP jargon as the heading (see H1) |
| F17 Form body over 64 KiB | `curl` a post with a 70,000-character field | 413 error page | "Request too large" / "That form submission was too large." | Back button only | needs work: HTTP jargon as the heading (see H1) |

## Auth and sessions

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| S1 Login-only page while logged out | Open `/feeds` in a private window | 303 to Login | None | Yes: login returns to `/feeds` | needs work: nothing says the page needs logging in |
| S2 Session ended mid-action | Open Settings → Name, then `DELETE FROM user_sessions WHERE user_id = '{A}'` (or "Log out other devices" elsewhere), then save | 303 to Login | None | Partly: what they typed is lost, and after login they land on Home, not the form (only GETs are remembered) | needs work: say they were logged out; return to the page the form was on |
| S3 Session idle for 90 days | `UPDATE user_sessions SET last_seen_at = now() - interval '91 days' WHERE user_id = '{A}'`, open `/` | Logged-out landing page, 200 | None | Yes, by logging in | needs work: as S2 |
| S4 Save or Star after the session ended (JavaScript) | Open Home, run S2's `DELETE`, click Save on a row | The button turns on; the post was redirected to Login | None | No: it looks saved but isn't (`app.js` counts the redirect to `/login` as success) | needs work: bug, treat a redirect to Login as a failure (#429) |
| S5 Older articles after the session ended (JavaScript) | Code read: infinite scroll gets a redirect | The timeline stops loading more; "Older articles" stays | None | Yes: clicking it goes to Login | ok |
| S6 Log out | Settings → Log out | Login, flash | "You've been logged out successfully!" | n/a | ok (flash not announced; see N1) |
| S7 Logged in, opens Login or Signup | `/login` or `/signup?invite=<code>` | 303 to Home | None | n/a | ok |

## Account emails and settings

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| E1 Confirmation link unknown, mangled or without token, or for an address the account no longer has | `/confirm-email?token=garbage`; or change A's email, then open a confirmation link sent before | Link-expired page, 410 | "This link has expired" / "Confirmation links work for 7 days and only once." | Yes: the action fits who's asking (E2–E4) | ok (a mangled link also says "expired") |
| E2 Confirmation link used again, logged out | Open a used confirmation link in a private window | As E1 | As E1, with "Log in to get a new link" | Yes | ok |
| E3 Confirmation link expired, logged in and unconfirmed | `UPDATE user_tokens SET expires_at = now() - interval '1 minute' WHERE purpose = 'confirm_email'`, open the link | As E1 | As E1, with a "Send a new link" button | Yes | ok |
| E4 Confirmation link used again, already confirmed | Open the link again after confirming | As E1 | As E1, plus "Your email address is already confirmed." and "Go to Settings" | n/a | ok |
| E5 Unconfirmed address notice | A new account's Home and Settings | Notice with a Resend button | (the notice) | Yes | ok |
| E6 Resend confirmation | Resend from the notice | Flash on the same page | "We sent a new link to {email}." | n/a | ok |
| E7 Resend rate limited | A 4th resend in an hour | Flash | "We've already sent you several links. Check your spam folder, or try again in an hour." | Yes | ok |
| E8 Reset: unknown email | Forgot password with `nobody@example.com` | Forgot password, 422, under Email | "There's no account with this email." | Yes | ok (says so on purpose) |
| E9 Reset: invalid email | Email `nope` | Forgot password, 422 | "Email must be a valid email address" | Yes | ok |
| E10 Reset: rate limited | A 4th reset for one email in an hour, or an 11th from one IP | Forgot password, 429, alert | "Too many attempts. Try again in 60 minutes." | After the wait | ok |
| E11 Reset link unknown, used, expired, or for an address the account no longer has | `/password/reset?token=garbage`, or `UPDATE user_tokens SET expires_at = now() - interval '1 minute' WHERE purpose = 'reset_password'` | Link-expired page, 410 | "This link has expired" / "Reset links work for 1 hour and only once." + "Send a new link" | Yes | ok |
| E12 Reset: new password invalid | Password `short` on the reset page | Reset, 422, under Password | As F2–F4 | Yes; the password isn't echoed | ok |
| E13 An email over the outbox's hourly cap | `INSERT INTO emails (to_address, subject, text_body, html_body) SELECT '{email}', 'x', 'x', 'x' FROM generate_series(1, 20)`, then ask for a reset | "Check your email" page | "We sent a link to {email}. It works for 1 hour." | No: nothing was sent (only the log says so) | needs work (discuss): pages say an email was sent when the cap dropped it; same for Resend (E6) and the email change (E17) |
| E14 Change password: wrong current password | Settings → Password | 422, under Current password | "That password isn't right." | Yes | ok |
| E15 Change password: same as current, or empty | New password = current; or submit empty | 422 | "Your new password must be different from your current one."; "Enter your current password." with "Password is required" | Yes | needs work: two styles of "required" message on one form (see N2) |
| E16 Change email: wrong password, same address, taken, invalid | Settings → Email | 422, under the field | "That password isn't right."; "That's already your email address."; "There's already an account with this email."; "Email must be a valid email address" and "Enter your password." | Yes | ok |
| E17 Change email requested | A valid new address and password | Settings, flash | "We sent a link to {new}. Open it to finish changing your email." | n/a | ok |
| E18 Change-email link unknown, used or expired | `/email-change?token=garbage` | Link-expired page, 410 | "This link has expired" / "Links to change your email work for 24 hours and only once." + "Try again" | Yes | ok |
| E19 Change-email link, address taken since | Request a change, then give another account that address (`UPDATE users SET email = '{new}' WHERE id = '{B}'`), then confirm | 409 page | "That address is taken" / "Another account started using {new} in the meantime. Your email is still {old}." | No way on: the page has no link or button | needs work: add a way on (Settings) |
| E20 Settings rate limits | 11 password changes or deletion attempts in 10 minutes; 6 email changes in an hour | Each form, 429, alert | "Too many attempts. Try again in N minutes." | After the wait | ok (code read) |
| E21 Name blank | Settings → Name, blank | 422, under Name | "Name is required" | Yes | ok |
| E25 Theme: no choice or an unknown one | Settings → Theme, then in devtools change a radio's value to `sepia` and submit it (the radios always have one checked, so only a tampered post gets here) | 422, under the choices; System is focused | "Choose System, Light or Dark." | Yes | ok (2026-10-30, #413) |
| E22 Delete account: wrong or empty password | Settings → Delete | 422, under Password | "That password isn't right."; "Enter your password." | Yes | ok |
| E23 Account deleted | Delete with the right password | Logged-out landing page, flash | "Your account was deleted. Thanks for trying rss." | n/a | needs work: says "rss", not "Paper Round" (#419) |
| E24 Log out other devices, none | Settings → Log out other devices with one session | Settings, flash | "There were no other devices to log out." | n/a | ok |

## HTTP errors

Every error page is the same `.solo` panel with a heading, a sentence and "Go to the home page"; logged-in users lose the nav, and the tab title is "Paper Round - error" whatever the status.

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| H1 Error page in general | Any row below | Error page | (per row) | Only "Go to the home page" | needs work: tab title doesn't say what failed, headings are HTTP terms ("Bad request", "Method not allowed", "Request too large"), and the 500 doesn't say whether to retry |
| H2 404: unknown or malformed ID | `/feeds/{uuid}`, `/feeds/abc`, `/sites/{uuid}`, `/lookups/{uuid}`, `/articles/{uuid}`, `/tags/{uuid}`, `/timelines/{uuid}`, `/settings/imports/{uuid}` | 404 | "Page not found" / "The page you're looking for doesn't exist." | Home only | ok (see H1) |
| H3 404: another user's record | As B, open A's `/tags/{id}`, `/timelines/{id}`, `/settings/imports/{id}`, or `/feeds/{id}/edit` for a feed B doesn't follow | 404, as H2 | As H2 | Home only | ok |
| H4 404: unknown route | `/nope`, logged in or out | 404, as H2 | As H2 | Home only | ok |
| H5 405 | `curl localhost:8080/logout` | 405 with `Allow: POST` | "Method not allowed" / "That action isn't supported for this page." | Home only | needs work: see H1 |
| H6 403, 400, 413 | F15–F17 | | | | see F15–F17 |
| H7 429: lookups | 11 lookups in 10 minutes | 429 error page with `Retry-After` | "Too many requests" / "You've looked up a lot of sites recently. Please wait a few minutes and try again." | Home only; the query is lost | ok (decided: "Security" in decisions.md) — discuss: forms say how many minutes, this doesn't |
| H8 500 from a handler | Code read: any store error | 500 error page | "Something went wrong" / "The server encountered a problem and could not process your request." | Home only | needs work: see H1 |
| H9 Database down, with a session cookie | Stop Postgres, reload a logged-in page | 500, plain text | "Internal Server Error" | Reload later; nothing says so | needs work: scs's default error handler answers before ours; set `sessionManager.ErrorFunc` |
| H10 Database down, without a session | Stop Postgres, open `/`, `/privacy` logged out; then log in | Pages without the database work; posting Login gets H8 | As H8 | As H8 | ok |
| H11 Database down: health check | Stop Postgres, `curl localhost:8080/healthcheck` | 200 | `{"status":"available",…}` | n/a | needs work (discuss): the uptime monitor (#391) watches `/healthcheck`, which doesn't touch the database, so it misses an outage every logged-in page has (#430) |
| H12 Another host (production only) | Code read: a request for any host but `-base-url`'s | 308 to the same path on `-base-url` (#390); not `/healthcheck` | None | n/a | ok |

## Lookups and discovery

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| L1 Query that isn't a website address | Search `zzzz` | Search, 200 | "No sites or feeds match “zzzz”." and no lookup offered | The search box's placeholder mentions URLs; the result doesn't | ok |
| L2 Address that can't be looked up | Search `127.0.0.1`, `localhost:8080` or `ftp://example.com` | Search, 200, no lookup offered | As L1 | As L1 | ok (`LooksLikeURL` refuses them; a hostname that resolves to a private address fails as L4) |
| L3 Invalid address posted | Code read: only a crafted post to `/lookups` | Search, 422, above the search box | "Enter a website address like example.com" | Yes | needs work: the error isn't tied to the input (see R4) |
| L4 Lookup fails | Look up `nonexistent.invalid` (also: a timeout, too many redirects, a blocked or huge or non-HTML response) | Lookup page, 200 | "We couldn't reach {site}. Try again later." + "Back to search" | Not for an hour: a failed lookup is reused for an hour, and nothing says so | needs work: one message for every cause, and "later" means an hour |
| L5 Lookup in progress | Look up a slow site (or `INSERT INTO lookups (site_key, url) VALUES ('pending.example', 'https://pending.example/')`, open `/lookups/{id}`) | Lookup page, refreshes itself every 2 s | "Looking for feeds on {site}…" + "Refresh" | Waits | ok |
| L6 Lookup done, no feeds | Look up a site without feeds, or whose only feed doesn't parse (or the row above with `status = 'done'`) | Lookup page, 200 | "No feeds found on {site}." + "Back to search" | Yes | ok |
| L7 Lookup done with feeds | Look up a site with feeds | 303 to the site page | None | n/a | ok |
| L8 Lookup row gone | `/lookups/{uuid}` | 404 (H2) | As H2 | Home only | ok |
| L9 Site page with no feeds | `/sites/{empty.example}` | Site page, 200 | "No feeds found for this site yet." (and "Check again" when allowed) | Yes | ok |
| L10 Rate limited | H7 | | | | see H7 |

## OPML import

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| O1 No file | Settings → Import without a file (the input is `required`, so turn off validation or use `curl`) | Import, 422, under the file | "Choose an OPML file to import." | Yes | ok |
| O2 Not OPML | Upload a text file | Import, 422 | "That file isn't an OPML file. Export one from your other reader's settings." | Yes | ok |
| O3 No feeds | An OPML file with an empty `<body>` | Import, 422 | "That file has no feeds in it." | Yes | ok |
| O4 Over 1 MiB | A 1.5 MB OPML file | Import, 413 | "That file is too large. Imports can be up to 1 MB." | Yes | ok |
| O5 Over 1,000 feeds | An OPML file with 1,001 outlines | Import, 422 | "That file has more than 1,000 feeds. Split it into smaller files and import each one." | Yes | ok |
| O6 Rate limited | A 6th import in an hour | Import, 429, under the file | "You've started several imports in the last hour. Try again later." | After the wait, but it doesn't say how long | needs work: say how long, like the other limits |
| O7 Import running | Any valid import, at once | Import results, refreshes itself | "Importing {file}…" / "Checked N of M feeds." | Waits | ok |
| O8 Import with failures | Outlines with `xmlUrl="not a url"`, an unreachable host, and an address without a feed | Import results, 200 | Per feed: "Not a valid web address."; "Couldn't reach the site." + "Search for {host}"; "There's no feed at this address." | Yes | ok |
| O9 Another user's import | As B, open A's import URL | 404 (H3) | As H2 | Home only | ok |
| O10 Malformed upload | `curl` a post that isn't multipart | 400 error page | "Bad request" / "That form submission couldn't be read." | Home only | needs work: see H1 |

## Feeds

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| D1 Fetch failed once | "Failing Once" from the setup | Feed page, 200 | "The last attempt to fetch this feed failed ({time}). We'll keep trying." | Nothing to do | ok |
| D2 Fetch failing (3+ times) | "Failing Thrice" | My feeds: "This feed is failing to update."; feed page: "The last 3 attempts to fetch this feed failed, most recently {time}. We'll keep trying." | (as Where) | Nothing to do but wait or unsubscribe | ok |
| D3 Gone (410) | "Gone Feed" | My feeds: "This feed no longer exists."; feed page: "This feed no longer exists: its publisher removed it. We've stopped checking it." with no Refresh button | (as Where) | Unsubscribe | ok |
| D4 Never fetched, no articles | "Never Fetched" | Feed page, 200 | "Never fetched." and "No articles yet." | Waits | ok |
| D5 Refresh within 5 minutes of a fetch | `UPDATE feeds SET last_fetched_at = now() WHERE id = '{feed}'`, click Refresh | Feed page, flash | "This feed was refreshed in the last few minutes. Try again later." | Yes | ok |
| D6 Refresh fails | Refresh "OK Feed" (its host doesn't exist); a feed whose address returns a web page; one that answers 410 | Feed page, flash | "Couldn't reach this feed. Try again later."; "This feed's address didn't return a feed. Try again later."; "This feed no longer exists. We've stopped checking it." | Yes | ok |
| D7 Refresh a gone feed, or subscribe to one | `curl` a post to `/feeds/{gone}/refresh` or `/subscribe` (the buttons aren't shown) | Feed page, flash | "This feed no longer exists, so it can't be refreshed."; "…so you can't subscribe to it." | n/a | ok |
| D8 Refresh rate limited | 11 refreshes in 10 minutes | Feed page, flash | "You've refreshed a lot of feeds recently. Please wait a few minutes and try again." | After the wait | ok |
| D9 Refresh while one is running | Code read: click Refresh twice | The second waits, then gets D5 if the first succeeded | As D5 | Yes | ok |
| D10 Feed page at 375 px, subscribed | Any feed A follows, at 375 px | Feed page | n/a | The Unsubscribe button is cut off and the page scrolls sideways | needs work: the profile's buttons overflow below about 405 px (#431) |

## Search

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| R1 Empty query | `/search` or `/search?q=` | Search, 200 | Just the search box | n/a | ok |
| R2 No results | `/search?q=zzzz` | Search, 200 | "No sites or feeds match “zzzz”." | Yes (L1) | ok |
| R3 Special characters | `/search?q=%25`, `_`, `"<script>\` | Search, 200 | As R2; matched literally and escaped | Yes | ok |
| R4 Query over 200 characters | `/search?q=` + 201 letters | Search, 422, above the search box | "Search must be 200 characters or fewer" | Yes; the query is kept | needs work: the error isn't tied to the input (no `aria-invalid`, `aria-describedby` or focus) and isn't announced |

## Empty states

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| Y1 Home, no subscriptions | A new account's `/` | Home, 200 | "Your timeline is empty. Find feeds to follow with Search, or import your subscriptions from another reader; their articles will show up here." | Yes, with links | ok |
| Y2 My feeds, none | New account's `/feeds` | 200 | "You're not subscribed to any feeds yet. Search for a site to find some." | Yes | ok |
| Y3 Saved, none | New account's `/saved` | 200 | "Nothing saved yet. Use the bookmark on any article to keep it here." | Yes | ok |
| Y4 Saved search, no matches | `/saved?q=zzz` with saves | 200 | "No saved articles match “zzz”." | Yes | ok |
| Y5 Tags, none | New account's `/tags` | 200 | "You don't have any tags yet. Tags group your feeds, for example "News" or "AI", so you can make timelines from them." | Yes | ok |
| Y6 Timelines, none | New account's `/timelines` | 200 | "You don't have any timelines yet. A timeline shows some of your feeds, for example everything tagged “News”." | Yes | ok |
| Y7 Site page, no feeds | L9 | | | | see L9 |
| Y8 Feed, no articles | D4 | | | | see D4 |

## Subscriptions

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| U1 Subscribe or unsubscribe succeeds | Click Subscribe / Unsubscribe | Same page, flash | "Subscribed to {feed}."; "Unsubscribed from {feed}." | n/a | ok |
| U2 Subscribe to an unknown feed | `curl` a post to `/feeds/{uuid}/subscribe` | 404 (H2) | As H2 | Home only | ok |
| U3 Subscribe or unsubscribe fails | Code read: a store error | 500 (H8) | As H8 | As H8 | ok (see H1) |
| U4 Edit: name too long | Edit subscription, name of 101 characters | 422, under Name | "Must be 100 characters or fewer." | Yes | ok |
| U5 Edit: new tag too long, or over 100 tags | New tag of 51 characters; or 100 tags already | 422, under New tags | "Each tag must be 50 characters or fewer."; "You can have up to 100 tags." | Yes | ok |
| U6 Edit a feed you don't follow | As B, `/feeds/{A's feed}/edit` | 404 (H3) | As H2 | Home only | ok |

## Reading

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| P1 Empty timeline | Y1 | | | | see Y1 |
| P2 No older articles | `/?before=2000-01-01T00:00:00Z_00000000-0000-0000-0000-000000000000` (also feed pages, Saved) | 200 | "No older articles." (Saved: "No older saved articles.", "No older matches.") | Yes | ok |
| P3 Invalid `before` cursor | `/?before=garbage`, `/feeds/{feed}?before=garbage`, `/saved?before=garbage` | 400 error page | "Bad request" / "That link to older articles isn't valid." | Home only | needs work: see H1 |
| P4 Article with no text in its feed | The `notext` article | Article page, 200 | "This article has no text in its feed." + "Read on {site}" | Yes | ok |
| P5 Article with no URL | The `nourl` article | Article page, 200, no "Read on" | None | n/a | ok |
| P6 Article with no title, text or URL | The `nothing` article | Article page, 200 | "(untitled)" and P4's text | n/a | ok |
| P7 Unknown or malformed article | `/articles/{uuid}`, `/articles/abc` | 404 (H2) | As H2 | Home only | ok |
| P8 Read ping fails | `curl` a post to `/articles/{uuid}/read` | 404, unseen | None | n/a | ok (silent by design) |
| P9 Save or Star fails | Without JavaScript: a post for `/articles/{uuid}/save`; with it, see S4 | 404 (H2) | As H2 | Home only | ok (S4 is the problem with JavaScript) |

## Timelines

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| T1 Name blank or too long; description too long | New timeline | 422, under the field | "Enter a name."; "Must be 50 characters or fewer."; "Must be 280 characters or fewer." | Yes | ok |
| T2 No tags or feeds chosen | "Only feeds with these tags…" with none ticked | 422, under Show | "Choose at least one tag or feed, or show all your feeds." | Yes | ok |
| T3 Bad "at most N posts a week" | `0` or `101` | 422 | "Enter a whole number from 1 to 100, or leave it blank." | Yes | ok |
| T4 Bad words or phrases | Include words `!!!`, a 51-character word, or 21 words | 422 | "Each word or phrase needs at least one letter or number."; "Each word or phrase must be 50 characters or fewer."; "At most 20 words or phrases." | Yes | ok |
| T5 Over 20 timelines | `INSERT INTO timelines (user_id, name) SELECT '{A}', 't' \|\| g FROM generate_series(1, 20) g`, then New timeline | 422, alert above the form | "You can have up to 20 timelines." | Only by deleting one; found out after filling the form | ok |
| T6 A tag or feed deleted in another tab before saving | Code read | 404 (H2) | As H2 | Home only | ok (rare) |
| T7 Empty custom timeline | A timeline whose words match nothing | 200 | "Nothing in this timeline yet." | Edit is linked above | ok |
| T8 Timeline with no feeds to show | A timeline limited to a tag with no feeds | 200 | "This timeline has no feeds to show. Edit it to choose some." | Yes | ok |
| T9 Starred-only, nothing starred | The Starred preset with no stars | 200 | "Nothing starred here yet. Star articles in your timelines and they'll show up here." | Yes | ok |
| T10 Unknown or another user's timeline | `/timelines/{uuid}`; as B, A's | 404 (H2) | As H2 | Home only | ok |

## Tags

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| G1 Name blank, with a comma, or too long | New tag, or rename | 422, under Name | "Enter a name."; "Tags can't contain commas."; "Must be 50 characters or fewer." | Yes | ok |
| G2 Duplicate name | A tag that exists, in any case | 422, under Name | "You already have a tag called “news”." | Yes | ok |
| G3 Over 100 tags | `INSERT INTO tags (user_id, name) SELECT '{A}', 't' \|\| g FROM generate_series(1, 100) g`, then New tag | 422, under Name | "You can have up to 100 tags." | Only by deleting one | ok |
| G4 Tag page without subscriptions | A tag on an account with no feeds | 200 | "You're not subscribed to any feeds yet. Search for a site to find some." | Yes | ok |
| G5 Unknown or another user's tag | `/tags/{uuid}`; as B, A's | 404 (H2) | As H2 | Home only | ok |

## Shared timelines

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| X1 Share with an unconfirmed address | B's timeline → Edit: the Sharing section offers Resend instead; or `curl` a post to `/timelines/{id}/share` | Edit page, flash | "Confirm your email address to share timelines." | Yes | ok |
| X2 Unknown or malformed link | `/shared/AAAA…` (43 characters), `/shared/x` | 404 (H2) | As H2 | Home only (the logged-out landing page) | ok |
| X3 Link after Stop sharing or New link | Open the old link and its `feed.atom` | 404 (H2); the feed's 404 is HTML | As H2 | n/a | ok (decided: "Sharing" in decisions.md) — discuss: the page is cached for 5 minutes and the feed for 15, so "The links no longer work" isn't true at once for whoever loaded them |
| X4 Sharing turned on, off, new link | Edit → Sharing | Edit page, flash | "Sharing turned on."; "Sharing turned off. The links no longer work."; "New link made. The old one no longer works." | n/a | ok |

## Across pages

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| N1 Flash messages | Any flash above (S6, E6, D5, U1, …) | Top of the page after a redirect | (per row) | n/a | needs work: `.flash` has no `role="status"`, so screen readers don't announce it, including failures (D6) |
| N2 Required-field messages | F12, E15, E22, T1, G1 | | "Email is required" and "Name is required" beside "Enter your password." and "Enter a name." | n/a | needs work: two styles, with and without a final period |
