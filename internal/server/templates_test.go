package server

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/ui"
	"github.com/grodier/rss/internal/validator"
)

var labelForRe = regexp.MustCompile(`<label[^>]*\sfor="([^"]+)"`)

func TestTemplateLabelsMatchInputs(t *testing.T) {
	files, err := fs.Glob(ui.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := fs.ReadFile(ui.Templates, f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, m := range labelForRe.FindAllStringSubmatch(src, -1) {
			if !strings.Contains(src, `id="`+m[1]+`"`) {
				t.Errorf("%s: label for=%q has no element with that id", f, m[1])
			}
		}
	}
}

func TestPasswordNotEchoed(t *testing.T) {
	const sentinel = "sentinel-pw-123"
	tests := []struct {
		name     string
		template string
		data     any
	}{
		{
			name:     "login",
			template: "login.html",
			data:     loginPage{Form: loginForm{Email: "a@b.co", Password: sentinel}},
		},
		{
			name:     "signup",
			template: "signup.html",
			data: signupPage{
				Form: signupForm{Name: "Ann", Email: "a@b.co", Password: sentinel},
			},
		},
		{
			name:     "reset password",
			template: "reset_password.html",
			data:     resetPasswordPage{Email: "a@b.co", Form: resetPasswordForm{Password: sentinel}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t)
			rr := httptest.NewRecorder()
			if err := s.renderHTML(rr, http.StatusUnprocessableEntity, tt.template, tt.data); err != nil {
				t.Fatal(err)
			}
			body := rr.Body.String()
			if strings.Contains(body, sentinel) {
				t.Error("response body contains the submitted password")
			}
			if !strings.Contains(body, "a@b.co") {
				t.Error("response body no longer echoes the email")
			}
		})
	}
}

// TestNoSignupLinks checks that no logged-out page links to /signup, whether
// or not an invite code is set: while signup needs a code, the page is only
// reached through an invite link.
func TestNoSignupLinks(t *testing.T) {
	for _, code := range []string{"", testInviteCode} {
		t.Run(fmt.Sprintf("code=%q", code), func(t *testing.T) {
			s := newTestServerConfig(t, Config{Port: 4000, Env: "development", InviteCode: code}, Services{})
			h := s.router()

			do := func(method, target string) *httptest.ResponseRecorder {
				var body io.Reader
				if method == http.MethodPost {
					body = strings.NewReader("")
				}
				req := httptest.NewRequestWithContext(t.Context(), method, target, body)
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, req)
				return rr
			}

			home := do(http.MethodGet, "/")
			login := do(http.MethodGet, "/login")
			failed := do(http.MethodPost, "/login")
			if failed.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST /login = %d, want 422", failed.Code)
			}
			for name, rr := range map[string]*httptest.ResponseRecorder{"GET /": home, "GET /login": login, "failed login": failed} {
				if strings.Contains(rr.Body.String(), `href="/signup"`) {
					t.Errorf("%s links to /signup", name)
				}
			}
			if !strings.Contains(home.Body.String(), `href="/login"`) {
				t.Error("GET / has no link to /login")
			}
		})
	}
}

// TestAppNav checks the nav partial: the link to /feeds says My feeds, the
// account link goes to /settings with the user's monogram, there's no logout
// form, and only the current section's link is marked aria-current.
func TestAppNav(t *testing.T) {
	ts, err := parseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	user := rss.User{ID: "00000000-0000-4000-8000-000000000001", Name: "ada lovelace"}
	tests := []struct {
		section string
		current string // href of the link marked current; "" for none
	}{
		{"home", "/"},
		{"feeds", "/feeds"},
		{"search", "/search"},
		{"settings", "/settings"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run("section "+tt.section, func(t *testing.T) {
			var buf bytes.Buffer
			if err := ts["timeline.html"].ExecuteTemplate(&buf, "app-nav", navData{Section: tt.section, User: user}); err != nil {
				t.Fatal(err)
			}
			doc, err := html.Parse(&buf)
			if err != nil {
				t.Fatal(err)
			}
			links := findAll(doc, func(n *html.Node) bool { return n.Data == "a" && n.Parent != nil && n.Parent.Data == "li" })
			if len(links) != 3 {
				t.Fatalf("found %d nav links, want 3", len(links))
			}
			accounts := findAll(doc, func(n *html.Node) bool { return n.Data == "a" && hasClass(n, "rail-account") })
			if len(accounts) != 1 {
				t.Fatalf("found %d account links, want 1", len(accounts))
			}
			account := accounts[0]
			if got := attr(account, "href"); got != "/settings" {
				t.Errorf("account link href = %q, want /settings", got)
			}
			if got := strings.TrimSpace(textContent(account)); !strings.HasSuffix(got, "Settings") {
				t.Errorf("account link text = %q, want it to end with Settings", got)
			}
			avatars := findAll(account, func(n *html.Node) bool { return hasClass(n, "avatar") })
			if len(avatars) != 1 || textContent(avatars[0]) != "A" {
				t.Errorf("account link has no avatar with the monogram A")
			}
			if forms := findAll(doc, func(n *html.Node) bool { return n.Data == "form" }); len(forms) != 0 {
				t.Errorf("found %d forms in the nav, want none", len(forms))
			}

			var current []string
			for _, a := range append(links, account) {
				href := attr(a, "href")
				if href == "/feeds" {
					if got := strings.TrimSpace(textContent(a)); got != "My feeds" {
						t.Errorf("the nav link to /feeds says %q, want My feeds", got)
					}
				}
				if attr(a, "aria-current") == "page" {
					current = append(current, href)
				}
			}
			var want []string
			if tt.current != "" {
				want = []string{tt.current}
			}
			if !slices.Equal(current, want) {
				t.Errorf("links marked current = %q, want %q", current, want)
			}
		})
	}
}

func textContent(n *html.Node) string {
	var b strings.Builder
	for d := range n.Descendants() {
		if d.Type == html.TextNode {
			b.WriteString(d.Data)
		}
	}
	return b.String()
}

func TestAvatarTone(t *testing.T) {
	seen := map[int]bool{}
	for i := range 100 {
		id := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i)
		tone := avatarTone(id)
		if tone < 1 || tone > avatarTones {
			t.Fatalf("avatarTone(%q) = %d, want 1 to %d", id, tone, avatarTones)
		}
		if avatarTone(id) != tone {
			t.Fatalf("avatarTone(%q) is not stable", id)
		}
		seen[tone] = true
	}
	if len(seen) != avatarTones {
		t.Errorf("100 IDs got %d of the %d tones", len(seen), avatarTones)
	}
}

func TestMonogram(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"title", "kernel notes", "K"},
		{"leading punctuation", "“The Slow Web”", "T"},
		{"digit", "99% Invisible", "9"},
		{"non-Latin letter", "ëlan", "Ë"},
		{"https URL", "https://example.com/feed.xml", "E"},
		{"http URL with www", "http://www.blog.example/rss", "B"},
		{"empty", "", ""},
		{"no letters", "—", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := monogram(tt.in); got != tt.want {
				t.Errorf("monogram(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseTemplates(t *testing.T) {
	ts, err := parseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	pages, err := fs.Glob(ui.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != len(pages) {
		t.Errorf("parsed %d pages, want %d", len(ts), len(pages))
	}
	for name, tmpl := range ts {
		if tmpl.Lookup("article-row") == nil {
			t.Errorf("%s: article-row is not defined", name)
		}
	}
	if _, ok := ts["feed.html"]; !ok {
		t.Error("feed.html was not parsed")
	}
}

// renderForm renders a form page with s.renderHTML and parses it.
func renderForm(t *testing.T, template string, data any) *html.Node {
	t.Helper()
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	if err := s.renderHTML(rr, http.StatusUnprocessableEntity, template, data); err != nil {
		t.Fatal(err)
	}
	doc, err := html.Parse(rr.Body)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func withFieldErrors(fields ...string) validator.Validator {
	var v validator.Validator
	for _, f := range fields {
		v.AddFieldError(f, f+" is wrong")
	}
	return v
}

// formInputs returns the inputs a user fills in: not hidden or read-only ones.
func formInputs(doc *html.Node) []*html.Node {
	return findAll(doc, func(n *html.Node) bool {
		return n.Data == "input" && attr(n, "type") != "hidden" && !hasAttr(n, "readonly")
	})
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func byID(doc *html.Node, id string) *html.Node {
	found := findAll(doc, func(n *html.Node) bool { return attr(n, "id") == id })
	if len(found) == 0 {
		return nil
	}
	return found[0]
}

func TestFormFieldErrorsDescribed(t *testing.T) {
	tests := []struct {
		name     string
		template string
		data     func(errFields ...string) any
		fields   []string
	}{
		{
			name:     "login",
			template: "login.html",
			data: func(errFields ...string) any {
				return loginPage{Form: loginForm{Validator: withFieldErrors(errFields...)}}
			},
			fields: []string{"email", "password"},
		},
		{
			name:     "signup",
			template: "signup.html",
			data: func(errFields ...string) any {
				return signupPage{Form: signupForm{Validator: withFieldErrors(errFields...)}}
			},
			fields: []string{"invite", "name", "email", "password"},
		},
		{
			name:     "forgot password",
			template: "forgot_password.html",
			data: func(errFields ...string) any {
				return forgotPasswordPage{Form: forgotPasswordForm{Validator: withFieldErrors(errFields...)}}
			},
			fields: []string{"email"},
		},
		{
			name:     "reset password",
			template: "reset_password.html",
			data: func(errFields ...string) any {
				return resetPasswordPage{Form: resetPasswordForm{Validator: withFieldErrors(errFields...)}}
			},
			fields: []string{"password"},
		},
		{
			name:     "settings name",
			template: "settings_name.html",
			data: func(errFields ...string) any {
				return settingsNamePage{Form: nameForm{Validator: withFieldErrors(errFields...)}}
			},
			fields: []string{"name"},
		},
		{
			name:     "settings password",
			template: "settings_password.html",
			data: func(errFields ...string) any {
				return settingsPasswordPage{Form: passwordForm{Validator: withFieldErrors(errFields...)}}
			},
			fields: []string{"current_password", "new_password"},
		},
		{
			name:     "settings delete",
			template: "settings_delete.html",
			data: func(errFields ...string) any {
				return settingsDeletePage{Form: deleteAccountForm{Validator: withFieldErrors(errFields...)}}
			},
			fields: []string{"password"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name+" with errors", func(t *testing.T) {
			doc := renderForm(t, tt.template, tt.data(tt.fields...))
			inputs := formInputs(doc)
			if len(inputs) != len(tt.fields) {
				t.Fatalf("found %d inputs, want %d", len(inputs), len(tt.fields))
			}
			for _, in := range inputs {
				id := attr(in, "id")
				if got := attr(in, "aria-invalid"); got != "true" {
					t.Errorf("#%s: aria-invalid = %q, want true", id, got)
				}
				ids := strings.Fields(attr(in, "aria-describedby"))
				if !slices.Contains(ids, id+"-error") {
					t.Errorf("#%s: aria-describedby = %q, want it to name %s-error", id, attr(in, "aria-describedby"), id)
				}
				for _, ref := range ids {
					if byID(doc, ref) == nil {
						t.Errorf("#%s: aria-describedby names #%s, which isn't in the page", id, ref)
					}
				}
				if el := byID(doc, id+"-error"); el != nil {
					if want := id + " is wrong"; strings.TrimSpace(textContent(el)) != want {
						t.Errorf("#%s-error says %q, want %q", id, textContent(el), want)
					}
				}
			}
		})
		t.Run(tt.name+" without errors", func(t *testing.T) {
			doc := renderForm(t, tt.template, tt.data())
			for _, in := range formInputs(doc) {
				id := attr(in, "id")
				if hasAttr(in, "aria-invalid") {
					t.Errorf("#%s has aria-invalid without an error", id)
				}
				for _, ref := range strings.Fields(attr(in, "aria-describedby")) {
					if byID(doc, ref) == nil {
						t.Errorf("#%s: aria-describedby names #%s, which isn't in the page", id, ref)
					}
				}
			}
		})
	}
}

func TestFormAutofocus(t *testing.T) {
	autofocused := func(doc *html.Node) []string {
		var ids []string
		for _, n := range findAll(doc, func(n *html.Node) bool { return hasAttr(n, "autofocus") }) {
			ids = append(ids, attr(n, "id"))
		}
		return ids
	}

	t.Run("signup errors on email and password", func(t *testing.T) {
		doc := renderForm(t, "signup.html", signupPage{
			Form: signupForm{Validator: withFieldErrors("password", "email")},
		})
		if got := autofocused(doc); !slices.Equal(got, []string{"email"}) {
			t.Errorf("autofocus on %q, want [email]", got)
		}
	})
	t.Run("signup without errors", func(t *testing.T) {
		doc := renderForm(t, "signup.html", signupPage{Form: signupForm{}})
		if got := autofocused(doc); len(got) != 0 {
			t.Errorf("autofocus on %q, want none", got)
		}
	})
	t.Run("login without errors", func(t *testing.T) {
		doc := renderForm(t, "login.html", loginPage{})
		if got := autofocused(doc); len(got) != 0 {
			t.Errorf("autofocus on %q, want none", got)
		}
	})
	t.Run("login with only a non-field error", func(t *testing.T) {
		var v validator.Validator
		v.AddNonFieldError("Too many attempts. Try again in 1 minute.")
		doc := renderForm(t, "login.html", loginPage{Form: loginForm{Validator: v}})
		if got := autofocused(doc); len(got) != 0 {
			t.Errorf("autofocus on %q, want none", got)
		}
		alerts := findAll(doc, func(n *html.Node) bool { return n.Data == "div" && hasClass(n, "error") })
		if len(alerts) != 1 {
			t.Fatalf("found %d error divs, want 1", len(alerts))
		}
		if got := attr(alerts[0], "role"); got != "alert" {
			t.Errorf("error div role = %q, want alert", got)
		}
	})
}

func TestSignupHints(t *testing.T) {
	tests := []struct {
		field string
		hint  string
	}{
		{"name", "What should we call you? We'll use it in emails."},
		{"password", "At least 8 characters."},
	}
	for _, withErrors := range []bool{false, true} {
		var v validator.Validator
		if withErrors {
			v = withFieldErrors("name", "password")
		}
		doc := renderForm(t, "signup.html", signupPage{Form: signupForm{Validator: v}})
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s errors=%t", tt.field, withErrors), func(t *testing.T) {
				hint := byID(doc, tt.field+"-hint")
				if hint == nil {
					t.Fatalf("no #%s-hint", tt.field)
				}
				if got := strings.TrimSpace(textContent(hint)); got != tt.hint {
					t.Errorf("#%s-hint says %q, want %q", tt.field, got, tt.hint)
				}
				input := byID(doc, tt.field)
				if input == nil {
					t.Fatalf("no #%s", tt.field)
				}
				ids := strings.Fields(attr(input, "aria-describedby"))
				if !slices.Contains(ids, tt.field+"-hint") {
					t.Errorf("#%s: aria-describedby = %q, want it to name %s-hint", tt.field, attr(input, "aria-describedby"), tt.field)
				}
			})
		}
	}
}
