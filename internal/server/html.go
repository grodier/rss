package server

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"

	"github.com/grodier/rss/internal/ui"
)

func parseTemplates() (map[string]*template.Template, error) {
	pages, err := fs.Glob(ui.Templates, "templates/*.html")
	if err != nil {
		return nil, err
	}

	ts := map[string]*template.Template{}
	for _, page := range pages {
		name := filepath.Base(page)
		t, err := template.New(name).ParseFS(ui.Templates, page)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		ts[name] = t
	}
	return ts, nil
}

func (s *Server) renderHTML(w http.ResponseWriter, status int, name string, data any) error {
	t, ok := s.templates[name]
	if !ok {
		return fmt.Errorf("template %q not found", name)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}
