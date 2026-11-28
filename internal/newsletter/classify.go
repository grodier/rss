package newsletter

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/grodier/rss/internal/rss"
	xhtml "golang.org/x/net/html"
)

// AppDomain is the app's own domain: mail from it, or from a subdomain such
// as the newsletter domain, is never a newsletter, since the app never
// mails a reader's address.
const AppDomain = "paperround.app"

// gmailForwarding is the address Gmail's forwarding confirmation comes from.
const gmailForwarding = "forwarding-noreply@google.com"

// Kind is what kind of mail an email is, as Classify decides.
type Kind string

const (
	FromApp         Kind = "from_app"         // claims to come from the app's domain
	Failed          Kind = "failed"           // its sender failed authentication
	GmailForwarding Kind = "gmail_forwarding" // Gmail's forwarding confirmation
	ManualForward   Kind = "manual_forward"   // forwarded by hand by the reader
	Confirmation    Kind = "confirmation"     // a sign-up to confirm
	Newsletter      Kind = "newsletter"       // List-Unsubscribe or List-Id
	Other           Kind = "other"
)

// confirmWords match a confirmation's subject, and its link's text or URL.
// English first.
var confirmWords = regexp.MustCompile(`(?i)\b(confirm|confirmation|verify|verification|activate|activation|double[- ]opt[- ]in)\b`)

// Classify returns what kind of mail e is, the first that applies. A failed
// verdict is checked before everything but the app's own domain, so a forged
// email can't pose as a forward, a confirmation or a newsletter. Blocks,
// and whether the reader already follows the sender, are for the store.
func Classify(e rss.Email, v Verdict, readerEmail string) Kind {
	switch {
	case fromApp(e.From):
		return FromApp
	case v == Fail:
		return Failed
	case e.From == gmailForwarding:
		return GmailForwarding
	case manualForward(e, readerEmail):
		return ManualForward
	case confirmWords.MatchString(e.Subject):
		return Confirmation
	case e.ListUnsubscribe != "" || e.ListID != "":
		return Newsletter
	}
	return Other
}

// fromApp reports whether from's domain is AppDomain or a subdomain of it.
func fromApp(from string) bool {
	d := domain(from)
	return d == AppDomain || strings.HasSuffix(d, "."+AppDomain)
}

// manualForward reports whether e is a forward the reader typed: from their
// account's address, with a forwarded block in the body.
func manualForward(e rss.Email, readerEmail string) bool {
	return e.ForwardedFrom != "" && readerEmail != "" && e.From == strings.ToLower(readerEmail)
}

// Sender returns the key of the feed e belongs to, the first that applies:
// a manual forward's forwarded sender; List-Id's identifier when its
// organizational domain is one v authenticated; for PassVia with DKIM from
// another organization, "<from> via <domain>", so someone else signing as
// that From gets a feed of their own; otherwise From's address.
func Sender(e rss.Email, v Verdict, readerEmail string) string {
	if manualForward(e, readerEmail) {
		return e.ForwardedFrom
	}
	authenticated := authenticatedDomains(e, v)
	if e.ListID != "" {
		list := orgDomain(e.ListID)
		for _, d := range authenticated {
			if orgDomain(d) == list {
				return e.ListID
			}
		}
	}
	if v == PassVia {
		from := orgDomain(domain(e.From))
		aligned := false
		for _, d := range authenticated {
			aligned = aligned || orgDomain(d) == from
		}
		if !aligned && len(authenticated) > 0 {
			return e.From + " via " + authenticated[0]
		}
	}
	return e.From
}

// ToReader reports whether e was sent to address, the reader's newsletter
// address, in To or Cc (ignoring case and a +tag), rather than forwarded
// from their own inbox, whose List-Unsubscribe isn't the newsletter's.
func ToReader(e rss.Email, address string) bool {
	want := untagged(address)
	for _, list := range [][]string{e.To, e.Cc} {
		for _, a := range list {
			if want != "" && untagged(a) == want {
				return true
			}
		}
	}
	return false
}

// untagged returns an address lowercased and without a +tag.
func untagged(addr string) string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return ""
	}
	local, _, _ := strings.Cut(addr[:at], "+")
	return local + addr[at:]
}

// ConfirmLink returns the link the reader opens to confirm: for Gmail's
// forwarding confirmation, which has a link and no code (#869), its first
// https link to google.com; for a confirmation, the first https link whose
// text or URL looks like confirming. It's "" for other kinds, or when
// there's none. Links are never opened for the reader ("Newsletters" in
// docs/decisions.md).
func ConfirmLink(e rss.Email, k Kind) string {
	var match func(l link) bool
	switch k {
	case GmailForwarding:
		match = func(l link) bool {
			host := l.url.Hostname()
			return host == "google.com" || strings.HasSuffix(host, ".google.com")
		}
	case Confirmation:
		match = func(l link) bool { return confirmWords.MatchString(l.text) || confirmWords.MatchString(l.url.String()) }
	default:
		return ""
	}
	for _, l := range links(e.HTML) {
		if match(l) {
			return l.url.String()
		}
	}
	return ""
}

// link is an https link in a body, with its text.
type link struct {
	url  *url.URL
	text string
}

var bareURL = regexp.MustCompile(`https://[^\s<>"']+`)

// links returns a body's https links in order: its anchors, then the bare
// URLs in its text (a text email's links aren't anchors).
func links(body string) []link {
	var out []link
	add := func(href, text string) {
		if u, err := url.Parse(strings.TrimSpace(href)); err == nil && u.Scheme == "https" && u.Host != "" {
			out = append(out, link{url: u, text: strings.Join(strings.Fields(text), " ")})
		}
	}
	z := xhtml.NewTokenizer(strings.NewReader(body))
	var href string
	var text strings.Builder
	inAnchor := false
	for tt := z.Next(); tt != xhtml.ErrorToken; tt = z.Next() {
		tok := z.Token()
		switch {
		case tok.Type == xhtml.StartTagToken && tok.Data == "a":
			href, inAnchor = "", true
			text.Reset()
			for _, a := range tok.Attr {
				if a.Key == "href" {
					href = a.Val
				}
			}
		case tok.Type == xhtml.EndTagToken && tok.Data == "a" && inAnchor:
			add(href, text.String())
			inAnchor = false
		case tok.Type == xhtml.TextToken && inAnchor:
			text.WriteString(tok.Data)
		}
	}
	for _, u := range bareURL.FindAllString(htmlText(body), -1) {
		add(strings.TrimRight(u, ".,);"), "")
	}
	return out
}
