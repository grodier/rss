package server

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/grodier/rss/internal/ui"
)

// avatarTones is how many avatar tints main.css defines (.tone-1 … .tone-6).
const avatarTones = 6

var templateFuncs = template.FuncMap{
	"avatarTone": avatarTone,
	"monogram":   monogram,
}

// avatarTone picks the avatar tint for a site ID, from 1 to avatarTones, so
// a site and its feeds always get the same color.
func avatarTone(siteID string) int {
	h := fnv.New32a()
	h.Write([]byte(siteID))
	return int(h.Sum32()%avatarTones) + 1
}

// monogram is the letter shown in an avatar: the first letter or digit of
// name, upper-cased, skipping "http(s)://" and "www." so a feed known only
// by its URL gets its host's letter. It is "" if name has none.
func monogram(name string) string {
	name = strings.TrimPrefix(name, "https://")
	name = strings.TrimPrefix(name, "http://")
	name = strings.TrimPrefix(name, "www.")
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return string(unicode.ToUpper(r))
		}
	}
	return ""
}

func parseTemplates() (map[string]*template.Template, error) {
	pages, err := fs.Glob(ui.Templates, "templates/*.html")
	if err != nil {
		return nil, err
	}

	ts := map[string]*template.Template{}
	for _, page := range pages {
		name := filepath.Base(page)
		t, err := template.New(name).Funcs(templateFuncs).ParseFS(ui.Templates, page, "templates/partials/*.html")
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
