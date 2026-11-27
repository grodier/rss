package psql

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

func TestInboundRepository(t *testing.T) {
	t.Parallel()
	db := psqltest.NewDB(t)
	users := NewUserRepository(db)
	repo := NewInboundRepository(db)

	// newReader makes a user, with newsletters when access is true.
	newReader := func(t *testing.T, access bool) string {
		t.Helper()
		email := fmt.Sprintf("test-inbound-%d@example.com", psqltest.Unique())
		t.Cleanup(func() {
			if _, err := db.Exec(`DELETE FROM users WHERE email = $1`, email); err != nil {
				t.Errorf("cleanup user: %v", err)
			}
		})
		id, _, err := users.Create(t.Context(), "Inbound Reader", email, "correct-horse-battery", nil)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		if access {
			if _, err := db.Exec(`INSERT INTO user_features (user_id, feature, source) VALUES ($1, 'newsletters', 'admin')`, id); err != nil {
				t.Fatalf("grant newsletters: %v", err)
			}
		}
		return id
	}
	// newAddress gives the user an address retired retiredAgo ago ("" for
	// a current one), and returns its ID and local part.
	newAddress := func(t *testing.T, userID, retiredAgo string) (string, string) {
		t.Helper()
		lp := fmt.Sprintf("inbound.t%d", psqltest.Unique())
		var retired any
		if retiredAgo != "" {
			retired = retiredAgo
		}
		var id string
		err := db.QueryRow(`INSERT INTO newsletter_addresses (user_id, local_part, retired_at)
			VALUES ($1, $2, now() - $3::interval) RETURNING id`, userID, lp, retired).Scan(&id)
		if err != nil {
			t.Fatalf("new address: %v", err)
		}
		return id, lp
	}
	stored := func(t *testing.T, addressID string) [][]byte {
		t.Helper()
		rows, err := db.Query(`SELECT raw, digest FROM inbound_emails WHERE address_id = $1 ORDER BY received_at`, addressID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var raws [][]byte
		for rows.Next() {
			var raw, digest []byte
			if err := rows.Scan(&raw, &digest); err != nil {
				t.Fatal(err)
			}
			if sum := sha256.Sum256(raw); !bytes.Equal(digest, sum[:]) {
				t.Errorf("digest = %x, want sha256 of raw %x", digest, sum)
			}
			raws = append(raws, raw)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return raws
	}

	t.Run("ResolveAddress", func(t *testing.T) {
		t.Parallel()
		reader := newReader(t, true)
		current, currentLP := newAddress(t, reader, "")
		recent, recentLP := newAddress(t, reader, "29 days")
		_, expiredLP := newAddress(t, reader, "31 days")
		lapsed := newReader(t, false)
		_, lapsedLP := newAddress(t, lapsed, "")

		tests := []struct {
			name, localPart     string
			wantAddress, wantID string
			wantErr             error
		}{
			{"current", currentLP, current, reader, nil},
			{"ignoring case", strings.ToUpper(currentLP), current, reader, nil},
			{"retired 29 days ago", recentLP, recent, reader, nil},
			{"retired 31 days ago", expiredLP, "", "", rss.ErrNoRecord},
			{"unknown", fmt.Sprintf("nobody.t%d", psqltest.Unique()), "", "", rss.ErrNoRecord},
			{"a reader without newsletters", lapsedLP, "", "", rss.ErrNoAccess},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				addressID, userID, err := repo.ResolveAddress(t.Context(), tt.localPart)
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if addressID != tt.wantAddress || userID != tt.wantID {
					t.Errorf("= %q, %q; want %q, %q", addressID, userID, tt.wantAddress, tt.wantID)
				}
			})
		}
	})

	t.Run("Store", func(t *testing.T) {
		t.Parallel()
		reader := newReader(t, true)
		current, _ := newAddress(t, reader, "")
		retired, _ := newAddress(t, reader, "1 day")
		raw := []byte("From: news@example.com\r\n\r\n\x00\xff")

		if err := repo.Store(t.Context(), current, reader, raw); err != nil {
			t.Fatalf("Store: %v", err)
		}
		if got := stored(t, current); len(got) != 1 || !bytes.Equal(got[0], raw) {
			t.Fatalf("stored %q, want exactly raw", got)
		}

		// A replay while the first copy waits.
		if err := repo.Store(t.Context(), current, reader, raw); !errors.Is(err, rss.ErrDuplicateInbound) {
			t.Errorf("replay: err = %v, want ErrDuplicateInbound", err)
		}
		// The same email to the reader's other address is another email.
		if err := repo.Store(t.Context(), retired, reader, raw); err != nil {
			t.Errorf("other address: %v", err)
		}
		if n := len(stored(t, current)); n != 1 {
			t.Errorf("current address has %d emails, want 1", n)
		}
	})

	t.Run("Store unknown user", func(t *testing.T) {
		t.Parallel()
		reader := newReader(t, true)
		address, _ := newAddress(t, reader, "")
		err := repo.Store(t.Context(), address, "00000000-0000-0000-0000-000000000000", []byte("x"))
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("err = %v, want ErrNoRecord", err)
		}
	})

	t.Run("Store caps a reader's waiting bytes", func(t *testing.T) {
		t.Parallel()
		reader := newReader(t, true)
		current, _ := newAddress(t, reader, "")
		retired, _ := newAddress(t, reader, "1 day")
		// One email waiting on the retired address fills all but 10 bytes
		// of the cap; it compresses, so it's cheap to store.
		_, err := db.Exec(`INSERT INTO inbound_emails (address_id, raw, digest)
			VALUES ($1, convert_to(repeat('a', $2), 'UTF8'), '\x00')`, retired, rss.InboundPendingMaxBytes-10)
		if err != nil {
			t.Fatal(err)
		}

		if err := repo.Store(t.Context(), current, reader, bytes.Repeat([]byte("b"), 11)); !errors.Is(err, rss.ErrInboundFull) {
			t.Errorf("over the cap: err = %v, want ErrInboundFull", err)
		}
		if err := repo.Store(t.Context(), current, reader, bytes.Repeat([]byte("b"), 10)); err != nil {
			t.Errorf("at the cap: %v", err)
		}

		// Another reader isn't affected.
		other := newReader(t, true)
		otherAddress, _ := newAddress(t, other, "")
		if err := repo.Store(t.Context(), otherAddress, other, []byte("hello")); err != nil {
			t.Errorf("another reader: %v", err)
		}
	})
}
