//go:build integration

package pgsql

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/grodier/rss/internal/auth"
	"github.com/grodier/rss/internal/domain"
	_ "github.com/lib/pq"
)

const testDSN = "postgres://rssapp:dev-password@localhost:5432/rssapp_dev?sslmode=disable"

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", testDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("database not reachable: %v (is 'make db/start' running?)", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testTx(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	return tx
}

type testFixture struct {
	user       domain.User
	account    domain.Account
	membership domain.Membership
}

func createFixture(t *testing.T, tx *sql.Tx) testFixture {
	t.Helper()
	userID := uuid.New()
	accountID := uuid.New()
	displayName := "Test User"
	accountName := "Test Account"

	var user domain.User
	err := tx.QueryRow(
		`INSERT INTO users (id, display_name) VALUES ($1, $2)
		RETURNING id, display_name, created_at, deleted_at, purge_after`,
		userID, displayName,
	).Scan(&user.ID, &user.DisplayName, &user.CreatedAt, &user.DeletedAt, &user.PurgeAfter)
	if err != nil {
		t.Fatal(err)
	}

	var account domain.Account
	err = tx.QueryRow(
		`INSERT INTO accounts (id, name) VALUES ($1, $2)
		RETURNING id, name, created_at, deleted_at, purge_after`,
		accountID, accountName,
	).Scan(&account.ID, &account.Name, &account.CreatedAt, &account.DeletedAt, &account.PurgeAfter)
	if err != nil {
		t.Fatal(err)
	}

	var membership domain.Membership
	err = tx.QueryRow(
		`INSERT INTO memberships (user_id, account_id, role) VALUES ($1, $2, $3)
		RETURNING user_id, account_id, role, created_at, last_used_at`,
		userID, accountID, domain.RoleOwner,
	).Scan(&membership.UserID, &membership.AccountID, &membership.Role, &membership.CreatedAt, &membership.LastUsedAt)
	if err != nil {
		t.Fatal(err)
	}

	return testFixture{user: user, account: account, membership: membership}
}

func TestGetUserByID(t *testing.T) {
	db := testDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	t.Run("found", func(t *testing.T) {
		tx := testTx(t, db)
		f := createFixture(t, tx)

		got, err := repo.GetUserByID(ctx, tx, f.user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != f.user.ID {
			t.Errorf("got ID %v, want %v", got.ID, f.user.ID)
		}
		if got.DisplayName == nil || *got.DisplayName != *f.user.DisplayName {
			t.Errorf("got DisplayName %v, want %v", got.DisplayName, f.user.DisplayName)
		}
	})

	t.Run("not found", func(t *testing.T) {
		tx := testTx(t, db)

		_, err := repo.GetUserByID(ctx, tx, uuid.New())
		if !errors.Is(err, auth.ErrUserNotFound) {
			t.Errorf("got err %v, want %v", err, auth.ErrUserNotFound)
		}
	})

	t.Run("soft-deleted excluded", func(t *testing.T) {
		tx := testTx(t, db)
		f := createFixture(t, tx)

		_, err := tx.Exec(
			`UPDATE users SET deleted_at = now() WHERE id = $1`, f.user.ID,
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = repo.GetUserByID(ctx, tx, f.user.ID)
		if !errors.Is(err, auth.ErrUserNotFound) {
			t.Errorf("got err %v, want %v", err, auth.ErrUserNotFound)
		}
	})
}

func TestUpdateUserDisplayName(t *testing.T) {
	db := testDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()
	tx := testTx(t, db)
	f := createFixture(t, tx)

	newName := "Updated Name"
	got, err := repo.UpdateUserDisplayName(ctx, tx, f.user.ID, &newName)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != f.user.ID {
		t.Errorf("got ID %v, want %v", got.ID, f.user.ID)
	}
	if got.DisplayName == nil || *got.DisplayName != newName {
		t.Errorf("got DisplayName %v, want %q", got.DisplayName, newName)
	}
}

func TestSoftDeleteUser(t *testing.T) {
	db := testDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()
	tx := testTx(t, db)
	f := createFixture(t, tx)

	if err := repo.SoftDeleteUser(ctx, tx, f.user.ID); err != nil {
		t.Fatal(err)
	}

	var deletedAt, purgeAfter sql.NullTime
	err := tx.QueryRow(
		`SELECT deleted_at, purge_after FROM users WHERE id = $1`, f.user.ID,
	).Scan(&deletedAt, &purgeAfter)
	if err != nil {
		t.Fatal(err)
	}
	if !deletedAt.Valid {
		t.Error("deleted_at should be set")
	}
	if !purgeAfter.Valid {
		t.Error("purge_after should be set")
	}
}

func TestSoftDeleteAccount(t *testing.T) {
	db := testDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()
	tx := testTx(t, db)
	f := createFixture(t, tx)

	if err := repo.SoftDeleteAccount(ctx, tx, f.account.ID); err != nil {
		t.Fatal(err)
	}

	var deletedAt, purgeAfter sql.NullTime
	err := tx.QueryRow(
		`SELECT deleted_at, purge_after FROM accounts WHERE id = $1`, f.account.ID,
	).Scan(&deletedAt, &purgeAfter)
	if err != nil {
		t.Fatal(err)
	}
	if !deletedAt.Valid {
		t.Error("deleted_at should be set")
	}
	if !purgeAfter.Valid {
		t.Error("purge_after should be set")
	}
}

func TestListMembershipsByUserID(t *testing.T) {
	db := testDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()
	tx := testTx(t, db)
	f := createFixture(t, tx)

	secondAccountID := uuid.New()
	_, err := tx.Exec(
		`INSERT INTO accounts (id, name) VALUES ($1, $2)`,
		secondAccountID, "Second Account",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(
		`INSERT INTO memberships (user_id, account_id, role) VALUES ($1, $2, $3)`,
		f.user.ID, secondAccountID, domain.RoleMember,
	)
	if err != nil {
		t.Fatal(err)
	}

	memberships, err := repo.ListMembershipsByUserID(ctx, tx, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(memberships) != 2 {
		t.Fatalf("got %d memberships, want 2", len(memberships))
	}
}

func TestGetPrimaryMembership(t *testing.T) {
	db := testDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	t.Run("returns most recently used", func(t *testing.T) {
		tx := testTx(t, db)
		f := createFixture(t, tx)

		secondAccountID := uuid.New()
		_, err := tx.Exec(
			`INSERT INTO accounts (id, name) VALUES ($1, $2)`,
			secondAccountID, "Second Account",
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(
			`INSERT INTO memberships (user_id, account_id, role, last_used_at)
			VALUES ($1, $2, $3, now() + interval '1 hour')`,
			f.user.ID, secondAccountID, domain.RoleMember,
		)
		if err != nil {
			t.Fatal(err)
		}

		got, err := repo.GetPrimaryMembership(ctx, tx, f.user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.AccountID != secondAccountID {
			t.Errorf("got AccountID %v, want %v", got.AccountID, secondAccountID)
		}
	})

	t.Run("falls back to owner role", func(t *testing.T) {
		tx := testTx(t, db)
		f := createFixture(t, tx)

		secondAccountID := uuid.New()
		_, err := tx.Exec(
			`INSERT INTO accounts (id, name) VALUES ($1, $2)`,
			secondAccountID, "Second Account",
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(
			`INSERT INTO memberships (user_id, account_id, role) VALUES ($1, $2, $3)`,
			f.user.ID, secondAccountID, domain.RoleMember,
		)
		if err != nil {
			t.Fatal(err)
		}

		got, err := repo.GetPrimaryMembership(ctx, tx, f.user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Role != domain.RoleOwner {
			t.Errorf("got Role %q, want %q", got.Role, domain.RoleOwner)
		}
		if got.AccountID != f.account.ID {
			t.Errorf("got AccountID %v, want %v", got.AccountID, f.account.ID)
		}
	})
}

func TestUpdateMembershipLastUsedAt(t *testing.T) {
	db := testDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()
	tx := testTx(t, db)
	f := createFixture(t, tx)

	_, err := tx.Exec(
		`UPDATE memberships SET last_used_at = now() - interval '1 hour'
		WHERE user_id = $1 AND account_id = $2`,
		f.user.ID, f.account.ID,
	)
	if err != nil {
		t.Fatal(err)
	}

	var before domain.Membership
	err = tx.QueryRow(
		`SELECT last_used_at FROM memberships WHERE user_id = $1 AND account_id = $2`,
		f.user.ID, f.account.ID,
	).Scan(&before.LastUsedAt)
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.UpdateMembershipLastUsedAt(ctx, tx, f.user.ID, f.account.ID); err != nil {
		t.Fatal(err)
	}

	var after domain.Membership
	err = tx.QueryRow(
		`SELECT last_used_at FROM memberships WHERE user_id = $1 AND account_id = $2`,
		f.user.ID, f.account.ID,
	).Scan(&after.LastUsedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !after.LastUsedAt.After(before.LastUsedAt) {
		t.Errorf("last_used_at should be updated: before=%v, after=%v", before.LastUsedAt, after.LastUsedAt)
	}
}
