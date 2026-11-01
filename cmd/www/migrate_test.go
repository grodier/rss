package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/catalog"
)

func TestRunMigratePrintFingerprint(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"migrate", "-print-fingerprint"}, noEnv, nil, &out, io.Discard); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got) {
		t.Errorf("printed %q, want a 64-character hex fingerprint", got)
	}
}

func TestRunMigrateRequiresDSN(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"migrate"}, noEnv, nil, io.Discard, &stderr); code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if want := "database DSN must not be empty"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}

type fakeCatalogStore struct {
	got []catalog.Entry
	res catalog.Result
	err error
}

func (f *fakeCatalogStore) Replace(_ context.Context, entries []catalog.Entry, _ time.Duration) (catalog.Result, error) {
	f.got = entries
	return f.res, f.err
}

func TestSyncCatalog(t *testing.T) {
	entries := catalog.Entries()

	t.Run("logs what it queued and the sites not found", func(t *testing.T) {
		store := &fakeCatalogStore{res: catalog.Result{Queued: 2, Retried: []string{"npr.org"}, Missing: []string{"bbc.co.uk"}}}
		var log bytes.Buffer
		if err := syncCatalog(t.Context(), store, entries, slog.New(slog.NewTextHandler(&log, nil))); err != nil {
			t.Fatal(err)
		}
		if len(store.got) != len(entries) {
			t.Errorf("stored %d entries; want %d", len(store.got), len(entries))
		}
		for _, want := range []string{
			fmt.Sprintf("entries=%d lookups_queued=2 lookups_retried=1", len(entries)),
			"site_keys=[npr.org]",
			"site_keys=[bbc.co.uk]",
		} {
			if !strings.Contains(log.String(), want) {
				t.Errorf("log missing %q:\n%s", want, log.String())
			}
		}
	})

	t.Run("a store error is returned", func(t *testing.T) {
		boom := errors.New("boom")
		err := syncCatalog(t.Context(), &fakeCatalogStore{err: boom}, entries, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if !errors.Is(err, boom) {
			t.Errorf("err = %v; want boom", err)
		}
	})
}
