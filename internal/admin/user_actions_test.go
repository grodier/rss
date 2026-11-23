package admin

import (
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// actionsServer is a logged-in admin's browser on a server with users and
// emails, whose links point at https://app.example, and the server's
// clock.
func actionsServer(t *testing.T, users *fakeUsers, emails *fakeEmails) (*browser, *time.Time) {
	t.Helper()
	s, now := newTestServerWithAdminConfig(t,
		Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001", AppURL: "https://app.example"},
		Services{Database: pinger{}, Users: users, Emails: emails, Audit: &fakeAudit{}})
	return loggedIn(t, s), now
}

// unconfirmedUser is a reader who hasn't confirmed their address.
func unconfirmedUser() rss.AdminUser {
	row := testUserRow(1)
	row.EmailConfirmedAt = time.Time{}
	return rss.AdminUser{AdminUserRow: row}
}

// confirmedUser is a reader who has.
func confirmedUser() rss.AdminUser {
	return rss.AdminUser{AdminUserRow: testUserRow(2)}
}

// followFlash checks rr redirects to want and that the page there says
// flash.
func followFlash(t *testing.T, b *browser, rr *httptest.ResponseRecorder, want, flash string) {
	t.Helper()
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != want {
		t.Fatalf("response = %d to %q, want 303 to %q", rr.Code, rr.Header().Get("Location"), want)
	}
	page := b.get(t, want)
	if !strings.Contains(page.Body.String(), `<div class="flash" role="status">`+html.EscapeString(flash)+`</div>`) {
		t.Errorf("%s doesn't say %q:\n%s", want, flash, page.Body)
	}
}

// checkActor checks the call was asked by the logged-in admin, from the
// web, so the repository records it as theirs.
func checkActor(t *testing.T, c userCall) {
	t.Helper()
	if c.by.AdminID == "" || c.by.AdminEmail != testEmail || c.by.Via != rss.AuditViaWeb || c.by.IP == "" {
		t.Errorf("%s by %+v, want the logged-in admin from the web", c.method, c.by)
	}
}

// onlyCall returns users' one call, failing unless there's exactly one,
// of method on id.
func onlyCall(t *testing.T, users *fakeUsers, method, id string) userCall {
	t.Helper()
	if len(users.calls) != 1 {
		t.Fatalf("calls = %+v, want one %s", users.calls, method)
	}
	c := users.calls[0]
	if c.method != method || c.id != id {
		t.Fatalf("call = %s on %s, want %s on %s", c.method, c.id, method, id)
	}
	checkActor(t, c)
	return c
}

func TestUserActionsSection(t *testing.T) {
	tests := []struct {
		name string
		user rss.AdminUser
		want []string
		not  []string
	}{
		{"unconfirmed", unconfirmedUser(),
			[]string{"/resend-confirmation", "/mark-confirmed", "/send-password-reset", "/log-out-everywhere", `href="/users/` + userID(1) + `/delete"`,
				`<input type="hidden" name="email" value="reader1@example.com" />`},
			nil},
		{"confirmed", confirmedUser(),
			[]string{"/send-password-reset", "/log-out-everywhere", `href="/users/` + userID(2) + `/delete"`},
			[]string{"/resend-confirmation", "/mark-confirmed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := actionsServer(t, &fakeUsers{user: tt.user}, &fakeEmails{})
			body := b.get(t, "/users/"+tt.user.ID).Body.String()
			for _, s := range tt.want {
				if !strings.Contains(body, s) {
					t.Errorf("the page lacks %s", s)
				}
			}
			for _, s := range tt.not {
				if strings.Contains(body, s) {
					t.Errorf("the page offers %s", s)
				}
			}
		})
	}
}

func TestUserResendConfirmation(t *testing.T) {
	user := unconfirmedUser()
	users := &fakeUsers{user: user}
	b, _ := actionsServer(t, users, &fakeEmails{})

	rr := b.post(t, "/users/"+user.ID+"/resend-confirmation", url.Values{"email": {user.Email}})
	followFlash(t, b, rr, "/users/"+user.ID, "Confirmation sent to "+user.Email+".")
	c := onlyCall(t, users, "SendLink", user.ID)
	if c.token.Purpose != rss.TokenConfirmEmail || c.token.Email != user.Email || c.email.To != user.Email || len(c.token.Hash) == 0 {
		t.Errorf("SendLink(%+v, to %q), want a confirmation to %s", c.token, c.email.To, user.Email)
	}
	if !strings.Contains(c.email.Text, "https://app.example/confirm-email?token=") {
		t.Errorf("the email has no link to the reader:\n%s", c.email.Text)
	}

	addressActionErrors(t, "/resend-confirmation", unconfirmedUser)
}

func TestUserSendPasswordReset(t *testing.T) {
	user := confirmedUser()
	users := &fakeUsers{user: user}
	b, _ := actionsServer(t, users, &fakeEmails{})

	rr := b.post(t, "/users/"+user.ID+"/send-password-reset", url.Values{"email": {user.Email}})
	followFlash(t, b, rr, "/users/"+user.ID, "Reset link sent to "+user.Email+".")
	c := onlyCall(t, users, "SendLink", user.ID)
	if c.token.Purpose != rss.TokenResetPassword || c.token.Email != user.Email || c.email.To != user.Email {
		t.Errorf("SendLink(%+v, to %q), want a reset link to %s", c.token, c.email.To, user.Email)
	}
	if !strings.Contains(c.email.Text, "https://app.example/password/reset?token=") {
		t.Errorf("the email has no link to the reader:\n%s", c.email.Text)
	}

	addressActionErrors(t, "/send-password-reset", confirmedUser)
}

func TestUserMarkConfirmed(t *testing.T) {
	user := unconfirmedUser()
	users := &fakeUsers{user: user}
	b, _ := actionsServer(t, users, &fakeEmails{})
	rr := b.post(t, "/users/"+user.ID+"/mark-confirmed", url.Values{"email": {user.Email}})
	followFlash(t, b, rr, "/users/"+user.ID, "Marked "+user.Email+" as confirmed.")
	if c := onlyCall(t, users, "MarkConfirmed", user.ID); c.address != user.Email {
		t.Errorf("MarkConfirmed(%q), want %q", c.address, user.Email)
	}

	addressActionErrors(t, "/mark-confirmed", unconfirmedUser)
}

// addressActionErrors checks that the action at path, about the address
// user's page showed, acts on the address the form posts, not the one the
// account has now, which its store checks; and how it answers the store's
// errors.
func addressActionErrors(t *testing.T, path string, user func() rss.AdminUser) {
	t.Helper()
	t.Run("the address the page showed", func(t *testing.T) {
		u := user()
		users := &fakeUsers{user: u}
		b, _ := actionsServer(t, users, &fakeEmails{})
		b.post(t, "/users/"+u.ID+path, url.Values{"email": {"old@example.com"}})
		if len(users.calls) != 1 {
			t.Fatalf("calls = %+v, want one", users.calls)
		}
		c := users.calls[0]
		got := c.address
		if c.method == "SendLink" {
			got = c.token.Email
		}
		if got != "old@example.com" {
			t.Errorf("%s about %q, want the posted old@example.com", c.method, got)
		}
	})

	tests := []struct {
		err   error
		code  int
		flash string
	}{
		{rss.ErrAlreadyConfirmed, http.StatusSeeOther, alreadyConfirmedMessage},
		{rss.ErrAddressChanged, http.StatusSeeOther, addressChangedMessage},
		{rss.ErrTooManyEmails, http.StatusSeeOther, tooManyEmailsMessage(user().Email)},
		{rss.ErrNoRecord, http.StatusNotFound, ""},
		{errors.New("boom"), http.StatusInternalServerError, ""},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			u := user()
			b, _ := actionsServer(t, &fakeUsers{user: u, actionErr: tt.err}, &fakeEmails{})
			rr := b.post(t, "/users/"+u.ID+path, url.Values{"email": {u.Email}})
			if tt.flash != "" {
				followFlash(t, b, rr, "/users/"+u.ID, tt.flash)
			} else if rr.Code != tt.code {
				t.Errorf("POST %s with %v = %d, want %d", path, tt.err, rr.Code, tt.code)
			}
		})
	}
}

func TestUserLogOutEverywhere(t *testing.T) {
	for _, tt := range []struct {
		devices int
		flash   string
	}{
		{2, "Logged out of 2 devices."},
		{1, "Logged out of 1 device."},
		{0, "They weren't logged in on any device."},
	} {
		t.Run(tt.flash, func(t *testing.T) {
			user := confirmedUser()
			users := &fakeUsers{user: user, devices: tt.devices}
			b, _ := actionsServer(t, users, &fakeEmails{})
			rr := b.post(t, "/users/"+user.ID+"/log-out-everywhere", nil)
			followFlash(t, b, rr, "/users/"+user.ID, tt.flash)
			onlyCall(t, users, "LogOutEverywhere", user.ID)
		})
	}
}

func TestUserActionsMissingUser(t *testing.T) {
	for _, path := range []string{"/resend-confirmation", "/mark-confirmed", "/send-password-reset", "/log-out-everywhere", "/delete"} {
		for _, id := range []string{userID(9), "not-a-uuid"} {
			users := &fakeUsers{user: confirmedUser()}
			b, _ := actionsServer(t, users, &fakeEmails{})
			if rr := b.post(t, "/users/"+id+path, url.Values{"email": {"reader9@example.com"}}); rr.Code != http.StatusNotFound {
				t.Errorf("POST /users/%s%s = %d, want 404", id, path, rr.Code)
			}
			if len(users.calls) != 0 {
				t.Errorf("POST /users/%s%s called %+v", id, path, users.calls)
			}
		}
	}
	b, _ := actionsServer(t, &fakeUsers{user: confirmedUser()}, &fakeEmails{})
	if rr := b.get(t, "/users/"+userID(9)+"/delete"); rr.Code != http.StatusNotFound {
		t.Errorf("GET a missing user's delete page = %d, want 404", rr.Code)
	}
}

func TestUserDelete(t *testing.T) {
	user := confirmedUser()
	path := "/users/" + user.ID + "/delete"

	t.Run("the page", func(t *testing.T) {
		b, _ := actionsServer(t, &fakeUsers{user: user}, &fakeEmails{})
		rr := b.get(t, path)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rr.Code)
		}
		for _, want := range []string{`<form class="admin-form" action="` + path + `" method="post">`, `name="email" value=""`, user.Email, `<button class="btn-danger" type="submit">Delete account</button>`} {
			if !strings.Contains(rr.Body.String(), want) {
				t.Errorf("the page lacks %s", want)
			}
		}
	})

	t.Run("deletes, and emails them", func(t *testing.T) {
		users := &fakeUsers{user: user}
		emails := &fakeEmails{}
		b, _ := actionsServer(t, users, emails)
		// Any case, and spaces around it.
		rr := b.post(t, path, url.Values{"email": {"  " + strings.ToUpper(user.Email) + " "}})
		followFlash(t, b, rr, "/users", "Account deleted. We emailed "+user.Email+" to say so.")
		onlyCall(t, users, "Delete", user.ID)
		if len(emails.queued) != 1 || emails.queued[0].To != user.Email || !strings.Contains(emails.queued[0].Text, user.Name) {
			t.Errorf("queued %+v, want the account_deleted email to %s", emails.queued, user.Email)
		}
	})

	t.Run("the email over the cap", func(t *testing.T) {
		users := &fakeUsers{user: user}
		b, _ := actionsServer(t, users, &fakeEmails{err: rss.ErrTooManyEmails})
		rr := b.post(t, path, url.Values{"email": {user.Email}})
		followFlash(t, b, rr, "/users", "Account deleted. We couldn't email "+user.Email+": it has had too many emails in the last hour.")
		onlyCall(t, users, "Delete", user.ID)
	})

	t.Run("the email failing", func(t *testing.T) {
		b, _ := actionsServer(t, &fakeUsers{user: user}, &fakeEmails{err: errors.New("boom")})
		rr := b.post(t, path, url.Values{"email": {user.Email}})
		followFlash(t, b, rr, "/users", "Account deleted. The email to "+user.Email+" to say so couldn't be queued.")
	})

	for _, typed := range []string{"", "reader2@example.org", "someone@example.com"} {
		t.Run("refuses "+typed, func(t *testing.T) {
			users := &fakeUsers{user: user}
			emails := &fakeEmails{}
			b, _ := actionsServer(t, users, emails)
			rr := b.post(t, path, url.Values{"email": {typed}})
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST with %q = %d, want 422", typed, rr.Code)
			}
			for _, want := range []string{`<p class="error" id="email-error">Type ` + user.Email + ` to confirm.</p>`, `aria-describedby="email-hint email-error"`, `aria-invalid="true"`, `value="` + typed + `"`} {
				if !strings.Contains(rr.Body.String(), want) {
					t.Errorf("the page lacks %s:\n%s", want, rr.Body)
				}
			}
			if len(users.calls) != 0 || len(emails.queued) != 0 {
				t.Errorf("calls %+v, emails %+v; want none", users.calls, emails.queued)
			}
		})
	}

	t.Run("deleted since the page loaded", func(t *testing.T) {
		b, _ := actionsServer(t, &fakeUsers{user: user, actionErr: rss.ErrNoRecord}, &fakeEmails{})
		if rr := b.post(t, path, url.Values{"email": {user.Email}}); rr.Code != http.StatusNotFound {
			t.Errorf("POST = %d, want 404", rr.Code)
		}
	})

	t.Run("asks for a recent code", func(t *testing.T) {
		users := &fakeUsers{user: user}
		b, now := actionsServer(t, users, &fakeEmails{})
		*now = now.Add(recentCodeWindow + time.Second)

		rr := b.get(t, path)
		if want := "/login/code?next=" + url.QueryEscape(path); rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != want {
			t.Errorf("GET with an old code = %d to %q, want 303 to %q", rr.Code, rr.Header().Get("Location"), want)
		}
		rr = b.post(t, path, url.Values{"email": {user.Email}})
		if want := "/login/code?next=" + url.QueryEscape(path); rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != want {
			t.Errorf("POST with an old code = %d to %q, want 303 to %q", rr.Code, rr.Header().Get("Location"), want)
		}
		if len(users.calls) != 0 {
			t.Errorf("calls = %+v, want none", users.calls)
		}
	})
}
