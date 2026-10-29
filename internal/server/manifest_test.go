package server

import (
	"encoding/json"
	"image"
	_ "image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/ui"
)

func TestManifestHandler(t *testing.T) {
	rr := httptest.NewRecorder()
	newTestServer(t).router().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/manifest+json" {
		t.Errorf("Content-Type = %q, want application/manifest+json", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q, want public, max-age=86400", got)
	}

	var m struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		StartURL string `json:"start_url"`
		Scope    string `json:"scope"`
		Display  string `json:"display"`
		Icons    []struct {
			Src   string `json:"src"`
			Sizes string `json:"sizes"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	for field, got := range map[string]string{"id": m.ID, "start_url": m.StartURL, "scope": m.Scope} {
		if got != "/" {
			t.Errorf("%s = %q, want /", field, got)
		}
	}
	if m.Display != "standalone" {
		t.Errorf("display = %q, want standalone", m.Display)
	}
	if m.Name == "" {
		t.Error("name is empty")
	}

	sizes := map[string]bool{}
	for _, icon := range m.Icons {
		sizes[icon.Sizes] = true
		name, ok := strings.CutPrefix(icon.Src, "/static/")
		if !ok {
			t.Errorf("icon src %q is not under /static/", icon.Src)
			continue
		}
		if _, err := fs.Stat(ui.Static, "static/"+name); err != nil {
			t.Errorf("icon %q: %v", icon.Src, err)
		}
	}
	for _, want := range []string{"192x192", "512x512"} {
		if !sizes[want] {
			t.Errorf("no %s icon", want)
		}
	}
}

func TestPagesIncludeAppHead(t *testing.T) {
	files, err := fs.Glob(ui.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := fs.ReadFile(ui.Templates, f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `{{template "app-head"}}`) {
			t.Errorf("%s: pages are standalone, so each needs {{template \"app-head\"}} for its icons and manifest", f)
		}
	}
}

func TestAppHeadRendered(t *testing.T) {
	h := newTestServer(t).router()
	for _, path := range []string{"/login", "/no-such-page"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		body := rr.Body.String()
		for _, want := range []string{
			`<link rel="manifest" href="/manifest.webmanifest"`,
			`<link rel="apple-touch-icon" href="/static/img/apple-touch-icon.png?v=`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s: body lacks %s", path, want)
			}
		}
	}
}

func TestInstallIconSizes(t *testing.T) {
	for name, size := range map[string]int{
		"favicon-32.png":       32,
		"apple-touch-icon.png": 180,
		"icon-192.png":         192,
		"icon-512.png":         512,
	} {
		f, err := ui.Static.Open("static/img/" + name)
		if err != nil {
			t.Fatal(err)
		}
		cfg, format, err := image.DecodeConfig(f)
		f.Close()
		if err != nil || format != "png" {
			t.Errorf("%s: format %q, err %v; want a PNG", name, format, err)
			continue
		}
		if cfg.Width != size || cfg.Height != size {
			t.Errorf("%s is %dx%d, want %dx%d", name, cfg.Width, cfg.Height, size, size)
		}
	}
}
