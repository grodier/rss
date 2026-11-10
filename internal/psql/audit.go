package psql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grodier/rss/internal/rss"
)

// AuditRepository keeps the admin's audit log (admin_audit rows), which the
// app only ever adds to. A repository method that makes an admin's change
// takes its rss.AuditActor and records the change itself (recordAudit), in
// its own transaction, so the change is never made without its record and
// is recorded the same way whoever calls it; Record is for what changes
// nothing (logins).
type AuditRepository struct {
	DB *sql.DB
}

func NewAuditRepository(db *sql.DB) *AuditRepository {
	return &AuditRepository{DB: db}
}

// execer runs a statement: *sql.DB, or *sql.Tx in an action's transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Record adds e to the audit log on its own.
func (r *AuditRepository) Record(ctx context.Context, e rss.AuditEntry) error {
	return recordAudit(ctx, r.DB, e)
}

// recordAudit adds e to the audit log on db. An empty AdminEmail is filled
// from AdminID's admin.
func recordAudit(ctx context.Context, db execer, e rss.AuditEntry) error {
	before, err := auditJSON(e.Before)
	if err != nil {
		return err
	}
	after, err := auditJSON(e.After)
	if err != nil {
		return err
	}
	stmt := `INSERT INTO admin_audit (admin_id, admin_email, via, action, target_type, target_id, before, after, ip)
		VALUES (nullif($1, '')::uuid,
			coalesce(nullif($2, ''), (SELECT email FROM admins WHERE id = nullif($1, '')::uuid), ''),
			$3, $4, $5, $6, $7, $8, $9)`
	_, err = db.ExecContext(ctx, stmt, e.AdminID, e.AdminEmail, e.Via, e.Action, e.TargetType, e.TargetID, before, after, e.IP)
	if err != nil {
		return fmt.Errorf("record %s in the audit log: %w", e.Action, err)
	}
	return nil
}

// auditJSON encodes fields for a JSONB column: nil for none.
func auditJSON(fields map[string]any) ([]byte, error) {
	if fields == nil {
		return nil, nil
	}
	return json.Marshal(fields)
}

// List returns at most limit of the audit log's entries that match f,
// newest first.
func (r *AuditRepository) List(ctx context.Context, f rss.AuditFilter, limit int) ([]rss.AuditEvent, error) {
	var where []string
	args := []any{limit}
	add := func(cond string, arg any) {
		args = append(args, arg)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Action != "" {
		add("action = $%d", f.Action)
	}
	if f.TargetType != "" {
		add("target_type = $%d", f.TargetType)
	}
	if f.TargetID != "" {
		add("target_id = $%d", f.TargetID)
	}
	if f.BeforeID > 0 {
		add("(occurred_at, id) < (SELECT occurred_at, id FROM admin_audit WHERE id = $%d)", f.BeforeID)
	}
	stmt := `SELECT id, occurred_at, coalesce(admin_id::text, ''), admin_email, via, action,
			target_type, target_id, before, after, ip
		FROM admin_audit`
	if len(where) > 0 {
		stmt += ` WHERE ` + strings.Join(where, " AND ")
	}
	stmt += ` ORDER BY occurred_at DESC, id DESC LIMIT $1`

	rows, err := r.DB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []rss.AuditEvent
	for rows.Next() {
		var e rss.AuditEvent
		var before, after []byte
		err := rows.Scan(&e.ID, &e.OccurredAt, &e.AdminID, &e.AdminEmail, &e.Via, &e.Action,
			&e.TargetType, &e.TargetID, &before, &after, &e.IP)
		if err != nil {
			return nil, err
		}
		if e.Before, err = auditFields(before); err != nil {
			return nil, err
		}
		if e.After, err = auditFields(after); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// auditFields decodes a before or after column: nil for NULL.
func auditFields(b []byte) (map[string]any, error) {
	if b == nil {
		return nil, nil
	}
	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, fmt.Errorf("decode audit fields: %w", err)
	}
	return fields, nil
}
