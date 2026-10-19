package mail

import (
	"io/fs"
	"path"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/grodier/rss/internal/ui"
)

var testApp = App{URL: "https://rss.example.com", SupportEmail: "help@example.com"}

func TestRender(t *testing.T) {
	ts, err := ParseTemplates(ui.Emails, testApp)
	if err != nil {
		t.Fatalf("ParseTemplates() error = %v", err)
	}

	t.Run("test", func(t *testing.T) {
		m, err := ts.Render("test", nil)
		if err != nil {
			t.Fatalf("Render() error = %v", err)
		}
		if m.To != "" {
			t.Errorf("To = %q, want empty", m.To)
		}
		if want := "Test email from rss"; m.Subject != want {
			t.Errorf("Subject = %q, want %q", m.Subject, want)
		}
		for _, want := range []string{
			"This is a test email from rss at https://rss.example.com. If you can read it, sending works.",
			"—\nrss · https://rss.example.com\nQuestions? Reply to this email or write to help@example.com.\n",
		} {
			if !strings.Contains(m.Text, want) {
				t.Errorf("Text = %q, want it to contain %q", m.Text, want)
			}
		}
		for _, want := range []string{
			`<a href="https://rss.example.com">https://rss.example.com</a>`,
			"write to help@example.com.",
		} {
			if !strings.Contains(m.HTML, want) {
				t.Errorf("HTML = %q, want it to contain %q", m.HTML, want)
			}
		}
	})

	t.Run("data", func(t *testing.T) {
		layout, err := fs.ReadFile(ui.Emails, "emails/layout.tmpl")
		if err != nil {
			t.Fatal(err)
		}
		fsys := fstest.MapFS{
			"emails/layout.tmpl": {Data: layout},
			"emails/hello.tmpl": {Data: []byte(`
{{define "subject"}}
  Hello {{.}}
{{end}}
{{define "text"}}Hello {{.}}{{end}}
{{define "html"}}<p>Hello {{.}}</p>{{end}}
`)},
		}
		ts, err := ParseTemplates(fsys, testApp)
		if err != nil {
			t.Fatalf("ParseTemplates() error = %v", err)
		}
		m, err := ts.Render("hello", "<b>Ada</b>")
		if err != nil {
			t.Fatalf("Render() error = %v", err)
		}
		if want := "Hello <b>Ada</b>"; m.Subject != want {
			t.Errorf("Subject = %q, want %q (trimmed, not escaped)", m.Subject, want)
		}
		if want := "Hello <b>Ada</b>\n"; m.Text != want {
			t.Errorf("Text = %q, want %q (not escaped)", m.Text, want)
		}
		if want := "<p>Hello &lt;b&gt;Ada&lt;/b&gt;</p>\n"; m.HTML != want {
			t.Errorf("HTML = %q, want %q (escaped)", m.HTML, want)
		}

		if _, err := ts.Render("hello", "Ada\nBcc: eve@example.com"); err == nil {
			t.Error("Render() with a two-line subject: error = nil, want an error")
		}
		if _, err := ts.Render("missing", nil); err == nil {
			t.Error(`Render("missing") error = nil, want an error`)
		}
	})
}

// TestEmailTemplates checks that every email parses and defines the three
// blocks Render needs.
func TestEmailTemplates(t *testing.T) {
	ts, err := ParseTemplates(ui.Emails, testApp)
	if err != nil {
		t.Fatalf("ParseTemplates() error = %v", err)
	}
	files, err := fs.Glob(ui.Emails, "emails/*.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(path.Base(file), ".tmpl")
		if name == "layout" {
			continue
		}
		e, ok := ts.emails[name]
		if !ok {
			t.Errorf("%s: not parsed", file)
			continue
		}
		for _, block := range []string{"subject", "text"} {
			if e.text.Lookup(block) == nil {
				t.Errorf("%s doesn't define %q", file, block)
			}
		}
		if e.html.Lookup("html") == nil {
			t.Errorf("%s doesn't define %q", file, "html")
		}
	}
}

func TestParseTemplatesRequiresBlocks(t *testing.T) {
	layout, err := fs.ReadFile(ui.Emails, "emails/layout.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, email string
	}{
		{"no subject", `{{define "text"}}t{{end}}{{define "html"}}h{{end}}`},
		{"no text", `{{define "subject"}}s{{end}}{{define "html"}}h{{end}}`},
		{"no html", `{{define "subject"}}s{{end}}{{define "text"}}t{{end}}`},
		{"syntax error", `{{define "subject"}}s{{end}}{{define "text"}}{{.{{end}}{{define "html"}}h{{end}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"emails/layout.tmpl": {Data: layout},
				"emails/broken.tmpl": {Data: []byte(tt.email)},
			}
			if _, err := ParseTemplates(fsys, testApp); err == nil {
				t.Fatal("ParseTemplates() error = nil, want an error")
			}
		})
	}
}
