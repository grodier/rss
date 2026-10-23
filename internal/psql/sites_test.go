package psql

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// Requires a migrated database; see psqltest.NewDB.
func TestSiteRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSiteRepository(db)

	host := fmt.Sprintf("site-%d.example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM sites WHERE host = $1`, host); err != nil {
			t.Errorf("cleanup site: %v", err)
		}
	})

	want := rss.Site{
		Host:        host,
		URL:         "https://www." + host + "/",
		Title:       "Example",
		Description: "An example site",
	}
	id, err := repo.Upsert(t.Context(), want)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if id == "" {
		t.Fatal("Upsert returned an empty id")
	}

	check := func(t *testing.T, got rss.Site, want rss.Site) {
		t.Helper()
		if got.ID != id || got.Host != want.Host || got.URL != want.URL || got.Title != want.Title || got.Description != want.Description {
			t.Errorf("got %+v; want id %q and fields of %+v", got, id, want)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Errorf("got created_at %v, updated_at %v; want non-zero", got.CreatedAt, got.UpdatedAt)
		}
		if got.UpdatedAt.Before(got.CreatedAt) {
			t.Errorf("updated_at %v is before created_at %v", got.UpdatedAt, got.CreatedAt)
		}
	}

	t.Run("GetByID and GetByHost return the inserted site", func(t *testing.T) {
		got, err := repo.GetByID(t.Context(), id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		check(t, got, want)

		got, err = repo.GetByHost(t.Context(), host)
		if err != nil {
			t.Fatalf("GetByHost: %v", err)
		}
		check(t, got, want)
	})

	t.Run("Upsert same host updates fields", func(t *testing.T) {
		want.URL = "https://" + host + "/"
		want.Title = "New title"
		gotID, err := repo.Upsert(t.Context(), want)
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if gotID != id {
			t.Errorf("id = %q; want %q", gotID, id)
		}

		got, err := repo.GetByHost(t.Context(), host)
		if err != nil {
			t.Fatalf("GetByHost: %v", err)
		}
		check(t, got, want)
	})

	t.Run("Upsert with empty title and description keeps previous values", func(t *testing.T) {
		gotID, err := repo.Upsert(t.Context(), rss.Site{Host: host, URL: want.URL})
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if gotID != id {
			t.Errorf("id = %q; want %q", gotID, id)
		}

		got, err := repo.GetByID(t.Context(), id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		check(t, got, want)
	})

	t.Run("Feed with site scans SiteID", func(t *testing.T) {
		url := fmt.Sprintf("https://%s/feed-%d.xml", host, time.Now().UnixNano())
		t.Cleanup(func() {
			if _, err := db.Exec(`DELETE FROM feeds WHERE url = $1`, url); err != nil {
				t.Errorf("cleanup feed: %v", err)
			}
		})

		var feedID string
		if err := db.QueryRow(`INSERT INTO feeds (url, title, site_id) VALUES ($1, 't', $2) RETURNING id`, url, id).Scan(&feedID); err != nil {
			t.Fatalf("insert feed: %v", err)
		}

		feed, err := NewFeedRepository(db).GetByID(t.Context(), feedID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if feed.SiteID != id {
			t.Errorf("SiteID = %q; want %q", feed.SiteID, id)
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestSiteRepositoryMissing(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSiteRepository(db)

	if _, err := repo.GetByID(t.Context(), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("GetByID err = %v; want rss.ErrNoRecord", err)
	}

	host := fmt.Sprintf("missing-%d.example.com", time.Now().UnixNano())
	if _, err := repo.GetByHost(t.Context(), host); !errors.Is(err, rss.ErrNoRecord) {
		t.Errorf("GetByHost err = %v; want rss.ErrNoRecord", err)
	}
}

// setIconCheck sets site id's icon_checked_at (NULL if checked is zero) and
// icon_next_check_at.
func setIconCheck(t *testing.T, db *sql.DB, id string, checked, next time.Time) {
	t.Helper()
	var checkedAt sql.NullTime
	if !checked.IsZero() {
		checkedAt = sql.NullTime{Time: checked, Valid: true}
	}
	if _, err := db.Exec(`UPDATE sites SET icon_checked_at = $2, icon_next_check_at = $3 WHERE id = $1`, id, checkedAt, next); err != nil {
		t.Fatalf("set icon check: %v", err)
	}
}

// iconCheck returns site id's icon_checked_at (zero if NULL) and
// icon_next_check_at.
func iconCheck(t *testing.T, db *sql.DB, id string) (checked, next time.Time) {
	t.Helper()
	var checkedAt sql.NullTime
	if err := db.QueryRow(`SELECT icon_checked_at, icon_next_check_at FROM sites WHERE id = $1`, id).Scan(&checkedAt, &next); err != nil {
		t.Fatalf("select icon check: %v", err)
	}
	return checkedAt.Time, next
}

// iconCount returns how many site_icons rows site id has.
func iconCount(t *testing.T, db *sql.DB, id string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM site_icons WHERE site_id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count site_icons: %v", err)
	}
	return n
}

// Requires a migrated database; see psqltest.NewDB.
func TestSiteRepositoryClaimIconCheck(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSiteRepository(db)
	userID := newTestUser(t, db)
	const lease = 10 * time.Minute
	// Other tests share the database, so make this test's sites due longest.
	longAgo := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	// claim claims a site and fails unless it is want.
	claim := func(t *testing.T, want string) (rss.Site, rss.SiteIcon) {
		t.Helper()
		site, icon, err := repo.ClaimIconCheck(t.Context(), lease)
		if err != nil {
			t.Fatalf("ClaimIconCheck: %v", err)
		}
		if site.ID != want {
			t.Fatalf("claimed %q; want %q", site.ID, want)
		}
		return site, icon
	}

	t.Run("takes a never-checked site and leases it", func(t *testing.T) {
		id := newTestSite(t, db)
		setIconCheck(t, db, id, time.Time{}, longAgo)

		before := dbNow(t, db)
		site, icon := claim(t, id)
		if site.Host == "" || site.URL == "" {
			t.Errorf("claimed site %+v; want its host and URL", site)
		}
		if icon.SiteID != "" || icon.SourceURL != "" || icon.ETag != "" || icon.LastModified != "" {
			t.Errorf("icon = %+v; want zero", icon)
		}
		if _, next := iconCheck(t, db, id); next.Before(before.Add(lease)) || next.After(dbNow(t, db).Add(lease)) {
			t.Errorf("icon_next_check_at %v; want about now + %v", next, lease)
		}
	})

	t.Run("returns the current icon's validators", func(t *testing.T) {
		id := newTestSite(t, db)
		icon := rss.SiteIcon{SiteID: id, SourceURL: "https://example.com/touch.png", ContentType: "image/png",
			Width: 180, Height: 180, Data: []byte("png"), Hash: "abc", ETag: `"v1"`, LastModified: "Mon, 02 Jan 2006 15:04:05 GMT"}
		if err := repo.SaveIcon(t.Context(), icon, longAgo); err != nil {
			t.Fatalf("SaveIcon: %v", err)
		}
		setIconCheck(t, db, id, time.Time{}, longAgo)

		_, got := claim(t, id)
		want := rss.SiteIcon{SiteID: id, SourceURL: icon.SourceURL, ETag: icon.ETag, LastModified: icon.LastModified}
		if got.SiteID != want.SiteID || got.SourceURL != want.SourceURL || got.ETag != want.ETag || got.LastModified != want.LastModified || got.Data != nil {
			t.Errorf("icon = %+v; want %+v", got, want)
		}
	})

	t.Run("skips a checked site with no subscriptions", func(t *testing.T) {
		checked, fresh := newTestSite(t, db), newTestSite(t, db)
		setIconCheck(t, db, checked, longAgo, longAgo.Add(-time.Hour))
		setIconCheck(t, db, fresh, time.Time{}, longAgo)

		claim(t, fresh)
	})

	t.Run("takes a checked, subscribed site that's due", func(t *testing.T) {
		feed := newSubscribedFeed(t, db, userID)
		setIconCheck(t, db, feed.SiteID, longAgo, longAgo)

		claim(t, feed.SiteID)
	})

	t.Run("skips a site whose check isn't due", func(t *testing.T) {
		later, due := newTestSite(t, db), newTestSite(t, db)
		setIconCheck(t, db, later, time.Time{}, time.Now().Add(time.Hour))
		setIconCheck(t, db, due, time.Time{}, longAgo)

		claim(t, due)
		if _, next := iconCheck(t, db, later); next.Before(time.Now().Add(50 * time.Minute)) {
			t.Errorf("not-due site's icon_next_check_at moved to %v", next)
		}
	})

	t.Run("two claims return different sites", func(t *testing.T) {
		var ids []string
		for range 2 {
			id := newTestSite(t, db)
			setIconCheck(t, db, id, time.Time{}, longAgo)
			ids = append(ids, id)
		}

		var wg sync.WaitGroup
		got := make([]rss.Site, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() {
				got[i], _, errs[i] = repo.ClaimIconCheck(t.Context(), lease)
			})
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("ClaimIconCheck %d: %v", i, err)
			}
		}
		if got[0].ID == got[1].ID {
			t.Errorf("both claims returned %q", got[0].ID)
		}
		// Hand back anything claimed that this test didn't create, so other
		// tests' sites aren't left leased.
		for _, s := range got {
			if s.ID != ids[0] && s.ID != ids[1] {
				if _, err := db.Exec(`UPDATE sites SET icon_next_check_at = now() WHERE id = $1`, s.ID); err != nil {
					t.Errorf("hand back site: %v", err)
				}
			}
		}
	})
}

// Requires a migrated database; see psqltest.NewDB.
func TestSiteRepositoryIcons(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewSiteRepository(db)
	next := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Microsecond)
	missing := "00000000-0000-0000-0000-000000000000"

	getIcon := func(t *testing.T, id string) rss.SiteIcon {
		t.Helper()
		var i rss.SiteIcon
		err := db.QueryRow(`SELECT site_id, source_url, content_type, width, height, data, hash, etag, last_modified, fetched_at
			FROM site_icons WHERE site_id = $1`, id).Scan(
			&i.SiteID, &i.SourceURL, &i.ContentType, &i.Width, &i.Height, &i.Data, &i.Hash, &i.ETag, &i.LastModified, &i.FetchedAt)
		if err != nil {
			t.Fatalf("select site_icons: %v", err)
		}
		return i
	}

	// wantChecked fails unless site id's icon was just checked and is next
	// checked at next.
	wantChecked := func(t *testing.T, id string, before time.Time) {
		t.Helper()
		checked, gotNext := iconCheck(t, db, id)
		if checked.Before(before) || checked.After(dbNow(t, db)) {
			t.Errorf("icon_checked_at %v; want now", checked)
		}
		if !gotNext.Equal(next) {
			t.Errorf("icon_next_check_at %v; want %v", gotNext, next)
		}
	}

	icon := func(id string, n int) rss.SiteIcon {
		return rss.SiteIcon{
			SiteID: id, SourceURL: fmt.Sprintf("https://example.com/icon-%d.png", n), ContentType: "image/png",
			Width: 32 * n, Height: 32 * n, Data: bytes.Repeat([]byte{byte(n)}, 10), Hash: fmt.Sprintf("hash-%d", n),
			ETag: fmt.Sprintf(`"v%d"`, n), LastModified: "Mon, 02 Jan 2006 15:04:05 GMT",
		}
	}
	wantIcon := func(t *testing.T, got, want rss.SiteIcon) {
		t.Helper()
		if got.SiteID != want.SiteID || got.SourceURL != want.SourceURL || got.ContentType != want.ContentType ||
			got.Width != want.Width || got.Height != want.Height || !bytes.Equal(got.Data, want.Data) ||
			got.Hash != want.Hash || got.ETag != want.ETag || got.LastModified != want.LastModified || got.FetchedAt.IsZero() {
			t.Errorf("stored icon %+v; want %+v", got, want)
		}
	}

	t.Run("SaveIcon inserts, then replaces", func(t *testing.T) {
		id := newTestSite(t, db)
		before := dbNow(t, db)
		if err := repo.SaveIcon(t.Context(), icon(id, 1), next); err != nil {
			t.Fatalf("SaveIcon: %v", err)
		}
		wantIcon(t, getIcon(t, id), icon(id, 1))
		wantChecked(t, id, before)

		if err := repo.SaveIcon(t.Context(), icon(id, 2), next); err != nil {
			t.Fatalf("SaveIcon again: %v", err)
		}
		wantIcon(t, getIcon(t, id), icon(id, 2))
		if n := iconCount(t, db, id); n != 1 {
			t.Errorf("site has %d icons; want 1", n)
		}
	})

	t.Run("RemoveIcon deletes", func(t *testing.T) {
		id := newTestSite(t, db)
		if err := repo.SaveIcon(t.Context(), icon(id, 1), next); err != nil {
			t.Fatalf("SaveIcon: %v", err)
		}
		setIconCheck(t, db, id, time.Time{}, time.Now())

		before := dbNow(t, db)
		if err := repo.RemoveIcon(t.Context(), id, next); err != nil {
			t.Fatalf("RemoveIcon: %v", err)
		}
		if n := iconCount(t, db, id); n != 0 {
			t.Errorf("site has %d icons; want 0", n)
		}
		wantChecked(t, id, before)

		// A site without an icon is still recorded as checked.
		if err := repo.RemoveIcon(t.Context(), id, next); err != nil {
			t.Errorf("RemoveIcon without an icon: %v", err)
		}
	})

	t.Run("KeepIcon checked", func(t *testing.T) {
		id := newTestSite(t, db)
		if err := repo.SaveIcon(t.Context(), icon(id, 1), time.Now()); err != nil {
			t.Fatalf("SaveIcon: %v", err)
		}
		setIconCheck(t, db, id, time.Time{}, time.Now())

		before := dbNow(t, db)
		if err := repo.KeepIcon(t.Context(), id, true, next); err != nil {
			t.Fatalf("KeepIcon: %v", err)
		}
		wantChecked(t, id, before)
		wantIcon(t, getIcon(t, id), icon(id, 1))
	})

	t.Run("KeepIcon not checked", func(t *testing.T) {
		id := newTestSite(t, db)
		if err := repo.SaveIcon(t.Context(), icon(id, 1), time.Now()); err != nil {
			t.Fatalf("SaveIcon: %v", err)
		}
		lastChecked := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		setIconCheck(t, db, id, lastChecked, time.Now())

		if err := repo.KeepIcon(t.Context(), id, false, next); err != nil {
			t.Fatalf("KeepIcon: %v", err)
		}
		checked, gotNext := iconCheck(t, db, id)
		if !checked.Equal(lastChecked) {
			t.Errorf("icon_checked_at %v; want unchanged %v", checked, lastChecked)
		}
		if !gotNext.Equal(next) {
			t.Errorf("icon_next_check_at %v; want %v", gotNext, next)
		}
		wantIcon(t, getIcon(t, id), icon(id, 1))
	})

	t.Run("GetIcon", func(t *testing.T) {
		id := newTestSite(t, db)
		if _, err := repo.GetIcon(t.Context(), id); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetIcon without an icon: err = %v; want rss.ErrNoRecord", err)
		}

		if err := repo.SaveIcon(t.Context(), icon(id, 2), next); err != nil {
			t.Fatalf("SaveIcon: %v", err)
		}
		got, err := repo.GetIcon(t.Context(), id)
		if err != nil {
			t.Fatalf("GetIcon: %v", err)
		}
		wantIcon(t, got, icon(id, 2))
	})

	t.Run("IconHashes", func(t *testing.T) {
		with1, with2, without := newTestSite(t, db), newTestSite(t, db), newTestSite(t, db)
		if err := repo.SaveIcon(t.Context(), icon(with1, 1), next); err != nil {
			t.Fatalf("SaveIcon: %v", err)
		}
		if err := repo.SaveIcon(t.Context(), icon(with2, 2), next); err != nil {
			t.Fatalf("SaveIcon: %v", err)
		}

		got, err := repo.IconHashes(t.Context(), []string{with1, without, with2, with1, missing})
		if err != nil {
			t.Fatalf("IconHashes: %v", err)
		}
		want := map[string]string{with1: "hash-1", with2: "hash-2"}
		if !maps.Equal(got, want) {
			t.Errorf("IconHashes = %v; want %v", got, want)
		}

		got, err = repo.IconHashes(t.Context(), nil)
		if err != nil || len(got) != 0 {
			t.Errorf("IconHashes(nil) = %v, %v; want empty, nil", got, err)
		}
	})

	t.Run("missing site", func(t *testing.T) {
		if err := repo.SaveIcon(t.Context(), icon(missing, 1), next); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("SaveIcon err = %v; want rss.ErrNoRecord", err)
		}
		if err := repo.RemoveIcon(t.Context(), missing, next); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("RemoveIcon err = %v; want rss.ErrNoRecord", err)
		}
		if err := repo.KeepIcon(t.Context(), missing, true, next); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("KeepIcon err = %v; want rss.ErrNoRecord", err)
		}
	})

	t.Run("deleting the site cascades", func(t *testing.T) {
		id := newTestSite(t, db)
		if err := repo.SaveIcon(t.Context(), icon(id, 1), next); err != nil {
			t.Fatalf("SaveIcon: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM sites WHERE id = $1`, id); err != nil {
			t.Fatalf("delete site: %v", err)
		}
		if n := iconCount(t, db, id); n != 0 {
			t.Errorf("deleted site has %d icons; want 0", n)
		}
	})
}
