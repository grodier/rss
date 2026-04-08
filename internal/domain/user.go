package domain

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID          uuid.UUID
	DisplayName *string
	CreatedAt   time.Time
	DeletedAt   *time.Time
	PurgeAfter  *time.Time
}
