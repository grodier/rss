package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

// waitlistLinkRX finds the token in a waitlist email's confirm or leave link.
var waitlistLinkRX = regexp.MustCompile(`https://rss\.example\.com/waitlist/(confirm|leave)\?token=([A-Za-z0-9_-]{43})`)

const waitlistMember = "member@example.com"

// waitlistSections are the sections newWaitlistTestServer's form offers.
var waitlistSections = []rss.Section{
	{ID: "sec-news", Slug: "news", Name: "News"},
	{ID: "sec-food", Slug: "food", Name: "Food"},
	{ID: "sec-film", Slug: "film-and-tv", Name: "Film and TV"},
}

// newWaitlistTestServer returns a server whose GetByEmail knows only
// waitlistMember, with its waitlist store and email queue.
func newWaitlistTestServer(t *testing.T) (*Server, *fakeWaitlistStore, *fakeEmailQueue) {
	t.Helper()
	store, queue := &fakeWaitlistStore{}, &fakeEmailQueue{}
	s := newTestServerConfig(t, Config{Port: 4000, Env: "development", BaseURL: "https://rss.example.com"}, Services{
		UserService: &fakeUserStore{getByEmailFn: func(ctx context.Context, email string) (rss.User, error) {
			if strings.EqualFold(email, waitlistMember) {
				return rss.User{ID: "user-1", Email: waitlistMember}, nil
			}
			return rss.User{}, rss.ErrNoRecord
		}},
		WaitlistService: store,
		DiscoverService: &fakeDiscoverStore{listSectionsFn: func(context.Context) ([]rss.Section, error) {
			return waitlistSections, nil
		}},
		EmailQueue: queue,
	})
	return s, store, queue
}

// waitlistValues returns a valid join form for email, with the given
// fields replaced.
func waitlistValues(email string, replace ...string) url.Values {
	v := url.Values{
		"email": {email},
		"name":  {"Ada Lovelace"},
		"sites": {"Daring Fireball, kottke.org"},
	}
	for i := 0; i+1 < len(replace); i += 2 {
		v.Set(replace[i], replace[i+1])
	}
	return v
}

// postWaitlist posts form to /waitlist through the router from ip.
func postWaitlist(t *testing.T, s *Server, ip string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return postForm(t, s.router().ServeHTTP, "/waitlist", ip, form)
}

func TestWaitlistJoin(t *testing.T) {
	t.Run("valid: stored, emailed, and the joined page", func(t *testing.T) {
		s, store, queue := newWaitlistTestServer(t)
		form := waitlistValues("ada@example.com", "name", "  Ada Lovelace  ", "source", "spring-launch")
		rr := postWaitlist(t, s, "192.0.2.1:1234", form)
		assertRedirect(t, rr, "/waitlist/joined")

		if len(store.entries) != 1 {
			t.Fatalf("stored %d entries, want 1", len(store.entries))
		}
		var hash string
		var e rss.WaitlistEntry
		for hash, e = range store.entries {
		}
		want := rss.WaitlistEntry{Email: "ada@example.com", Name: "Ada Lovelace", Sites: "Daring Fireball, kottke.org", Source: "spring-launch"}
		if e.Email != want.Email || e.Name != want.Name || e.Sites != want.Sites || e.Source != want.Source {
			t.Errorf("entry = %+v, want %+v", e, want)
		}

		msgs := queue.messages()
		if len(msgs) != 1 {
			t.Fatalf("queued %d emails, want 1", len(msgs))
		}
		m := msgs[0]
		if m.To != "ada@example.com" {
			t.Errorf("To = %q", m.To)
		}
		if want := "Confirm your place on the Paper Round waitlist"; m.Subject != want {
			t.Errorf("Subject = %q, want %q", m.Subject, want)
		}
		links := waitlistLinkRX.FindAllStringSubmatch(m.Text, -1)
		if len(links) != 2 || links[0][1] != "confirm" || links[1][1] != "leave" {
			t.Fatalf("Text's links = %q, want a confirm link then a leave link: %s", links, m.Text)
		}
		for _, l := range links {
			if !bytes.Equal(token.Hash(l[2]), []byte(hash)) {
				t.Errorf("the %s link's token doesn't hash to the stored hash", l[1])
			}
		}
		assertBodyContains(t, m.Text, "Didn't ask for this? Leave the waitlist: https://rss.example.com/waitlist/leave?token=")
		assertBodyContains(t, m.HTML, `href="https://rss.example.com/waitlist/confirm?token=`, `href="https://rss.example.com/waitlist/leave?token=`)

		req := httptest.NewRequest(http.MethodGet, "/waitlist/joined", nil)
		for _, c := range rr.Result().Cookies() {
			req.AddCookie(c)
		}
		joined := httptest.NewRecorder()
		s.router().ServeHTTP(joined, req)
		if joined.Code != http.StatusOK {
			t.Fatalf("GET /waitlist/joined: status = %d", joined.Code)
		}
		assertBodyContains(t, joined.Body.String(), "<h1>Check your email</h1>",
			"We sent a link to ada@example.com. Open it to confirm your place on the waitlist.")
	})

	t.Run("the source is kept only when valid, the sites trimmed", func(t *testing.T) {
		tests := []struct{ source, sites, wantSource, wantSites string }{
			{"newsletter-2", "  kottke.org  ", "newsletter-2", "kottke.org"},
			{"Bad Ref!", "", "", ""},
			{strings.Repeat("a", 65), "   ", "", ""},
		}
		for _, tt := range tests {
			s, store, _ := newWaitlistTestServer(t)
			assertRedirect(t, postWaitlist(t, s, "192.0.2.1:1234", waitlistValues("ada@example.com", "source", tt.source, "sites", tt.sites)), "/waitlist/joined")
			for _, e := range store.entries {
				if e.Source != tt.wantSource || e.Sites != tt.wantSites {
					t.Errorf("source %q, sites %q: stored %q, %q; want %q, %q", tt.source, tt.sites, e.Source, e.Sites, tt.wantSource, tt.wantSites)
				}
			}
		}
	})

	t.Run("sections: known ones kept once each, in the sections' order", func(t *testing.T) {
		tests := []struct {
			name   string
			posted []string
			want   []string
		}{
			{"none", nil, nil},
			{"in the sections' order", []string{"sec-film", "sec-news"}, []string{"sec-news", "sec-film"}},
			{"unknown dropped", []string{"sec-food", "sec-gone", "News"}, []string{"sec-food"}},
			{"duplicates once", []string{"sec-food", "sec-food"}, []string{"sec-food"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, store, _ := newWaitlistTestServer(t)
				form := waitlistValues("ada@example.com")
				form["sections"] = tt.posted
				assertRedirect(t, postWaitlist(t, s, "192.0.2.1:1234", form), "/waitlist/joined")
				for _, e := range store.entries {
					if !slices.Equal(e.Sections, tt.want) {
						t.Errorf("stored sections %q, want %q", e.Sections, tt.want)
					}
				}
			})
		}
	})

	t.Run("joining again replaces the sections", func(t *testing.T) {
		s, store, _ := newWaitlistTestServer(t)
		form := waitlistValues("ada@example.com")
		form["sections"] = []string{"sec-news", "sec-food"}
		assertRedirect(t, postWaitlist(t, s, "192.0.2.1:1234", form), "/waitlist/joined")
		form["sections"] = []string{"sec-film"}
		assertRedirect(t, postWaitlist(t, s, "192.0.2.1:1234", form), "/waitlist/joined")
		if len(store.entries) != 1 {
			t.Fatalf("stored %d entries, want 1", len(store.entries))
		}
		for _, e := range store.entries {
			if !slices.Equal(e.Sections, []string{"sec-film"}) {
				t.Errorf("stored sections %q, want the second answer's", e.Sections)
			}
		}
	})

	t.Run("the home page asks what they like to read", func(t *testing.T) {
		s, _, _ := newWaitlistTestServer(t)
		body := getHome(t, s, "/", false).Body.String()
		assertBodyContains(t, body,
			`<fieldset class="field section-choices" aria-describedby="sections-hint">`,
			`<ul class="check-pills" role="list">`,
			`<legend>What do you like to read? (optional)</legend>`,
			`<input type="checkbox" name="sections" value="sec-news" />`, `</svg>News`,
			`<input type="checkbox" name="sections" value="sec-film" />`, `</svg>Film and TV`,
			`<p class="hint" id="sections-hint">We'll show these sections first in Discover.</p>`,
		)
		if strings.Contains(body, "checked") {
			t.Error("a section is checked on the first visit")
		}
		if strings.Index(body, `id="sites"`) > strings.Index(body, `name="sections"`) {
			t.Error("the sections aren't under the sites question")
		}
	})

	t.Run("no sections, no question", func(t *testing.T) {
		s, _, _ := newWaitlistTestServer(t)
		s.services.DiscoverService = emptyCatalog()
		if body := getHome(t, s, "/", false).Body.String(); strings.Contains(body, "section-choices") {
			t.Error("the question is shown without sections")
		}
	})

	t.Run("the sections' store failing is a 500", func(t *testing.T) {
		s, store, _ := newWaitlistTestServer(t)
		s.services.DiscoverService = &fakeDiscoverStore{listSectionsFn: func(context.Context) ([]rss.Section, error) {
			return nil, errors.New("db down")
		}}
		if rr := getHome(t, s, "/", false); rr.Code != http.StatusInternalServerError {
			t.Errorf("GET /: status = %d, want 500", rr.Code)
		}
		if rr := postWaitlist(t, s, "192.0.2.1:1234", waitlistValues("ada@example.com")); rr.Code != http.StatusInternalServerError {
			t.Errorf("POST /waitlist: status = %d, want 500", rr.Code)
		}
		if len(store.entries) != 0 {
			t.Errorf("stored %d entries, want none", len(store.entries))
		}
	})

	t.Run("the home page carries a valid ?ref=", func(t *testing.T) {
		s, _, _ := newWaitlistTestServer(t)
		body := getHome(t, s, "/?ref=spring-launch", false).Body.String()
		assertBodyContains(t, body,
			`<form action="/waitlist" method="post" data-busy>`,
			`<input type="hidden" name="source" value="spring-launch" />`,
			"<h2 id=\"waitlist-heading\">Join the waitlist</h2>",
			`<p class="alt">Already have an account? <a href="/login">Log in</a></p>`,
			"<p>The sites you follow, in timelines you shape.</p>",
			`<p class="waitlist-intro">Leave your email and we'll send you an invite when there's a place for you.</p>`,
			`<label for="sites">A few sites you'd like to follow (optional)</label>`,
		)
		if body := getHome(t, s, "/?ref=Bad%20Ref", false).Body.String(); strings.Contains(body, `name="source"`) {
			t.Errorf("an invalid ref is carried: %s", body)
		}
	})

	t.Run("the joined page without a join goes home", func(t *testing.T) {
		s, _, _ := newWaitlistTestServer(t)
		rr := httptest.NewRecorder()
		s.router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/waitlist/joined", nil))
		assertRedirect(t, rr, "/")
	})

	t.Run("logged in: home, nothing stored", func(t *testing.T) {
		s, store, queue := newWaitlistTestServer(t)
		req := postFormRequest("/waitlist", waitlistValues("ada@example.com"))
		req = req.WithContext(requestWithSession(confirmedSession).Context())
		rr := httptest.NewRecorder()
		s.sessionManager.LoadAndSave(http.HandlerFunc(s.waitlistJoinHandler)).ServeHTTP(rr, req)
		assertRedirect(t, rr, "/")
		if len(store.entries) != 0 || len(queue.messages()) != 0 {
			t.Errorf("stored %d entries and queued %d emails, want none", len(store.entries), len(queue.messages()))
		}
	})
}

func TestWaitlistJoinInvalid(t *testing.T) {
	tests := []struct {
		name, field, value, wantError string
	}{
		{"no email", "email", "", "Email is required"},
		{"bad email", "email", "not-an-email", "Email must be a valid email address"},
		{"newsletter address", "email", "sam.abc234@in.localhost", "Use your own email address; this one is for newsletters."},
		{"no name", "name", "   ", "Name is required"},
		{"long name", "name", strings.Repeat("a", 101), "Name must not be more than 100 characters long"},
		{"long sites", "sites", strings.Repeat("é", 501), "Keep this under 500 characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, queue := newWaitlistTestServer(t)
			s.config.NewsletterDomain = "in.localhost"
			rr := postWaitlist(t, s, "192.0.2.1:1234", waitlistValues("ada@example.com", tt.field, tt.value))
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
			}
			assertBodyContains(t, rr.Body.String(),
				fmt.Sprintf(`<p class="error" id="%s-error">%s</p>`, tt.field, tt.wantError),
				`aria-invalid="true"`,
				"autofocus",
			)
			// What was typed is kept.
			if tt.field != "sites" {
				assertBodyContains(t, rr.Body.String(), `Daring Fireball, kottke.org</textarea>`)
			}
			if len(store.entries) != 0 || len(queue.messages()) != 0 {
				t.Errorf("stored %d entries and queued %d emails, want none", len(store.entries), len(queue.messages()))
			}
		})
	}

	t.Run("the chosen sections stay checked", func(t *testing.T) {
		s, _, _ := newWaitlistTestServer(t)
		form := waitlistValues("ada@example.com", "email", "")
		form["sections"] = []string{"sec-food", "sec-gone"}
		rr := postWaitlist(t, s, "192.0.2.1:1234", form)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
		body := rr.Body.String()
		assertBodyContains(t, body,
			`<input type="checkbox" name="sections" value="sec-food" checked />`, `</svg>Food`,
			`<input type="checkbox" name="sections" value="sec-news" />`, `</svg>News`,
		)
		if strings.Contains(body, "sec-gone") {
			t.Error("an unknown section is echoed back")
		}
	})

	t.Run("500 characters is fine", func(t *testing.T) {
		s, _, _ := newWaitlistTestServer(t)
		assertRedirect(t, postWaitlist(t, s, "192.0.2.1:1234", waitlistValues("ada@example.com", "sites", strings.Repeat("é", 500))), "/waitlist/joined")
	})
}

func TestWaitlistJoinDuplicate(t *testing.T) {
	t.Run("a confirmed address", func(t *testing.T) {
		s, store, queue := newWaitlistTestServer(t)
		store.entries = map[string]rss.WaitlistEntry{"hash": {ID: "entry-1", Email: "ada@example.com", Name: "Ada", ConfirmedAt: time.Now()}}
		rr := postWaitlist(t, s, "192.0.2.1:1234", waitlistValues("Ada@Example.com"))
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
		assertBodyContains(t, rr.Body.String(), `<p class="error" id="email-error">You&#39;re already on the waitlist.</p>`)
		if len(queue.messages()) != 0 {
			t.Errorf("queued %d emails, want none", len(queue.messages()))
		}
	})

	t.Run("an unconfirmed address: a new email, and only its link works", func(t *testing.T) {
		s, store, queue := newWaitlistTestServer(t)
		assertRedirect(t, postWaitlist(t, s, "192.0.2.1:1234", waitlistValues("ada@example.com")), "/waitlist/joined")
		assertRedirect(t, postWaitlist(t, s, "192.0.2.1:1234", waitlistValues("ada@example.com", "name", "Ada King")), "/waitlist/joined")

		msgs := queue.messages()
		if len(msgs) != 2 {
			t.Fatalf("queued %d emails, want 2", len(msgs))
		}
		if len(store.entries) != 1 {
			t.Fatalf("stored %d entries, want 1", len(store.entries))
		}
		first := waitlistLinkRX.FindStringSubmatch(msgs[0].Text)[2]
		second := waitlistLinkRX.FindStringSubmatch(msgs[1].Text)[2]
		if _, ok := store.entries[string(token.Hash(first))]; ok {
			t.Error("the first email's token still works")
		}
		if e, ok := store.entries[string(token.Hash(second))]; !ok || e.Name != "Ada King" {
			t.Errorf("entry by the second token = %+v, %v; want Ada King", e, ok)
		}
	})

	t.Run("an account's address", func(t *testing.T) {
		s, store, queue := newWaitlistTestServer(t)
		rr := postWaitlist(t, s, "192.0.2.1:1234", waitlistValues("Member@Example.com"))
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
		}
		assertBodyContains(t, rr.Body.String(),
			`<p class="error" id="email-error">There&#39;s already an account with this email.</p>`,
			`<p class="hint"><a href="/login">Log in</a></p>`,
		)
		if len(store.entries) != 0 || len(queue.messages()) != 0 {
			t.Errorf("stored %d entries and queued %d emails, want none", len(store.entries), len(queue.messages()))
		}
	})
}

func TestWaitlistHoneypot(t *testing.T) {
	s, store, queue := newWaitlistTestServer(t)
	assertRedirect(t, postWaitlist(t, s, "192.0.2.1:1234", waitlistValues("ada@example.com", "website", "https://spam.example")), "/waitlist/joined")
	if len(store.entries) != 0 || len(queue.messages()) != 0 {
		t.Errorf("stored %d entries and queued %d emails, want none", len(store.entries), len(queue.messages()))
	}
}

func TestWaitlistRateLimit(t *testing.T) {
	s, store, _ := newWaitlistTestServer(t)
	for i := 1; i <= 10; i++ {
		rr := postWaitlist(t, s, "192.0.2.1:1234", waitlistValues(fmt.Sprintf("user%d@example.com", i)))
		assertRedirect(t, rr, "/waitlist/joined")
	}
	rr := postWaitlist(t, s, "192.0.2.1:1234", waitlistValues("user@example.com"))
	assertRateLimitedForm(t, rr, "user@example.com")
	if len(store.entries) != 10 {
		t.Errorf("stored %d entries, want 10", len(store.entries))
	}

	// Another address isn't limited.
	assertRedirect(t, postWaitlist(t, s, "198.51.100.1:1234", waitlistValues("user@example.com")), "/waitlist/joined")
}

// newWaitlistEntry stores an entry in store with a new link token and
// returns the token.
func newWaitlistEntry(t *testing.T, store *fakeWaitlistStore, confirmed bool) string {
	t.Helper()
	plain, hash, err := token.New()
	if err != nil {
		t.Fatal(err)
	}
	e := rss.WaitlistEntry{ID: "entry-1", Email: "ada@example.com", Name: "Ada"}
	if confirmed {
		e.ConfirmedAt = time.Now()
	}
	store.entries = map[string]rss.WaitlistEntry{string(hash): e}
	return plain
}

// testWaitlistLink checks a waitlist email link: GET shows its page with
// the button and doesn't write; POST acts (check says it did) and goes home
// with flash; an unknown token is the expired-link page.
func testWaitlistLink(t *testing.T, path string, get, post http.HandlerFunc, store *fakeWaitlistStore, s *Server, wantPage []string, flash string, check func(t *testing.T, plain string)) {
	t.Run("GET shows the button and doesn't write", func(t *testing.T) {
		plain := newWaitlistEntry(t, store, false)
		rr, _ := serveConfirm(t, s, get, httptest.NewRequest(http.MethodGet, path+"?token="+plain, nil), nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		assertBodyContains(t, rr.Body.String(), append(wantPage,
			`<form action="`+path+`" method="post">`,
			`<input type="hidden" name="token" value="`+plain+`" />`,
		)...)
		e, ok := store.entries[string(token.Hash(plain))]
		if !ok || !e.ConfirmedAt.IsZero() {
			t.Errorf("after GET, entry = %+v, %v; want it there, unconfirmed", e, ok)
		}
	})

	t.Run("POST acts and goes home with a flash", func(t *testing.T) {
		plain := newWaitlistEntry(t, store, false)
		rr, gotFlash := serveConfirm(t, s, post, postFormRequest(path, url.Values{"token": {plain}}), nil)
		assertRedirect(t, rr, "/")
		if gotFlash != flash {
			t.Errorf("flash = %q, want %q", gotFlash, flash)
		}
		check(t, plain)
	})

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method+" with an unknown token: expired", func(t *testing.T) {
			newWaitlistEntry(t, store, false)
			req := httptest.NewRequest(http.MethodGet, path+"?token=nope", nil)
			h := get
			if method == http.MethodPost {
				req, h = postFormRequest(path, url.Values{"token": {"nope"}}), post
			}
			rr, _ := serveConfirm(t, s, h, req, nil)
			if rr.Code != http.StatusGone {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusGone)
			}
			assertBodyContains(t, rr.Body.String(), "<h1>This link has expired</h1>", `<a class="btn" href="/">Join the waitlist</a>`)
		})
	}
}

func TestWaitlistConfirm(t *testing.T) {
	s, store, _ := newWaitlistTestServer(t)
	testWaitlistLink(t, "/waitlist/confirm", s.waitlistConfirmHandler, s.waitlistConfirmFormHandler, store, s,
		[]string{"<h1>Confirm your place</h1>", "Confirm ada@example.com on the Paper Round waitlist.", ">Confirm</button>"},
		"You're on the waitlist. We'll email you when there's a place for you.",
		func(t *testing.T, plain string) {
			if e := store.entries[string(token.Hash(plain))]; e.ConfirmedAt.IsZero() {
				t.Error("the entry isn't confirmed")
			}
		})

	t.Run("confirming twice is fine", func(t *testing.T) {
		plain := newWaitlistEntry(t, store, true)
		rr, _ := serveConfirm(t, s, s.waitlistConfirmFormHandler, postFormRequest("/waitlist/confirm", url.Values{"token": {plain}}), nil)
		assertRedirect(t, rr, "/")
	})
}

func TestWaitlistLeave(t *testing.T) {
	s, store, _ := newWaitlistTestServer(t)
	testWaitlistLink(t, "/waitlist/leave", s.waitlistLeaveHandler, s.waitlistLeaveFormHandler, store, s,
		[]string{"<h1>Leave the waitlist?</h1>", "We'll delete your email and name.", ">Leave the waitlist</button>"},
		"You've left the waitlist. We won't email you again.",
		func(t *testing.T, plain string) {
			if _, ok := store.entries[string(token.Hash(plain))]; ok {
				t.Error("the entry wasn't deleted")
			}
		})
}
