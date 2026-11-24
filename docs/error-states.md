# Error states

Every error and empty state a user can reach, with how to trigger it locally, what it shows, and the verdict of its last review.
It's the checklist for an error-state pass: reproduce each row, at desktop width and at 375 px, and update its verdict.
Unlike the other docs, it quotes user-facing text, since judging that text is the point.

**Last pass:** 2026-10-30, the first one (#392), at 1280 px and 375 px in Chromium.
States marked "code read" couldn't be reproduced locally and were checked by reading the code.

Keep it up to date: a PR that adds, removes or changes a user-visible failure or empty state updates its row in the same PR (see "Docs" in CLAUDE.md).
A pass repeats every row and moves the date above; the fixes it finds become issues (see "Error-state walkthrough" in [backlog.md](backlog.md)).

## Running a pass

- Start from an empty database (`make db/reset`, which also starts Mailpit) and run the app: `go run ./cmd/www`.
  With the README's `RSS_SMTP_` settings, emails and their links show up in Mailpit (http://localhost:8025); without an SMTP host, they're written to the app's log.
- Make two accounts with `go run ./cmd/admin create-user` (see the README): **A**, the one you review with, and **B**, to open A's pages as someone else.
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
| F6 Signup: no invite | `/signup` without `?invite=` | Message page, 200, with a "Join the waitlist" button, no form | "Paper Round is invite-only for now" | Yes, by joining the waitlist | new (#569), not yet reviewed |
| F7 Signup: email already has an account | A's email | Signup, 422, under Email, with links to log in and reset the password | "There's already an account with this email." | Yes | ok (says so on purpose; "Accounts" in decisions.md) |
| F8 Signup: invite expired | In the admin, Invites → New with the shortest expiry (1 day), then expire it in the local database (`UPDATE invites SET expires_at = now() WHERE id = '<id>'`) and open its link | Message page, 410, no form | "This invite has expired" / "Ask whoever invited you for a new one." | Only with a new invite | new (#569), not yet reviewed |
| F10 Signup: invite already used | Open the link of an invite someone signed up with | Message page, 410, with a "Log in" button, no form | "This invite has already been used" | Yes, if it was theirs: by logging in | new (#569), not yet reviewed |
| F11 Signup: invite revoked or unknown | Revoke an invite on the admin's Invites page, open its link; or `?invite=wrong` | Message page, 410, no form | "This invite is no longer valid" | Only with a new invite | new (#569), not yet reviewed |
| F12 Signup: invite used up while the form was open | Open one invite's link in two tabs, sign up in one, then submit the other | Message page, 422, with a "Log in" button, no form | "This invite has already been used" | As F10 | new (#569), not yet reviewed |
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
| S2 Session ended mid-action | Open Settings → Account → Name, then `DELETE FROM user_sessions WHERE user_id = '{A}'` (or "Log them out" under Other devices elsewhere), then save | 303 to Login | None | Partly: what they typed is lost, and after login they land on Home, not the form (only GETs are remembered) | needs work: say they were logged out; return to the page the form was on |
| S3 Session idle for 90 days | `UPDATE user_sessions SET last_seen_at = now() - interval '91 days' WHERE user_id = '{A}'`, open `/` | Logged-out landing page, 200 | None | Yes, by logging in | needs work: as S2 |
| S4 Save or Star after the session ended (JavaScript) | Open Home, run S2's `DELETE`, click Save on a row | 303 to Login (`save-star.ts` gets the login page instead of a 204, so it puts the button back and submits the form normally) | None | Yes: nothing is saved, and the button, which turned on as it was pressed, goes back off before Login loads; after login they land on Home (as S2) and can Save again | changed in #775 (was "ok (saying they were logged out is S2's)"), not reviewed |
| S5 Older articles after the session ended (JavaScript) | Code read: infinite scroll gets a redirect | The timeline stops loading more; "Older articles" stays | None | Yes: clicking it goes to Login | ok |
| S6 Log out | Settings → Log out | Login, flash | "You've been logged out successfully!" | n/a | ok (flash not announced; see N1) |
| S7 Logged in, opens Login or Signup | `/login` or `/signup?invite=<token>` | 303 to Home | None | n/a | ok |

## Account emails and settings

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| E1 Confirmation link unknown, mangled or without token, or for an address the account no longer has | `/confirm-email?token=garbage`; or change A's email, then open a confirmation link sent before | Link-expired page, 410 | "This link has expired" / "Confirmation links work for 7 days and only once." | Yes: the action fits who's asking (E2–E4) | ok (a mangled link also says "expired") |
| E2 Confirmation link used again, logged out | Open a used confirmation link in a private window | As E1 | As E1, with "Log in to get a new link" | Yes | ok |
| E3 Confirmation link expired, logged in and unconfirmed | `UPDATE user_tokens SET expires_at = now() - interval '1 minute' WHERE purpose = 'confirm_email'`, open the link | As E1 | As E1, with a "Send a new link" button | Yes | ok |
| E4 Confirmation link used again, already confirmed | Open the link again after confirming | As E1 | As E1, plus "Your email address is already confirmed." and "Go to Settings" | n/a | ok |
| E5 Unconfirmed address notice | A new account's Home, Settings and Account | Notice with a Resend button | (the notice) | Yes | ok |
| E6 Resend confirmation | Resend from the notice | Flash on the same page | "We sent a new link to {email}." | n/a | ok |
| E6a Confirmation link opened on another device, logged out (or as another account) | Open a confirmation link in a private window and confirm | "Email address confirmed" page, 200 | "Thanks! You can keep reading wherever you're logged in to Paper Round." with "Log in on this device" (logged in: "Go to Home") | Yes | ok |
| E7 Resend rate limited | A 4th resend in an hour | Flash | "We've already sent you several links. Check your spam folder, or try again in an hour." | Yes | ok |
| E8 Reset: unknown email | Forgot password with `nobody@example.com` | Forgot password, 422, under Email | "There's no account with this email." | Yes | ok (says so on purpose) |
| E9 Reset: invalid email | Email `nope` | Forgot password, 422 | "Email must be a valid email address" | Yes | ok |
| E10 Reset: rate limited | A 4th reset for one email in an hour, or an 11th from one IP | Forgot password, 429, alert | "Too many attempts. Try again in 60 minutes." | After the wait | ok |
| E11 Reset link unknown, used, expired, or for an address the account no longer has | `/password/reset?token=garbage`, or `UPDATE user_tokens SET expires_at = now() - interval '1 minute' WHERE purpose = 'reset_password'` | Link-expired page, 410 | "This link has expired" / "Reset links work for 1 hour and only once." + "Send a new link" | Yes | ok |
| E12 Reset: new password invalid | Password `short` on the reset page | Reset, 422, under Password | As F2–F4 | Yes; the password isn't echoed | ok |
| E13 An email over the outbox's hourly cap | `INSERT INTO emails (to_address, subject, text_body, html_body) SELECT '{email}', 'x', 'x', 'x' FROM generate_series(1, 20)`, then ask for a reset | "Check your email" page | "We sent a link to {email}. It works for 1 hour." | No: nothing was sent (only the log says so) | needs work (discuss): pages say an email was sent when the cap dropped it; same for Resend (E6) and the email change (E17) |
| E14 Change password: wrong current password | Settings → Account → Password | 422, under Current password | "That password isn't right." | Yes | ok |
| E15 Change password: same as current, or empty | New password = current; or submit empty | 422 | "Your new password must be different from your current one."; "Enter your current password." with "Password is required" | Yes | needs work: two styles of "required" message on one form (see N2) |
| E16 Change email: wrong password, same address, taken, invalid | Settings → Account → Email | 422, under the field | "That password isn't right."; "That's already your email address."; "There's already an account with this email."; "Email must be a valid email address" and "Enter your password." | Yes | ok |
| E17 Change email requested | A valid new address and password | Account, flash | "We sent a link to {new}. Open it to finish changing your email." | n/a | ok |
| E18 Change-email link unknown, used or expired | `/email-change?token=garbage` | Link-expired page, 410 | "This link has expired" / "Links to change your email work for 24 hours and only once." + "Try again" | Yes | ok |
| E19 Change-email link, address taken since | Request a change, then give another account that address (`UPDATE users SET email = '{new}' WHERE id = '{B}'`), then confirm | 409 page | "That address is taken" / "Another account started using {new} in the meantime. Your email is still {old}." | No way on: the page has no link or button | needs work: add a way on (Settings) |
| E20 Settings rate limits | 11 password changes or deletion attempts in 10 minutes; 6 email changes in an hour | Each form, 429, alert | "Too many attempts. Try again in N minutes." | After the wait | ok (code read) |
| E21 Name blank | Settings → Account → Name, blank | 422, under Name | "Name is required" | Yes | ok |
| E25 Theme: no choice or an unknown one | Settings → Reading, then in devtools change a radio's value to `sepia` and submit it (the radios always have one checked, so only a tampered post gets here) | Reading, 422, under the choices; System is focused | "Choose System, Light or Dark." | Yes | ok (2026-11-18, tests) |
| E25b Article layout: no choice or an unknown one | As E25, with a layout radio's value changed to `grid` | Reading, 422, under the layout's choices; Headlines is focused unless the theme is wrong too | "Choose Headlines or Cards." | Yes | ok (2026-11-16, #680, tests) |
| E22 Delete account: wrong or empty password | Settings → Account → Delete account | 422, under Password | "That password isn't right."; "Enter your password." | Yes | ok |
| E23 Account deleted | Delete with the right password | Logged-out landing page, flash | "Your account was deleted. Thanks for trying rss." | n/a | needs work: says "rss", not "Paper Round" (#419) |
| E24 Log out other devices, none | Settings → Account → Other devices → "Log them out" with one session | Account, flash | "There were no other devices to log out." | n/a | ok |

## Waitlist

The join form is on the logged-out landing page (`/`) and follows the "Forms" conventions; its errors re-render the landing page.

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| W1 Join: email missing or invalid | Join with email blank, or `ann` | Landing page, 422, under Email | "Email is required"; "Email must be a valid email address" | Yes | new (#566), not yet reviewed |
| W2 Join: name blank or too long | Name blank, or 101 characters | Landing page, 422, under Name | "Name is required"; "Name must not be more than 100 characters long" | Yes | new (#566), not yet reviewed |
| W3 Join: sites too long | 501 characters in "A few sites you'd like to follow" | Landing page, 422, under the field | "Keep this under 500 characters" | Yes | new (#566), not yet reviewed |
| W4 Join: already on the waitlist | Join with an address whose entry is confirmed | Landing page, 422, under Email | "You're already on the waitlist." | n/a | new (#566), not yet reviewed |
| W5 Join: unconfirmed address joins again | Join twice without confirming | "Check your email" page | "We sent a link to {email}. Open it to confirm your place on the waitlist." | Yes: only the newest email's link works | new (#566), not yet reviewed |
| W6 Join: address has an account | Join with A's email | Landing page, 422, under Email, with a "Log in" link | "There's already an account with this email." | Yes | new (#566), not yet reviewed (says so on purpose, as F7) |
| W7 Join: rate limited | 11 join posts from one IP in an hour | Landing page, 429 with `Retry-After`, alert above the form | "Too many attempts. Try again in 60 minutes." | After the wait; fields kept | new (#566), not yet reviewed |
| W8 Join: honeypot filled | `curl` a join post with `website=x` (with an `Origin` matching the app) | 303 to "Check your email" | As W5 | n/a: only bots get here; nothing is stored or sent | new (#566), not yet reviewed |
| W9 Waitlist link unknown, replaced or after leaving | `/waitlist/confirm?token=garbage`; an older confirmation email's link after joining again; either link after leaving | Link-expired page, 410 | "This link has expired" / "Only the link in the newest waitlist email works, and none work once you've left the list." + "Join the waitlist" | Yes | new (#566), not yet reviewed |
| W10 Confirmed, or left | Confirm or leave from the email | Landing page, flash | "You're on the waitlist. We'll email you when there's a place for you."; "You've left the waitlist. We won't email you again." | n/a | new (#566), not yet reviewed |

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
| H11 Database down: health check | Stop Postgres, `curl localhost:8080/healthcheck` | 200 | `{"status":"degraded","database":"unreachable",…}` within 1 s, so the uptime monitor's keyword `available` stops matching (#430) | n/a | ok |
| H12 Another host (production only) | Code read: a request for any host but `-base-url`'s | 308 to the same path on `-base-url` (#390); not `/healthcheck` | None | n/a | ok |

## Lookups and discovery

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| L1 Query that isn't a website address | Search `zzzz` | Discover, 200 | "No sites or feeds match “zzzz”." and no lookup offered | The search box's placeholder mentions addresses; the result doesn't | ok |
| L2 Address that can't be looked up | Search `127.0.0.1`, `localhost:8080` or `ftp://example.com` | Discover, 200, no lookup offered | As L1 | As L1 | ok (`discovery.ParseInput` refuses them; a hostname that resolves to a private address fails as L4) |
| L2a Link to the app itself | Run with `-base-url http://paperround.test:8080` (`localhost` is L2) and search `paperround.test:8080/shared/x` | Discover, 200, in place of Look up | "That's a Paper Round link. To follow a shared timeline, paste its link into another feed reader." | Yes: it says where to follow it | new (#471) |
| L3 Invalid address posted | Code read: only a crafted post to `/lookups` | Discover, 422, under the search box | "Enter a website address like example.com" | Yes | tied to the input as R4 |
| L3a Link to the app itself posted | As L2a, then a crafted post of that address to `/lookups` | Discover, 422, under the search box | As L2a; not counted by the lookup limit | Yes | new (#471); tied to the input, as L3 |
| L4 Lookup fails | Look up `nonexistent.invalid` (also: a timeout, too many redirects, a blocked or huge or non-HTML response) | Lookup page, 200 | "We couldn't reach {site}. Try again later." + "Back to Discover" | Not for an hour: a failed lookup is reused for an hour, and nothing says so | needs work: one message for every cause, and "later" means an hour |
| L5 Lookup in progress | Look up a slow site (or `INSERT INTO lookups (site_key, url) VALUES ('pending.example', 'https://pending.example/')`, open `/lookups/{id}`) | Lookup page, updates in place (JavaScript) or refreshes itself every 2 s | "Looking for feeds on {site}…" + "Refresh" | Waits | ok |
| L6 Lookup done, no feeds | Look up a site without feeds, or whose only feed doesn't parse (or the row above with `status = 'done'`) | Lookup page, 200 | "No feeds found on {site}." + "Back to Discover" | Yes | ok |
| L7 Lookup done with feeds | Look up a site with feeds | 303 to the site page | None | n/a | ok |
| L8 Lookup row gone | `/lookups/{uuid}` | 404 (H2) | As H2 | Home only | ok |
| L9 Site page with no feeds | `/sites/{empty.example}` | Site page, 200 | "No feeds found for this site yet.", with no Recent posts (and "Missing a feed? Check {host} for new feeds" at the foot when allowed) | Yes | ok (#635) |
| L9a Site page, no posts yet | A site whose live feeds have no articles | Site page, 200 | "No posts yet." under Recent posts; the feeds and Follow still show | Yes | new in #635, not reviewed |
| L9b Site page, every feed gone | A site whose feeds all answered 410 | Site page, 200 | "No posts to show: this site's feeds no longer exist."; each feed says "This feed no longer exists." with no Follow (a followed one keeps "Following" to unfollow) | Yes, Check {host} for new feeds | new in #635, not reviewed |
| L10 Rate limited | H7 | | | | see H7 |

## OPML import

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| O1 No file | Settings → Your data → Import without a file (the input is `required`, so turn off validation or use `curl`) | Import, 422, under the file | "Choose an OPML file to import." | Yes | ok |
| O2 Not OPML | Upload a text file | Import, 422 | "That file isn't an OPML file. Export one from your other reader's settings." | Yes | ok |
| O3 No feeds | An OPML file with an empty `<body>` | Import, 422 | "That file has no feeds in it." | Yes | ok |
| O4 Over 1 MiB | A 1.5 MB OPML file | Import, 413 | "That file is too large. Imports can be up to 1 MB." | Yes | ok |
| O5 Over 1,000 feeds | An OPML file with 1,001 outlines | Import, 422 | "That file has more than 1,000 feeds. Split it into smaller files and import each one." | Yes | ok |
| O6 Rate limited | A 6th import in an hour | Import, 429, under the file | "You've started several imports in the last hour. Try again later." | After the wait, but it doesn't say how long | needs work: say how long, like the other limits |
| O7 Import running | Any valid import, at once | Import results, updates in place (JavaScript) or refreshes itself every 2 s | "Importing {file}…" / "Checked N of M feeds." | Waits | ok |
| O8 Import with failures | Outlines with `xmlUrl="not a url"`, an unreachable host, and an address without a feed | Import results, 200 | Per feed: "Not a valid web address."; "Couldn't reach the site." + "Search for {host}"; "There's no feed at this address." | Yes | ok |
| O8a Link to the app itself | As L2a, an outline with `xmlUrl="http://paperround.test:8080/shared/x/feed.atom"` | Import results, 200 | "A Paper Round link. Follow it in another feed reader." + "Search for {host}" | Yes | new (#471) |
| O9 Another user's import | As B, open A's import URL | 404 (H3) | As H2 | Home only | ok |
| O10 Malformed upload | `curl` a post that isn't multipart | 400 error page | "Bad request" / "That form submission couldn't be read." | Home only | needs work: see H1 |

## Feeds

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| D1 Fetch failed once | "Failing Once" from the setup | Feed page, 200 | "The last attempt to fetch this feed failed ({time}). We'll keep trying." | Nothing to do | ok |
| D2 Fetch failing (3+ times) | "Failing Thrice" | My feeds: "This feed is failing to update."; feed page: "The last 3 attempts to fetch this feed failed, most recently {time}. We'll keep trying." | (as Where) | Nothing to do but wait or unfollow | ok |
| D3 Gone (410) | "Gone Feed" | My feeds: "This feed no longer exists."; feed page: "This feed no longer exists: its publisher removed it. We've stopped checking it." with no Refresh button | (as Where) | Unfollow | ok |
| D4 Never fetched, no posts | "Never Fetched" | Feed page, 200 | "Never fetched." and "No posts yet." | Waits | ok |
| D5 Refresh within 5 minutes of a fetch | `UPDATE feeds SET last_fetched_at = now() WHERE id = '{feed}'`, click Refresh | Feed page, flash | "This feed was refreshed in the last few minutes. Try again later." | Yes | ok |
| D6 Refresh fails | Refresh "OK Feed" (its host doesn't exist); a feed whose address returns a web page; one that answers 410 | Feed page, flash | "Couldn't reach this feed. Try again later."; "This feed's address didn't return a feed. Try again later."; "This feed no longer exists. We've stopped checking it." | Yes | ok |
| D7 Refresh a gone feed, or follow one | `curl` a post to `/feeds/{gone}/refresh` or `/subscribe` (the buttons aren't shown) | Feed page, flash | "This feed no longer exists, so it can't be refreshed."; "…so you can't follow it." | n/a | ok |
| D8 Refresh rate limited | 11 refreshes in 10 minutes | Feed page, flash | "You've refreshed a lot of feeds recently. Please wait a few minutes and try again." | After the wait | ok |
| D9 Refresh while one is running | Code read: click Refresh twice | The second waits, then gets D5 if the first succeeded | As D5 | Yes | ok |
| D10 Feed page at 375 px, followed | Any feed A follows, at 320, 360, 375 and 390 px | Feed page | n/a | Edit and Following fit beside the avatar; no sideways scroll | ok (2026-11-23, #752: checked at 320 and 390 px; #431 checked 320, 360, 375 and 390 px, and the site page at 320 px) |

## Discover

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| R1 Empty query | `/discover` or `/discover?q=`, or clear the box (with JavaScript, it fetches the no-query page) | Discover, 200 | Discover's sections under the search box; with an empty catalog (or every catalog feed followed or gone), just the search box and the import card or line (#636); a store error, including reading the reader's imports or subscriptions, is a 500 (H8) | n/a | new (#633) |
| R1a Section with nothing to show | A section with no catalog feeds, or whose feeds the reader all follows | Discover, 200 | The section and its chip are left out | n/a | new (#633) |
| R1b Section page with no feeds | An old link to `/discover/sections/{slug}` for a section with no catalog feeds left (Discover itself leaves such a section out, R1a) | Section page, 200 | Feeds: "No feeds in {section} yet." with a link back to Discover; Recent posts: "No posts in {section} yet." | Yes, the link and the nav | new (#634) |
| R1c Unknown section | `/discover/sections/nope`, or a section since renamed in its slug | 404 page | The 404 page | Yes, the nav | new (#634) |
| R1d Bad paging link | `/discover/sections/{slug}/posts?before=zzz` | 400 page | "That link to older posts isn't valid." | Yes, the tabs and the nav | new (#634) |
| R2 No results | `/discover?q=zzzz&scope=top`, or submit `zzzz` | Discover, 200 | "No results for “zzzz”." (Top, or no tabs) | Yes (L1) | new (#645) |
| R2a An empty tab | `/discover?q=…&scope=feeds` or `&scope=saved` for a query matching only the other kind | Discover, 200 | "No feeds match “{q}”." or "No saved articles match “{q}”.", the tab dimmed with its 0 | Yes, the other tabs | new (#645) |
| R2b A pasted address | `/discover?q=newsite.example`, with or without `&scope=top` | Discover, 200 | A row "newsite.example", "Find the feeds this site publishes", Look up, above the suggestions or results (or above R2's line); none on the Saved tab, or when the address is one of a found site's feeds, or its home page once a lookup has finished there (its own row is there) | Yes, Look up | new (#645) |
| R2c A link to the app itself | As L2a | Discover, 200 | The Look up row's place: "That's a Paper Round link. To follow a shared timeline, paste its link into another feed reader." | Yes | new (#645) |
| R2d A store error | Any of the search's or the suggestions' reads fails | 500 page | As H8 | As H8 | new (#645) |
| R2e No suggestions | `/discover?q=zzzz`, or type `zzzz` (with JavaScript, suggestions update as you type) | Discover, 200 | Only the last row, "Search everything for “zzzz”" (with Look up above it for an address); live search announces "No feeds" | Yes, the row or Enter shows the results (R2) | new (#666) |
| R2f No sites on a shared host | `/discover?q=bsky&scope=feeds&host=nope.example`: a group row's link whose sites no longer match, or any other `host` | Discover, 200 | "No sites on {host} match “{q}”.", under "Sites on {host} that match “{q}” · All results" | Yes, All results | new (#739) |
| R3 Special characters | `/discover?q=%25`, `_`, `"<script>\` | Discover, 200 | As R2; matched literally and escaped | Yes | ok |
| R4 Query over 200 characters | `/discover?q=` + 201 letters | Discover, 422, under the search box | "Search must be 200 characters or fewer"; the input is `aria-invalid`, described by the error and focused; while typing, live search ignores the 422 and leaves the last results until the form is submitted | Yes; the query is kept, and the clear button empties it | tied to the input (#764); not announced beyond focus |

## Empty states

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| Y1 Home, no subscriptions | A new account's `/`; also an account following only feeds that were fetched and have no articles | Home, 200 | "Nothing on Home yet." "Follow sites and their new articles show up here.", with the buttons "Find sites in Discover" and "Import from another reader" | Yes, with buttons | new (#639) |
| Y1a Home, following feeds never fetched | Follow only feeds never fetched (locally, a `subscriptions` row for a feed with no `last_attempt_at`) | Home, 200 | A spinner, "Getting the latest from A and B…" ("A, B and 3 more" past two), "Their newest articles will show up here in a moment."; the page checks again every 3 s for a minute (the spinner goes when it stops; a meta refresh every 5 s without JavaScript) and shows and announces the articles when they arrive | Yes, it fills in by itself | new (#639) |
| Y1b The end of All feeds | Scroll to the last page of `/timelines/all` | Home, 200, after the last article | "That's everything from your 3 sites. Find more in Discover" | Yes, with a link | new (#639) |
| Y2 My feeds, none | New account's `/feeds` | 200 | "You're not following any feeds yet.", with the button "Find sites in Discover" | Yes, with a button | new (#757) |
| Y2a My feeds search, no matches | Type "zzz" in My feeds' search, or open `/feeds?q=zzz` | 200 | "None of your feeds match “zzz”. Search Discover for sites to follow.", the sort links still shown; with JavaScript it updates as you type and announces "No feeds" | Yes, the link searches Discover for the same words | new |
| Y3 Saved, none | New account's `/saved` | 200 | "Nothing saved yet." "Use the bookmark on any article to keep it here." | Yes | new (#757) |
| Y4 Saved search, no matches | `/saved?q=zzz` with saves | 200 | "No saved articles match “zzz”." | Yes, the box's clear button (#764) | ok |
| Y5 Tags, none | New account's `/tags` | 200 | "You don't have any tags yet." "Tags group your feeds, for example "News" or "AI", so you can make timelines from them." | Yes | new (#757) |
| Y6 Timelines, none | `/timelines` after deleting a new account's Quiet posters and Starred | 200 | Home's row, then "You don't have any timelines yet." "A timeline shows some of your feeds, for example everything tagged “News”." | Yes | new (#757) |
| Y7 Site page, no feeds | L9 | | | | see L9 |
| Y8 Feed, no posts | D4 | | | | see D4 |

## Subscriptions

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| U1 Follow or unfollow succeeds | Click Follow, or Following on a feed's or site's page (Unfollow on Edit subscription or the confirmation); with JavaScript, Follow and Following on a site's page and in lists change in place instead | Same page, flash | "Following {feed}."; "Unfollowed {feed}." | n/a | ok |
| U1a Unfollow a feed with a name or tags | Press Following on a feed you renamed or tagged, on its page or its site's, with or without JavaScript | Confirmation page, 200 (`/feeds/{id}/unfollow`) | "Unfollow {name}?"; "Your name and tags for it will be removed." (or only "name", or only "tags"), with red Unfollow and Cancel | Yes: Cancel goes back, changing nothing | new in #752, not reviewed |
| U1b The confirmation for a feed you no longer follow | Unfollow in another tab, then open the confirmation | 404 (H3) | As H2 | Home only | new in #752, not reviewed |
| U2 Follow an unknown feed | `curl` a post to `/feeds/{uuid}/subscribe` | 404 (H2) | As H2 | Home only | ok |
| U3 Follow or unfollow fails | Code read: a store error | 500 (H8) | As H8 | As H8 | ok (see H1) |
| U4 Edit: name too long | Edit subscription, name of 101 characters | 422, under Name | "Must be 100 characters or fewer." | Yes | ok |
| U5 Edit: new tag too long, or over 100 tags | New tag of 51 characters; or 100 tags already | 422, under New tags | "Each tag must be 50 characters or fewer."; "You can have up to 100 tags." | Yes | ok |
| U6 Edit a feed you don't follow | As B, `/feeds/{A's feed}/edit` | 404 (H3) | As H2 | Home only | ok |

## Reading

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| P1 Empty timeline | Y1 | | | | see Y1 |
| P2 No older articles | `/?before=0_2000-01-01T00:00:00Z_00000000-0000-0000-0000-000000000000` (also feed pages, Saved) | 200 | "No older articles." (feed pages: "No older posts."; Saved: "No older saved articles.", "No older matches.") | Yes | ok |
| P3 Invalid `before` cursor | `/?before=garbage`, `/feeds/{feed}?before=garbage`, `/saved?before=garbage` | 400 error page | "Bad request" / "That link to older articles isn't valid." (feed pages: "That link to older posts isn't valid.") | Home only | needs work: see H1 |
| P4 Article with no text in its feed | The `notext` article | Article page, 200 | "This article has no text in its feed." + "Read on {site}" | Yes | ok |
| P5 Article with no URL | The `nourl` article | Article page, 200, no "Read on" | None | n/a | ok |
| P6 Article with no title, text or URL | The `nothing` article | Article page, 200 | "(untitled)" and P4's text | n/a | ok |
| P7 Unknown or malformed article | `/articles/{uuid}`, `/articles/abc` | 404 (H2) | As H2 | Home only | ok |
| P8 Read ping fails | `curl` a post to `/articles/{uuid}/read` | 404, unseen | None | n/a | ok (silent by design) |
| P9 Save or Star fails | A post for `/articles/{uuid}/save`; with JavaScript, a click on a row whose article was deleted (anything but a 204 puts the button back and submits the form normally) | 404 (H2) | As H2 | Home only | changed in #775 (was "ok"), not reviewed |
| P10 Share can't copy the link | Where there's no share sheet (or it fails), deny clipboard permission (in Playwright, don't grant `clipboard-write`) and press Share | Any page with Share, above the button until the next press or key; the share icon stays | "Couldn't copy" (visible, hidden from assistive technology); "Couldn't copy the link" announced | Yes: copy the link from the article page's "Read on" link | ok |
| P11 An image fails to load | A card, lead or body image whose URL 404s or whose host has no working https (locally, point an article's `image_url` at `https://localhost:1/x.jpg`) | Rows, the article page | None: it tries the page's image (`data-fallback-src`), then is removed, so the row or article reads as one without an image (an image still loading is unseen until it loads or fails); without JavaScript, the browser's broken-image box | n/a | new (#525), not reviewed |
| P12 A code block's copy button can't copy | On an article with a code block, deny clipboard permission (in Playwright, don't grant `clipboard-write`) and press the block's copy button | Article page, above the button until the next press or key | "Couldn't copy" (visible, hidden from assistive technology), announced | Yes: select the code by hand | ok |

## Timelines

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| T1 Name blank or too long; description too long | New timeline | 422, under the field | "Enter a name."; "Must be 50 characters or fewer."; "Must be 280 characters or fewer." | Yes | ok |
| T2 No tags or feeds chosen | "Only feeds with these tags…" with none ticked | 422, under Show | "Choose at least one tag or feed, or show all your feeds." | Yes | ok |
| T3 Bad "at most N posts a week" | "From quiet feeds" ticked with `0`, `101` or nothing | 422, under the number | "Enter a whole number from 1 to 100." (unticked, the number isn't checked or stored) | Yes | ok |
| T4 Bad words or phrases | "Mentioning" ticked with `!!!`, a 51-character word, or 21 words | 422 | "Each word or phrase needs at least one letter or number."; "Each word or phrase must be 50 characters or fewer."; "At most 20 words or phrases." | Yes | ok |
| T4a No words or phrases | "Mentioning" or "Not mentioning" ticked with its field empty | 422, under the field | "Enter at least one word or phrase." (unticked, the field isn't checked or stored) | Yes | ok |
| T5 Over 20 timelines | `INSERT INTO timelines (user_id, name) SELECT '{A}', 't' \|\| g FROM generate_series(1, 20) g`, then New timeline | 422, alert above the form | "You can have up to 20 timelines." | Only by deleting one; found out after filling the form | ok |
| T6 A tag or feed deleted in another tab before saving | Code read | 404 (H2) | As H2 | Home only | ok (rare) |
| T7 Empty custom timeline | A timeline whose words match nothing | 200 | "Nothing in this timeline yet." | The tab row's Edit leads to its row on `/timelines`, and its pencil | new (#757) |
| T7a Empty quiet posters timeline | A new account's Quiet posters, or any timeline with "at most N posts a week" that has no articles | 200 | "No quiet posts here yet." "When a feed you follow posts at most 2 times a week, its articles show up here, so busy feeds never bury them." ("once a week" for 1) | Yes: it explains what will show up | new (#757) |
| T8 Timeline with no feeds to show | A timeline limited to a tag with no feeds | 200 | "This timeline has no feeds to show.", with the button "Edit timeline" | Yes, with a button | new (#757) |
| T9 Starred-only, nothing starred | A new account's Starred | 200 | "Nothing starred here yet." "Star the articles you like most and they'll show up here." | Yes | new (#757) |
| T9a New timeline without tags | New timeline with no tags | 200, the list of starting points | "Feeds with a tag" says "Make a tag first: tags group your feeds, like News or Tech." and links to Tags | Yes | ok |
| T10 Unknown or another user's timeline | `/timelines/{uuid}`; as B, A's | 404 (H2) | As H2 | Home only | ok |

## Tags

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| G1 Name blank, with a comma, or too long | New tag, or rename | 422, under Name | "Enter a name."; "Tags can't contain commas."; "Must be 50 characters or fewer." | Yes | ok |
| G2 Duplicate name | A tag that exists, in any case | 422, under Name | "You already have a tag called “news”." | Yes | ok |
| G3 Over 100 tags | `INSERT INTO tags (user_id, name) SELECT '{A}', 't' \|\| g FROM generate_series(1, 100) g`, then New tag | 422, under Name | "You can have up to 100 tags." | Only by deleting one | ok |
| G4 Tag page without subscriptions | A tag on an account with no feeds | 200 | "You're not following any feeds yet. Find sites to follow in Discover.", a quiet line above Rename and Delete | Yes | new (#757) |
| G5 Unknown or another user's tag | `/tags/{uuid}`; as B, A's | 404 (H2) | As H2 | Home only | ok |

## Shared timelines

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| X1 Share with an unconfirmed address | B's timeline → Edit → Share: the share page offers Resend instead; or `curl` a post to `/timelines/{id}/share` | Share page, flash | "Confirm your email address to share timelines." | Yes | ok |
| X2 Unknown or malformed link | `/shared/AAAA…` (43 characters), `/shared/x` | 404 (H2) | As H2 | Home only (the logged-out landing page) | ok |
| X3 Link after Stop sharing or New link | Open the old link and its `feed.atom` | 404 (H2); the feed's 404 is HTML | As H2 | n/a | ok (decided: "Sharing" in decisions.md) — discuss: the page is cached for 5 minutes and the feed for 15, so "The links no longer work" isn't true at once for whoever loaded them |
| X4 Sharing turned on, off, new link | Edit → Sharing | Edit page, flash | "Sharing turned on."; "Sharing turned off. The links no longer work."; "New link made. The old one no longer works." | n/a | ok |

## Across pages

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| N1 Flash messages | Any flash above (S6, E6, D5, U1, …) | Top of the page after a redirect | (per row) | n/a | needs work: `.flash` has no `role="status"`, so screen readers don't announce it, including failures (D6) |
| N2 Required-field messages | F12, E15, E22, T1, G1 | | "Email is required" and "Name is required" beside "Enter your password." and "Enter a name." | n/a | needs work: two styles, with and without a final period |

## Admin

The admin site (`make run/admin`, http://localhost:4001) is the owner's alone; its error page is the admin layout (the sidebar, no section current; just the brand when no one is logged in) with a heading, a sentence and "Go to the overview".

| State | Trigger | Where (page, status) | Message | Can the user recover? | Verdict |
| --- | --- | --- | --- | --- | --- |
| A1 Overview: an empty database | Open `/` on a database with no accounts, feeds or queued work | 200, the Overview | Every figure 0 (second lines "of 0", "0 in 30 days"), every queue 0 with "—" for its longest wait, and A1b for the jobs | n/a | changed in #575 and #610 (was #562's "Nothing here yet."), not reviewed |
| A1a Overview: a queue behind | A queue's oldest due job waited over 15 minutes, e.g. `UPDATE emails SET next_attempt_at = now() - interval '20 minutes' WHERE status = 'pending'` | 200, the Overview | "Behind" on the queue | n/a: says the runner isn't keeping up | new in #610, not reviewed |
| A1b Overview: no job metrics yet | Open `/` on a database whose `job_metrics` is empty (a new database, or `www` never ran a job) | 200, the Overview | "No job metrics yet. `www` writes them once a minute." | n/a: fills in once `www` runs | new in #611, not reviewed |
| A1c Overview: a runner with no jobs | A runner finished no jobs in the last 24 hours | 200, the Overview | "No jobs" on its row | n/a | new in #611, not reviewed |
| A1d Web performance: no views | No beacon in the period (or on the device) | 200, the page with its filters | "No page views in the last 7 days. Each page the reader shows sends one beacon when it’s hidden, and they show up here." | n/a | new in #578, not reviewed |
| A1e Web performance: a metric no view reported | A browser or route with no LCP or INP (Safari before 26.2, Samsung Internet) | 200 | "—" in its cells and "No LCP or INP reported" in its note; a metric with no view at all says "No view reported LCP.", and the assessment "Not assessed: No LCP reported" | n/a | new in #578, not reviewed |
| A1f Web performance: few views | A breakdown's row under 100 views | 200 | The row dimmed, tagged "few views" | n/a | new in #578, not reviewed |
| A1g Web performance: not in the rollups | Choose 90d or 13m | 200 | "Only the last 30 days’ views say whether the app was installed: choose 24h, 7d or 30d." and the same for referrers, protocols and why Back and Forward weren't restored (#771) | n/a | new in #578, not reviewed |
| A1g2 Web performance: no Back or Forward | No `restore` or `back_forward` view in the period (or on the device) | 200 | "No Back or Forward in the last 7 days." in the Back and Forward section | n/a | new in #771, not reviewed |
| A1g3 Web performance: every Back and Forward restored | Only `restore` views in the period | 200 | The figures (100% restored, 0 loaded again) without the reasons table | n/a | new in #771, not reviewed |
| A1g4 Web performance: a route with no views | Open `/web-performance/route?route=/nowhere`, or a real route in a period (or on a device) it had no views | 200, the route's page with its filters | "No page views of /nowhere in the last 7 days. Choose a longer period to look further back. Only the reader’s pages send them, one when each is hidden." (no "Choose…" at 13m); on a device, "No page views of /nowhere on tablets in the last 7 days. Choose All to see every device. …" | Yes, a longer period or All devices | new in #589, not reviewed |
| A1g5 Web performance: a route with few views | A route under 100 views in the period | 200 | "Only 42 page views: percentiles from so few shift a lot from one view to the next." above its vitals, and its breakdowns' rows dimmed as A1f | n/a | new in #589, not reviewed |
| A1g6 Web performance: a route's metric no view reported | A route whose views reported no LCP (or no server time: no Server-Timing header) | 200 | As A1e in its vitals; "No view reported LCP." in place of the histogram, and "No view reported its LCP." under What’s slow; for the server's time "—" and "No view reported the server’s time: it comes from the page’s Server-Timing header." | n/a | new in #589, not reviewed |
| A1g7 Web performance: a route's details not in the rollups | Choose 90d or 13m on a route's page | 200 | "Only the last 30 days’ views say what their LCP element and slowest interaction were, how they were loaded and their size: choose 24h, 7d or 30d." under What’s slow | Yes, a shorter period | new in #589, not reviewed |
| A1g8 Web performance: a route with no route | Open `/web-performance/route` without `route` | 404 error page | As A2 | As A2 | new in #589, not reviewed |
| A1h Service: the pinger not configured | Start the admin without `-app-internal-url` | 200, the Service page | "Not configured: set -app-internal-url (RSS_APP_INTERNAL_URL) to check www every minute." in Availability and Version; the runtime charts still show | Yes, set the flag | new in #616, not reviewed |
| A1i Service: no checks or runtime metrics yet | Open `/service` on a database with no `availability_checks` (a new pinger) or no `runtime_metrics` in the period (`www` not running) | 200 | "No checks yet: the admin checks www every minute." / "No runtime metrics yet: www writes them once a minute." / "No version reported yet." | Yes, wait a minute | new in #616, not reviewed |
| A1j Service: no incidents | No failed check in the period | 200 | "No incidents in the last 24 hours." (7 days, 30 days, 90 days) | n/a | new in #616, not reviewed |
| A1k Service: www down now | Stop `www` with the pinger running, then open `/service` after a minute | 200 | An incident marked "Ongoing", its duration up to now | n/a: www's own state | new in #616, not reviewed |
| A1l Service: past what runtime keeps | Choose 90d | 200 | "Runtime metrics are kept for 30 days: choose 24h, 7d or 30d." in place of the runtime charts | Yes, a shorter period | new in #616, not reviewed |
| A1m Service: Render not configured | Start the admin without `-render-api-key` or `-render-service-id` | 200, the Service page | "Not configured: set -render-api-key (RSS_RENDER_API_KEY) and -render-service-id (RSS_RENDER_SERVICE_ID) to read www’s figures from Render." in Instances and under Deploys; the charts have no deploy markers | Yes, set both flags | new in #576, not reviewed |
| A1n Service: Render unreachable | Set `-render-api-key` to a revoked key (or block `api.render.com`), then open `/service` | 200 | "Couldn’t reach Render." in Instances and under Deploys (either alone when only one request fails); the rest of the page renders, and the admin logs a warning | Yes, reload in a minute | new in #576, not reviewed |
| A1o Service: nothing from Render | Render has no figures for the period, or the service has no deploys | 200 | "No figures from Render for the last 24 hours." (7 days, 30 days, 90 days) / "No deploys yet." | n/a | new in #576, not reviewed |
| A1p Endpoints: no requests | No `request_metrics` rows in the period (`www` not running, or a new database) | 200, the Endpoints page with its period | "No requests in the last 24 hours. www writes its request metrics once a minute, and they show up here." (hour, 7 days, 30 days) | Yes, wait a minute or choose a longer period | new in #577, not reviewed |
| A1q Endpoints: a route with no requests | Open `/endpoints/route?route=/nowhere&method=GET`, or a real route in a period it had no requests | 200, the route's page | "No requests to GET /nowhere in the last 24 hours. Choose a longer period to look further back." (no "Choose…" at 30d), then its server errors, if any are kept | Yes, a longer period | new in #577, not reviewed |
| A1r Endpoints: no route or a bad method | Open `/endpoints/route` without `route`, or with a `method` outside the standard ones and `OTHER` (`get`, `BREW`) | 404 error page | As A2 | As A2 | new in #577, not reviewed |
| A1s Endpoints: no server errors on a route | A route none of the newest 1,000 kept server errors is on | 200 | "No server errors kept for this route. www keeps its newest 1,000 across every route." | n/a | new in #577, not reviewed |
| A1t Endpoints: deploys not marked | Start the admin without Render's settings, or with Render unreachable (a route's web performance page says the same, #589) | 200 | "Deploys aren’t marked: set -render-api-key (RSS_RENDER_API_KEY) and -render-service-id (RSS_RENDER_SERVICE_ID) to read them from Render." / "Couldn’t reach Render, so deploys aren’t marked." under the charts; the admin logs a warning when Render fails | Yes, set both flags, or reload in a minute | new in #577, not reviewed |
| A1u Endpoints: instances from hourly metrics | Open a route's page at 7d or 30d | 200 | "Hourly metrics don’t say which instance served a request: choose 1h or 24h to see the instances." in place of the instances table | Yes, a shorter period | new in #577, not reviewed |
| A2 404 | `/nope`, or a missing file under `/static/` | 404 error page | "Page not found" / "There's no admin page here." | The sidebar, "Go to the overview" | new in #562, not reviewed |
| A3 405 | `curl -X POST -H 'Sec-Fetch-Site: same-origin' localhost:4001/` | 405 with `Allow: GET, HEAD` | "Method not allowed" / "That action isn't supported for this page." | As A2 | new in #562, not reviewed |
| A4 403: a post from another origin, the reader's included | `curl -X POST -H 'Sec-Fetch-Site: same-site' localhost:4001/` | 403 error page | "Request blocked" / "This form was submitted from another site, so it was rejected." | As A2 | new in #562, not reviewed |
| A5 500 | Code read: a template or handler error | 500 error page | "Something went wrong" / "The server encountered a problem and could not process your request." | As A2 | new in #562, not reviewed |
| A6 Database down: health check | Stop Postgres, `curl localhost:4001/healthcheck` | 200 | `{"status":"degraded","database":"unreachable",…}`, as H11 | n/a | new in #562, not reviewed |
| A7 Started in production without a key | `go run ./cmd/admin -env production -base-url https://admin.example.com` | Exits 2 | "-secret-key (RSS_SECRET_KEY) must be set in production" | Set the key | new in #567, not reviewed |
| A8 Anonymous request | Open `/` in a private window | 303 to Log in | None | Yes: after both steps it returns to the page (GETs only) | new in #567, not reviewed |
| A9 Wrong email or password | Log in with an unknown email, or a wrong password | Log in, 422 | "That email and password don't match an admin." | Yes | new in #567, not reviewed |
| A10 Wrong or reused code | On the code step, type `000000`, or a code or recovery code already used | Log in (code), 422 | "That code didn't work." | Yes, with the next code or a recovery code | new in #567, not reviewed |
| A11 Too many attempts | 11 login attempts from one IP in 15 minutes, or 6 wrong codes for one admin | Log in, 429 with `Retry-After` | "Too many attempts. Try again in N minutes." | Yes, after the wait | new in #567, not reviewed |
| A12 Code step too late | Wait over 5 minutes between the password and the code | 303 to Log in, flash | "That took too long. Log in again." | Yes | new in #567, not reviewed |
| A13 Session ended | 30 minutes idle, or 12 hours after logging in | 303 to Log in | None | Yes, as A8 | new in #567, not reviewed |
| A14 Log out | Log out, under the email in the nav | Log in, flash | "You've logged out." | n/a | new in #567, not reviewed |
| A15 Destructive action with an old code | Open a reader's Delete account page, or submit it, over 10 minutes after the last code | 303 to "Confirm it's you" | "This action asks for a new code." | Yes: the code returns to the action's page | changed in #590 (was code read only), not reviewed |
| A16 Enroll problems | `admin enroll` with an existing email (no `-reset`), `-reset` for no admin, no `RSS_SECRET_KEY`, or three wrong first codes | Exits 1 or 2 | Says which, and that nothing was saved | Yes | new in #567, not reviewed |
| A17 Audit log: nothing recorded, or nothing matches | Open `/audit` on a database with no entries, or filter by an action no entry has | 200, the Audit log | "Nothing recorded yet." / "Nothing matches these filters." with Clear | Yes, Clear | new in #572, not reviewed |
| A18 Audit log: bad page | `/audit?before=x` | 400 error page | "Bad request" / "That page of the audit log doesn't exist." | As A2 | new in #572, not reviewed |
| A19 Destructive action not confirmed | Code read: `confirmDestructive`, the target's name typed wrong (no action uses it yet) | The action's form, 422 | "Type {name} to confirm." | Yes | new in #572, not reviewed |
| A20 Waitlist: an empty tab | Open `/waitlist` with no one waiting, or a tab with no one on it | 200, the Waitlist | "No one is waiting." / "No one has an invite waiting to be used." / "No one is waiting to confirm." | n/a | new in #574, not reviewed |
| A21 Waitlist: bad tab or page | `/waitlist?tab=x`, `?page=0`, or a page past the last | 404 error page | As A2 | As A2 | new in #574, not reviewed |
| A22 Review: nobody, or too many | Post the waitlist's form with nothing ticked, or "Select the oldest" with 0 or over 100 | 422 error page | "Can't review that" / "Select between 1 and 100 people to invite." (or "Choose between …") | The sidebar, or Back | new in #574, not reviewed |
| A23 Review: no one left to invite | Review people who were all invited or left since the page loaded | 422 error page | "No one to invite" / "Everyone selected has been invited or has left the waitlist." | As A22 | new in #574, not reviewed |
| A24 Review: past 80% of the day | Start the admin with `-mail-daily-limit 10 -mail-reserve 1`, send 8 emails, review 1 | 200, the review | "This leaves {left} emails today for account emails like password resets." | n/a: sending is allowed | new in #574, not reviewed |
| A25 Review: over what fits | As A24, review more than fit | 200, the review, with only "Send the oldest {k}" (none when 0 fit) | "Only {k} fit today without risking account emails." (or "No more invites fit today …") | Yes: send those, the rest wait | new in #574, not reviewed |
| A26 Review: invalid label or expiry | Clear the wave's label, or set the expiry to 0 or over 90 days | The review, 422 | "Give the wave a label." / "Keep the label to 100 characters." / "Choose between 1 and 90 days." | Yes | new in #574, not reviewed |
| A27 Send: things changed since the review | Review in two tabs and send from both, or let other emails fill the day before sending | The review, 409, as things are now | "More emails were sent since you reviewed this wave, so nothing was sent. …" / "Some of them were invited or left the waitlist since you reviewed this wave, so nothing was sent." | Yes, send again | new in #574, not reviewed |
| A28 Sent | Send a wave | 303 to the Invited tab, flash | "Sent {n} invites in {wave}." | n/a | new in #574, not reviewed |
| A29 Invites: none yet | Open `/invites` before any invite exists | 200, the Invites page | "No invites yet. Invite people from the waitlist, or invite someone by hand." | Yes, the links | new in #607, not reviewed |
| A30 Invites: bad page, or an unknown invite | `/invites?before=x`, or post to `/invites/<unknown id>/revoke` | 404 error page | As A2 | As A2 | new in #607, not reviewed |
| A31 Revoke a used invite | Revoke an invite someone signs up with before you press it | The invites page, flash | "That invite has been used, so it can't be revoked." | n/a | new in #607, not reviewed |
| A32 Resend refused | Resend an invite that was used or revoked meanwhile, or when no more emails fit | The invites page, flash | "That invite can't be resent: it's open, used or revoked." / "No more emails fit today without risking account emails." | Yes, tomorrow for the limits | new in #607, not reviewed |
| A33 Invite by hand: the email can't have one | An address not on the waitlist or unconfirmed, or with an invite still pending | The form, 422 | "No one with this email is confirmed on the waitlist. Leave it empty for an open invite." / "{email} already has an invite waiting to be used: resend it from the invites page." | Yes | new in #607, not reviewed |
| A34 Invite by hand: invalid or over the limits | A bad address, a note over 200 characters, an expiry outside 1–90 days, or no more emails fit | The form, 422 | "Enter an email address, or leave it empty." / "Keep it to 200 characters." / "Choose between 1 and 90 days." / "No more emails fit today without risking account emails." | Yes | new in #607, not reviewed |
| A35 Invite by hand: made | Create an invite | 200, the form's page with the link | "Copy this link and send it yourself:" or "Invite emailed to {email}. Its link:" | n/a: the link isn't shown again | new in #607, not reviewed |
| A36 Feeds: none here | Open `/feeds` with no failing feeds, or search for a title nothing has | 200, the Feeds page | "No feeds here." / "No feeds here match “{q}”." | Yes, another filter or Clear | new in #581, not reviewed |
| A37 Feeds: bad page, or an unknown feed or site | `/feeds?after=x`, a page past the last, `/feeds/<unknown id>` or `/sites/<unknown id>` (or an action on one) | 404 error page | As A2 | As A2 | new in #581, not reviewed |
| A38 Refresh now: the outcome | Refresh now a feed whose URL answers, answers 304, answers 410, or fails (a dead URL, or one that isn't a feed) | The feed's page, flash | "Refreshed: {n} new articles, {m} updated." / "Refreshed: not modified since the last fetch." / "The feed answered 410 Gone, so it's marked gone." / "The refresh failed: {error}" | Yes, Reset backoff or try later | new in #581, not reviewed |
| A39 Refresh now or Clear gone: not offered | Code read: post Refresh now to a gone feed, or Clear gone to one that isn't (the page offers neither) | The feed's page, flash | "This feed is marked gone: clear it first." / "This feed isn't marked gone." | Yes, Clear gone | new in #581, not reviewed |
| A40 Site: no lookup or no feeds | A site found without a lookup of its host (a feed's site from an import), or with no feeds left | 200, the site's page | "No lookup of this site yet." / "No feeds." | Yes, Check for new feeds | new in #581, not reviewed |
| A41 Feed: a problem | A feed with failures in a row, answered 410, or never fetched | 200, the feeds list's Problem column and the feed's status panel | "Gone since {date}" / "Failing {n}×" (3 or more, danger tone) / "Failed {n}×" / "Never fetched", then the last error; the panel adds what happened and when, the whole error, and its actions | Yes, Refresh now, Reset backoff or Clear gone | new in #581, not reviewed |
| A42 Feed: late | A subscribed feed with no successful fetch for over a day and no problem above | 200 | Its last success in bold in the list; "Last fetched {when}" atop its page | Yes, Refresh now | new in #581, not reviewed |
| A43 Site: its lookup failed | A site whose lookup failed (its home page unreachable) | 200, the site's page | "Failed {when} after {n} attempts." in the danger tone, with the error | Yes, Check for new feeds | new in #581, not reviewed |
| A44 Discover: a gone feed | On a gone feed's page, pick a section and Save | The feed page, 422 | "This feed is gone, so it can't be in Discover. Clear gone first if it's back." under the section select | Yes, Clear gone first | new in #628, not reviewed |
| A45 Discover: an unknown section | Post a `section` that isn't a `discover_sections` ID to `/feeds/{id}/discover`, or one deleted since the page loaded | The feed page, 422 | "Choose one of the sections." | Yes | new in #628, not reviewed |
| A46 Seed list: empty, or nothing under a filter | Open `/seed` with no `seed_sites` rows, or a filter no entry is under | 200, the Seed list page | "The seed list is empty." / "No entries here." | Yes, another filter | new in #648, changed in #710, not reviewed |
| A47 Seed list: an entry not found | An entry whose lookup failed (an unreachable site) or found no feeds | 200, the Seed list page | "Failed" in the danger tone with the error cut to one line (whole on hover) / "No feeds", and Look up again; Not found offers "Look up all {N} again" | Yes, Look up again, or Look up all again; nothing retries on its own | new in #648, changed in #710, not reviewed |
| A48 Seed list: Look up again an unknown entry, or a bad page | Code read: post a `site_key` that isn't on the list to `/seed/lookup`; or open `/seed?after=x` for a site key not on the list, or a page past the last | 404 error page | As A2 | As A2 | new in #648, changed in #710, not reviewed |
| A53 Seed list: Look up all again with nothing to look up | Post Look up all again after another admin's has already queued every Not found entry (or code read: post to `/seed/retry` with none) | 303 to the Not found filter | "Nothing to look up again." | Yes, nothing to do | new in #710, not reviewed |
| A54 Add sites: a field error | Post Preview or Add with no addresses (blank, or only `#` lines), over 10,000 lines, or a source over 100 characters | The Add sites form, 422, the text kept | "Paste at least one website address." / "Add at most 10,000 lines at a time." / "Keep the source under 100 characters." | Yes | new in #711, not reviewed |
| A55 Add sites: lines that can't be added | Preview lines that aren't addresses, name no section, are on Paper Round itself, repeat an earlier line's site, or are already on the list | 200, the preview, problems first; skipped on Add | "Not a website address" / "No section called “{text}”" / "An address on Paper Round itself" (danger tone) / "Same site as line {N}" / "Already on the seed list"; with nothing to add, "Nothing here can be added." in place of Add | Yes, fix the lines and Preview again | new in #711, not reviewed |
| A56 Add sites: the outcome | Add a list | 303 to `/seed`, flash | "Added {N} sites to the seed list; their lookups are queued." (one: "Added 1 site to the seed list; its lookup is queued.") / "Nothing to add: every site is already on the list or couldn't be read." | Yes | new in #711, not reviewed |
| A57 Add sites: too large | Code read: post over 1 MiB to `/seed/add` | 413 error page | "Request too large" / "That form submission was too large." | Back button only; add the list in parts | new in #711, not reviewed |
| A72 Add their sites: no one selected, or a tampered selection | Press Add their sites to the seed list with nothing ticked; code read: post an `id` that isn't a UUID, or over 100, to `/seed/add/waitlist` | 303 to the waitlist, flash; or the 400 error page | "Select at least one person." / "Bad request" / "That selection isn't valid." | Yes, tick someone | new in #714, not reviewed |
| A73 Add their sites: nothing to fill in | Add the sites of people who didn't answer the sites question, or whose answers have no addresses | 200, the Add sites form, empty or with only the list under it | "None of the people selected named any sites." / "Not website addresses, to look up and paste by hand:" with the parts | Yes, paste addresses by hand | new in #714, not reviewed |
| A58 Pick feeds: nothing to pick | Open `/discover/pick` when every found site's feeds are in Discover, skipped or gone (or, From the seed list, no seed entry has found a site); or `?filter=skipped` with no skips | 200, the Pick feeds page | "Nothing to pick: every found site’s feeds are in Discover or skipped." / "No skipped feeds." | Yes, another filter, or add sites to the seed list | new in #712, not reviewed |
| A59 Pick feeds: a ticked feed without a section | Tick a feed, leave its section on "Choose a section", Save | The Pick feeds page, 422, the posted ticks, sections and Skip the rest kept, nothing saved | "Choose a section." under the feed's select, which takes the focus, its row tinted, and "Nothing was saved: choose a section for each feed you ticked." above the table | Yes | new in #712, not reviewed |
| A60 Pick feeds: a feed refused on Save | Tick a feed that went gone, or whose section was deleted, or that was deleted, since the page loaded | The Pick feeds page, 422, nothing saved | "Nothing was saved: …" above the table and on the feed's row (if it's still listed): "This feed is gone, so it can't be in Discover." / "Choose one of the sections." / "This feed no longer exists." | Yes, untick it or choose another section | new in #712, not reviewed |
| A61 Pick feeds: a bad page or unskip | Open `/discover/pick?after=x` for a malformed cursor, a site not in the filter's order, or a page past the last; code read: unskip a feed that isn't skipped | 404 error page | As A2 | As A2 | new in #712, not reviewed |
| A62 Pick feeds: a tampered Save | Code read: post a feed or site ID that isn't a UUID to `/discover/pick` | 400 error page | "That page of picks isn't valid." | Back button | new in #712, not reviewed |
| A63 Pick feeds: the outcome | Save the page, or Unskip a feed | 303 to the filter's first page, flash | "Added {N} feeds to Discover and skipped {M}." (one: "Added 1 feed …") / "Unskipped: the feed is back in the queue." | Yes | new in #712, not reviewed |
| A49 Discover's catalog: empty | Open `/discover` before any feed is in the catalog, or a section with none | 200, the Discover page | "Discover’s catalog is empty: put a feed in a section from its page." / "No feeds in {section} yet." | Yes, from a feed's page | new in #648, not reviewed |
| A50 Discover's catalog: a gone feed | Put a feed in a section, then mark it gone (it answers 410) | 200, the Discover page | "Gone: hidden from Discover" in the danger tone under the feed | Yes, take it out or Clear gone on its page | new in #648, not reviewed |
| A51 Sites: none, or none match | Open `/sites` before any site is found, or search for a host or title nothing has, or a filter no site is under | 200, the Sites page | "No sites." / "No sites match “{q}”." | Yes, Clear or another filter | new in #706, not reviewed |
| A64 Users: none, or none match | Open `/users` before anyone signs up, or search for a start of an email or name nobody has | 200, the Users page | "No users yet." / "No users’ email or name starts with “{q}”." | Yes, Clear | new in #579, not reviewed |
| A65 Users: a missing user or page | Open `/users/{id}` for a deleted or unknown user, or `/users?after=x` for a malformed cursor or a page past the last | 404 error page | As A2 | As A2 | new in #579, not reviewed |
| A66 A user's page: nothing to show | A reader with no emails in the outbox since they joined (none sent, or all over a week old), no outstanding email links, no imports or no invite | 200, the reader's page | "No emails to them in the outbox, which keeps sent and failed emails for a week." / "No email links waiting to be used." / Imports "none yet" / Invite "None" | n/a | new in #579, not reviewed |
| A67 A user's page: a failed email | An email to the reader (their address, or the new one of a change of address) that the outbox gave up on | 200, the reader's page | "Failed" in the danger tone, with its attempts | Yes, Resend confirmation or Send a password reset link | changed in #590, not reviewed |
| A68 A user's actions: refused | Resend confirmation or Mark confirmed on an account confirmed since the page loaded; Resend, Mark confirmed or Send a password reset link after the account's address changed since the page loaded; or send a link to an address with 20 emails queued in the last hour | 303 back to the reader's page, flash | "Their email address is already confirmed." / "Their email address changed since the page loaded: check it, then try again." / "Nothing sent: {email} has had too many emails in the last hour. Try again later." | Yes: the page shows the account as it is now | new in #590, not reviewed |
| A69 A user's actions: a missing user | Post an action, or open `/users/{id}/delete`, for a deleted or unknown user | 404 error page | As A2 | As A2 | new in #590, not reviewed |
| A70 Delete account: the email mistyped | Submit `/users/{id}/delete` empty or with another address | The delete page, 422 | "Type {email} to confirm." under the field | Yes | new in #590, not reviewed |
| A71 Delete account: the email to them not queued | Delete an account whose address has had 20 emails in the last hour, or when queuing fails | 303 to Users, flash | "Account deleted. We couldn't email {email}: it has had too many emails in the last hour." / "Account deleted. The email to {email} to say so couldn't be queued." | No: the account is gone; write to them | new in #590, not reviewed |
| A52 Sites: a failed lookup | A site whose lookup (keyed by its host) failed | 200, the Sites list | "Failed {when} after {n} attempts." in the danger tone, the error cut to one line (whole on hover) | Yes, on the site's page, Check for new feeds | new in #706, not reviewed |
