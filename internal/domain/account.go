package domain

import (
	"time"

	"github.com/google/uuid"
)

type Account struct {
	ID         uuid.UUID
	Name       *string
	CreatedAt  time.Time
	DeletedAt  *time.Time
	PurgeAfter *time.Time
}
