package admin

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"sync"

	"github.com/grodier/rss/internal/ui"
)

var templateFuncs = template.FuncMap{
	"asset":     static.URL,
	"assetTags": static.Tags,
}

// navData is what the admin-nav partial shows: the current section
// ("overview", or "" on pages outside the sections). Every page's data
// carries it as Nav, like the reader's pages (#567 adds the admin).
type navData struct {
	Section string
}

// nav returns the navData for an admin page in section.
func (s *Server) nav(r *http.Request, section string) navData {
	return navData{Section: section}
}

// homePage is the Overview's data.
type homePage struct {
	Nav navData
}

// parseTemplates parses the admin's page templates once per process: they're
// embedded, so every Server can share them. Callers must not modify the map
// or the templates.
var parseTemplates = sync.OnceValues(func() (map[string]*template.Template, error) {
	pages, err := fs.Glob(ui.AdminTemplates, "admin/*.html")
	if err != nil {
		return nil, err
	}

	ts := map[string]*template.Template{}
	for _, page := range pages {
		name := path.Base(page)
		t, err := template.New(name).Funcs(templateFuncs).ParseFS(ui.AdminTemplates, page, "admin/partials/*.html")
		if err != nil {
			return nil, fmt.Errorf("parse admin template %s: %w", name, err)
		}
		ts[name] = t
	}
	return ts, nil
})

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
