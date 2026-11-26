package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/rss"
)

// newsletterSession is a reader with newsletters.
var newsletterSession = rss.Session{
	ID:   "123e4567-e89b-12d3-a456-426614174097",
	User: rss.User{ID: "123e4567-e89b-12d3-a456-426614174003", Name: "Sam Reader", Email: "sam@example.com", Features: rss.NewFeatures(rss.FeatureNewsletters)},
}

func newsletterServer(t *testing.T, store *fakeNewsletterStore) *Server {
	t.Helper()
	return newTestServerConfig(t, Config{Port: 4000, Env: "development", NewsletterDomain: "in.localhost"}, Services{NewsletterService: store})
}

// serveNewsletters serves req through the newsletter routes' middleware as
// sess, and returns the response and the flash left in the session.
func serveNewsletters(t *testing.T, s *Server, h http.HandlerFunc, req *http.Request, sess rss.Session) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var flash string
	rr := httptest.NewRecorder()
	s.sessionManager.LoadAndSave(s.requireFeature(rss.FeatureNewsletters)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r)
		flash = s.sessionManager.GetString(r.Context(), "flash")
	}))).ServeHTTP(rr, withSession(req, sess))
	return rr, flash
}

func postNewsletterAddress(t *testing.T, s *Server, form url.Values) (*httptest.ResponseRecorder, string) {
	t.Helper()
	return serveNewsletters(t, s, s.settingsNewsletterAddressHandler, postFormRequest("/settings/newsletters/address", form), newsletterSession)
}

func addressOf(localPart string) func(context.Context, string) (rss.NewsletterAddress, error) {
	return func(context.Context, string) (rss.NewsletterAddress, error) {
		return rss.NewsletterAddress{ID: "address-1", LocalPart: localPart}, nil
	}
}

func TestNewsletterLocalPart(t *testing.T) {
	zeros := func() *bytes.Reader { return bytes.NewReader(make([]byte, newsletterRandomLen)) }
	tests := []struct{ name, want string }{
		{"George", "george"},
		{"Zoë Ångström", "zoeangstrom"},
		{"Søren Weiß", "sorenweiss"},
		{"Łukasz Đorđević", "lukaszdordevic"},
		{"Æsa Þórsdóttir", "aesathorsdottir"},
		{"李", "reader"},
		{"  ", "reader"},
		{"Ada-Lovelace O'Brien 2nd", "adalovelaceobriennd"},
		{"Bartholomew Montgomery-Smythe", "bartholomewmontgomer"},
	}
	for _, tt := range tests {
		got, err := newsletterLocalPart(tt.name, zeros())
		if err != nil {
			t.Fatal(err)
		}
		if want := tt.want + ".aaaaaa"; got != want {
			t.Errorf("newsletterLocalPart(%q) = %q, want %q", tt.name, got, want)
		}
	}

	// Every byte maps into the alphabet.
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	r := bytes.NewReader(all)
	for range 256 / newsletterRandomLen {
		got, err := newsletterLocalPart("x", r)
		if err != nil {
			t.Fatal(err)
		}
		suffix := strings.TrimPrefix(got, "x.")
		if len(suffix) != newsletterRandomLen || strings.Trim(suffix, newsletterAlphabet) != "" {
			t.Errorf("random part %q isn't %d of the alphabet's characters", suffix, newsletterRandomLen)
		}
	}
	if _, err := newsletterLocalPart("x", bytes.NewReader(nil)); err == nil {
		t.Error("a short random reader isn't an error")
	}
}

func TestNewsletterRoutesNeedTheFeature(t *testing.T) {
	without := newsletterSession
	without.User.Features = 0
	tests := []struct {
		name   string
		domain string
		sess   rss.Session
		want   int
	}{
		{"with the feature", "in.localhost", newsletterSession, 0},
		{"without the feature", "in.localhost", without, http.StatusNotFound},
		{"with no newsletter domain", "", newsletterSession, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeNewsletterStore{}
			s := newTestServerConfig(t, Config{Port: 4000, Env: "development", NewsletterDomain: tt.domain}, Services{NewsletterService: store})
			get, _ := serveNewsletters(t, s, s.settingsNewslettersHandler, httptest.NewRequest(http.MethodGet, "/settings/newsletters", nil), tt.sess)
			post, _ := serveNewsletters(t, s, s.settingsNewsletterAddressHandler, postFormRequest("/settings/newsletters/address", nil), tt.sess)
			if tt.want == 0 {
				if get.Code != http.StatusOK || post.Code != http.StatusSeeOther {
					t.Errorf("GET = %d, POST = %d; want 200 and 303", get.Code, post.Code)
				}
				return
			}
			if get.Code != tt.want || post.Code != tt.want {
				t.Errorf("GET = %d, POST = %d; want %d", get.Code, post.Code, tt.want)
			}
			if len(store.made) != 0 {
				t.Errorf("made %v", store.made)
			}
		})
	}
}

func TestSettingsNewslettersPage(t *testing.T) {
	t.Run("before an address", func(t *testing.T) {
		s := newsletterServer(t, &fakeNewsletterStore{})
		rr, _ := serveNewsletters(t, s, s.settingsNewslettersHandler, httptest.NewRequest(http.MethodGet, "/settings/newsletters", nil), newsletterSession)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		body := rr.Body.String()
		assertBodyContains(t, body, `<h1>Newsletters</h1>`, `action="/settings/newsletters/address"`, "Get your address", "None yet")
		if strings.Contains(body, "@in.localhost") || strings.Contains(body, `name="new"`) {
			t.Error("the page shows an address, or Get a new address, before there is one")
		}
	})
	t.Run("with an address", func(t *testing.T) {
		s := newsletterServer(t, &fakeNewsletterStore{currentFn: addressOf("sam.k7x2m9")})
		rr, _ := serveNewsletters(t, s, s.settingsNewslettersHandler, httptest.NewRequest(http.MethodGet, "/settings/newsletters", nil), newsletterSession)
		assertBodyContains(t, rr.Body.String(),
			`value="sam.k7x2m9@in.localhost" size="23" readonly`,
			`data-copy="newsletter-address" data-copy-what="your address"`,
			`<input type="hidden" name="new" value="true" />`,
			"The old one keeps working for 30 days,",
		)
	})
}

func TestSettingsNewsletterAddress(t *testing.T) {
	t.Run("the first address", func(t *testing.T) {
		store := &fakeNewsletterStore{}
		rr, flash := postNewsletterAddress(t, newsletterServer(t, store), nil)
		assertRedirect(t, rr, "/settings/newsletters")
		if len(store.made) != 1 || !strings.HasPrefix(store.made[0], "samreader.") {
			t.Errorf("made %v, want one samreader.…", store.made)
		}
		if flash != "" {
			t.Errorf("flash = %q, want none", flash)
		}
	})

	t.Run("back to a safe return_to only", func(t *testing.T) {
		rr, _ := postNewsletterAddress(t, newsletterServer(t, &fakeNewsletterStore{}), url.Values{"return_to": {"/feeds?kind=newsletters"}})
		assertRedirect(t, rr, "/feeds?kind=newsletters")
		rr, _ = postNewsletterAddress(t, newsletterServer(t, &fakeNewsletterStore{}), url.Values{"return_to": {"//evil.example/"}})
		assertRedirect(t, rr, "/settings/newsletters")
	})

	t.Run("a duplicate local part is retried", func(t *testing.T) {
		store := &fakeNewsletterStore{}
		store.newFn = func(context.Context, string, string) (rss.NewsletterAddress, error) {
			if len(store.made) < 3 {
				return rss.NewsletterAddress{}, rss.ErrDuplicateAddress
			}
			return rss.NewsletterAddress{}, nil
		}
		rr, _ := postNewsletterAddress(t, newsletterServer(t, store), nil)
		assertRedirect(t, rr, "/settings/newsletters")
		if len(store.made) != 3 || store.made[0] == store.made[1] {
			t.Errorf("made %v, want 3 different tries", store.made)
		}
	})

	t.Run("duplicates every time", func(t *testing.T) {
		store := &fakeNewsletterStore{newFn: func(context.Context, string, string) (rss.NewsletterAddress, error) {
			return rss.NewsletterAddress{}, rss.ErrDuplicateAddress
		}}
		rr, _ := postNewsletterAddress(t, newsletterServer(t, store), nil)
		if rr.Code != http.StatusInternalServerError || len(store.made) != newsletterAddressTries {
			t.Errorf("status = %d after %d tries, want 500 after %d", rr.Code, len(store.made), newsletterAddressTries)
		}
	})

	t.Run("a double submit of the first address", func(t *testing.T) {
		store := &fakeNewsletterStore{currentFn: addressOf("sam.k7x2m9")}
		rr, _ := postNewsletterAddress(t, newsletterServer(t, store), nil)
		assertRedirect(t, rr, "/settings/newsletters")
		if len(store.made) != 0 {
			t.Errorf("made %v, want none", store.made)
		}
	})

	t.Run("a double submit made at the same time", func(t *testing.T) {
		store := &fakeNewsletterStore{newFn: func(context.Context, string, string) (rss.NewsletterAddress, error) {
			return rss.NewsletterAddress{}, rss.ErrAddressTooRecent
		}}
		rr, _ := postNewsletterAddress(t, newsletterServer(t, store), nil)
		assertRedirect(t, rr, "/settings/newsletters")
	})

	t.Run("a new address", func(t *testing.T) {
		store := &fakeNewsletterStore{currentFn: addressOf("sam.k7x2m9")}
		rr, flash := postNewsletterAddress(t, newsletterServer(t, store), url.Values{"new": {"true"}})
		assertRedirect(t, rr, "/settings/newsletters")
		if len(store.made) != 1 {
			t.Errorf("made %v, want one", store.made)
		}
		if !strings.Contains(flash, "Your old one keeps working for 30 days") {
			t.Errorf("flash = %q", flash)
		}
	})

	t.Run("a new address without one to replace", func(t *testing.T) {
		store := &fakeNewsletterStore{}
		rr, flash := postNewsletterAddress(t, newsletterServer(t, store), url.Values{"new": {"true"}})
		assertRedirect(t, rr, "/settings/newsletters")
		if len(store.made) != 1 || flash != "" {
			t.Errorf("made %v with flash %q, want one and no flash about an old address", store.made, flash)
		}
	})

	t.Run("a second new address within a day", func(t *testing.T) {
		store := &fakeNewsletterStore{currentFn: addressOf("sam.k7x2m9"), newFn: func(context.Context, string, string) (rss.NewsletterAddress, error) {
			return rss.NewsletterAddress{}, rss.ErrAddressTooRecent
		}}
		rr, _ := postNewsletterAddress(t, newsletterServer(t, store), url.Values{"new": {"true"}})
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusTooManyRequests)
		}
		assertBodyContains(t, rr.Body.String(), `role="alert">You can get a new address once a day.`, `value="sam.k7x2m9@in.localhost"`)
	})
}

func TestSettingsNewslettersRow(t *testing.T) {
	without := newsletterSession
	without.User.Features = 0
	tests := []struct {
		name    string
		domain  string
		sess    rss.Session
		current func(context.Context, string) (rss.NewsletterAddress, error)
		want    string // the row's hint; "" for no row
	}{
		{"before an address", "in.localhost", newsletterSession, nil, "Get your address"},
		{"with an address", "in.localhost", newsletterSession, addressOf("sam.k7x2m9"), "sam.k7x2m9@in.localhost"},
		{"without the feature", "in.localhost", without, addressOf("sam.k7x2m9"), ""},
		{"with no newsletter domain", "", newsletterSession, addressOf("sam.k7x2m9"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServerConfig(t, Config{Port: 4000, Env: "development", NewsletterDomain: tt.domain}, Services{NewsletterService: &fakeNewsletterStore{currentFn: tt.current}})
			rr := serveSettingsPage(t, s, s.settingsHandler, tt.sess)
			body := rr.Body.String()
			if tt.want == "" {
				if strings.Contains(body, "/settings/newsletters") {
					t.Error("Settings has a Newsletters row")
				}
				return
			}
			assertBodyContains(t, body, `href="/settings/newsletters"`, `<span class="hint settings-email">`+tt.want+`</span>`)
		})
	}
}
