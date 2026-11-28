package newsletter

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/grodier/rss/internal/rss"
)

// The fixtures in testdata are made up, never real newsletters. Their
// Cloudflare headers follow what real mail showed (#869).
const (
	testReader     = "reader@inbox.example"         // the reader's account address
	testNewsletter = "reader.k7x2m9@in.example.com" // the reader's newsletter address
)

// load parses testdata/name.
func load(t *testing.T, name string) rss.Email {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	e, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return e
}

func TestParseHeaders(t *testing.T) {
	e := load(t, "alternative.eml")
	checks := []struct{ name, got, want string }{
		{"FromName", e.FromName, "Café News"},
		{"From", e.From, "news@news.example.com"},
		{"ReplyTo", e.ReplyTo, "editor@news.example.com"},
		{"Subject", e.Subject, "Café weekly"},
		{"MessageID", e.MessageID, "<abc123@news.example.com>"},
		{"ListID", e.ListID, "weekly.news.example.com"},
		{"ListUnsubscribe", e.ListUnsubscribe, "<https://news.example.com/u/abc>, <mailto:u@news.example.com>"},
		{"ListUnsubscribePost", e.ListUnsubscribePost, "List-Unsubscribe=One-Click"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if want := []string{testNewsletter, "other@example.org"}; !slices.Equal(e.To, want) {
		t.Errorf("To = %q, want %q", e.To, want)
	}
	if want := []string{"cc@example.org"}; !slices.Equal(e.Cc, want) {
		t.Errorf("Cc = %q, want %q", e.Cc, want)
	}
	if want := time.Date(2026, 11, 27, 11, 59, 0, 0, time.UTC); !e.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", e.Date, want)
	}
	if len(e.AuthenticationResults) != 1 || !strings.HasPrefix(e.AuthenticationResults[0], "mx.cloudflare.net;") {
		t.Errorf("AuthenticationResults = %q, want Cloudflare's", e.AuthenticationResults)
	}
}

func TestParseBody(t *testing.T) {
	tests := []struct {
		fixture string
		want    string // in the HTML
		notWant string
	}{
		// multipart/alternative: the HTML part (base64) over the text part.
		{"alternative.eml", "<p>Hello <b>reader</b>, this week’s issue.</p>", "Hello reader, this week"},
		// quoted-printable, its soft line break joined.
		{"html-only.eml", "so it continues on the next line.</p>", "=\n"},
		// ISO-8859-1, text only: escaped and wrapped in paragraphs.
		{"text-only.eml", "<p>Un café &lt;noir&gt;.<br>Second line.</p>\n<p>Second paragraph.</p>", ""},
		// The attached text part isn't the body, and the PDF is dropped.
		{"attachment.eml", "<p>The body.</p>", "Attached notes"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			e := load(t, tt.fixture)
			if !strings.Contains(e.HTML, tt.want) {
				t.Errorf("HTML = %q, want it to contain %q", e.HTML, tt.want)
			}
			if tt.notWant != "" && strings.Contains(e.HTML, tt.notWant) {
				t.Errorf("HTML = %q, want it without %q", e.HTML, tt.notWant)
			}
		})
	}
}

func TestParseCutsHeaders(t *testing.T) {
	e := load(t, "long-header.eml")
	if len(e.Subject) != maxHeaderBytes {
		t.Errorf("len(Subject) = %d, want %d", len(e.Subject), maxHeaderBytes)
	}
}

func TestParseCutsBodyAndInvalidUTF8(t *testing.T) {
	raw := "From: news@example.com\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>\xff\x00" + strings.Repeat("a", 2*maxBodyBytes)
	e, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.HTML) > maxBodyBytes {
		t.Errorf("len(HTML) = %d, want at most %d", len(e.HTML), maxBodyBytes)
	}
	if !utf8.ValidString(e.HTML) || strings.ContainsRune(e.HTML, 0) {
		t.Error("HTML isn't valid UTF-8 without NUL bytes")
	}
}

func TestParseARC(t *testing.T) {
	e := load(t, "forwarded.eml")
	var got []string
	for _, s := range e.ARC {
		got = append(got, s.Sealer)
	}
	if want := []string{"google.com", "google.com", "cloudflare-email.net"}; !slices.Equal(got, want) {
		t.Errorf("ARC sealers, by instance = %q, want %q", got, want)
	}
	if r := e.ARC[1].Results; !strings.HasPrefix(r, "mx.google.com;") {
		t.Errorf("ARC[1].Results = %q, want it after its i=", r)
	}
}

func TestParseRefusesNoHeaders(t *testing.T) {
	if _, err := Parse([]byte("not an email")); err == nil {
		t.Error("Parse of a message without headers succeeded")
	}
}

func TestForwardedSender(t *testing.T) {
	tests := []struct{ name, text, want string }{
		{"gmail", "Worth it.\n\n---------- Forwarded message ---------\nFrom: Example News <News@Example.com>\nDate: today\n", "news@example.com"},
		{"apple mail", "Begin forwarded message:\n\nFrom: Example News <news@example.com>\nSubject: Hi\n", "news@example.com"},
		{"outlook", "________________________________\nFrom: Example News <news@example.com>\nSent: Friday, November 27, 2026 9:00 AM\nTo: Reader\n", "news@example.com"},
		{"quoted", "> ---------- Forwarded message ---------\n> From: news@example.com\n", "news@example.com"},
		{"a From line without Sent", "From: someone@example.com\nTo: you\n", ""},
		{"no block", "Just an email.\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := forwardedSender(tt.text); got != tt.want {
				t.Errorf("forwardedSender = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseManualForward(t *testing.T) {
	if got := load(t, "manual-forward.eml").ForwardedFrom; got != "news@news.example.org" {
		t.Errorf("ForwardedFrom = %q, want news@news.example.org", got)
	}
}
