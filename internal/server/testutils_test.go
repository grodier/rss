package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
)

// newTestServer returns a Server wired with no database and no services.
// Services{} holds nil stores (but a SiteService reporting no icons), so tests
// can only exercise code paths that return before a store call (validation,
// routing, middleware, templates). Use newTestServerWith and the fakes in
// fakes_test.go for the rest.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerWith(t, Services{})
}

// newTestServerWith returns a Server wired with the given services (typically
// fakes from fakes_test.go) and no database.
func newTestServerWith(t *testing.T, svc Services) *Server {
	t.Helper()
	return newTestServerConfig(t, Config{Port: 4000, Env: "development"}, svc)
}

// newTestServerConfig is newTestServerWith with an explicit Config.
func newTestServerConfig(t *testing.T, cfg Config, svc Services) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Most pages look up site icons; a test that doesn't care gets none.
	if svc.SiteService == nil {
		svc.SiteService = &fakeSiteStore{}
	}
	// Every page with article rows reads stars; a test that doesn't care
	// gets none.
	if svc.StarService == nil {
		svc.StarService = &fakeStarStore{}
	}
	// Every timeline page lists the tabs; a test that doesn't care gets none.
	if svc.TimelineService == nil {
		svc.TimelineService = &fakeTimelineStore{}
	}
	// Search with no query browses the catalog; a test that doesn't care
	// gets an empty one.
	if svc.CatalogService == nil {
		svc.CatalogService = &fakeCatalogStore{}
	}
	// /healthcheck pings the database; a test that doesn't care gets one
	// that answers.
	if svc.Database == nil {
		svc.Database = &fakeDatabase{}
	}
	s, err := NewServer(logger, cfg, svc, scs.New())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// testSiteIcons checks how a page shows site siteID's avatar for each outcome
// of IconHashes: its icon, its letter on its tint, and its letter (with the
// error logged) when the store fails. s's SiteService must be a
// *fakeSiteStore; render serves the page from s and returns its body.
func testSiteIcons(t *testing.T, s *Server, siteID, letter string, render func(t *testing.T) string) {
	t.Helper()
	sites := s.services.SiteService.(*fakeSiteStore)
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, nil))
	letterAvatar := fmt.Sprintf(`<span class="avatar tone-%d" aria-hidden="true">%s</span>`, avatarTone(siteID), letter)

	t.Run("icon", func(t *testing.T) {
		sites.iconHashesFn = func(_ context.Context, ids []string) (map[string]string, error) {
			if !slices.Contains(ids, siteID) {
				t.Errorf("IconHashes(%v) doesn't ask for %s", ids, siteID)
			}
			return map[string]string{siteID: "abc123"}, nil
		}
		body := render(t)
		if want := `<img src="/sites/` + siteID + `/icon?v=abc123"`; !strings.Contains(body, want) {
			t.Errorf("body does not contain %q: %s", want, body)
		}
		if strings.Contains(body, letterAvatar) {
			t.Errorf("body still contains the letter avatar %q", letterAvatar)
		}
	})

	t.Run("no icon", func(t *testing.T) {
		sites.iconHashesFn = func(context.Context, []string) (map[string]string, error) {
			return map[string]string{}, nil
		}
		body := render(t)
		if !strings.Contains(body, letterAvatar) {
			t.Errorf("body does not contain %q: %s", letterAvatar, body)
		}
		if strings.Contains(body, "avatar-icon") {
			t.Errorf("body contains an icon avatar: %s", body)
		}
	})

	t.Run("IconHashes error", func(t *testing.T) {
		logs.Reset()
		sites.iconHashesFn = func(context.Context, []string) (map[string]string, error) {
			return nil, errors.New("icon hashes boom")
		}
		body := render(t)
		if !strings.Contains(body, letterAvatar) {
			t.Errorf("body does not contain %q: %s", letterAvatar, body)
		}
		if !strings.Contains(logs.String(), "icon hashes boom") {
			t.Errorf("error not logged: %s", logs.String())
		}
	})
}
