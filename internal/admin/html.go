package admin

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/grodier/rss/internal/admin/chart"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/ui"
)

var templateFuncs = template.FuncMap{
	"asset":     static.URL,
	"assetTags": static.Tags,
	// chart draws a chart.LineChart or chart.Histogram: {{chart .Latency}}.
	"chart": func(c chart.Chart) template.HTML { return c.SVG() },
	// waitlistStatus says where a waitlist row stands, at now.
	"waitlistStatus": waitlistStatus,
	// inviteStatus is an invite's status at now, as the invites page says
	// it.
	"inviteStatus": inviteStatus,
	// percent is part as a share of whole, like "40%", or "" when whole is 0.
	"percent": func(part, whole int) string {
		if whole == 0 {
			return ""
		}
		return fmt.Sprintf("%d%%", part*100/whole)
	},
	// count formats n with commas between thousands: 1,284.
	"count": count,
	// subf subtracts b from a, to place an SVG shape from its end.
	"subf": func(a, b float64) float64 { return a - b },
	// ratingWord says a rating (good, needs-improvement, poor) in words.
	"ratingWord": func(r string) string { return ratingWords[r] },
	// segmented is a filter's data for the segmented partial: its label
	// and choices.
	"segmented": func(label string, links []filterLink) map[string]any {
		return map[string]any{"Label": label, "Links": links}
	},
	// countTable is the count-table partial's data: its columns' names, its
	// rows, and whether it shows their shares rather than their views.
	"countTable": func(name, column string, rows []countRow, share bool) map[string]any {
		return map[string]any{"Name": name, "Column": column, "Rows": rows, "Share": share}
	},
	"lower": strings.ToLower,
	// date formats t as a UTC date.
	"date": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
	// stamp formats t as a UTC date and time to the minute, "—" when zero.
	"stamp": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.UTC().Format("2006-01-02 15:04")
	},
	// perWeek formats a feed's posts a week to one decimal place.
	"perWeek": func(n float64) string { return fmt.Sprintf("%.1f", n) },
	// failing is whether a feed's failures in a row put it under the feeds
	// page's Failing filter.
	"failing": func(n int) bool { return n >= rss.AdminFailingAfter },
}

// navData is what the admin-nav partial shows: the current section
// ("overview", "waitlist", "feeds", "service", "webperf", "audit", or ""
// on pages outside the sections) and the logged-in admin's email, "" when
// no one is logged in, which hides the sections and Log out. Every page's data carries it as Nav, like the reader's pages.
type navData struct {
	Section string
	Email   string
}

// nav returns the navData for an admin page in section.
func (s *Server) nav(r *http.Request, section string) navData {
	nav := navData{Section: section}
	if admin, ok := s.authenticatedAdmin(r); ok {
		nav.Email = admin.Email
	}
	return nav
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
