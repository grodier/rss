package psql

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

const testLease = 2 * time.Minute

// newEmailAddress returns a unique address whose emails are deleted when the
// test ends.
func newEmailAddress(t *testing.T, repo *EmailRepository) string {
	t.Helper()
	addr := fmt.Sprintf("em-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := repo.DB.Exec(`DELETE FROM emails WHERE to_address = $1`, addr); err != nil {
			t.Errorf("cleanup emails: %v", err)
		}
	})
	return addr
}

// emailRow is an emails row as the tests check it.
type emailRow struct {
	status        string
	textBody      string
	htmlBody      string
	attempts      int
	nextAttemptAt time.Time
	lastError     string
	sentAt        *time.Time
}

func getEmail(t *testing.T, repo *EmailRepository, id string) emailRow {
	t.Helper()
	var e emailRow
	err := repo.DB.QueryRow(`SELECT status, text_body, html_body, attempts, next_attempt_at, last_error, sent_at
		FROM emails WHERE id = $1`, id).
		Scan(&e.status, &e.textBody, &e.htmlBody, &e.attempts, &e.nextAttemptAt, &e.lastError, &e.sentAt)
	if err != nil {
		t.Fatalf("get email %s: %v", id, err)
	}
	return e
}

// claimOwn enqueues a message to addr and claims it.
func claimOwn(t *testing.T, repo *EmailRepository, addr string) mail.Queued {
	t.Helper()
	m := mail.Message{To: addr, Subject: "Subject", Text: "text body", HTML: "<p>html body</p>"}
	if err := repo.Enqueue(t.Context(), m); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	dueFirst(t, repo, addr)

	q, err := repo.ClaimNext(t.Context(), testLease)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if q.Message.To != addr {
		t.Fatalf("ClaimNext claimed an email to %q, want %q", q.Message.To, addr)
	}
	return q
}

// dueFirst makes addr's pending emails due before any other, which a database
// shared with the app (make test/db) may also hold.
func dueFirst(t *testing.T, repo *EmailRepository, addr string) {
	t.Helper()
	execEmailSQL(t, repo, `UPDATE emails SET next_attempt_at = '-infinity' WHERE to_address = $1 AND status = 'pending'`, addr)
}

func execEmailSQL(t *testing.T, repo *EmailRepository, stmt string, args ...any) {
	t.Helper()
	if _, err := repo.DB.Exec(stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

// Requires a migrated database; see psqltest.NewDB.
func TestEmailRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewEmailRepository(db)

	t.Run("Enqueue then ClaimNext returns the email, leased", func(t *testing.T) {
		addr := newEmailAddress(t, repo)
		m := mail.Message{To: addr, Subject: "Subject", Text: "text body", HTML: "<p>html body</p>"}
		if err := repo.Enqueue(t.Context(), m); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		dueFirst(t, repo, addr)

		before := time.Now()
		q, err := repo.ClaimNext(t.Context(), testLease)
		if err != nil {
			t.Fatalf("ClaimNext: %v", err)
		}
		if q.ID == "" || q.Message != m || q.Attempts != 1 {
			t.Errorf("ClaimNext = %+v, want %+v with 1 attempt", q, m)
		}
		e := getEmail(t, repo, q.ID)
		if e.status != "pending" || e.attempts != 1 {
			t.Errorf("row = %+v, want pending with 1 attempt", e)
		}
		if want := before.Add(testLease); e.nextAttemptAt.Before(want.Add(-5*time.Second)) || e.nextAttemptAt.After(want.Add(5*time.Second)) {
			t.Errorf("next_attempt_at is %s from now, want about the lease (%s)", e.nextAttemptAt.Sub(before), testLease)
		}

		// Leased, so no worker claims it again until the lease runs out.
		again, err := repo.ClaimNext(t.Context(), testLease)
		switch {
		case errors.Is(err, rss.ErrNoRecord):
		case err != nil:
			t.Fatalf("ClaimNext again: %v", err)
		case again.ID == q.ID:
			t.Fatal("ClaimNext claimed the leased email again")
		default:
			t.Fatalf("ClaimNext again = %+v, want ErrNoRecord", again)
		}
	})

	t.Run("MarkSent sets sent and clears the bodies", func(t *testing.T) {
		addr := newEmailAddress(t, repo)
		q := claimOwn(t, repo, addr)

		if err := repo.MarkSent(t.Context(), q.ID); err != nil {
			t.Fatalf("MarkSent: %v", err)
		}
		e := getEmail(t, repo, q.ID)
		if e.status != "sent" || e.sentAt == nil || e.textBody != "" || e.htmlBody != "" {
			t.Errorf("row = %+v, want sent with sent_at and no bodies", e)
		}
	})

	t.Run("MarkFailed keeps it pending until the retry time", func(t *testing.T) {
		addr := newEmailAddress(t, repo)
		q := claimOwn(t, repo, addr)
		retryAt := time.Now().Add(4 * time.Minute).Truncate(time.Microsecond)

		if err := repo.MarkFailed(t.Context(), q.ID, "421 try again later", retryAt); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
		e := getEmail(t, repo, q.ID)
		if e.status != "pending" || !e.nextAttemptAt.Equal(retryAt) || e.lastError != "421 try again later" {
			t.Errorf("row = %+v, want pending until %s with the error", e, retryAt)
		}
		if e.textBody == "" || e.htmlBody == "" || e.sentAt != nil {
			t.Errorf("row = %+v, want the bodies kept for the retry", e)
		}
	})

	t.Run("MarkFailed truncates long errors", func(t *testing.T) {
		addr := newEmailAddress(t, repo)
		q := claimOwn(t, repo, addr)

		if err := repo.MarkFailed(t.Context(), q.ID, strings.Repeat("é", maxErrorLen), time.Now()); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
		if e := getEmail(t, repo, q.ID); len(e.lastError) > maxErrorLen {
			t.Errorf("last_error is %d bytes, want at most %d", len(e.lastError), maxErrorLen)
		}
	})

	t.Run("GiveUp sets failed and clears the bodies", func(t *testing.T) {
		addr := newEmailAddress(t, repo)
		q := claimOwn(t, repo, addr)

		if err := repo.GiveUp(t.Context(), q.ID, "550 no such user"); err != nil {
			t.Fatalf("GiveUp: %v", err)
		}
		e := getEmail(t, repo, q.ID)
		if e.status != "failed" || e.lastError != "550 no such user" || e.textBody != "" || e.htmlBody != "" || e.sentAt != nil {
			t.Errorf("row = %+v, want failed with the error and no bodies", e)
		}
	})

	t.Run("updates of a missing email are ErrNoRecord", func(t *testing.T) {
		if err := repo.MarkSent(t.Context(), missingID); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("MarkSent error = %v, want ErrNoRecord", err)
		}
		if err := repo.MarkFailed(t.Context(), missingID, "x", time.Now()); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("MarkFailed error = %v, want ErrNoRecord", err)
		}
		if err := repo.GiveUp(t.Context(), missingID, "x"); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GiveUp error = %v, want ErrNoRecord", err)
		}
	})

	t.Run("DeleteFinished deletes old sent and failed emails, never pending", func(t *testing.T) {
		addr := newEmailAddress(t, repo)
		for _, status := range []string{"sent", "failed", "pending"} {
			for _, age := range []string{"8 days", "1 day"} {
				execEmailSQL(t, repo, `INSERT INTO emails (to_address, subject, text_body, html_body, status, created_at)
					VALUES ($1, $2, '', '', $3, now() - $4::interval)`, addr, status+" "+age, status, age)
			}
		}

		n, err := repo.DeleteFinished(t.Context(), time.Now().Add(-7*24*time.Hour))
		if err != nil {
			t.Fatalf("DeleteFinished: %v", err)
		}
		if n < 2 {
			t.Errorf("DeleteFinished deleted %d rows, want at least this test's 2", n)
		}

		rows, err := repo.DB.Query(`SELECT subject FROM emails WHERE to_address = $1 ORDER BY subject`, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var left []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			left = append(left, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		want := []string{"failed 1 day", "pending 1 day", "pending 8 days", "sent 1 day"}
		if fmt.Sprint(left) != fmt.Sprint(want) {
			t.Errorf("left %v, want %v", left, want)
		}
	})

	t.Run("at most 20 emails to an address an hour, ignoring case", func(t *testing.T) {
		addr := newEmailAddress(t, repo)
		other := newEmailAddress(t, repo)
		m := mail.Message{To: addr, Subject: "Subject", Text: "text", HTML: "html"}

		for i := range maxEmailsPerAddressPerHour {
			if err := repo.Enqueue(t.Context(), m); err != nil {
				t.Fatalf("Enqueue %d: %v", i+1, err)
			}
		}
		m.To = strings.ToUpper(addr)
		if err := repo.Enqueue(t.Context(), m); !errors.Is(err, rss.ErrTooManyEmails) {
			t.Fatalf("Enqueue 21 error = %v, want ErrTooManyEmails", err)
		}
		var n int
		if err := repo.DB.QueryRow(`SELECT count(*) FROM emails WHERE to_address = $1`, addr).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != maxEmailsPerAddressPerHour {
			t.Errorf("%d emails queued, want %d", n, maxEmailsPerAddressPerHour)
		}

		m.To = other
		if err := repo.Enqueue(t.Context(), m); err != nil {
			t.Errorf("Enqueue to another address: %v", err)
		}

		// Emails older than an hour don't count.
		execEmailSQL(t, repo, `UPDATE emails SET created_at = now() - interval '61 minutes' WHERE to_address = $1`, addr)
		m.To = addr
		if err := repo.Enqueue(t.Context(), m); err != nil {
			t.Errorf("Enqueue after an hour: %v", err)
		}
	})

	t.Run("concurrent ClaimNext calls never return the same email", func(t *testing.T) {
		addr := newEmailAddress(t, repo)
		const count = 10
		for i := range count {
			m := mail.Message{To: addr, Subject: fmt.Sprint(i), Text: "text", HTML: "html"}
			if err := repo.Enqueue(t.Context(), m); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
		}
		dueFirst(t, repo, addr)

		var (
			mu      sync.Mutex
			claimed = map[string]int{}
			wg      sync.WaitGroup
		)
		for range count {
			wg.Add(1)
			go func() {
				defer wg.Done()
				q, err := repo.ClaimNext(t.Context(), testLease)
				if err != nil {
					t.Errorf("ClaimNext: %v", err)
					return
				}
				if q.Message.To != addr {
					t.Errorf("claimed an email to %q, want %q", q.Message.To, addr)
				}
				mu.Lock()
				claimed[q.ID]++
				mu.Unlock()
			}()
		}
		wg.Wait()

		for id, n := range claimed {
			if n != 1 {
				t.Errorf("email %s claimed %d times, want once", id, n)
			}
		}
		if len(claimed) != count {
			t.Errorf("claimed %d distinct emails, want %d", len(claimed), count)
		}
	})
}
