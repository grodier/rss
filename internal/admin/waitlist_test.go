package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

const testAppURL = "https://app.example.com"

// waitlistServer returns a logged-in browser to a server with the fakes,
// the email limits free (100 a day, 3000 a month, 10 kept back) and
// testAppURL.
func waitlistServer(t *testing.T, wl *fakeWaitlist, inv *fakeInvites, em *fakeEmails) *browser {
	t.Helper()
	cfg := Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001",
		AppURL: testAppURL, MailLimits: MailLimits{Daily: 100, Monthly: 3000, Reserve: 10}}
	s, _ := newTestServerWithAdminConfig(t, cfg, Services{Database: pinger{}, Waitlist: wl, Invites: inv, Emails: em})
	return loggedIn(t, s)
}

// entryID is the UUID of the nth test entry.
func entryID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

// waitingRow is the nth test entry, confirmed and waiting, joined n hours
// after the first.
func waitingRow(n int) rss.WaitlistRow {
	joined := testNow.Add(-1000*time.Hour + time.Duration(n)*time.Hour)
	return rss.WaitlistRow{WaitlistEntry: rss.WaitlistEntry{
		ID: entryID(n), Email: fmt.Sprintf("person%d@example.com", n), Name: fmt.Sprintf("Person %d", n),
		Sites: "lwn.net", Source: "hn", CreatedAt: joined, ConfirmedAt: joined.Add(time.Minute),
	}}
}

// testWaitlist has two waiting entries (one whose invite expired), one
// invited and one unconfirmed.
func testWaitlist() *fakeWaitlist {
	expired, invited, unconfirmed := waitingRow(2), waitingRow(3), waitingRow(4)
	expired.Invite = rss.Invite{ID: "inv-2", ExpiresAt: testNow.Add(-time.Hour)}
	invited.Invite = rss.Invite{ID: "inv-3", ExpiresAt: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}
	unconfirmed.ConfirmedAt = time.Time{}
	return &fakeWaitlist{rows: []rss.WaitlistRow{waitingRow(1), expired, invited, unconfirmed}}
}

func TestWaitlistPage(t *testing.T) {
	b := waitlistServer(t, testWaitlist(), &fakeInvites{}, &fakeEmails{})

	rr := b.get(t, "/waitlist")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /waitlist = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`>Waiting <span class="count">2</span></a>`,
		`>Invited <span class="count">1</span></a>`,
		`>Unconfirmed <span class="count">1</span></a>`,
		`aria-current="page">Waiting`,
		"Person 1", "person1@example.com", "lwn.net", "hn",
		"Invite expired 2026-11-09",
		`name="id" value="` + entryID(1) + `"`,
		`name="oldest" value="2"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /waitlist lacks %q", want)
		}
	}
	for _, unwanted := range []string{"Person 3", "Person 4"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("GET /waitlist shows %q, who isn't waiting", unwanted)
		}
	}

	body = b.get(t, "/waitlist?tab=invited").Body.String()
	if !strings.Contains(body, "Person 3") || !strings.Contains(body, "Invited until 2026-11-20") {
		t.Errorf("the Invited tab lacks Person 3 and their invite's expiry:\n%s", body)
	}
	if strings.Contains(body, `name="id"`) || strings.Contains(body, `name="oldest"`) {
		t.Error("the Invited tab can select people; only waiting people can be invited")
	}

	body = b.get(t, "/waitlist?tab=unconfirmed").Body.String()
	if !strings.Contains(body, "Person 4") || !strings.Contains(body, "Unconfirmed") {
		t.Errorf("the Unconfirmed tab lacks Person 4")
	}

	for _, path := range []string{"/waitlist?tab=everyone", "/waitlist?page=0", "/waitlist?page=x", "/waitlist?page=2", "/waitlist?page=99999999999999999"} {
		if rr := b.get(t, path); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rr.Code)
		}
	}
}

func TestWaitlistPageEmpty(t *testing.T) {
	b := waitlistServer(t, &fakeWaitlist{}, &fakeInvites{}, &fakeEmails{})
	body := b.get(t, "/waitlist").Body.String()
	if !strings.Contains(body, "No one is waiting.") || strings.Contains(body, `name="oldest"`) {
		t.Errorf("an empty waitlist lacks its empty state, or offers to select people:\n%s", body)
	}
}

func TestWaitlistPagination(t *testing.T) {
	wl := &fakeWaitlist{}
	for n := 1; n <= waitlistPageSize+1; n++ {
		wl.rows = append(wl.rows, waitingRow(n))
	}
	b := waitlistServer(t, wl, &fakeInvites{}, &fakeEmails{})

	body := b.get(t, "/waitlist").Body.String()
	if !strings.Contains(body, `href="/waitlist?page=2"`) || strings.Contains(body, entryID(waitlistPageSize+1)) {
		t.Errorf("the first page lacks the next page's link, or shows more than a page")
	}
	body = b.get(t, "/waitlist?page=2").Body.String()
	if !strings.Contains(body, entryID(waitlistPageSize+1)) || strings.Contains(body, `value="`+entryID(1)+`"`) {
		t.Errorf("the second page doesn't show only the last entry")
	}
	if !strings.Contains(body, `href="/waitlist">Previous page`) || strings.Contains(body, "Next page") {
		t.Errorf("the second page lacks the previous page's link, or has a next one")
	}
}

func TestWaitlistReview(t *testing.T) {
	b := waitlistServer(t, testWaitlist(), &fakeInvites{}, &fakeEmails{usage: rss.EmailUsage{Last24h: 12, ThisMonth: 240}})

	rr := b.post(t, "/waitlist/review", url.Values{"id": {entryID(1), entryID(2)}})
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /waitlist/review = %d, want 200:\n%s", rr.Code, rr.Body)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Invite 2 people", "Person 1", "Person 2",
		"2 invites. 12 of 100 emails used today, 240 of 3000 this month.",
		`value="Wave 2026-11-09"`, `name="days" value="14"`,
		`name="id" value="` + entryID(1) + `"`, `name="id" value="` + entryID(2) + `"`,
		"Send 2 invites",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the review lacks %q", want)
		}
	}
	if strings.Contains(body, `role="alert"`) {
		t.Error("a wave well within the limits has an alert")
	}

	body = b.post(t, "/waitlist/review", url.Values{"oldest": {"1"}}).Body.String()
	if !strings.Contains(body, "Invite 1 person") || !strings.Contains(body, "Person 1") || strings.Contains(body, "Person 2") {
		t.Errorf("oldest=1 doesn't review only the oldest waiting entry:\n%s", body)
	}

	// Entries no longer waiting are left out.
	body = b.post(t, "/waitlist/review", url.Values{"id": {entryID(1), entryID(3)}}).Body.String()
	if !strings.Contains(body, "Invite 1 person") || strings.Contains(body, "Person 3") {
		t.Errorf("the review includes someone already invited:\n%s", body)
	}

	tests := []struct {
		name string
		form url.Values
		code int
		want string
	}{
		{"nobody selected", url.Values{}, http.StatusUnprocessableEntity, "Select between 1 and 100 people to invite."},
		{"oldest zero", url.Values{"oldest": {"0"}}, http.StatusUnprocessableEntity, "Choose between 1 and 100 people to invite."},
		{"oldest too many", url.Values{"oldest": {"101"}}, http.StatusUnprocessableEntity, "Choose between 1 and 100 people to invite."},
		{"oldest not a number", url.Values{"oldest": {"ten"}}, http.StatusUnprocessableEntity, "Choose between 1 and 100 people to invite."},
		{"not an ID", url.Values{"id": {"1 OR 1=1"}}, http.StatusBadRequest, "That selection isn&#39;t valid."},
		{"nobody waiting", url.Values{"id": {entryID(3), entryID(4)}}, http.StatusUnprocessableEntity, "Everyone selected has been invited or has left the waitlist."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := b.post(t, "/waitlist/review", tt.form)
			if rr.Code != tt.code || !strings.Contains(rr.Body.String(), tt.want) {
				t.Errorf("POST /waitlist/review = %d, want %d saying %q:\n%s", rr.Code, tt.code, tt.want, rr.Body)
			}
		})
	}
}

func TestWaitlistReviewLimits(t *testing.T) {
	wl := &fakeWaitlist{}
	for n := 1; n <= 12; n++ {
		wl.rows = append(wl.rows, waitingRow(n))
	}
	tests := []struct {
		name          string
		used, n       int
		want, unwants []string
	}{
		{name: "within 80% of the day", used: 12, n: 5,
			want:    []string{"Send 5 invites", "5 invites. 12 of 100 emails used today"},
			unwants: []string{`role="alert"`}},
		{name: "past 80%", used: 74, n: 8,
			want:    []string{"Send 8 invites", "This leaves 18 emails today for account emails like password resets."},
			unwants: []string{"fit today"}},
		{name: "over what fits", used: 85, n: 12,
			want:    []string{"Only 5 fit today without risking account emails.", "Send the oldest 5", "Waits"},
			unwants: []string{"Send 12 invites", `value="` + entryID(6) + `"`}},
		{name: "none fit", used: 95, n: 3,
			want:    []string{"No more invites fit today without risking account emails."},
			unwants: []string{"Send the oldest", "Send 3 invites"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := waitlistServer(t, wl, &fakeInvites{}, &fakeEmails{usage: rss.EmailUsage{Last24h: tt.used, ThisMonth: tt.used}})
			body := b.post(t, "/waitlist/review", url.Values{"oldest": {fmt.Sprint(tt.n)}}).Body.String()
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Errorf("the review lacks %q", want)
				}
			}
			for _, unwanted := range tt.unwants {
				if strings.Contains(body, unwanted) {
					t.Errorf("the review has %q", unwanted)
				}
			}
		})
	}
}

// signupToken matches an invite's link in its email.
var signupToken = regexp.MustCompile(regexp.QuoteMeta(testAppURL) + `/signup\?invite=([A-Za-z0-9_-]+)`)

// leaveToken matches an entry's leave link in its invite.
var leaveToken = regexp.MustCompile(regexp.QuoteMeta(testAppURL) + `/waitlist/leave\?token=([A-Za-z0-9_-]+)`)

func TestWaitlistInvite(t *testing.T) {
	inv := &fakeInvites{}
	b := waitlistServer(t, testWaitlist(), inv, &fakeEmails{})

	rr := b.post(t, "/waitlist/invite", url.Values{"id": {entryID(1), entryID(2)}, "wave": {"  Friends  "}, "days": {"7"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/waitlist?tab=invited" {
		t.Fatalf("POST /waitlist/invite = %d to %q, want 303 to /waitlist?tab=invited:\n%s", rr.Code, rr.Header().Get("Location"), rr.Body)
	}
	if len(inv.waves) != 1 {
		t.Fatalf("sent %d waves, want 1", len(inv.waves))
	}
	w := inv.waves[0]
	if w.Label != "Friends" || !w.ExpiresAt.Equal(testNow.Add(7*24*time.Hour)) || len(w.Invites) != 2 {
		t.Fatalf("wave = %q expiring %v with %d invites; want Friends, in 7 days, with 2", w.Label, w.ExpiresAt, len(w.Invites))
	}
	if inv.available != 90 {
		t.Errorf("the wave was checked against %d available, want the limits' 90", inv.available)
	}
	if by := inv.actors[0]; by.AdminEmail != testEmail || by.Via != rss.AuditViaWeb {
		t.Errorf("sent by %+v, want the test admin from the web", by)
	}
	for i, n := range []int{1, 2} {
		in := w.Invites[i]
		if in.WaitlistID != entryID(n) || in.Email != fmt.Sprintf("person%d@example.com", n) {
			t.Errorf("invite %d is for %s %s, want entry %d", i, in.WaitlistID, in.Email, n)
		}
		if in.Subject != "Your invite to Paper Round" {
			t.Errorf("invite %d's subject = %q", i, in.Subject)
		}
		for _, body := range []string{in.Text, in.HTML} {
			if !strings.Contains(body, fmt.Sprintf("Hi Person %d,", n)) || !strings.Contains(body, "until 16 November 2026") {
				t.Errorf("invite %d's email lacks its greeting or expiry:\n%s", i, body)
			}
			m := signupToken.FindStringSubmatch(body)
			if m == nil || string(token.Hash(m[1])) != string(in.InviteTokenHash) {
				t.Errorf("invite %d's email lacks the signup link of its token:\n%s", i, body)
			}
			m = leaveToken.FindStringSubmatch(body)
			if m == nil || string(token.Hash(m[1])) != string(in.WaitlistTokenHash) {
				t.Errorf("invite %d's email lacks the leave link of the entry's new token:\n%s", i, body)
			}
		}
	}
	if string(w.Invites[0].InviteTokenHash) == string(w.Invites[1].InviteTokenHash) {
		t.Error("two invites share a token")
	}

	if body := b.get(t, "/waitlist?tab=invited").Body.String(); !strings.Contains(body, "Sent 2 invites in Friends.") {
		t.Errorf("the waitlist lacks the flash after sending:\n%s", body)
	}
}

func TestWaitlistInviteInvalid(t *testing.T) {
	inv := &fakeInvites{}
	b := waitlistServer(t, testWaitlist(), inv, &fakeEmails{})
	tests := []struct {
		name string
		form url.Values
		code int
		want string
	}{
		{"blank label", url.Values{"id": {entryID(1)}, "wave": {"  "}, "days": {"14"}}, http.StatusUnprocessableEntity, "Give the wave a label."},
		{"long label", url.Values{"id": {entryID(1)}, "wave": {strings.Repeat("w", 101)}, "days": {"14"}}, http.StatusUnprocessableEntity, "Keep the label to 100 characters."},
		{"no days", url.Values{"id": {entryID(1)}, "wave": {"W"}, "days": {"0"}}, http.StatusUnprocessableEntity, "Choose between 1 and 90 days."},
		{"too many days", url.Values{"id": {entryID(1)}, "wave": {"W"}, "days": {"91"}}, http.StatusUnprocessableEntity, "Choose between 1 and 90 days."},
		{"nobody", url.Values{"wave": {"W"}, "days": {"14"}}, http.StatusUnprocessableEntity, "Select between 1 and 100 people to invite."},
		{"not an ID", url.Values{"id": {"x"}, "wave": {"W"}, "days": {"14"}}, http.StatusBadRequest, "That selection isn&#39;t valid."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := b.post(t, "/waitlist/invite", tt.form)
			if rr.Code != tt.code || !strings.Contains(rr.Body.String(), tt.want) {
				t.Errorf("POST /waitlist/invite = %d, want %d saying %q:\n%s", rr.Code, tt.code, tt.want, rr.Body)
			}
		})
	}
	if len(inv.waves) != 0 {
		t.Errorf("sent %d waves from invalid forms", len(inv.waves))
	}
}

func TestWaitlistInviteChanged(t *testing.T) {
	// Since the review: the limits filled up, or someone was invited
	// elsewhere (seen here, or only in the transaction). The review comes
	// back as things are now, and nothing is sent.
	tests := []struct {
		name string
		ids  []string
		err  error
		want string
	}{
		{"over the limits", []string{entryID(1), entryID(2)}, rss.ErrOverLimit, "More emails were sent since you reviewed this wave"},
		{"someone invited", []string{entryID(1), entryID(3)}, nil, "Some of them were invited or left the waitlist since you reviewed this wave"},
		{"someone invited meanwhile", []string{entryID(1), entryID(2)}, rss.ErrNoRecord, "Some of them were invited or left the waitlist since you reviewed this wave"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := &fakeInvites{err: tt.err}
			b := waitlistServer(t, testWaitlist(), inv, &fakeEmails{usage: rss.EmailUsage{Last24h: 85, ThisMonth: 85}})
			rr := b.post(t, "/waitlist/invite", url.Values{"id": tt.ids, "wave": {"My wave"}, "days": {"10"}})
			body := rr.Body.String()
			if rr.Code != http.StatusConflict || !strings.Contains(body, tt.want) {
				t.Fatalf("POST /waitlist/invite = %d, want 409 saying %q:\n%s", rr.Code, tt.want, body)
			}
			for _, want := range []string{"85 of 100 emails used today", `value="My wave"`, `name="days" value="10"`} {
				if !strings.Contains(body, want) {
					t.Errorf("the review shown again lacks %q", want)
				}
			}
			if len(inv.waves) != 0 {
				t.Error("a wave was sent")
			}
		})
	}
}
