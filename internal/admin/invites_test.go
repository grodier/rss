package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

// inviteID is the UUID of the nth test invite.
func inviteID(n int) string { return fmt.Sprintf("10000000-0000-4000-8000-%012d", n) }

// testInvites are an invite in each status, newest first: a pending bound
// one (from entry 1), an expired bound one, a used one, a revoked one and a
// pending open one with a note.
func testInvites() *fakeInvites {
	day := func(d int) time.Time { return testNow.Add(time.Duration(d) * 24 * time.Hour) }
	return &fakeInvites{
		invites: []rss.Invite{
			{ID: inviteID(1), Email: "person1@example.com", WaitlistID: entryID(1), Name: "Person 1", Wave: "Wave A", CreatedAt: day(-1), ExpiresAt: day(13)},
			{ID: inviteID(2), Email: "expired@example.com", Wave: "Wave A", CreatedAt: day(-20), ExpiresAt: day(-6)},
			{ID: inviteID(3), Email: "used@example.com", Wave: "Wave B", CreatedAt: day(-30), ExpiresAt: day(-16), RedeemedAt: day(-29)},
			{ID: inviteID(4), Email: "revoked@example.com", CreatedAt: day(-31), ExpiresAt: day(-17), RevokedAt: day(-30)},
			{ID: inviteID(5), Note: "Sam from the gym", CreatedAt: day(-32), ExpiresAt: day(20)},
		},
		waveSum: []rss.InviteWave{
			{Label: "Wave A", SentAt: day(-20), Sent: 10, SignedUp: 4, Active: 3},
			{Label: "Wave B", SentAt: day(-30), Sent: 5, SignedUp: 0, Active: 0},
		},
	}
}

func TestInvitesPage(t *testing.T) {
	b := waitlistServer(t, testWaitlist(), testInvites(), &fakeEmails{})

	rr := b.get(t, "/invites")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /invites = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Wave A", "10", "4 <small>40%</small>", "3 <small>75%</small>",
		"Wave B", "0 <small>0%</small>",
		"person1@example.com", "Sam from the gym",
		">Pending<", ">Expired<", ">Signed up<", ">Revoked<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /invites lacks %q", want)
		}
	}

	// Each status gets only the actions it can take.
	tests := []struct {
		id             string
		resend, revoke bool
	}{
		{inviteID(1), true, true},
		{inviteID(2), true, false},
		{inviteID(5), false, true},
	}
	for _, tt := range tests {
		if got := strings.Contains(body, "/invites/"+tt.id+"/resend"); got != tt.resend {
			t.Errorf("invite %s offers Resend = %t, want %t", tt.id, got, tt.resend)
		}
		if got := strings.Contains(body, "/invites/"+tt.id+"/revoke"); got != tt.revoke {
			t.Errorf("invite %s offers Revoke = %t, want %t", tt.id, got, tt.revoke)
		}
	}
	for _, id := range []string{inviteID(3), inviteID(4)} {
		if strings.Contains(body, "/invites/"+id+"/") {
			t.Errorf("invite %s, used or revoked, offers an action", id)
		}
	}
}

func TestInvitesPageEmpty(t *testing.T) {
	b := waitlistServer(t, testWaitlist(), &fakeInvites{}, &fakeEmails{})
	body := b.get(t, "/invites").Body.String()
	if !strings.Contains(body, "No invites yet.") || strings.Contains(body, "Waves") {
		t.Errorf("an empty invites page lacks its empty state, or shows waves:\n%s", body)
	}
}

func TestInvitesPagination(t *testing.T) {
	inv := &fakeInvites{waveSum: []rss.InviteWave{{Label: "Wave A", Sent: 1}}}
	for n := 1; n <= invitesPageSize+1; n++ {
		inv.invites = append(inv.invites, rss.Invite{ID: inviteID(n), Note: fmt.Sprintf("note %d", n), ExpiresAt: testNow.Add(time.Hour)})
	}
	b := waitlistServer(t, testWaitlist(), inv, &fakeEmails{})

	body := b.get(t, "/invites").Body.String()
	next := "/invites?before=" + inviteID(invitesPageSize)
	if !strings.Contains(body, `href="`+next+`"`) || strings.Contains(body, inviteID(invitesPageSize+1)) {
		t.Fatalf("the first page lacks the Older link, or shows more than a page")
	}
	body = b.get(t, next).Body.String()
	if !strings.Contains(body, inviteID(invitesPageSize+1)) || strings.Contains(body, "Older") || strings.Contains(body, "waves-heading") {
		t.Errorf("the second page doesn't show only the last invite, without waves:\n%s", body)
	}
	for _, path := range []string{"/invites?before=x", "/invites?before=" + inviteID(999)} {
		if rr := b.get(t, path); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rr.Code)
		}
	}
}

func TestInviteRevoke(t *testing.T) {
	inv := testInvites()
	b := waitlistServer(t, testWaitlist(), inv, &fakeEmails{})

	rr := b.post(t, "/invites/"+inviteID(5)+"/revoke", nil)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/invites" {
		t.Fatalf("POST revoke = %d to %q, want 303 to /invites", rr.Code, rr.Header().Get("Location"))
	}
	if len(inv.revoked) != 1 || inv.revoked[0] != inviteID(5) || inv.actors[0].AdminEmail != testEmail {
		t.Errorf("revoked %v by %+v", inv.revoked, inv.actors)
	}
	if body := b.get(t, "/invites").Body.String(); !strings.Contains(body, "Revoked the invite to Sam from the gym.") {
		t.Error("the invites page lacks the flash")
	}

	inv.revokeErr = rss.ErrInviteUnusable
	b.post(t, "/invites/"+inviteID(1)+"/revoke", nil)
	if body := b.get(t, "/invites").Body.String(); !strings.Contains(body, "That invite has been used, so it can&#39;t be revoked.") {
		t.Error("revoking a used invite doesn't say so")
	}

	for _, id := range []string{"x", inviteID(999)} {
		if rr := b.post(t, "/invites/"+id+"/revoke", nil); rr.Code != http.StatusNotFound {
			t.Errorf("POST revoke %s = %d, want 404", id, rr.Code)
		}
	}
}

func TestInviteResend(t *testing.T) {
	t.Run("a new link, emailed", func(t *testing.T) {
		inv := testInvites()
		b := waitlistServer(t, testWaitlist(), inv, &fakeEmails{})
		rr := b.post(t, "/invites/"+inviteID(1)+"/resend", nil)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("POST resend = %d, want 303", rr.Code)
		}
		if len(inv.resent) != 1 || inv.resent[0] != inviteID(1) || inv.available != 90 {
			t.Fatalf("resent %v, checked against %d; want invite 1 against 90", inv.resent, inv.available)
		}
		sent := inv.sent[0]
		m := signupToken.FindStringSubmatch(sent.Text)
		if m == nil || string(token.Hash(m[1])) != string(sent.InviteTokenHash) {
			t.Errorf("the email lacks its new link:\n%s", sent.Text)
		}
		m = leaveToken.FindStringSubmatch(sent.Text)
		if m == nil || string(token.Hash(m[1])) != string(sent.WaitlistTokenHash) || !strings.Contains(sent.Text, "Hi Person 1,") {
			t.Errorf("the email lacks the entry's greeting or leave link:\n%s", sent.Text)
		}
		if body := b.get(t, "/invites").Body.String(); !strings.Contains(body, "Sent a new invite to person1@example.com.") {
			t.Error("the invites page lacks the flash")
		}
	})

	t.Run("no waitlist entry: no leave link", func(t *testing.T) {
		inv := testInvites()
		b := waitlistServer(t, testWaitlist(), inv, &fakeEmails{})
		b.post(t, "/invites/"+inviteID(2)+"/resend", nil)
		sent := inv.sent[0]
		if sent.WaitlistTokenHash != nil || strings.Contains(sent.Text, "Leave the waitlist") || !strings.Contains(sent.Text, "Hi,") {
			t.Errorf("an invite with no entry's email = %q", sent.Text)
		}
	})

	for _, tt := range []struct {
		err  error
		want string
	}{
		{rss.ErrOverLimit, "No more emails fit today without risking account emails."},
		{rss.ErrInviteUnusable, "That invite can&#39;t be resent: it&#39;s open, used or revoked."},
	} {
		t.Run(tt.want, func(t *testing.T) {
			inv := testInvites()
			inv.resendErr = tt.err
			b := waitlistServer(t, testWaitlist(), inv, &fakeEmails{})
			b.post(t, "/invites/"+inviteID(1)+"/resend", nil)
			if body := b.get(t, "/invites").Body.String(); !strings.Contains(body, tt.want) {
				t.Errorf("the invites page lacks %q", tt.want)
			}
		})
	}
}

func TestNewInvite(t *testing.T) {
	if body := waitlistServer(t, testWaitlist(), &fakeInvites{}, &fakeEmails{}).get(t, "/invites/new").Body.String(); !strings.Contains(body, `name="days" value="14"`) {
		t.Errorf("the form lacks its default expiry:\n%s", body)
	}

	t.Run("open", func(t *testing.T) {
		inv := &fakeInvites{}
		b := waitlistServer(t, testWaitlist(), inv, &fakeEmails{})
		rr := b.post(t, "/invites/new", url.Values{"note": {" Sam "}, "days": {"3"}})
		body := rr.Body.String()
		if rr.Code != http.StatusOK || !strings.Contains(body, "Copy this link and send it yourself:") || !strings.Contains(body, "until 12 November 2026") {
			t.Fatalf("POST /invites/new = %d:\n%s", rr.Code, body)
		}
		if len(inv.created) != 1 || inv.created[0].Note != "Sam" || inv.created[0].Email != "" || !inv.created[0].ExpiresAt.Equal(testNow.Add(72*time.Hour)) {
			t.Errorf("created %+v", inv.created)
		}
		if signupToken.FindStringSubmatch(body) == nil {
			t.Error("the page lacks the link")
		}
	})

	t.Run("bound and emailed", func(t *testing.T) {
		inv := &fakeInvites{}
		b := waitlistServer(t, testWaitlist(), inv, &fakeEmails{})
		rr := b.post(t, "/invites/new", url.Values{"email": {"PERSON1@example.com"}, "note": {"asked"}, "days": {"14"}})
		body := rr.Body.String()
		if rr.Code != http.StatusOK || !strings.Contains(body, "Invite emailed to person1@example.com.") {
			t.Fatalf("POST /invites/new = %d:\n%s", rr.Code, body)
		}
		if len(inv.sent) != 1 || inv.sent[0].WaitlistID != entryID(1) || inv.sent[0].Email != "person1@example.com" || inv.created[0].Note != "asked" {
			t.Fatalf("sent %+v, created %+v", inv.sent, inv.created)
		}
		m := signupToken.FindStringSubmatch(body)
		if m == nil || string(token.Hash(m[1])) != string(inv.sent[0].InviteTokenHash) || !strings.Contains(inv.sent[0].Text, m[0]) {
			t.Error("the page's link isn't the emailed invite's")
		}
	})

	tests := []struct {
		name string
		form url.Values
		err  error
		want string
	}{
		{"not an email", url.Values{"email": {"nope"}, "days": {"14"}}, nil, "Enter an email address, or leave it empty."},
		{"not on the waitlist", url.Values{"email": {"someone@example.com"}, "days": {"14"}}, nil, "No one with this email is confirmed on the waitlist."},
		{"unconfirmed", url.Values{"email": {"person4@example.com"}, "days": {"14"}}, nil, "No one with this email is confirmed on the waitlist."},
		{"already invited", url.Values{"email": {"person3@example.com"}, "days": {"14"}}, nil, "person3@example.com already has an invite waiting to be used"},
		{"invited meanwhile", url.Values{"email": {"person1@example.com"}, "days": {"14"}}, rss.ErrNoRecord, "person1@example.com already has an invite waiting to be used"},
		{"over the limits", url.Values{"email": {"person1@example.com"}, "days": {"14"}}, rss.ErrOverLimit, "No more emails fit today without risking account emails."},
		{"long note", url.Values{"note": {strings.Repeat("n", 201)}, "days": {"14"}}, nil, "Keep it to 200 characters."},
		{"no days", url.Values{"days": {"0"}}, nil, "Choose between 1 and 90 days."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := &fakeInvites{sendErr: tt.err}
			b := waitlistServer(t, testWaitlist(), inv, &fakeEmails{})
			rr := b.post(t, "/invites/new", tt.form)
			if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), tt.want) {
				t.Errorf("POST /invites/new = %d, want 422 saying %q:\n%s", rr.Code, tt.want, rr.Body)
			}
			if len(inv.created) != 0 {
				t.Errorf("created %+v", inv.created)
			}
		})
	}
}

func TestRevokedMessage(t *testing.T) {
	for _, tt := range []struct {
		inv  rss.Invite
		want string
	}{
		{rss.Invite{Email: "a@example.com", Note: "n"}, "Revoked the invite to a@example.com."},
		{rss.Invite{Note: "Sam"}, "Revoked the invite to Sam."},
		{rss.Invite{}, "Revoked the open invite."},
	} {
		if got := revokedMessage(tt.inv); got != tt.want {
			t.Errorf("revokedMessage(%+v) = %q, want %q", tt.inv, got, tt.want)
		}
	}
}
