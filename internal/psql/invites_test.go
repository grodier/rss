package psql

import (
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/psql/psqltest"
	"github.com/grodier/rss/internal/rss"
)

// newInvite stores inv with a new token and returns it with the token's
// hash; it's deleted when the test ends.
func newInvite(t *testing.T, db *sql.DB, inv rss.Invite) (rss.Invite, []byte) {
	t.Helper()
	hash := newWaitlistHash(t)
	if inv.ExpiresAt.IsZero() {
		inv.ExpiresAt = time.Now().Add(14 * 24 * time.Hour)
	}
	got, err := NewInviteRepository(db).Create(t.Context(), inv, hash, testActor(t, db))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM invites WHERE id = $1`, got.ID); err != nil {
			t.Errorf("cleanup invite: %v", err)
		}
	})
	return got, hash
}

func TestInviteRepository(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewInviteRepository(db)
	waitlist := NewWaitlistRepository(db)

	email := newWaitlistEmail(t, db)
	entry, err := waitlist.Join(t.Context(), rss.WaitlistEntry{Email: email, Name: "Ada"}, newWaitlistHash(t))
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	expires := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	bound, boundHash := newInvite(t, db, rss.Invite{Email: email, WaitlistID: entry.ID, Wave: "wave-1", ExpiresAt: expires})
	open, openHash := newInvite(t, db, rss.Invite{Note: "for Grace"})

	t.Run("Create returns the stored invite", func(t *testing.T) {
		if bound.ID == "" || bound.CreatedAt.IsZero() {
			t.Errorf("Create = %+v, want an ID and created_at", bound)
		}
		if bound.Email != email || bound.WaitlistID != entry.ID || bound.Name != "Ada" || bound.Wave != "wave-1" || !bound.ExpiresAt.Equal(expires) {
			t.Errorf("Create = %+v", bound)
		}
		if st := bound.Status(time.Now()); st != rss.InvitePending {
			t.Errorf("Status = %q, want pending", st)
		}
		if open.Email != "" || open.WaitlistID != "" || open.Name != "" || open.Note != "for Grace" {
			t.Errorf("open invite = %+v", open)
		}
	})

	t.Run("GetByToken", func(t *testing.T) {
		got, err := repo.GetByToken(t.Context(), boundHash)
		if err != nil {
			t.Fatalf("GetByToken: %v", err)
		}
		if got.ID != bound.ID || got.Name != "Ada" || got.Email != email {
			t.Errorf("GetByToken = %+v, want %+v", got, bound)
		}
		if _, err := repo.GetByToken(t.Context(), newWaitlistHash(t)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("GetByToken(unknown) error = %v, want ErrNoRecord", err)
		}
	})

	t.Run("List has both, newest first", func(t *testing.T) {
		all, err := repo.List(t.Context())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		ib := slices.IndexFunc(all, func(i rss.Invite) bool { return i.ID == bound.ID })
		io := slices.IndexFunc(all, func(i rss.Invite) bool { return i.ID == open.ID })
		if ib < 0 || io < 0 || io > ib {
			t.Errorf("List: bound at %d, open at %d; want both, open first", ib, io)
		}
	})

	t.Run("Revoke", func(t *testing.T) {
		if err := repo.Revoke(t.Context(), open.ID, testActor(t, db)); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		got, err := repo.GetByToken(t.Context(), openHash)
		if err != nil {
			t.Fatalf("GetByToken: %v", err)
		}
		if st := got.Status(time.Now()); st != rss.InviteRevoked {
			t.Errorf("Status = %q, want revoked", st)
		}
		// Revoking again keeps it revoked.
		if err := repo.Revoke(t.Context(), open.ID, testActor(t, db)); err != nil {
			t.Errorf("Revoke again: %v", err)
		}
		if err := repo.Revoke(t.Context(), "00000000-0000-0000-0000-000000000000", testActor(t, db)); !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("Revoke(unknown) error = %v, want ErrNoRecord", err)
		}
	})

	t.Run("leaving the waitlist unlinks the invite", func(t *testing.T) {
		inv, hash := newInvite(t, db, rss.Invite{Email: email, WaitlistID: entry.ID})
		if _, err := db.Exec(`DELETE FROM waitlist WHERE id = $1`, entry.ID); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetByToken(t.Context(), hash)
		if err != nil {
			t.Fatalf("GetByToken: %v", err)
		}
		if got.ID != inv.ID || got.WaitlistID != "" || got.Name != "" || got.Email != email {
			t.Errorf("GetByToken = %+v, want no entry and the email kept", got)
		}
	})
}

// testWave is a wave to entries, each with new tokens and an email.
func testWave(t *testing.T, label string, entries ...rss.WaitlistEntry) rss.Wave {
	t.Helper()
	w := rss.Wave{Label: label, ExpiresAt: time.Now().Add(14 * 24 * time.Hour).Truncate(time.Microsecond)}
	for _, e := range entries {
		w.Invites = append(w.Invites, rss.WaveInvite{
			WaitlistID: e.ID, Email: e.Email,
			InviteTokenHash: newWaitlistHash(t), WaitlistTokenHash: newWaitlistHash(t),
			Subject: "Your invite", Text: "Hi " + e.Name, HTML: "<p>Hi " + e.Name,
		})
	}
	return w
}

// fits is SendWave's available with room for n emails.
func fits(n int) func(rss.EmailUsage) int { return func(rss.EmailUsage) int { return n } }

// invitesTo returns the invites to email.
func invitesTo(t *testing.T, repo *InviteRepository, email string) []rss.Invite {
	t.Helper()
	all, err := repo.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(all, func(i rss.Invite) bool { return i.Email != email })
}

// emailsTo counts the emails queued to addr.
func emailsTo(t *testing.T, db *sql.DB, addr string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM emails WHERE to_address = $1`, addr).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSendWave(t *testing.T) {
	db := psqltest.NewDB(t)
	repo := NewInviteRepository(db)
	waitlist := NewWaitlistRepository(db)
	ctx := t.Context()
	joined := time.Now().Add(-time.Hour)

	t.Run("sends the invites, their emails and one audit entry", func(t *testing.T) {
		ada, grace := confirmedEntry(t, db, "Ada", joined), confirmedEntry(t, db, "Grace", joined)
		w := testWave(t, "Wave 1", ada, grace)
		by := testActor(t, db)
		before := todayCount(t, db)

		var usage rss.EmailUsage
		available := func(u rss.EmailUsage) int { usage = u; return 2 }
		if err := repo.SendWave(ctx, w, available, by); err != nil {
			t.Fatalf("SendWave: %v", err)
		}
		if now, err := NewEmailRepository(db).Usage(ctx, time.Now()); err != nil || usage.Last24h > now.Last24h-2 {
			t.Errorf("available was given %+v, want the usage before the wave (now %+v, %v)", usage, now, err)
		}

		for i, e := range []rss.WaitlistEntry{ada, grace} {
			got := invitesTo(t, repo, e.Email)
			if len(got) != 1 || got[0].WaitlistID != e.ID || got[0].Wave != "Wave 1" || !got[0].ExpiresAt.Equal(w.ExpiresAt) {
				t.Errorf("invites to %s = %+v", e.Name, got)
			}
			if inv, err := repo.GetByToken(ctx, w.Invites[i].InviteTokenHash); err != nil || inv.Email != e.Email {
				t.Errorf("GetByToken(%s's invite) = %+v, %v", e.Name, inv, err)
			}
			if got, err := waitlist.GetByToken(ctx, w.Invites[i].WaitlistTokenHash); err != nil || got.ID != e.ID {
				t.Errorf("%s's entry doesn't have its new link token: %+v, %v", e.Name, got, err)
			}
			if n := emailsTo(t, db, e.Email); n != 1 {
				t.Errorf("%d emails to %s, want 1", n, e.Name)
			}
		}
		if got := todayCount(t, db); got != before+2 {
			t.Errorf("today's count = %d, want %d", got, before+2)
		}
		events := auditBy(t, db, by)
		if len(events) != 1 || events[0].Action != "invite.wave" || events[0].After["wave"] != "Wave 1" || events[0].After["count"] != float64(2) {
			t.Fatalf("audit = %+v, want one invite.wave of 2", events)
		}
		ids, _ := events[0].After["waitlist_ids"].([]any)
		if len(ids) != 2 || ids[0] != ada.ID || ids[1] != grace.ID {
			t.Errorf("audit's waitlist_ids = %v", events[0].After["waitlist_ids"])
		}

		// Now invited, they can't be in another wave.
		err := repo.SendWave(ctx, testWave(t, "Again", ada), fits(10), by)
		if !errors.Is(err, rss.ErrNoRecord) {
			t.Errorf("SendWave to an invited entry error = %v, want ErrNoRecord", err)
		}
	})

	t.Run("over the limits sends nothing", func(t *testing.T) {
		ada, grace := confirmedEntry(t, db, "Ada", joined), confirmedEntry(t, db, "Grace", joined)
		by := testActor(t, db)
		if err := repo.SendWave(ctx, testWave(t, "Big", ada, grace), fits(1), by); !errors.Is(err, rss.ErrOverLimit) {
			t.Fatalf("SendWave error = %v, want ErrOverLimit", err)
		}
		for _, e := range []rss.WaitlistEntry{ada, grace} {
			if len(invitesTo(t, repo, e.Email)) != 0 || emailsTo(t, db, e.Email) != 0 {
				t.Errorf("something was sent to %s", e.Name)
			}
		}
		if len(auditBy(t, db, by)) != 0 {
			t.Error("the audit log has an entry")
		}
	})

	t.Run("an entry no longer waiting sends nothing", func(t *testing.T) {
		ada := confirmedEntry(t, db, "Ada", joined)
		left := confirmedEntry(t, db, "Left", joined)
		w := testWave(t, "W", ada, left)
		if _, err := db.Exec(`DELETE FROM waitlist WHERE id = $1`, left.ID); err != nil {
			t.Fatal(err)
		}
		if err := repo.SendWave(ctx, w, fits(10), testActor(t, db)); !errors.Is(err, rss.ErrNoRecord) {
			t.Fatalf("SendWave error = %v, want ErrNoRecord", err)
		}
		if len(invitesTo(t, repo, ada.Email)) != 0 || emailsTo(t, db, ada.Email) != 0 {
			t.Error("Ada was sent an invite")
		}
	})

	t.Run("a failed email sends nothing", func(t *testing.T) {
		ada := confirmedEntry(t, db, "Ada", joined)
		// Ada's address is at its hourly cap.
		for range maxEmailsPerAddressPerHour {
			if err := NewEmailRepository(db).Enqueue(ctx, mail.Message{To: ada.Email, Subject: "s"}); err != nil {
				t.Fatal(err)
			}
		}
		w := testWave(t, "W", ada)
		if err := repo.SendWave(ctx, w, fits(10), testActor(t, db)); !errors.Is(err, rss.ErrTooManyEmails) {
			t.Fatalf("SendWave error = %v, want ErrTooManyEmails", err)
		}
		if len(invitesTo(t, repo, ada.Email)) != 0 {
			t.Error("Ada's invite was stored")
		}
		if _, err := waitlist.GetByToken(ctx, w.Invites[0].WaitlistTokenHash); !errors.Is(err, rss.ErrNoRecord) {
			t.Error("Ada's entry got its new token")
		}
	})
}
