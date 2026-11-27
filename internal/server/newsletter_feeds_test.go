package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/rss"
)

// A newsletter feed of debugSession's reader (reader A), with one article,
// on the newsletters' internal site; newsletterReaderB is another reader.
const (
	newsletterFeedID    = "c0c0c0c0-0000-4000-8000-0000000000c1"
	newsletterArticleID = "d0d0d0d0-0000-4000-8000-0000000000d1"
	newsletterSiteID    = "e0e0e0e0-0000-4000-8000-0000000000e1"
	newsletterReaderB   = "b0b0b0b0-0000-4000-8000-0000000000b1"
)

// ownedFeedServer is a server whose stores hold reader A's newsletter feed
// and answer as psql's do: GetVisible is rss.ErrNoRecord for anyone else,
// and so is a subscription to it. It returns the writes and fetches made,
// by name, in order.
func ownedFeedServer(t *testing.T) (*Server, *[]string) {
	t.Helper()
	writes := new([]string)
	record := func(what string) func() { return func() { *writes = append(*writes, what) } }
	owner := debugSession.User.ID
	feed := rss.Feed{
		ID: newsletterFeedID, SiteID: newsletterSiteID, OwnerID: owner, Url: "newsletter:" + newsletterFeedID,
		Title: "A newsletter", NextFetch: time.Now().Add(-time.Hour),
	}
	article := rss.Article{
		ID: newsletterArticleID, FeedID: newsletterFeedID, URL: "https://example.com/issue-1?u=personal",
		Title: "Issue 1", ImageURL: "https://example.com/i.png",
	}
	fetcher := &fakeFetcher{err: errors.New("fetcher must not be called")}
	discoverer := &fakeDiscoverer{err: errors.New("discoverer must not be called")}
	t.Cleanup(func() {
		if len(fetcher.urls) != 0 || len(discoverer.urls) != 0 {
			t.Errorf("fetched %v and discovered %v, want neither", fetcher.urls, discoverer.urls)
		}
	})
	mark := func(what string) func(context.Context, string, string) error {
		return func(context.Context, string, string) error { record(what)(); return nil }
	}
	s := newTestServerConfig(t, debugConfig, Services{
		FeedService: &fakeFeedStore{
			getVisibleFn: func(_ context.Context, userID, id string) (rss.Feed, error) {
				if id != newsletterFeedID || userID != owner {
					return rss.Feed{}, rss.ErrNoRecord
				}
				return feed, nil
			},
			requestRefreshFn: func(context.Context, string) error { record("RequestRefresh")(); return nil },
		},
		ArticleService: &fakeArticleStore{
			getVisibleFn: func(_ context.Context, userID, id string) (rss.Article, error) {
				if id != newsletterArticleID || userID != owner {
					return rss.Article{}, rss.ErrNoRecord
				}
				return article, nil
			},
			reportImageFn: func(context.Context, string) error { record("ReportImageFailed")(); return nil },
		},
		SiteService: &fakeSiteStore{getByIDFn: func(_ context.Context, id string) (rss.Site, error) {
			if id != newsletterSiteID {
				return rss.Site{}, rss.ErrNoRecord
			}
			return rss.Site{ID: id, Host: rss.NewsletterSiteHost, Title: "Newsletters"}, nil
		}},
		SubscriptionService: &fakeSubscriptionStore{
			getFn: func(_ context.Context, userID, feedID string) (rss.Subscription, error) {
				if feedID != newsletterFeedID || userID != owner {
					return rss.Subscription{}, rss.ErrNoRecord
				}
				return rss.Subscription{Feed: feed}, nil
			},
			subscribeFn:   mark("Subscribe"),
			unsubscribeFn: mark("Unsubscribe"),
			renameFn:      func(context.Context, string, string, string) error { record("Rename")(); return nil },
		},
		ReadService: &fakeReadStore{markReadFn: mark("MarkRead")},
		SaveService: &fakeSaveStore{saveFn: mark("Save"), unsaveFn: mark("Unsave")},
		StarService: &fakeStarStore{starFn: mark("Star"), unstarFn: mark("Unstar")},
		Refresher: &fakeRefresher{refreshFn: func(context.Context, rss.Feed) (rss.FetchResult, error) {
			record("Refresh")()
			return rss.FetchResult{}, nil
		}},
		Fetcher:    fetcher,
		Discoverer: discoverer,
	})
	return s, writes
}

// serveAs serves h for a request with method to target, with the route's
// id, as userID. A POST carries return_to=/. Debug mode is on, and h runs
// behind requireDebug, when debug is set.
func serveAs(t *testing.T, s *Server, h http.HandlerFunc, method, target, id, userID string, debug bool) (rr *httptest.ResponseRecorder, flash string) {
	t.Helper()
	body := ""
	if method == http.MethodPost {
		body = "return_to=%2F"
	}
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if debug {
		h = s.requireDebug(h).ServeHTTP
	}
	sess := rss.Session{ID: debugSession.ID, User: rss.User{ID: userID, Email: debugSession.User.Email}}
	rr, _, flash = serveDebug(t, s, h, withRouteID(req, id), &sess, debug)
	return rr, flash
}

// newsletterRoute is a route that takes a newsletter feed's or article's ID.
type newsletterRoute struct {
	name, method, target, id string
	h                        func(s *Server) http.HandlerFunc
	debug                    bool // a debug inspector, behind requireDebug
}

var newsletterRoutes = func() []newsletterRoute {
	feed, article := "/feeds/"+newsletterFeedID, "/articles/"+newsletterArticleID
	get, post := http.MethodGet, http.MethodPost
	f, a := newsletterFeedID, newsletterArticleID
	return []newsletterRoute{
		{"feed page", get, feed, f, func(s *Server) http.HandlerFunc { return s.feedHandler }, false},
		{"refresh", post, feed + "/refresh", f, func(s *Server) http.HandlerFunc { return s.feedRefreshHandler }, false},
		{"subscribe", post, feed + "/subscribe", f, func(s *Server) http.HandlerFunc { return s.feedSubscribeHandler }, false},
		{"unsubscribe", post, feed + "/unsubscribe", f, func(s *Server) http.HandlerFunc { return s.feedUnsubscribeHandler }, false},
		{"unfollow", get, feed + "/unfollow", f, func(s *Server) http.HandlerFunc { return s.feedUnfollowHandler }, false},
		{"edit", get, feed + "/edit", f, func(s *Server) http.HandlerFunc { return s.subscriptionEditHandler }, false},
		{"edit post", post, feed + "/edit", f, func(s *Server) http.HandlerFunc { return s.subscriptionEditPostHandler }, false},
		{"article page", get, article, a, func(s *Server) http.HandlerFunc { return s.articleHandler }, false},
		{"read", post, article + "/read", a, func(s *Server) http.HandlerFunc { return s.articleReadHandler }, false},
		{"image failed", post, article + "/image-failed", a, func(s *Server) http.HandlerFunc { return s.articleImageFailedHandler }, false},
		{"save", post, article + "/save", a, func(s *Server) http.HandlerFunc { return s.articleSaveHandler }, false},
		{"unsave", post, article + "/unsave", a, func(s *Server) http.HandlerFunc { return s.articleUnsaveHandler }, false},
		{"star", post, article + "/star", a, func(s *Server) http.HandlerFunc { return s.articleStarHandler }, false},
		{"unstar", post, article + "/unstar", a, func(s *Server) http.HandlerFunc { return s.articleUnstarHandler }, false},
		{"feed inspector", post, "/debug" + feed + "/inspect", f, func(s *Server) http.HandlerFunc { return s.feedInspectHandler }, true},
		{"page inspector", post, "/debug" + article + "/inspect-page", a, func(s *Server) http.HandlerFunc { return s.articlePageInspectHandler }, true},
	}
}()

// TestNewsletterFeedOtherReader checks that another reader can't reach
// reader A's newsletter feed or its article by ID: every route is a 404,
// and nothing is written or fetched.
func TestNewsletterFeedOtherReader(t *testing.T) {
	for _, tt := range newsletterRoutes {
		t.Run(tt.name, func(t *testing.T) {
			s, writes := ownedFeedServer(t)
			rr, _ := serveAs(t, s, tt.h(s), tt.method, tt.target, tt.id, newsletterReaderB, tt.debug)
			assertNotFoundHTML(t, rr)
			if len(*writes) != 0 {
				t.Errorf("writes = %v, want none", *writes)
			}
		})
	}
}

// TestNewsletterFeedOwner checks what reader A gets for their own newsletter
// feed: its pages work without queuing a refresh, while Refresh and the
// inspectors refuse it without fetching.
func TestNewsletterFeedOwner(t *testing.T) {
	owner := debugSession.User.ID
	serve := func(t *testing.T, name string) (*httptest.ResponseRecorder, string, []string) {
		t.Helper()
		for _, tt := range newsletterRoutes {
			if tt.name == name {
				s, writes := ownedFeedServer(t)
				rr, flash := serveAs(t, s, tt.h(s), tt.method, tt.target, tt.id, owner, tt.debug)
				return rr, flash, *writes
			}
		}
		t.Fatalf("no route %q", name)
		return nil, "", nil
	}

	t.Run("feed page, without a refresh", func(t *testing.T) {
		rr, _, writes := serve(t, "feed page")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		if len(writes) != 0 {
			t.Errorf("writes = %v, want none (no RequestRefresh)", writes)
		}
	})

	t.Run("article page", func(t *testing.T) {
		rr, _, writes := serve(t, "article page")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		if want := []string{"MarkRead"}; strings.Join(writes, ",") != strings.Join(want, ",") {
			t.Errorf("writes = %v, want %v", writes, want)
		}
	})

	t.Run("save", func(t *testing.T) {
		rr, _, writes := serve(t, "save")
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
		}
		if want := []string{"Save"}; strings.Join(writes, ",") != strings.Join(want, ",") {
			t.Errorf("writes = %v, want %v", writes, want)
		}
	})

	t.Run("refresh is refused", func(t *testing.T) {
		rr, flash, writes := serve(t, "refresh")
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
		}
		if loc := rr.Header().Get("Location"); loc != "/feeds/"+newsletterFeedID {
			t.Errorf("Location = %q, want the feed page", loc)
		}
		if want := "Newsletters arrive by email, so they can't be refreshed."; flash != want {
			t.Errorf("flash = %q, want %q", flash, want)
		}
		if len(writes) != 0 {
			t.Errorf("writes = %v, want none (no Refresh)", writes)
		}
	})

	for _, name := range []string{"feed inspector", "page inspector"} {
		t.Run(name+" refuses it", func(t *testing.T) {
			rr, _, writes := serve(t, name)
			assertNotFoundHTML(t, rr)
			if len(writes) != 0 {
				t.Errorf("writes = %v, want none", writes)
			}
		})
	}
}

// TestNewsletterSite checks that the newsletters' internal site has no page
// and can't be discovered from, even with debug mode on.
func TestNewsletterSite(t *testing.T) {
	t.Run("site page", func(t *testing.T) {
		s, _ := ownedFeedServer(t)
		s.services.FeedService.(*fakeFeedStore).listBySiteFn = func(context.Context, string) ([]rss.Feed, error) {
			t.Error("ListBySite called")
			return nil, nil
		}
		rr, _ := serveAs(t, s, s.siteHandler, http.MethodGet, "/sites/"+newsletterSiteID, newsletterSiteID, debugSession.User.ID, false)
		assertNotFoundHTML(t, rr)
	})

	t.Run("discovery inspector", func(t *testing.T) {
		s, _ := ownedFeedServer(t)
		s.services.FeedService.(*fakeFeedStore).listBySiteFn = func(context.Context, string) ([]rss.Feed, error) {
			t.Error("ListBySite called")
			return nil, nil
		}
		rr, _ := serveAs(t, s, s.siteDiscoverHandler, http.MethodPost, "/debug/sites/"+newsletterSiteID+"/discover", newsletterSiteID, debugSession.User.ID, true)
		assertNotFoundHTML(t, rr)
	})
}
