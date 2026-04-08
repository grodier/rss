package auth

import (
	"time"

	"github.com/google/uuid"
	"github.com/grodier/rss/internal/domain"
)

type EmailIdentity struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	Provider     string
	Identifier   string
	PasswordHash *string
	VerifiedAt   *time.Time
	IsPrimary    bool
	CreatedAt    time.Time
	DeletedAt    *time.Time
	PurgeAfter   *time.Time
}

type AuthToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	IdentityID *uuid.UUID
	Type       string
	TokenHash  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	UsedAt     *time.Time
}

type Session struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	AccountID      uuid.UUID
	TokenHash      string
	CreatedAt      time.Time
	LastActivityAt time.Time
	ExpiresAt      time.Time
	IPAddress      *string
	UserAgent      *string
	LastStepUpAt   *time.Time
	RevokedAt      *time.Time
}

type RegisterParams struct {
	Email       string
	Password    string
	DisplayName *string
	IPAddress   *string
	UserAgent   *string
}

type RegisterResult struct {
	User     domain.User
	Account  domain.Account
	Session  Session
	RawToken string
}

type LoginParams struct {
	Email     string
	Password  string
	IPAddress *string
	UserAgent *string
}

type LoginResult struct {
	User     domain.User
	Account  domain.Account
	Session  Session
	RawToken string
}

type ConfirmPasswordResetParams struct {
	Token       string
	NewPassword string
}

type ChangePasswordParams struct {
	UserID          uuid.UUID
	CurrentPassword string
	NewPassword     string
}

type CreateRegistrationParams struct {
	UserID       uuid.UUID
	DisplayName  *string
	AccountID    uuid.UUID
	AccountName  *string
	IdentityID   uuid.UUID
	Email        string
	PasswordHash string
	SessionID    uuid.UUID
	TokenHash    string
	ExpiresAt    time.Time
	IPAddress    *string
	UserAgent    *string
}

type CreateRegistrationResult struct {
	User    domain.User
	Account domain.Account
	Session Session
}
