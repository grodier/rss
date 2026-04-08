package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

type Membership struct {
	UserID     uuid.UUID
	AccountID  uuid.UUID
	Role       string
	CreatedAt  time.Time
	LastUsedAt time.Time
}
