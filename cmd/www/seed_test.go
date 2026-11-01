package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/grodier/rss/internal/seed"
)

type fakeSeedStore struct {
	got []seed.Entry
	res seed.Result
	err error
}

func (f *fakeSeedStore) Queue(_ context.Context, entries []seed.Entry, _ time.Duration) (seed.Result, error) {
	f.got = entries
	return f.res, f.err
}

func TestSeedSites(t *testing.T) {
	entries := []seed.Entry{{URL: "https://npr.org/", SiteKey: "npr.org"}, {URL: "https://bbc.co.uk/", SiteKey: "bbc.co.uk"}}

	t.Run("logs what it queued and the sites not found", func(t *testing.T) {
		store := &fakeSeedStore{res: seed.Result{Queued: 2, Retried: []string{"npr.org"}, Missing: []string{"bbc.co.uk"}}}
		var log bytes.Buffer
		if err := seedSites(t.Context(), store, entries, slog.New(slog.NewTextHandler(&log, nil))); err != nil {
			t.Fatal(err)
		}
		if len(store.got) != len(entries) {
			t.Errorf("queued %d entries; want %d", len(store.got), len(entries))
		}
		for _, want := range []string{
			fmt.Sprintf("sites=%d lookups_queued=2 lookups_retried=1", len(entries)),
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
		err := seedSites(t.Context(), &fakeSeedStore{err: boom}, entries, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if !errors.Is(err, boom) {
			t.Errorf("err = %v; want boom", err)
		}
	})
}

func TestRunSeedRequiresDSN(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"seed"}, noEnv, nil, io.Discard, &stderr); code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if want := "database DSN must not be empty"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}
