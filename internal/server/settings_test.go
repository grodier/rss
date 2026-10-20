package server

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

var settingsSession = rss.Session{
	ID:   "123e4567-e89b-12d3-a456-426614174099",
	User: rss.User{ID: "123e4567-e89b-12d3-a456-426614174001", Name: "Ada Lovelace", Email: "ada@example.com"},
}

func TestSettingsHandler(t *testing.T) {
	t.Run("shows the account", func(t *testing.T) {
		s := newTestServer(t)
		rr := httptest.NewRecorder()
		s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsHandler)).ServeHTTP(rr, requestWithSession(settingsSession))

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		assertBodyContains(t, rr.Body.String(),
			"Ada Lovelace",
			"ada@example.com",
			`href="/settings/name"`,
			`<a class="settings-row" href="/settings/subscriptions.opml" download`,
			"An OPML file you can import into another reader.",
			`<form class="settings-logout" action="/logout" method="post">`,
			`<a class="rail-account" href="/settings" aria-current="page">`,
		)
	})

	t.Run("flash is shown once", func(t *testing.T) {
		s := newTestServer(t)
		rr := httptest.NewRecorder()
		var flashLeft bool
		s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.sessionManager.Put(r.Context(), "flash", "Your name was updated.")
			s.settingsHandler(w, r)
			flashLeft = s.sessionManager.Exists(r.Context(), "flash")
		})).ServeHTTP(rr, requestWithSession(settingsSession))

		assertBodyContains(t, rr.Body.String(), `<div class="flash">Your name was updated.</div>`)
		if flashLeft {
			t.Error("flash is still in the session after the page showed it")
		}
	})
}

func TestSettingsRoutesRequireAuthentication(t *testing.T) {
	tests := []struct {
		method, target string
	}{
		{http.MethodGet, "/settings"},
		{http.MethodGet, "/settings/name"},
		{http.MethodPost, "/settings/name"},
		{http.MethodGet, "/settings/subscriptions.opml"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			s := newTestServer(t)
			req := httptest.NewRequest(tt.method, tt.target, nil)
			rr := httptest.NewRecorder()

			s.router().ServeHTTP(rr, req)

			assertRedirect(t, rr, "/login")
		})
	}
}

func TestSettingsNameHandler(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.settingsNameHandler)).ServeHTTP(rr, requestWithSession(settingsSession))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	assertBodyContains(t, body, `value="Ada Lovelace"`, `<a class="btn" href="/settings">Cancel</a>`)
	if strings.Contains(body, "autofocus") {
		t.Error("the form has autofocus without an error")
	}
}

// postSettingsName posts name to POST /settings/name as settingsSession. It
// returns the response and the flash message left in the session.
func postSettingsName(t *testing.T, s *Server, name string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	form := url.Values{"name": {name}}
	req := httptest.NewRequest(http.MethodPost, "/settings/name", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(requestWithSession(settingsSession).Context())
	rr := httptest.NewRecorder()
	var flash string
	s.sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.settingsNameFormHandler(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	})).ServeHTTP(rr, req)
	return rr, flash
}

func TestSettingsNameForm(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantStatus int
		wantError  string
	}{
		{"blank", "   ", http.StatusUnprocessableEntity, "Name is required"},
		{"101 characters", strings.Repeat("a", 101), http.StatusUnprocessableEntity, "Name must not be more than 100 characters long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			s := newTestServerWith(t, Services{UserService: &fakeUserStore{
				updateNameFn: func(context.Context, string, string) error {
					calls++
					return nil
				},
			}})

			rr, _ := postSettingsName(t, s, tt.in)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			assertBodyContains(t, rr.Body.String(),
				`<p class="error" id="name-error">`+tt.wantError+`</p>`,
				`aria-invalid="true"`,
				"autofocus",
			)
			if calls != 0 {
				t.Errorf("UpdateName called %d times, want 0", calls)
			}
		})
	}

	t.Run("valid name is trimmed and saved", func(t *testing.T) {
		var gotID, gotName string
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			updateNameFn: func(ctx context.Context, id, name string) error {
				gotID, gotName = id, name
				return nil
			},
		}})

		rr, flash := postSettingsName(t, s, "  Ann  ")

		assertRedirect(t, rr, "/settings")
		if gotID != settingsSession.User.ID || gotName != "Ann" {
			t.Errorf("UpdateName(%q, %q), want (%q, %q)", gotID, gotName, settingsSession.User.ID, "Ann")
		}
		if want := "Your name was updated."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
	})

	t.Run("store error", func(t *testing.T) {
		s := newTestServerWith(t, Services{UserService: &fakeUserStore{
			updateNameFn: func(context.Context, string, string) error {
				return errors.New("boom")
			},
		}})

		rr, flash := postSettingsName(t, s, "Ann")

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
		if flash != "" {
			t.Errorf("flash = %q, want none", flash)
		}
	})
}

// getOPML requests the OPML export as settingsSession from a server whose
// subscriptions come from listByUser.
func getOPML(t *testing.T, listByUser func(context.Context, string) ([]rss.Subscription, error)) *httptest.ResponseRecorder {
	t.Helper()
	s := newTestServerWith(t, Services{SubscriptionService: &fakeSubscriptionStore{listByUserFn: listByUser}})
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(http.HandlerFunc(s.exportOPMLHandler)).ServeHTTP(rr, requestWithSession(settingsSession))
	return rr
}

// parseOPML checks the export's status and headers and decodes its body.
func parseOPML(t *testing.T, rr *httptest.ResponseRecorder) opmlDoc {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got, want := rr.Header().Get("Content-Type"), "text/x-opml; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	// The date is UTC; allow for the request straddling midnight.
	var dates []string
	for _, d := range []time.Time{time.Now().UTC(), time.Now().UTC().Add(-time.Minute)} {
		dates = append(dates, `attachment; filename="rss-subscriptions-`+d.Format(time.DateOnly)+`.opml"`)
	}
	if got := rr.Header().Get("Content-Disposition"); got != dates[0] && got != dates[1] {
		t.Errorf("Content-Disposition = %q, want %q", got, dates[0])
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, xml.Header) {
		t.Errorf("body doesn't start with the XML header: %q", body)
	}

	var doc opmlDoc
	if err := xml.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("parsing the body: %v\n%s", err, body)
	}
	if doc.Version != "2.0" {
		t.Errorf("version = %q, want %q", doc.Version, "2.0")
	}
	if doc.Head.Title != "rss subscriptions" {
		t.Errorf("head title = %q, want %q", doc.Head.Title, "rss subscriptions")
	}
	if _, err := time.Parse(time.RFC1123Z, doc.Head.DateCreated); err != nil {
		t.Errorf("dateCreated %q isn't RFC 1123: %v", doc.Head.DateCreated, err)
	}
	return doc
}

func TestExportOPML(t *testing.T) {
	t.Run("subscriptions", func(t *testing.T) {
		const title = `Tom & Jerry's <"Best"> Feed`
		subs := []rss.Subscription{
			{
				Feed: rss.Feed{ID: "feed-1", Url: "https://a.example/feed.xml"},
				Site: rss.Site{URL: "https://a.example/"},
			},
			{
				Feed: rss.Feed{ID: "feed-2", Url: "https://b.example/rss?x=1&y=2", SiteUrl: "https://b.example/blog", Title: title, GoneAt: time.Now()},
				Site: rss.Site{URL: "https://b.example/"},
			},
		}
		var gotUserID string
		rr := getOPML(t, func(ctx context.Context, userID string) ([]rss.Subscription, error) {
			gotUserID = userID
			return subs, nil
		})

		doc := parseOPML(t, rr)
		if gotUserID != settingsSession.User.ID {
			t.Errorf("ListByUser(%q), want %q", gotUserID, settingsSession.User.ID)
		}
		want := []opmlOutline{
			{Type: "rss", Text: "https://a.example/feed.xml", XMLURL: "https://a.example/feed.xml", HTMLURL: "https://a.example/"},
			{Type: "rss", Text: title, Title: title, XMLURL: "https://b.example/rss?x=1&y=2", HTMLURL: "https://b.example/blog"},
		}
		if len(doc.Body) != len(want) {
			t.Fatalf("got %d outlines, want %d: %+v", len(doc.Body), len(want), doc.Body)
		}
		for i := range want {
			if doc.Body[i] != want[i] {
				t.Errorf("outline %d = %+v, want %+v", i, doc.Body[i], want[i])
			}
		}
	})

	t.Run("no subscriptions", func(t *testing.T) {
		rr := getOPML(t, func(context.Context, string) ([]rss.Subscription, error) {
			return nil, nil
		})

		doc := parseOPML(t, rr)
		if len(doc.Body) != 0 {
			t.Errorf("got %d outlines, want none", len(doc.Body))
		}
		assertBodyContains(t, rr.Body.String(), "<body></body>")
	})

	t.Run("store error", func(t *testing.T) {
		rr := getOPML(t, func(context.Context, string) ([]rss.Subscription, error) {
			return nil, errors.New("boom")
		})

		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
		if got := rr.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("Content-Type = %q, want HTML", got)
		}
		if got := rr.Header().Get("Content-Disposition"); got != "" {
			t.Errorf("Content-Disposition = %q, want none", got)
		}
	})
}
