package psql

import (
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"

	"github.com/grodier/rss/internal/rss"
)

// MaxServerErrorMessage is the most bytes of a server error's message that
// Record keeps.
const MaxServerErrorMessage = 2000

// ServerErrorRepository stores www's most recent server errors
// (server_errors).
type ServerErrorRepository struct {
	DB *sql.DB
}

func NewServerErrorRepository(db *sql.DB) *ServerErrorRepository {
	return &ServerErrorRepository{DB: db}
}

// Record stores e, with its message cut to MaxServerErrorMessage bytes. A
// zero OccurredAt is the database's now().
func (r *ServerErrorRepository) Record(ctx context.Context, e rss.ServerError) error {
	var occurredAt any
	if !e.OccurredAt.IsZero() {
		occurredAt = e.OccurredAt
	}
	_, err := r.DB.ExecContext(ctx, `INSERT INTO server_errors (occurred_at, instance, route, method, message, panic)
		VALUES (coalesce($1, now()), $2, $3, $4, $5, $6)`,
		occurredAt, e.Instance, e.Route, e.Method, cutMessage(e.Message), e.Panic)
	return err
}

// Trim deletes every server error but the newest keep.
func (r *ServerErrorRepository) Trim(ctx context.Context, keep int) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM server_errors
		WHERE id NOT IN (SELECT id FROM server_errors ORDER BY occurred_at DESC, id DESC LIMIT $1)`, keep)
	return err
}

// cutMessage makes msg valid UTF-8, which Postgres requires of text, and
// cuts it to MaxServerErrorMessage bytes without splitting a character.
func cutMessage(msg string) string {
	msg = strings.ToValidUTF8(msg, "�")
	if len(msg) <= MaxServerErrorMessage {
		return msg
	}
	end := MaxServerErrorMessage
	for end > 0 && !utf8.RuneStart(msg[end]) {
		end--
	}
	return msg[:end]
}
