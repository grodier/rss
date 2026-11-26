package server

import (
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/grodier/rss/internal/httpserve"
	"github.com/grodier/rss/internal/rss"
	"golang.org/x/text/unicode/norm"
)

// newslettersOn reports whether the request's reader can use newsletters:
// they have the feature and -newsletter-domain is set.
func (s *Server) newslettersOn(r *http.Request) bool {
	if s.config.NewsletterDomain == "" {
		return false
	}
	sess, ok := s.authenticatedSession(r)
	return ok && sess.User.Has(rss.FeatureNewsletters)
}

// requireFeature answers 404 unless the reader has feature (one of the
// rss.Feature constants), as debug routes do, so a reader without it can't
// tell the routes exist. For newsletters it also needs -newsletter-domain.
func (s *Server) requireFeature(feature string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, ok := s.authenticatedSession(r)
			if !ok || !sess.User.Has(feature) || (feature == rss.FeatureNewsletters && s.config.NewsletterDomain == "") {
				s.notFoundResponse(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// newsletterLocalPartMax is how many letters of the reader's name a local
// part keeps.
const newsletterLocalPartMax = 20

// newsletterAlphabet is the random part's alphabet: lowercase letters and
// digits without l, o, 0 and 1, which are easy to misread.
const newsletterAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

// newsletterRandomLen is the random part's length: 32^6, about a billion.
const newsletterRandomLen = 6

// transliterations are the letters NFKD doesn't take apart into an ASCII
// letter and an accent.
var transliterations = map[rune]string{
	'ø': "o", 'ß': "ss", 'æ': "ae", 'œ': "oe", 'ł': "l", 'đ': "d", 'ð': "d", 'þ': "th", 'ı': "i",
}

// newsletterLocalPart makes the part before the @ of a new address from the
// reader's name: its letters as lowercase ASCII (common letters
// transliterated, other accents removed, everything else dropped), at most
// newsletterLocalPartMax, or "reader" if none are left; a dot; and
// newsletterRandomLen characters of newsletterAlphabet read from random.
// The name makes it recognizable in a sign-up form, and the random part
// unguessable. It never has a hyphen, so the admin's canary address (#844)
// can't be a reader's.
func newsletterLocalPart(name string, random io.Reader) (string, error) {
	var b strings.Builder
	for _, r := range norm.NFKD.String(strings.ToLower(name)) {
		if b.Len() >= newsletterLocalPartMax {
			break
		}
		// After NFKD an accented letter is its base letter and a combining
		// mark, which is dropped with everything else that isn't a-z.
		t, ok := transliterations[r]
		if !ok {
			t = string(r)
		}
		for _, c := range t {
			if c >= 'a' && c <= 'z' && b.Len() < newsletterLocalPartMax {
				b.WriteRune(c)
			}
		}
	}
	prefix := b.String()
	if prefix == "" {
		prefix = "reader"
	}

	buf := make([]byte, newsletterRandomLen)
	if _, err := io.ReadFull(random, buf); err != nil {
		return "", err
	}
	suffix := make([]byte, newsletterRandomLen)
	for i, c := range buf {
		// 256 is a multiple of 32, so every character is equally likely.
		suffix[i] = newsletterAlphabet[int(c)%len(newsletterAlphabet)]
	}
	return prefix + "." + string(suffix), nil
}

// newsletterAddressTries is how many random parts a new address tries
// before giving up on duplicates.
const newsletterAddressTries = 3

// newsletterPage is Settings › Newsletters' data.
type newsletterPage struct {
	Nav   navData
	Flash string
	// Address is the reader's address at the newsletter domain, "" before
	// they've got one.
	Address string
	// Error is a refusal of Get a new address.
	Error string
	// Option is the design checkpoint's option (a, b or c), from ?option=.
	// DESIGN CHECKPOINT ONLY: it goes with the options not picked.
	Option string
}

// settingsNewslettersHandler shows Settings › Newsletters: the reader's
// address, or Get your address before they have one.
func (s *Server) settingsNewslettersHandler(w http.ResponseWriter, r *http.Request) {
	s.renderSettingsNewsletters(w, r, http.StatusOK, "")
}

func (s *Server) renderSettingsNewsletters(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	nav := s.nav(r, "settings")
	data := newsletterPage{
		Nav:   nav,
		Flash: s.sessionManager.PopString(r.Context(), "flash"),
		Error: errMsg,
	}
	switch o := r.URL.Query().Get("option"); o {
	case "b", "c":
		data.Option = o
	default:
		data.Option = "a"
	}
	a, err := s.services.NewsletterService.CurrentAddress(r.Context(), nav.User.ID)
	switch {
	case errors.Is(err, rss.ErrNoRecord):
	case err != nil:
		s.serverErrorHTML(w, r, err)
		return
	default:
		data.Address = a.Email(s.config.NewsletterDomain)
	}
	if err := s.renderHTML(w, status, "settings_newsletters.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// newsletterAddressForm is Get your address (no New) and Get a new address
// (New). ReturnTo is where to go after, checked with SafeReturnPath, so My
// feeds' Newsletters view (#832) can post here too.
type newsletterAddressForm struct {
	New      bool   `form:"new"`
	ReturnTo string `form:"return_to"`
}

// settingsNewsletterAddressHandler makes the reader an address: their first
// (Get your address), or a new one that retires the current one, which
// keeps receiving for rss.NewsletterRetiredFor (Get a new address), at most
// once a day. A first address asked for twice (a double submit) just shows
// the one made.
func (s *Server) settingsNewsletterAddressHandler(w http.ResponseWriter, r *http.Request) {
	var form newsletterAddressForm
	if err := s.decodePostForm(w, r, &form); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	user := s.nav(r, "").User
	back := httpserve.SafeReturnPath(form.ReturnTo, "/settings/newsletters")

	if !form.New {
		_, err := s.services.NewsletterService.CurrentAddress(r.Context(), user.ID)
		if err == nil {
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		if !errors.Is(err, rss.ErrNoRecord) {
			s.serverErrorHTML(w, r, err)
			return
		}
	}

	var err error
	for range newsletterAddressTries {
		var localPart string
		localPart, err = newsletterLocalPart(user.Name, rand.Reader)
		if err != nil {
			break
		}
		_, err = s.services.NewsletterService.NewAddress(r.Context(), user.ID, localPart)
		if !errors.Is(err, rss.ErrDuplicateAddress) {
			break
		}
	}
	switch {
	case err == nil:
		if form.New {
			s.sessionManager.Put(r.Context(), "flash", "Here's your new address. Your old one keeps working for 30 days, so you can move your newsletters over.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
	case errors.Is(err, rss.ErrAddressTooRecent) && !form.New:
		// Another request made the first address meanwhile.
		http.Redirect(w, r, back, http.StatusSeeOther)
	case errors.Is(err, rss.ErrAddressTooRecent):
		s.renderSettingsNewsletters(w, r, http.StatusTooManyRequests, "You can get a new address once a day.")
	default:
		s.serverErrorHTML(w, r, err)
	}
}
