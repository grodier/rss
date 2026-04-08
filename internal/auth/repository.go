package auth

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
	"github.com/grodier/rss/internal/domain"
)

// DBTX allows repository methods to work with both *sql.DB and *sql.Tx.
type DBTX interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Repository defines the persistence boundary for auth operations.
// User, account, and membership queries live here because all mutations currently
// happen through auth flows. If non-auth management surfaces are added later,
// consider extracting those into a separate repository.
type Repository interface {
	// CreateRegistration is transactional — the implementation manages its own transaction.
	CreateRegistration(ctx context.Context, params CreateRegistrationParams) (CreateRegistrationResult, error)

	// Users
	GetUserByID(ctx context.Context, db DBTX, id uuid.UUID) (domain.User, error)
	UpdateUserDisplayName(ctx context.Context, db DBTX, id uuid.UUID, displayName *string) (domain.User, error)
	SoftDeleteUser(ctx context.Context, db DBTX, id uuid.UUID) error

	// Accounts
	SoftDeleteAccount(ctx context.Context, db DBTX, id uuid.UUID) error

	// Memberships
	ListMembershipsByUserID(ctx context.Context, db DBTX, userID uuid.UUID) ([]domain.Membership, error)
	GetPrimaryMembership(ctx context.Context, db DBTX, userID uuid.UUID) (domain.Membership, error)
	UpdateMembershipLastUsedAt(ctx context.Context, db DBTX, userID uuid.UUID, accountID uuid.UUID) error

	// Email Identities
	GetEmailIdentityByEmail(ctx context.Context, db DBTX, email string) (EmailIdentity, error)
	GetEmailIdentityByID(ctx context.Context, db DBTX, id uuid.UUID) (EmailIdentity, error)
	ListEmailIdentitiesByUserID(ctx context.Context, db DBTX, userID uuid.UUID) ([]EmailIdentity, error)
	CreateEmailIdentity(ctx context.Context, db DBTX, identity EmailIdentity) (EmailIdentity, error)
	SetEmailIdentityVerified(ctx context.Context, db DBTX, id uuid.UUID) error
	SetPrimaryEmail(ctx context.Context, db DBTX, userID uuid.UUID, identityID uuid.UUID) error
	SoftDeleteEmailIdentity(ctx context.Context, db DBTX, id uuid.UUID) error
	CountActiveEmailIdentities(ctx context.Context, db DBTX, userID uuid.UUID) (int, error)
	UpdatePasswordHash(ctx context.Context, db DBTX, identityID uuid.UUID, hash string) error

	// Auth Tokens
	CreateAuthToken(ctx context.Context, db DBTX, token AuthToken) (AuthToken, error)
	ConsumeAuthToken(ctx context.Context, db DBTX, tokenHash string, tokenType string) (AuthToken, error)

	// Sessions
	CreateSession(ctx context.Context, db DBTX, session Session) (Session, error)
	GetSessionByTokenHash(ctx context.Context, db DBTX, tokenHash string) (Session, error)
	UpdateSessionActivity(ctx context.Context, db DBTX, id uuid.UUID) error
	UpdateSessionStepUp(ctx context.Context, db DBTX, id uuid.UUID) error
	UpdateSessionAccount(ctx context.Context, db DBTX, id uuid.UUID, accountID uuid.UUID) error
	RevokeSession(ctx context.Context, db DBTX, id uuid.UUID) error
	RevokeAllUserSessions(ctx context.Context, db DBTX, userID uuid.UUID) error
}
