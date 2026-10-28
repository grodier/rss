package mail

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"path"
	"strings"
	texttemplate "text/template"
)

// App is what every email template can use, as {{appURL}} and
// {{supportEmail}}.
type App struct {
	URL          string // the base URL, e.g. https://paperround.app
	SupportEmail string
}

// Templates renders the emails in fsys (ui.Emails).
type Templates struct {
	emails map[string]emailTemplate
}

// emailTemplate is one email's file, parsed with layout.tmpl by both engines:
// text has its subject and text blocks, html its html block.
type emailTemplate struct {
	text *texttemplate.Template
	html *htmltemplate.Template
}

// ParseTemplates parses each emails/<name>.tmpl in fsys, together with
// emails/layout.tmpl. Each must define subject, text and html.
func ParseTemplates(fsys fs.FS, app App) (*Templates, error) {
	funcs := map[string]any{
		"appURL":       func() string { return app.URL },
		"supportEmail": func() string { return app.SupportEmail },
	}

	files, err := fs.Glob(fsys, "emails/*.tmpl")
	if err != nil {
		return nil, err
	}

	t := &Templates{emails: map[string]emailTemplate{}}
	for _, file := range files {
		name := strings.TrimSuffix(path.Base(file), ".tmpl")
		if name == "layout" {
			continue
		}
		text, err := texttemplate.New(name).Funcs(funcs).ParseFS(fsys, "emails/layout.tmpl", file)
		if err != nil {
			return nil, fmt.Errorf("parse email %s: %w", name, err)
		}
		html, err := htmltemplate.New(name).Funcs(funcs).ParseFS(fsys, "emails/layout.tmpl", file)
		if err != nil {
			return nil, fmt.Errorf("parse email %s: %w", name, err)
		}
		for _, block := range []string{"subject", "text"} {
			if text.Lookup(block) == nil {
				return nil, fmt.Errorf("email %s doesn't define %q", name, block)
			}
		}
		if html.Lookup("html") == nil {
			return nil, fmt.Errorf("email %s doesn't define %q", name, "html")
		}
		t.emails[name] = emailTemplate{text: text, html: html}
	}
	return t, nil
}

// Render returns the named email (without To) rendered with data.
func (t *Templates) Render(name string, data any) (Message, error) {
	e, ok := t.emails[name]
	if !ok {
		return Message{}, fmt.Errorf("email %q not found", name)
	}

	var subject, text, html bytes.Buffer
	if err := e.text.ExecuteTemplate(&subject, "subject", data); err != nil {
		return Message{}, fmt.Errorf("render email %s: %w", name, err)
	}
	if err := e.text.ExecuteTemplate(&text, "text", data); err != nil {
		return Message{}, fmt.Errorf("render email %s: %w", name, err)
	}
	if err := e.html.ExecuteTemplate(&html, "html", data); err != nil {
		return Message{}, fmt.Errorf("render email %s: %w", name, err)
	}

	m := Message{
		Subject: strings.TrimSpace(subject.String()),
		Text:    strings.TrimSpace(text.String()) + "\n",
		HTML:    strings.TrimSpace(html.String()) + "\n",
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return Message{}, fmt.Errorf("render email %s: subject spans more than one line", name)
	}
	return m, nil
}
