-- +goose Up
-- Every admin action and login (internal/admin audit.go, psql audit.go).
-- Rows are never updated or deleted by the app.
CREATE TABLE admin_audit (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    admin_id UUID REFERENCES admins(id) ON DELETE SET NULL, -- NULL for a failed password or a CLI command
    admin_email citext NOT NULL DEFAULT '', -- kept if the admin is deleted; the submitted email for a failed password
    via TEXT NOT NULL CHECK (via IN ('web', 'cli')),
    action TEXT NOT NULL, -- e.g. 'login', 'login_failed', 'invite.create', 'user.delete'
    target_type TEXT NOT NULL DEFAULT '', -- e.g. 'user', 'feed', 'invite'; '' for none
    target_id TEXT NOT NULL DEFAULT '',
    before JSONB, -- the fields the action changed, as they were; NULL for none
    after JSONB,  -- and as they became
    ip TEXT NOT NULL DEFAULT '' -- the admin's client IP (web only)
);

CREATE INDEX admin_audit_occurred_at_idx ON admin_audit (occurred_at DESC);
CREATE INDEX admin_audit_target_idx ON admin_audit (target_type, target_id, occurred_at DESC);

-- +goose Down
DROP TABLE admin_audit;
