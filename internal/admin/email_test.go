package admin

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// emailID is the UUID of the nth test email.
func emailID(n int) string { return fmt.Sprintf("50000000-0000-4000-8000-%012d", 100+n) }

// emailServer returns a logged-in admin's browser on a server with emails
// and the default mail limits, whose emails' links point at
// https://app.example.
func emailServer(t *testing.T, emails *fakeEmails) *browser {
	t.Helper()
	s, _ := newTestServerWithAdminConfig(t,
		Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001", AppURL: "https://app.example",
			MailLimits: MailLimits{Daily: 100, Monthly: 3000, Reserve: 10}},
		Services{Database: pinger{}, Emails: emails, Audit: &fakeAudit{}})
	return loggedIn(t, s)
}

// testOutbox is the outbox, newest first: a pending email not yet tried,
// one waiting to be tried again whose error is markup, one sent, one
// failed to a reader's address and one failed to an address with no
// account.
func testOutbox() []rss.AdminEmail {
	return []rss.AdminEmail{
		{ID: emailID(1), To: "new@example.com", Subject: "Confirm your email", Status: rss.EmailPending,
			NextAttemptAt: testNow, CreatedAt: testNow.Add(-time.Minute)},
		{ID: emailID(2), To: "retry@example.com", Subject: "Reset your password", Status: rss.EmailPending, Attempts: 2,
			NextAttemptAt: testNow.Add(10 * time.Minute), LastError: `<b>dial tcp: connection refused</b>`, CreatedAt: testNow.Add(-time.Hour)},
		{ID: emailID(3), To: "sent@example.com", Subject: "You're invited", Status: rss.EmailSent, Attempts: 1,
			CreatedAt: testNow.Add(-2 * time.Hour), SentAt: testNow.Add(-2 * time.Hour)},
		{ID: emailID(4), To: "reader@example.com", Subject: "Reset your password", Status: rss.EmailFailed, Attempts: 8,
			LastError: "550 mailbox unavailable", CreatedAt: testNow.Add(-24 * time.Hour), UserID: userID(4)},
		{ID: emailID(5), To: "nobody@example.com", Subject: "Confirm your place", Status: rss.EmailFailed, Attempts: 8,
			LastError: "canceled from the admin", CreatedAt: testNow.Add(-48 * time.Hour)},
	}
}

// emailRowOf returns the email page's row for the email id.
func emailRowOf(t *testing.T, body, id string) string {
	t.Helper()
	start := strings.Index(body, `<tr id="email-`+id+`">`)
	if start < 0 {
		t.Fatalf("no row for email %s in:\n%s", id, body)
	}
	end := strings.Index(body[start:], "</tr>")
	return body[start : start+end]
}

func TestEmailPage(t *testing.T) {
	b := emailServer(t, &fakeEmails{outbox: testOutbox(), usage: rss.EmailUsage{Last24h: 42, ThisMonth: 1234}})

	rr := b.get(t, "/email")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /email = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/email" aria-current="page">Email</a>`,
		// The usage tiles, against the limits, with the reserve.
		"<dt>Used today</dt>\n    <dd>42</dd>\n    <dd class=\"stat-note\">of 100 a day, in the last 24 hours</dd>",
		"<dt>Used this month</dt>\n    <dd>1,234</dd>\n    <dd class=\"stat-note\">of 3,000 a month, UTC</dd>",
		"<dt>Kept for account emails</dt>\n    <dd>10</dd>",
		// min(100-10-42, 3000-10-1234) = 48.
		"<dt>Invites can send</dt>\n    <dd>48</dd>",
		// The filters, with their counts, All current.
		`<a href="/email" aria-current="page">All <span class="seg-count">5</span></a>`,
		`<a href="/email?status=pending">Pending <span class="seg-count">2</span></a>`,
		`<a href="/email?status=sent">Sent <span class="seg-count">1</span></a>`,
		`<a href="/email?status=failed">Failed <span class="seg-count">2</span></a>`,
		"The outbox deletes sent and failed emails after a week.",
		`<form class="admin-form" action="/email/test" method="post" data-busy>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page doesn't contain %q", want)
		}
	}
	if strings.Contains(body, "<b>dial") {
		t.Error("an email's error isn't escaped")
	}

	rowTests := []struct {
		name      string
		id        int
		want, not []string
	}{
		{
			name: "pending, not yet tried",
			id:   1,
			want: []string{"new@example.com", "Confirm your email", `<span class="status status-pending">Pending</span>`, "Due now",
				`action="/email/` + emailID(1) + `/cancel"`},
			not: []string{"Retry now", "<code>"},
		},
		{
			name: "pending, waiting to be tried again",
			id:   2,
			want: []string{`<td class="num">2</td>`, ">in 10 min<", "<code>&lt;b&gt;dial tcp: connection refused&lt;/b&gt;</code>",
				`<form action="/email/` + emailID(2) + `/retry" method="post"><button class="btn btn-sm" type="submit">Retry now</button></form>`,
				`action="/email/` + emailID(2) + `/cancel"`},
		},
		{
			name: "sent",
			id:   3,
			want: []string{`<span class="status status-redeemed">Sent</span>`, ">2 h ago<"},
			not:  []string{"Retry now", "Cancel", "can’t be resent"},
		},
		{
			name: "failed, to a reader",
			id:   4,
			want: []string{`<span class="status status-danger">Failed</span>`, "<code>550 mailbox unavailable</code>",
				`This email can’t be resent: its contents were cleared. Send the person a new one from <a href="/users/` + userID(4) + `">their page</a>.`},
			not: []string{"Retry now", "Cancel"},
		},
		{
			name: "failed, to an address with no account",
			id:   5,
			want: []string{"This email can’t be resent: its contents were cleared.</td>", "<code>canceled from the admin</code>"},
			not:  []string{"Retry now", "Cancel", "their page"},
		},
	}
	for _, tt := range rowTests {
		t.Run(tt.name, func(t *testing.T) {
			row := emailRowOf(t, body, emailID(tt.id))
			for _, want := range tt.want {
				if !strings.Contains(row, want) {
					t.Errorf("row doesn't contain %q:\n%s", want, row)
				}
			}
			for _, not := range tt.not {
				if strings.Contains(row, not) {
					t.Errorf("row contains %q:\n%s", not, row)
				}
			}
		})
	}
}

func TestEmailPageFilters(t *testing.T) {
	b := emailServer(t, &fakeEmails{outbox: testOutbox()})

	tests := []struct {
		path      string
		want, not []string
	}{
		{"/email?status=failed", []string{emailID(4), emailID(5), `<a href="/email?status=failed" aria-current="page">Failed`}, []string{emailID(1), emailID(2), emailID(3)}},
		{"/email?status=pending", []string{emailID(1), emailID(2)}, []string{emailID(3), emailID(4), emailID(5)}},
		{"/email?status=sent", []string{emailID(3)}, []string{emailID(1), emailID(4)}},
		// A status the page doesn't offer is all of them.
		{"/email?status=bogus", []string{emailID(1), emailID(5), `<a href="/email" aria-current="page">All`}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rr := b.get(t, tt.path)
			if rr.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", tt.path, rr.Code)
			}
			body := rr.Body.String()
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Errorf("page doesn't contain %q", want)
				}
			}
			for _, not := range tt.not {
				if strings.Contains(body, `id="email-`+not+`"`) {
					t.Errorf("page lists email %s", not)
				}
			}
		})
	}

	t.Run("no emails under a filter", func(t *testing.T) {
		b := emailServer(t, &fakeEmails{outbox: testOutbox()[2:]})
		if body := b.get(t, "/email?status=pending").Body.String(); !strings.Contains(body, `<p class="empty">No pending emails.</p>`) {
			t.Errorf("page doesn't say there are no pending emails:\n%s", body)
		}
		b = emailServer(t, &fakeEmails{})
		if body := b.get(t, "/email").Body.String(); !strings.Contains(body, `<p class="empty">No emails in the outbox.`) {
			t.Errorf("page doesn't say the outbox is empty:\n%s", body)
		}
	})
}

func TestEmailPagePages(t *testing.T) {
	var outbox []rss.AdminEmail
	for i := range emailPageSize + 5 {
		outbox = append(outbox, rss.AdminEmail{ID: emailID(i), To: "a@example.com", Subject: "s", Status: rss.EmailFailed, CreatedAt: testNow.Add(-time.Duration(i) * time.Minute)})
	}
	b := emailServer(t, &fakeEmails{outbox: outbox})

	body := b.get(t, "/email?status=failed").Body.String()
	next := `href="/email?after=` + emailID(emailPageSize-1) + `&amp;status=failed">Older</a>`
	if !strings.Contains(body, next) {
		t.Fatalf("first page doesn't link to the next with %q", next)
	}
	if strings.Contains(body, `id="email-`+emailID(emailPageSize)+`"`) {
		t.Error("first page lists more than a page")
	}
	body = b.get(t, "/email?status=failed&after="+emailID(emailPageSize-1)).Body.String()
	if !strings.Contains(body, `id="email-`+emailID(emailPageSize)+`"`) || strings.Contains(body, "Older") {
		t.Error("the last page doesn't list the rest, or links to another")
	}

	for _, path := range []string{"/email?after=x", "/email?after=" + emailID(9999)} {
		if rr := b.get(t, path); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rr.Code)
		}
	}
}

// TestEmailPageHoldsNoBodies checks the page's emails can't carry their
// bodies, which can hold one-time links.
func TestEmailPageHoldsNoBodies(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[emailRow](), reflect.TypeFor[rss.AdminEmail]()} {
		for f := range typ.Fields() {
			for _, bad := range []string{"body", "text", "html", "content"} {
				if strings.Contains(strings.ToLower(f.Name), bad) {
					t.Errorf("%s.%s may hold an email's body", typ, f.Name)
				}
			}
		}
	}
}

func TestEmailActions(t *testing.T) {
	tests := []struct {
		name, action, method, flash string
	}{
		{"retry", "retry", "Retry", "Retrying: www’s mail runner sends it within a minute."},
		{"cancel", "cancel", "Cancel", "Canceled: it won’t be sent."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			emails := &fakeEmails{outbox: testOutbox()}
			b := emailServer(t, emails)
			rr := b.post(t, "/email/"+emailID(2)+"/"+tt.action, nil)
			followEmailFlash(t, b, rr, tt.flash)
			checkEmailCall(t, emails, tt.method, emailID(2))
		})
		t.Run(tt.name+" an email no longer pending", func(t *testing.T) {
			emails := &fakeEmails{outbox: testOutbox(), actionErr: rss.ErrEmailNotPending}
			b := emailServer(t, emails)
			rr := b.post(t, "/email/"+emailID(4)+"/"+tt.action, nil)
			followEmailFlash(t, b, rr, "That email isn’t pending any more: it was sent or given up on.")
		})
		t.Run(tt.name+" an unknown email", func(t *testing.T) {
			emails := &fakeEmails{actionErr: rss.ErrNoRecord}
			b := emailServer(t, emails)
			if rr := b.post(t, "/email/"+emailID(9)+"/"+tt.action, nil); rr.Code != http.StatusNotFound {
				t.Errorf("POST = %d, want 404", rr.Code)
			}
			if rr := b.post(t, "/email/x/"+tt.action, nil); rr.Code != http.StatusNotFound {
				t.Errorf("POST with a malformed ID = %d, want 404", rr.Code)
			}
		})
	}
}

// followEmailFlash checks rr redirects to the email page and that it says
// flash.
func followEmailFlash(t *testing.T, b *browser, rr *httptest.ResponseRecorder, flash string) {
	t.Helper()
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/email" {
		t.Fatalf("response = %d to %q, want 303 to /email", rr.Code, rr.Header().Get("Location"))
	}
	page := b.get(t, "/email").Body.String()
	if !strings.Contains(page, `<div class="flash" role="status">`+html.EscapeString(flash)) {
		t.Errorf("the email page doesn't say %q:\n%s", flash, page)
	}
}

// checkEmailCall checks emails' one call is method on id, by the logged-in
// admin from the web, so the repository records it as theirs.
func checkEmailCall(t *testing.T, emails *fakeEmails, method, id string) {
	t.Helper()
	if len(emails.calls) != 1 {
		t.Fatalf("calls = %+v, want one %s", emails.calls, method)
	}
	c := emails.calls[0]
	if c.method != method || c.id != id {
		t.Errorf("call = %s on %s, want %s on %s", c.method, c.id, method, id)
	}
	if c.by.AdminID == "" || c.by.AdminEmail != testEmail || c.by.Via != rss.AuditViaWeb || c.by.IP == "" {
		t.Errorf("%s by %+v, want the logged-in admin from the web", c.method, c.by)
	}
}

func TestEmailTest(t *testing.T) {
	t.Run("queues the test email", func(t *testing.T) {
		emails := &fakeEmails{}
		b := emailServer(t, emails)
		rr := b.post(t, "/email/test", url.Values{"to": {"  me@example.com "}})
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/email" {
			t.Fatalf("POST /email/test = %d to %q, want 303 to /email", rr.Code, rr.Header().Get("Location"))
		}
		checkEmailCall(t, emails, "QueueTest", "me@example.com")
		if len(emails.queued) != 1 {
			t.Fatalf("queued %d emails, want 1", len(emails.queued))
		}
		m := emails.queued[0]
		if m.To != "me@example.com" || m.Subject != "Test email from Paper Round" || !strings.Contains(m.Text, "https://app.example") {
			t.Errorf("queued %+v, want the test email to me@example.com with the reader's URL", m)
		}
		page := b.get(t, "/email").Body.String()
		want := `<div class="flash" role="status">Test email queued to me@example.com. <a href="#email-` + testEmailID + `">See it below</a>.</div>`
		if !strings.Contains(page, want) {
			t.Errorf("the email page doesn't say %q:\n%s", want, page)
		}
		// The link is only for the next page.
		if page := b.get(t, "/email").Body.String(); strings.Contains(page, "See it below") {
			t.Error("the flash's link shows again")
		}
	})

	t.Run("an address over the per-address cap", func(t *testing.T) {
		emails := &fakeEmails{err: rss.ErrTooManyEmails}
		b := emailServer(t, emails)
		rr := b.post(t, "/email/test", url.Values{"to": {"me@example.com"}})
		followEmailFlash(t, b, rr, tooManyEmailsMessage("me@example.com"))
	})

	for _, tt := range []struct{ to, msg string }{
		{"", "Enter an email address."},
		{"  ", "Enter an email address."},
		{"not an address", "Enter an email address, like you@example.com."},
	} {
		t.Run(fmt.Sprintf("invalid %q", tt.to), func(t *testing.T) {
			emails := &fakeEmails{outbox: testOutbox()}
			b := emailServer(t, emails)
			rr := b.post(t, "/email/test", url.Values{"to": {tt.to}})
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST /email/test = %d, want 422", rr.Code)
			}
			body := rr.Body.String()
			for _, want := range []string{
				`<p class="error" id="to-error">` + html.EscapeString(tt.msg) + `</p>`,
				`aria-describedby="to-hint to-error"`,
				`aria-invalid="true" autofocus`,
				// The rest of the page still shows.
				`id="email-` + emailID(1) + `"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("page doesn't contain %q", want)
				}
			}
			if len(emails.calls) != 0 {
				t.Errorf("calls = %+v, want none", emails.calls)
			}
		})
	}

	t.Run("an error queuing it", func(t *testing.T) {
		b := emailServer(t, &fakeEmails{err: errors.New("boom")})
		if rr := b.post(t, "/email/test", url.Values{"to": {"me@example.com"}}); rr.Code != http.StatusInternalServerError {
			t.Errorf("POST /email/test = %d, want 500", rr.Code)
		}
	})
}
